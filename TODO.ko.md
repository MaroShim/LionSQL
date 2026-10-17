# LionSQL Engineering TODO & Backlog

이 문서는 LionSQL의 실무 엔지니어링 실행 과제, 도메인별 백로그 매트릭스, 최근 완료된 변경 이력을 관리하는 공식 엔지니어링 작업 대시보드입니다.

> [!NOTE]
> 중장기 아키텍처 로드맵 및 버전별 마일스톤 릴리스 계획은 [ROADMAP.md](ROADMAP.md)를 참조하세요.
> 분류 체계 및 식별 코드 규칙의 전체 명세는 [notes/TAXONOMY.md](notes/TAXONOMY.md)에 상세히 기술되어 있습니다.

---

## 1. 식별 코드 체계 범례 (Taxonomy Legend)

| 접두사 / 코드 | 트랙 명칭 (도메인) | 번호 범위 | 설명 |
| :---: | :--- | :---: | :--- |
| **`P1` ~ `P3`** | **우선순위 (Priority)** | `P1` ~ `P3` | 개발 착수 긴급도 (P1: 활성 스프린트/즉시 착수, P2: 차기 착수, P3: 생태계 검증) |
| **`P-1` ~ `P-17`** | **엔진 성능 (Performance)** | `P-1` ~ `P-17` | 스토리지, 디코딩, 와이어, 캐시, 정규식, JSON, 메모리 풀링 17대 성능 트랙 |
| **`R-1` ~ `R-5`** | **코드 리팩토링 (Refactoring)** | `R-1` ~ `R-5` | 거대 파일 모듈 분해, 단일 책임 원칙(SRP), DRY 중복 해소 및 가상 뷰 레지스트리화 |
| **`A` ~ `D`** | **CBO 최적화 (Optimizer)** | `A-1` ~ `D-9` | 비용 기반 옵티마이저 4대 기능 트랙 (A: 통계 수집, B: 조인 알고리즘, C: 선택도, D: 운영 제어) |
| **`S-1` ~ `S-7`** | **SQL 문법 (Syntax)** | `S-1` ~ `S-7` | T-SQL 및 ANSI SQL 7대 문법 호환성 완비 트랙 (전 항목 완료) |
| **`S3-1` ~ `S3-4`** | **오브젝트 스토리지 (Storage)** | `S3-1` ~ `S3-4` | 클라우드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 연동 트랙 |
| **`OBS-1` ~ `OBS-3`** | **관제 및 프로파일링 (Observability)** | `OBS-1` ~ `OBS-3` | Prometheus 메트릭, EXPLAIN ANALYZE, 슬로우 쿼리 웹훅 연동 트랙 |
| **`SEC-1` ~ `SEC-2`** | **엔터프라이즈 보안 (Security)** | `SEC-1` ~ `SEC-2` | TDS Pre-Login TLS 암호화 협상 및 프로토콜 보안 강화 트랙 |

---

## 2. 현재 활성 스프린트 과제 (Active Sprint Queue)

| 우선순위 | 영역 | 핵심 과제 | 대상 위치 | 상태 | 관련 기술 문서 |
| :---: | :---: | :--- | :--- | :---: | :--- |
| **P1** | **리소스/안정성** | **글로벌 메모리 예산 상한제 및 회로 차단기 (Global Memory Governor & Circuit Breaker)** | `pkg/config`, `pkg/execution`, `cmd/server` | **완료 (`[x]`)** | [`doc/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **P1** | **보안/감사** | **불변 엔터프라이즈 감사 로그 (Enterprise Immutable Audit Log)** | `pkg/audit`, `pkg/api`, `cmd/lionsql` | **차주 P1 착수 예정** | [`doc/IMMUTABLE_AUDIT_LOG.md`](doc/IMMUTABLE_AUDIT_LOG.md) |
| **P2** | **스토리지/가드** | **In-Memory OCC 엔진 데이터 용량 한도 가드 (`MEM-3`)** | `pkg/storage/memengine`, `pkg/config` | **차기 스프린트 대기** | [`doc/ZERO_ALLOCATION_ARCHITECTURE.md`](doc/ZERO_ALLOCATION_ARCHITECTURE.md) |
| **P2** | **스토리지/클라우드** | **클라우드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 (`S3-1` ~ `S3-4`)** | `pkg/storage/s3sign`, `pkg/config`, `pkg/api`, `pkg/proc` | **스프린트 백로그 대기** | [`doc/S3_PRESIGNED_STORAGE_PLAN.md`](doc/S3_PRESIGNED_STORAGE_PLAN.md) |
| **P2** | **운영/관제** | **Prometheus / OpenTelemetry 표준 관제 메트릭 엔드포인트 (`OBS-1`)** | `pkg/api`, `pkg/engine` | **스프린트 백로그 대기** | - |
| **P2** | **최적화/DX** | **`EXPLAIN ANALYZE` 런타임 실행 프로파일러 (`OBS-2`)** | `pkg/planner`, `pkg/execution`, `pkg/engine` | **스프린트 백로그 대기** | [`doc/INTERNALS_CBO_AND_STATISTICS.md`](doc/INTERNALS_CBO_AND_STATISTICS.md) |
| **P3** | **알림/운영** | **슬로우 쿼리 실시간 웹훅 알림 (Slack / Discord / Webhook) (`OBS-3`)** | `pkg/engine`, `pkg/config` | **백로그 등록 (Post-1.0 후보)** | - |
| **P3** | **프로토콜/보안** | **TDS 8.0 / Pre-Login SSL/TLS 암호화 핸드셰이크 보강 (`SEC-1`)** | `pkg/wire/tds` | **백로그 등록 (Post-1.0 후보)** | - |

### P1 과제 세부 명세 및 수락 기준 (Acceptance Criteria)

#### 1. 글로벌 메모리 예산 상한제 및 회로 차단기 (`pkg/config`, `pkg/execution`, `cmd/server`)
* **과제 개요**: 무제한 메모리 할당으로 인한 OS OOM Killer 사살 방지(Golden Rule 1, 23)를 위한 프로세스/세션 레벨 방어 체계 구축.
* **핵심 아키텍처 및 구현 요구사항**:
  1. **`--max-memory` 글로벌 CLI 플래그 및 환경 설정 도입**:
     - `--max-memory` (예: `2GB`, `4GB`) 플래그 및 `resources.max_memory` 설정 연동.
     - 미설정 시 기본값: **호스트 시스템 물리 메모리(RAM)의 70%** 자동 계산 적용 (cgroups/sysctl/OS 메모리 자동 감지).
  2. **Go 런타임 플랫폼 프리미티브 연동 (`debug.SetMemoryLimit`)**:
     - Go 1.19+ `debug.SetMemoryLimit`을 호출하여 힙 메모리가 상한에 근접할 시 Go 런타임 GC가 사전에 능동적으로 메모리를 회수하도록 보장 (Golden Rule 14).
     - 서버 기동 배너에 실시간 적용 메모리 예산 로깅 (`[Memory Governor] max_memory: X.XGB (70% of RAM), work_mem: 64MB`).
  3. **대량 조인(`HashJoinIterator`) Fail-Fast 회로 차단기 (Circuit Breaker)**:
     - 복잡한 디스크 스필러 오버엔지니어링(YAGNI, Golden Rule 15)을 배제하고, Build 단계에서 `buildMemory > workMem` 도달 시 즉시 안전한 중단 및 에러 반환 (`hash join memory budget exceeded: ... > work_mem`).
     - 비정상적인 카테시안 조인 또는 초대용량 해시 조인으로부터 다른 정상 OLTP 쿼리들을 완벽히 격리 보호.

#### 2. 엔터프라이즈 암호학적 불변 감사 로그 서브시스템 구축 (`pkg/audit`)
* **표준 규격**: PCI-DSS v4.0 Requirement 10, SOC 2 Type II, HIPAA §164.312(b)
* **핵심 아키텍처 및 구현 요구사항**:
  1. **SHA-256 해시 체이닝 (Cryptographic Hash Chaining)**:
     - 각 감사 레코드는 이전 레코드의 해시값을 포함하여 연결 ($H_n = \text{SHA256}(H_{n-1} \parallel R_n)$).
     - 로그 파일 임의 변조, 행 삭제, 재정렬 시 오프라인 검증기에서 $100\%$ 즉각 탐지.
  2. **비차단 MPSC 링 버퍼 (Non-Blocking Ring Buffer, Golden Rule 29)**:
     - 트랜잭션 커밋 핫패스에서 디스크 I/O를 직접 수행하지 않고 인메모리 링 버퍼로 원자적 전달.
     - 전용 백그라운드 플러셔 goroutine이 배치 단위 fsync 기록하여 OLTP 트랜잭션 지연 영향 $0\text{ms}$ 보장.
  3. **감사 대상 이벤트 태깅**:
     - 인증 이벤트: 로그인 성공/실패, 토큰 발급/폐기, 패스워드 변경.
     - 권한 제어: `CREATE LOGIN/USER`, `GRANT`, `REVOKE`, `DENY`.
     - 스키마 변경: `CREATE/ALTER/DROP TABLE`, `INDEX`, `PROCEDURE`, `VIEW`.
     - 데이터 조작(선택적): 슈퍼유저 DML, 테이블 벌크 반출입.
  4. **독립 CLI 무결성 검증기**:
     - `lionsql audit verify -file=<audit.log>` 명령어를 통해 외부 감사인이 오프라인에서 암호학적 무결성 검증 가능.

#### 3. 클라우드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 (`pkg/storage/s3sign`, `pkg/config`, `pkg/api`, `pkg/proc`)
* **과제 개요**: 대용량 바이너리 스트리밍 및 이미지 썸네일 처리를 DB 커널에서 배제(Spec-Out, `GEMINI.md` 8.7절)하고, 클라이언트가 외부 S3/R2/MinIO로 직접 업로드하도록 지원하는 제로-의존성 순수 Go SigV4 서명 서브시스템 구축.
* **사전 구현 세부 요구사항 (Phase 1 ~ Phase 4)**:
  1. **제로-의존성 경량 AWS SigV4 서명기 (`pkg/storage/s3sign`)**:
     - 외부 AWS SDK(수십 MB) 없이 Go 표준 `crypto/hmac`, `crypto/sha256`만으로 Canonical Request, StringToSign, 다단계 파생 서명 키 계산.
     - `SignPresignedPUT(bucket, key, contentType, expiresIn)` 및 `SignPresignedGET(bucket, key, expiresIn)` 구현.
     - AWS 공식 Canonical Test Suite 기반 100% 호환성 단위 테스트 검증.
  2. **통합 스토리지 설정 서브시스템 (`pkg/config`)**:
     - `S3Config` (endpoint, region, access_key_id, secret_access_key, default_bucket, force_path_style, cdn_base_url) 구조체 및 환경 변수 바인딩 (`SQLION_S3_*`).
  3. **HTTP REST Pre-signed 발급 API 엔드포인트 (`pkg/api/handlers_storage.go`)**:
     - `POST /api/storage/presign` 엔드포인트 (JWT/API-Key RBAC 인증).
     - 업로드용 `upload_url`, 조회용 `public_url`, 유효기간 반환 및 Content-Length/MIME 타입 화이트리스트 가드.
  4. **T-SQL 내장 스칼라 함수 확장 (`pkg/proc/builtins_s3.go`, `pkg/engine`)**:
     - `S3_PRESIGNED_PUT_URL('avatars/user.png')` 및 `S3_PRESIGNED_GET_URL(...)` 스칼라 함수 등록.
     - Stored Procedure 및 대화형 쿼리에서 직접 업로드 링크 생성 지원.

---

## 3. 도메인별 통합 백로그 매트릭스 (Domain Backlog Matrices)

### 3.1. CBO & 쿼리 최적화 트랙 (`A` ~ `D`)

| 번호 | 과제명 | 구분 | 상태 | 구현 내용 요약 및 전략적 가치 |
| :---: | :--- | :---: | :---: | :--- |
| **A-1** | `ANALYZE TABLE` 멀티코어 병렬 통계 수집 | 통계 수집 | **완료 (`[x]`)** | `sync.WaitGroup` 기반 멀티코어 병렬화로 통계 수집 4~8배 가속 |
| **A-2** | CBO 통계 메타데이터 디스크 영속화 | 통계 수집 | **완료 (`[x]`)** | `cbo_stats.json` 원자적 파일 쓰기 및 기동 시 자동 복원 연동 |
| **B-3** | 소트-머지 조인 (Sort-Merge Join) 연산자 | 조인 엔진 | **완료 (`[x]`)** | 사전 정렬된 관계에 대해 $O(M+N)$ 단일 패스 해시리스 스트리밍 조인 |
| **B-4** | $N > 4$ 체인 조인 동적 계획법 (Selinger DP) | 조인 최적화 | **완료 (`[x]`)** | Left-Deep DP 비트마스크 메모이제이션으로 $O(N!)$ 순열 폭증 방지 |
| **C-5** | 다중 컬럼 확장 통계 (Extended Stats) | 선택도 모델 | **완료 (`[x]`)** | 다중 컬럼 결합 NDV 및 함수적 종속성($dep$) 모델링으로 과소평가 제거 |
| **C-6** | OR 조건 및 불리언 트리 선택도 계산 | 선택도 모델 | **완료 (`[x]`)** | AST 재귀 평가, 배타합/독립합집합, NOT, IN, BETWEEN 정밀 산출 |
| **C-7** | MCV (Most Common Values) 최빈값 목록 결합 | 선택도 모델 | **완료 (`[x]`)** | Top-20 최빈값 분리 보관 및 비-MCV 롱테일 히스토그램 이원화 |
| **D-8** | 백그라운드 자동 통계 갱신 데몬 | 운영 제어 | **완료 (`[x]`)** | DML 변동량(Dirty Tuples) 임계치 도달 시 비동기 `ANALYZE` 자동 트리거 |
| **D-9** | CBO 옵티마이저 힌트 오버라이드 제어 | 운영 제어 | **완료 (`[x]`)** | `OPTION (HASH/LOOP/MERGE JOIN)`, `WITH (INDEX(...))` 계획 강제 반영 |

---

### 3.2. 엔진 성능 및 무할당 풀링 트랙 (`P-1` ~ `P-17`)

| 번호 | 전략 분야 | 핵심 구현 과제 | 상태 | 구현 성과 및 최적화 효과 |
| :---: | :--- | :--- | :---: | :--- |
| **P-1** | 스토리지 | 배치형 그룹 커밋 & 무경합 Fast-Path | **완료 (`[x]`)** | `FsyncAlways` 지연 100배 단축(0.038ms/op), TPC-C 2.8k TPS 기여 |
| **P-2** | 디코딩 | 바이너리 튜플 지연 역직렬화 & 커버링 인덱스 | **완료 (`[x]`)** | `SkipTupleColumn`, $O(1)$ `LazyTuple`, Index-Only Scan 100% 힙 바이패스 |
| **P-3** | 와이어 | 제로카피 와이어 인코딩 & I/O 버퍼 결합 | **완료 (`[x]`)** | `WriteNBCRowDirect` 직결, 중간 `[]any` 레코드 박싱 제거 |
| **P-4** | 실행 엔진 | 벡터화 청크 반복자 (Chunked Batch Next) | **완료 (`[x]`)** | 1,024건 튜플 배치 스트리밍으로 가상 함수 디스패치 오버헤드 99% 제거 |
| **P-5** | 캐시 | 고속 SQL 지문 해싱 & 단순 PK 조회 바이패스 | **완료 (`[x]`)** | FNV-1a 지문 해싱으로 CBO/VDBE 우회, 단순 PK 룩업 TPS 50%+ 향상 |
| **P-6** | 파서/엔진 | 핫패스 정규식 사전 컴파일 및 캐싱 | **완료 (`[x]`)** | 40여 개 정규식 패키지 싱글톤 승격으로 쿼리당 5~15µs 힙 할당 제거 |
| **P-7** | JSON | 반정형 JSON 동일 튜플 DOM 캐시 & 키 스캐너 | **완료 (`[x]`)** | `ScanJSONTopLevelKey` 무할당 스캐너 및 1,024건 DOM FIFO 캐시 |
| **P-8** | 스토리지 | WiredTiger 커서 풀링 & `cur->reset` 재사용 | **완료 (`[x]`)** | 세션별 커서 풀링 및 C 헬퍼 단일화로 894ns 룩업(1.13M+ OPS) 달성 |
| **P-9** | 옵티마이저 | 정형 SQL Parameterized 실행 계획 캐시 | **보류 (`[-]`)** | SP/Prepared/PK 바이패스로 고주파 경로가 기충족되어 보류 |
| **P-10** | 실행기 | Volcano 대규모 분석 쿼리 멀티코어 병렬 실행기 | **보류 (`[-]`)** | OLTP 동시성 간섭 방지를 위해 1.0 보류 (v1.2+ 이관) |
| **P-11** | 스토리지 | In-Memory OCC 트랜잭션 객체 & 맵 풀링 | **완료 (`[x]`)** | `tx.workspaces` 맵 `clear()` 재사용으로 트랜잭션 시작 할당 0건화 (20k TPS) |
| **P-12** | 실행기 | Volcano 벡터화 배치 청크(`Chunk`) 풀링 | **완료 (`[x]`)** | `sync.Pool` 기반 1,024건 청크 재활용으로 대량 스캔 GC 부하 극소화 |
| **P-13** | 실행기 | DML Record(Map) 제거 ➔ Row(Slice) 직결 | **보류 (`[-]`)** | 외래키 연쇄 및 제약조건 엔진 회귀 위험으로 공식 보류 |
| **P-14** | 와이어 | Prepared Statement 바이너리 바인드 직접 디코딩 | **완료 (`[x]`)** | 중간 문자열 변환 제거 및 64개 정적 파라미터명 슬라이스 풀링 |
| **P-15** | 스토리지 | WiredTiger 다중 행 배치 CGo 삽입 | **보류 (`[-]`)** | CGo 포인터 세그폴트 및 트랜잭션 롤백 복잡성으로 보류 |
| **P-16** | 스토리지 | In-Memory OCC Row-Level Atomic CAS 커밋 | **보류 (`[-]`)** | 동시성 엣지 케이스 및 데이터 사일런트 손상 위험으로 보류 |
| **P-17** | VM | VDBE 컬럼 산술 융합 슈퍼 명령어 (`OpIncrColInt`) | **보류 (`[-]`)** | 바이트코드 점프 타깃(PC) 오프셋 불일치 및 VM 복잡성으로 보류 |

---

### 3.3. 코드베이스 아키텍처 리팩토링 트랙 (`R-1` ~ `R-5`)

| 번호 | 영역 | 핵심 리팩토링 과제 | 상태 | 목표 및 엔지니어링 성과 |
| :---: | :--- | :--- | :---: | :--- |
| **R-1** | 카탈로그 | 가상 시스템 카탈로그 모듈 분리 및 레지스트리화 | **완료 (`[x]`)** | 3,284줄 모놀리식 파일을 PG/MSSQL/ANSI 4개 도메인으로 분해, $O(1)$ 라우팅 |
| **R-2** | 파서 | Pratt 파서 및 내장 함수 도메인별 관심사 분리 | **완료 (`[x]`)** | `parser.go`(3.2k줄 ➔ 5개 파일), `builtins.go`(2.8k줄 ➔ 6개 파일) 모듈화 |
| **R-3** | 플래너 | 단일 테이블 패스트패스 vs Volcano 바운드 일원화 | **완료 (`[x]`)** | `pkg/planner/bounds.go` 공통 모듈 신설로 260줄 중복 제거 (DRY) |
| **R-4** | 스토리지 | In-Memory OCC 트랜잭션 라이프사이클 모듈 분해 | **Post-1.0 (`[-]`)** | 1.0 안정성 최우선. 동시성 검증 완료 상태 보존을 위해 v1.1+ 이관 |
| **R-5** | 스토리지 | WiredTiger CGo 바인딩 격리 및 트랜잭션 분해 | **Post-1.0 (`[-]`)** | 1.0 안정성 최우선. CGo 빌드 태그 복잡도 방지를 위해 v1.1+ 이관 |

---

### 3.4. 클라우드 오브젝트 스토리지 & Pre-signed URL 연동 트랙 (`S3-1` ~ `S3-4`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **S3-1** | 스토리지 | 제로-의존성 경량 AWS SigV4 서명기 구축 | **대기 (`[ ]`)** | 순수 Go 표준 라이브러리(`crypto/hmac`, `crypto/sha256`) 기반 Canonical Request & 다단계 파생 키 서명 (`pkg/storage/s3sign`) |
| **S3-2** | 설정/API | S3 스토리지 설정 및 HTTP REST Pre-signed API | **대기 (`[ ]`)** | `POST /api/storage/presign` 엔드포인트, MIME/크기 가드, `S3Config` 환경 변수 연동 (`pkg/config`, `pkg/api`) |
| **S3-3** | 파서/엔진 | T-SQL Pre-signed URL 내장 스칼라 함수 등록 | **대기 (`[ ]`)** | `S3_PRESIGNED_PUT_URL`, `S3_PRESIGNED_GET_URL` 내장 함수 및 SP 연동 (`pkg/proc`, `pkg/engine`) |
| **S3-4** | UI/스튜디오 | 웹 관리자 스튜디오(Web Studio) 파일 업로드 위젯 | **대기 (`[ ]`)** | 레코드 편집 모달 내 브라우저 ➔ S3 직접 업로드 및 URL 자동 입력 위젯 연동 (`pkg/ui`) |

---

### 3.5. 엔터프라이즈 관제 및 쿼리 프로파일링 트랙 (`OBS-1` ~ `OBS-3`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **OBS-1** | 관제/메트릭 | Prometheus / OpenTelemetry 표준 관제 메트릭 엔드포인트 | **대기 (`[ ]`)** | `/metrics` 텍스트 규격, 실시간 TPS/세션/메모리/복제 랙/스토리지 I/O 메트릭 노출 (`pkg/api`, `pkg/engine`) |
| **OBS-2** | 최적화/DX | `EXPLAIN ANALYZE` 런타임 실행 프로파일러 | **대기 (`[ ]`)** | Volcano Iterator 래핑, 실제 소요 시간(Actual Time), 처리 행(Actual Rows), 피크 메모리 트리 시각화 (`pkg/planner`, `pkg/execution`) |
| **OBS-3** | 운영/알림 | 슬로우 쿼리 실시간 웹훅 알림 (Slack / Discord / Webhook) | **대기 (`[ ]`)** | 임계치 초과 쿼리 실시간 비동기 웹훅 디스패처, 슬랙/디스코드 포맷터 연동 (`pkg/engine`, `pkg/config`) |

---

### 3.6. 프로토콜 보안 및 스토리지 가드 트랙 (`SEC-1`, `MEM-3`)

| 번호 | 영역 | 핵심 구현 과제 | 상태 | 구현 목표 및 기술 명세 |
| :---: | :--- | :--- | :---: | :--- |
| **SEC-1** | 와이어/보안 | TDS 8.0 / Pre-Login SSL/TLS 암호화 핸드셰이크 보강 | **대기 (`[ ]`)** | MS-TDS 2.2.6.4 Pre-Login 토큰 내 암호화 협상 플래그 교환 및 엄격 암호화 핸드셰이크 완비 (`pkg/wire/tds`) |
| **MEM-3** | 스토리지/가드 | In-Memory OCC 엔진 데이터 용량 한도 가드 (`memengine`) | **대기 (`[ ]`)** | 테이블/작업공간 데이터 크기 임계치 초과 시 `ErrTableFull` / `ErrOutOfMemory` 페일세이프 방어 (`pkg/storage/memengine`) |

---

## 4. 공식 보류 및 스펙-아웃 사유 명세 (Deferred & Spec-Out Rationale)

1.0 릴리스 안정성 확보 및 극단적 KISS/YAGNI 원칙(Golden Rule 14, 15)에 따라 보류 및 배제된 항목들의 기술적 결정 사유입니다:

### 4.1. 성능 및 실행 엔진 보류 과제
* **DML `storage.Record` 제거 ➔ `storage.Row` 직결 (`P-13`)**:
  `memengine` 및 WiredTiger 스토리지 계층의 복잡한 외래키 연쇄(CASCADE), 유니크 인덱스 충돌 검사, 복합 제약조건 검증 로직이 `storage.Record(map[string]Value)` 구조와 강하게 결합되어 있음. 이를 슬라이스 직결로 무리하게 전면 개편할 경우 엣지 케이스에서의 ACID 무결성 훼손 및 롤백 불안정 위험이 있어 보류.
* **WiredTiger 다중 행 배치 CGo 삽입 (`P-15`)**:
  CGo 프리앰블 수준의 일괄 삽입은 C 포인터 메모리 관리 오류, 세그폴트(SIGSEGV), 해저드 포인터 레이스 등 시스템 크래시 위험을 수반함. 이미 단일 노드 16,814 TPS의 높은 처리량을 안정적으로 달성 중이므로 크래시 방지(Rule 1, 36)를 위해 보류.
* **In-Memory OCC Row-Level Atomic CAS 커밋 검증 (`P-16`)**:
  롤백 시 버전 복원 경쟁, 툼스톤 동시성 처리 등 동시성 엣지 케이스와 데이터 사일런트 손상(Silent Corruption) 위험이 큼. 이미 TPC-C 20,087 TPS 환경에서 직렬화 어보트율이 0.10%로 극히 안정적이므로 시스템 무결성을 최우선으로 보류.
* **VDBE 컬럼 산술 융합 슈퍼 명령어 (`P-17`)**:
  바이트코드 점프 타깃(PC) 오프셋 불일치, 타입 불일치 등 가상 머신 실행 무결성 위협 위험이 큼. `sync.Pool` 레지스터 재사용과 서브마이크로초 디스패치로 20k TPS를 안정적으로 달성 중이므로 KISS 원칙에 따라 보류.

### 4.2. 구조 리팩토링 및 플랫폼 보류 과제
* **In-Memory OCC 트랜잭션 라이프사이클 모듈 분해 (`R-4`) & WiredTiger CGo 분해 (`R-5`)**:
  현재 25개 전 패키지 레이스 검사 100% PASS(0 Data Race)로 동시성 안정성이 완벽히 입증됨. 1.0 출시 전 단순 파일 분리는 런타임 성능 이득이 없고 오히려 회귀 위험을 유발하므로 v1.1+로 공식 이관.
* **투명 데이터 암호화 (In-Kernel TDE)**:
  인-커널 소프트웨어 암호화는 클라우드 볼륨(AWS EBS, GCP PD) 및 OS 블록 디바이스(LUKS2)와 중복 암호화되어 NVMe 처리량을 최대 35% 저하시킴. 하드웨어 가속(AES-NI) 기반 스토리지 파일시스템 암호화 표준 가이드([`doc/STORAGE_ENCRYPTION_GUIDE.md`](doc/STORAGE_ENCRYPTION_GUIDE.md)) 수립으로 대체 충족.
* **Pure-Go 영속 스토리지 엔진 (Pebble / bbolt)**:
  1.0 프로덕션에서는 검증된 WiredTiger B+Tree 엔진과 순수 Go 초고속 In-Memory OCC 엔진 2대 백엔드 체제에 집중(v1.1+ 이관).

### 4.3. 영구 스펙-아웃 (Spec-Out Invariants)
* **내장형 OAuth2 소셜 로그인 배제**:
  데이터베이스 커널 내부에서 구글/깃허브 웹 리다이렉트 및 아웃바운드 인터넷 토큰 교환을 처리하는 것은 단일 책임 원칙 및 KISS(Rule 15)를 위반하고 공격 표면을 확대함. 엔터프라이즈 리버스 프록시(Keycloak, OAuth2-Proxy) 위임으로 영구 표준화 (`GEMINI.md` 8.6절, ADR-021).
* **내장형 S3 Blob 파일 스토리지 및 썸네일 리사이징 배제**:
  대용량 바이너리 스트리밍 및 이미지 리사이징으로 인한 OLTP CPU/메모리 기아를 방지하고, S3/MinIO 직접 업로드(Pre-signed URL) 위임으로 영구 표준화 (`GEMINI.md` 8.7절, `doc/S3_PRESIGNED_STORAGE_PLAN.md`).

---

## 5. 최근 완료 마일스톤 상세 이력 (Recent Completed History)

### 5.1. 최근 고성능 최적화 및 안정화 과제
- [x] **글로벌 메모리 예산 상한제 및 대량 조인 회로 차단기 (`pkg/config`, `pkg/execution`, `cmd/server`)**:
  - 호스트/컨테이너 물리 메모리 자동 감지(Darwin `sysctl`, Linux cgroups v1/v2 및 `/proc/meminfo`) 기반 시스템 RAM의 70%를 기본 글로벌 상한으로 자동 계산 및 적용.
  - Go 런타임 플랫폼 프리미티브 연동 (`debug.SetMemoryLimit` / GOMEMLIMIT)으로 힙 메모리 상한 근접 시 능동적 사전 GC 트리거 및 OS OOM Killer 사살 방지.
  - 대량 해시 조인(`HashJoinIterator`) 빌드 단계에서 `buildMemory > workMem` 초과 시 Fail-Fast 회로 차단기(`hash join memory budget exceeded`) 가동으로 비정상 쿼리 즉시 차단 및 타 세션 OLTP 격리 보호.
- [x] **테스트 스위트 중복 보일러플레이트 대규모 슬림화 (Net -291줄) (`pkg/wire`, `pkg/api`)**:
  - `pkg/wire/server_test.go`: `authenticatePGClient` 헬퍼 도입 및 동시성/커서 테스트 슬림화 (-176줄).
  - `pkg/api/sqlexec_test.go`: 31개 단위 테스트에 걸친 중복 인메모리 서버 초기화를 `setupMemServer(t)`로 단일화 (-115줄).
  - 전체 패키지 무결성 유지 및 291줄 순수 코드 삭제 달성.
- [x] **가상 카탈로그 쿼리 인터셉터 0-Alloc 프리필터 & 공백 무관 등호 매칭 리팩토링 (`pkg/catalog/virtual`)**:
  - `containsFold` (ASCII 대소문자 무관 무할당 탐색) 및 `hasCatalogCandidate` 도입: 일반 OLTP 쿼리 103.5ns, 0 B/op 탈출.
  - `containsEq`: 키와 값 사이 등호(`=`) 주변의 임의 공백 순열(`= `, ` =`, ` = `, `   =   `, 탭)을 완벽 수용하는 강건한 매처 구현.
- [x] **ODBC / JDBC 엔터프라이즈 드라이버 적합성 인증 (`pkg/wire`)**:
  - PostgreSQL 공식 JDBC(`pgjdbc`) 및 `psqlODBC` 드라이버 초기화 파라미터(`extra_float_digits`, `application_name`) 및 카탈로그 뷰 E2E 적합성 검증 완비.
- [x] **Prepared Statement 바이너리 바인드 파라미터 직접 디코딩 (`P-14`) (`pkg/wire`, `pkg/wire/tds`)**:
  - OID 기반 `storage.Value` 직접 디코딩으로 중간 문자열 변환 제거, 64개 정적 파라미터명 슬라이스 풀링으로 `fmt.Sprintf` 힙 할당 100% 제거.
- [x] **TDS 단일 프레임 버퍼 풀링 & 네트워크 시스템 콜 50% 삭감 (`pkg/wire/tds`)**:
  - `sync.Pool` 기반 4KB 고정 프레임 풀 구축, 8바이트 헤더와 페이로드를 단일 연속 프레임에 패킹하여 패킷당 1회의 `w.Write()` 시스템 콜 원자적 전송.
- [x] **Volcano 벡터화 배치 청크(`Chunk`) 풀링 (`P-12`) (`pkg/execution`)**:
  - 1,024건 단위 튜플 배치 스트리밍 버퍼를 `sync.Pool`로 재활용하여 대규모 테이블 스캔 및 조인 시 메모리 누수 방지 및 GC 부하 극소화.
- [x] **In-Memory OCC 트랜잭션 객체 & 워크스페이스 맵 풀링 (`P-11`) (`pkg/storage/memengine`)**:
  - 트랜잭션 종료 시 Go 1.21+ `clear(m)`으로 해시 버킷 용량을 유지한 채 $O(1)$ 초기화 및 풀링, TPC-C Scale 10에서 20,087 TPS 공식 돌파.
- [x] **반정형 JSON 동일 튜플 DOM 캐시 & 제로-할당 키 스캐너 (`P-7`) (`pkg/storage`, `pkg/engine`)**:
  - `ScanJSONTopLevelKey` 제로-할당 스캐너 및 1,024건 FIFO DOM 캐시 결합으로 다중 JSON 필드 추출 쿼리 CPU/GC 부하 50~70% 경감.
- [x] **가상 카탈로그 멀티테이블 조인 인터셉터 격리 리팩토링 (`pkg/catalog/virtual`, `pkg/engine`)**:
  - DBeaver/Beekeeper/DbGate 메타데이터 크로스 조인 쿼리 가로채기 레이어를 독립 패키지 `pkg/catalog/virtual`로 분리, 1,300여 라인 중복 코드 제거.
- [x] **하드코딩 Go 시딩 제거 및 5개 테이블 기반 독립 데모 스크립트(`scripts/demo_seed.sql`) & `--seed=false` 기본값 전환**:
  - `customers`, `accounts`, `products`, `sales`, `sales_details` 5개 표준 테이블과 `TransferFunds`, `ProcessOrder` 프로시저를 독립 SQL 파일로 분리.
- [x] **PGWire, TDS, HTTP 전반의 통합 관리자 인증 및 AuthProvider SPI 추상화 (`pkg/auth`)**:
  - 단일 슈퍼유저 비밀번호로 3개 와이어 프로토콜 통합 검증, `AuthProvider` SPI 인터페이스 신설로 외부 디렉터리 확장성 확보.
- [x] **SQL 파서 & 엔진 핫패스 정규식 사전 컴파일 리팩토링 (`P-6`) (`pkg/engine`)**:
  - 40여 개 동적 `regexp.MustCompile`을 패키지 싱글톤 및 `rePatternCache`로 승격하여 쿼리당 5~15µs 지연 및 힙 할당 100% 제거.
- [x] **WiredTiger 커서 풀링 & CGo 왕복 비용 절감 (`P-8`) (`pkg/storage/wiredtiger`)**:
  - 세션별 `cursorPool` 보관 및 `C.go_wt_cursor_reset` 재사용으로 894ns 룩업(1.13M+ OPS) 달성.
- [x] **단일 테이블 패스트패스 vs Volcano 인덱스 바운드 로직 일원화 (`R-3`) (`pkg/planner`)**:
  - `pkg/planner/bounds.go` 공통 모듈 신설로 260줄 이상의 중복 바운드 파싱 로직 제거.
- [x] **Pratt 파서 및 내장 함수 도메인별 관심사 분리 (`R-2`) (`pkg/proc`)**:
  - 비대했던 `parser.go`와 `builtins.go`를 11개 도메인 모듈 파일로 깔끔하게 분해.
- [x] **가상 시스템 카탈로그 모듈 분리 및 가상 뷰 레지스트리화 (`R-1`) (`pkg/engine`)**:
  - 거대한 switch 분기를 15줄 수준의 $O(1)$ 레지스트리 맵 디스패치(`VirtualCatalogView`)로 슬림화.

### 5.2. 코어 아키텍처 및 기능 마일스톤 이력
- [x] **반정형 JSON 표현식 B-Tree 인덱스 & 내장함수 완비 (`ADR-022`)**:
  - `->`, `->>`, `ISJSON`, `JSON_VALUE`, `JSON_QUERY` 지원, `((data->>'key'))` 기능 인덱스 DDL 파싱, CBO 인덱스 스캔 6.5~7.3배 가속.
- [x] **T-SQL `STRING_SPLIT` 테이블 반환 함수 & 서브쿼리/CROSS APPLY 완비 (`pkg/engine`)**:
  - `STRING_SPLIT` 파싱, 세션 변수 연동, `WHERE id IN (SELECT value FROM STRING_SPLIT(...))` 서브쿼리 해결 및 `CROSS APPLY` 연동.
- [x] **Transactional Outbox 브로커 디스패처 (Kafka/RabbitMQ/Webhook) (`pkg/cdc`)**:
  - CDC 커밋 스트림 기반 아웃박스 릴레이, 무손실 체크포인트(`outbox_checkpoint.json`), 지수 백오프 재시도 및 플러그형 싱크 완비.
- [x] **PostgreSQL 서버 사이드 커서 및 Extended Query 포털 배치 스트리밍 (`pkg/wire`, `pkg/engine`)**:
  - `DECLARE CURSOR`, `FETCH`, `MOVE`, `CLOSE` 커서 라이프사이클 및 Extended Query `Execute` `maxRows` 분할 스트리밍 지원.
- [x] **관용적 No-Op `USE <database>` 및 세션 가상 DB 컨텍스트 동기화 (`pkg/engine`, `pkg/wire/tds`)**:
  - `USE` 명령 무오류 즉시 수용, `ClientSession.Database` 동기화, `DB_NAME()` 반환, TDS `TokenEnvChange (0xE3)` 송출.
- [x] **Always On 스타일 자동 장애 조치(Auto-Failover) 및 리더 선출 (`pkg/replication`)**:
  - 단조 증가 텀, LSN 우선권 리더 선출, 스플릿 브레인 방지 펜싱, 하트비트 타임아웃 감지 및 자동 승격 완비.
- [x] **선언적 스키마 마이그레이션 CLI (`lionsql migrate`) (`cmd/lionsql`, `pkg/migrate`)**:
  - `_migrations` 메타데이터 추적, `new`, `up`, `down`, `status` 라이프사이클, 임베디드 직접 쿼리 실행기 `lionsql sql`.
- [x] **MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합 (`C-7`) (`pkg/planner`)**:
  - Top-20 최빈값 분리 보관 및 비-MCV 롱테일 히스토그램 이원화로 왜곡 데이터 선택도 오차 최소화.
- [x] **다중 컬럼 확장 통계 및 함수적 종속성 모델링 (`C-5`) (`pkg/planner`)**:
  - 결합 NDV 수집, 종속성 계수($dep$) 산출, `CREATE/DROP STATISTICS` DDL 지원.
- [x] **$N > 4$ 체인 조인 동적 계획법 (Selinger DP) (`B-4`) (`pkg/planner`)**:
  - 비트마스크 메모이제이션 기반 Selinger DP, 데카르트 곱 가지치기, 6개/8개 테이블 체인 서브밀리초 플래닝.
- [x] **스토리지 ➔ 와이어 제로카피 직접 직렬화 (`P-3`) (`pkg/wire`, `pkg/wire/tds`)**:
  - `WriteNBCRowDirect` 직결, 네트워크 직렬화 버퍼 직접 기록으로 중간 힙 할당 제거.
- [x] **TDS PLP (Partially Length-Prefixed) LOB 청크 스트리밍 (`pkg/wire/tds`)**:
  - [MS-TDS] 2.2.5.2 PLP 규격 구현, 8KB 초과 `VARCHAR(MAX)` 청크 분할 전송 완비.
- [x] **볼케이노 벡터화 청크 반복자 (Chunked Batch Next: 1,024건) (`P-4`) (`pkg/execution`)**:
  - 1,024건 프리알로케이션 청크 기반 볼케이노 배치 파이프라인 관통 및 기존 단건 `Next()` 투명 버퍼링 100% 하위 호환성 보장.
- [x] **바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 커버링 인덱스 (`P-2`) (`pkg/storage`)**:
  - `SkipTupleColumn`, $O(1)$ `LazyTuple`, `CREATE INDEX ... INCLUDE(...)` 커버링 인덱스로 메인 테이블 힙 조회 100% 생략.
