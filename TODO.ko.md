# SQLion Engineering TODO & Backlog

This document tracks active tasks, short-term engineering priorities, and conceptual backlog items for SQLion. For long-term architectural visions, refer to [ROADMAP.md](ROADMAP.md).

---

## 1. High Priority (Near-Term)

- [x] **공식 Microsoft MSSQL 드라이버 E2E 통합 검증 & TDS 호환성 강화 (`pkg/wire/tds`)**
  - `github.com/microsoft/go-mssqldb` 공식 Go 드라이버 기반 실제 E2E 통합 테스트 스위트 구축 (`driver_integration_test.go`).
  - TDS `PacketTransactionManager` (0x0E), Collation/Transaction `ENVCHANGE` 토큰, 트랜잭션 디스크립터 및 `DoneInXact` 상태 머신 완비.
  - 드라이버 Ping, CRUD, Prepared Statements (`sp_prepare`), 매개변수 바인딩, 세션 SET 옵션, 트랜잭션 커밋/롤백 100% 통과.
- [x] **공식 PostgreSQL lib/pq 드라이버 E2E 통합 검증 & Wire 프로토콜 호환성 강화 (`pkg/wire`)**
  - `github.com/lib/pq` 공식 Go 드라이버 기반 실제 E2E 통합 테스트 스위트 구축 (`driver_integration_test.go`).
  - Extended Query 프로토콜 매개변수 개수 추론(`handleParse`), 투영 컬럼 스키마 매칭(`writeSchemaDescription`), `Bind` 결과 바이너리 포맷 코드 지원(`formatColumnValue`, `WriteDataRowBytes`).
  - `BEGIN READ WRITE` 등 유연한 트랜잭션 구문 확장 및 PostgreSQL 표준 CommandComplete 태그 매핑.
  - 드라이버 Ping, 기본 쿼리, CRUD, 매개변수 쿼리, Prepared Statements, 트랜잭션(커밋/롤백), 세션 옵션, 메타데이터 100% 통과.
- [x] **현대 표준 Go 드라이버 `pgx/v5` 통합 검증 및 파이프라이닝 지원 (`pkg/wire`)**
  - `github.com/jackc/pgx/v5` 기반 E2E 통합 테스트 스위트 구축.
  - Extended Query `Flush`(`'H'`) 메시지 처리 및 파이프라이닝 상태 머신 동기화.
  - `timestamp`, `timestamptz`, `bool`, `float8` 등 `pgx` 바이너리 인코딩/디코딩 타입 확장.
- [ ] **외부 언어 생태계(Python/Node.js/C#) 클라이언트 호환성 검증 및 하네스 구축 (`pkg/wire`, `pkg/wire/tds`)**
  - Python `psycopg2`/`psycopg3`, `pymssql` 드라이버 연결 및 초기 세션 쿼리(`extra_float_digits`, `server_version_num`) 처리.
  - Node.js `pg`, `tedious` 라이브러리를 활용한 커넥션 풀링 및 쿼리 실행 검증.
  - .NET / C# `Microsoft.Data.SqlClient` 연결 및 기본 쿼리 검증.
- [ ] **TDS 프로토콜 고도화: NBCROW 행 압축 및 Attention 쿼리 취소 (`pkg/wire/tds`)**
  - TDS `NBCROW` (Null Bitmap Compressed Row, `0xD2`): NULL 컬럼에 대해 0바이트로 전송하여 네트워크 대역폭 30%+ 절감.
  - **[완료] Attention 패킷(`Type 0x06`) 처리**: 장기 실행 쿼리 취소 시 비동기 패킷 디커플링 및 VDBE 바이트코드 루프 중단(`context.CancelFunc`), `DoneAttn (0x0020)` 응답 완비 완료.
- [ ] **T-SQL 특수 타입 직렬화 및 PLP LOB 청크 스트리밍 (`pkg/wire/tds`)**
  - `UNIQUEIDENTIFIER` (UUID 16바이트 GUID 바이트 스왑), `DATETIME2` (`0x2A`), `DATE` (`0x28`), `TIME` (`0x29`) TDS 바이너리 인코딩.
  - PLP (Partially Length-Prefixed) 기반 8KB 초과 `VARCHAR(MAX)` / `VARBINARY(MAX)` 대용량 청크 스트리밍.
- [ ] **SSMS (SQL Server Management Studio) Object Explorer 가상 카탈로그/DMV 뷰 (`pkg/api`, `pkg/catalog`)**
  - SSMS 접속 시 필수 조회되는 `sys.configurations`, `sys.dm_exec_sessions`, `sys.dm_exec_connections`, `sys.database_principals` 가상 뷰 완비로 SSMS 탐색기 트리 100% 호환.
- [ ] **임베디드 웹 스튜디오 HA 복제 모니터링 대시보드 (`pkg/ui`)**
  - 브라우저 웹 스튜디오(`:8090/_/`)에 고가용성(HA) 복제 상태(Primary vs Replica) 시각화 카드 추가.
  - 연결된 Replica 목록, LSN 랙(Lag) 지연 시간, 스트리밍 상태 표시 및 수동 승격(`Promote`) 버튼 제공.
- [ ] **클라이언트 읽기 분산 로드밸런싱 가이드 & 라우팅 인터페이스 (`pkg/wire`, `pkg/api`)**
  - 클라이언트 드라이버/HTTP 요청에서 쓰기(`Primary`)와 조회(`SELECT`)를 자동 분기할 수 있는 라우팅 정책 및 헬퍼 정리.

---

## 2. Analytics & Parallelism Backlog (중기 과제)

- [ ] **공유 메모리(POSIX shm / mmap) 기반 Apache Arrow IPC 컬럼 스냅샷 (Zero-Copy DuckDB 연동)**
  - 특정 시점의 테이블 데이터를 Apache Arrow IPC RecordBatch 포맷으로 직렬화하여 `/dev/shm` 또는 `mmap` 임시 파일에 기록 (`SNAPSHOT TABLE <t> TO ARROW_SHM <name>`).
  - 외부 분석 프로세스(DuckDB, Python Polars, Pandas)에 파일 디스크립터(FD) 및 경로를 전달하여 Zero-Copy 커널 페이지 맵핑 기반 고속 벡터화 분석 실현.
  - 무결함 격리(Zero Blast Radius): 무거운 분석 쿼리가 OOM을 유발해도 OLTP 쓰기 트랜잭션은 100% 무중단 유지.
  - TTL 기반 자동 해제(`unlink` / `munmap`)로 메모리 누수 방지.
- [ ] **`CREATE INDEX` 백그라운드 병렬 정렬 및 벌크 로딩 (`pkg/catalog`, `pkg/storage`)**
  - 대용량 데이터에서 보조 인덱스 생성 시 데이터를 N개 청크로 분할하여 병렬 정렬 후 B+Tree/SkipList 벌크 삽입.
- [x] **Go 네이티브 스칼라 함수 등록 SPI (In-Process Go UDF) (`pkg/proc`)**
  - SQLion을 라이브러리로 임베딩하는 개발자를 위해 `proc.RegisterScalarFunction(name, fn)` Go API 제공. 제로 오버헤드 나노초 단위 직결 호출.
- [ ] **WebAssembly (Wasm) 런타임 기반 동적 UDF 샌드박스 (`pkg/extension`, `wazero`)**
  - 독립 서버 환경에서 `CREATE FUNCTION <name> AS WASM '<path.wasm>'` DDL 지원.
  - CGO 없는 순수 Go `wazero` 런타임 탑재, 메모리 샌드박싱(Golden Rule 26)을 통한 크래시/패닉 격리 및 크로스 플랫폼 호환.

---

## 3. CBO (Cost-Based Optimizer) & Query Optimization Backlog

- [x] **CBO 통계 메타데이터 디스크 영속화 (`pkg/catalog`, `cbo_stats.json`)**
  - `SetPersistenceDir` 기반 `cbo_stats.json` 원자적 파일 쓰기(`*.tmp` -> rename) 및 기동 시 자동 복원. 서버 재시작 후에도 `ANALYZE TABLE` 통계 및 최적화 실행 계획 영속 유지.
- [x] **`ANALYZE TABLE` 통계 및 히스토그램 멀티코어 병렬 수집 (`pkg/planner`, `pkg/api`)**
  - 대형 테이블 분석 시 컬럼별 NDV, Min/Max, 20-버킷 등빈도 히스토그램 수집을 `sync.WaitGroup` 멀티코어 병렬화(`BuildTableStatsParallel`)하여 통계 수집 시간 4~8배 단축 완료.
- [ ] **소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델 (`pkg/execution`, `pkg/planner`)**
  - 양측 릴레이션이 인덱스 순서 또는 ORDER BY 정렬 상태일 때 메모리 해시 빌드 없이 $O(M+N)$ 단일 패스로 처리하는 `MergeJoinIterator` 및 CBO 비용 산출 공식 추가.
- [ ] **동적 계획법(DP / Selinger) 기반 대규모 조인 순서 최적화 ($N > 4$) (`pkg/planner`)**
  - 4개 이하의 체인 조인 전수 순열 탐색을 넘어, $N > 4$ 테이블 조인에 대한 Left-Deep DP / Bushy Tree 동적 계획법(DPccp) 탐색 지원.
- [ ] **다중 컬럼 확장 통계 (Extended / Multi-Column Statistics) (`pkg/planner`)**
  - 컬럼 간 강한 상관관계(예: 국가-도시)로 인한 복합 조건 카디널리티 과소평가(Underestimation)를 방지하는 다중 컬럼 NDV 및 상관관계 통계 수집.
- [ ] **OR 조건 및 불리언 표현식 트리 선택도 모델링 (`pkg/planner`)**
  - `OR` 조건 및 중첩 괄호 조건에 대해 합집합 공식 ($P(A \lor B) = P(A) + P(B) - P(A \land B)$) 기반 정밀 선택도 산출.
- [ ] **MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합 (`pkg/planner`)**
  - 왜곡(Skew)이 심한 상위 N개 최빈값은 MCV 목록으로 보관하고 나머지 균등 분포는 히스토그램으로 이원화 처리.
- [ ] **백그라운드 자동 통계 갱신 데몬 (Auto-Analyze Daemon) (`pkg/catalog`, `pkg/api`)**
  - 테이블 DML 변경 비율(예: 10% 이상 Row 변동 발생)을 추적하여 백그라운드에서 비동기로 `ANALYZE` 자동 트리거.
- [ ] **CBO 옵티마이저 힌트 오버라이드 제어 (Optimizer Hints) (`pkg/planner`)**
  - `WITH (INDEX(...))`, `OPTION (HASH JOIN)`, `OPTION (FORCE ORDER)` 힌트로 CBO 비용 결정을 명시적으로 강제 제어.

---

## 4. Storage & Engine Hardening (장기 과제)

- [x] **Primary Key 식별자 표준화: ULID 대신 IETF 공식 표준 UUIDv7 (RFC 9562) 채택 (`pkg/storage`, `pkg/proc`)**
  - **결정 배경**: 2024년 5월 공식 제정된 RFC 9562 준수. ULID(비공식 커뮤니티 스펙, 26자 Base32) 대신 기존 16바이트 네이티브 `UUID` / `UNIQUEIDENTIFIER` 타입 및 와이어 프로토콜(PGWire, TDS)과 100% 무손실 호환되는 `UUIDv7` 채택.
  - **B+Tree 순차 삽입 성능**: 상위 48비트 Unix Epoch 밀리초 타임스탬프로 단조 증가(Monotonic) 시퀀스를 형성하여 WiredTiger B+Tree 페이지 분할(Page Split) 및 Random I/O 제거 완료.
  - **내장 함수 및 기본값 지원**: T-SQL `NEWID7()`, `UUIDV7()` 및 PostgreSQL 호환 `GEN_RANDOM_UUID(7)` 내장 함수 구현, `DEFAULT NEWID7()` 테이블 PK 자동 생성 지원 완료.
- [ ] **Volcano 청크 단위 배치 실행 (Vectorized Chunk Iterator)**
  - 행 단위 `Next()` 호출 오버헤드를 줄이기 위해 1,024개 튜플 벡터 단위 슬라이스 배치 스트리밍.
- [ ] **WiredTiger 세션 및 커서 풀링 최적화 (`pkg/storage/wiredtiger`)**
  - 고빈도 트랜잭션 경로에서 Cgo 커서 오픈/클로즈 오버헤드를 줄이기 위한 스레드 로컬 커서 재사용 고도화.
- [ ] **백그라운드 MVCC Vacuum / Version GC (`pkg/storage/memengine`)**
  - 장기 트랜잭션 종료 후 구버전 튜플 체인을 비동기로 회수/압축하여 메모리 단편화 및 누적 방지.

---

## 5. Next Week Architecture & Engine Refactoring Backlog (차주 최우선 핵심 리팩토링)

### 차주 최우선 착수 과제 (Top Priorities)
| 마일스톤 | 우선순위 | 핵심 과제 | 대상 위치 | 기대 효과 | 상태 |
| :---: | :---: | :--- | :--- | :--- | :---: |
| **Phase 5** | **P1-1** | **VDBE Instruction 16B 패킹** | `pkg/proc/bytecode.go`, `pkg/proc/runtime/` | 64B L1 Cache Line당 4개 명령어 적재, VM 레이턴시 단축 | **완료** |
| **Phase 5** | **P1-2** | **SQL 코디네이터 분리 (`pkg/engine`)** | `pkg/api/sqlexec_*.go` ➔ `pkg/engine/` | `pkg/api`에서 SQL 실행 엔진 분리, 단방향 클린 아키텍처 확립 | 대기 |
| **Phase 5** | **P2** | **PlanCache Bounded LRU 캐시** | `pkg/proc/runtime/vm.go` | 메모리 상한 방어, OOM 크래시 방지 (Golden Rule 29) | **완료** |
| **Phase 5** | **P2** | **T-SQL 내장 스칼라 함수 확장** | `pkg/proc/builtins.go` | `CONCAT_WS`, `QUOTENAME`, `ASCII`, `CHAR`, `NULLIF`, `ISNUMERIC` | **완료** |

- [x] **[완료] [Phase 5 / P1] VDBE Instruction 구조체 16바이트 패킹 (CPU L1 Data Cache Locality 극대화) (`pkg/proc/bytecode.go`, `pkg/proc/compiler.go`, `pkg/proc/runtime/`)**
  - **상수 & 메타데이터 풀(AuxData) 분리**: `Data any` 인터페이스(16B)를 제거하고 `Program.AuxData []any` 슬라이스로 외주화하여 `inst.Aux`(uint16) 인덱스로 참조.
  - **16바이트 고정 크기 레코드 압축**: `Op uint8, Flags uint8, Aux uint16, A int32, B int32, C int32` 16바이트 구조체로 재설계하여 64B L1 Cache Line당 정확히 4개 명령어 적재 (Zero Padding, `unsafe.Sizeof == 16` 테스트 검증 완료).
  - **검증**: `TestInstruction_Size16Bytes` 단위 테스트 및 T-SQL 런타임/컴파일러 회귀 테스트 검증.
- [x] **[완료] [Phase 5] PlanCache Bounded LRU 캐시 및 메모리 상한 방어 (Golden Rule 29) (`pkg/proc/runtime`)**
  - 무제한 맵이었던 `PlanCache`를 `container/list` 기반 상한 용량(`DefaultPlanCacheCapacity = 1024`) LRU 캐시로 전면 개편.
  - 용량 초과 시 자동 LRU 퇴출, 최근 참조 항목 앞단 승격, 프로시저/함수 대소문자 무관 검색(`GetByName`) 지원 및 `plancache_test.go` 단위 테스트 100% 통과.
- [x] **[완료] [Phase 5] T-SQL 필수 내장 스칼라 함수 확장 (`pkg/proc/builtins.go`, `pkg/proc/builtins_test.go`)**
  - `CONCAT_WS` (NULL 건너뛰기 지원 결합), `QUOTENAME` (구분 기호 및 이스케이프 중복), `ASCII`, `CHAR`, `NULLIF`, `ISNUMERIC` (NaN/Inf 방어) 내장 스칼라 함수 구현.
  - `IsBuiltinFunction` 등록 및 단위 테스트 100% 통과.
- [ ] **[Phase 5 / P1] 아키텍처 계층 정립: SQL 실행 엔진 코디네이터 패키지 분리 (`pkg/api` ➔ `pkg/engine`)**
  - `pkg/api`에 집중되어 있는 15,000+ 라인의 SQL 실행 오케스트레이션 로직(`sqlexec_*.go`)을 `pkg/engine` 코어 레이어로 독립 승격.
  - `pkg/api`(HTTP REST/Studio), `pkg/wire`(PGWire), `pkg/wire/tds`(TDS) 세 어댑터가 동등하게 코어 엔진 인터페이스를 호출하도록 단방향 클린 아키텍처 확립.
- [x] **[완료] VDBE 바이트코드 디스패처 모듈화 (`pkg/proc/runtime/vm.go`, `pkg/proc/runtime/vm_dispatcher.go`)**
  - `VMFrame.Execute` 내 824라인 거대 `switch op.Code` 블록을 기능군별 private 헬퍼 메서드로 분리 완료 (`execFlowOp`, `execComputeOp`, `execTxOp`, `execStorageOp`, `execCursorAndCallOp`, `execRelOp`).
  - 중복 catch stack 핸들링을 `handleCatch`로 통합(SSOT)하고, 순환 복잡도(Cyclomatic Complexity) 완화 및 프로파일링 스택 추적성 개선.
- [x] **[완료] VDBE 바이트코드 엔진 성능 튜닝 & 제로 할당 고속화 (`pkg/proc/runtime`)**
  - **1. 로컬 변수 슬롯화 (Local Variable Slot Indexing)**: T-SQL 파라미터 및 로컬 변수(`@from`, `@amount` 등) 접근 시 `map[string]storage.Value` 해시맵 조회 및 문자열 해싱/소문자 변환을 제거하고, 컴파일 타임 슬롯 인덱스(0, 1, 2...)를 부여하여 `f.slots[inst.B]` 고정 슬라이스 $O(1)$ 직접 배열 접근으로 전환 완료.
  - **2. 중간 컨텍스트 맵 생성(`buildRowEvalMap`) 제거 (Zero-Allocation Expression Evaluation)**: DML 및 수식 평가 시 `f.evalCtx` 사전 동기화 및 빈 행 캐싱을 적용하여 매번 새 `map[string]any` 생성/복사 병목 제거 완료 (프로시저 호출당 할당 171건 ➔ 122건으로 대폭 감소).
  - **3. 컨텍스트 취소 검사 배치화 (Batch Cancellation Check)**: 매 단일 바이트코드 명령마다 채널 `select / default`를 호출하여 발생하는 20~40ns 고정 오버헤드를 해소하기 위해 64개 명령 주기(`if pc&63 == 0 && f.ctx != nil`)로 취소 검사 주기 최적화 완료.
  - **4. 오프코드 특화(Specialization) 및 인터페이스 비교 제거**: `OpEq` 내 `inst.Data == "AND"` 등 동적 인터페이스 문자열 비교를 완전 제거하고 `OpAnd`, `OpOr` 독립 오프코드 분리 및 정수 핫패스 인라인 특화 오프코드(`OpAddInt`, `OpSubInt`, `OpMulInt`, `OpEqInt`, `OpNeqInt`, `OpLtInt`, `OpLteInt`, `OpGtInt`, `OpGteInt`) 도입 완료.
  - **5. 비교-점프 융합 및 피프홀 점프 체이닝 (Fused Compare-and-Branch & Jump Threading)**: `IF`/`WHILE` 조건문 평가 시 비교 오프코드와 점프 명령어를 단일 융합 오프코드(`OpJumpIfLtInt`, `OpJumpIfGteInt` 등)로 결합하여 불리언 레지스터 쓰기 및 VM 루프 디스패치를 50% 절감하고, `finalizeProgram` 피프홀 패스에서 징검다리 점프 체이닝 및 인접 Fall-through 점프 제거 완료.
  - **6. 마이크로 벤치마크 및 TPC-C 성능 추적 하네스 검증**: VDBE 슬롯화, 디스패처 모듈화 및 16B 인스트럭션 패킹 적용 후 TPC-C VDBE **18,542 TPS (495,834 tpmC)** 달성 (네이티브 Go 29,068 TPS 대비 63.8% 유지율, New-Order p50 0.56ms / p95 6.22ms, Payment p50 0.05ms / p95 2.99ms) 검증 완료.
- [ ] **관계형 스캔 벡터화 배치 파이프라이닝 (Vectorized Chunk Scan) (`pkg/proc/runtime`)**
  - `OpRelScan`, `OpIndexScan`에서 행 단위 VM 왕복 오버헤드를 줄이기 위한 N개(64~1024건) 단위 청크 벡터화 스트리밍.

---

## 6. SQL Syntax & Parser Coverage Backlog (T-SQL & ANSI SQL 문법 완비 과제)

### 6.1. [완료] 완료된 핵심 문법
- [x] **[완료] T-SQL & ANSI 표준 페이징 정규 문법 완비 (`OFFSET ... FETCH NEXT ... ROWS ONLY` 및 `TOP`) (`pkg/proc`, `pkg/planner`, `pkg/api`)**
  - 단순 `LIMIT / OFFSET`을 넘어 `ORDER BY ... OFFSET n ROWS FETCH NEXT m ROWS ONLY` 표준 구문을 Pratt AST 및 CBO 플래너에 1급 시민(First-Class)으로 완전 통합.
  - T-SQL `SELECT [DISTINCT] TOP (n) [PERCENT] [WITH TIES]` 구문 AST 파싱 및 Volcano/CBO 실행 연동, 단위 및 E2E 테스트 검증 완료.
- [x] **[완료] 세션 제어문(SET Statements) 정식 AST 파서 및 세션 플래그 영속화 (`pkg/api`, `pkg/wire`, `pkg/proc`)**
  - GUI 툴 접속 및 프로시저 내부 `SET NOCOUNT ON/OFF`, `SET ANSI_NULLS ON`, `SET QUOTED_IDENTIFIER ON`, 다중 옵션 쉼표 구분 문법 정식 AST(`SetOptionStmt`) 파싱, 바이트코드 컴파일 및 세션 플래그 바인딩 완료.
- [x] **[완료] 복합 한정자(Qualifier) 및 인라인 별칭(Alias) 파싱 정밀화 (`pkg/proc/parser.go`, `pkg/planner/planner.go`, `pkg/api/sqlexec.go`)**
  - 3단계/4단계 한정자(`[db].[schema].[table].[col]`, `db..table`) 및 2단계 한정자(`alias.col`, `"schema"."table"`)의 정규 토큰화 및 카탈로그 해석.
  - `AS` 키워드가 생략된 인라인 테이블 별칭(`FROM accounts basetbl`) 및 컬럼 별칭(`SELECT id id_alias`) 파싱 안정화.

### 6.2. P1: 실무 및 ORM/드라이버 필수 문법 (High Priority)
- [ ] **표준 타입 캐스팅 & 수식 정식 AST화 (`pkg/proc/eval.go`, `pkg/proc/compiler.go`)**
  - 데이터 타입 변환: `CAST(expr AS type)` 및 `CONVERT(type, expr [, style])`의 정규 Pratt AST 파싱 및 네이티브 캐스팅 함수 엔진 직결.
  - `CASE WHEN ... THEN ... ELSE ... END` 복합 조건식의 완전한 AST 표현 및 단락 평가(Short-Circuit Evaluation).
  - 애드혹 문자열 파싱 탈피: `COALESCE()`, `ISNULL()`, `NULLIF()`.
- [ ] **`INSERT INTO ... SELECT ...` 및 `SELECT ... INTO <new_table>` (`pkg/proc`, `pkg/api`)**
  - `INSERT INTO target (cols...) SELECT ... FROM source WHERE ...` 대량 배치 적재 DML 지원.
  - `SELECT ... INTO <table_name> FROM ...` 쿼리 결과 즉시 신규 테이블 생성 및 적재 지원.
- [ ] **FROM 절 파생 테이블(Derived Table) 및 인라인 서브쿼리 (`pkg/proc`, `pkg/planner`, `pkg/execution`)**
  - `SELECT * FROM (SELECT id, balance FROM accounts) AS t WHERE t.balance > 100` 형태의 FROM 절 서브쿼리 AST 노드화 및 Volcano 실행 연동 (ORM 호환 필수).
- [ ] **`WHERE EXISTS` / `NOT EXISTS` 상관 서브쿼리 술어 (`pkg/planner/decorrelate.go`, `pkg/execution`)**
  - `WHERE EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id)` 술어의 Semi-Join / Anti-Join 플래너 변환 및 실행.

### 6.3. P2: T-SQL 핵심 특화 문법 (MSSQL 마이그레이션 호환성)
- [ ] **`CROSS APPLY` 및 `OUTER APPLY` 조인 (`pkg/proc`, `pkg/planner`, `pkg/execution`)**
  - 테이블 반환 함수(TVF) 및 행별 상관 서브쿼리 조인을 위한 T-SQL 핵심 조인 연산자 (PostgreSQL `LATERAL JOIN` 대응).
- [ ] **DML `OUTPUT` 절 (`pkg/proc`, `pkg/execution`, `pkg/api`)**
  - `INSERT ... OUTPUT INSERTED.id`, `UPDATE ... OUTPUT DELETED.col, INSERTED.col`, `DELETE ... OUTPUT DELETED.*` DML 변경 델타 즉시 반환 (PostgreSQL `RETURNING` 대응).
- [ ] **로컬 임시 테이블 (`#temp_table`) 및 테이블 변수 (`DECLARE @tbl TABLE`) (`pkg/catalog`, `pkg/proc`)**
  - 세션 스코프 로컬 인메모리 임시 테이블 생명주기 관리 및 저장 프로시저 내부 테이블 변수 연동.
- [ ] **커버링 인덱스 비키 컬럼: `CREATE INDEX ... INCLUDE (col1, col2)` (`pkg/catalog`, `pkg/planner`)**
  - B+Tree 리프 노드에 비키 컬럼을 페이로드로 적재하여 메인 테이블 힙 조회를 100% 생략하는 Index-Only Scan 극대화.
- [ ] **자주 쓰이는 T-SQL 테이블/쿼리 힌트 매핑 (`pkg/proc`, `pkg/planner`)**
  - `SELECT * FROM tbl WITH (NOLOCK)`: SQLion 논블로킹 스냅샷 격리로 안전한 no-op 매핑.
  - `WITH (INDEX(ix_name))`, `OPTION (RECOMPILE)`, `OPTION (MAXDOP n)` 힌트 파서 수용 및 카탈로그 플래그 연동.
- [ ] **T-SQL 시스템/카탈로그 메타데이터 내장 함수 (`pkg/proc/builtins.go`)**
  - `OBJECT_ID()`, `OBJECT_NAME()`, `SCHEMA_NAME()`, `COL_LENGTH()`, `IDENT_CURRENT()`, `@@ROWCOUNT` 시스템 변수 정규화.

### 6.4. P3: 표준 SQL 고급 분석 및 조인 확장
- [ ] **`CROSS JOIN` 및 `FULL OUTER JOIN` (`pkg/proc`, `pkg/planner`, `pkg/execution`)**
  - 명시적 `CROSS JOIN` 구문 및 쉼표 조인(`FROM a, b`)의 데카르트 곱 실행기 완비.
  - 양방향 외부 조인 `FULL OUTER JOIN` 파싱 및 연산자 지원.
- [ ] **다차원 소계 집계: `GROUP BY GROUPING SETS`, `ROLLUP`, `CUBE` (`pkg/proc`, `pkg/planner`, `pkg/execution`)**
  - 다차원 분석 리포팅 쿼리를 위한 단일 패스 집계 그룹화 확장.

---

## 7. Overall System & Database Engine Performance Optimization Roadmap (전체 엔진 성능 고속화 로드맵)

- [ ] **[Phase 1: 스토리지 & 트랜잭션] 그룹 커밋(Group Commit) 및 읽기 전용 패스트패스**
  - **배치형 그룹 커밋 (Group Commit Pipelining)**: 개별 트랜잭션의 동기식 `fsync` 호출을 제거하고, 마이크로초 윈도우(50~100µs) 또는 버퍼 크기(32건) 단위로 단 1회의 시스템 콜(`writev` + `fdatasync`) 일괄 영속화 (동시성 OLTP TPS 2~4배 향상).
  - **[완료] 읽기 전용 트랜잭션 패스트패스 (Read-Only Fast-Path)**: DML이 발생하지 않은 트랜잭션 및 ReadOnly 트랜잭션은 OCC 유효성 검증과 WAL 기록을 건너뛰고 스냅샷 해제 및 불필요한 메타데이터 맵 할당 제거 완료.
- [ ] **[Phase 2: 튜플 디코딩 & 스토리지] 바이너리 튜플 지연 역직렬화 & 커버링 인덱스 스캔**
  - **프로젝션 푸시다운 & 지연 디코딩 (Lazy Tuple Decoding)**: 테이블 풀 스캔 또는 범위 스캔 시 전체 컬럼을 역직렬화하지 않고, Null 비트맵과 헤더 검사 후 쿼리에서 요구하는 특정 컬럼 오프셋만 선택적 슬라이싱하여 디코딩 (할당 및 CPU 절감).
  - **[완료] 커버링 인덱스 스캔 (Covering Index / Index-Only Scan)**: 인덱스 키와 테이블 PK만으로 투영이 충족될 때 기본 테이블 힙 페치 및 락을 100% 생략하는 `NodeIndexOnlyScan` 및 `SkipList.ScanRangeEntries` 가상 레코드 합성 완비 (`4f69eb6`). (후속 과제: T-SQL `INCLUDE (col...)` 비키 컬럼 문법 지원).
- [ ] **[Phase 3: 와이어 프로토콜 & 네트워크] 제로카피 와이어 인코딩 & I/O 버퍼 결합**
  - **스토리지 ➔ 와이어 제로카피 직접 직렬화**: 중간 `[]any` 레코드 슬라이스 변환을 제거하고, `sync.Pool`로 관리되는 와이어 바이트 버퍼에 바이너리 튜플 데이터를 직접 포맷팅.
  - **[완료] I/O 버퍼 결합 및 지연 플러시 (Write Combining & TCP_NODELAY)**: 행 단위 개별 `Write()`를 지양하고 32KB `bufio.Writer`로 행 및 명령 완료 패킷을 묶어 일괄 플러시하고 TCP_NODELAY를 적용하여 패킷 지연 및 syscall 횟수 최소화 완료.
- [ ] **[Phase 4: 실행 엔진 & 볼케이노] 청크 단위 배치 반복자 (Chunked / Vectorized Volcano Execution)**
  - **배치 튜플 프로세싱 (`NextBatch([64]Row)`)**: 행 단위 단일 `Next()` 가상 함수 호출을 64~256건 단위 튜플 청크 벡터 처리로 전환하여 함수 호출 오버헤드 완화 및 CPU L1 Data Cache / SIMD 파이프라이닝 극대화.
- [ ] **[Phase 5: 쿼리 캐시 & 메모리] 고속 SQL 핑거프린팅 & 쿼리 전용 아레나(Arena)**
  - **SQL 전문 고속 지문 해싱 (Fast SQL Fingerprinting)**: 파싱 전 Wyhash / FNV-1a 기반 텍스트 지문으로 `PlanCache` 선조회 및 단순 PK 점조회(`WHERE id = ?`) 스토리지 숏컷 라우팅.
  - **세션/쿼리 전용 메모리 슬랩/아레나 (Per-Query Arena Allocator)**: 조인, 해시 테이블, 정렬 시 자잘한 힙 객체 할당 대신 아레나 포인터 전진 방식으로 할당 후 쿼리 종료 시 일괄 해제 (GC 압력 제거 및 p99 지연 시간 안정화).

---

## 8. Enterprise Security, Governance & Compliance (보안, 권한 & 감사)

- [ ] **세분화된 SQL 객체 수준 RBAC (Role-Based Access Control) (`pkg/catalog`, `pkg/api`, `pkg/wire`)**
  - SQL 표준 역할 및 사용자 관리 DDL: `CREATE ROLE <role>`, `CREATE USER <user> WITH PASSWORD = '...'`, `ALTER ROLE`, `DROP ROLE`.
  - 객체별 세분화 권한 제어: `GRANT SELECT, INSERT, UPDATE, DELETE ON <table> TO <role/user>`, `REVOKE`, `DENY`.
  - 쿼리 실행 전 카탈로그 권한 인터셉터를 통한 권한 검증 및 세션 사용자 컨텍스트 바인딩.
- [ ] **투명 데이터 암호화 (TDE: Transparent Data Encryption) (`pkg/storage`)**
  - 디스크에 기록되는 WiredTiger 데이터 파일, WAL/AOF 저널 및 백업 아카이브를 AES-256-GCM으로 투명 암호화(Data at Rest Encryption).
  - 마스터 암호화 키(MEK) 로테이션 및 환경 변수/KMS 키 주입 인터페이스.
- [ ] **불변 엔터프라이즈 감사 로그 (Enterprise Audit Log) (`pkg/logger`, `pkg/api`)**
  - 로그인 성공/실패, 권한 변경(`GRANT/REVOKE`), DDL 스키마 변경, 관리자 데이터 조작 이벤트를 태깅하여 위변조가 불가능한 불변 감사 파일로 안전 기록.

---

## 9. BaaS & Developer Experience Backlog (PocketBase 호환 & 개발자 생산성)

- [ ] **선언적 스키마 마이그레이션 CLI (`cmd/server`, `pkg/migrate`)**
  - `sqlion migrate new <name>`: 타임스탬프 기반 SQL 마이그레이션 파일 템플릿 자동 생성.
  - `sqlion migrate up / down`: 트랜잭션 내에서 원자적 스키마 업그레이드 및 롤백 실행, 마이그레이션 적용 버전 이력 테이블(`_migrations`) 추적.
- [ ] **소셜 로그인 (OAuth2 Providers) (`pkg/api/handlers_auth.go`)**
  - Google, GitHub, Apple OAuth2 콜백 엔드포인트 내장 (`/api/auth/oauth2/callback`).
  - 외부 ID 기반 사용자 자동 프로비저닝 및 SQLion JWT 토큰 발급.
- [ ] **로컬 / S3 호환 파일 스토리지 모듈 (`pkg/api/handlers_files.go`)**
  - 멀티파트 파일 업로드 API (`POST /api/files/:table/:id`) 및 정적 파일 서빙.
  - 업로드된 이미지 파일의 비동기 썸네일 리사이징 및 MIME 타입 검증/용량 거버너.

---

## 10. High Availability, Always On & Event Streaming (HA 복제 & CDC)

- [ ] **Always On 스타일 자동 장애 조치 및 리더 선출 (Automatic Failover & Leader Election) (`pkg/replication`)**
  - Primary 노드 다운 시 복제 랙(LSN Lag)이 가장 적은 최신 Replica 노드를 새로운 Primary로 자동 승격 (Leader Promotion).
  - 클라이언트 라우팅 갱신 및 Split-Brain 방지를 위한 리더 텀(Term) 검증.
- [ ] **트랜잭셔널 아웃박스(Transactional Outbox) & CDC 이벤트 스트리밍 (`pkg/storage`, `pkg/api`)**
  - 트랜잭션 커밋과 100% 원자적으로 연동되는 Row-level 변경 델타(Inserted/Updated/Deleted) 이벤트 스트림.
  - 외부 메시지 브로커(Kafka, RabbitMQ) 또는 웹훅으로 무손실 발행하는 Outbox 디스패처.

---

## 11. Core Engine Hardening & Defensive Resources (자원 한도 방어 & 엔진 고도화)

- [ ] **Prepared Statement & PlanCache 메모리 방어용 Bounded LRU 캐시 (Golden Rule 29 준수) (`pkg/proc/runtime`, `pkg/wire`)**
  - PGWire, TDS 및 VDBE의 준비된 문장(`PlanCache`)에 세션당/글로벌 최대 용량(예: 256~1024건) 제한 및 LRU Eviction 메커니즘을 적용하여 장시간 연결에서의 메모리 누수 원천 방지.
- [ ] **Hash Join 대규모 데이터 메모리 방어선 (Grace Hash Join / Spill to TempDir) (`pkg/execution`)**
  - HashJoinIterator 빌드 테이블 적재 시 `work_mem` 한도를 초과할 경우 임시 디스크로 파티션 분할 처리하는 External Hash Join 지원.
- [ ] **Pure Go 영속 스토리지 엔진 옵션 (Pebble / bbolt) (`pkg/storage`)**
  - CGO/WiredTiger 의존성 없이 완전한 정적 바이너리(`CGO_ENABLED=0`, Golden Rule 86) 크로스 컴파일 배포를 위한 `storage.Engine` 플러그인 백엔드 추가.
- [ ] **`INSTEAD OF` 트리거 런타임 인터셉터 (`pkg/proc/runtime`, `pkg/api`)**
  - 뷰(View) 및 복합 테이블에 대한 `INSERT/UPDATE/DELETE` 작업을 가로채서 사용자 정의 프로시저/배치로 치환하는 런타임 DML 인터셉터 완비.
- [ ] **PostgreSQL 와이어 프로토콜 서버 사이드 커서 (`pkg/wire`)**
  - 대규모 쿼리 결과 집합을 분할 수신하는 드라이버 호환성을 위한 `DECLARE ... CURSOR FOR` 및 `FETCH n` 서버 사이드 커서 라이프사이클 지원.
- [ ] **Web Management Studio UI 내 바이트코드 디스어셈블러 뷰어 (`pkg/ui`)**
  - 웹 콘솔 UI에서 `EXPLAIN BYTECODE` 어셈블리 명령어와 오프코드 트레이스를 시각적으로 검사할 수 있는 어셈블리 뷰어 창 연동.


