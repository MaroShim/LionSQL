# LionSQL Pattern 1: S3 Pre-signed URL Direct Upload Architecture & Implementation Plan

이 문서는 대용량 바이너리 스트리밍 및 이미지 썸네일 처리를 데이터베이스 커널 내부에서 배제(Spec-Out, `GEMINI.md` 8.7절)하고, 클라이언트가 외부 오브젝트 스토리지(AWS S3, MinIO, Cloudflare R2 등)로 직접 업로드한 후 그 링크를 관계형 테이블 컬럼에 안전하게 보관하는 **패턴 1(Client Direct Pre-signed URL Upload)**의 기술 설계 및 단계별 작업 계획서입니다.

---

## 1. 아키텍처 배경 및 설계 원칙

### 1.1. In-Kernel 바이너리 스트리밍 배제 배경 (GEMINI.md 8.7절)
- **OLTP 성능 격리 (Golden Rule 15: Radical KISS & YAGNI)**: 대용량 파일 청크(수십 MB~GB) 업로드 스트리밍과 무거운 래스터 이미지 리사이징 연산이 DB 커널 프로세스 내부에서 동작하면 Go 런타임 힙 메모리 할당 폭증, GC 지연, CPU 코어 독점으로 인해 초당 18,500+ TPS의 트랜잭션 p99 지연 시간이 붕괴됩니다.
- **플랫폼 프리미티브 우선 (Golden Rule 14)**: 대용량 오브젝트 분산 저장 및 엣지 미디어 리사이징은 전용 오브젝트 스토리지(AWS S3, MinIO, Cloudflare R2) 및 엣지 CDN(Cloudflare Images)의 고유 영역입니다.
- **데이터베이스의 역할 정의**: LionSQL은 파일의 메타데이터(파일명, MIME 타입, 파일 크기, 업로드 일시)와 접근 링크(`VARCHAR(512)` S3 URI)를 관계형 테이블에 원자적으로 보관하고, 바이너리 페이로드 I/O는 1바이트도 데이터베이스 프로세스를 통과시키지 않습니다.

### 1.2. 패턴 1(Pre-signed URL 직접 업로드)의 핵심 가치
1. **DB 프로세스 부하 제로**: 데이터베이스 네트워크 인터페이스와 힙 메모리를 전혀 소모하지 않고 수천 명의 동시 업로드를 수용합니다.
2. **글로벌 클라우드 표준 DX**: AWS S3, Cloudflare R2, MinIO, Google Cloud Storage, Wasabi 등 모든 S3 API 호환 스토리지와 무수정 호환됩니다.
3. **외부 무의존성 정적 바이너리 유지 (Golden Rule 36)**: 무거운 외부 AWS SDK 대신 Go 표준 라이브러리(`crypto/hmac`, `crypto/sha256`)만으로 순수 SigV4 서명 엔진을 구현하여 CGo나 외부 종속성을 0건으로 유지합니다.

---

## 2. 전체 아키텍처 및 데이터 흐름

```
+----------------------------------------------------------------------------------------------------+
|                                    Pattern 1 Data Flow Architecture                                 |
+----------------------------------------------------------------------------------------------------+

     [ 1. Presign 요청 ]              [ 2. Pre-signed PUT URL 발급 ]
 클라이언트 ────────────────────────► LionSQL (HTTP :8090 또는 T-SQL)
    │                                              │ (순수 Go SigV4 서명, 0 AWS SDK)
    │                                              ▼
    │                              [ upload_url & public_url 반환 ]
    │ ◄────────────────────────────────────────────┘
    │
    │ [ 3. 바이너리 직접 업로드 (HTTP PUT) ]
    └──────────────────────────────────────────────────────────────┐
                                                                   ▼
                                                       [ 외부 S3 / MinIO / R2 ]
                                                                   │
                                                                   │ (업로드 성공: 200 OK)
    ┌──────────────────────────────────────────────────────────────┘
    │
    ▼ [ 4. 레코드 생성 / 컬럼 갱신 (SQL INSERT / UPDATE) ]
 LionSQL Database Kernel
    │  - INSERT INTO products (id, name, image_url) VALUES (1, '...', 'https://s3.../item.png');
    │
    ▼ [ 5. (선택사항) CDC 스트림 이벤트 방출 ]
 LionSQL CDC / Outbox Streamer
    │
    ▼
 비동기 이미지 워커 (Lambda / Sidecar) ──► 썸네일 생성 ──► S3 업로드 ──► UPDATE products.thumb_url
```

---

## 3. 핵심 설계 컴포넌트

### 3.1. 제로 의존성 경량 AWS SigV4 서명 모듈 (`pkg/storage/s3sign`)
무거운 AWS 공식 SDK( 수십 메가바이트 크기)를 배제하고, Go 표준 암호화 라이브러리만으로 AWS Signature Version 4 규격을 완벽하게 충족하는 단일 패키지를 구축합니다.

- **위치**: `pkg/storage/s3sign`
- **주요 기능**:
  - `SignPresignedPUT(bucket, key string, contentType string, expiresIn time.Duration) (string, error)`
  - `SignPresignedGET(bucket, key string, expiresIn time.Duration) (string, error)`
- **동작 원리**:
  1. ISO 8601 타임스탬프(`YYYYMMDD'T'HHMMSS'Z'`) 및 날짜 스탬프(`YYYYMMDD`) 산출.
  2. Canonical Request 구성: HTTP 메서드(PUT/GET), URI, Canonical Query String (`X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=...&X-Amz-Date=...&X-Amz-Expires=...&X-Amz-SignedHeaders=host`).
  3. String to Sign 생성: `AWS4-HMAC-SHA256` + 타임스탬프 + Credential Scope + SHA-256(Canonical Request).
  4. 파생 서명 키 계산: HMAC-SHA256 다단계 체이닝:
     $$K_{date} = \text{HMAC}(AWS4 + Secret, Date)$$
     $$K_{region} = \text{HMAC}(K_{date}, Region)$$
     $$K_{service} = \text{HMAC}(K_{region}, "s3")$$
     $$K_{signing} = \text{HMAC}(K_{service}, "aws4\_request")$$
  5. 최종 서명 산출: $\text{Hex}(\text{HMAC}(K_{signing}, \text{StringToSign}))$.
  6. 완전한 쿼리 파라미터가 결합된 URL 반환.

### 3.2. 스토리지 설정 서브시스템 (`pkg/config`)
다양한 오브젝트 스토리지 공급자(AWS S3, Cloudflare R2, 로컬 MinIO)를 수용할 수 있는 통합 설정 구조입니다.

```toml
# config.toml
[storage.s3]
enabled           = true
endpoint          = "https://s3.ap-northeast-2.amazonaws.com" # MinIO: "http://minio:9000", R2: "https://<account>.r2.cloudflarestorage.com"
region            = "ap-northeast-2"
access_key_id     = "${AWS_ACCESS_KEY_ID}"
secret_access_key = "${AWS_SECRET_ACCESS_KEY}"
default_bucket    = "sqlion-assets"
force_path_style  = false                                     # MinIO/로컬 개발 환경의 경우 true
cdn_base_url      = "https://cdn.example.com"                 # (선택) Cloudflare / CloudFront CDN 퍼블릭 베이스 URL
```

### 3.3. HTTP REST API 엔드포인트 (`pkg/api/handlers_storage.go`)
웹 프론트엔드 및 관리자 스튜디오가 직접 호출할 수 있는 Pre-signed URL 발급 API입니다.

- **엔드포인트**: `POST /api/storage/presign`
- **인증**: JWT Bearer 토큰 또는 API-Key (`Authorization: Bearer <token>`)
- **요청 본문 (JSON)**:
  ```json
  {
    "bucket": "sqlion-assets",
    "key": "avatars/user_1001.png",
    "method": "PUT",
    "expires_in": 600,
    "content_type": "image/png"
  }
  ```
- **응답 본문 (JSON)**:
  ```json
  {
    "upload_url": "https://sqlion-assets.s3.ap-northeast-2.amazonaws.com/avatars/user_1001.png?X-Amz-Algorithm=AWS4-HMAC-SHA256&...",
    "public_url": "https://cdn.example.com/avatars/user_1001.png",
    "key": "avatars/user_1001.png",
    "expires_at": "2026-10-17T01:00:00Z"
  }
  ```

### 3.4. T-SQL 내장 스칼라 함수 확장 (`pkg/proc/builtins_s3.go`)
Stored Procedure 및 대화형 SQL 쿼리에서 직접 Pre-signed URL을 생성할 수 있는 함수입니다.

```sql
-- 1. 업로드용 PUT Pre-signed URL 발급 (기본 600초 유효)
SELECT S3_PRESIGNED_PUT_URL('avatars/user_1001.png') AS upload_url;

-- 2. 버킷명 및 만료 시간 명시적 지정
SELECT S3_PRESIGNED_PUT_URL('my-bucket', 'docs/contract_99.pdf', 300) AS upload_url;

-- 3. 프라이빗 버킷 다운로드용 GET Pre-signed URL 발급
SELECT S3_PRESIGNED_GET_URL('invoices/inv_202610.pdf', 180) AS download_url;
```

### 3.5. 고아 객체 정리 파이프라인 (CDC Event-Driven Lifecycle)
데이터베이스 레코드가 삭제되었을 때 S3에 남겨진 실제 파일을 안전하게 제거하기 위해, LionSQL의 내장 CDC 프레임워크를 활용합니다.

1. **레코드 삭제 시**:
   ```sql
   DELETE FROM users WHERE id = 1001;
   ```
2. **LionSQL CDC 이벤트 방출**:
   삭제된 행의 `avatar_url` 정보를 포함하는 `ChangeEvent (kind: DELETE)`가 내장 스트리머(`pkg/cdc`)를 통해 방출됩니다.
3. **비동기 정리 핸들러**:
   S3 삭제 권한을 가진 가벼운 아웃박스 릴레이 워커가 해당 S3 Key를 파싱하여 백그라운드에서 S3 `DeleteObject`를 호출합니다.

---

## 4. 단계별 구현 작업 계획 (Implementation Phases)

### Phase 1: 경량 SigV4 서명기 및 단위 테스트 구축 (`pkg/storage/s3sign`)
1. `Signer` 구조체 정의 및 SHA-256 / HMAC 유틸리티 작성.
2. Canonical Request 및 StringToSign 생성 로직 구현.
3. AWS 공식 테스트 스위트(Canonical Test Suite) 기반 단위 테스트 작성:
   - AWS S3, Cloudflare R2, MinIO 호환성 테스트.
   - 특수 문자, 슬래시(`/`), 쿼리 파라미터 인코딩 무결성 검증.

### Phase 2: 설정 및 HTTP REST API 계층 연동 (`pkg/config`, `pkg/api`)
1. `pkg/config/config.go`에 `S3Config` 구조체 및 환경 변수 바인딩 추가.
2. `pkg/api/handlers_storage.go` 신설:
   - `POST /api/storage/presign` 핸들러 구현.
   - Content-Length 상한 및 허용된 MIME 타입(이미지, PDF, 문서 등) 유효성 검사.
3. `pkg/api/server.go`에 라우트 등록 및 RBAC 권한 검사 바인딩.
4. HTTP API 통합 테스트 작성 (`handlers_storage_test.go`).

### Phase 3: T-SQL 내장 함수 등록 (`pkg/proc`, `pkg/engine`)
1. `pkg/proc/builtins_s3.go` 신설:
   - `S3_PRESIGNED_PUT_URL`, `S3_PRESIGNED_GET_URL` 내장 함수 등록.
2. VDBE 런타임 및 SQL 인터프리터 연동.
3. 저장 프로시저(Stored Procedure) 내 호출 및 파라미터 전달 E2E 테스트 작성.

### Phase 4: 웹 관리자 스튜디오(Web Studio) UI 연동 (`pkg/ui`)
1. 웹 스튜디오 레코드 상세 편집기 모달에 "파일 업로드" UI 필드 타입(`file / image_url`) 추가.
2. 브라우저에서 파일 선택 시:
   - `/api/storage/presign`을 호출하여 1회용 업로드 링크 획득.
   - S3로 파일 직접 전송 (Progress Bar 표시).
   - 반환된 `public_url`을 해당 컬럼 입력창에 자동 기입.

---

## 5. 엔지니어링 가이드라인 준수 매핑

| 가이드라인 / 규칙 | 적용 방식 |
| :--- | :--- |
| **Golden Rule 14 (Platform Primitives First)** | S3 호환 스토리지를 DB 내부에서 재발명하지 않고 클라우드/OS 플랫폼 프리미티브를 직접 활용. |
| **Golden Rule 15 (Radical KISS & DRY)** | 무거운 외부 AWS SDK 및 S3 서버 데몬을 완전히 배제하고 150줄의 순수 Go 표준 암호화 라이브러리로 서명기 완성. |
| **Golden Rule 23 (Bounded Buffers)** | Pre-signed URL 발급 시 Content-Length 상한 헤더(`x-amz-content-sha256`)를 강제하여 클라이언트가 허가된 크기 이상의 파일을 업로드하지 못하도록 차단. |
| **Golden Rule 29 (Non-blocking)** | 고아 파일 정리는 DML 쓰기 트랜잭션을 블로킹하지 않고 비동기 CDC 이벤트 파이프라인으로 안전 분리. |
| **Golden Rule 36 (Pure Static Binaries)** | CGo 의존성 0건, 외부 네트워크 SDK 0건으로 정적 단일 바이너리 배포 원칙을 100% 사수. |
