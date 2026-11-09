# LionSQL Post-1.0 엔지니어링 후보 과제 (Candidates) (`notes/POST_1.0_CANDIDATES.ko.md`)

> - **최종 갱신일**: 2026-11-05 (Phase 3~4 초고난도 과제 Deep Freeze 냉동 보관 분류 및 PERF-SERVER-PIPELINE Spec-Out 반영)
> - **최초 작성일**: 2026-11-04  
> - **문서 성격**: **Architecture Candidates (사전 기술 분석을 완료하고 실제 니즈 발생 시 채택을 심사하는 온디맨드 후보 과제)**  
> - **핵심 원칙**: 본 문서는 의무적으로 완수해야 하는 개발 로드맵이 아니며, 이미 심층 설계와 스펙 분석이 완료되어 안전하게 준비해 둔 후보 과제(Candidates) 목록입니다. 향후 특정 비즈니스 요구나 극단적인 하드웨어 스케일링 니즈가 실제로 발생할 때에만 온디맨드(On-Demand)로 시동을 걸어 착수합니다.  
> - **연계 문서**:  
>   - 액티브 엔지니어링 대시보드: [`TODO.md`](../TODO.md)  
>   - 전사 전략 로드맵: [`ROADMAP.md`](../ROADMAP.md)  
>   - 아키텍처 불변 원칙: [`notes/ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md)  
>   - 과제 식별자 분류체계: [`notes/TAXONOMY.ko.md`](TAXONOMY.ko.md)  

---

## 1. 개요 및 목적 (Context & Objectives)

LionSQL은 v0.90.1을 거치며 **1.0 GA 릴리스에 필요한 모든 핵심 엔진 기능(이중 스토리지 엔진, 듀얼 와이어 프로토콜, 2-Tier CBO/VDBE 쿼리 엔진, T-SQL 저장 프로시저 런타임, 정규식 제로 불변식 등)의 구현 및 검증을 100% 완료**하였습니다.

1.0 이전의 잔여 작업은 기능 개발이 아닌 **초장기 운영 검증 게이트(`DURA-KILL-1` 1,000 라운드 사살 복구, `DURA-SOAK-1` 72시간 무중단 부하, 앱 섀도 모드 도그푸딩)**에 국한됩니다.

본 문서는 의무적인 개발 부채가 아닌 **"언젠가 필요해지면 열어볼 서랍 속의 완성도 높은 후보 과제 자산"**으로서, 11개 과제의 기술적 난이도, 시스템 파급 영향도, 핵심 우려사항(Risks & Caveats), 그리고 필요 시 착수할 수 있는 선택적 가이드를 보존하는 데 목적이 있습니다.

---

## 2. 난이도·영향도·우려사항 종합 매트릭스 (Summary Matrix)

| 번호 | 과제 코드 | 과제명 | 구현 난이도 | 시스템 영향도 | ROI (효익) | 핵심 우려사항 (Risks & Caveats) | 추천 추진 페이즈 |
| :---: | :--- | :--- | :---: | :---: | :---: | :--- | :---: |
| **1** | **`PKG-OFFLINE-DOCS`** | 배포 패키지 오프라인 문서 번들링 (`docs/index.html`) | **낮음 (★☆☆)** | **배포 스크립트 (None)** | **매우 높음** | 릴리스 아카이브 생성 스크립트 작성 및 오프라인 HTML 뷰어 템플릿 제작 | **Phase 1 (DX Hero)** |
| **2** | **`WEB-SPA-1`** | 단일 바이너리 내장 SPA 웹 서버 (`--app`, `--public`) | **낮음 (★☆☆)** | **국소 (Low)** | **높음** | 디렉토리 트래버설(`../`) 경로 보안 탈출 및 정적 파일 I/O 메모리 누수 | **Phase 1 (Zero Risk)** |
| **-** | **`PERF-SERVER-PIPELINE`**| Generic LRU 커서 & ReadOnly 자동 바이패스 | **중간 (★★☆)** | **위험 (Critical)** | **기각 (Spec-Out)** | 겉형식(SELECT)과 실질(시퀀스/부수효과) 불일치로 인한 격리성 파괴 및 LRU 락 경합 | **영구 보류 (Spec-Out)** |
| **4** | **`AUDIT-LOG-1`** | SHA-256 체이닝 불변 감사 로그 서브시스템 (SOC 2) | **중간 (★★☆)** | **중간 (Med)** | **중간** | 동기 I/O 지연으로 인한 TPS 급락, 감사 로그 디스크 공간 고갈 | **Phase 2 (Enterprise)** |
| **5** | **`INTEG-NOCODB/MATTERMOST`** | NocoDB & Mattermost 실전 엔터프라이즈 앱 연동 검증 | **중간 (★★☆)** | **국소~중간** | **높음** | 앱 전용 비표준 복합 쿼리로 인한 가상 카탈로그 규칙 복잡화 | **Phase 2 (Ecosystem)** |
| **6** | **`S3-1~4`** | 클라이언트 사이드 S3 Pre-signed URL 및 SigV4 서명기 | **중간 (★★☆)** | **독립적 (Low)** | **중간** | 서버-클라이언트 간 시계 오차(Clock Skew)로 인한 서명 만료 오류 | **Phase 2 (Utility)** |
| **12**| **`REPL-STREAM-BIN-1`** | Replication 네트워크 Length-Prefixed Binary Frame Zero-Copy 스트리밍 | **낮음~중간 (★☆☆~★★☆)** | **국소 (Low)** | **높음** | 네트워크 스트리밍 프로토콜 바이너리화 및 프레이밍 동기화 | **Phase 2 (Replication)** |
| **13**| **`WAL-DELTA-1`** | Partial / Delta WAL 로깅 (UPDATE 컬럼 차분 비트마스킹) | **중간~높음 (★★☆~★★★)** | **엔진 내부 (Med)** | **중간~높음** | 리플레이 시 Read-Modify-Write 오버헤드 및 보조 인덱스 변경 추적 정합성 | **Phase 2 (Engine Tuning)** |
| **7** | **`BENCH-TPCH-1`** | CBO 서브쿼리 Decorrelation (`IN`/`EXISTS` ➔ Semi-Join) | **높음 (★★★)** | **높음 (High)** | **매우 높음** | `NULL` 3치 논리(3VL) 정합성 깨짐 및 서브쿼리 카디널리티 오산출로 인한 잘못된 플랜 | **Phase 3 (Optimizer)** |
| **8** | **`PERF-COMPLEX-QUERY-2`**| Open-Addressing 정수 플랫 해시 조인 (Flat Hash Join) | **높음 (★★★)** | **중간 (Engine)** | **높음** | 고부하 해시 클러스터링/리사이징 시 순간 지연 및 메모리 스파이크 | **Phase 3 (Analytical)** |
| **9** | **`OCC-HYPER-2`** | In-Memory OCC Pointer-Free Flat Record (4~5x 메모리 압축)| **매우 높음 (★★★★)**| **광범위 (Critical)**| **극상 (Extreme)** | 엔진 전반 레이아웃 대수술로 인한 회귀 버그, 가변 길이 문자열 갱신 단편화 | **Phase 4 (Deep Kernel)** |
| **10**| **`LOC-1~4`** | MSSQL Collation 및 유니코드 다국어 로케일 정렬/비교 | **높음 (★★★)** | **광범위 (High)** | **중간** | 로케일 키 인코딩 오버헤드로 인한 순수 ASCII 문자열 비교 성능 저하 | **Phase 4 (Specialized)**|
| **11**| **`TDS-COMPAT-6`** | TDS 7.x/8.0 MARS (Multiple Active Result Sets) 스트리밍 | **매우 높음 (★★★★)**| **광범위 (Critical)**| **낮음~중간** | 복잡한 패킷 헤더 상태 머신으로 인한 교착 상태(Deadlock) 및 가치 대비 과도한 비용 | **Phase 5 (Deprioritized)**|
| **-** | **`FRIENDLY-SQL`** | DuckDB 스타일 편의 SQL (`* EXCLUDE`, `FROM tbl;` 등) | **낮음 (★☆☆)** | **국소 (Low)** | **기각 (Spec-Out)** | 오타 침묵 위험 및 RDBMS 엄격성 훼손 (LionSQL is NOT DuckDB 철학) | **영구 보류 (Spec-Out)** |

---

## 3. 과제별 심층 분석 및 세부 우려사항 (Deep-Dive Analysis & Caveats)

---

### 도메인 A. 개발자 경험 및 편의성 (Ergonomics & Standalone Ecosystem)

#### 1. `FRIENDLY-SQL`: DuckDB 스타일 편의 SQL 문법 지원
- **상세 설계서**: [`notes/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md`](plans/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md)
- **주요 구현 내용**:
  1. `SELECT * EXCLUDE (col1, col2) FROM tbl`: 와일드카드 프로젝션에서 특정 컬럼 제외.
  2. `FROM tbl;`: `SELECT *` 생략형 테이블 전체 스캔 구문 허용.
  3. `GROUP BY ALL`: 프로젝션 목록 내 비집계 수식을 감지하여 `GROUP BY` 절 자동 채우기.
  4. 로컬 파일 직접 조회: `SELECT * FROM 'data.csv'` 파서 Desugaring.
- **난이도 및 영향도**:
  - **난이도**: **낮음 (★☆☆)** — Pratt AST 파서(`pkg/proc/parser.go`)에서 Desugaring 수행.
  - **영향도**: **국소 (Low)** — 스토리지 엔진 및 실행기 변경 0건.
- **주요 우려사항 및 아키텍처 딜레마 (Architectural Dilemma & Key Caveats)**:
  - **1) 오타(Typo)의 조용한 침묵 (Silent Failure)**: 
    - 쿼리 복사/붙여넣기 중 `SELECT id, name ` 앞부분이 잘려나갔거나 오타가 났음에도(`FROM tbl;`), 엔진이 에러를 뱉지 않고 전체 수백만 행을 스캔하여 덤프하는 대형 운영 사고 유발 위험.
  - **2) 이식성(Portability) 훼손 및 프로덕션 배포 함정**: 
    - 개발자가 로컬에서 비표준 구문(`EXCLUDE`, `FROM tbl;`)에 익숙해진 뒤 순정 PostgreSQL / MSSQL 프로덕션 환경에 배포했을 때 즉각적인 `Syntax Error` 폭발.
  - **3) 문법 충돌 및 식별자 오염**: 
    - 실제 테이블에 `exclude`, `all`이라는 컬럼명이 존재하는 기존 레거시 스키마와의 파싱 모호성.
  - **4) 파일 직접 조회 보안 홀**: 
    - `SELECT * FROM '/etc/passwd'` 등 임의 시스템 파일 경로 탈출 시도 원천 차단([`[RULE:PathSecurity]`]) 필수.
- **아키텍처 제어 모델에 대한 심층 검토 및 미결 쟁점 (Open Debate)**:
  - **모델 1: 기본 탑재 (Default On / No Flags)**:
    - *장점*: 플래그 없는 극도의 단순함([`[RULE:RadicalKISS]`]).
    - *치명적 단점*: 위에서 제기된 오타 침묵, 비표준 문법 오염, 엔터프라이즈 RDBMS로서의 엄격함 훼손.
  - **모델 2: Strict Mode (옵트인 / `sql_mode = 'strict'`)**:
    - *장점*: 기본은 엄격한 표준 SQL을 강제하여 안전성을 보장하고, 필요한 개발자만 활성화.
    - *단점*: MySQL 특유의 악명 높은 '모드 지옥(Configuration Hell)'을 답습하게 되어 복잡도 증가.
  - **모델 3: 접속 채널/Dialect별 분기 (PG/TDS 차단 vs Web/CLI 허용)**:
    - *표면적 장점*: 드라이버 연동 앱(안전)과 사람의 콘솔 타이핑(편의)을 분리.
    - *치명적 단점 (세련되지 못한 분열증)*: **"동일한 데이터베이스 엔진인데 Web Studio에서는 도는 쿼리가 DataGrip(PGWire)이나 SSMS(TDS)에서는 문법 에러가 나는 기괴한 파편화(Inconsistency)"**를 낳게 됨. 사용자 입장에서 직관성을 심각하게 파괴하는 안티패턴이 될 위험 농후.
  - **잠정 보류 결론 (Conclusion)**:
    - 어떤 제어 방식을 택하든(Default On, Strict Mode, Dialect 분기) 뚜렷한 장단점과 설계적 결함이 공존하므로, **명확한 설계적 확신과 실제 사용자 요구가 수렴될 때까지 Candidates에 둔 채 성급한 구현을 영구 보류**함.

#### 2. `WEB-SPA-1`: 단일 바이너리 내장 SPA 웹 서버 호스팅
- **상세 설계서**: [`notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](plans/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md)
- **주요 구현 내용**:
  1. `--app <dir>` 및 `--public <dir>` CLI 플래그 추가.
  2. React/Vue/Svelte 빌드 정적 자산을 DB 엔진과 동일한 단일 바이너리에서 HTTP 서빙.
  3. 미등록 경로 요청 시 `index.html` 폴백(HTML5 History API 지원).
- **난이도 및 영향도**:
  - **난이도**: **낮음 (★☆☆)** — Go 표준 `http.FileServer` 및 `http.FS` 라우팅 결합.
  - **영향도**: **국소 (Low)** — DB 코어 커널과 완전 분리된 HTTP 핸들러.
- **주요 우려사항 (Key Caveats)**:
  - **경로 탈출(Directory Traversal) 취약점**: 악의적인 URL 인코딩(`%2e%2e%2f` 등)을 통한 시스템 파일 유출 위험 (`filepath.Clean` 및 상위 경로 격리 강제).
  - **대용량 정적 파일 I/O로 인한 DB 간섭**: 고용량 에셋(동영상, 거대 JS 번들) 요청이 폭주할 경우 DB 쿼리 서비스의 OS 소켓 및 버퍼 메모리를 잠식할 위험 (정적 서빙 대역폭/동시성 상한선 제한 필요).

#### 3. `PKG-OFFLINE-DOCS`: 배포 패키지 오프라인 문서 번들링 (`docs/index.html`)
- **상세 설계서**: [`notes/OFFLINE_DOCS_PACKAGING_PLAN.ko.md`](plans/OFFLINE_DOCS_PACKAGING_PLAN.ko.md)
- **개념 및 비전**: 
  - *"현대 개발자는 터미널로 폴더를 뒤지지 않고 브라우저로 문서를 본다."*
  - **DB 엔진 바이너리 순수성 보존**: 바이너리에 문서를 억지로 내장(`embed.FS`)하지 않고, 배포 압축 패키지(`tar.gz` / `.zip`) 내에 소스코드를 제외한 순수 배포용 문서(`docs/`)와 오프라인 브라우저 뷰어(`docs/index.html`)를 함께 번들링.
  - **폐쇄망 완전 자립**: 인터넷이 차단된 에어갭 환경에서도 압축 해제 후 `docs/index.html`을 브라우저로 **더블 클릭**하기만 하면, 로컬 브라우저에서 목차 트리와 검색을 갖춘 완전한 공식 매뉴얼을 열람 가능.
- **주요 구현 내용**:
  1. 순수 바닐라 JS/CSS 기반 외부 CDN 의존성 0% 오프라인 브라우저 뷰어(`docs/index.html`) 작성.
  2. 릴리스 스크립트(`scripts/release.sh`)에서 소스코드를 제외한 `doc/` 및 `guide/` 문서를 번들링하여 `lionsql-<os>-<arch>.tar.gz` 및 `.zip` 아카이브 생성.
- **난이도 및 영향도**:
  - **난이도**: **낮음 (★☆☆)** — 순수 릴리스 패키징 및 HTML 템플릿 스크립트 작업.
  - **영향도**: **엔진 코어 영향 0 (Zero Engine Impact)** — Go 소스코드 수정 0줄, 바이너리 크기 증가 0바이트.

---

### 도메인 B. 처리량 및 엔터프라이즈 보안 (Pipeline & Enterprise Governance)

#### 3. `PERF-SERVER-PIPELINE-P3-P4`: 서버 파이프라인 Phase 3/4 — **[영구 보류 (Spec-Out)]**
- **상세 설계서**: [`notes/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`](plans/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md)
- **주요 제안 내용**:
  1. Generic LRU 커서/Prepared Statement 캐시 도입.
  2. ReadOnly 쿼리에 대해 트랜잭션 락/OCC 스냅샷 생성 비용을 면제하는 자동 바이패스 경로 구축.
  3. 고동시성 쓰기 트래픽 감지 시 적응형 그룹 커밋(Adaptive Group Commit).
- **영구 보류(Spec-Out) 확정 사유 (형식과 실제의 불일치 딜레마)**:
  - **1) 겉형식(Syntax)과 실질(Semantics)의 불일치로 인한 격리성 파괴**:
    - 쿼리의 겉모습이 `SELECT ...` 형태라 하더라도 내부에는 `NEXTVAL('seq')`, 임시 테이블 생성(`SELECT ... INTO #temp`), 세션 변수 조작(`SET @x = ...`), 부수효과(Side-Effect)를 가진 UDF 호출 등이 포함될 수 있습니다.
    - 엔진이 이를 순수 읽기로 오판하여 트랜잭션 락/스냅샷을 바이패스하는 순간, **더티 리드(Dirty Read), 팬텀 리드, 쓰기 손실 등 ACID 트랜잭션 무결성이 치명적으로 파괴**됩니다.
    - "이 쿼리가 100% 순수한 ReadOnly인가?"를 완벽히 가려내려면 복잡한 AST 심층 분석을 매번 수행해야 하므로, **바이패스로 얻는 미세한 이득보다 검증 오버헤드가 더 커지는 본말전도(배보다 배꼽)**가 발생합니다.
  - **2) LRU 캐시 락 경합(Cache Contention)으로 인한 성능 역전**:
    - 고동시성 워커(100~200개)가 단일 LRU 뮤텍스에 몰릴 경우 락 경합(Lock Convoy)으로 인해 오히려 전체 QPS가 급락하는 역효과를 초래합니다.
  - **결론**: 형식과 실제가 다른 SQL의 본질적인 특성상 완벽한 안전성을 담보할 수 없으므로, **ReadOnly 자동 바이패스는 영구 기각(Spec-Out)**하고 표준 트랜잭션 경계를 엄격히 유지합니다.

#### 4. `AUDIT-LOG-1`: SHA-256 불변 감사 로그 서브시스템
- **상세 설계서**: [`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](plans/IMMUTABLE_AUDIT_LOG_PLAN.md)
- **주요 구현 내용**:
  1. 모든 DDL 변경, 사용자 권한 수정, 주요 DML 이력을 기록하는 Append-Only 전용 로그 블록.
  2. 이전 블록의 SHA-256 해시를 다음 레코드에 포함하는 해시 체인 구조.
  3. SOC 2 Type II 및 PCI-DSS 준수용 검증 CLI (`lionsql audit verify`).
- **난이도 및 영향도**:
  - **난이도**: **중간 (★★☆)** — 암호화 해시 연산 및 링 버퍼 논블로킹 I/O 구현.
  - **영향도**: **중간 (Med)** — 코디네이터 커밋 경로에 감사 로거 훅 인터페이스 결합.
- **주요 우려사항 (Key Caveats)**:
  - **OLTP 핫패스 지연 폭증**: 감사 로그를 디스크에 동기(`fsync`) 커밋할 경우 트랜잭션 TPS가 최대 80% 이상 폭락할 위험 (비동기 링 버퍼 플러시 및 타임아웃 윈도우 필수).
  - **디스크 공간 고갈(Disk Exhaustion)**: DML이 빈번한 환경에서 감사 로그가 기가바이트 단위로 급증하여 디스크 풀을 유발할 위험 (순환 아카이빙 및 압축 정책 필수).

#### 5. `INTEG-NOCODB-1` & `INTEG-MATTERMOST-1`: 실전 앱 2종 추가 연동
- **주요 구현 내용**:
  1. NocoDB: Knex 기반 다중 스키마 인트로스펙션 및 REST 기반 스프레드시트 CRUD 검증 (듀얼 프로토콜 PG+TDS).
  2. Mattermost: Go `sqlx`/`squirrel` 기반 대규모 채널/메시지 색인 쿼리 통과.
- **난이도 및 영향도**:
  - **난이도**: **중간 (★★☆)** — 기존 10개 앱 연동 인프라 기반 수용.
  - **영향도**: **국소~중간** — 가상 카탈로그 및 파라미터 매핑 보강.
- **주요 우려사항 (Key Caveats)**:
  - **특화 쿼리로 인한 카탈로그 복잡도 증가**: NocoDB가 요구하는 방대한 시스템 카탈로그 뷰(`pg_attribute`, `information_schema`)의 희귀 컬럼 요구로 인해 가상 카탈로그 코드가 복잡해질 우려.

#### 6. `S3-1` ~ `S3-4`: Client-Side S3 Pre-signed URL 직접 업로드 및 SigV4 서명기
- **상세 설계서**: [`notes/S3_PRESIGNED_STORAGE_PLAN.md`](plans/S3_PRESIGNED_STORAGE_PLAN.md)
- **주요 구현 내용**:
  1. 순수 Go 기반 AWS Signature Version 4 (SigV4) 서명 생성기 구현.
  2. SQL 내장 함수 `GENERATE_S3_PRESIGNED_URL(bucket, key, method, expire)` 제공.
- **난이도 및 영향도**:
  - **난이도**: **중간 (★★☆)** — HMAC-SHA256 암호화 서명 알고리즘 표준 구현.
  - **영향도**: **독립적 (Low)** — DB 커널 스토리지와 무관한 독립적 헬퍼 모듈.
- **주요 우려사항 (Key Caveats)**:
  - **시계 오차(Clock Skew)로 인한 인증 거부**: DB 호스트 서버와 AWS S3 엔드포인트 간 시스템 시계가 몇 초 이상 어긋날 경우 발급된 서명이 즉시 만료되어 클라이언트 업로드가 실패할 위험.
  - **Secret Key 노출 위험**: SQL 쿼리 문맥에 AWS Secret Access Key가 평문으로 전달될 경우 실행 로그나 슬로우 쿼리에 자격 증명이 유출될 위험 (환경 변수 또는 DB 보안 시크릿 저장소 연동 필수).

---

### 도메인 C. 옵티마이저 & 분석 성능 쥐어짜기 (CBO & Analytical Performance)

#### 7. `BENCH-TPCH-1`: Mini TPC-H 스위트 및 CBO 서브쿼리 Decorrelation
- **상세 설계서**: [`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](plans/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)
- **주요 구현 내용**:
  1. 상관 서브쿼리(`WHERE EXISTS`, `WHERE col IN`)를 CBO 플래너에서 Semi-Join / Anti-Join으로 재작성(Decorrelation).
  2. TPC-H 복합 분석 핵심 쿼리(Q1, Q3, Q4, Q6, Q9) 전용 벤치마크 하네스 구축.
- **난이도 및 영향도**:
  - **난이도**: **높음 (★★★)** — 상관 서브쿼리의 외부 참조 변수 바인딩 해제 및 조인 트리 재작성.
  - **영향도**: **높음 (High)** — `pkg/planner` 전반의 플랜 생성 경로에 광범위한 영향.
- **주요 우려사항 (Key Caveats)**:
  - **SQL 3-Valued Logic (`NULL`) 정합성 파손**: `NOT IN (SELECT col FROM t)` 서브쿼리 결과에 `NULL`이 하나라도 포함될 경우 전체 결과가 공집합이 되어야 하는 SQL 표준 3VL 정합성이 Anti-Join 변환 시 왜곡될 위험.
  - **잘못된 카디널리티 추정으로 인한 플랜 역주행**: 서브쿼리 카디널리티를 잘못 추정하여 대규모 테이블을 Nested Loop로 풀거나 메모리 초과 해시 조인을 생성하여 성능이 오히려 극단적으로 악화될 위험.

#### 8. `PERF-COMPLEX-QUERY-2`: Open-Addressing 정수 플랫 해시 조인 (Flat Hash Join)
- **상세 설계서**: [`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](plans/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)
- **주요 구현 내용**:
  1. Go 맵 기반 해시 조인을 CPU 캐시 친화적인 단일 연속 메모리 슬라이스 오픈 어드레싱 해시 테이블로 전환.
  2. 정수 조인 키에 대해 해시 계산을 바이패스하고 비트 연산으로 슬롯 직결.
- **난이도 및 영향도**:
  - **난이도**: **높음 (★★★)** — 선형 탐색 해시 충돌 해결 및 동적 리사이징 구현.
  - **영향도**: **중간 (Volcano Engine)** — `pkg/execution/hash_join.go` 내부 루프 중심 교체.
- **주요 우려사항 (Key Caveats)**:
  - **해시 클러스터링 및 선형 탐색 지연**: 로드 팩터(Load Factor)가 70%를 초과할 때 충돌 체인이 길어져 O(1) 조인이 최악의 경우 O(N)으로 퇴화할 위험.
  - **테이블 리사이징 메모리 스파이크**: 대규모 빌드 단계에서 슬라이스 크기를 2배로 확장할 때 일시적인 메모리 할당량 2배 스파이크로 인한 OOM(Out of Memory) 위험.

---

### 도메인 D. 코어 스토리지 & 프로토콜 딥 아키텍처 (Deep Kernel Redesign)

#### 9. `OCC-HYPER-2`: In-Memory OCC Pointer-Free Flat Record & Memory Compaction
- **상세 설계서**: [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](plans/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)
- **주요 구현 내용**:
  1. 레코드 표현을 Go 인터페이스 슬라이스(`[]any`)에서 오프셋 기반 단일 연속 바이트(`[]byte`)로 전면 개편.
  2. 튜플 언박싱 없이 고정 오프셋 O(1) 직접 읽기(0.5ns) 달성.
  3. Go GC 스캔 대상 힙 포인터 0화 및 4~5배 메모리 압축.
- **난이도 및 영향도**:
  - **난이도**: **매우 높음 (★★★★)** — 메모리 레이아웃 직렬화/역직렬화 엔진 완전 재설계.
  - **영향도**: **광범위 (Critical)** — 인메모리 스토리지(`pkg/storage/memengine`), 인덱스, VDBE VM 바이트코드 연산자, Volcano 튜플 프로젝션 전체가 참조하므로 **엔진 코어 전반의 대수술**.
- **주요 우려사항 (Key Caveats)**:
  - **광범위한 회귀 버그 유발 위험**: 데이터 접근의 근간이 바뀌므로 사소한 오프셋 계산 실수(`[RULE:DefensiveIndexing]`)로도 사일런트 데이터 오염 또는 패닉 유발 가능성.
  - **가변 길이 문자열 갱신 시 메모리 단편화**: 고정 길이 컬럼과 달리 `VARCHAR` 데이터의 크기가 빈번히 변경되는 UPDATE 발생 시 슬롯 재할당 및 메모리 단편화(Fragmentation)가 누적될 위험.
  - **포맷 변경에 따른 성능 Trade-off 및 도입 판단 불확실성**:
    - 현재 In-Memory OCC의 압도적인 15만~21만 TPS는 Go 구조체(`storage.Row { Values: []Value }`)를 메모리에 직접 들고 있어 $O(1)$(0.5ns)로 즉시 참조하는 아키텍처에 기인함.
    - 메모리 집약도를 높이기 위해 인메모리 포맷을 `[]byte` 압축 형태로 전환할 경우, 메모리는 4~5배 절감되지만 매 접근마다 디코딩 비용이 발생하여 CPU 처리량이 하락할 수 있음.
    - 따라서 이 포맷 전환 시 **Lazy Decoding(지연 디코딩)과 Zero-Copy 슬롯화**가 성능 저하를 방어하는 핵심 열쇠가 되며, 실무적으로 "메모리 절감 vs 극강의 CPU 속도" 사이에서 전환을 실제로 단행해야 할지 여부는 실측 벤치마크 기반의 신중한 재검토가 필요함.
  - **반드시 1.0 릴리스 이후 별도 격리 브랜치에서 고립 개발 필수**.

#### 10. `LOC-1` ~ `LOC-4`: MSSQL Collation 및 유니코드 다국어 로케일
- **상세 설계서**: [`notes/COLLATION_AND_LOCALE_PLAN.md`](plans/COLLATION_AND_LOCALE_PLAN.md)
- **주요 구현 내용**:
  1. `Korean_Wansung_CI_AS`, `SQL_Latin1_General_CP1_CI_AS` 등 대소문자 무시(CI), 악센트 구분(AS) 지원.
  2. B+Tree 인덱스 키 생성기 및 정렬 연산자에 언어별 로케일 가중치 키(Collation Key) 인코딩 통합.
- **난이도 및 영향도**:
  - **난이도**: **높음 (★★★)** — 유니코드 표준 정렬 알고리즘(UCA) 적용 및 인덱스 키 호환성 유지.
  - **영향도**: **광범위 (High)** — 인덱스 순서와 문자열 비교 연산자 전체에 영향.
- **주요 우려사항 (Key Caveats)**:
  - **문자열 비교 성능 급락**: 단순 바이트 비교(`bytes.Compare`) 대신 유니코드 로케일 가중치 테이블을 참조해야 하므로, 문자열 필터링/정렬 처리량이 최대 3~5배 하락할 위험 (ASCII/Binary Fast-Path 우회 경로 필수).
  - **스토리지 인덱스 비호환**: 로케일 규칙이 변경되면 기존에 빌드된 B+Tree 인덱스의 정렬 순서가 틀어져 전체 인덱스를 재빌드(`REINDEX`)해야 하는 호환성 마찰.

#### 11. `TDS-COMPAT-6`: TDS MARS (Multiple Active Result Sets) 스트리밍
- **주요 구현 내용**:
  1. 하나의 TCP 연결 내에서 여러 쿼리의 결과 토큰 스트림을 패킷 단위로 인터리빙 스트리밍(Multiplexing).
- **난이도 및 영향도**:
  - **난이도**: **매우 높음 (★★★★)** — 복잡한 TDS 패킷 헤더 상태 머신 및 세션 다중화 동시성 제어.
  - **영향도**: **광범위 (Critical)** — TDS 리스너(`pkg/wire/tds/server.go`) 아키텍처의 전면 비동기 프레임화.
- **주요 우려사항 (Key Caveats)**:
  - **상태 머신 복잡도로 인한 데드락 및 누수**: 클라이언트가 1번 쿼리의 결과 패킷을 다 읽지 않은 채 2번 쿼리를 실행하다 연결이 끊길 때 버퍼 고갈이나 고루틴 누수([`[RULE:ZeroOrphan]`]) 발생 위험.
  - **가치 대비 극도로 높은 엔지니어링 비용**: Dapper 등 극히 일부 C# 라이브러리 외에는 실무에서 잘 쓰이지 않으며 모던 애플리케이션은 커넥션 풀을 선호하므로, **노력 대비 실익이 가장 빈약함 (최후순위 유예 권장)**.

---

## 4. 단계별 실행 로드맵 (Phased Execution Strategy)

```
[Phase 1: Quick-Wins & High ROI] (우려사항 극소, 체감 가치 극대화)
  ├── 1. PKG-OFFLINE-DOCS (Web Studio 내장 오프라인 문서 뷰어 /_/docs)
  ├── 2. WEB-SPA-1 (단일 바이너리 내장 SPA 웹 서버)
  └── [보류] FRIENDLY-SQL (LionSQL is NOT DuckDB 철학에 따라 영구 보류)

[Phase 2: Throughput & Enterprise Extensibility] (처리량 확장 및 보안 거버넌스)
  ├── 3. AUDIT-LOG-1 (SHA-256 체이닝 불변 감사 로그)
  ├── 4. INTEG-NOCODB / MATTERMOST (실전 앱 연동 확장)
  ├── 5. S3-1~4 (클라이언트 사이드 S3 Pre-signed URL)
  └── [보류] PERF-SERVER-PIPELINE (ReadOnly 형식-실질 불일치 격리성 파괴 위험으로 영구 보류)

[Phase 3 & 4: Deep Freezer Backlog] (초고난도 & 시스템 파급 위험으로 초장기 냉동 보관)
  ├── 6. BENCH-TPCH-1 (CBO 서브쿼리 Decorrelation: NULL 3VL 정합성 파괴 위험)
  ├── 7. PERF-COMPLEX-QUERY-2 (Open-Addressing 플랫 해시 조인: 해시 충돌 및 메모리 스파이크 위험)
  ├── 8. OCC-HYPER-2 (Pointer-Free Flat Record: 가변 문자열 단편화 및 인메모리 스토리지 전면 재작성)
  └── 9. LOC-1~4 (MSSQL Collation 언어별 로케일: 문자열 비교 3~5배 급락 및 B+Tree 인덱스 순서 파괴)

[Phase 5: Deprioritized Niche Protocol] (실익 빈약으로 최후순위 유예)
  └── 10. TDS-COMPAT-6 (TDS MARS 인터리빙 스트리밍)
```

---

---
## 4. Post-1.0 v1.1.0 P2 이관 백로그 전수 카탈로그 (총 92건)
> - **이관 근거**: `[RULE:RadicalKISS]`, `[RULE:PipelineVerification]`
> - **성격**: 상용 OLTP 및 일반 프로덕션 워크로드에서 발생하지 않는 극한의 엣지 케이스, 비현실적 중첩 깊이 및 미세 마진 항목
> - **관리 원칙**: 1.0 코드베이스 복잡도 억제 및 무결성 보존을 위해 v1.1.0 마이너 릴리스 로드맵으로 안전하게 이관하며, 아래 카탈로그를 단일 진실 공급원(SSOT)으로 보존함.

### A. 쿼리 컴파일러, 파서 & CBO 엣지 케이스 (Query Compiler & Parser Margins) (10건)
#### `P2-09` [CBO 테이블 통계 캐시, 원자적 JSON 영속화(`catalog_objects.json`), 뷰(View) 및 확장 통계 객체 관리] (`Scope-026`)
* **원천 감사 코드**: `Scope-026` (심각도: `Medium`)
* **상세 시나리오**: CBO 테이블 통계 캐시, 원자적 JSON 영속화(`catalog_objects.json`), 뷰(View) 및 확장 통계 객체 관리
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-17` [2-Tier 쿼리 라우팅 (VDBE vs CBO 선택), 트랜잭션 컨텍스트 전파 및 세션 캐시 무효화] (`Scope-036`)
* **원천 감사 코드**: `Scope-036` (심각도: `Medium`)
* **상세 시나리오**: 2-Tier 쿼리 라우팅 (VDBE vs CBO 선택), 트랜잭션 컨텍스트 전파 및 세션 캐시 무효화
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-29` [`CREATE PROCEDURE`, `EXEC` 프로시저 실행기, 파라미터 역바인딩 및 인라인 권한] (`Scope-050`)
* **원천 감사 코드**: `Scope-050` (심각도: `Medium`)
* **상세 시나리오**: `CREATE PROCEDURE`, `EXEC` 프로시저 실행기, 파라미터 역바인딩 및 인라인 권한
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-33` [T-SQL Fast-Path 어휘 분석기(Lexer) 및 토큰 정의] (`Scope-056`)
* **원천 감사 코드**: `Scope-056` (심각도: `Medium`)
* **상세 시나리오**: T-SQL Fast-Path 어휘 분석기(Lexer) 및 토큰 정의
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-37` [AST 노드 정의, 프로시저 특성 판별 및 엄격 모드 검사] (`Scope-060`)
* **원천 감사 코드**: `Scope-060` (심각도: `Medium`)
* **상세 시나리오**: AST 노드 정의, 프로시저 특성 판별 및 엄격 모드 검사
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-38` [T-SQL AST ➔ VDBE 바이트코드 컴파일러 및 Opcode 정의] (`Scope-061`)
* **원천 감사 코드**: `Scope-061` (심각도: `Medium`)
* **상세 시나리오**: T-SQL AST ➔ VDBE 바이트코드 컴파일러 및 Opcode 정의
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-40` [Tier 2 ANTLR4 공식 문법 파서 래퍼 및 AST 변환기] (`Scope-063`)
* **원천 감사 코드**: `Scope-063` (심각도: `Medium`)
* **상세 시나리오**: Tier 2 ANTLR4 공식 문법 파서 래퍼 및 AST 변환기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-50` [CBO 논리 플랜 생성기 및 관계대수 트리 변환] (`Scope-076`)
* **원천 감사 코드**: `Scope-076` (심각도: `Medium`)
* **상세 시나리오**: CBO 논리 플랜 생성기 및 관계대수 트리 변환
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-51` [CBO 비용 모델링 (CPU/IO Cost 평가 및 최적 조인 순서)] (`Scope-077`)
* **원천 감사 코드**: `Scope-077` (심각도: `Medium`)
* **상세 시나리오**: CBO 비용 모델링 (CPU/IO Cost 평가 및 최적 조인 순서)
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-56` [CBO 조인 변환 규칙 및 7개 이상 테이블 탐욕적 조인 순서 최적화] (`Scope-083`)
* **원천 감사 코드**: `Scope-083` (심각도: `Medium`)
* **상세 시나리오**: CBO 조인 변환 규칙 및 7개 이상 테이블 탐욕적 조인 순서 최적화
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

### B. 실행 엔진, 조인/집계 & 슬라이스 경계값 (Execution Engine & Join Margins) (1건)
#### `P2-14` [`FormatResult`, `evaluateWhereConditionMap`, `parseImplicitAlias`, `ExtractSQLPa] (`Scope-033`)
* **원천 감사 코드**: `Scope-033` (심각도: `Medium`)
* **상세 시나리오**: `FormatResult`, `evaluateWhereConditionMap`, `parseImplicitAlias`, `ExtractSQLParts`
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

### C. 스토리지, 인메모리 OCC & 복구 미세 튜닝 (Storage, OCC & Recovery Margins) (5건)
#### `P2-02` [CGo/WiredTiger 비활성화 빌드 시 스텁 구현체] (`Scope-016`)
* **원천 감사 코드**: `Scope-016` (심각도: `Medium`)
* **상세 시나리오**: CGo/WiredTiger 비활성화 빌드 시 스텁 구현체
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-03` [`storage.Engine`, `Transaction`, `Session`, `Cursor` 핵심 인터페이스 명세 및 `DataType`, `] (`Scope-017`)
* **원천 감사 코드**: `Scope-017` (심각도: `Medium`)
* **상세 시나리오**: `storage.Engine`, `Transaction`, `Session`, `Cursor` 핵심 인터페이스 명세 및 `DataType`, `TableSchema`
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-71` [1-Writer Multi-Reader WAL 복제 코디네이터] (`Scope-106`)
* **원천 감사 코드**: `Scope-106` (심각도: `Medium`)
* **상세 시나리오**: 1-Writer Multi-Reader WAL 복제 코디네이터
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-72` [레플리카 WAL 수신 클라이언트 및 변경사항 브로드캐스터] (`Scope-107`)
* **원천 감사 코드**: `Scope-107` (심각도: `Medium`)
* **상세 시나리오**: 레플리카 WAL 수신 클라이언트 및 변경사항 브로드캐스터
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-75` [시점 복구(PITR) 및 지속적 WAL 아카이빙 엔진] (`Scope-110`)
* **원천 감사 코드**: `Scope-110` (심각도: `Medium`)
* **상세 시나리오**: 시점 복구(PITR) 및 지속적 WAL 아카이빙 엔진
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

### D. 와이어 프로토콜, 보안 & 네트워크 경계 조건 (Wire Protocols & Network Edge Cases) (76건)
#### `P2-01` [`IndexScan`, `IndexScanDesc`, `wtFilteredIndexCursor`, 커서 풀링] (`Scope-015`)
* **원천 감사 코드**: `Scope-015` (심각도: `Medium`)
* **상세 시나리오**: `IndexScan`, `IndexScanDesc`, `wtFilteredIndexCursor`, 커서 풀링
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-04` [임의 정밀도 고정소수점 `Decimal` 산술 연산(`Add`, `Sub`, `Mul`, `Div`, `Rescale`) 및 JSON 코덱] (`Scope-019`)
* **원천 감사 코드**: `Scope-019` (심각도: `Medium`)
* **상세 시나리오**: 임의 정밀도 고정소수점 `Decimal` 산술 연산(`Add`, `Sub`, `Mul`, `Div`, `Rescale`) 및 JSON 코덱
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-05` [MessagePack 콤팩트 2-요소 배열(`[Type, Payload]`) 인코딩/디코딩, 레거시 맵 호환성] (`Scope-020`)
* **원천 감사 코드**: `Scope-020` (심각도: `Medium`)
* **상세 시나리오**: MessagePack 콤팩트 2-요소 배열(`[Type, Payload]`) 인코딩/디코딩, 레거시 맵 호환성
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-06` [SQL 데이터 타입 상향 승격 격자(Type Promotion Lattice), DateTime/Date 변환, UUID 및 Hex 바이너리 파] (`Scope-021`)
* **원천 감사 코드**: `Scope-021` (심각도: `Medium`)
* **상세 시나리오**: SQL 데이터 타입 상향 승격 격자(Type Promotion Lattice), DateTime/Date 변환, UUID 및 Hex 바이너리 파서
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-07` [인조 기본키 컬럼 정의(`_row_id`), 벤치마크 디렉터리 격리 생성, UUIDv7 생성기 및 디폴트 리터럴 파서] (`Scope-024`)
* **원천 감사 코드**: `Scope-024` (심각도: `Medium`)
* **상세 시나리오**: 인조 기본키 컬럼 정의(`_row_id`), 벤치마크 디렉터리 격리 생성, UUIDv7 생성기 및 디폴트 리터럴 파서
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-08` [저장 프로시저, 사용자 정의 함수(UDF), 트리거 등록/수정/삭제 및 대소문자 무시 식별자 정규화] (`Scope-025`)
* **원천 감사 코드**: `Scope-025` (심각도: `Medium`)
* **상세 시나리오**: 저장 프로시저, 사용자 정의 함수(UDF), 트리거 등록/수정/삭제 및 대소문자 무시 식별자 정규화
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-10` [Table-Driven 가상 카탈로그 라우팅 디스패처(`catalogRouteTable`), MSSQL/PG 쿼리 디스패치 및 폴백] (`Scope-028`)
* **원천 감사 코드**: `Scope-028` (심각도: `Medium`)
* **상세 시나리오**: Table-Driven 가상 카탈로그 라우팅 디스패처(`catalogRouteTable`), MSSQL/PG 쿼리 디스패치 및 폴백
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-11` [MSSQL 테이블/스키마명 동등 조건 추출(`ExtractMssqlTableNameEq`), `OBJECT_ID` 추출기, 데이터 타입 포맷터] (`Scope-029`)
* **원천 감사 코드**: `Scope-029` (심각도: `Medium`)
* **상세 시나리오**: MSSQL 테이블/스키마명 동등 조건 추출(`ExtractMssqlTableNameEq`), `OBJECT_ID` 추출기, 데이터 타입 포맷터
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-12` [외래키 제약조건(`execReferentialConstraintsForeignKey`), 인덱스/컬럼 메타데이터, `sys.all_objects] (`Scope-030`)
* **원천 감사 코드**: `Scope-030` (심각도: `Medium`)
* **상세 시나리오**: 외래키 제약조건(`execReferentialConstraintsForeignKey`), 인덱스/컬럼 메타데이터, `sys.all_objects` 시스템 뷰
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-13` [`pg_class`, `pg_attribute`, `pg_index`, `tableBaseOID`] (`Scope-031`)
* **원천 감사 코드**: `Scope-031` (심각도: `Medium`)
* **상세 시나리오**: `pg_class`, `pg_attribute`, `pg_index`, `tableBaseOID`
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-15` [`information_schema.tables`, `columns`, `schemata`, `sys_tables.go` 가상 테이블 소스] (`Scope-034`)
* **원천 감사 코드**: `Scope-034` (심각도: `Medium`)
* **상세 시나리오**: `information_schema.tables`, `columns`, `schemata`, `sys_tables.go` 가상 테이블 소스
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-16` [`Coordinator` 구조체 수명주기, 세션 풀 연동, 동적 튜닝 및 `GetOrCreateSession`] (`Scope-035`)
* **원천 감사 코드**: `Scope-035` (심각도: `Medium`)
* **상세 시나리오**: `Coordinator` 구조체 수명주기, 세션 풀 연동, 동적 튜닝 및 `GetOrCreateSession`
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-18` [`CancelSessionQuery`, `CancelQueryBySPID`, `GetActiveQueries`, 세션 종료 및 자원 정리(`Cl] (`Scope-037`)
* **원천 감사 코드**: `Scope-037` (심각도: `Medium`)
* **상세 시나리오**: `CancelSessionQuery`, `CancelQueryBySPID`, `GetActiveQueries`, 세션 종료 및 자원 정리(`CloseSession`)
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-19` [다중 쿼리 배치 분해(`SplitSQLStatements`), 세미콜론 및 `GO` 배치 분리, 세션 상태 변수] (`Scope-039`)
* **원천 감사 코드**: `Scope-039` (심각도: `Medium`)
* **상세 시나리오**: 다중 쿼리 배치 분해(`SplitSQLStatements`), 세미콜론 및 `GO` 배치 분리, 세션 상태 변수
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-20` [`CREATE TABLE`, `DROP TABLE`, `TRUNCATE`, 복합 기본키 및 외래키 인라인 파싱] (`Scope-040`)
* **원천 감사 코드**: `Scope-040` (심각도: `Medium`)
* **상세 시나리오**: `CREATE TABLE`, `DROP TABLE`, `TRUNCATE`, 복합 기본키 및 외래키 인라인 파싱
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-21` [`ALTER TABLE` (컬럼 추가/수정/삭제, 제약조건, 이름 변경) 및 관용 조건부 DDL] (`Scope-041`)
* **원천 감사 코드**: `Scope-041` (심각도: `Medium`)
* **상세 시나리오**: `ALTER TABLE` (컬럼 추가/수정/삭제, 제약조건, 이름 변경) 및 관용 조건부 DDL
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-22` [`CREATE/DROP INDEX`, 관용 DDL 모드 및 식별자 경계 정규화] (`Scope-042`)
* **원천 감사 코드**: `Scope-042` (심각도: `Medium`)
* **상세 시나리오**: `CREATE/DROP INDEX`, 관용 DDL 모드 및 식별자 경계 정규화
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-23` [`INSERT INTO`, `SELECT INTO` 실행기, `ON CONFLICT` 절 및 DML 어휘 파서] (`Scope-043`)
* **원천 감사 코드**: `Scope-043` (심각도: `Medium`)
* **상세 시나리오**: `INSERT INTO`, `SELECT INTO` 실행기, `ON CONFLICT` 절 및 DML 어휘 파서
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-24` [`UPDATE`, `DELETE` 실행기, 사설 워크스페이스 변경 및 외래키 연쇄 삭제] (`Scope-044`)
* **원천 감사 코드**: `Scope-044` (심각도: `Medium`)
* **상세 시나리오**: `UPDATE`, `DELETE` 실행기, 사설 워크스페이스 변경 및 외래키 연쇄 삭제
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-25` [최상위 `SELECT` 절 실행 및 프로젝션 평가, 계산식 별칭 매핑] (`Scope-045`)
* **원천 감사 코드**: `Scope-045` (심각도: `Medium`)
* **상세 시나리오**: 최상위 `SELECT` 절 실행 및 프로젝션 평가, 계산식 별칭 매핑
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-26` [T-SQL 페이징(`TOP`, `OFFSET FETCH`), `Bare IN` 리스트 평가] (`Scope-046`)
* **원천 감사 코드**: `Scope-046` (심각도: `Medium`)
* **상세 시나리오**: T-SQL 페이징(`TOP`, `OFFSET FETCH`), `Bare IN` 리스트 평가
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-27` [`MERGE` (UPSERT) 문 실행기, `CROSS APPLY` / `OUTER APPLY` 관계 확장] (`Scope-048`)
* **원천 감사 코드**: `Scope-048` (심각도: `Medium`)
* **상세 시나리오**: `MERGE` (UPSERT) 문 실행기, `CROSS APPLY` / `OUTER APPLY` 관계 확장
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-28` [FORWARD-ONLY 세션 커서 및 `PREPARE` / `EXECUTE` 실행기] (`Scope-049`)
* **원천 감사 코드**: `Scope-049` (심각도: `Medium`)
* **상세 시나리오**: FORWARD-ONLY 세션 커서 및 `PREPARE` / `EXECUTE` 실행기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-30` [행 단위 조건식 평가기 및 SQL Row 직렬화/역직렬화] (`Scope-051`)
* **원천 감사 코드**: `Scope-051` (심각도: `Medium`)
* **상세 시나리오**: 행 단위 조건식 평가기 및 SQL Row 직렬화/역직렬화
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-31` [`PIVOT`, `UNPIVOT`, `STRING_SPLIT` 및 `EXPLAIN` 플랜 서술] (`Scope-052`)
* **원천 감사 코드**: `Scope-052` (심각도: `Medium`)
* **상세 시나리오**: `PIVOT`, `UNPIVOT`, `STRING_SPLIT` 및 `EXPLAIN` 플랜 서술
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-32` [JDBC 메타데이터 변환, 가상 프로시저 및 `DBCC` 실행기] (`Scope-053`)
* **원천 감사 코드**: `Scope-053` (심각도: `Medium`)
* **상세 시나리오**: JDBC 메타데이터 변환, 가상 프로시저 및 `DBCC` 실행기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-34` [Pratt 파서 표현식 파싱 및 연산자 결합 법칙] (`Scope-057`)
* **원천 감사 코드**: `Scope-057` (심각도: `Medium`)
* **상세 시나리오**: Pratt 파서 표현식 파싱 및 연산자 결합 법칙
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-35` [T-SQL 제어문 파싱 (`IF-ELSE`, `WHILE`, `TRY-CATCH`)] (`Scope-058`)
* **원천 감사 코드**: `Scope-058` (심각도: `Medium`)
* **상세 시나리오**: T-SQL 제어문 파싱 (`IF-ELSE`, `WHILE`, `TRY-CATCH`)
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-36` [프로시저 내 인라인 DDL/DML 파서] (`Scope-059`)
* **원천 감사 코드**: `Scope-059` (심각도: `Medium`)
* **상세 시나리오**: 프로시저 내 인라인 DDL/DML 파서
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-39` [컴파일러 최적화 패스 및 레지스터 수명주기 관리] (`Scope-062`)
* **원천 감사 코드**: `Scope-062` (심각도: `Medium`)
* **상세 시나리오**: 컴파일러 최적화 패스 및 레지스터 수명주기 관리
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-41` [프로시저 바이트코드 계획 캐시 및 상수 폴딩(Constant Folding)] (`Scope-065`)
* **원천 감사 코드**: `Scope-065` (심각도: `Medium`)
* **상세 시나리오**: 프로시저 바이트코드 계획 캐시 및 상수 폴딩(Constant Folding)
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-42` [VDBE 바이트코드 가상머신 인터프리터 코어 루프 및 PC 점프 제어] (`Scope-066`)
* **원천 감사 코드**: `Scope-066` (심각도: `Medium`)
* **상세 시나리오**: VDBE 바이트코드 가상머신 인터프리터 코어 루프 및 PC 점프 제어
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-43` [Opcode 디스패치 테이블 및 트랜잭션/상태머신 전이] (`Scope-068`)
* **원천 감사 코드**: `Scope-068` (심각도: `Medium`)
* **상세 시나리오**: Opcode 디스패치 테이블 및 트랜잭션/상태머신 전이
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-44` [문자열 내장 함수 (`SUBSTRING`, `CHARINDEX`, `LEN`, `REPLACE` 등)] (`Scope-070`)
* **원천 감사 코드**: `Scope-070` (심각도: `Medium`)
* **상세 시나리오**: 문자열 내장 함수 (`SUBSTRING`, `CHARINDEX`, `LEN`, `REPLACE` 등)
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-45` [수학 및 날짜/시간 내장 함수 (`DATEADD`, `DATEDIFF`, `ROUND`, `MOD` 등)] (`Scope-071`)
* **원천 감사 코드**: `Scope-071` (심각도: `Medium`)
* **상세 시나리오**: 수학 및 날짜/시간 내장 함수 (`DATEADD`, `DATEDIFF`, `ROUND`, `MOD` 등)
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-46` [T-SQL JSON 내장 함수 (`ISJSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_MODIFY`)] (`Scope-072`)
* **원천 감사 코드**: `Scope-072` (심각도: `Medium`)
* **상세 시나리오**: T-SQL JSON 내장 함수 (`ISJSON`, `JSON_VALUE`, `JSON_QUERY`, `JSON_MODIFY`)
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-47` [시스템 속성 및 메타데이터 함수 (`SERVERPROPERTY`, `OBJECT_ID` 등)] (`Scope-073`)
* **원천 감사 코드**: `Scope-073` (심각도: `Medium`)
* **상세 시나리오**: 시스템 속성 및 메타데이터 함수 (`SERVERPROPERTY`, `OBJECT_ID` 등)
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-48` [런타임 표현식 인터프리터 및 연산자 타입 강제 변환] (`Scope-074`)
* **원천 감사 코드**: `Scope-074` (심각도: `Medium`)
* **상세 시나리오**: 런타임 표현식 인터프리터 및 연산자 타입 강제 변환
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-49` [동적 SQL 실행기(`sp_executesql`) 및 쿼리 취소 연동] (`Scope-075`)
* **원천 감사 코드**: `Scope-075` (심각도: `Medium`)
* **상세 시나리오**: 동적 SQL 실행기(`sp_executesql`) 및 쿼리 취소 연동
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-52` [상관 서브쿼리 비상관화(Decorrelation) 규칙] (`Scope-078`)
* **원천 감사 코드**: `Scope-078` (심각도: `Medium`)
* **상세 시나리오**: 상관 서브쿼리 비상관화(Decorrelation) 규칙
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-53` [조건절 푸시다운(Predicate Pushdown) 및 인덱스 선택기] (`Scope-079`)
* **원천 감사 코드**: `Scope-079` (심각도: `Medium`)
* **상세 시나리오**: 조건절 푸시다운(Predicate Pushdown) 및 인덱스 선택기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-54` [집합 연산(UNION, INTERSECT, EXCEPT) 플랜 및 테이블 통계 연동] (`Scope-081`)
* **원천 감사 코드**: `Scope-081` (심각도: `Medium`)
* **상세 시나리오**: 집합 연산(UNION, INTERSECT, EXCEPT) 플랜 및 테이블 통계 연동
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-55` [`EXPLAIN` / `EXPLAIN ANALYZE` 플랜 트리 포맷터] (`Scope-082`)
* **원천 감사 코드**: `Scope-082` (심각도: `Medium`)
* **상세 시나리오**: `EXPLAIN` / `EXPLAIN ANALYZE` 플랜 트리 포맷터
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-57` [테이블/인덱스 스캔 반복자 및 프로젝션 수식 평가기] (`Scope-084`)
* **원천 감사 코드**: `Scope-084` (심각도: `Medium`)
* **상세 시나리오**: 테이블/인덱스 스캔 반복자 및 프로젝션 수식 평가기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-58` [행 필터 반복자 및 튜플 수식 평가기] (`Scope-085`)
* **원천 감사 코드**: `Scope-085` (심각도: `Medium`)
* **상세 시나리오**: 행 필터 반복자 및 튜플 수식 평가기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-59` [`COUNT`, `SUM`, `AVG`, `GROUP BY` 해시 집계 반복자] (`Scope-088`)
* **원천 감사 코드**: `Scope-088` (심각도: `Medium`)
* **상세 시나리오**: `COUNT`, `SUM`, `AVG`, `GROUP BY` 해시 집계 반복자
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-60` [UNION / EXCEPT 반복자 및 1,024-행 벡터화 청크 스트리머] (`Scope-091`)
* **원천 감사 코드**: `Scope-091` (심각도: `Medium`)
* **상세 시나리오**: UNION / EXCEPT 반복자 및 1,024-행 벡터화 청크 스트리머
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-61` [물리 실행 플랜 빌더 및 최상위 반복자 체인 인터페이스] (`Scope-092`)
* **원천 감사 코드**: `Scope-092` (심각도: `Medium`)
* **상세 시나리오**: 물리 실행 플랜 빌더 및 최상위 반복자 체인 인터페이스
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-62` [P99 지연시간 T-Digest 추정기 및 실행 프로파일러] (`Scope-093`)
* **원천 감사 코드**: `Scope-093` (심각도: `Medium`)
* **상세 시나리오**: P99 지연시간 T-Digest 추정기 및 실행 프로파일러
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-63` [OS 네이티브 메모리 압력 감지기 및 플랫폼별 물리 메모리 탐색기] (`Scope-094`)
* **원천 감사 코드**: `Scope-094` (심각도: `Medium`)
* **상세 시나리오**: OS 네이티브 메모리 압력 감지기 및 플랫폼별 물리 메모리 탐색기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-64` [글로벌 메모리 거버너 및 GOMEMLIMIT 동적 튜닝] (`Scope-095`)
* **원천 감사 코드**: `Scope-095` (심각도: `Medium`)
* **상세 시나리오**: 글로벌 메모리 거버너 및 GOMEMLIMIT 동적 튜닝
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-65` [PGWire v3 TCP 리스너 및 연결 수명주기 관리] (`Scope-096`)
* **원천 감사 코드**: `Scope-096` (심각도: `Medium`)
* **상세 시나리오**: PGWire v3 TCP 리스너 및 연결 수명주기 관리
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-66` [PGWire 메시지 인코딩/디코딩 및 패킷 버퍼 풀링] (`Scope-098`)
* **원천 감사 코드**: `Scope-098` (심각도: `Medium`)
* **상세 시나리오**: PGWire 메시지 인코딩/디코딩 및 패킷 버퍼 풀링
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-67` [PGWire 대량 COPY 프로토콜 및 Numeric BCD 디코더] (`Scope-099`)
* **원천 감사 코드**: `Scope-099` (심각도: `Medium`)
* **상세 시나리오**: PGWire 대량 COPY 프로토콜 및 Numeric BCD 디코더
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-68` [TDS SQL Batch 및 RPC 실행 파이프라인] (`Scope-102`)
* **원천 감사 코드**: `Scope-102` (심각도: `Medium`)
* **상세 시나리오**: TDS SQL Batch 및 RPC 실행 파이프라인
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-69` [TDS 토큰 인코더 (`COLMETADATA`, `ROW`, `DONE`) 및 패킷 청크 재조립] (`Scope-103`)
* **원천 감사 코드**: `Scope-103` (심각도: `Medium`)
* **상세 시나리오**: TDS 토큰 인코더 (`COLMETADATA`, `ROW`, `DONE`) 및 패킷 청크 재조립
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-70` [`sp_executesql`, `sp_prepare` 파라미터 RPC 파서 및 SQL 바인딩 빌더] (`Scope-105`)
* **원천 감사 코드**: `Scope-105` (심각도: `Medium`)
* **상세 시나리오**: `sp_executesql`, `sp_prepare` 파라미터 RPC 파서 및 SQL 바인딩 빌더
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-73` [Always On 자동 장애조치 및 쿼럼 선거] (`Scope-108`)
* **원천 감사 코드**: `Scope-108` (심각도: `Medium`)
* **상세 시나리오**: Always On 자동 장애조치 및 쿼럼 선거
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-74` [스냅샷 백업 및 전체 복원 파이프라인] (`Scope-109`)
* **원천 감사 코드**: `Scope-109` (심각도: `Medium`)
* **상세 시나리오**: 스냅샷 백업 및 전체 복원 파이프라인
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-76` [Transactional Outbox 및 실시간 CDC 스트리머] (`Scope-111`)
* **원천 감사 코드**: `Scope-111` (심각도: `Medium`)
* **상세 시나리오**: Transactional Outbox 및 실시간 CDC 스트리머
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-77` [CDC 이벤트 직렬화 및 외부 싱크 어댑터] (`Scope-112`)
* **원천 감사 코드**: `Scope-112` (심각도: `Medium`)
* **상세 시나리오**: CDC 이벤트 직렬화 및 외부 싱크 어댑터
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-78` [SQLLogicTest 내장 러너 및 스크립트 파서] (`Scope-113`)
* **원천 감사 코드**: `Scope-113` (심각도: `Medium`)
* **상세 시나리오**: SQLLogicTest 내장 러너 및 스크립트 파서
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-79` [괄호 균형 Context-Free 조건식 파서 및 토크나이저] (`Scope-114`)
* **원천 감사 코드**: `Scope-114` (심각도: `Medium`)
* **상세 시나리오**: 괄호 균형 Context-Free 조건식 파서 및 토크나이저
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-80` [고속 제로할당 LIKE 매처 및 $N 파라미터 변환기] (`Scope-115`)
* **원천 감사 코드**: `Scope-115` (심각도: `Medium`)
* **상세 시나리오**: 고속 제로할당 LIKE 매처 및 $N 파라미터 변환기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-81` [대소문자 무시 식별자 정규화, 다형성 비교기, MSSQL TOP/OFFSET 페이지네이션 변환기] (`Scope-116`)
* **원천 감사 코드**: `Scope-116` (심각도: `Medium`)
* **상세 시나리오**: 대소문자 무시 식별자 정규화, 다형성 비교기, MSSQL TOP/OFFSET 페이지네이션 변환기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-82` [슈퍼유저 인증, Salted SHA256 해시, JWT 서명/검증, API 키 발급 및 외부 AuthProvider SPI] (`Scope-117`)
* **원천 감사 코드**: `Scope-117` (심각도: `Medium`)
* **상세 시나리오**: 슈퍼유저 인증, Salted SHA256 해시, JWT 서명/검증, API 키 발급 및 외부 AuthProvider SPI
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-83` [T-SQL 스타일 역할 기반 접근 제어(RBAC), GRANT/REVOKE/DENY 권한 매트릭스] (`Scope-118`)
* **원천 감사 코드**: `Scope-118` (심각도: `Medium`)
* **상세 시나리오**: T-SQL 스타일 역할 기반 접근 제어(RBAC), GRANT/REVOKE/DENY 권한 매트릭스
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-84` [구성 설정 모델, 자동 탐색 우선순위 계층, 빌드 태그 상수, 프로덕션 보안 가드] (`Scope-119`)
* **원천 감사 코드**: `Scope-119` (심각도: `Medium`)
* **상세 시나리오**: 구성 설정 모델, 자동 탐색 우선순위 계층, 빌드 태그 상수, 프로덕션 보안 가드
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-85` [REST API HTTP 라우터, CORS/보안 미들웨어, Gzip 압축, 요청 로깅] (`Scope-120`)
* **원천 감사 코드**: `Scope-120` (심각도: `Medium`)
* **상세 시나리오**: REST API HTTP 라우터, CORS/보안 미들웨어, Gzip 압축, 요청 로깅
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-86` [HTTP 레코드 CRUD 엔드포인트 및 URL 필터 파서/평가기] (`Scope-121`)
* **원천 감사 코드**: `Scope-121` (심각도: `Medium`)
* **상세 시나리오**: HTTP 레코드 CRUD 엔드포인트 및 URL 필터 파서/평가기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-87` [테이블 스키마 관리, DDL DTO 매핑, CSV/JSON 데이터 임포트/익스포트] (`Scope-122`)
* **원천 감사 코드**: `Scope-122` (심각도: `Medium`)
* **상세 시나리오**: 테이블 스키마 관리, DDL DTO 매핑, CSV/JSON 데이터 임포트/익스포트
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-88` [인증 핸들러, SQL 즉시 실행, 쿼리 모니터링/취소, 백업/복원 REST 엔드포인트] (`Scope-123`)
* **원천 감사 코드**: `Scope-123` (심각도: `Medium`)
* **상세 시나리오**: 인증 핸들러, SQL 즉시 실행, 쿼리 모니터링/취소, 백업/복원 REST 엔드포인트
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-89` [행 단위 보안(RLS) 규칙 관리, `@auth.*` 토큰 치환 및 필터 합성] (`Scope-124`)
* **원천 감사 코드**: `Scope-124` (심각도: `Medium`)
* **상세 시나리오**: 행 단위 보안(RLS) 규칙 관리, `@auth.*` 토큰 치환 및 필터 합성
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-90` [저장 프로시저 등록, T-SQL 바이트코드 컴파일 트리거, REST 실행 엔드포인트] (`Scope-125`)
* **원천 감사 코드**: `Scope-125` (심각도: `Medium`)
* **상세 시나리오**: 저장 프로시저 등록, T-SQL 바이트코드 컴파일 트리거, REST 실행 엔드포인트
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-91` [백그라운드 크론 스케줄러, 표준 5필드 크론 표현식 검증, 비동기 작업 실행기] (`Scope-126`)
* **원천 감사 코드**: `Scope-126` (심각도: `Medium`)
* **상세 시나리오**: 백그라운드 크론 스케줄러, 표준 5필드 크론 표현식 검증, 비동기 작업 실행기
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑

#### `P2-92` [싱글 페이지 웹 애플리케이션(SPA) 정적 핸들러, 플랫 디렉토리 검증기, 임베디드 웹 스튜디오 UI] (`Scope-128`)
* **원천 감사 코드**: `Scope-128` (심각도: `Medium`)
* **상세 시나리오**: 싱글 페이지 웹 애플리케이션(SPA) 정적 핸들러, 플랫 디렉토리 검증기, 임베디드 웹 스튜디오 UI
* **v1.1.0 권장 완화안**: v1.1.0 정밀 단위 테스트 추가 및 안전 경계값 클램핑


---
## 5. LionSQL By-Design & Spec-Out 영구 아키텍처 불변 목록 (P3, 총 25건)
> - **처리 방침**: LionSQL 핵심 철학(경량 단일 바이너리, 서브마이크로초 레이턴시, Zero Sidecars)에 입각한 의도된 설계이므로 **코드 수정 일절 배제 (No Action)** 및 문서 보존
> - **참조 문서**: [`GEMINI.md`](../GEMINI.md) 3장 (Explicit Spec-Out Decisions), [`notes/ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md)

#### `P3-01` [PL/pgSQL 인터프리터 미지원]
* **설계 결정**: T-SQL VDBE VM으로 저장 프로시저 엔진을 단일 통합하여 단일 바이너리 경량화 유지.

#### `P3-02` [2PL 비관적 공유 읽기 락 미지원]
* **설계 결정**: Non-blocking OCC/MVCC 단일 채택으로 읽기가 쓰기를 막지 않는 초고속 동시성 달성.

#### `P3-03` [인메모리 OCC 백그라운드 Vacuum 스레드 미지원]
* **설계 결정**: 작업 영역 원자적 스왑/폐기로 데드 튜플 체인이 발생하지 않아 진공 청소 스레드 불필요(YAGNI).

#### `P3-04` [MySQL 전용 DML (ON DUPLICATE KEY UPDATE) 미지원]
* **설계 결정**: PostgreSQL ON CONFLICT 및 ANSI/MSSQL MERGE가 완비되어 DML 충돌 경로 삼원화 방지.

#### `P3-05` [POSIX 공유 메모리 (Arrow IPC) 스냅샷 미지원]
* **설계 결정**: OS 호환성 한계(/dev/shm 64MB 캡) 방지 및 WAL/CDC 표준 스트리밍 규격화.

#### `P3-06` [분산 2PC 코디네이터 (XA / MS DTC) 미지원]
* **설계 결정**: 단일 노드 스케일업 우선주의 및 20,000+ TPS 포화 집중, 2PC 지연 오버헤드 차단.

#### `P3-07` [커널 내장 OAuth2 웹 플로우 미지원]
* **설계 결정**: 아웃바운드 HTTP 호출 및 시크릿 유출 차단을 위해 클라이언트 사이드 토큰 교환으로 표준화.

#### `P3-08` [커널 내장 대용량 Blob 스토리지 및 이미지 리사이징 미지원]
* **설계 결정**: 서버 CPU/메모리 고갈 방지를 위해 S3 Pre-signed URL 위임 아키텍처 채택.

#### `P3-09` [미성숙 순수 Go 스토리지 엔진 (Pebble/bbolt) 미지원]
* **설계 결정**: 1.0은 검증된 WiredTiger MVCC 및 In-Memory OCC 2대 엔진에 100% 집중.

#### `P3-10` [커널 내장 실시간 슬로우 쿼리 웹훅 (OBS-3) 미지원]
* **설계 결정**: 엔진 내부 블로킹 HTTP 호출 배제, Prometheus (/metrics) 및 OTLP 표준화.

#### `P3-11` [커널 내장 Wasm UDF 샌드박스 (wazero) 미지원]
* **설계 결정**: 바이너리 비대화 및 JIT 오버헤드 방지, 무할당 Go 네이티브 UDF SPI 채택.

#### `P3-12` [복잡 쿼리 전면 바이트코드 변환 (ARCH-2TIER-1) 미지원]
* **설계 결정**: 복잡 관계대수는 Volcano CBO 엔진(Tier 2)으로 실행하는 2-Tier 최적 아키텍처 고수.

#### `P3-13` [순수 컬럼형 벡터화 엔진 전면 재작성 미지원]
* **설계 결정**: 극단적 복잡도 방지, 1,024-row 청크 스트리밍(ChunkedIterator)으로 고성능 달성.

#### `P3-14` [인메모리 Varint/비트 패킹 레코드 압축 미지원]
* **설계 결정**: 0.5ns sub-nanosecond O(1) 읽기 성능 유지를 위해 포인터 프리 플랫 레코드(Option A) 채택.

#### `P3-15` [DuckDB 스타일 편의 SQL (* EXCLUDE, FROM tbl;) 미지원]
* **설계 결정**: 오타 침묵 위험 및 RDBMS 엄격성 보존을 위해 배제 (LionSQL is NOT DuckDB 철학).

#### `P3-16` [MySQL 쉼표 LIMIT offset, count 문법 미지원]
* **설계 결정**: 표준 ANSI SQL LIMIT count OFFSET offset 단일 권장.

#### `P3-17` [MySQL 레거시 8-컬럼 평면 EXPLAIN 미지원]
* **설계 결정**: Volcano CBO 플랜 트리의 계층 구조 파괴 방지, 현대적 인덴트 텍스트 및 JSON 플랜 단일 표준화.

#### `P3-18` [READ UNCOMMITTED (Dirty Read) 격리 수준 미지원]
* **설계 결정**: 더티 리드로 인한 데이터 부정합 방지, Read Committed / Snapshot Isolation 단일 고수.

#### `P3-19` [레거시 XML DML / XQuery 미지원]
* **설계 결정**: 현대적 JSON 표준 함수(ISJSON, JSON_VALUE, JSON_MODIFY)로 일원화.

#### `P3-20` [클러스터 분산 멀티 마스터 샤딩 미지원]
* **설계 결정**: 단일 노드 스케일업 및 1-Writer Multi-Reader 비동기 복제 아키텍처 집중.

#### `P3-21` [TDS MARS (Multiple Active Result Sets) 동시 인터리빙 미지원]
* **설계 결정**: 패킷 상태 머신 데드락 위험 방지, 순차 독립 연결 스트리밍 권장.

#### `P3-22` [인라인 파생 테이블 임시 인덱스 강제 생성 미지원]
* **설계 결정**: 인메모리 해시 조인 및 스트리밍 처리로 충분하므로 무거운 임시 B-Tree 인덱스 배제.

#### `P3-23` [커널 내장 메일러 백그라운드 큐 미지원]
* **설계 결정**: DB 커널 복잡도 최소화 및 외부 워커/CDC 기반 아키텍처 권장.

#### `P3-24` [임의 사용자 정의 집계함수(UDAF) 동적 로딩 미지원]
* **설계 결정**: 동적 바이너리 로딩 보안 취약점 차단, 정적 네이티브 빌드 표준화.

#### `P3-25` [정규식 기반 중첩 SQL 절 파싱 영구 금지 (Invariant 7)]
* **설계 결정**: 정규식의 문맥 자유 문법(CFG) 파싱 불능으로 인한 데이터 오염 원천 차단.


---

## 6. 결론 및 거버넌스 원칙 (Governance & Invariants)

1. **1.0 GA 릴리스 스코프 락(Scope Lock)**: 상기 11개 과제는 1.0 코어에 일체 조기 투입되지 않으며, 1.0은 장기 내구성(Crash/Soak) 검증 완료 후 안정적으로 출시됩니다.
2. **골든 룰 준수**: 모든 향후 구현은 `[RULE:PipelineVerification]` (Build ➔ Test `-race` ➔ Commit), `[RULE:ModularAbstraction]` (Rule of Two), `[RULE:RadicalKISS]`를 철저히 준수합니다.
3. **불변식 수호**: 정규식을 통한 SQL 파싱 금지(`Invariant 7`), 단일 바이너리 지향(`Invariant 4`), 불필요한 분산 2PC 배제(`Invariant 5`) 원칙은 본 로드맵 전반에 걸쳐 영구 불변으로 유지됩니다.
