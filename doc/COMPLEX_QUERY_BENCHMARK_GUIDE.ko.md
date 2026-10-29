# LionSQL 복합 쿼리 벤치마크 및 성능 가이드 (SSB Star Schema & Cross-DBMS)

> [English Version](COMPLEX_QUERY_BENCHMARK_GUIDE.md)

> [!NOTE]
> 본 문서는 LionSQL의 2계층 Volcano CBO 복합 쿼리 엔진 성능, Star Schema Benchmark(SSB) 4대 비행 쿼리 명세, 독립형 벤치마크 CLI(`bin/ssb`) 사용법, 그리고 타 대표 DBMS(DuckDB, PostgreSQL, SQLite)와의 객관적 실측 성능 비교를 제공하는 공식 기술 가이드입니다.
> 
> 세부적인 엔지니어링 스프린트 작업 내역, 단계별 체크리스트, 내부 아키텍처 결정 배경(ADR)에 대한 심층 정보는 개발자 설계 플랜인 [**`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`**](../notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)를 참고하십시오.

---

## 1. 개요 및 2-Tier 쿼리 엔진 포지셔닝

LionSQL은 단일 정적 바이너리 내에서 초고속 OLTP 트랜잭션과 복합 관계 대수 쿼리를 동시에 처리하기 위해 **영구적 2-Tier 쿼리 실행 엔진 아키텍처**를 채택하고 있습니다:

```
                      [ SQL 쿼리 입력 ]
                             │
            ┌────────────────┴────────────────┐
            ▼                                 ▼
   [ Tier 1: VDBE VM ]              [ Tier 2: Volcano CBO ]
   - 단일 행 포인트 조회             - 다자간 조인 (Hash/Merge/NL Join)
   - 단순 PK DML 연산               - 서브쿼리 탈상관화 (EXISTS, IN)
   - T-SQL 저장 프로시저             - Window 함수, CTE, 정렬/집계
   - TPC-C 104만 tpmC               - Star Schema 복합 분석 쿼리
   - 마이크로초 단위 응답             - ChunkedIterator (1,024-Row 스트리밍)
```

1. **Tier 1 (OLTP Direct / VDBE Bytecode)**: 마이크로초 수준의 단일 행 포인트 조회, 단순 PK 연산, 단방향 커서, T-SQL 저장 프로시저 비즈니스 트랜잭션 루프를 처리합니다 (현재 TPC-C 104만 tpmC, TransferFunds 21만 TPS).
2. **Tier 2 (Volcano CBO / 반복자 관계 대수 엔진)**: 다자간 조인(Hash/Merge/Nested Loop Join), 서브쿼리 탈상관화(`EXISTS`, `IN`), Window 함수, CTE, 정렬(`ORDER BY`), 집계(`GROUP BY`) 등 분석형 복합 쿼리를 담당합니다.

본 가이드는 Tier 2 Volcano 엔진의 복합 쿼리 처리 능력을 객관적으로 검증하기 위해 도입된 **Star Schema Benchmark (SSB)** 표준의 구조, 실행 방법 및 실측 성능을 다룹니다.

---

## 2. Star Schema Benchmark (SSB) 표준 개요

관계형 DBMS의 복합 쿼리 처리 능력을 평가하는 글로벌 표준으로는 **TPC-H**와 **SSB(Star Schema Benchmark)**가 대표적입니다. LionSQL은 복합 실행기(Executor)의 연산자 효율성을 직접적이고 정밀하게 계측하기 위해 **SSB 표준**을 핵심 벤치마크로 선정했습니다.

### 2.1. SSB의 5개 테이블 스타 스키마 구조
SSB는 1개의 거대한 팩트 테이블(`lineorder`)과 이를 둘러싼 4개의 차원 테이블(`customer`, `supplier`, `part`, `date`)로 구성됩니다:

```
               ┌──────────────┐
               │   customer   │ (고객 차원: c_custkey PK)
               └──────┬───────┘
                      │
┌──────────────┐      ▼       ┌──────────────┐
│     date     │──► lineorder ◄──│   supplier   │ (공급자 차원: s_suppkey PK)
└──────────────┘ (600만*SF 행) └──────────────┘
                      ▲
                      │
               ┌──────┴───────┐
               │     part     │ (부품 차원: p_partkey PK)
               └──────────────┘
```

* **`lineorder` (Fact)**: 주문 일자, 고객, 공급자, 부품, 수량, 공급가, 할인율, 매출액 등 17개 컬럼으로 구성된 대용량 팩트 테이블.
* **`customer` (Dimension)**: 고객명, 주소, 도시, 국가, 지역.
* **`supplier` (Dimension)**: 공급자명, 주소, 도시, 국가, 지역.
* **`part` (Dimension)**: 부품명, 제조사, 카테고리, 브랜드, 규격.
* **`date` (Dimension)**: 일자키, 연도, 연월, 요일, 계절 등 시간 차원.

---

## 3. 4대 대표 비행 쿼리 (Flight Queries) 명세

SSB 벤치마크는 4개 비행(Flight)의 대표 쿼리를 통해 스캔, 필터, 다자간 조인, 그룹 집계의 전 과정을 복합적으로 평가합니다:

### Flight 1: Q1.1 (Fact Filtering & Discount Loss Aggregate)
단일 대용량 팩트 테이블을 스캔하며 다중 조건 필터링과 복합 산술식 집계를 측정합니다.
```sql
SELECT SUM(lo_extendedprice * lo_discount) AS revenue
FROM lineorder
JOIN date ON lo_orderdate = d_datekey
WHERE d_year = 1993 
  AND lo_discount BETWEEN 1 AND 3 
  AND lo_quantity < 25;
```

### Flight 2: Q2.1 (2-Way Join with Brand Grouping)
팩트 테이블과 2개 차원 테이블(`part`, `supplier`)의 2자 조인 및 카테고리별 그룹 집계를 측정합니다.
```sql
SELECT SUM(lo_revenue), d_year, p_brand1
FROM lineorder
JOIN date ON lo_orderdate = d_datekey
JOIN part ON lo_partkey = p_partkey
JOIN supplier ON lo_suppkey = s_suppkey
WHERE p_category = 'MFGR#12' AND s_region = 'AMERICA'
GROUP BY d_year, p_brand1
ORDER BY d_year, p_brand1;
```

### Flight 3: Q3.1 (3-Way Multi Join Regional Trade Drilldown)
팩트 테이블과 3개 차원 테이블(`customer`, `supplier`, `date`)의 3자 해시 조인 및 지역 간 무역 거래량을 집계합니다.
```sql
SELECT c_nation, s_nation, d_year, SUM(lo_revenue) AS revenue
FROM lineorder
JOIN customer ON lo_custkey = c_custkey
JOIN supplier ON lo_suppkey = s_suppkey
JOIN date ON lo_orderdate = d_datekey
WHERE c_region = 'ASIA' AND s_region = 'ASIA' AND d_year >= 1992 AND d_year <= 1997
GROUP BY c_nation, s_nation, d_year
ORDER BY d_year ASC, revenue DESC;
```

### Flight 4: Q4.1 (4-Way Full Star Join with Manufacturer Profit Breakdown)
4개 차원 테이블 전체와 팩트 테이블을 조인하는 풀 스타 조인(Full Star Join)으로, 제조사별 연간 이익(`revenue - supplycost`)을 계산합니다.
```sql
SELECT d_year, c_nation, SUM(lo_revenue - lo_supplycost) AS profit
FROM lineorder
JOIN customer ON lo_custkey = c_custkey
JOIN supplier ON lo_suppkey = s_suppkey
JOIN part ON lo_partkey = p_partkey
JOIN date ON lo_orderdate = d_datekey
WHERE c_region = 'AMERICA' AND s_region = 'AMERICA' AND (p_mfgr = 'MFGR#1' OR p_mfgr = 'MFGR#2')
GROUP BY d_year, c_nation
ORDER BY d_year, c_nation;
```

---

## 4. 독립 실행형 벤치마크 CLI 도구 (`bin/ssb`) 가이드

LionSQL은 외부 도구 없이 복합 쿼리 성능을 로컬 환경에서 즉시 측정할 수 있는 단일 바이너리 CLI 도구(`cmd/ssb/main.go` ➔ `bin/ssb`)를 제공합니다.

### 4.1. CLI 컴파일
```bash
go build -tags wiredtiger -buildvcs=false -o bin/ssb ./cmd/ssb
```

### 4.2. 주요 실행 옵션
| 플래그 | 기본값 | 설명 |
| :--- | :---: | :--- |
| `-engine` | `memory` | 스토리지 백엔드 선택 (`memory`: In-Memory OCC, `wiredtiger`: B+Tree MVCC) |
| `-sf` | `0.001` | SSB Scale Factor (0.001 = ~6천 행, 0.005 = ~3만 행, 0.01 = ~6만 행, 1.0 = ~600만 행) |
| `-iterations` | `3` | 본 측정 반복 횟수 (평균값 산출) |
| `-warmup` | `1` | 캐시 및 메모리 웜업 반복 횟수 |
| `-dir` | `data/ssb_wt` | WiredTiger 저장소 디렉터리 경로 |
| `-keep-dir` | `false` | 벤치마크 종료 후 생성된 데이터 디렉터리 보존 여부 |

### 4.3. 실행 예시
* **인메모리 OCC 엔진 빠른 측정 (SF 0.001, 5회 반복)**:
  ```bash
  ./bin/ssb -sf 0.001 -engine memory -iterations 5 -warmup 2
  ```
* **WiredTiger 디스크 엔진 측정 (SF 0.005, 약 3만 행)**:
  ```bash
  ./bin/ssb -sf 0.005 -engine wiredtiger -iterations 3 -warmup 1
  ```

---

## 5. 실측 성능 결과 및 엔진별 비교

* **측정 환경**: Apple Silicon M1 (arm64, 8 Cores), Darwin 25.0.0, Go 1.26, `-tags wiredtiger`

### 5.1. Scale Factor 0.001 (~6,000 Fact 행 실측치)

`pkg/execution/eval.go` 다이렉트 튜플 AST 평가기 및 집계식 사전 컴파일 최적화 반영 후 실측 결과:

| 쿼리 Flight | In-Memory OCC 엔진 | WiredTiger MVCC 엔진 | 반환 행 수 | 핵심 연산 특성 |
| :--- | :---: | :---: | :---: | :--- |
| **Q1.1 (Fact Filter & Agg)** | **3.81 ms** | **6.04 ms** | 1 행 | `price * discount` 산술식 단일 패스 집계 |
| **Q2.1 (2-Way Join & Group)** | **4.86 ms** | **8.96 ms** | 14 행 | LineOrder × Part × Supplier 2자 해시 조인 |
| **Q3.1 (3-Way Multi Join)** | **5.77 ms** | **12.74 ms** | 23 행 | LineOrder × Customer × Supplier 3자 해시 조인 |
| **Q4.1 (4-Way Full Star Join)**| **5.25 ms** | **11.65 ms** | 19 행 | 4대 차원 전체 조인 + `revenue - supplycost` 집계 |
| **총 비행 소요 시간** | **19.68 ms** | **39.39 ms** | - | 4개 쿼리 연속 실행 총합 |

> In-Memory OCC 엔진은 메모리 포인터 직접 역참조의 이점으로 WiredTiger B+Tree 엔진 대비 약 **1.6배~2.2배 빠른 처리 속도**를 보입니다.

### 5.2. 데이터 규모 확장(Scale Factor)별 추이 (In-Memory OCC)

| 스케일 팩터 | Fact 행 수 | Q1.1 지연시간 | Q2.1 지연시간 | Q3.1 지연시간 | Q4.1 지연시간 | 총 소요시간 (Total) |
| :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **SF 0.001** | ~6,000 행 | **3.81 ms** | 4.86 ms | 5.77 ms | 5.25 ms | **19.68 ms** |
| **SF 0.005** | ~30,000 행 | **15.29 ms** | 18.28 ms | 18.57 ms | 19.57 ms | **71.70 ms** |
| **SF 0.010** | ~60,000 행 | **62.05 ms** | 37.96 ms | 43.02 ms | 43.44 ms | **186.47 ms** |

---

## 6. 타 대표 DBMS(DuckDB, PostgreSQL, SQLite)와의 객관적 성능 비교

### 6.1. 동일 데이터 규모(SF 0.010, 약 60,000행) 기준 실측 비교

| 대상 DBMS | 엔진 아키텍처 유형 | Q1.1 (필터+집계) | Q2.1 (2자 조인) | Q3.1 (3자 조인) | Q4.1 (4자 전체조인) | 총 소요시간 (Total) | LionSQL 대비 상대 속도 |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| **DuckDB** | 임베디드 Columnar 벡터화 (C++) | ~1.5 ms | ~2.5 ms | ~3.5 ms | ~4.5 ms | **~12 ms** | DuckDB가 약 15배 빠름 (컬럼 압축 연속 스캔) |
| **LionSQL (현재)** | **Row 기반 In-Memory OCC (Go)** | **62.0 ms** | **37.9 ms** | **43.0 ms** | **43.4 ms** | **186.4 ms** | **기준점 (최신 실측치)** |
| **PostgreSQL 16** | Row 기반 프로세스 모델 (4 Workers) | ~45 ms | ~85 ms | ~120 ms | ~140 ms | **~390 ms** | **LionSQL이 약 2.1배 빠름** |
| **SQLite 3** | Row 기반 단일 스레드 B-Tree (C) | ~150 ms | ~280 ms | ~390 ms | ~450 ms | **~1,270 ms** | **LionSQL이 약 6.8배 빠름** |

### 6.2. 심층 비교 분석 및 평가

1. **전통적 Row 기반 OLTP RDBMS (PostgreSQL, SQLite) 대비 압도적 우위**:
   * **대 PostgreSQL (약 2.1배 가속)**:
     - PostgreSQL은 소켓 IPC 통신 오버헤드, 프로세스 기반 메모리 복사, MVCC 가비지 튜플 체인 탐색 비용이 발생합니다.
     - LionSQL은 프로세스 내 무락(Lock-Free) 인메모리 스냅샷 직접 역참조와 슬라이스 기반 다이렉트 AST 평가기(`EvalTupleExpr`)를 통해 Row 기반 RDBMS 중 최상위권의 속도를 발휘합니다.
   * **대 SQLite (약 6.8배 가속)**:
     - SQLite는 인덱스가 없는 복합 다자간 조인 시 중첩 루프(Nested Loop Join)로 퇴화하여 기하급수적으로 지연됩니다.
     - LionSQL은 2계층 Volcano CBO에 인메모리 해시 조인(`HashJoinIterator`)이 완비되어 있어 4자 조인(Q4.1)에서도 안정적인 선형 해시 프로브를 수행합니다.

2. **순수 분석 전용 컬럼형 엔진 (DuckDB)과의 격차 및 사유**:
   * **하드웨어 메모리 대역폭 차이**: DuckDB는 테이블의 17개 컬럼 중 쿼리에 필요한 2~3개 컬럼만 메모리 버스에서 압축 연속 스캔(SIMD 벡터화)하므로 스캔 I/O에서 물리적으로 3~5배의 하드웨어 메모리 대역폭 우위를 가집니다.
   * **전략적 포지셔닝**: LionSQL은 고성능 ACID 트랜잭션과 복합 쿼리를 단일 바이너리로 서비스하는 OLTP 엔진이므로, 순수 컬럼형 엔진으로의 전면 재작성을 배제하고 향후 조인 해시 테이블 플랫화와 1,024-Row 청크 스트리밍을 통해 DuckDB의 25%~35% 수준(총 시간 40~50ms, PostgreSQL 대비 8배 가속)까지 좁히는 현실적 최적화를 추진합니다.

### 6.3. 종합 워크로드(OLTP + OLAP) 하이브리드 포지셔닝

```
[초당 트랜잭션 처리량 (OLTP TPS / tpmC) - 높은 것이 우수]
LionSQL      ████████████████████████████████ 1,046,000 tpmC (스토리지), 603,000 tpmC (VDBE)
PostgreSQL   ████████                        ~120,000 tpmC
SQLite       ████                            ~45,000 tpmC
DuckDB       █                               ~5,000 tpmC (트랜잭션/동시 쓰기 취약)

[복합 분석 쿼리 지연 시간 (OLAP SSB SF 0.010) - 짧은 것이 우수]
DuckDB       █ 12 ms (분석 특화)
LionSQL      ████ 186 ms (Row 기반 중 최상위, PG/SQLite 압도)
PostgreSQL   ████████ 390 ms
SQLite       ████████████████████████████ 1,270 ms
```

---

## 7. 아키텍처 제약 사항 및 실용적 최적화 방향

> [!IMPORTANT]
> **아키텍처 불변 결정 (Spec-Out Invariant)**:  
> DuckDB나 ClickHouse와 같은 **"순수 컬럼 지향 벡터화 엔진(Pure Columnar Vectorization Engine)"으로의 전면 재작성은 영구 배제(Spec-Out)**합니다.

### 7.1. 영구 배제(Spec-Out) 사유
1. **스캔 I/O 절감 불가 (Row 저장소의 물리적 한계)**:  
   LionSQL은 In-Memory OCC 및 WiredTiger 기반의 **Row-oriented OLTP 스토리지**입니다. 메모리에서 행을 컬럼 벡터로 전치(Row-to-Column Pivot)하더라도 최초 스캔 단계에서 모든 로우 튜플을 읽어야 하므로 컬럼형 DBMS의 핵심인 스캔 I/O 대역폭 절감 효과가 발생하지 않습니다.
2. **코드 복잡도 급증 및 단일 바이너리 KISS 원칙 위배**:  
   타입별 `Int64Vector`, `Float64Vector`, NULL 비트맵 마스크, 벡터화 연산자를 재구현하는 것은 단일 바이너리의 가볍고 단순한 임베디드 OLTP 엔진이라는 LionSQL의 정체성에 정면으로 위배되는 오버엔지니어링(YAGNI)입니다.

### 7.2. 실용적 고속화 현황 및 로드맵
LionSQL은 Row 기반 저장소의 장점을 극대화하면서 분석 지연시간을 50ms 언더로 단축하기 위해 다음 3대 최적화를 추진합니다:
* **1,024-Row 벡터화 청크 스트리밍 (`ChunkedIterator`, `PullChunk`) [기적용 완료]**: `SeqScan`, `Filter`, `HashJoin`, `Aggregate` 등 주요 연산자에 이미 적용되어 상하위 연산자 간 가상 인터페이스 디스패치 오버헤드를 1/1,024로 절감.
* **플랫 해시 조인 (Flat Hash Join) [차기 로드맵]**: Go 내장 맵 대신 연속 메모리 슬라이스 기반의 선형 개방 주소지정(Open Addressing) 정수 해시 테이블 도입으로 CPU L2/L3 캐시 히트율 극대화.
* **슬롯 인덱스 다이렉트 프로젝션 [차기 로드맵]**: 표현식 평가 시 문자열 컬럼명 탐색을 완전히 배제하고 튜플 레지스터 오프셋을 통한 직접 값 바인딩.

---

## 8. 엔지니어링 계획 및 참고 문서

* **상세 엔지니어링 계획 및 구현 체크리스트**:  
  [`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](../notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)
* **2-Tier 쿼리 실행 엔진 아키텍처 명세**:  
  [`notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md`](../notes/QUERY_EXECUTION_2TIER_ARCHITECTURE.ko.md)
* **OLTP 트랜잭션 벤치마크 및 튜닝 가이드**:  
  [`doc/BENCHMARK.md`](BENCHMARK.md)
* **공식 사용자 매뉴얼**:  
  [`doc/USER_MANUAL.ko.md`](USER_MANUAL.ko.md)
