# LionSQL 고성능 OLAP 및 대용량 분석 쿼리 엔진 아키텍처 플랜 (`notes/plans/OLAP_ENGINE_ENHANCEMENT_PLAN.ko.md`)

- **작성일자**: 2026-11-19
- **적용 마일스톤**: `Post-1.0` (v1.10.0 ~ v1.99.0)
- **배경 문서**: `/tmp/OLAP.txt`
- **목적**: LionSQL 1.0의 고동시성 OLTP 엔진 안정화 이후, 상용 엔터프라이즈 DBMS(PostgreSQL, SQL Server, Snowflake) 수준의 복합 분석 쿼리(OLAP) 처리 능력과 36코어+ 대규모 하드웨어 자원 포화 능력을 달성하기 위한 차세대 실행 엔진 아키텍처 청사진.

---

## 1. 현행 아키텍처 진단 및 OLAP 병목 분석

| 영역 | 현행 LionSQL (v1.0-GA 수준) | 엔터프라이즈 상용 DBMS (PostgreSQL / SQL Server) | 차세대 목표 (Post-1.0) |
| :--- | :--- | :--- | :--- |
| **통계 및 카디널리티 추정** | 기초 단일 컬럼 통계 (Min, Max, DistinctCount, NullCount) | 등고선 히스토그램, 최빈값(MCV), 다중 컬럼 상관관계 통계 | 다중 컬럼 확장 통계(`CREATE STATISTICS`), Equi-depth 히스토그램, MCV 리스트 |
| **대용량 조인/집계 스필** | 인메모리 HashJoin/HashAgg (work_mem 초과 시 OOM 위험 또는 에러) | 2-Pass Hybrid Grace Hash Join, 디스크 파티셔닝 스필 | 2-Pass Hybrid Grace Hash Join & HashAgg 구현 (SortIterator 스필 패턴 확장) |
| **조인 순서 탐색 (Join Ordering)** | 단순 휴리스틱 및 제한적 팩토리얼 순회 | Dynamic Programming (DPsize), 유전 알고리즘 (GEQO) | DP 기반 8~12 테이블 Bushy Tree 탐색기 및 GEQO 유전 알고리즘 |
| **실행기 병렬성 (Parallelism)** | **Inter-Query Concurrency만 지원** (단일 쿼리는 1개 CPU 코어 순차 처리) | **Intra-Query Parallelism** (단일 대용량 쿼리를 N개 코어가 분할 병렬 처리) | Volcano Exchange/Gather 노드 기반 병렬 스캔 및 병렬 해시 조인 |
| **파티션 프루닝** | 정적 상수 프루닝 (WHERE partition_key = '2026-01') | 런타임 동적 파티션 프루닝 (DPP via Bloom Filter) | 조인 결과 기반 런타임 Dynamic Partition Pruning (DPP) |

---

## 2. 3대 핵심 아키텍처 축 (3 Core Pillars)

```
+-----------------------------------------------------------------------------------+
|                        LionSQL Post-1.0 OLAP Core Pillars                         |
+-----------------------------------------------------------------------------------+
|  [Pillar 1: 정밀 CBO 통계 엔진]                                                    |
|  - Equi-depth Histogram (100~300 버킷)                                            |
|  - Most Common Values (MCV) 상위 50 빈도 리스트                                     |
|  - Extended Multi-Column Statistics (의존도 계수 d 및 N-Distinct 곱셈 보정)          |
|  - Bloom Filter 기반 Dynamic Partition Pruning (DPP)                              |
+-----------------------------------------------------------------------------------+
|  [Pillar 2: Out-of-Core 스필 조인 & 집계]                                         |
|  - 2-Pass Hybrid Grace Hash Join (256개 해시 버킷 디스크 스필 & 재귀 파티셔닝)         |
|  - Disk-Spilled Hybrid Hash Aggregation (그룹 버킷 디스크 스필)                    |
|  - DP (Dynamic Programming) 기반 Bushy / Left-Deep 조인 순서 열거기                 |
+-----------------------------------------------------------------------------------+
|  [Pillar 3: Intra-Query 쿼리 내 코어 병렬화]                                       |
|  - Parallel Sequential Scan (스토리지 페이지/LSN 청크 단위 워커 분할)               |
|  - Exchange / Gather / Repartition 화산 모델 연산자                               |
|  - Parallel Hash Join (워커 스레드 Shared Hash Table 동시 빌드 & 프로브)           |
+-----------------------------------------------------------------------------------+
```

---

## 3. 필러별 상세 기술 설계 및 구현 사양

### Pillar 1: 카디널리티 추정 고도화 (Advanced CBO & Statistics)

#### 1.1 등고선 히스토그램 (Equi-depth Histogram) & MCV 리스트
- **배경**: 현재의 Min/Max 선형 보간은 데이터 왜곡(Skew) 환경에서 카디널리티 오차가 100배 이상 발생하여 부적절한 Nested Loop 선택 유발.
- **설계 사양**:
  - `ColumnStats`에 `MCVValues []storage.Value`, `MCVFreqs []float64` (상위 50개 최빈값) 추가.
  - 나머지 데이터에 대해 균등한 행 수를 담는 100~300개 버킷의 `EquiDepthBounds []storage.Value` 유지.
  - 범위 쿼리(`col BETWEEN A AND B`, `col > C`) 시 MCV 빈도 합산 + 히스토그램 버킷 비율 계산을 결합하여 오차율을 5% 이내로 제어.

#### 1.2 다중 컬럼 상관관계 통계 (Extended Statistics)
- **문법 지원**:
  ```sql
  CREATE STATISTICS stat_emp_city_country ON (city, country) FROM employees;
  ```
- **수학적 모델**:
  - 함수 종속성 계수 $f \in [0.0, 1.0]$: $f=1.0$이면 두 컬럼이 완전 종속, $f=0.0$이면 독립.
  - 조건 결합 선택도 계산:
    $$P(A \land B) = f \cdot \min(P(A), P(B)) + (1 - f) \cdot (P(A) \times P(B))$$
  - `WHERE city = 'Seoul' AND country = 'Korea'` 시 극단적 과소평가(0.001%)를 원천 방지하고 실제 카디널리티(15%)를 정확히 산출.

#### 1.3 런타임 동적 파티션 프루닝 (Dynamic Partition Pruning, DPP)
- **아키텍처 경계 명확화 (연산자 레벨 vs 스토리지 파티션 레벨)**:
  - *연산자 레벨 블룸 필터(`PERF-RUNTIME-BLOOM-FILTER-1`, 완료 `1e9ec853`)*: `join.go` 내에서 프로브 튜플의 해시 버킷 맵 조회를 O(1) 생략하는 개별 행(Row) 단위 필터링.
  - *본 과제(스토리지 파티션 DPP)*: 대규모 Fact 테이블이 일자/월별 물리 B-Tree 파티션 파일(`table_part_*.wt`)로 쪼개져 있을 때, 조인 차원 테이블의 키로 블룸 필터를 생성하여 **수십 GB에 달하는 무관한 파티션 파일 자체를 열지 않고 스킵**하는 스토리지 I/O 차단 기술.
- **원리**: 대형 팩트 테이블과 소형 디멘션 테이블 조인 시, 디멘션 필터 결과 키들을 인메모리 **Bloom Filter**로 빌드하여 팩트 테이블 스토리지 스캔 단계에 주입.
- **동작**: 팩트 테이블의 파티션 메타데이터와 블룸 필터를 대조하여 80% 이상의 파티션 B-Tree 파일을 물리적으로 열지도 않고 스킵 (`[RULE:LatencyBudget]`).

---

### Pillar 2: Out-of-Core 조인 및 집계 엔진 (Spill-to-Disk Hash Operators)

#### 2.1 2-Pass Hybrid Grace Hash Join
- **배경**: 현행 HashJoin은 조인 빌드 테이블 전체를 메모리 해시맵(`map[string][]Tuple`)에 유지하여 대용량 테이블 조인 시 OOM 발생 위험.
- **알고리즘 사양**:
  1. **Phase 1 (파티셔닝)**: 빌드 및 프로브 튜플을 해시 함수 $h_1(K) \pmod N$으로 $N$개(예: 64~256개) 파티션으로 분할.
     - 가용 `work_mem` 내에 들어가는 파티션 0은 메모리에 상주 유지.
     - 나머지 파티션 $1 \sim N-1$은 임시 디스크 파일(`tmp_join_part_*.bin`)로 순차 스트리밍 기록 (`[RULE:HandleHygiene]`).
  2. **Phase 2 (조인)**:
     - 파티션 0을 즉시 프로브.
     - 이후 각 디스크 파티션 쌍 $(B_i, P_i)$을 순차적으로 메모리에 로드하여 보조 해시 $h_2(K)$로 조인 수행.
     - 단일 파티션이 여전히 `work_mem`을 초과하면 재귀적 Grace Split 수행 (최대 재귀 깊이 4단계 한정).

#### 2.2 2-Pass Hybrid Hash Aggregation
- **알고리즘 사양**:
  - 그룹화 키 수가 수백만 개 이상으로 `work_mem`을 초과할 때, 해시 파티셔닝을 통해 디스크 청크 파일들로 분할 스필 후 파티션 단위 순차 집계.

#### 2.3 DP 기반 조인 순서 탐색기 (DPsize / DPsb)
- **사양**:
  - 8개 이하 테이블: Dynamic Programming 기반 전역 최적 Bushy Tree 탐색.
  - 9~15개 테이블: DPsb (Dynamic Programming with Subsets) 휴리스틱 가지치기.
  - 16개 이상 테이블: GEQO (Genetic Query Optimization) 유전 알고리즘 모드로 자동 전환하여 플래너 컴파일 시간 16ms 이내 유지 (`[RULE:LatencyBudget]`).

---

### Pillar 3: 쿼리 내 코어 병렬화 실행기 (Intra-Query Parallelism)

#### 3.1 Exchange / Gather / Repartition 화산 모델 노드
- **GatherIterator**:
  - $K$개의 백그라운드 워커 고루틴이 각자의 하위 파티션 파이프라인을 실행하고 결과를 채널 버퍼로 전송.
  - Gather 노드는 메인 스레드에서 결과를 순차 또는 비순서 수집 (`[RULE:NonBlockingSink]`).
- **생명주기 안전성 (`[RULE:ZeroOrphan]`)**:
  - 모든 워커 고루틴은 쿼리 컨텍스트 `ctx`에 테더링되며 `sync.WaitGroup`으로 동기화 종료.
  - 쿼리 에러나 `statement_timeout` 발생 시 즉시 `cancel()` 브로드캐스트로 워커 정리.

#### 3.2 Parallel Sequential Scan (병렬 테이블 스캔)
- **WiredTiger 스토리지 분할**:
  - 단일 테이블의 B-Tree 리프 페이지 또는 Primary Key 범위를 $M$개 청크로 등분.
  - 각 워커는 원자적 카운터(`atomic.AddInt64(&chunkIdx, 1)`)를 통해 청크를 동적으로 가로채어 스캔 (동적 워크 스틸링).
- **In-Memory OCC 스토리지 분할**:
  - FlatRecordLayout 메모리 슬라이스를 `[startRow, endRow]` 인덱스 슬라이스로 나누어 Zero-Copy 병렬 스캔.

#### 3.3 Parallel Shared-Hash Join (병렬 해시 조인)
- **구조**:
  - 빌드 단계: $K$개 워커가 빌드 테이블을 병렬 스캔하고, 원자적 락-프리 슬롯 또는 샤디드 해시 버킷에 튜플 동시 적재.
  - 프로브 단계: $K$개 워커가 프로브 테이블 청크를 나누어 읽으며 동일한 읽기 전용 공유 해시 테이블을 동시 프로브.
  - **기대 성능**: 36코어 환경에서 단일 복합 집계/조인 쿼리 실행 속도 최대 20~28배 가속.

---

## 4. 단계별 구현 로드맵 (Post-1.0 Phased Roadmap)

```
[Phase 1: 통계 고도화 (v1.10.0)]
  - Equi-depth Histogram (100버킷) 및 MCV 최빈값 수집기 구현
  - CREATE STATISTICS 다중 컬럼 상관계수 산출 및 CBO 결합 선택도 주입
  - CBO 카디널리티 예측 오차율 벤치마크 (TPC-DS 99쿼리 기준)

[Phase 2: Out-of-Core 스필 조인 (v1.20.0)]
  - 2-Pass Hybrid Grace Hash Join 연산자 구현
  - 2-Pass Hybrid Hash Aggregation 구현
  - work_mem = 4MB 환경에서 10GB TPC-H Q1/Q3/Q10 OOM 방지 및 스필 검증

[Phase 3: DP 조인 플래너 & DPP (v1.30.0)]
  - DPsize 기반 Bushy Tree 조인 순서 최적화 탐색기
  - Bloom Filter 기반 Dynamic Partition Pruning (DPP)

[Phase 4: Intra-Query 코어 병렬화 (v1.40.0)]
  - Gather / Exchange 연산자 및 워커 라이프사이클 프레임워크 ([RULE:ZeroOrphan])
  - Parallel SeqScan (WiredTiger 청크 분할 및 MemEngine 슬라이스 분할)
  - Parallel Shared-Hash Join & Parallel Aggregator 구현
  - 36코어 머신 TPC-H 100GB 스케일아웃 가속 검증 (>= 15배 속도 향상)
```

---

## 5. 아키텍처 규칙 및 불변성 보장

1. **`[RULE:ZeroOrphan]` 및 고루틴 수명 통제**: 병렬 쿼리 워커는 어떤 상황에서도 고아 고루틴(Orphan Goroutine)으로 남아서는 안 되며, 쿼리 완료, 취소, 타임아웃 시 100% `sync.WaitGroup` 동기화 종료를 보장해야 한다.
2. **`[RULE:HandleHygiene]` 디스크 스필 안전성**: Grace Hash Join 및 HashAgg의 임시 디스크 파일은 세션 고유 디렉토리에 생성되고, 쿼리 종료(`Close()`) 즉시 원자적으로 삭제되어 디스크 누수를 원천 방지한다.
3. **`[RULE:BoundedBuffers]` 자원 상한**: 병렬 쿼리 워커 수는 `config.json`의 `max_workers_per_query` (기본 CPU 코어 수의 50%, 상한 32)로 명시적 바운딩된다.
4. **`[Invariant 3]` 비차단 읽기 일관성 보장**: Intra-Query 워커들은 동일한 부모 트랜잭션의 스냅샷 타임스탬프(`TxSnapshot`)를 공유하여, 병렬 스캔 중에도 쓰기 작업을 차단하지 않고 엄격한 Snapshot Isolation을 유지한다.
