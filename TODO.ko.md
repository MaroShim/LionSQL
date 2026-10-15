# SQLion Engineering TODO & Backlog

이 문서는 SQLion의 단기 엔지니어링 우선순위 과제, 도메인별 기능 백로그, 최근 완료된 마일스톤 이력을 관리하는 공식 엔지니어링 백로그입니다. 중장기 아키텍처 로드맵은 [ROADMAP.md](ROADMAP.md)를 참조하세요.

---

## 식별 코드 체계 범례 (Taxonomy Legend)

| 접두사 / 코드 | 트랙 명칭 (도메인) | 번호 부여 규칙 및 범위 | 설명 |
| :---: | :--- | :---: | :--- |
| **`P1` ~ `P3`** | **우선순위 (Priority)** | `P1` ~ `P3` | 개발 착수 긴급도 (P1: 즉시 착수/완료, P2: 차기 착수, P3: 생태계 검증) |
| **`P-1` ~ `P-5`** | **엔진 성능 (Performance)** | `P-1` ~ `P-5` | 스토리지, 디코딩, 와이어, 캐시 전반의 5대 핵심 성능 극대화 트랙 |
| **`A` ~ `D`** | **CBO 최적화 (Optimizer)** | `A-1` ~ `D-9` | 비용 기반 옵티마이저 4대 기능 트랙 (A: 통계 수집, B: 조인 알고리즘, C: 선택도, D: 운영 제어) |
| **`S-1` ~ `S-7`** | **SQL 문법 (Syntax)** | `S-1` ~ `S-7` | T-SQL 및 ANSI SQL 7대 문법 호환성 완비 트랙 (전 항목 완료) |

> [!NOTE]
> 전체 식별 코드 일람 및 상세 명세는 [notes/TAXONOMY.md](notes/TAXONOMY.md)를 참조하세요.

---

## 1. 차기 착수 우선순위 과제 (Next High Priorities)

| 우선순위 | 영역 | 핵심 과제 | 대상 위치 | 기대 효과 및 구현 요약 |
| :---: | :---: | :--- | :--- | :--- |
| **P1** | **스토리지/와이어** | **스토리지 ➔ 와이어 제로카피 직접 직렬화 (`P-3`)** | `pkg/storage`, `pkg/wire` | 중간 `[]any` 레코드 박싱 제거, TCP syscall 횟수 대폭 감소 |
| **P2** | **CBO 최적화** | **다중 컬럼 확장 통계 (Extended Stats) (`C-5`)** | `pkg/planner` | 컬럼 상관관계(예: 국가-도시)에 따른 카디널리티 과소평가 방지 |
| **P2** | **스토리지/보안** | **투명한 데이터 암호화 (TDE: AES-256-GCM) & 감사 로그** | `pkg/storage` | 데이터 파일 암호화 및 불변 감사 추적 로그 완비 |
| **P3** | **BaaS 확장** | **선언적 스키마 마이그레이션 CLI (`sqlion migrate`)** | `cmd/server`, `pkg/migrate` | `migrate new / up / down` 및 `_migrations` 이력 추적 |

### P1/P2 최근 완료 과제: 엔진 성능 & 최적화
- [x] **T-SQL 핵심 시스템 내장 함수 및 IDENTITY 추적 완비 (`pkg/proc`, `pkg/engine`)**
  - **배경**: SQL Server 애플리케이션 및 ORM(Dapper, EF Core) 호환성을 위한 핵심 시스템 스칼라 함수 및 ID 생성 추적 기능 제공.
  - **구현 내용**:
    - `ISNUMERIC`, `CHARINDEX`, `PATINDEX`, `STUFF`, `IDENT_CURRENT`, `COLUMNPROPERTY`(IsIdentity, AllowsNull, Precision 등), `SCOPE_IDENTITY` 함수 구현.
    - `Coordinator` 테이블별 마지막 발번 ID 추적(`SetTableLastIdentity`/`GetTableLastIdentity`).
    - 세션 변수 치환 시 단일 `@` 사용자 변수가 시스템 `@@IDENTITY`와 충돌하는 토큰 결함 수정.
- [x] **TDS PLP (Partially Length-Prefixed) LOB 청크 스트리밍 (`VARCHAR(MAX)`, `VARBINARY(MAX)`) (`pkg/wire/tds`)**
  - **배경**: TDS 프로토콜에서 8,000 바이트를 초과하는 대용량 LOB 데이터 전송 시 단일 패킷 길이를 초과하는 페이로드 전송 표준 규격 준수.
  - **구현 내용**:
    - [MS-TDS] 2.2.5.2 규격 기반 PLP(Partially Length-Prefixed) 청크 인코딩 엔진 구현 (`writePLPValue`).
    - `ColMetaData`에서 비제한 LOB 타입(`VARCHAR(MAX)`, `NVARCHAR(MAX)`, `VARBINARY(MAX)`)에 대해 `0xFFFF` (65535) 길이 지시자 지정.
    - 8바이트 전체 길이 헤더, 최대 8,000바이트 단위 청크 분할 전송, 4바이트 제로 종료 청크(`0x00000000`) 스트리밍.
    - PLP 8바이트 `0xFFFFFFFFFFFFFFFF` NULL 표현식 완비.
    - Go 공식 `sqlserver` 드라이버(12,000바이트) 및 Python `pymssql` 드라이버(10,000바이트) E2E 검증 완료.
- [x] **외부 언어 드라이버 E2E 통합 검증 하네스 (`pkg/wire`, `pkg/wire/tds`)**
  - **배경**: Go 공식 드라이버(`go-mssqldb`, `lib/pq`, `pgx/v5`)에 이어 엔터프라이즈 멀티 언어 클라이언트 검증.
  - **구현 내용**:
    - `pkg/wire/driver_external_test.go`: Python `psycopg3` (PGWire :5433) 연결, DDL/DML, NULL 값 처리, 명시적 트랜잭션(COMMIT/ROLLBACK) E2E 검증.
    - `pkg/wire/tds/driver_external_test.go`: Python `pymssql` (TDS :1433 / FreeTDS dblib) 연결, DDL/DML, DATE/TIME/GUID 바이너리 파싱, 트랜잭션 원자성 E2E 검증.
    - FreeTDS/pymssql 클라이언트 호환성: 일반 DDL 실행 시 unsolicited INFO 토큰 예외 방지 가드(`server.go` PRINT 전용 제한) 적용.
    - `uv run` 기반 격리형 실행 환경 및 미설치 환경 자동 스킵(`exec.LookPath`) 연동으로 100% 무결점 밀폐형 테스트(Golden Rule 17) 달성.
- [x] **볼케이노 벡터화 청크 반복자 (Chunked Batch Next: 64~1024건) (`P-4`) (`pkg/execution`, `pkg/engine`)**
  - **배경**: 행 단위 `Next()` 가상 함수 호출 및 `ctx.Done()` 검사 오버헤드로 인한 CPU 캐시 미스 해소.
  - **구현 내용**:
    - `pkg/execution/chunk.go`: `Chunk` 구조체(1,024건 프리알로케이션), `ChunkedIterator` 인터페이스, `PullChunk` 자동 폴백 엔진 신설.
    - `SeqScan`, `IndexScan`, `Filter`, `Projection`, `Limit`, `Aggregate`, `Sort`, `HashJoin`, `IndexNestedLoopJoin`, `sqlexec_select` 전반에 배치 파이프라인 관통.
    - 기존 단건 `Next()` 호출에 대해 내부 청크 버퍼링을 통한 100% 투명한 하위 호환성 보장.
- [x] **바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 커버링 인덱스 (`P-2`) (`pkg/storage`, `pkg/execution`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**
  - **배경**: 풀 스캔 및 인덱스 스캔 시 불필요한 모든 컬럼의 Varint/문자열 디코딩 힙 할당을 방지하고, 메인 테이블 조회를 생략.
  - **구현 내용**:
    - `pkg/storage/tuple.go`: 제로 할당 `SkipTupleColumn`, $O(1)$ 오프셋 캐싱 `LazyTuple`, 선별 디코딩 `DecodeTupleSelected` 구현.
    - `CREATE INDEX ... INCLUDE(...)` 문법 파싱, 카탈로그 및 `SkipList` / `IndexData` 비키 페이로드 적재 연동.
    - CBO 플래너 `isCovering` 및 `NodeIndexOnlyScan` 연동으로 테이블 힙 조회 100% 생략.
- [x] **CBO 옵티마이저 힌트 오버라이드 제어 (`D-9`) (`pkg/planner`, `pkg/proc`)**
  - 테이블 힌트(`WITH (INDEX(...))`) 및 쿼리 힌트(`OPTION (HASH/MERGE/LOOP JOIN, FORCE ORDER)`) 파싱 및 CBO 강제 오버라이드.
- [x] **소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델 (`B-3`) (`pkg/execution`, `pkg/planner`)**
  - 정렬 입력에 대한 $O(M+N)$ 해시리스 스트리밍 반복자 `MergeJoinIterator` 신설 및 CBO 비용 경쟁 모델 연동.
- [x] **단순 PK 조회 고속 지문 해싱 & 바이패스 캐시 (`P-5`) (`pkg/engine`, `pkg/storage`)**
  - FNV-1a 64비트 지문 해싱 기반 Fast-Path 템플릿 탐지, 정규 파서/CBO/VDBE 스택 우회 및 스토리지(`tx.Get`) 직결.
- [x] **TDS 프로토콜 고도화: NBCROW 행 압축 (`0xD2`) (`pkg/wire/tds`)**
  - TDS 7.3+ 표준 `BuildNBCRow` 신설, NULL 비트맵 패킹 및 0바이트 전송을 통한 네트워크 오버헤드 30%+ 절감.
- [x] **SQL Server 2008 / TDS 7.3 DATE, TIME, DATETIME2, DATETIMEOFFSET 및 UNIQUEIDENTIFIER 바이너리 직렬화 (`pkg/wire/tds`)**
  - `DATE`(0x28, 0001-01-01 기준 일수 3바이트 LE), `TIME`(0x29, Scale 0..7 100ns 틱 3~5바이트), `DATETIME2`(0x2A, Scale 0..7 틱 + 그레고리력 3바이트), `DATETIMEOFFSET`(0x2B, UTC DATETIME2 + 2바이트 분 오프셋), `UNIQUEIDENTIFIER`(0x24, Windows Mixed-Endian 바이트 스왑) 전 항목 완비.
  - 결과셋 컬럼 타입 자동 추론(`inferColumnInfos`) 고도화 및 공식 `github.com/microsoft/go-mssqldb`(`mssql.UniqueIdentifier`, `time.Time`) 통합 검증 완료.

---

## 2. 도메인별 대기 백로그 (Domain-Specific Backlogs)

### 2.1. CBO & 쿼리 최적화 (CBO & Query Engine)

| 번호 | 과제명 | 상태 | 설명 |
| :---: | :--- | :---: | :--- |
| **A-1** | **`ANALYZE TABLE` 멀티코어 병렬 통계 수집** | **완료 (`[x]`)** | 컬럼별 정렬 및 히스토그램 수집을 `sync.WaitGroup` 병렬화 완료 |
| **A-2** | **CBO 통계 메타데이터 디스크 영속화** | **완료 (`[x]`)** | `cbo_stats.json` 원자적 파일 쓰기 및 기동 시 자동 복원 연동 완료 |
| **C-6** | **OR 조건 및 불리언 트리 선택도 계산** | **완료 (`[x]`)** | AST 재귀 평가, 배타합/독립합집합, NOT, IN, BETWEEN 정밀 산출 완료 |
| **D-9** | **CBO 옵티마이저 힌트 오버라이드 제어** | **완료 (`[x]`)** | T-SQL 조인 힌트(`HASH/LOOP/MERGE`) 및 인덱스 힌트(`WITH (INDEX(...))`) 강제 반영 |
| **B-3** | **소트-머지 조인 (Sort-Merge Join) 연산자** | **완료 (`[x]`)** | 정렬된 입력에 대해 $O(M+N)$ 해시리스 스트리밍 조인(`MergeJoinIterator`) |
| **B-4** | **$N > 4$ 체인 조인 동적 계획법 (DP)** | **대기 (`[ ]`)** | 5개 이상 테이블 조인 시 Left-Deep DP 및 Bushy Tree 동적 탐색 |
| **C-5** | **다중 컬럼 확장 통계 (Extended Stats)** | **대기 (`[ ]`)** | 컬럼 상관관계로 인한 카디널리티 과소평가 방지 |
| **C-7** | **MCV (Most Common Values) 최빈값 목록 결합** | **대기 (`[ ]`)** | 왜곡 데이터 최빈값 분리 처리 |
| **D-8** | **백그라운드 자동 통계 갱신 데몬** | **완료 (`[x]`)** | DML 변동량(Dirty Tuples) 기반 자동 `ANALYZE` 트리거 |

- [ ] **동적 계획법(DP / Selinger) 기반 대규모 조인 순서 최적화 ($N > 4$) (`pkg/planner`)**
  - 4개 이하의 체인 조인 전수 순열 탐색을 넘어, $N > 4$ 테이블 조인에 대한 Left-Deep DP 및 Bushy Tree 동적 계획법(DPccp) 탐색 지원.
- [ ] **다중 컬럼 확장 통계 (Extended / Multi-Column Statistics) (`pkg/planner`)**
  - 컬럼 간 상관관계(예: 국가-도시)로 인한 복합 조건 카디널리티 과소평가(Underestimation)를 방지하는 다중 컬럼 NDV 수집.
- [ ] **MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합 (`pkg/planner`)**
  - 왜곡(Skew)이 심한 상위 N개 최빈값은 MCV 목록으로 보관하고 나머지 균등 분포는 히스토그램으로 이원화 처리.
- [x] **백그라운드 자동 통계 갱신 데몬 (Auto-Analyze Daemon) (`D-8`) (`pkg/engine`, `pkg/catalog`, `pkg/storage`)**
  - 테이블 DML 변경 비율(동적 임계치: `min_threshold + base_rows * scale_factor`)을 추적하여 백그라운드 비동기로 `coordinator.AnalyzeTable` 자동 트리거.
  - 트랜잭션 커밋 핫패스에 락 경합 없이 원자적 카운팅(`AddWALHook`) 및 디바운싱 완비.

### 2.2. 스토리지 & 엔진 성능 (Storage & Engine Performance)

| 단계 | 전략 분야 | 핵심 구현 과제 | 상태 | 예상 효과 및 ROI |
| :---: | :--- | :--- | :---: | :--- |
| **P-1** | **스토리지/트랜잭션** | **배치형 그룹 커밋 & 무경합 Fast-Path** | **완료 (`[x]`)** | `FsyncAlways` 100배 지연 단축(0.038ms/op), TPC-C 2.8K TPS |
| **P-2** | **스토리지/디코딩** | **바이너리 튜플 지연 역직렬화 & 커버링 인덱스** | **완료 (`[x]`)** | 스캔 속도 2배 향상, 불필요한 Varint 디코딩 힙 할당 제거 |
| **P-3** | **와이어 프로토콜** | **제로카피 와이어 인코딩 & I/O 버퍼 결합** | **대기 (`[ ]`)** | 중간 `[]any` 레코드 박싱 제거, TCP syscall 횟수 대폭 감소 |
| **P-4** | **실행 엔진** | **벡터화 청크 반복자 (Chunked Batch Next)** | **완료 (`[x]`)** | 볼케이노 가상 디스패치 및 컨텍스트 취소 오버헤드 99% 삭감, 1024 튜플 배치 스트리밍 |
| **P-5** | **캐시 및 메모리** | **고속 SQL 지문 해싱 & 단순 PK 조회 바이패스** | **완료 (`[x]`)** | 단순 PK 조회 파서 바이패스, TPS 50%+ 향상, GC 부하 90% 경감 |

- [x] **바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 투영 푸시다운 (`pkg/storage`, `pkg/execution`)**
  - 풀 스캔 시 WHERE 절 조건 컬럼만 선별 디코딩하고, 조건을 통과한 행에 한해서만 나머지 투영 컬럼을 언패킹하여 CPU 및 힙 할당 70% 삭감.
- [x] **커버링 인덱스 비키 컬럼 문법 지원: `CREATE INDEX ... INCLUDE (col...)` (`pkg/storage`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**
  - B+Tree/SkipList 리프 노드에 비키 컬럼을 페이로드로 적재하여 메인 테이블 힙 조회를 100% 생략하는 Index-Only Scan 극대화.
- [ ] **스토리지 ➔ 와이어 제로카피 직접 직렬화 (`pkg/storage`, `pkg/wire`)**
  - 중간 `[]any` 레코드 슬라이스 변환을 제거하고 `sync.Pool` 와이어 버퍼에 바이너리 튜플 데이터를 직접 포맷팅.
- [x] **Volcano 청크 단위 배치 실행 (Vectorized Chunk Iterator: 64~1024건) (`pkg/execution`, `pkg/engine`)**
  - 행 단위 `Next()` 가상 함수 호출 및 `ctx.Done()` 검사 오버헤드를 줄이기 위해 1,024개 튜플 벡터 단위 슬라이스 배치 스트리밍(`ChunkedIterator`, `NextChunk`).
  - `SeqScan`, `IndexScan`, `Filter`, `Projection`, `Limit`, `Aggregate`, `Sort`, `HashJoin`, `IndexNestedLoopJoin`, `sqlexec_select` 전반에 배치 파이프라인 관통 및 기존 단건 `Next()` 투명 버퍼링 100% 하위 호환성 보장.
- [ ] **세션/쿼리 전용 아레나 메모리 할당기 (Arena Allocator) (`pkg/execution`, `pkg/storage`)**
  - 해시 조인, 소트 버퍼에 대해 쿼리 수명주기에 묶인 아레나 할당기를 적용하여 Go GC 압박 원천 제거.

### 2.3. 와이어 프로토콜 & 생태계 (Wire Protocols & Ecosystem)
- [x] **T-SQL 특수 타입 직렬화 (TDS 7.3 DATE, TIME, DATETIME2, DATETIMEOFFSET, UNIQUEIDENTIFIER) (`pkg/wire/tds`)**
  - `UNIQUEIDENTIFIER` (UUID 16바이트 Windows GUID 바이트 스왑), `DATE` (`0x28`, 3바이트 LE), `TIME` (`0x29`, 100ns 틱 3~5바이트), `DATETIME2` (`0x2A`, 틱 + 3바이트 날짜), `DATETIMEOFFSET` (`0x2B`, UTC DATETIME2 + 2바이트 분 오프셋) TDS 바이너리 인코딩 완비.
  - 결과셋 컬럼 타입 자동 추론(`inferColumnInfos`) 고도화 및 `go-mssqldb` 공식 드라이버 통합 검증 완료.
- [ ] **PLP LOB 청크 스트리밍 (`pkg/wire/tds`)**
  - PLP (Partially Length-Prefixed) 기반 8KB 초과 `VARCHAR(MAX)` / `VARBINARY(MAX)` 대용량 청크 스트리밍.
- [x] **외부 언어 드라이버 E2E 통합 검증 하네스 (`pkg/wire`, `pkg/wire/tds`)**
  - Python `psycopg3` (PostgreSQL Wire Protocol :5433) 및 `pymssql` (TDS Protocol :1433) 종단간 통합 검증 완료.
  - `CREATE TABLE`, `INSERT`, `SELECT`, DATE/TIME/GUID 바이너리 파싱, 트랜잭션 `COMMIT`/`ROLLBACK` 원자성 및 NULL 값 처리 완비.
- [x] **SSMS (SQL Server Management Studio) Object Explorer 가상 카탈로그/DMV 뷰 (`pkg/engine`, `pkg/catalog`)**
  - SSMS 접속 및 탐색기 트리 조회에 필수적인 `sys.configurations`, `sys.dm_exec_sessions`, `sys.dm_exec_connections`, `sys.database_principals`, `sys.server_principals`, `sys.dm_os_sys_info`, `sys.endpoints`, `sys.database_files`, `sys.master_files`, `sys.all_objects`, `sys.all_columns`, `sys.all_views` 가상 뷰 완비.
- [ ] **PostgreSQL 와이어 프로토콜 서버 사이드 커서 (`pkg/wire`)**
  - 대규모 쿼리 결과 집합을 분할 수신하는 드라이버 호환성을 위한 `DECLARE ... CURSOR FOR` 및 `FETCH n` 서버 사이드 커서 라이프사이클 지원.
- [ ] **임베디드 웹 스튜디오 HA 복제 모니터링 카드 (`pkg/ui`)**
  - 브라우저 웹 스튜디오(`:8090/_/`)에 고가용성(HA) 복제 상태(Primary vs Replica) 시각화 카드 추가.
  - 연결된 Replica 목록, LSN 랙(Lag) 지연 시간, 스트리밍 상태 표시 및 수동 승격(`Promote`) 버튼 제공.

### 2.4. 엔터프라이즈 보안 & 거버넌스 (Enterprise Security & Governance)
- [ ] **투명 데이터 암호화 (TDE: Transparent Data Encryption) (`pkg/storage`)**
  - 디스크에 기록되는 WiredTiger 데이터 파일, WAL/AOF 저널 및 백업 아카이브를 AES-256-GCM으로 투명 암호화(Data at Rest Encryption).
  - 마스터 암호화 키(MEK) 로테이션 및 환경 변수/KMS 키 주입 인터페이스.
- [ ] **불변 엔터프라이즈 감사 로그 (Enterprise Immutable Audit Log) (`pkg/logger`, `pkg/api`)**
  - 로그인 성공/실패, 권한 변경(`GRANT/REVOKE`), DDL 스키마 변경, 관리자 데이터 조작 이벤트를 태깅하여 위변조가 불가능한 불변 감사 파일로 안전 기록.
- [ ] **`INSTEAD OF` 트리거 런타임 인터셉터 (`pkg/proc/runtime`, `pkg/api`)**
  - 뷰(View) 및 복합 테이블에 대한 `INSERT/UPDATE/DELETE` 작업을 가로채서 사용자 정의 프로시저/배치로 치환하는 런타임 DML 인터셉터 완비.

### 2.5. 고가용성 & 분석 확장 (HA & Analytical Extensions)
- [ ] **Always On 스타일 자동 장애 조치 및 리더 선출 (Automatic Failover & Leader Election) (`pkg/replication`)**
  - Primary 노드 다운 시 복제 랙(LSN Lag)이 가장 적은 최신 Replica 노드를 새로운 Primary로 자동 승격 (Leader Promotion).
  - 클라이언트 라우팅 갱신 및 Split-Brain 방지를 위한 리더 텀(Term) 검증.
- [x] **분리형 실시간 CDC (Change Data Capture) 스트리밍 프레임워크 (`pkg/cdc`, `pkg/api`, `cmd/server`)**
  - 트랜잭션 커밋과 100% 원자적으로 연동되는 Row-level 변경 델타(Inserted/Updated/Deleted) 이벤트 모델(`ChangeEvent`) 및 스트리머(`Streamer`) 구축.
  - 유계 링 버퍼(Bounded Ring Buffer, 10,000건) 기반 과거 LSN 재생, 비차단 팬아웃(Non-blocking Backpressure, Golden Rule 29), 테이블 필터링 지원.
  - `GET /api/cdc/stream` 실시간 NDJSON 및 SSE 스트리밍 HTTP 엔드포인트 완비.
  - 스토리지 엔진(`memengine`, `wiredtiger`) 커밋 훅 다중화(`AddWALHook`) 관통.
  - Python 표준 라이브러리 기반 DuckDB 마이크로 배치 수신 워커 레시피 작성 (`doc/CDC_DUCKDB_INTEGRATION.md`).
- [ ] **트랜잭셔널 아웃박스 외부 브로커 디스패처 (Kafka, RabbitMQ) (`pkg/cdc`, `pkg/storage`)**
  - CDC 스트림을 외부 분산 메시지 브로커로 무손실 발행하는 Outbox 릴레이 워커.
- [ ] **[보류 / Postponed] Pure Go 영속 스토리지 엔진 옵션 (Pebble / bbolt) (`pkg/storage`)**
  - CGO/WiredTiger 의존성 없이 완전한 정적 바이너리(`CGO_ENABLED=0`, Golden Rule 36) 크로스 컴파일 배포를 위한 `storage.Engine` 플러그인 백엔드 (현재는 네이티브 WiredTiger CGO 및 인메모리 OCC 고성능 엔진에 집중하며 보류).
- [ ] **WebAssembly (Wasm) 런타임 기반 동적 UDF 샌드박스 (`pkg/extension`, `wazero`)**
  - 독립 서버 환경에서 `CREATE FUNCTION <name> AS WASM '<path.wasm>'` DDL 지원.
  - CGO 없는 순수 Go `wazero` 런타임 탑재, 메모리 샌드박싱(Golden Rule 26)을 통한 크래시/패닉 격리.
- [ ] **선언적 스키마 마이그레이션 CLI (`cmd/server`, `pkg/migrate`) & 소셜 로그인 (`pkg/api/handlers_auth.go`)**
  - `sqlion migrate new / up / down` 스키마 이력 테이블(`_migrations`) 추적 관리.
  - Google, GitHub, Apple OAuth2 콜백 내장 및 자동 사용자 프로비저닝.

---

## 3. 최근 완료된 핵심 마일스톤 아카이브 (Recently Completed Milestones)

- [x] **[아키텍처] VDBE Instruction 구조체 16바이트 패킹 (`pkg/proc/bytecode.go`)**
  - `Op(1B) + Flags(1B) + Aux(2B) + A(4B) + B(4B) + C(4B) = Exactly 16 Bytes` 고정 크기 레코드 압축.
  - 64B L1 Cache Line당 정확히 4개 명령어 적재(`unsafe.Sizeof == 16`), VM 디스패치 레이턴시 단축.
- [x] **[아키텍처] SQL 실행 코디네이터 패키지 분리 (`pkg/api` ➔ `pkg/engine`)**
  - 13개 `sqlexec_*.go` 파일(14,000+ 라인)을 `pkg/engine`으로 완전 이관.
  - PGWire 및 TDS 리스너가 `pkg/api` 의존성 없이 `engine.SQLCoordinator` 단방향 직결 호출 확립.
- [x] **[스토리지] 스토리지 파이프라인 배치형 그룹 커밋 (Group Commit & Fsync 가속)**
  - `memengine` WAL 및 `wiredtiger` 양쪽 엔진 모두에 리더/팔로워 그룹 커밋 및 무경합 패스트패스 구현.
  - `FsyncAlways` 마이크로벤치마크 커밋 지연 100배 단축(0.038ms/op), TPC-C VDBE 14,800+ TPS (422,000+ tpmC) 달성.
- [x] **[SQL 문법] T-SQL & ANSI SQL 7대 문법 및 실행 파이프라인 전면 완비 (`S-1` ~ `S-7`)**
  - **S-1**: `OFFSET ... FETCH NEXT ... ROWS ONLY` 및 `TOP (n)` 정규 페이징 완비.
  - **S-2**: 복합 한정자(`[db].[schema].[tbl].[col]`) 및 인라인 별칭(`FROM accounts basetbl`) 파싱 정밀화.
  - **S-3**: FROM 절 파생 테이블(`FROM (SELECT ...) AS t`) 및 인라인 서브쿼리 AST 노드화, 격리 구체화(`__dt_*`).
  - **S-4**: `CAST`, `TRY_CAST`, `CONVERT`, `TRY_CONVERT`, `::` 및 `COALESCE`/`ISNULL` 단락 평가 완비.
  - **S-5**: `CROSS APPLY`, `OUTER APPLY` 및 `FULL OUTER JOIN`, `RIGHT OUTER JOIN` 볼케이노 해시조인 완비.
  - **S-6**: 세션 제어문(`SET NOCOUNT ON`, `SET ANSI_NULLS ON` 등) 정식 AST 파싱 및 세션 플래그 바인딩.
  - **S-7**: `WHERE [NOT] EXISTS` 상관 서브쿼리 Semi/Anti-Join CBO 변환 및 비상관 조기 평가.
- [x] **[CBO] CBO 불리언 트리 재귀 평가 & OR 조건 정밀 선택도 산출 (`C-6`)**
  - `TableStats.EstimateExprSelectivity()` 신설, 동일 컬럼 배타 합($P(A)+P(B)$), 독립 합집합, NOT, IN, BETWEEN 정밀 산출 완비.
- [x] **[보안] 세분화된 SQL 객체 수준 RBAC (Role-Based Access Control) (`pkg/auth`, `pkg/catalog`, `pkg/api`)**
  - `CREATE LOGIN/USER`, `SETUSER`, `GRANT/REVOKE/DENY` (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `EXECUTE`, `CONTROL`, `REFERENCES`) 및 사전 카탈로그 인터셉터(`checkTableAccess`) 완비.
- [x] **[안정성] PlanCache Bounded LRU 캐시 및 메모리 상한 방어 (Golden Rule 29) (`pkg/proc/runtime`)**
  - 무제한 맵을 `container/list` 기반 상한 용량(`DefaultPlanCacheCapacity = 1024`) LRU 캐시로 전면 개편하여 OOM 원천 방지.
- [x] **[T-SQL 내장함수] T-SQL 공식 내장 스칼라 함수 전수 완비 (A~F 그룹)**
  - 에러(`ERROR_MESSAGE`), 문자열(`PATINDEX`, `STUFF`, `CONCAT_WS`), 수학/삼각(`LOG`, `EXP`, `RAND`), 메타데이터(`OBJECT_ID`, `DB_NAME`), 암호화(`HASHBYTES`), 날짜(`DATEFROMPARTS`) 등 완비.
- [x] **[드라이버] 공식 클라이언트 드라이버 E2E 통합 검증**
  - Go 공식 드라이버 `go-mssqldb` (TDS), `lib/pq` (PGWire), `pgx/v5` (Extended Query Pipelining) 100% PASS 검증 완료.
- [x] **[성능] 단순 PK 조회 고속 지문 해싱 & 스토리지 바이패스 캐시 (`P-5`) (`pkg/engine`, `pkg/storage`)**
  - 쿼리 문자열 FNV-1a 64비트 지문 해싱 기반 Fast-Path 템플릿 탐지 및 바운디드 캐시(`PKLookupPlan`).
  - 정규 파서/CBO/VDBE 스택을 전면 우회하여 스토리지 엔진(`tx.Get`)으로 직결 반환, DDL 발생 시 자동 무효화(`InvalidatePKBypassCache`).
  - Prometheus 메트릭스 `sqlion_pk_bypass_hits_total` 연동 및 멀티 테이블 한정자/별칭 완벽 지원.
- [x] **[스토리지/디코딩] 바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 커버링 인덱스 INCLUDE (`P-2`) (`pkg/storage`, `pkg/execution`, `pkg/planner`, `pkg/proc`, `pkg/engine`)**
  - 제로 할당 `SkipTupleColumn`, $O(1)$ 오프셋 캐싱 `LazyTuple`, 선별 디코딩 `DecodeTupleSelected` 구현.
  - `CREATE INDEX ... INCLUDE(...)` 문법 파싱, SkipList/IndexData 비키 페이로드 적재 및 CBO `isCovering` Index-Only Scan 극대화.
- [x] **[실행 엔진] 볼케이노 벡터화 청크 반복자 및 1,024건 배치 스트리밍 (`P-4`) (`pkg/execution`, `pkg/engine`)**
  - `ChunkedIterator` 및 `NextChunk` 인터페이스 신설, 1,024건 프리알로케이션 청크 기반 볼케이노 배치 파이프라인 관통.
  - SeqScan, IndexScan, Filter, Project, Limit, Aggregate, Sort, HashJoin, IndexNestedLoopJoin, sqlexec_select 전반 배치 가속 및 100% 하위 호환성 보장.
- [x] **[와이어 프로토콜] TDS NBCROW 행 압축 (`0xD2`) (`pkg/wire/tds`)**
  - TDS 7.3+ 표준 `BuildNBCRow` 신설, NULL 비트맵 패킹 및 0바이트 전송으로 네트워크 대역폭 30%+ 절감.
- [x] **[CBO/조인] 소트-머지 조인 (Sort-Merge Join) 연산자 및 옵티마이저 힌트 (`B-3`, `D-9`) (`pkg/execution`, `pkg/planner`, `pkg/proc`)**
  - $O(M+N)$ 해시리스 스트리밍 반복자 `MergeJoinIterator` 신설 및 `OPTION (HASH/MERGE/LOOP JOIN)`, `WITH (INDEX(...))` 강제 오버라이드.
- [x] **[생태계/GUI] 3대 메이저 GUI 클라이언트(DBeaver, DbGate, Beekeeper Studio) 완벽 호환 (`pkg/engine`, `pkg/wire`, `pkg/wire/tds`)**
  - TDS 및 PGWire 양대 프로토콜 상에서 테이블 브라우징, 인덱스/외래키/프로시저 인트로스펙션, 스토리지 크기 산출 및 메타데이터 조회 전면 완비.
  - DBeaver SQL Server 무한 쿼리 루프(`totalSize`, `usedSize` 컬럼 및 단일 `object_id` 필터링) 해결 및 PostgreSQL 멀티 네임스페이스(`public`, `dbo`) `pg_class` 인트로스펙션 지원.
  - Beekeeper Studio 및 DbGate 테이블 한정 와일드카드 프로젝션(`alias.*`, `schema.table.*`)과 WiredTiger EOF 래핑 방지 래치 적용 완료.
