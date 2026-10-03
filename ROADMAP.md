# SQLion Engineering Roadmap & Core Technical Milestones

This document charts the development milestones, verified implementation status, and future engineering challenges for the SQLion high-concurrency database engine.

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

[ Phase 3: Replication & Engine Zero-Allocation (v0.11.0) ] ──► CURRENT (COMPLETED)
  - 1-Writer Multi-Reader WAL Streaming Replication & Failover Promotion
  - VDBE OpRelQuery Physical PlanNode Caching & Zero-Allocation Streaming
  - Wire Protocol Response Packet Buffer Pooling (sync.Pool)
  - storage.Value Integer Fast-Path & Hot-Path Formatting

[ Phase 4: Observability, Parallelism & Dual UDF SPI (v0.12.0) ] ──► NEXT IN PROGRESS
  - Embedded Web Studio HA Replication Dashboard & Failover UI
  - Client-Side Read-Scale Load Balancing & Routing Interfaces
  - ANALYZE TABLE Multi-Core Parallel Statistics & Histograms
  - CREATE INDEX Background Parallel Sorting & Bulk Loading
  - Dual-Track UDF SPI: Go Native In-Process API + Wasm Sandbox (wazero)

[ Phase 5: Decoupled HTAP & Production Hardening (v1.0.0 LTS) ] ──► PLANNED
  - Shared Memory (POSIX shm / mmap) Apache Arrow IPC Snapshots (DuckDB Zero-Copy)
  - WiredTiger Thread-Local Cursor & Session Pooling Optimization
  - Volcano Chunk-at-a-Time Vectorized Batch Streaming (1,024-tuple vectors)
  - Background MVCC Vacuuming & Undo Log Version Compaction
  - Real-Time CDC Streaming (Kafka / NATS Event Sourcing)
```

---

## 2. Verified Implementation Status (v0.11.0 기준 구현 완료 내역)

### 2.1. 코어 실행 엔진 (Core Execution Engine)
- [x] **VDBE 바이트코드 가상 머신 (`pkg/proc/runtime`)**: SQLite 스타일 3-어드레스 레지스터 기반 VM, `sync.Pool` 레지스터 재사용, 프로시저/함수/트리거/커서/대화형 쿼리 실행.
- [x] **VDBE `OpRelQuery` 물리 플랜 캐싱 (`pkg/proc`, `pkg/proc/runtime`, `pkg/planner`)**: AST 파싱(`CachedQueryInfo`) 및 물리 플랜 트리(`CachedPlan`) 딥 클론 캐싱, `sync.RWMutex` 스레드 안전성 보장.
- [x] **0-Allocation 행 스트리밍 (`Tuple.ToRow()`) (`pkg/execution`)**: 중간 `map[string]any` 해시맵 생성 없이 저수준 `storage.Value` 슬라이스 직결 변환, GC 압박 70% 절감.
- [x] **Volcano Iterator 관계형 실행 파이프라인 (`pkg/execution`)**: 풀 스트리밍 반복자 (`SeqScan`, `IndexScan`, `IndexOnlyScan`, `Filter`, `Project`, `Sort`, `Limit`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`, `CTE`).
- [x] **윈도우 함수 & 프레임 분석 엔진 (`pkg/execution/window.go`)**: `OVER (PARTITION BY ... ORDER BY ... [ROWS BETWEEN])`, `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `LEAD()`, `LAG()`, 윈도우 집계(`SUM`, `AVG`, `COUNT`, `MIN`, `MAX`).
- [x] **로그 추출 및 정규식 내장 연산자 (`pkg/proc/builtins.go`)**: 인메모리 고속 `GREP(text, pattern)` / `REGEXP_LIKE` 및 `AWK(text, [delim], idx)` / `SPLIT_PART`.
- [x] **T-Digest 분위수 분석 엔진 (`pkg/execution/tdigest.go`)**: 대용량 데이터셋에서 메모리 제한 내 정밀 백분위수 산출 (`PERCENTILE_CONT`, `PERCENTILE_DISC`).
- [x] **협력적 쿼리 취소 (`pkg/proc/runtime`, `pkg/api`, `pkg/wire`)**: `context.Context` 기반 무손상 실행 중단, T-SQL `KILL <spid>`, PostgreSQL `pg_cancel_backend()`, `CancelRequest`.

### 2.2. 쿼리 옵티마이저 & CBO (Query Optimizer)
- [x] **비용 기반 옵티마이저 (CBO) & 히스토그램 통계 (`pkg/planner/stats.go`)**: `ANALYZE TABLE` 64-버킷 등빈도 히스토그램, 카디널리티, NDV, NULL 비율 수집, Hash Join vs Index Nested Loop Join 자동 비용 산출.
- [x] **복합 조건절 선택도 추정 (Compound Selectivity) (`pkg/planner/stats.go`)**: 독립성 가정 결합 확률 계산 및 실시간 라이브 행 수 동적 반영.
- [x] **조건절 푸시다운 (Predicate Pushdown) (`pkg/planner/pushdown.go`)**: 복합 `WHERE` 조건절을 조인 전 하위 스캔 반복자로 조건 조기 전달.
- [x] **상수 폴딩 & 불리언 단순화 (Constant Folding) (`pkg/proc/fold.go`)**: 컴파일 타임 리터럴 연산, 내장 함수 선계산, 항진/모순 조건 \(O(1)\) 단축 평가.
- [x] **상관 서브쿼리 비상관화 (Decorrelation) (`pkg/planner/decorrelate.go`)**: `EXISTS`, `NOT EXISTS`, `IN`, `NOT IN`을 관계형 `SEMI` / `ANTI` 조인으로 자동 재작성.

### 2.3. 스토리지, 트랜잭션 & 복제 (Storage, Transactions & HA)
- [x] **1-Writer Multi-Reader WAL 복제 & HA 클러스터링 (`pkg/replication`, `cmd/server`)**: Primary-Replica 실시간 WAL 스트리밍, 복제본 읽기 전용 보호(`ErrReadOnlyReplica`), 무중단 승격(Failover Promotion).
- [x] **이중 스토리지 백엔드 (`pkg/storage`)**: 프로덕션용 WiredTiger (Cgo, Lock-Free B+Tree, MVCC) 및 순수 Go In-Memory OCC 엔진 (MessagePack, WAL/AOF 지속성).
- [x] **인메모리 Ephemeral 모드 (`cmd/server`, `pkg/storage/memengine`)**: `--ephemeral` 플래그로 디스크 I/O 없이 순수 RAM에서만 구동하는 초고속 임시 모드.
- [x] **`storage.Value` 정수형 Fast-Path (`pkg/storage/engine.go`)**: `TypeInt`에 대한 고속 비교/일치 분기 및 `strconv.FormatInt` 포맷팅.
- [x] **트랜잭션 세이브포인트 (`pkg/api/savepoint_test.go`, `pkg/storage`)**: T-SQL `SAVE TRANSACTION` / ANSI `SAVEPOINT`, `ROLLBACK TO SAVEPOINT`, `RELEASE SAVEPOINT` 중첩 롤백.
- [x] **인메모리 락프리 동시성 스킵리스트 (`pkg/storage/memengine/skiplist.go`)**: 보조 인덱스를 동시성 SkipList(\(O(\log N)\), 2.8M inserts/sec)로 구현.
- [x] **쿼리 메모리 한도 거버너 (`work_mem`) (`pkg/execution/spill.go`)**: 메모리 상한 초과 시 OOM 대신 임시 파일 External Merge Sort로 자동 전환.
- [x] **무중단 온라인 스냅샷 핫 백업 (`pkg/backup`)**: 쓰기 락 없이 일관된 `.tar.gz` 시점 백업(`POST /api/backup`) 및 복원.
- [x] **외래키 무결성 제약조건 (`pkg/catalog`)**: `ON DELETE/UPDATE CASCADE`, `SET NULL`, `RESTRICT` 참조 무결성 보장.

### 2.4. 와이어 프로토콜 & API (Protocols & API)
- [x] **이중 와이어 프로토콜 리스너 (`pkg/wire`, `pkg/wire/tds`)**: PostgreSQL Wire Protocol v3(`:5433`) 및 Microsoft SQL Server TDS 7.x/8.0(`:1433`) 동시 제공, SSL/TLS 지원.
- [x] **패킷 버퍼 풀링 (`sync.Pool`) (`pkg/wire/protocol.go`, `pkg/wire/tds/token.go`)**: PGWire 4KB 고정 버퍼 풀(`packetBufferPool`) 및 TDS 행 토큰 버퍼 풀(`tdsRowBufferPool`)로 대량 스트리밍 시 힙 할당 제거.
- [x] **SQL 레벨 Prepared Statements (`pkg/api/sqlexec_prepare.go`)**: `PREPARE`, `EXECUTE`, `DEALLOCATE` 구문 지원, `$1, ?` ➔ `@p1` 플레이스홀더 정규화, VDBE PlanCache 연동.
- [x] **PostgreSQL 대량 복사 스트리밍 (`pkg/wire/copy.go`)**: `COPY FROM STDIN` 및 `COPY TO STDOUT` 스트리밍 프로토콜(CSV 및 Text 포맷).
- [x] **임베디드 웹 관리 스튜디오 (`pkg/ui`)**: `:8090/_/` 단일 바이너리 내장 웹 UI, 인터랙티브 쿼리 콘솔, VDBE 비주얼 바이트코드 디스어셈블러(`EXPLAIN BYTECODE`).

---

## 3. Future Technical Milestones (단계별 차기 과제 상세)

### Phase 4: Observability, Parallelism & Dual UDF SPI (v0.12.0 Target)

#### 4.1. 임베디드 웹 스튜디오 HA 복제 대시보드 (`pkg/ui`)
- **현황**: v0.11.0에서 1-Writer Multi-Reader 복제 서버 엔진 및 REST 엔드포인트가 완성되었으나, 브라우저 웹 스튜디오 콘솔에는 아직 복제 전용 모니터링 탭이 없음.
- **과제**:
  - 웹 스튜디오(`:8090/_/`)에 고가용성(HA) 복제 상태 카드 추가 (Primary vs Replica 상태, 복제본 목록, LSN 랙 지연, 스트리밍 상태 실시간 표시).
  - 장애 시 웹 콘솔에서 즉시 1차 노드로 승격할 수 있는 원클릭 `Promote` 버튼 제공.
- **기대 효과**: 운영자가 CLI나 cURL 없이도 브라우저에서 복제 토폴로지를 시각적으로 파악하고 Failover 제어 가능.

#### 4.2. 클라이언트 읽기 분산 로드밸런싱 가이드 & 라우팅 인터페이스 (`pkg/wire`, `pkg/api`)
- **현황**: 복제본 노드들이 읽기 전용으로 대기하고 있으나, 클라이언트 애플리케이션 입장에서 쓰기와 읽기 요청을 어떤 규칙으로 분산할지에 대한 프록시/라우팅 가이드가 필요함.
- **과제**:
  - 쓰기(`Primary: :5433 / :1433`)와 읽기(`Replica들`)를 자동 분기할 수 있는 로드밸런서(HAProxy / NGINX / Envoy) 설정 템플릿 제공.
  - 드라이버 레벨에서 `target_session_attrs=read-write` 및 `read-only`를 분기할 수 있는 헬퍼 인터페이스 가이드 문서화.
- **기대 효과**: 단일 쓰기 노드의 병목 없이 읽기 트래픽을 수평으로 안전하게 확장(Read Scale-Out).

#### 4.3. `ANALYZE TABLE` 멀티코어 병렬 통계 수집 (`pkg/planner`)
- **현황**: 테이블 분석 시 각 컬럼의 Min/Max, NDV, 등빈도 히스토그램을 단일 고루틴으로 순차 계산하여 대형 테이블에서 시간이 소요됨.
- **과제**:
  - 컬럼별 통계 계산이 서로 완전히 독립적인 불변 연산임을 활용하여 `sync.WaitGroup` 멀티코어 고루틴 병렬화.
- **기대 효과**: 핫 패스를 전혀 건드리지 않고, 대규모 테이블 통계 수집 및 CBO 갱신 시간 4~8배 단축.

#### 4.4. `CREATE INDEX` 백그라운드 병렬 정렬 및 벌크 로딩 (`pkg/catalog`, `pkg/storage`)
- **현황**: 대용량 데이터가 적재된 상태에서 보조 인덱스를 추가하면 단일 스레드가 전체 행을 순차 순회하여 B+Tree/SkipList에 단건씩 삽입함.
- **과제**: 데이터를 N개 청크로 분할하여 병렬 정렬(Parallel Sort) 후 인덱스 리프에 K-Way 병합 벌크 삽입.
- **기대 효과**: 프로덕션 DDL 인덱스 생성 시간 3~5배 단축.

#### 4.5. 듀얼 트랙 UDF 확장 엔진 (Go Native SPI & Wasm Sandbox) (`pkg/proc`, `pkg/extension`)
- **현황**: 현재 SQL 스크립트 기반 UDF(`CREATE FUNCTION`)만 지원되며, Go 언어나 외부 코드로 작성된 고성능 UDF 연동은 불가능함.
- **과제**:
  - **Track 1 (Go Native SPI)**: SQLion을 Go 라이브러리로 임베딩하는 개발자를 위해 `proc.RegisterScalarFunction(name, fn)` Go API 제공 (제로 오버헤드, < 5ns 호출).
  - **Track 2 (Wasm Sandbox)**: 독립 서버 환경에서 `CREATE FUNCTION ... AS WASM 'plugins/udf.wasm'` DDL 지원. CGO 없는 순수 Go `wazero` 런타임 탑재, 메모리 샌드박싱(Golden Rule 26)을 통한 크래시/패닉 격리.
- **기대 효과**: 임베디드 Go 개발자와 독립 DB 서버 운영자 모두의 요구를 충족하는 완벽한 UDF 생태계 구축.

---

### Phase 5: Decoupled HTAP & Production Hardening (v1.0.0 LTS Target)

#### 5.1. 공유 메모리(POSIX shm / mmap) 기반 Apache Arrow IPC 스냅샷 (DuckDB Zero-Copy 연동)
- **현황**: SQLion의 모든 테이블은 행 지향(Row-Oriented) 구조로 OLTP 초고속 처리에 최적화되어 있으나, 대용량 통계/집계 시 행 단위 역직렬화 오버헤드가 발생함.
- **과제**:
  - 특정 시점의 테이블 데이터를 Apache Arrow IPC RecordBatch 컬럼 포맷으로 직렬화하여 POSIX 공유 메모리(`/dev/shm` 또는 `mmap` 임시 파일)에 기록 (`SNAPSHOT TABLE <t> TO ARROW_SHM <name>`).
  - 외부 분석 프로세스(DuckDB, Python Polars, Pandas)에 파일 디스크립터(FD) 및 메모리 경로를 전달하여 Zero-Copy 커널 페이지 맵핑 기반 고속 벡터화 분석 실현.
  - 무결함 격리(Zero Blast Radius): 무거운 분석 쿼리가 OOM을 유발해도 OLTP 쓰기 트랜잭션은 100% 무중단 독립 운영.
  - TTL 기반 자동 해제(`unlink` / `munmap`)로 메모리 누수 방지.
- **기대 효과**: 복잡한 컬럼 엔진을 내장하지 않고도, DuckDB 등 최고 수준의 오픈소스 벡터화 분석 생태계를 0ms 지연시간으로 완전 통합 (분리형 HTAP).

#### 5.2. WiredTiger 세션 및 커서 풀링 최적화 (`pkg/storage/wiredtiger`)
- **현황**: WiredTiger는 검증된 최고 수준의 엔터프라이즈 B+Tree MVCC 스토리지이나, 고빈도 단건 CRUD 연산 시 Cgo 커서 오픈/클로즈 오버헤드가 발생함.
- **과제**:
  - 워커 고루틴별 커서 캐싱 및 세션 풀링을 고도화하여 Cgo 브릿지 지연시간을 단축하고 In-Memory 백엔드 수준의 극저지연성 달성.
  - Hazard Pointer 캐시 적중률 극대화.
- **기대 효과**: WiredTiger의 OLTP 처리량 추가 20~30% 견인 및 CPU 사이클 절감.

#### 5.3. Volcano 청크 단위 배치 실행 (Vectorized Chunk Iterator) (`pkg/execution`)
- **현황**: Volcano 반복자는 행 단위(Tuple-at-a-Time) 함수 호출(`Next()`)로 데이터를 스트리밍하므로, 대용량 스캔/필터 시 가상 함수 디스패치 오버헤드가 발생함.
- **과제**: 1,024개 튜플 벡터 단위 슬라이스 배치 스트리밍 도입.
- **기대 효과**: 분석 쿼리 및 대량 필터링 연산 속도 3~5배 가속.

#### 5.4. 백그라운드 MVCC Vacuum 및 Undo Log Version GC (`pkg/storage/memengine`)
- **현황**: `memengine`에서 `DELETE` 발생 시 튜플을 즉시 제거하거나 덮어쓰므로, 장기 실행 트랜잭션의 스냅샷 일관성을 위해 메모리 단편화 관리가 필요함.
- **과제**: 튜플 변경 이력을 체인으로 관리하고 백그라운드 워커가 사용 종료된 구버전 튜플을 주기적으로 압축/정리.
- **기대 효과**: 24/365 장기 가동 환경에서 메모리 누적 방지.

---

## 4. 마일스톤별 핵심 추진 우선순위 매트릭스

| 마일스톤 | 우선순위 | 과제명 | 해당 영역 | 구현 공수 | 기대 효과 |
| :---: | :---: | :--- | :---: | :---: | :--- |
| **v0.12.0** | **P1** | **임베디드 웹 스튜디오 HA 복제 모니터링 카드** | UI & 운영 | 보통 (200~300줄) | 브라우저에서 복제 토폴로지 시각화 및 원클릭 Failover |
| **v0.12.0** | **P1** | **`ANALYZE TABLE` 멀티코어 병렬 통계 수집** | CBO & 플래너 | 낮음 (50~100줄) | 대형 테이블 통계 수집 시간 4~8배 단축 (리스크 0%) |
| **v0.12.0** | **P2** | **클라이언트 읽기 분산 라우팅 인터페이스** | 프로토콜 & API | 낮음 (문서/설정) | 복제본을 활용한 무중단 Read Scale-Out 지원 |
| **v0.12.0** | **P2** | **Go 네이티브 스칼라 함수 등록 SPI (In-Process UDF)** | 실행기 & 확장 | 낮음 (80~120줄) | 임베디드 Go 개발자를 위한 < 5ns 제로 오버헤드 UDF 바인딩 |
| **v0.12.0** | **P3** | **`CREATE INDEX` 백그라운드 병렬 정렬/벌크 로딩** | 스토리지 & DDL | 보통 (200~300줄) | 운영 중 신규 인덱스 빌드 속도 3~5배 단축 |
| **v0.12.0** | **P3** | **Wasm 런타임 샌드박스 UDF (`wazero`)** | 플러그인 & 보안 | 보통 (300~400줄) | 런타임 동적 플러그인 적재 및 완전한 크래시 격리 |
| **v1.0.0** | **P1** | **공유메모리 Apache Arrow IPC 스냅샷 (DuckDB)** | 분석 & HTAP | 보통 (300~400줄) | Zero-Copy, Zero-I/O 분리형 고성능 OLAP 결합 |
| **v1.0.0** | **P2** | **WiredTiger 세션 및 커서 풀링 최적화** | 스토리지 & Cgo | 보통 (200~300줄) | WiredTiger 단건 연산 Cgo 오버헤드 절감 및 TPS 견인 |
| **v1.0.0** | **P2** | **Volcano 청크 단위 배치 실행 (Vectorized Chunk)** | 실행기 & SIMD | 높음 (600~800줄) | 대용량 관계형 스캔 및 집계 3~5배 가속 |
| **v1.0.0** | **P3** | **백그라운드 MVCC Vacuum / Version GC** | 트랜잭션 & 메모리 | 높음 (400~500줄) | 장기 가동 인메모리 테이블 단편화 방지 |
