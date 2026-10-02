# SQLion Engineering Roadmap & Core Technical Milestones

This document charts the development milestones, current implementation status, and remaining technical challenges for the SQLion high-concurrency database engine.

---

## 1. Milestones Overview

```
[ Phase 1: Core Engine Foundation (v0.5.0) ] ───> [ Phase 2: Storage, Replication & HA (v0.11.0) ] ─> [ Phase 3: Analytics & Horizons (v1.0+) ]
- Dual Wire Protocols (PGWire + TDS)          - 1-Writer Multi-Reader WAL Replication (HA)          - Shared Memory Arrow IPC Snapshots (DuckDB)
- Dual Storage (WiredTiger + In-Memory OCC)    - Transaction Savepoints (SAVEPOINT / ROLLBACK)       - AI Vector Embeddings (HNSW & IVF-Flat)
- Dual Parser (Native Pratt + ANTLR4 Fallback) - VDBE PlanCache & OpRelQuery Physical Caching        - 3-Node Distributed Raft Consensus
- VDBE Virtual Machine & PlanCache Sharing     - Zero-Copy Streaming Cursor (memengine)             - Real-Time CDC Streaming (Kafka / NATS)
- Volcano Relational Engine & Window Functions - WiredTiger Cursor Pooling & Hazard Cache           - Full-Text Search Engine (BM25)
- CBO Histograms, Pushdown, Constant Folding   - Background MVCC Vacuum / Version GC
- Online Hot Backup & COPY Streaming           - Multi-Join DP / Genetic Query Optimizer
(COMPLETED - 100%)                             (COMPLETED & ACTIVE)                                 (PLANNED)
```

---

## 2. Current Implementation Status (구현 완료 내역)

### 2.1. 코어 실행 엔진 (Core Execution Engine)
- [x] **VDBE 바이트코드 가상 머신 (`pkg/proc/runtime`)**: SQLite 스타일 3-어드레스 레지스터 기반 가상 머신, `sync.Pool` 레지스터 재사용, 프로시저/함수/트리거/커서 실행.
- [x] **Volcano Iterator 관계형 실행 파이프라인 (`pkg/execution`)**: 풀 스트리밍 반복자 모델 (`SeqScan`, `IndexScan`, `IndexOnlyScan`, `FilterIterator`, `ProjectIterator`, `SortIterator`, `LimitIterator`, `HashJoinIterator`, `IndexNestedLoopJoinIterator`, `GroupByIterator`, `CTEIterator`).
- [x] **윈도우 함수 엔진 (`pkg/execution/window.go`)**: `OVER (PARTITION BY ... ORDER BY ...)` 구문 지원, `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `LEAD()`, `LAG()`, 윈도우 집계(`SUM`, `AVG`, `COUNT`, `MIN`, `MAX`) 완벽 구현.
- [x] **협력적 쿼리 취소 (`pkg/proc/runtime`, `pkg/api`, `pkg/wire`)**: `context.Context` 기반 무손상 실행 중단, T-SQL `KILL <spid>`, PostgreSQL `pg_cancel_backend()`, `pg_terminate_backend()`, PGWire `CancelRequest` 프로토콜 지원.

### 2.2. 쿼리 옵티마이저 (Query Optimizer & CBO)
- [x] **비용 기반 옵티마이저 (CBO) & 히스토그램 통계 (`pkg/planner/stats.go`)**: `ANALYZE TABLE <name>` 명령으로 64-버킷 등빈도 히스토그램, 카디널리티, NDV(고유값 수), NULL 비율 수집, Hash Join vs Index Nested Loop Join 자동 비용 산출.
- [x] **조건절 푸시다운 (Predicate Pushdown) (`pkg/planner/pushdown.go`)**: 복합 `WHERE` 조건절을 테이블 단위로 분할 분석하여 조인 전 하위 스캔 반복자로 조건 조기 전달, 인덱스 매칭 연계.
- [x] **상수 폴딩 & 불리언 단순화 (Constant Folding) (`pkg/proc/fold.go`)**: 컴파일 타임 리터럴 연산(`3000 * 12` -> `36000`), 내장 함수 선계산, 항진/모순 조건(`1=1`, `1=0`) \(O(1)\) 단축 평가.
- [x] **상관 서브쿼리 비상관화 (Decorrelation & Semi/Anti-Join) (`pkg/planner/decorrelate.go`)**: `EXISTS`, `NOT EXISTS`, `IN`, `NOT IN` 상관 서브쿼리를 관계형 `SEMI` / `ANTI` 조인으로 자동 재작성.

### 2.3. 스토리지 및 트랜잭션 (Storage & Transactions)
- [x] **이중 스토리지 백엔드 (`pkg/storage`)**: 프로덕션용 WiredTiger (Cgo, Lock-Free B+Tree, MVCC) 및 순수 Go In-Memory OCC 엔진 (MessagePack 직렬화, WAL/AOF 지속성).
- [x] **인메모리 락프리 동시성 스킵리스트 (`pkg/storage/memengine/skiplist.go`)**: `memengine` 보조 인덱스를 단순 슬라이스 재할당(\(O(N)\))에서 동시성 SkipList(\(O(\log N)\), 2.8M inserts/sec)로 전환 완료.
- [x] **쿼리 메모리 한도 거버너 (`work_mem`) (`pkg/execution/spill.go`)**: 쿼리별 메모리 상한 초과 시 OOM 대신 임시 파일 기반 External Merge Sort로 자동 전환.
- [x] **무중단 온라인 스냅샷 핫 백업 (`pkg/backup`, `pkg/api/backup.go`)**: 쓰기 락 없이 일관된 `.tar.gz` 시점 스냅샷 백업(`POST /api/backup`) 및 복원(`POST /api/restore`).
- [x] **외래키 무결성 제약조건 (`pkg/catalog`, `pkg/api/sqlexec.go`)**: `ON DELETE/UPDATE CASCADE`, `SET NULL`, `RESTRICT` 외래키 참조 무결성 보장.

### 2.4. 와이어 프로토콜 및 인터페이스 (Protocols & API)
- [x] **이중 와이어 프로토콜 리스너 (`pkg/wire`, `pkg/wire/tds`)**: PostgreSQL Wire Protocol v3(포트 `5433`) 및 Microsoft SQL Server TDS 7.x/8.0(포트 `1433`) 동시 제공, SSL/TLS 암호화 지원.
- [x] **전역 PlanCache 연동 & 바이트코드 재사용 (`pkg/wire`, `pkg/wire/tds`, `cmd/server`)**: PGWire Extended Query (`Parse`, `Bind`, `Execute`) 및 TDS RPC Prepare (`sp_prepare`, `sp_prepexec`)에서 컴파일된 VDBE 바이트코드 *proc.Program을 전역 캐시(`*runtime.PlanCache`)로 공유 및 \(O(1)\) 재사용.
- [x] **PostgreSQL 대량 복사 스트리밍 (`pkg/wire/copy.go`)**: 고성능 `COPY FROM STDIN` 및 `COPY TO STDOUT` 스트리밍 프로토콜(CSV 및 Text 포맷) 완벽 지원.
- [x] **테이블 기반 RESTful CRUD API 자동 생성 (`/api/records/:table`)**: zero-code CRUD, 비교/논리 필터 파서, 다중 컬럼 정렬, 페이지네이션, 실시간 SSE 스트림.

---

## 3. Remaining Technical Challenges (남은 핵심 개선 과제)

향후 SQLion의 엔터프라이즈 완성도를 높이기 위해 영역별로 고도화할 수 있는 핵심 내부 시스템 개선 과제 목록입니다.

```
┌────────────────────────────────────────────────────────────────────────┐
│                      SQLion 향후 핵심 기술 과제 맵                       │
├────────────────────┬────────────────────┬──────────────────────────────┤
│ 1. 코어 실행 엔진   │ 2. 스토리지 & 메모리 │ 3. 트랜잭션 & 가용성          │
│ - VDBE 완전 단일화  │ - Zero-Copy 커서   │ - MVCC Vacuum / Version GC   │
│ - 벡터화 배치 실행  │ - Pure-Go 영속 엔진 │ - Raft 3노드 분산 합의 복제   │
│                    │ - Columnar 포맷    │ - 트랜잭션 세이브포인트       │
├────────────────────┴────────────────────┴──────────────────────────────┤
│ 4. 쿼리 옵티마이저 & CBO               │ 5. 프로토콜, 스트리밍 & AI      │
│ - 다중 조인 DP 동적 프로그래밍 순서 탐색 │ - 실시간 CDC 스트리밍 (Kafka) │
│ - 다중 컬럼 상관관계 통계 수집        │ - AI 벡터 임베딩 (HNSW)       │
│                                        │ - BM25 내장 전문 검색 엔진   │
└────────────────────────────────────────┴──────────────────────────────┘
```

---

### 영역 1: 코어 실행 엔진 (Core Execution Engine)

#### 1.1. 대화형 쿼리 실행기의 VDBE 단일화 (Interactive Query → VM Bytecode)
- **현황**: 프로시저는 VDBE 바이트코드 VM으로 실행되며 단순 쿼리(`OpRelQuery`)는 연계되어 있으나, 복잡한 대화형 다중 조인/집계 쿼리는 여전히 `AST ➔ Planner ➔ Volcano Iterator Physical Pipeline`으로 분기되어 실행됨 (~65% 하이브리드 상태).
- **과제**:
  - `planner.PlanNode` 트리 전체를 일대일 대응되는 VDBE 바이트코드 명령어(`OpOpenCursor`, `OpNextRow`, `OpHashJoinProbe`, `OpFilterRow`, `OpSortRows`)로 컴파일하는 물리 코드 생성기(Code Generator) 완성.
  - 대화형 SQL과 저장 프로시저가 동일한 단일 가상 머신 위에서 일관되게 실행되도록 엔진 계층 완전 통합.
- **기대 효과**:
  - 실행기 코드베이스 단일화로 유지보수 복잡도 대폭 감소.
  - 대화형 쿼리 역시 바이트코드 레벨의 프로파일링, 인터럽트, 디버깅, JIT 최적화 혜택을 100% 동일하게 향유.

#### 1.2. 벡터화 쿼리 실행 엔진 (Vectorized / SIMD Batch-Oriented Execution)
- **현황**: 현재 Volcano 반복자는 행 단위(Tuple-at-a-Time) 함수 호출(`Next()`)로 데이터를 스트리밍하므로, 대용량 스캔/필터 시 가상 함수 디스패치 및 인터페이스 오버헤드가 발생함.
- **과제**:
  - 행 단위가 아닌 컬럼 블록 단위(예: 1,024개 튜플 벡터 배치)로 데이터를 처리하는 벡터화 반복자(Vectorized Iterator) 도입.
  - 수치 필터링 및 산술 연산 루프에 Go 어셈블리 또는 SIMD 친화적 슬라이스 배치 연산 적용.
- **기대 효과**: 분석 쿼리 및 대량 필터링 연산 속도 3~5배 이상 가속.

---

### 영역 2: 스토리지 엔진 및 메모리 관리 (Storage Engine & Memory)

#### 2.1. `memengine` Zero-Copy Streaming Cursor (Scan 시 슬라이스 전체 복사 제거)
- **현황**: Volcano Iterator 파이프라인은 스트리밍 구조이나, 최하단 `memengine.Scan()`이 여전히 테이블 전체 맵을 순회하며 `merged` 맵을 만들고, 전체 키를 정렬한 후 슬라이스를 일괄 할당하여 반환함.
- **과제**:
  - 테이블의 내부 행 데이터를 별도 슬라이스로 전체 복사하지 않고, `RLock` 보호 또는 버전 스냅샷 하에서 행 포인터를 순차적으로 1개씩 꺼내주는 진정한 제로 카피 스트리밍 커서 구현.
  - SkipList/B+Tree 인덱스 스캔과 동일한 지연 평가(Lazy Evaluation) 패턴 일원화.
- **기대 효과**: 수십만~수백만 행 스캔 시 메모리 할당 90% 이상 절감 및 Go 런타임 GC 스파이크 방지.

#### 2.2. WiredTiger 세션 및 커서 풀링 최적화 (Cgo 오버헤드 최소화)
- **현황**: WiredTiger는 검증된 최고 수준의 엔터프라이즈 B+Tree MVCC 스토리지이나, 고빈도 단건 CRUD 연산 시 Cgo 커서 오픈/클로즈 오버헤드가 발생함.
- **과제**:
  - 워커 고루틴별 커서 캐싱 및 세션 풀링을 고도화하여 Cgo 브릿지 지연시간을 단축하고 In-Memory 백엔드 수준의 극저지연성 달성.
  - 레코드 직렬화 경로의 Zero-Allocation 바이너리 코덱 유지보수 및 Hazard Pointer 캐시 적중률 극대화.
- **기대 효과**: WiredTiger의 OLTP 처리량 추가 20~30% 견인 및 CPU 사이클 절감.

#### 2.3. 공유 메모리(Shared Memory / mmap) 기반 Apache Arrow 컬럼 스냅샷 및 Zero-Copy OLAP 연동
- **현황**: SQLion의 모든 테이블은 행 지향(Row-Oriented) 구조로 OLTP 초고속 처리에 최적화되어 있으나, 대용량 집계(`COUNT/SUM/AVG/GROUP BY`) 및 분석 쿼리 시 행 단위 역직렬화 오버헤드가 발생함.
- **과제**:
  - 특정 시점의 테이블 데이터를 Apache Arrow IPC RecordBatch 컬럼 포맷으로 직렬화하여 POSIX 공유 메모리(`/dev/shm` 또는 `mmap` 임시 파일)에 기록하는 스냅샷 파이프라인 구축 (`SNAPSHOT TABLE <t> TO ARROW_SHM <name>`).
  - 외부 분석 프로세스(DuckDB, Python Polars, Pandas, DataFusion)가 네트워크 복사나 디스크 I/O 없이 Zero-Copy 커널 페이지 맵핑으로 즉시 고속 벡터화 분석을 수행할 수 있도록 파일 디스크립터(FD) 및 메모리 경로 전달 인터페이스 제공.
  - 무결함 격리(Zero Blast Radius): 무거운 분석 쿼리가 메모리를 소진하거나 비정상 종료되어도 SQLion의 OLTP 핵심 엔진(쓰기 트랜잭션)은 100% 무중단 독립 운영.
  - 스냅샷 수명 주기 관리(TTL 및 자동 `unlink`/`munmap`)를 통한 공유 메모리 누수 방지.
- **기대 효과**:
  - SQLion 엔진 내부를 복잡한 컬럼 스토어로 개조하지 않고도(KISS 원칙 준수), DuckDB 등 현존 최강의 오픈소스 벡터화 분석 생태계를 0ms 지연시간으로 완전 통합.
  - 네트워크 대역폭 및 디스크 I/O 비용 0(Zero)의 분리형 HTAP(Decoupled Hybrid Transactional/Analytical Processing) 실현.

---

### 영역 3: 트랜잭션, 동시성 및 가용성 (Transactions & High Availability)

#### 3.1. 백그라운드 MVCC Vacuum 및 Undo Log Version GC
- **현황**: `memengine`에서 `DELETE` 발생 시 튜플을 즉시 제거하거나 덮어쓰므로, 장기 실행 트랜잭션의 스냅샷 일관성이 위협받거나 메모리가 단편화될 수 있음.
- **과제**:
  - 튜플 변경 이력을 체인(Undo Log/Tombstone)으로 관리.
  - 활성 트랜잭션이 더 이상 참조하지 않는 이전 버전을 백그라운드 워커 고루틴이 주기적으로 정리(Vacuuming) 및 메모리 압축.
- **기대 효과**: 24/365 장기 가동 환경에서 잦은 갱신/삭제로 인한 메모리 누적 및 동시성 충돌 방지.

#### 3.2. 분산 3노드 Raft 합의 복제 (Distributed HA Clustering)
- **현황**: 단일 노드 스탠드얼론 실행 구조로, 노드 장애 시 고가용성(HA) 페일오버를 외부 인프라에 의존해야 함.
- **과제**:
  - HashiCorp Serf(멤버십) 및 Raft(합의 엔진)를 탑재하여 3노드 액티브-스탠바이 클러스터링 지원.
  - WAL 로그의 동기식 복제, 리더 선출, 자동 페일오버 및 팔로워 노드를 통한 읽기 확장(Read-Scaling) 구현.
- **기대 효과**: 엔터프라이즈 무중단 데이터베이스 운영 및 장애 복구 자동화.

#### 3.3. 트랜잭션 세이브포인트 (`SAVEPOINT`, `ROLLBACK TO SAVEPOINT`)
- **현황**: 트랜잭션 롤백 시 전체 트랜잭션 단위 취소만 지원됨.
- **과제**: T-SQL `SAVE TRANSACTION <name>` 및 ANSI `SAVEPOINT <name>`, `ROLLBACK TO <name>` 중첩 트랜잭션 지점 관리 구현.
- **기대 효과**: 대규모 비즈니스 로직 및 중첩 프로시저 실행 시 부분 롤백 복원력 제공.

---

### 영역 4: 쿼리 옵티마이저 고도화 (Query Optimizer & CBO)

#### 4.1. 다중 조인 동적 프로그래밍 순서 탐색 (Dynamic Programming / Genetic Join Order Optimizer)
- **현황**: 3개 이상의 다중 테이블 조인 시 탐욕적(Greedy) 휴리스틱 순서로 결합 경로를 평가함.
- **과제**:
  - 5~10개 이상의 다중 테이블 조인에 대해 System R 스타일의 상향식 동적 프로그래밍(DP) 또는 유전 알고리즘(Genetic Optimizer)을 적용하여 전역 최적 조인 트리(Left-Deep vs Bushy Tree) 탐색.
- **기대 효과**: 대규모 스타 스키마 및 복합 다중 조인 쿼리의 기하급수적 성능 개선.

#### 4.2. 다중 컬럼 상관관계 통계 및 확장 통계 (Multivariate Statistics & Correlated NDV)
- **현황**: 히스토그램이 단일 컬럼 독립 분포를 가정하여, 두 컬럼 간 강한 상관관계(예: `도시 = '서울' AND 구 = '강남구'`)가 있을 때 카디널리티를 과소평가함.
- **과제**: 다중 컬럼 NDV 및 상관 빈도 튜플 통계 수집 지원 (`CREATE STATISTICS`).
- **기대 효과**: 상관 조건절이 포함된 쿼리에서 인덱스 선택 및 조인 크기 예측 정확도 극대화.

---

### 영역 5: 프로토콜, 스트리밍 및 AI 워크로드 (Protocols, CDC & AI)

#### 5.1. 실시간 변경 데이터 캡처 (CDC: Change Data Capture via WAL/AOF Streaming)
- **현황**: 현재 데이터 변경 스트림은 `/api/realtime/stream` (SSE)을 통한 단기 브로드캐스트만 지원됨.
- **과제**:
  - 커밋된 트랜잭션의 커밋 LSN과 행 변경 델타를 지속적으로 캡처하여 Apache Kafka, RabbitMQ, NATS 등 외부 이벤트 버스로 스트리밍하는 Debezium 호환 CDC 엔진 탑재.
- **기대 효과**: 마이크로서비스 이벤트 소싱, 데이터 웨어하우스(ELT) 실시간 연동.

#### 5.2. AI 벡터 임베딩 및 유사도 검색 (`VECTOR(dim)`, HNSW Index)
- **현황**: 텍스트, 숫자, 날짜, JSON 등의 전통적 OLTP 데이터 타입만 지원됨.
- **과제**:
  - `VECTOR(1536)` 고정 차원 부동소수점 임베딩 데이터 타입 내장.
  - HNSW (Hierarchical Navigable Small World) 및 IVF-Flat 벡터 인덱스 구현.
  - 거리 연산자 지원: 코사인 거리 (`<=>`), L2 유클리드 거리 (`<->`), 내적 (`<#>`).
  - 관계형 필터와 결합된 하이브리드 검색 지원:
    ```sql
    SELECT id, title, text
    FROM knowledge_base
    WHERE category = 'finance'
    ORDER BY embedding <=> '[0.012, -0.045, ...]'
    LIMIT 5;
    ```
- **기대 효과**: 별도의 pgvector / Pinecone 없이 SQLion 단일 엔진으로 RAG 및 AI 검색 파이프라인 완결.

#### 5.3. 내장 전문 검색 엔진 (Full-Text Search with BM25 Ranking)
- **현황**: `LIKE '%keyword%'` 또는 정규식 매칭만 지원되어 긴 텍스트 문서 검색 시 전체 스캔이 불가피함.
- **과제**: 형태소 분해(Tokenization), 스테밍, 불용어 처리 기반 역색인(Inverted Index) 및 BM25 랭킹 점수화 엔진 구현.

#### 5.4. 자동 TLS/ACME (Let's Encrypt) 내장 프로비저닝
- **현황**: 수동 인증서 파일 로드(`-tls-cert`, `-tls-key`) 및 자체 서명 인증서 자동 생성 지원.
- **과제**: 도메인 지정 시 Let's Encrypt ACME 프로토콜을 통해 포트 80/443에서 공식 TLS 인증서를 자동 발급/갱신하는 기능 내장.

---

## 4. 추천 다음 마일스톤 로드맵

| 순위 | 과제명 | 해당 영역 | 난이도 / 분량 | 사용자 및 시스템 체감 효과 |
| :---: | :--- | :---: | :---: | :--- |
| **1위** | **`memengine` Zero-Copy Streaming Cursor** | 스토리지 & 메모리 | 보통 (300~400줄) | 대용량 스캔 메모리 할당 90% 절감, GC 정지 방지 |
| **2위** | **백그라운드 MVCC Vacuum / Version GC** | 트랜잭션 & 안정성 | 보통 (400~500줄) | 24/365 장기 가동 시 메모리 누적 및 단편화 방지 |
| **3위** | **Pure-Go 영속 스토리지 백엔드 (Pebble 탑재)** | 배포 & 이식성 | 보통 (500~600줄) | `CGO_ENABLED=0` 크로스 컴파일 & 완전한 단일 바이너리 배포 |
| **4위** | **AI 벡터 임베딩 & 유사도 검색 (`VECTOR(dim)`)** | AI & 확장 | 다소 높음 (800~1,000줄) | RAG 및 시맨틱 검색 단일 DB 완결 |
| **5위** | **대화형 쿼리 실행기의 VDBE 완전 단일화** | 코어 실행 엔진 | 높음 (1,000줄 이상) | VM 아키텍처 일원화 및 단일 엔진 완성 |
| **6위** | **분산 3노드 Raft 합의 복제** | 고가용성 (HA) | 높음 (1,500줄 이상) | 무중단 페일오버 및 자동 리더 선출 클러스터링 |
