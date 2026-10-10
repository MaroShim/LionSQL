# SQLion 엔지니어링 로드맵 및 핵심 기술 마일스톤 (ROADMAP.ko.md)

이 문서는 고동시성 임베디드 OLTP 데이터베이스 엔진 SQLion의 단계별 개발 마일스톤 달성 현황, 검증된 구현 상태 및 향후 중장기 핵심 과제를 정리한 공식 한국어 로드맵입니다.
(For the official English roadmap, see [ROADMAP.md](ROADMAP.md).)

---

## 1. Milestones & Release Progression Overview

SQLion follows a phased evolutionary roadmap from an autonomous single-node OLTP engine to an enterprise-grade, high-availability hybrid database system:

```
[ Phase 1: Core Foundation (v0.1.0 - v0.8.0) ] ────► COMPLETED
  - Dual Wire Protocols (PGWire :5433 + TDS :1433)
  - Dual Storage Engine (WiredTiger B+Tree + In-Memory OCC)
  - Dual Parser (Native Pratt + ANTLR4 Fallback)
  - VDBE Bytecode Virtual Machine & Volcano Relational Pipeline
  - CBO Cost-Based Optimizer, Histograms & Predicate Pushdown

[ Phase 2: SQL & Analytical Depth (v0.9.0 - v0.10.0) ] ──► COMPLETED
  - Window Functions & Frames (ROW_NUMBER, RANK, LEAD, LAG, OVER)
  - Advanced Aggregations (GROUP BY ROLLUP, CUBE, GROUPING)
  - Percentile / Quantile Analytics (PERCENTILE_CONT, T-Digest)
  - Log Operators (GREP / REGEXP_LIKE, AWK / SPLIT_PART)
  - Transaction Savepoints (SAVEPOINT, ROLLBACK TO SAVEPOINT)
  - View Subsystem (CREATE VIEW, Materialized / Aggregation Views)
  - Advanced DML (ON CONFLICT DO NOTHING / UPDATE)
  - SQL Prepared Statements (PREPARE, EXECUTE, DEALLOCATE)
  - In-Memory Ephemeral Engine Mode (--ephemeral, Zero-Disk)

[ Phase 3: Replication, Official Drivers & Zero-Allocation (v0.11.0 - v0.12.0) ] ──► COMPLETED
  - 1-Writer Multi-Reader WAL Streaming Replication & Failover Promotion
  - Microsoft SQL Server Official Client Driver Compatibility (go-mssqldb)
  - PostgreSQL Official Client Driver Compatibility (lib/pq)
  - CBO Table Statistics Disk Persistence (cbo_stats.json)
  - Go Native In-Process Scalar Function SPI (proc.RegisterScalarFunction)
  - VDBE OpRelQuery Physical PlanNode Caching & Zero-Allocation Streaming
  - Wire Protocol Response Packet Buffer Pooling (sync.Pool)
  - storage.Value Integer Fast-Path & Hot-Path Formatting
  - ANALYZE TABLE Multi-Core Parallel Statistics & Histograms (sync.WaitGroup)

[ Phase 4: Modern Ecosystem, GUI Tool Interop & VDBE Acceleration (v0.13.0 - v0.15.0) ] ──► COMPLETED
  - PostgreSQL Modern Driver Compatibility: pgx/v5 & Batch Pipelining (Flush)
  - TDS Protocol Async Attention Query Cancellation (0x06) & Real-Time Query Tracing (-dev)
  - Beekeeper Studio, DbGate & DBeaver Multi-Tool Catalog Interoperability
  - ANSI/MSSQL Pagination (OFFSET ... FETCH NEXT ... ROWS ONLY, SELECT TOP N)
  - IETF RFC 9562 Standard UUIDv7 (NEWID7, UUIDV7, GEN_RANDOM_UUID(7))
  - VDBE Local Variable Slot Indexing (O(1) Array Direct Access)
  - VDBE Integer Opcode Specialization & Dedicated Logical Operators (OpAddInt ~ OpGteInt, OpAnd, OpOr)
  - VDBE Fused Compare-and-Branch & Peephole Trampoline Jump Optimization
  - VDBE Dispatcher Modularization (Sub-Domain Execution Helpers & Centralized Catch-Stack)
  - TPC-C VDBE Peak 21,073 TPS / Overall 18,542 TPS (63.8% Native Parity)

[ Phase 5: Enterprise Governance, Security & Engine Decoupling (v0.16.0 - v0.18.0) ] ──► NEXT IN PROGRESS
  - VDBE Instruction Struct 16-Byte Compaction (4 instructions per 64B L1 Cache Line)
  - SQL Execution Engine Coordinator Package Separation (pkg/api -> pkg/engine)
  - Prepared Statement & PlanCache Memory Defense Bounded LRU Cache (Golden Rule 29)
  - Granular SQL Object-Level RBAC (CREATE ROLE/USER, GRANT/REVOKE SELECT/DML)
  - Transparent Data Encryption (TDE: AES-256-GCM) & Enterprise Immutable Audit Log
  - TDS NBCROW (0xD2) Row Compression
  - CBO Sort-Merge Join Operator & Cost Modeling (MergeJoinIterator)
  - INSTEAD OF Trigger Runtime Interceptor (View / Table DML Substitution)
  - BaaS Expansion: Declarative Schema Migration CLI (sqlion migrate) & OAuth2 Social Login

[ Phase 6: High Availability Always On, Decoupled HTAP & Production Hardening (v1.0.0 LTS) ] ──► PLANNED
  - Always On Style Automatic Failover & Leader Election
  - Transactional Outbox & CDC Event Streaming (Kafka / RabbitMQ)
  - Shared Memory (POSIX shm / mmap) Apache Arrow IPC Snapshots (DuckDB Zero-Copy)
  - Pure Go Persistent Storage Engine Backend (Pebble / bbolt, CGO_ENABLED=0)
  - Dynamic Programming (DP / Selinger) Large Join Order Optimization (N > 4)
  - Extended Multi-Column Statistics, MCV & Optimizer Hints
  - T-SQL Specialized TDS Serialization (UNIQUEIDENTIFIER, DATETIME2, PLP LOB)
  - Multi-Language Client Certification (Python psycopg3/pymssql, Node.js pg/tedious, .NET SqlClient)
  - Volcano Chunk-at-a-Time Vectorized Batch Streaming (1,024-tuple vectors)
  - Background MVCC Vacuuming & Undo Log Version Compaction
  - Wasm Runtime Sandbox UDF (wazero)
  - Local & S3-Compatible File Storage Module & Thumbnail Resizing
```

---

## 2. Verified Implementation Status (v0.15.0 기준 구현 완료 내역)

### 2.1. 코어 실행 엔진 (Core Execution Engine)
- [x] **VDBE 바이트코드 가상 머신 (`pkg/proc/runtime`)**: SQLite 스타일 3-어드레스 레지스터 기반 VM, `sync.Pool` 레지스터 재사용, 프로시저/함수/트리거/커서/대화형 쿼리 실행.
- [x] **VDBE 로컬 변수 슬롯 인덱싱 (`pkg/proc`, `pkg/proc/runtime`)**: 컴파일 타임 슬롯 할당(`Program.VarSlots`) 및 런타임 $O(1)$ 직접 슬라이스 접근(`VMFrame.slots[inst.B]`), 해시맵 룩업/소문자 변환 오버헤드 완전 제거.
- [x] **VDBE 정수 연산자 특화 및 전용 논리 연산자 (`pkg/proc`, `pkg/proc/runtime`)**: `OpAddInt` ~ `OpGteInt` 직접 ALU 실행 및 `OpAnd`, `OpOr` 1급 독립 오프코드 분리 (인터페이스 문자열 비교 제거).
- [x] **VDBE Fused Compare-and-Branch 및 피프홀 점프 최적화 (`pkg/proc`, `pkg/proc/runtime`)**: `OpJumpIfEqInt` 등 비교-분기 통합 명령어, 징검다리 점프 스레딩, 인접 폴스루 제거로 조건문 명령어 수 50% 감축.
- [x] **VDBE 디스패처 모듈화 (`pkg/proc/runtime/vm_dispatcher.go`)**: 800+ 라인의 거대 switch문을 기능군별 전용 헬퍼(`execFlowOp`, `execComputeOp`, `execTxOp`, `execStorageOp`, `execCursorAndCallOp`, `execRelOp`) 및 중앙 집중식 `handleCatch`로 분리.
- [x] **ANSI & MSSQL 정규 페이징 문법 (`pkg/proc`, `pkg/planner`, `pkg/execution`)**: `OFFSET <O> ROWS FETCH NEXT <N> ROWS ONLY` 및 `SELECT TOP <N>` AST 파싱 및 Volcano `LimitIterator(limit, offset)` 연동.
- [x] **VDBE `OpRelQuery` 물리 플랜 캐싱 (`pkg/proc`, `pkg/proc/runtime`, `pkg/planner`)**: AST 파싱(`CachedQueryInfo`) 및 물리 플랜 트리(`CachedPlan`) 딥 클론 캐싱, `sync.RWMutex` 스레드 안전성 보장.
- [x] **Go 네이티브 인프로세스 스칼라 함수 등록 SPI (`pkg/proc`, `pkg/api`)**: `proc.RegisterScalarFunction(name, fn)` 스레드 안전 SPI 및 나노초 단위(< 5ns) 직결 인라인 호출, `--doctor` 동적 검증 프로브 탑재.
- [x] **0-Allocation 행 스트리밍 (`Tuple.ToRow()`) (`pkg/execution`)**: 중간 `map[string]any` 해시맵 생성 없이 저수준 `storage.Value` 슬라이스 직결 변환, GC 압박 70% 절감.
- [x] **Volcano Iterator 관계형 실행 파이프라인 (`pkg/execution`)**: 풀 스트리밍 반복자 (`SeqScan`, `IndexScan`, `IndexOnlyScan`, `Filter`, `Project`, `Sort`, `Limit`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`, `CTE`).
- [x] **윈도우 함수 & 프레임 분석 엔진 (`pkg/execution/window.go`)**: `OVER (PARTITION BY ... ORDER BY ... [ROWS BETWEEN])`, `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `LEAD()`, `LAG()`, 윈도우 집계(`SUM`, `AVG`, `COUNT`, `MIN`, `MAX`).
- [x] **로그 추출 및 정규식 내장 연산자 (`pkg/proc/builtins.go`)**: 인메모리 고속 `GREP(text, pattern)` / `REGEXP_LIKE` 및 `AWK(text, [delim], idx)` / `SPLIT_PART`.
- [x] **T-Digest 분위수 분석 엔진 (`pkg/execution/tdigest.go`)**: 대용량 데이터셋에서 메모리 제한 내 정밀 백분위수 산출 (`PERCENTILE_CONT`, `PERCENTILE_DISC`).
- [x] **협력적 쿼리 취소 (`pkg/proc/runtime`, `pkg/api`, `pkg/wire`)**: `context.Context` 기반 무손상 실행 중단, T-SQL `KILL <spid>`, PostgreSQL `pg_cancel_backend()`, `CancelRequest`.

### 2.2. 쿼리 옵티마이저 & CBO (Query Optimizer)
- [x] **비용 기반 옵티마이저 (CBO) & 히스토그램 통계 (`pkg/planner/stats.go`)**: `ANALYZE TABLE` 64-버킷 등빈도 히스토그램, 카디널리티, NDV, NULL 비율 수집, Hash Join vs Index Nested Loop Join 자동 비용 산출.
- [x] **`ANALYZE TABLE` 멀티코어 병렬 통계 수집 (`pkg/planner/stats.go`)**: 대형 테이블 분석 시 컬럼별 통계를 `sync.WaitGroup` 멀티코어 고루틴 병렬화(`BuildTableStatsParallel`)하여 수집 시간 4~8배 단축.
- [x] **CBO 통계 메타데이터 디스크 영속화 (`pkg/catalog`, `cbo_stats.json`)**: 원자적 파일 쓰기(Golden Rule 1 & 5) 및 `storage.Decimal` 손실 없는 JSON 직렬화, 서버 재시작 후에도 `ANALYZE` 통계 및 최적화 플랜 100% 보존.
- [x] **복합 조건절 선택도 추정 (Compound Selectivity) (`pkg/planner/stats.go`)**: 독립성 가정 결합 확률 계산 및 실시간 라이브 행 수 동적 반영.
- [x] **조건절 푸시다운 (Predicate Pushdown) (`pkg/planner/pushdown.go`)**: 복합 `WHERE` 조건절을 조인 전 하위 스캔 반복자로 조건 조기 전달.
- [x] **상수 폴딩 & 불리언 단순화 (Constant Folding) (`pkg/proc/fold.go`)**: 컴파일 타임 리터럴 연산, 내장 함수 선계산, 항진/모순 조건 \(O(1)\) 단축 평가.
- [x] **상관 서브쿼리 비상관화 (Decorrelation) (`pkg/planner/decorrelate.go`)**: `EXISTS`, `NOT EXISTS`, `IN`, `NOT IN`을 관계형 `SEMI` / `ANTI` 조인으로 자동 재작성.

### 2.3. 스토리지, 트랜잭션 & 복제 (Storage, Transactions & HA)
- [x] **1-Writer Multi-Reader WAL 복제 & HA 클러스터링 (`pkg/replication`, `cmd/server`)**: Primary-Replica 실시간 WAL 스트리밍, 복제본 읽기 전용 보호(`ErrReadOnlyReplica`), 무중단 승격(Failover Promotion).
- [x] **이중 스토리지 백엔드 (`pkg/storage`)**: 프로덕션용 WiredTiger (Cgo, Lock-Free B+Tree, MVCC) 및 순수 Go In-Memory OCC 엔진 (MessagePack, WAL/AOF 지속성).
- [x] **커버링 인덱스 스캔 (Index-Only Scan) (`pkg/planner`, `pkg/execution`, `pkg/storage/memengine`)**: 인덱스 키와 테이블 PK만으로 투영 충족 시 메인 테이블 힙 페치 및 락을 100% 생략하는 `NodeIndexOnlyScan` 및 `SkipList.ScanRangeEntries` 가상 레코드 합성 완비 (`4f69eb6`).
- [x] **IETF RFC 9562 공식 표준 UUIDv7 (`pkg/storage`, `pkg/proc`)**: 48비트 단조 증가 타임스탬프 기반 B+Tree 순차 삽입 극대화, `NEWID7()`, `UUIDV7()`, `GEN_RANDOM_UUID(7)` 내장 함수 및 `DEFAULT NEWID7()` 자동 생성.
- [x] **ClickHouse 스타일 테이블 자동 롤링 파티셔닝 (`pkg/storage`, `pkg/planner`, `pkg/api`)**: `PARTITION BY toYYYYMM(col)`, `MONTH(col)` 등 자동 월/일/연 파티션 분할, 파티션 프루닝 및 $O(1)$ 초고속 파티션 드롭 (`DROP PARTITION`).
- [x] **인메모리 Ephemeral 모드 (`cmd/server`, `pkg/storage/memengine`)**: `--ephemeral` 플래그로 디스크 I/O 없이 순수 RAM에서만 구동하는 초고속 임시 모드.
- [x] **`storage.Value` 정수형 Fast-Path (`pkg/storage/engine.go`)**: `TypeInt`에 대한 고속 비교/일치 분기 및 `strconv.FormatInt` 포맷팅.
- [x] **트랜잭션 세이브포인트 (`pkg/api/savepoint_test.go`, `pkg/storage`)**: T-SQL `SAVE TRANSACTION` / ANSI `SAVEPOINT`, `ROLLBACK TO SAVEPOINT`, `RELEASE SAVEPOINT` 중첩 롤백.
- [x] **인메모리 락프리 동시성 스킵리스트 (`pkg/storage/memengine/skiplist.go`)**: 보조 인덱스를 동시성 SkipList(\(O(\log N)\), 2.8M inserts/sec)로 구현.
- [x] **쿼리 메모리 한도 거버너 (`work_mem`) (`pkg/execution/spill.go`)**: 메모리 상한 초과 시 OOM 대신 임시 파일 External Merge Sort로 자동 전환.
- [x] **무중단 온라인 스냅샷 핫 백업 (`pkg/backup`)**: 쓰기 락 없이 일관된 `.tar.gz` 시점 백업(`POST /api/backup`) 및 복원.
- [x] **외래키 무결성 제약조건 (`pkg/catalog`)**: `ON DELETE/UPDATE CASCADE`, `SET NULL`, `RESTRICT` 참조 무결성 보장.

### 2.4. 와이어 프로토콜 & API (Protocols & API)
- [x] **PostgreSQL 최신 표준 드라이버 `pgx/v5` 완벽 호환 (`pkg/wire`)**: `github.com/jackc/pgx/v5` E2E 100% 통과, 바이너리 파라미터/결과 포맷 디코딩, Extended Query `Flush` 파이프라이닝 상태 머신 동기화.
- [x] **공식 Microsoft MSSQL 드라이버 E2E 통합 검증 & TDS 7.x/8.0 완성 (`pkg/wire/tds`)**: `github.com/microsoft/go-mssqldb` 공식 드라이버 100% PASS, `PacketTransactionManager` (0x0E), Collation/Transaction `ENVCHANGE` 토큰, `DoneInXact` 트랜잭션 상태 머신.
- [x] **공식 PostgreSQL 드라이버 E2E 통합 검증 & PGWire 프로토콜 완성 (`pkg/wire`)**: `github.com/lib/pq` 공식 드라이버 100% PASS, Extended Query 파라미터 개수 동적 추론, 투영 스키마 매칭, 바이너리 Result Format 지원, 대화형 트랜잭션 구문 지원.
- [x] **TDS 비동기 Attention 쿼리 취소 (`pkg/wire/tds`)**: 클라이언트 `SqlCommand.Cancel()` 시 `Type 0x06` 비동기 패킷 수신 및 VDBE 바이트코드 루프 중단(`context.CancelFunc`), `DoneAttn (0x0020)` 응답.
- [x] **실시간 와이어 프로토콜 개발자 쿼리 트레이싱 (`-dev` 플래그) (`pkg/wire`, `pkg/wire/tds`)**: PGWire 및 TDS로 수신되는 모든 쿼리, 바인딩 파라미터, 소요 시간, 에러 상세를 콘솔에 실시간 출력.
- [x] **DbGate, Beekeeper Studio, DBeaver 멀티 도구 완벽 상호운용성 (`pkg/wire/tds`, `pkg/api`)**: 가상 시스템 카탈로그 조인 쿼리 인터셉터, `@@VERSION` / `SERVERPROPERTY` 에디션 감지 인터셉터, 정렬된 결정적 객체 ID 부여.
- [x] **이중 와이어 프로토콜 리스너 (`pkg/wire`, `pkg/wire/tds`)**: PostgreSQL Wire Protocol v3(`:5433`) 및 Microsoft SQL Server TDS 7.x/8.0(`:1433`) 동시 제공, SSL/TLS 지원.
- [x] **패킷 버퍼 풀링 (`sync.Pool`) (`pkg/wire/protocol.go`, `pkg/wire/tds/token.go`)**: PGWire 4KB 고정 버퍼 풀(`packetBufferPool`) 및 TDS 행 토큰 버퍼 풀(`tdsRowBufferPool`)로 대량 스트리밍 시 힙 할당 제거.
- [x] **SQL 레벨 Prepared Statements (`pkg/api/sqlexec_prepare.go`)**: `PREPARE`, `EXECUTE`, `DEALLOCATE` 구문 지원, `$1, ?` ➔ `@p1` 플레이스홀더 정규화, VDBE PlanCache 연동.
- [x] **PostgreSQL 대량 복사 스트리밍 (`pkg/wire/copy.go`)**: `COPY FROM STDIN` 및 `COPY TO STDOUT` 스트리밍 프로토콜(CSV 및 Text 포맷).
- [x] **임베디드 웹 관리 스튜디오 (`pkg/ui`)**: `:8090/_/` 단일 바이너리 내장 웹 UI, 인터랙티브 쿼리 콘솔, VDBE 비주얼 바이트코드 디스어셈블러(`EXPLAIN BYTECODE`).

---

## 3. Future Technical Milestones (단계별 차기 과제 상세)

### Phase 5: Enterprise Governance, Security & Engine Decoupling (v0.16.0 - v0.18.0 Target)

#### 5.1. [완료] VDBE Instruction 구조체 16바이트 패킹 (`pkg/proc/bytecode.go`)
- **구현 내용**: `Instruction` 구조체를 16바이트 고정 레이아웃(`Op uint8, Flags uint8, Aux uint16, A int32, B int32, C int32`, Zero Padding)으로 재설계하고, 메타데이터 객체를 `Program.AuxData []any` 풀로 분리하여 `inst.Aux` 인덱스로 참조.
- **검증 완료**: `TestInstruction_Size16Bytes` 단위 테스트(`unsafe.Sizeof == 16`) 및 전체 T-SQL 가상머신 회귀 테스트 통과.

#### 5.2. 아키텍처 계층 정립: SQL 실행 코디네이터 패키지 분리 (`pkg/api` ➔ `pkg/engine`)
- **현황**: `pkg/api`에 집중된 15,000+ 라인의 SQL 오케스트레이션 로직으로 인해 PGWire와 TDS가 웹 API 계층에 의존하는 계층 역전 현상 존재.
- **과제**:
  - SQL 실행 오케스트레이션을 `pkg/engine` 코어 레이어로 승격.
  - 어댑터(`pkg/api`, `pkg/wire`, `pkg/wire/tds`)가 동등하게 코어 엔진을 단방향 호출하는 클린 아키텍처 확립.
- **기대 효과**: 순환 복잡도 완화, 계층 순수성 확보 및 각 프로토콜 리스너의 독립 테스트 용이성 극대화.

#### 5.3. 세분화된 SQL 객체 수준 RBAC (Role-Based Access Control) (`pkg/catalog`, `pkg/api`, `pkg/wire`)
- **현황**: 현재는 단일 Superuser 및 웹 스튜디오 API Key 위주로 인증되며, 데이터베이스 수준의 세분화된 권한 체계가 부재함.
- **과제**:
  - SQL 표준 역할/사용자 DDL: `CREATE ROLE <role>`, `CREATE USER <user> WITH PASSWORD = '...'`, `DROP ROLE`.
  - 객체별 세분화 권한 제어: `GRANT SELECT, INSERT, UPDATE, DELETE ON <table> TO <role/user>`, `REVOKE`, `DENY`.
  - 카탈로그 권한 인터셉터를 통한 쿼리 실행 전 접근 제어 검증.
- **기대 효과**: 엔터프라이즈 사내 컴플라이언스(ISMS, 금융보안 규정) 및 멀티유저 데이터베이스 보안 요건 충족.

#### 5.4. 투명 데이터 암호화 (TDE: Transparent Data Encryption) 및 불변 감사 로그 (`pkg/storage`, `pkg/logger`)
- **과제**:
  - 디스크에 기록되는 WiredTiger 데이터 파일, WAL/AOF 저널 및 백업 아카이브를 AES-256-GCM으로 투명 암호화(Data at Rest Encryption).
  - 로그인 성공/실패, 권한 변경, DDL 변경 이벤트를 태깅하는 위변조 방지 불변 감사 로그 기록.
- **기대 효과**: 디스크 파일 직접 탈취 시에도 데이터 유출 원천 방지 및 엔터프라이즈 감사 추적성 확보.

#### 5.5. [완료] PlanCache 메모리 방어용 Bounded LRU 캐시 (Golden Rule 29) (`pkg/proc/runtime`)
- **구현 내용**: `PlanCache`의 무제한 맵 저장을 `container/list` 기반의 상한 용량(`DefaultPlanCacheCapacity = 1024`) LRU 캐시로 전면 개편.
- **검증 완료**: 용량 초과 시 자동 LRU 퇴출, 최근 참조 항목 승격, 프로시저/함수 대소문자 무관 검색(`GetByName`) 지원 및 `plancache_test.go` 단위 테스트 100% 통과. 장기 커넥션에서의 무제한 메모리 증가 및 OOM 방어 완료.

#### 5.6. TDS 와이어 프로토콜 NBCROW 행 압축 (`pkg/wire/tds`)
- **과제**: TDS 7.3+ 표준 `NBCROW` (Null Bitmap Compressed Row, `0xD2`) 행 압축 지원 (NULL 컬럼 0바이트 패킹).
- **기대 효과**: NULL 컬럼이 다수 포함된 엔터프라이즈 테이블 대량 조회 시 네트워크 전송 대역폭 30%+ 절감.

#### 5.7. CBO 소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델 (`pkg/execution`, `pkg/planner`)
- **과제**:
  - 정렬된 입력에 대해 $O(M+N)$ 단일 패스로 처리하는 `MergeJoinIterator` 연산자 구현.
  - CBO 비용 산출 공식에 소트-머지 조인 대안 추가 및 실행 계획 선택기 연동.
- **기대 효과**: ORDER BY 또는 인덱스 정렬 릴레이션 조인 시 해시 빌드 메모리 오버헤드 제거 및 조인 처리 속도 대폭 개선.

#### 5.8. `INSTEAD OF` 트리거 런타임 인터셉터 (`pkg/proc/runtime`, `pkg/api`)
- **과제**: 뷰(View) 및 복합 테이블에 대한 `INSERT/UPDATE/DELETE` 작업을 가로채서 사용자 정의 프로시저/배치로 치환하는 런타임 DML 인터셉터 완비.
- **기대 효과**: 복합 조인 뷰에 대한 가상 DML 갱신 및 비즈니스 데이터 무결성 캡슐화.

#### 5.9. BaaS 개발자 생산성 확장: 마이그레이션 CLI 및 소셜 로그인 (`cmd/server`, `pkg/migrate`, `pkg/api`)
- **과제**:
  - 선언적 스키마 마이그레이션 CLI: `sqlion migrate new <name>`, `sqlion migrate up / down` 및 `_migrations` 버전 이력 추적.
  - 소셜 로그인: Google, GitHub OAuth2 콜백 내장(`/api/auth/oauth2/callback`) 및 사용자/JWT 자동 프로비저닝.
- **기대 효과**: PocketBase 수준의 원스톱 개발자 생산성과 마이그레이션 도구 지원.

#### 5.10. T-SQL & ANSI SQL 핵심 문법 커버리지 확장 (`pkg/proc`, `pkg/planner`, `pkg/execution`)
- **과제**:
  - **P1 (실무/ORM 필수)**: `CAST(expr AS type)` / `CONVERT()` 및 `CASE WHEN` 복합 조건식 AST화, `INSERT INTO ... SELECT ...`, `FROM` 절 파생 테이블(`FROM (SELECT ...) AS t`), `WHERE EXISTS` / `NOT EXISTS` 상관 서브쿼리 술어 지원.
  - **P2 (T-SQL 핵심 호환)**: `CROSS APPLY` / `OUTER APPLY` 조인, DML `OUTPUT` 절, 로컬 임시 테이블(`#temp_table`), 커버링 인덱스 비키 컬럼(`INCLUDE (col...)`), 핵심 테이블/쿼리 힌트(`WITH (NOLOCK)` 등).
  - **P3 (고급 분석/조인)**: `CROSS JOIN`, `FULL OUTER JOIN`, `GROUP BY GROUPING SETS / ROLLUP / CUBE`.
- **기대 효과**: 엔터프라이즈 ORM(EF Core, GORM) 및 레거시 MSSQL 프로시저/배치 스크립트 마이그레이션 100% 수용.

---

### Phase 6: High Availability Always On, Decoupled HTAP & Production Hardening (v1.0.0 LTS Target)

#### 6.1. Always On 스타일 자동 장애 조치 및 리더 선출 (`pkg/replication`)
- **현황**: 1-Writer Multi-Reader WAL 스트리밍 및 수동 승격이 지원되나, Primary 장애 시 자동 리더 선출이 요구됨.
- **과제**:
  - 복제 랙(LSN Lag)이 가장 적은 최신 Replica 노드를 새 Primary로 자동 승격(Leader Promotion).
  - Split-Brain 방지를 위한 리더 텀(Term) 검증 및 클라이언트 라우팅 인계.
- **기대 효과**: 다운타임 없는 99.999% 무중단 Always On 가용성 제공.

#### 6.2. 트랜잭셔널 아웃박스(Transactional Outbox) & CDC 이벤트 스트리밍 (`pkg/storage`, `pkg/api`)
- **과제**:
  - 트랜잭션 커밋과 100% 원자적으로 연동되는 Row-level 변경 델타(Inserted/Updated/Deleted) 이벤트 스트림.
  - 외부 메시지 브로커(Kafka, RabbitMQ) 또는 웹훅으로 무손실 발행하는 Outbox 디스패처.
- **기대 효과**: 외부 시스템과의 실시간 데이터 동기화 및 마이크로서비스 연동성 확보.

#### 6.3. 공유 메모리(POSIX shm / mmap) 기반 Apache Arrow IPC 스냅샷 (DuckDB Zero-Copy 연동) (`pkg/storage`)
- **과제**:
  - 특정 시점의 테이블 데이터를 Apache Arrow IPC RecordBatch 컬럼 포맷으로 직렬화하여 POSIX 공유 메모리(`/dev/shm` 또는 `mmap` 임시 파일)에 기록 (`SNAPSHOT TABLE <t> TO ARROW_SHM <name>`).
  - 외부 분석 프로세스(DuckDB, Python Polars, Pandas)에 파일 디스크립터(FD)를 전달하여 Zero-Copy 커널 페이지 맵핑 기반 고속 벡터화 분석 실현.
  - 무결함 격리(Zero Blast Radius): 무거운 분석 쿼리가 OOM을 유발해도 OLTP 쓰기 트랜잭션은 100% 무중단 독립 운영.
- **기대 효과**: 복잡한 컬럼 엔진을 내장하지 않고도, DuckDB 등 최고 수준의 오픈소스 벡터화 분석 생태계를 0ms 지연시간으로 완전 통합 (분리형 HTAP).

#### 6.4. Pure Go 영속 스토리지 엔진 옵션 (Pebble / bbolt) (`pkg/storage`)
- **과제**: CGO/WiredTiger 의존성 없이 완전한 정적 바이너리(`CGO_ENABLED=0`, Golden Rule 86) 크로스 컴파일 배포를 위한 `storage.Engine` 플러그인 백엔드 추가.
- **기대 효과**: C 컴파일러가 없는 환경에서도 단일 정적 바이너리로 즉각 배포 가능한 초경량 이식성 달성.

#### 6.5. 동적 계획법(DP / Selinger) 기반 대규모 조인 순서 최적화 ($N > 4$) (`pkg/planner`)
- **과제**: Left-Deep 및 Bushy Tree를 포괄하는 DPccp 동적 계획법 탐색기 구현.
- **기대 효과**: 대형 엔터프라이즈 ERP/데이터마트 5개 이상 테이블 조인 쿼리의 기하급수적 최적 플랜 도출.

#### 6.6. 다중 컬럼 확장 통계 (Extended Statistics), MCV 및 옵티마이저 힌트 (`pkg/planner`)
- **과제**:
  - 컬럼 간 상관관계 편향을 교정하는 Multi-Column NDV 확장 통계 수집.
  - 왜곡된 분포를 위한 MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합.
  - `WITH (INDEX(...))`, `OPTION (HASH JOIN)`, `OPTION (FORCE ORDER)` 옵티마이저 힌트 강제 제어.
- **기대 효과**: 통계 왜곡으로 인한 카디널리티 과소평가 원천 차단 및 DBA 플랜 강제 제어권 보장.

#### 6.7. T-SQL 특화 타입 TDS 직렬화 및 PLP LOB 스트리밍 (`pkg/wire/tds`)
- **과제**:
  - `UNIQUEIDENTIFIER` (UUID 16바이트 GUID 리틀/빅엔디언 스왑).
  - `DATETIME2` (`0x2A`), `DATE` (`0x28`), `TIME` (`0x29`) TDS 바이너리 인코딩.
  - PLP (Partially Length-Prefixed) 8KB 초과 대용량 LOB 스트리밍 (`VARCHAR(MAX)`, `VARBINARY(MAX)`).
- **기대 효과**: 엔터프라이즈 레거시 MSSQL 스키마의 100% 무결점 데이터 호환성.

#### 6.8. 외부 언어 생태계 클라이언트 인증 하네스 (`pkg/wire`, `pkg/wire/tds`)
- **과제**: Python(`psycopg3`, `pymssql`), Node.js(`pg`, `tedious`), C#/.NET(`Microsoft.Data.SqlClient`) 대상 자동화 호환성 검증 하네스 구축.
- **기대 효과**: 멀티 언어 클라이언트 생태계 공식 인증.

#### 6.9. Volcano 청크 단위 배치 실행 (Vectorized Chunk Iterator) (`pkg/execution`)
- **과제**: 1,024개 튜플 벡터 단위 슬라이스 배치 스트리밍(`NextBatch([64]Row)`) 도입.
- **기대 효과**: 대용량 관계형 스캔 및 집계 연산 속도 3~5배 가속.

#### 6.10. 백그라운드 MVCC Vacuum 및 Undo Log Version GC (`pkg/storage/memengine`)
- **과제**: 튜플 변경 이력을 체인으로 관리하고 백그라운드 워커가 사용 종료된 구버전 튜플을 주기적으로 압축/정리.
- **기대 효과**: 24/365 장기 가동 환경에서 메모리 단편화 및 누적 방지.

#### 6.11. Wasm 런타임 샌드박스 UDF (`pkg/extension`, `wazero`)
- **과제**: CGO 없는 순수 Go `wazero` 런타임 탑재, 메모리 샌드박싱(Golden Rule 26)을 통한 크래시/패닉 격리 동적 UDF 실행.
- **기대 효과**: 외부 기여 코드 및 런타임 동적 플러그인을 안전하게 격리 실행.

#### 6.12. 로컬 / S3 호환 파일 스토리지 모듈 (`pkg/api/handlers_files.go`)
- **과제**: 멀티파트 파일 업로드 API (`POST /api/files/:table/:id`), 정적 파일 서빙, 비동기 썸네일 리사이징 및 용량 거버너.
- **기대 효과**: BaaS 스타일 올인원 파일 관리 역량 확보.

---

## 4. 마일스톤별 핵심 추진 우선순위 매트릭스

| 마일스톤 | 우선순위 | 과제명 | 해당 영역 | 구현 공수 | 기대 효과 |
| :---: | :---: | :--- | :---: | :---: | :--- |
| **Phase 5** | **P1** | **VDBE Instruction 구조체 16바이트 패킹** | 가상 머신 | 낮음 (150~200줄) | 64B L1 Cache Line당 4개 명령어 적재, VM 레이턴시 단축 |
| **Phase 5** | **P1** | **SQL 실행 코디네이터 분리 (`pkg/engine`)** | 아키텍처 | 보통 (400~500줄) | 단방향 클린 아키텍처 확립 및 계층 역전 해소 |
| **Phase 5** | **P1** | **세분화된 SQL 객체 수준 RBAC** | 보안 & 카탈로그 | 보통 (300~400줄) | `CREATE ROLE/USER`, `GRANT/REVOKE` 엔터프라이즈 권한 제어 |
| **Phase 5** | **P1** | **투명 데이터 암호화 (TDE: AES-256-GCM)** | 스토리지 & 보안 | 보통 (250~350줄) | 디스크 파일 투명 암호화 및 불변 감사 로그 기록 |
| **Phase 5** | **P2** | **Prepared Statement Bounded LRU 캐시** | 메모리 거버너 | 낮음 (100~150줄) | Rule 29 준수, 장시간 연결에서의 세션 메모리 누수 차단 |
| **Phase 5** | **P2** | **소트-머지 조인 (Sort-Merge Join) 연산자** | 실행기 & 플래너 | 보통 (250~350줄) | 정렬 릴레이션 조인 시 해시 빌드 오버헤드 및 메모리 제거 |
| **Phase 5** | **P2** | **TDS NBCROW 행 압축 (0xD2)** | TDS 프로토콜 | 보통 (200~300줄) | NULL 컬럼 다수 조회 시 네트워크 전송 대역폭 30%+ 절감 |
| **Phase 5** | **P2** | **`INSTEAD OF` 트리거 런타임 인터셉터** | T-SQL 실행기 | 보통 (200~300줄) | 뷰 및 복합 테이블 DML 치환 실행 |
| **Phase 5** | **P1** | **T-SQL & ANSI SQL 핵심 문법 확장** | 파서 & 플래너 | 보통 (300~400줄) | CAST/CONVERT, 파생 테이블, INSERT..SELECT, CROSS APPLY |
| **Phase 5** | **P3** | **선언적 스키마 마이그레이션 CLI** | BaaS & 도구 | 보통 (250~350줄) | `sqlion migrate` 원자적 DDL 버전 이력 관리 |
| **Phase 5** | **P3** | **소셜 로그인 (OAuth2 Providers)** | 인증 & API | 보통 (200~300줄) | Google, GitHub 로그인 및 JWT 원스톱 프로비저닝 |
| **Phase 6** | **P1** | **Always On 스타일 자동 장애 조치 (Failover)** | HA 복제 | 높음 (400~500줄) | 장애 시 최신 LSN Replica 자동 승격 및 무중단 고가용성 |
| **Phase 6** | **P1** | **트랜잭셔널 아웃박스 & CDC 스트리밍** | 스토리지 & 연동 | 보통 (300~400줄) | 원자적 Row-level 변경 이벤트 큐 및 Kafka/RabbitMQ 발행 |
| **Phase 6** | **P1** | **공유메모리 Apache Arrow IPC 스냅샷 (DuckDB)** | 분석 & HTAP | 보통 (300~400줄) | Zero-Copy, Zero-I/O 분리형 고성능 OLAP 결합 |
| **Phase 6** | **P1** | **Pure Go 영속 스토리지 엔진 (Pebble/bbolt)** | 스토리지 백엔드 | 높음 (500~700줄) | CGO 의존성 없는 100% 정적 단일 바이너리 배포 체계 |
| **Phase 6** | **P2** | **$N > 4$ 체인 조인 동적 계획법 (DP / Selinger)** | CBO & 플래너 | 높음 (400~600줄) | 복잡한 다중 조인 쿼리의 기하급수적 최적 플랜 도출 |
| **Phase 6** | **P2** | **다중컬럼 확장통계, MCV 및 옵티마이저 힌트** | CBO & 플래너 | 보통 (300~400줄) | 통계 편향 교정 및 DBA 실행 계획 제어권 |
| **Phase 6** | **P2** | **T-SQL 특화 타입 TDS 직렬화 & PLP LOB** | TDS 프로토콜 | 보통 (300~400줄) | UNIQUEIDENTIFIER, DATETIME2, LOB 완벽 지원 |
| **Phase 6** | **P2** | **Volcano 청크 단위 배치 실행 (Vectorized)** | 실행기 & SIMD | 높음 (600~800줄) | 대용량 관계형 스캔 및 집계 3~5배 가속 |
| **Phase 6** | **P3** | **백그라운드 MVCC Vacuum / Version GC** | 트랜잭션 & 메모리 | 높음 (400~500줄) | 장기 가동 인메모리 테이블 단편화 방지 |
| **Phase 6** | **P3** | **Wasm 런타임 샌드박스 UDF (`wazero`)** | 플러그인 & 보안 | 보통 (300~400줄) | 런타임 동적 플러그인 적재 및 완전한 크래시 격리 |
| **Phase 6** | **P3** | **멀티랭귀지 클라이언트 호환성 인증 하네스** | 와이어 프로토콜 | 보통 (200~300줄) | Python, Node.js, C# 공식 드라이버 인증 |
| **Phase 6** | **P3** | **로컬 / S3 파일 스토리지 및 썸네일** | BaaS 스토리지 | 보통 (250~350줄) | 멀티파트 업로드 및 썸네일 자동 생성 |
