# LionSQL Post-1.0 기술 과제 파킹랏 (`notes/POST_1.0_PARKING_LOT.ko.md`)

> - **최초 작성일**: 2026-11-04  
> - **문서 성격**: **Architecture Parking Lot (언젠가 필요해지면 열어보는 온디맨드 기술 저장소)**  
> - **핵심 원칙**: 본 문서는 의무적으로 완수해야 하는 개발 로드맵이 아니며, 이미 심층 설계와 스펙 분석이 완료되어 안전하게 주차(Parking)해 둔 기술 자산 목록입니다. 향후 특정 비즈니스 요구나 극단적인 하드웨어 스케일링 니즈가 실제로 발생할 때에만 온디맨드(On-Demand)로 시동을 걸어 착수합니다.  
> - **연계 문서**:  
>   - 액티브 엔지니어링 대시보드: [`TODO.md`](../TODO.md)  
>   - 전사 전략 로드맵: [`ROADMAP.md`](../ROADMAP.md)  
>   - 아키텍처 불변 원칙: [`notes/ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md)  
>   - 과제 식별자 분류체계: [`notes/TAXONOMY.ko.md`](TAXONOMY.ko.md)  

---

## 1. 개요 및 목적 (Context & Objectives)

LionSQL은 v0.90.1을 거치며 **1.0 GA 릴리스에 필요한 모든 핵심 엔진 기능(이중 스토리지 엔진, 듀얼 와이어 프로토콜, 2-Tier CBO/VDBE 쿼리 엔진, T-SQL 저장 프로시저 런타임, 정규식 제로 불변식 등)의 구현 및 검증을 100% 완료**하였습니다.

1.0 이전의 잔여 작업은 기능 개발이 아닌 **초장기 운영 검증 게이트(`DURA-KILL-1` 1,000 라운드 사살 복구, `DURA-SOAK-1` 72시간 무중단 부하, 앱 섀도 모드 도그푸딩)**에 국한됩니다.

본 문서는 의무적인 개발 부채가 아닌 **"언젠가 필요해지면 열어볼 서랍 속의 완성도 높은 설계 자산"**으로서, 11개 과제의 기술적 난이도, 시스템 파급 영향도, 핵심 우려사항(Risks & Caveats), 그리고 필요 시 착수할 수 있는 선택적 가이드를 보존하는 데 목적이 있습니다.

---

## 2. 난이도·영향도·우려사항 종합 매트릭스 (Summary Matrix)

| 번호 | 과제 코드 | 과제명 | 구현 난이도 | 시스템 영향도 | ROI (효익) | 핵심 우려사항 (Risks & Caveats) | 추천 추진 페이즈 |
| :---: | :--- | :--- | :---: | :---: | :---: | :--- | :---: |
| **1** | **`FRIENDLY-SQL`** | DuckDB 스타일 편의 SQL (`* EXCLUDE`, `FROM tbl;` 등) | **낮음 (★☆☆)** | **국소 (Low)** | **매우 높음** | 문법 모호성(키워드/식별자 충돌) 및 ANSI 표준 호환성 훼손 우려 | **Phase 1 (Quick Win)** |
| **2** | **`WEB-SPA-1`** | 단일 바이너리 내장 SPA 웹 서버 (`--app`, `--public`) | **낮음 (★☆☆)** | **국소 (Low)** | **높음** | 디렉토리 트래버설(`../`) 경로 보안 탈출 및 정적 파일 I/O 메모리 누수 | **Phase 1 (Zero Risk)** |
| **3** | **`PERF-SERVER-PIPELINE`**| Generic LRU 커서 & ReadOnly 트랜잭션 자동 바이패스 | **중간 (★★☆)** | **중간 (Med)** | **높음** | 쓰기 함수/부수효과 오탐으로 인한 일관성 위반, LRU 락 경합 병목 | **Phase 2 (Throughput)** |
| **4** | **`AUDIT-LOG-1`** | SHA-256 체이닝 불변 감사 로그 서브시스템 (SOC 2) | **중간 (★★☆)** | **중간 (Med)** | **중간** | 동기 I/O 지연으로 인한 TPS 급락, 감사 로그 디스크 공간 고갈 | **Phase 2 (Enterprise)** |
| **5** | **`INTEG-NOCODB/MATTERMOST`** | NocoDB & Mattermost 실전 엔터프라이즈 앱 연동 검증 | **중간 (★★☆)** | **국소~중간** | **높음** | 앱 전용 비표준 복합 쿼리로 인한 가상 카탈로그 규칙 복잡화 | **Phase 2 (Ecosystem)** |
| **6** | **`S3-1~4`** | 클라이언트 사이드 S3 Pre-signed URL 및 SigV4 서명기 | **중간 (★★☆)** | **독립적 (Low)** | **중간** | 서버-클라이언트 간 시계 오차(Clock Skew)로 인한 서명 만료 오류 | **Phase 2 (Utility)** |
| **7** | **`BENCH-TPCH-1`** | CBO 서브쿼리 Decorrelation (`IN`/`EXISTS` ➔ Semi-Join) | **높음 (★★★)** | **높음 (High)** | **매우 높음** | `NULL` 3치 논리(3VL) 정합성 깨짐 및 서브쿼리 카디널리티 오산출로 인한 잘못된 플랜 | **Phase 3 (Optimizer)** |
| **8** | **`PERF-COMPLEX-QUERY-2`**| Open-Addressing 정수 플랫 해시 조인 (Flat Hash Join) | **높음 (★★★)** | **중간 (Engine)** | **높음** | 고부하 해시 클러스터링/리사이징 시 순간 지연 및 메모리 스파이크 | **Phase 3 (Analytical)** |
| **9** | **`OCC-HYPER-2`** | In-Memory OCC Pointer-Free Flat Record (4~5x 메모리 압축)| **매우 높음 (★★★★)**| **광범위 (Critical)**| **극상 (Extreme)** | 엔진 전반 레이아웃 대수술로 인한 회귀 버그, 가변 길이 문자열 갱신 단편화 | **Phase 4 (Deep Kernel)** |
| **10**| **`LOC-1~4`** | MSSQL Collation 및 유니코드 다국어 로케일 정렬/비교 | **높음 (★★★)** | **광범위 (High)** | **중간** | 로케일 키 인코딩 오버헤드로 인한 순수 ASCII 문자열 비교 성능 저하 | **Phase 4 (Specialized)**|
| **11**| **`TDS-COMPAT-6`** | TDS 7.x/8.0 MARS (Multiple Active Result Sets) 스트리밍 | **매우 높음 (★★★★)**| **광범위 (Critical)**| **낮음~중간** | 복잡한 패킷 헤더 상태 머신으로 인한 교착 상태(Deadlock) 및 가치 대비 과도한 비용 | **Phase 5 (Deprioritized)**|

---

## 3. 과제별 심층 분석 및 세부 우려사항 (Deep-Dive Analysis & Caveats)

---

### 도메인 A. 개발자 경험 및 편의성 (Ergonomics & Standalone Ecosystem)

#### 1. `FRIENDLY-SQL`: DuckDB 스타일 편의 SQL 문법 지원
- **상세 설계서**: [`notes/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md`](DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md)
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
    - 어떤 제어 방식을 택하든(Default On, Strict Mode, Dialect 분기) 뚜렷한 장단점과 설계적 결함이 공존하므로, **명확한 설계적 확신과 실제 사용자 요구가 수렴될 때까지 Parking Lot에 둔 채 성급한 구현을 영구 보류**함.

#### 2. `WEB-SPA-1`: 단일 바이너리 내장 SPA 웹 서버 호스팅
- **상세 설계서**: [`notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md)
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

---

### 도메인 B. 처리량 및 엔터프라이즈 보안 (Pipeline & Enterprise Governance)

#### 3. `PERF-SERVER-PIPELINE-P3-P4`: 서버 파이프라인 Phase 3/4
- **상세 설계서**: [`notes/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`](SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md)
- **주요 구현 내용**:
  1. Generic LRU 커서/Prepared Statement 캐시 도입.
  2. ReadOnly 쿼리에 대해 트랜잭션 락/OCC 스냅샷 생성 비용을 면제하는 자동 바이패스 경로 구축.
  3. 고동시성 쓰기 트래픽 감지 시 적응형 그룹 커밋(Adaptive Group Commit).
- **난이도 및 영향도**:
  - **난이도**: **중간 (★★☆)** — 캐시 동시성 제어 및 ReadOnly 감지 로직.
  - **영향도**: **중간 (Med)** — `pkg/wire`와 `pkg/engine/coordinator_impl.go`의 쿼리 수신 핫패스에 개입.
- **주요 우려사항 (Key Caveats)**:
  - **ReadOnly 오탐으로 인한 데이터 불일치**: 쿼리 내부에 `NEXTVAL`, 임시 테이블 생성, 부수효과를 가진 사용자 정의 함수(UDF)가 포함되어 있음에도 ReadOnly로 오판하여 격리성 위반을 초래할 위험.
  - **LRU 캐시 락 경합(Cache Contention)**: 수백 개의 워커가 단일 LRU 뮤텍스에 몰릴 경우 락 경합으로 인해 오히려 TPS가 하락하는 역효과 (샤딩된 캐시 또는 Lock-Free 슬롯 필요).

#### 4. `AUDIT-LOG-1`: SHA-256 불변 감사 로그 서브시스템
- **상세 설계서**: [`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](IMMUTABLE_AUDIT_LOG_PLAN.md)
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
- **상세 설계서**: [`notes/S3_PRESIGNED_STORAGE_PLAN.md`](S3_PRESIGNED_STORAGE_PLAN.md)
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
- **상세 설계서**: [`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)
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
- **상세 설계서**: [`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)
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
- **상세 설계서**: [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)
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
  - **반드시 1.0 릴리스 이후 별도 격리 브랜치에서 고립 개발 필수**.

#### 10. `LOC-1` ~ `LOC-4`: MSSQL Collation 및 유니코드 다국어 로케일
- **상세 설계서**: [`notes/COLLATION_AND_LOCALE_PLAN.md`](COLLATION_AND_LOCALE_PLAN.md)
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
  ├── 1. FRIENDLY-SQL (DuckDB 스타일 편의 SQL 문법)
  └── 2. WEB-SPA-1 (단일 바이너리 내장 SPA 웹 서버)

[Phase 2: Throughput & Enterprise Extensibility] (처리량 확장 및 보안 거버넌스)
  ├── 3. PERF-SERVER-PIPELINE (Generic LRU & ReadOnly Auto-Bypass)
  ├── 4. AUDIT-LOG-1 (SHA-256 체이닝 불변 감사 로그)
  ├── 5. INTEG-NOCODB / MATTERMOST (실전 앱 연동 확장)
  └── 6. S3-1~4 (클라이언트 사이드 S3 Pre-signed URL)

[Phase 3: Deep Query Optimization] (대규모 복합 분석 쿼리 가속)
  ├── 7. BENCH-TPCH-1 (CBO 서브쿼리 Decorrelation)
  └── 8. PERF-COMPLEX-QUERY-2 (Open-Addressing 플랫 해시 조인)

[Phase 4: Core Memory Revolution] (스토리지 및 메모리 아키텍처 대수술)
  ├── 9. OCC-HYPER-2 (Pointer-Free Flat Record & Zero-GC)
  └── 10. LOC-1~4 (MSSQL Collation & 다국어 로케일)

[Phase 5: Deprioritized Niche Protocol] (필요 시 선택적 착수)
  └── 11. TDS-COMPAT-6 (TDS MARS 인터리빙 스트리밍)
```

---

## 5. 결론 및 거버넌스 원칙 (Governance & Invariants)

1. **1.0 GA 릴리스 스코프 락(Scope Lock)**: 상기 11개 과제는 1.0 코어에 일체 조기 투입되지 않으며, 1.0은 장기 내구성(Crash/Soak) 검증 완료 후 안정적으로 출시됩니다.
2. **골든 룰 준수**: 모든 향후 구현은 `[RULE:PipelineVerification]` (Build ➔ Test `-race` ➔ Commit), `[RULE:ModularAbstraction]` (Rule of Two), `[RULE:RadicalKISS]`를 철저히 준수합니다.
3. **불변식 수호**: 정규식을 통한 SQL 파싱 금지(`Invariant 7`), 단일 바이너리 지향(`Invariant 4`), 불필요한 분산 2PC 배제(`Invariant 5`) 원칙은 본 로드맵 전반에 걸쳐 영구 불변으로 유지됩니다.
