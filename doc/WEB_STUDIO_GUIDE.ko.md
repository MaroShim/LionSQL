# LionSQL Web Studio 사용 설명서 (User Guide)

LionSQL Web Studio는 별도의 외부 웹 서버(Nginx, Apache)나 런타임(Node.js, Python) 없이, LionSQL 단일 정적 바이너리(`embed.FS`)에 내장된 올인원 브라우저 기반 데이터베이스 관리 및 개발 도구입니다.

로컬 개발 환경부터 클라우드 프로덕션 환경까지, 브라우저 접속만으로 스키마 설계, 데이터 CRUD, T-SQL 쿼리 실행, 실행 계획(EXPLAIN) 분석, REST API 코드 생성, 실시간 쿼리 감사 로그 조회, 무중단 핫 백업을 완벽하게 수행할 수 있습니다.

---

## 목차 (Table of Contents)

1. [접속 및 기본 인터페이스](#1-접속-및-기본-인터페이스)
2. [개요 대시보드 및 실시간 모니터링](#2-개요-대시보드-및-실시간-모니터링)
3. [테이블 및 스키마 관리](#3-테이블-및-스키마-관리)
4. [데이터 그리드 조회 및 편집 (CRUD)](#4-데이터-그리드-조회-및-편집-crud)
5. [데이터 가져오기 및 내보내기 (Import / Export)](#5-데이터-가져오기-및-내보내기-import--export)
6. [SQL 쿼리 콘솔 및 실행 계획 분석](#6-sql-쿼리-콘솔-및-실행-계획-분석)
7. [개발자 API 프리뷰 (Developer API Preview)](#7-개발자-api-프리뷰-developer-api-preview)
8. [실시간 쿼리 및 감사 로그 뷰어 (Query Logs)](#8-실시간-쿼리-및-감사-로그-뷰어-query-logs)
9. [데이터베이스 백업 및 복원 (Hot Backup & Restore)](#9-데이터베이스-백업-및-복원-hot-backup--restore)
10. [보안 및 성능 최적화](#10-보안-및-성능-최적화)

---

## 1. 접속 및 기본 인터페이스

### 1.1. 접속 주소 및 자동 리다이렉트
LionSQL 서버가 기동되면 HTTP 관리 포트(기본값: TCP `8090`)에서 Web Studio가 즉시 서비스됩니다.

- **기본 접속 URL**: `http://127.0.0.1:8090/_/`
- **루트 리다이렉트**: 브라우저에서 `http://127.0.0.1:8090/`으로 접근 시 자동으로 관리 스튜디오 URL인 `/_/`로 안전하게 302 리다이렉트됩니다.

### 1.2. 관리자 인증 및 비밀번호 가시성 토글
보안이 활성화된 환경에서는 관리자 계정 로그인이 필요합니다.
- **기본 관리자 이메일**: `admin@lionsql.local`
- **기본 비밀번호**: `1234567890` (서버 기동 옵션 또는 환경 변수로 변경 가능)
- **비밀번호 가시성 토글 (Eye Toggle)**: 로그인 화면 및 설정 모달의 비밀번호 입력란 우측에 눈 모양 아이콘 버튼이 제공되어, 마우스 클릭 한 번으로 마스킹 해제(`text`)와 마스킹(`password`)을 손쉽게 전환할 수 있습니다.

### 1.3. 다크 모드 / 라이트 모드 테마 전환
상단 네비게이션 바 우측의 테마 전환 버튼을 클릭하여 다크 모드(Dark Mode)와 라이트 모드(Light Mode)를 전환할 수 있습니다.
- **시스템 선호도 자동 반영**: 사용자의 OS 또는 브라우저 테마 설정을 자동으로 감지하여 초기 테마를 구성합니다.
- **고대비 가독성 보장**: 라이트 모드에서도 기본키(PK) 뱃지, 경고 알림창, 비활성 탭 글자색, 삭제 확인창 텍스트가 명확하게 식별되도록 CSS 변수 기반 고대비 컬러 팔레트가 적용되어 있습니다.

### 1.4. 좌측 사이드 패널 네비게이션 체계 (4개 카테고리)
좌측 사이드 패널은 총 12개의 핵심 도구를 작업 흐름에 맞추어 4개의 카테고리로 체계화하여 제공합니다:
- **DATABASE & QUERY**:
  - `Overview`: 서버 리소스, 가동 시간, 초당 처리량(TPS/QPS) 모니터링
  - `Tables & Records`: 테이블 스키마 설계 및 데이터 CRUD 스프레드시트 그리드
  - `SQL Query`: 다중 쿼리 실행, T-SQL 배치, 바이트코드 EXPLAIN 실행 계획 분석 콘솔
  - `Stored Procedures`: T-SQL 저장 프로시저 및 스칼라 함수(UDF) 소스 조회 및 즉시 실행
- **AUTOMATION & STREAM**:
  - `Realtime Live Stream`: 웹소켓/SSE 기반 실시간 테이블 변경(CDC) 이벤트 스트리밍 모니터
  - `Web Hooks`: INSERT, UPDATE, DELETE 변경 시 외부 엔드포인트를 호출하는 웹훅 관리
  - `Cron Jobs`: 정기적 SQL 실행, 데이터 집계, 백업을 위한 내장 분초 단위 크론 스케줄러
- **DEVELOPER & ACCESS**:
  - `Auth & API Keys`: 사용자 계정 발급, RBAC 역할(Admin, Read-Only) 부여 및 API Key 관리
  - `Client SDK`: JavaScript/TypeScript 클라이언트 라이브러리 연동 및 언어별 REST API 코드 스니펫
- **MONITORING & SYSTEM**:
  - `Request Logs`: 실시간 HTTP 요청/응답 처리 시간(Latency), 상태 코드, Remote IP 감사 로그 뷰어
  - `Replication (HA)`: Primary/Replica 복제 토폴로지, LSN 동기화 지연 및 Failover 상태 점검
  - `Settings / Mail`: 내장 SMTP 발송 엔진 설정, 이메일 템플릿 테스트 및 시스템 환경 확인

---

## 2. 개요 대시보드 및 실시간 모니터링

메인 화면의 **Overview** 탭에서는 LionSQL 엔진의 실시간 운영 상태와 클라이언트 연결 정보를 한눈에 파악할 수 있습니다.

### 2.1. 핵심 운영 지표 (System Metrics)
- **Database Engine**: 현재 구동 중인 스토리지 백엔드 표시 (`In-Memory OCC` 또는 `WiredTiger B+Tree MVCC`).
- **Active Sessions**: 고루틴 연결 풀러를 통해 현재 활성화된 세션 수.
- **Tables**: 등록된 전체 사용자 테이블 및 뷰 개수.
- **Uptime**: 데이터베이스 엔진 무중단 연속 가동 시간.
- **TPS (Transactions Per Second)**: 실시간 트랜잭션 처리량.

### 2.2. 실시간 자동 갱신 (Real-Time Auto-Refresh)
상단 대시보드 영역에는 실시간 모니터링을 위한 제어 패널이 제공됩니다 (`UI-REALTIME-1`).
- **Live 인디케이터**: 활성화 상태 시 녹색 펄스 신호로 실시간 동기화 중임을 표시합니다.
- **Auto-Refresh 토글**: 자동 갱신 기능을 켜거나 끌 수 있습니다.
- **갱신 주기 선택기**: `5초`, `10초`, `30초`, `60초` 단위로 백그라운드 갱신 주기를 자유롭게 조정할 수 있습니다.

### 2.3. 클라이언트 연결 가이드 패널
DBeaver, Beekeeper Studio, DbGate, sqlcmd, psql 등 외부 클라이언트 연동에 필요한 주소와 포트 정보가 카드 형태로 제공되며, 복사 버튼을 통해 즉시 클립보드로 가져올 수 있습니다.
- **PostgreSQL Wire Protocol**: `localhost:5433` (기본 사용자: `admin`, 데이터베이스: `postgres`)
- **Microsoft SQL Server TDS**: `localhost:1433` (기본 사용자: `sa`, 데이터베이스: `master`)
- **HTTP REST & BaaS**: `localhost:8090` (Bearer Token 인증)

### 2.4. 샘플 데이터 시딩 (Seed Sample Database)
신규 인스턴스를 처음 테스트할 때 유용한 원클릭 샘플 데이터 생성 기능입니다.
- **빈 데이터베이스 안전 검증 (Empty DB Guard)**: 기존 사용자 데이터의 우발적 덮어쓰기를 방지하기 위해, 데이터베이스 내에 테이블이 존재하지 않는 깨끗한 상태에서만 시딩 버튼이 활성화됩니다.
- **시딩되는 테이블 구성**:
  - `customers`: 고객 정보 (ID, 이름, 이메일, 잔고)
  - `products`: 상품 카탈로그 (ID, 상품명, 가격, 재고)
  - `sales`: 주문 헤더 (주문 번호, 고객 ID, 주문 일시, 총액)
  - `accounts`: 결제 계좌 원장 (계좌 번호, 소유자, 통화, 잔고)

---

## 3. 테이블 및 스키마 관리

좌측 사이드바의 **Tables** 탭에서는 데이터베이스 내 모든 테이블의 스키마와 물리적 정의를 관리할 수 있습니다.

### 3.1. 테이블 및 컬럼 정의 조회
테이블을 선택하면 상단에 스키마 구조가 카드 형태로 렌더링됩니다.
- **Column Name**: 컬럼 식별자
- **Data Type**: 정규화된 SQL 데이터 타입 (`INT`, `BIGINT`, `VARCHAR`, `DECIMAL`, `DATETIME`, `BIT`, `UUID`, `JSON` 등)
- **Primary Key (PK)**: 기본키 여부 (명확한 가독성의 전용 뱃지 표시)
- **Nullable**: `NULL` 허용 여부
- **Default Value**: 컬럼 기본값 식별

### 3.2. DDL 뷰어 및 내보내기 (DDL Viewer & Export)
테이블 상세 뷰 우측 상단의 **`DDL`** 버튼을 클릭하면 DDL 뷰어 모달이 열립니다 (`UI-DDL-1`).
- **SQL DDL 탭**: LionSQL 엔진에서 테이블을 정확히 재현할 수 있는 `CREATE TABLE` 전체 DDL 스크립트를 제공합니다.
- **JSON Schema 탭**: 프론트엔드 연동 및 외부 시스템 전송을 위한 구조화된 JSON 스키마 명세를 제공합니다.
- **원클릭 복사**: `Copy SQL` 및 `Copy JSON` 버튼을 통해 스크립트를 즉시 복사할 수 있습니다.

### 3.3. 테이블 삭제 안전 확인 모달 (Safe Drop Table)
데이터 유실을 원천적으로 차단하기 위한 2단계 안전 장치가 적용되어 있습니다 (`UI-DROP-CONFIRM`).
- **테이블 이름 타이핑 검증**: 테이블 삭제(`DROP TABLE`) 버튼을 누르면 즉시 삭제되지 않고 안전 모달이 표시됩니다.
- **활성화 조건**: 모달 입력창에 삭제하려는 대상 테이블의 이름을 오타 없이 정확하게 입력해야만 빨간색 `Drop Table` 확인 버튼이 활성화됩니다.

---

## 4. 데이터 그리드 조회 및 편집 (CRUD)

선택한 테이블의 실제 레코드를 엑셀 형태의 고속 인터랙티브 그리드로 탐색하고 직접 편집할 수 있습니다.

### 4.1. 레코드 탐색 및 페이지네이션
- 대용량 테이블도 브라우저 멈춤 없이 렌더링되도록 페이지당 20개/50개 단위 페이징을 지원합니다.
- 검색 필터를 통해 특정 컬럼 값이나 문자열을 실시간으로 검색할 수 있습니다.

### 4.2. 레코드 추가 (Add Record)
- **`+ Add Row`** 버튼을 누르면 스키마 기반의 입력 폼 모달이 열립니다.
- 각 컬럼의 데이터 타입에 맞춰 유효성 검증(Validation)이 수행되며, 기본키가 `IDENTITY`인 경우 자동 증가값이 반영됩니다.

### 4.3. 인라인 레코드 편집 및 단일/일괄 삭제
- 각 행의 **Edit** 버튼을 클릭하여 필드 값을 즉시 수정하고 `PUT /api/records/:table/:id` 요청으로 저장할 수 있습니다.
- 개별 행 삭제(**Delete**) 및 체크박스를 통한 일괄 삭제를 안전하게 지원합니다.

---

## 5. 데이터 가져오기 및 내보내기 (Import / Export)

외부 파일로부터 데이터를 일괄 적재하거나, 현재 테이블의 데이터를 외부로 백업하기 위한 모달 인터페이스를 제공합니다 (`UI-DATA-1`).

### 5.1. 데이터 가져오기 (Import Modal)
테이블 뷰의 **`Import`** 버튼을 클릭하여 진입합니다.
1. **포맷 지원**: CSV 파일 및 JSON 배열 파일 지원.
2. **드래그 앤 드롭 파일 업로드**: 파일을 모달 창에 끌어다 놓거나 파일 탐색기로 선택합니다.
3. **사전 미리보기 (Data Preview)**: 파일 파싱 후 상위 5개 행의 미리보기 테이블이 렌더링되어 매핑 상태를 사전 점검할 수 있습니다.
4. **원자적 일괄 삽입 (Batch Insert)**: 수천 건의 레코드도 내부 트랜잭션 단위를 통해 고속으로 데이터베이스에 적재됩니다.

### 5.2. 데이터 내보내기 (Export Modal)
테이블 뷰의 **`Export`** 버튼을 클릭하여 원하는 파일 포맷을 선택합니다.
- **CSV Export**: 스프레드시트 호환 UTF-8 CSV 파일로 즉시 다운로드됩니다.
- **JSON Export**: REST API 표준 규격의 구조화된 JSON 데이터 배열 파일로 다운로드됩니다.

---

## 6. SQL 쿼리 콘솔 및 실행 계획 분석

상단 네비게이션의 **SQL Console** 탭을 통해 자유로운 T-SQL 쿼리를 작성하고 실행할 수 있습니다.

### 6.1. SQL 에디터 및 단축키
- 다중 라인 SQL 쿼리 입력창과 구문 하이라이팅을 제공합니다.
- **단축키**: `Ctrl + Enter` (Windows/Linux) 또는 `Cmd + Enter` (macOS)를 눌러 에디터 내에서 마우스 조작 없이 즉시 쿼리를 실행할 수 있습니다.
- `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `CREATE PROCEDURE`, `EXEC` 등 모든 T-SQL 문장을 지원합니다.

### 6.2. 실행 결과 및 실행 시간 측정
- 쿼리 실행 결과는 하단 그리드에 즉시 표출됩니다.
- 소요 시간(마이크로초/밀리초 단위) 및 처리된 행 수(Rows Affected)가 실시간으로 집계됩니다.

### 6.3. 실행 계획 시각화 (EXPLAIN / EXPLAIN ANALYZE)
- 쿼리 작성 후 **`Explain`** 버튼을 누르면 LionSQL CBO(Cost-Based Optimizer)와 Volcano 실행 엔진이 수립한 물리적 쿼리 플랜 트리를 시각적으로 확인할 수 있습니다.
- **분석 지표**:
  - `SeqScan` (풀 테이블 스캔) vs `IndexScan` (B+Tree 인덱스 스캔) 식별
  - `HashJoin` vs `IndexNestedLoopJoin`의 조인 방식 확인
  - `Sort`, `Aggregate`, `Filter` 연산자의 예상 비용(Cost) 및 실제 실행 시간 분석

---

## 7. 개발자 API 프리뷰 (Developer API Preview)

LionSQL은 테이블이 생성되는 즉시 해당 테이블 전용의 무설정 RESTful CRUD 엔드포인트를 노출합니다. Web Studio의 **Developer API Preview**는 개발자가 API 문서를 따로 찾아볼 필요 없이, 현재 테이블에 맞는 실제 동작 가능한 클라이언트 코드를 복사할 수 있도록 지원합니다.

### 7.1. 모달 진입
테이블 상세 화면 우측 상단의 **`</> API Preview`** 버튼을 클릭합니다.

### 7.2. 3대 개발 환경 코드 탭
모달 내부에서 원하는 도구 및 언어 탭을 선택하여 즉시 실행 가능한 코드를 확인할 수 있습니다:

1. **cURL 탭**:
   - 터미널이나 쉘 스크립트에서 즉시 실행 가능한 CLI 요청 명령어를 생성합니다.
   - `GET /api/records/:table`, `POST`, `PUT`, `DELETE` 요청별 헤더 및 JSON 바디 예제를 완벽하게 포함합니다.
2. **Fetch (JavaScript) 탭**:
   - 모던 웹 브라우저, Next.js, Node.js 환경에서 사용 가능한 네이티브 `fetch()` 비동기 코드를 생성합니다.
3. **LionSQL Web SDK 탭**:
   - 브라우저 정적 번들로 제공되는 경량 클라이언트 SDK(`lionsql.js`) 메서드 호출 코드를 제공합니다 (`db.records('customers').find()`, `create()`, `update()`, `delete()`).

### 7.3. 스키마 맞춤형 페이로드 자동 매핑
정적 템플릿이 아닌, 현재 선택된 테이블의 실제 컬럼명과 데이터 타입을 분석하여 유효한 기본값(정수형은 `1`, 문자열은 `"샘플텍스트"`, 불리언은 `true` 등)이 채워진 JSON 바디를 자동으로 완성해 줍니다.

---

## 8. 실시간 쿼리 및 감사 로그 뷰어 (Query Logs)

상단 네비게이션의 **Logs** 탭에서는 LionSQL 엔진으로 인입된 모든 API 요청 및 SQL 실행 내역을 실시간 감사 로그 형태로 조회할 수 있습니다 (`UI-LOGS-1`).

### 8.1. 실시간 쿼리 내역 추적
내부 인메모리 요청 로거(`/api/logs/requests`)와 연동되어 최근 실행된 쿼리와 API 호출 이력이 실시간으로 기록됩니다.
- **Timestamp**: 요청 시각 (YYYY-MM-DD HH:MM:SS)
- **Method / Operation**: HTTP 메서드(`GET`, `POST`, `PUT`, `DELETE`) 또는 SQL 연산 분류
- **Query / Path**: 실행된 구체적인 SQL 쿼리문 또는 REST 엔드포인트 경로
- **Duration**: 실행 소요 시간 (밀리초 단위, 느린 쿼리는 색상으로 강조)
- **Status**: 응답 상태 코드 (`200 OK`, `400 Bad Request`, `500 Internal Error` 등)
- **Remote IP**: 요청을 보낸 클라이언트 IP 주소

### 8.2. 로그 필터링 및 초기화
- **검색 필터**: 검색창에 테이블명, 특정 쿼리 키워드, IP 등을 입력하면 일치하는 로그만 실시간 필터링됩니다.
- **Clear Logs**: 뷰어 상단의 **`Clear`** 버튼을 눌러 누적된 로컬 뷰어 버퍼를 초기화할 수 있습니다.

---

## 9. 데이터베이스 백업 및 복원 (Hot Backup & Restore)

상단 네비게이션의 **Backup** 메뉴를 통해 운영 중단이나 서비스 잠금 없이 안전하게 데이터베이스 상태를 보존하고 복구할 수 있습니다.

### 9.1. 원클릭 핫 백업 (Hot Backup)
- **`Create Backup`** 버튼을 클릭하면 서버 내부에서 트랜잭션 일관성을 유지한 채 스냅샷을 생성합니다.
- 백업 파일은 tar.gz 압축 아카이브 형태로 브라우저를 통해 로컬 디스크로 즉시 다운로드됩니다.
- 백업 중에도 읽기 및 쓰기 쿼리가 차단되지 않는 비차단(Non-blocking) 방식으로 동작합니다.

### 9.2. 백업 파일 복원 (Hot Restore)
- 사전에 다운로드받은 백업 아카이브를 선택하고 업로드하면, 엔진이 데이터 디렉토리 상태를 해당 시점으로 안전하게 복원합니다.
- 복원 전 확인 경고 모달을 통해 데이터 덮어쓰기 위험을 사용자에게 사전에 고지합니다.

---

## 10. 보안 및 성능 최적화

LionSQL Web Studio는 외부 프로덕션 배포 시에도 안전하게 사용할 수 있도록 엄격한 엔지니어링 표준이 기본 적용되어 있습니다.

### 10.1. 엔터프라이즈 HTTP 보안 헤더
모든 Web Studio 요청 및 응답에 대해 다음 보안 헤더가 기본 적용됩니다:
- **`X-Content-Type-Options: nosniff`**: MIME 타입 스니핑 방지
- **`X-Frame-Options: DENY`**: 클릭재킹(Clickjacking) 공격 원천 방지
- **`X-XSS-Protection: 1; mode=block`**: 브라우저 XSS 필터링 강제
- **`Referrer-Policy: strict-origin-when-cross-origin`**: 리퍼러 정보 유출 방지

### 10.2. 캐시 무효화 및 에셋 신선도 보장
- Web Studio 메인 HTML(`/_/`)은 `Cache-Control: no-cache, no-store, must-revalidate` 헤더를 통해 브라우저가 이전 버전의 대시보드를 캐싱하지 않고 항상 최신 UI를 로드하도록 보장합니다.
- JavaScript SDK 에셋(`/sdk/lionsql.js`)은 적절한 유효기간과 ETag를 통해 네트워크 대역폭을 절약합니다.

### 10.3. Gzip 고속 압축 전송
모든 HTML, CSS, JavaScript 응답은 `Accept-Encoding: gzip` 요청에 대해 서버 수준에서 자동으로 gzip 압축되어 전송되므로, 수백 킬로바이트의 단일 페이지 애플리케이션도 수십 밀리초 이내에 로딩됩니다.

---

## 관련 참고 문서
- [LionSQL 전체 사용자 매뉴얼](USER_MANUAL.ko.md)
- [LionSQL Auto-REST API 가이드](REST_API_AUTO_GENERATED.md)
- [LionSQL Web SDK 개발자 가이드](WEB_SDK.md)
- [LionSQL 클라이언트 도구 연동 가이드](CLIENT_TOOL_INTEGRATION.md)
