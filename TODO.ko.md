# SQLion Engineering TODO & Backlog

이 문서는 SQLion의 단기 엔지니어링 우선순위 과제, 도메인별 기능 백로그, 최근 완료된 마일스톤 이력을 관리하는 공식 엔지니어링 백로그입니다. 중장기 아키텍처 로드맵은 [ROADMAP.ko.md](ROADMAP.ko.md) ([English: ROADMAP.md](ROADMAP.md))를 참조하세요.

---

## 1. 차기 착수 우선순위 과제 (Next High Priorities)

| 우선순위 | 영역 | 핵심 과제 | 대상 위치 | 기대 효과 및 구현 요약 |
| :---: | :---: | :--- | :--- | :--- |
| **P1** | **CBO** | **소트-머지 조인 (Sort-Merge Join) 연산자 (`B-3`)** | `pkg/execution`, `pkg/planner` | 정렬된 입력에 대해 해시리스 $O(M+N)$ 단일 패스 스트리밍 조인(`MergeJoinIterator`) 구현 |
| **P2** | **성능** | **단순 PK 조회 고속 지문 해싱 & 바이패스 캐시 (`P-5`)** | `pkg/engine`, `pkg/storage` | `SELECT * FROM tbl WHERE id = ?` 정규 파서/플래너 우회 스토리지 직결 (TPS 50%+ 향상) |
| **P2** | **프로토콜** | **TDS NBCROW 행 압축 (`0xD2`)** | `pkg/wire/tds` | NULL 비트맵 기반 0바이트 전송으로 네트워크 대역폭 30%+ 절감 |
| **P3** | **생태계** | **외부 언어 드라이버 E2E 검증 하네스** | `pkg/wire`, `pkg/wire/tds` | Python(`psycopg3`, `pymssql`), Node.js(`pg`, `tedious`), .NET(`SqlClient`) 검증 |

### P1 세부 과제: CBO & 쿼리 최적화
- [x] **CBO 옵티마이저 힌트 오버라이드 제어 (`D-9`) (`pkg/planner`, `pkg/proc`)**
  - **배경**: CBO 통계가 미비하거나 특수한 워크로드에서 DBA나 개발자가 실행 계획을 명시적으로 제어할 수 있는 수단 제공.
  - **구현 내용**:
    - 테이블 힌트: `WITH (INDEX(<index_name>))` 강제 인덱스 스캔, `WITH (INDEX(0))` 강제 테이블 풀 스캔.
    - 쿼리 힌트: `OPTION (HASH JOIN)`, `OPTION (MERGE JOIN)`, `OPTION (LOOP JOIN)`, `OPTION (FORCE ORDER)` 조인 전략 강제.
    - CBO 플래너(`pkg/planner/cbo.go`)에서 힌트 존재 시 비용 계산 우회 및 힌트 지정 연산자 강제 선택.
- [ ] **소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델 (`B-3`) (`pkg/execution`, `pkg/planner`)**
  - **배경**: 양측 입력 릴레이션이 이미 인덱스 정렬 순서이거나 사전 `ORDER BY`로 정렬된 경우, 거대한 인메모리 해시 테이블 빌드 비용을 완전히 제거.
  - **구현 내용**:
    - `pkg/execution/merge_join.go`: $O(M+N)$ 단일 패스 스트리밍 반복자 `MergeJoinIterator` 신설 (동일 조인 키 중복 시 Mark & Restore 윈도우 스캔).
    - `pkg/planner/cbo.go`: 양측 릴레이션의 정렬 속성(Sort Property)을 감지하고 해시 조인 vs 머지 조인 비용 경쟁 공식 추가.

### P2 세부 과제: 엔진 성능 & 프로토콜
- [ ] **단순 PK 조회 고속 지문 해싱 & 바이패스 캐시 (`P-5`) (`pkg/engine`, `pkg/storage`)**
  - **배경**: 전체 쿼리 트래픽의 70~80%를 차지하는 단순 단건 PK 점조회(`SELECT * FROM tbl WHERE id = ?`)에 대해 매번 정규 파서, 바인더, CBO 플래너, VDBE 컴파일러를 거치는 CPU 오버헤드 해소.
  - **구현 내용**:
    - 쿼리 문자열 Wyhash/FNV 지문 해싱 기반 템플릿 인식.
    - 정규 실행 엔진을 우회(Bypass)하여 스토리지 엔진 PK 인덱스 겟(`Engine.Get`)으로 직결 반환.
- [ ] **TDS 프로토콜 고도화: NBCROW 행 압축 (`0xD2`) (`pkg/wire/tds`)**
  - **배경**: TDS 7.3+ 표준 `NBCROW` (Null Bitmap Compressed Row) 토큰을 적용하여 NULL 필드가 많은 희소 테이블 조회 시 네트워크 대역폭 대폭 절감.
  - **구현 내용**:
    - `pkg/wire/tds/token.go`: 컬럼별 NULL 유무를 바이트 단위 비트맵으로 선행 패킹하고, NULL 컬럼에 대한 페이로드 전송 0바이트 처리.

### P3 세부 과제: 드라이버 생태계
- [ ] **외부 언어 드라이버 E2E 통합 검증 하네스 (`pkg/wire`, `pkg/wire/tds`)**
  - **배경**: Go 공식 드라이버(`go-mssqldb`, `lib/pq`, `pgx/v5`)에 이어 엔터프라이즈 멀티 언어 클라이언트 검증.
  - **구현 내용**:
    - Python: `psycopg3` (PGWire) 및 `pymssql` (TDS) 연결, 세션 초기화 파라미터(`extra_float_digits` 등), 기본 CRUD 검증.
    - Node.js: `pg` 및 `tedious` 연결 풀링 및 쿼리 파이프라이닝 검증.
    - C# / .NET: `Microsoft.Data.SqlClient` TDS 접속 및 매개변수화 프로시저 호출 검증.

---

## 2. 도메인별 대기 백로그 (Domain-Specific Backlogs)

### 2.1. CBO & 쿼리 최적화 (CBO & Query Engine)

| 번호 | 과제명 | 상태 | 설명 |
| :---: | :--- | :---: | :--- |
| **A-1** | **`ANALYZE TABLE` 멀티코어 병렬 통계 수집** | **완료 (`[x]`)** | 컬럼별 정렬 및 히스토그램 수집을 `sync.WaitGroup` 병렬화 완료 |
| **A-2** | **CBO 통계 메타데이터 디스크 영속화** | **완료 (`[x]`)** | `cbo_stats.json` 원자적 파일 쓰기 및 기동 시 자동 복원 연동 완료 |
| **C-6** | **OR 조건 및 불리언 트리 선택도 계산** | **완료 (`[x]`)** | AST 재귀 평가, 배타합/독립합집합, NOT, IN, BETWEEN 정밀 산출 완료 |
| **D-9** | **CBO 옵티마이저 힌트 오버라이드 제어** | **완료 (`[x]`)** | T-SQL 조인 힌트(`HASH/LOOP/MERGE`) 및 인덱스 힌트(`WITH (INDEX(...))`) 강제 반영 |
| **B-3** | **소트-머지 조인 (Sort-Merge Join) 연산자** | **착수 예정 (`[ ]`)** | 정렬된 입력에 대해 $O(M+N)$ 해시리스 스트리밍 조인(`MergeJoinIterator`) |
| **B-4** | **$N > 4$ 체인 조인 동적 계획법 (DP)** | **대기 (`[ ]`)** | 5개 이상 테이블 조인 시 Left-Deep DP 및 Bushy Tree 동적 탐색 |
| **C-5** | **다중 컬럼 확장 통계 (Extended Stats)** | **대기 (`[ ]`)** | 컬럼 상관관계로 인한 카디널리티 과소평가 방지 |
| **C-7** | **MCV (Most Common Values) 최빈값 목록 결합** | **대기 (`[ ]`)** | 왜곡 데이터 최빈값 분리 처리 |
| **D-8** | **백그라운드 자동 통계 갱신 데몬** | **대기 (`[ ]`)** | DML 변동량(Dirty Tuples) 기반 자동 `ANALYZE` 트리거 |

- [ ] **동적 계획법(DP / Selinger) 기반 대규모 조인 순서 최적화 ($N > 4$) (`pkg/planner`)**
  - 4개 이하의 체인 조인 전수 순열 탐색을 넘어, $N > 4$ 테이블 조인에 대한 Left-Deep DP 및 Bushy Tree 동적 계획법(DPccp) 탐색 지원.
- [ ] **다중 컬럼 확장 통계 (Extended / Multi-Column Statistics) (`pkg/planner`)**
  - 컬럼 간 상관관계(예: 국가-도시)로 인한 복합 조건 카디널리티 과소평가(Underestimation)를 방지하는 다중 컬럼 NDV 수집.
- [ ] **MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합 (`pkg/planner`)**
  - 왜곡(Skew)이 심한 상위 N개 최빈값은 MCV 목록으로 보관하고 나머지 균등 분포는 히스토그램으로 이원화 처리.
- [ ] **백그라운드 자동 통계 갱신 데몬 (Auto-Analyze Daemon) (`pkg/catalog`, `pkg/api`)**
  - 테이블 DML 변경 비율(예: 10% 이상 Row 변동 발생)을 추적하여 백그라운드에서 비동기로 `ANALYZE` 자동 트리거.

### 2.2. 스토리지 & 엔진 성능 (Storage & Engine Performance)

| 단계 | 전략 분야 | 핵심 구현 과제 | 상태 | 예상 효과 및 ROI |
| :---: | :--- | :--- | :---: | :--- |
| **P-1** | **스토리지/트랜잭션** | **배치형 그룹 커밋 & 무경합 Fast-Path** | **완료 (`[x]`)** | `FsyncAlways` 100배 지연 단축(0.038ms/op), TPC-C 2.8K TPS |
| **P-2** | **스토리지/디코딩** | **바이너리 튜플 지연 역직렬화 & 커버링 인덱스** | **대기 (`[ ]`)** | 스캔 속도 2배 향상, 불필요한 Varint 디코딩 힙 할당 제거 |
| **P-3** | **와이어 프로토콜** | **제로카피 와이어 인코딩 & I/O 버퍼 결합** | **대기 (`[ ]`)** | 중간 `[]any` 레코드 박싱 제거, TCP syscall 횟수 대폭 감소 |
| **P-4** | **실행 엔진** | **벡터화 청크 반복자 (Chunked Batch Next)** | **대기 (`[ ]`)** | 볼케이노 함수 호출 오버헤드 감소, 대량 집계/스캔 지연 60% 단축 |
| **P-5** | **캐시 및 메모리** | **고속 SQL 지문 해싱 & 단순 PK 조회 바이패스** | **대기 (`[ ]`)** | 단순 PK 조회 파서 바이패스, TPS 50%+ 향상, GC 부하 90% 경감 |

- [ ] **바이너리 튜플 지연 역직렬화 (Lazy Decoding) & 투영 푸시다운 (`pkg/storage`, `pkg/execution`)**
  - 풀 스캔 시 WHERE 절 조건 컬럼만 선별 디코딩하고, 조건을 통과한 행에 한해서만 나머지 투영 컬럼을 언패킹하여 CPU 및 힙 할당 70% 삭감.
- [ ] **커버링 인덱스 비키 컬럼 문법 지원: `CREATE INDEX ... INCLUDE (col...)` (`pkg/catalog`, `pkg/planner`)**
  - B+Tree 리프 노드에 비키 컬럼을 페이로드로 적재하여 메인 테이블 힙 조회를 100% 생략하는 Index-Only Scan 극대화.
- [ ] **스토리지 ➔ 와이어 제로카피 직접 직렬화 (`pkg/storage`, `pkg/wire`)**
  - 중간 `[]any` 레코드 슬라이스 변환을 제거하고 `sync.Pool` 와이어 버퍼에 바이너리 튜플 데이터를 직접 포맷팅.
- [ ] **Volcano 청크 단위 배치 실행 (Vectorized Chunk Iterator: 64~1024건) (`pkg/execution`)**
  - 행 단위 `Next()` 가상 함수 호출 오버헤드를 줄이기 위해 1,024개 튜플 벡터 단위 슬라이스 배치 스트리밍.
- [ ] **세션/쿼리 전용 아레나 메모리 할당기 (Arena Allocator) (`pkg/execution`, `pkg/storage`)**
  - 해시 조인, 소트 버퍼에 대해 쿼리 수명주기에 묶인 아레나 할당기를 적용하여 Go GC 압박 원천 제거.
- [ ] **백그라운드 MVCC Vacuum / Version GC (`pkg/storage/memengine`)**
  - 장기 트랜잭션 종료 후 구버전 튜플 체인을 비동기로 회수/압축하여 메모리 단편화 및 누적 방지.

### 2.3. 와이어 프로토콜 & 생태계 (Wire Protocols & Ecosystem)
- [ ] **T-SQL 특수 타입 직렬화 및 PLP LOB 청크 스트리밍 (`pkg/wire/tds`)**
  - `UNIQUEIDENTIFIER` (UUID 16바이트 GUID 바이트 스왑), `DATETIME2` (`0x2A`), `DATE` (`0x28`), `TIME` (`0x29`) TDS 바이너리 인코딩.
  - PLP (Partially Length-Prefixed) 기반 8KB 초과 `VARCHAR(MAX)` / `VARBINARY(MAX)` 대용량 청크 스트리밍.
- [ ] **SSMS (SQL Server Management Studio) Object Explorer 가상 카탈로그/DMV 뷰 (`pkg/api`, `pkg/catalog`)**
  - SSMS 접속 시 필수 조회되는 `sys.configurations`, `sys.dm_exec_sessions`, `sys.dm_exec_connections`, `sys.database_principals` 가상 뷰 완비로 SSMS 탐색기 트리 100% 호환.
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
- [ ] **트랜잭셔널 아웃박스(Transactional Outbox) & CDC 이벤트 스트리밍 (`pkg/storage`, `pkg/api`)**
  - 트랜잭션 커밋과 100% 원자적으로 연동되는 Row-level 변경 델타(Inserted/Updated/Deleted) 이벤트 스트림.
  - 외부 메시지 브로커(Kafka, RabbitMQ) 또는 웹훅으로 무손실 발행하는 Outbox 디스패처.
- [ ] **공유 메모리(POSIX shm / mmap) 기반 Apache Arrow IPC 컬럼 스냅샷 (Zero-Copy DuckDB 연동) (`pkg/storage`)**
  - 특정 시점의 테이블 데이터를 Apache Arrow IPC RecordBatch 포맷으로 직렬화하여 `/dev/shm` 또는 `mmap` 임시 파일에 기록 (`SNAPSHOT TABLE <t> TO ARROW_SHM <name>`).
  - 외부 분석 프로세스(DuckDB, Python Polars, Pandas)에 파일 디스크립터(FD)를 전달하여 Zero-Copy 커널 페이지 맵핑 기반 고속 분석 실현 (OLTP 쓰기 격리 유지).
- [ ] **Pure Go 영속 스토리지 엔진 옵션 (Pebble / bbolt) (`pkg/storage`)**
  - CGO/WiredTiger 의존성 없이 완전한 정적 바이너리(`CGO_ENABLED=0`, Golden Rule 86) 크로스 컴파일 배포를 위한 `storage.Engine` 플러그인 백엔드 추가.
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
