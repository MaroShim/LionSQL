# LionSQL Vector Search & LSM Time-Series Architecture Plan (`POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-05
> - **상태**: 승인 완료 (1.0 이후 로드맵 반영: Vector `v1.1.0` / Time-Series `v1.2.0`)
> - **관련 태스크**: `FEAT-VECTOR-1`, `FEAT-TIMESERIES-LSM-1`
> - **핵심 불변식**: `[RULE:StaticBinary]` (단일 정적 바이너리), `[RULE:RadicalKISS]` (단순성 및 제로 외부 의존성), `[RULE:ModularInterface]` (스토리지 엔진 인터페이스 분리)

---

## 1. 개요 및 배경

LionSQL 1.0(LTS) 릴리스 이후, 최신 엔터프라이즈 환경 및 AI 생태계 수용을 위해 다음 2가지 핵심 영역의 확장을 추진한다:
1. **Vector (임베딩 및 유사도 검색)**: AI RAG 파이프라인(LangChain, LlamaIndex, Dify) 및 추천 시스템 지원을 위한 `pgvector` 호환 벡터 엔진.
2. **Time Series (시계열 데이터 처리) with LSM**: IoT 센서, 금융 틱 데이터, 서버 메트릭, 감사 로그 수집을 위한 초고속 순차 적재 및 고압축 스토리지 엔진.
3. **Geo/Spatial 기각 (Spec-Out)**: 방대한 C++ 외부 라이브러리(`GEOS`, `PROJ`) 의존성으로 인해 단일 정적 바이너리 철학을 파괴하므로 포괄적 PostGIS 지원은 영구 기각(단순 하버사인 2D 거리 계산 함수 `ST_Distance` 수준만 필요 시 고려).

---

## 2. Vector 지원 아키텍처 (`v1.1.0`)

### 2.1 설계 원칙
- **순수 Go 기반 무의존성 (Zero External Dependency)**: 외부 C 벡터 라이브러리 없이 Go 슬라이스 및 표준 런타임 CPU 벡터화(AVX2/NEON)를 활용하여 `[RULE:StaticBinary]` 완벽 준수.
- **In-Memory Flat Record 친화성**: LionSQL의 `OCC-HYPER-2` (Pointer-Free Flat Record) 메모리 공간에 연속 배열로 벡터를 배치하여 캐시 라인 미스 최소화.

### 2.2 기술 규격
1. **데이터 타입**:
   - `VECTOR(dim)`: 기본 32비트 부동소수점(`[]float32`) 슬라이스.
   - 최대 차원 수: 4,096차원 (OpenAI 1,536차원, Cohere 1,024차원, Llama 4,096차원 완전 커버).
2. **거리 연산자 및 스칼라 함수**:
   - L2 거리 (유클리드 거리): `<->` 연산자, `l2_distance(v1, v2)`
   - 코사인 거리 (Cosine Distance): `<=>` 연산자, `cosine_distance(v1, v2)`
   - 내적 (Inner Product / Negative Dot Product): `<#>` 연산자, `inner_product(v1, v2)`
3. **실행 계층 (단계별 로드맵)**:
   - **Phase 1 (v1.1.0 Exact Flat KNN)**:
     - 인덱스 빌드 오버헤드 없이 SIMD/Goroutine 병렬 전수 스캔(Exact KNN).
     - 10만 건 이하 벡터에서 1~5ms 이내 sub-millisecond 검색 달성.
   - **Phase 2 (v1.2.0+ In-Memory HNSW / IVFFlat)**:
     - 100만 건 이상 대규모 검색을 위한 순수 Go HNSW 인덱스 메모리 구조체 도입.

---

## 3. Time Series with LSM 아키텍처 (`v1.2.0`)

시계열 데이터의 100% Append-Only(순차 추가), 무수정(No Updates), 시간 범위 조회(Time-Range Scans), 과거 청크 일괄 삭제(Retention Drop) 특성을 극대화하기 위해 **LSM(Log-Structured Merge-Tree)** 구조를 채택한다.

### 3.1 경로 A vs 경로 B 종합 비교

| 비교 항목 | 경로 A (WiredTiger Native LSM) | 경로 B (Pure-Go Immutable Chunk LSM) |
| :--- | :--- | :--- |
| **핵심 메커니즘** | WiredTiger C 라이브러리 내장 `lsm:` URI 핸들러 | 메모리 MemTable + 시간 단위 불변 SSTable 파일 플러시 |
| **CGo 의존성** | CGo 필요 (`-tags wiredtiger`, 기본 빌드) | **순수 Go 100% (`CGO_ENABLED=0` 무의존성)** |
| **컴팩션 복잡도** | WiredTiger 엔진 내부 다계층 백그라운드 머지 | **컴팩션 0 (시간별 불변 블록화, 컴팩션 불필요)** |
| **디스크 삭제 비용** | LSM 톰스톤 기록 후 머지 단계에서 회수 | **OS 파일 삭제(`os.Remove`)로 $O(1)$ 즉각 회수** |
| **도입 난이도** | 낮음~중간 (URI 스토리지 매핑 설정) | 중간 (청크 롤오버 및 CBO 파티션 프루닝) |
| **주 타겟 환경** | 고성능 엔터프라이즈 서버 환경 | 경량 임베디드, 에지, Windows/ARM 순수 Go 환경 |

---

### 3.2 [전략 채택] 경로 A와 경로 B의 유기적 결합 (하이브리드 전략)

LionSQL은 단일 엔진 강제가 아닌, **WiredTiger와 In-Memory OCC 듀얼 엔진 아키텍처**를 이미 보유하고 있다. 따라서 두 경로를 대립시키지 않고 다음과 같이 **역할을 분담하여 유기적으로 통합**한다:

```
                      [ Client Application / Wire Protocol ]
                                         │
                   ┌─────────────────────┴─────────────────────┐
                   ▼                                           ▼
         [ 경로 A: WiredTiger ]                      [ 경로 B: Pure-Go OCC ]
      (-tags wiredtiger, 디스크형)              (CGO_ENABLED=0, 순수 Go/메모리형)
                   │                                           │
         ┌─────────┴─────────┐                       ┌─────────┴─────────┐
         ▼                   ▼                       ▼                   ▼
     [ B+Tree ]          [ LSM ]                 [ MemTable ]     [ Immutable Chunks ]
  일반 OLTP 테이블    대규모 시계열 테이블        현재 시계열 수집     과거 시계열 디스크 보존
   (table:users)       (lsm:sensor)           (RAM Flat Record)     (data/chunks/2026-11)
```

#### 1) 런타임 및 DDL 통일 인터페이스
사용자는 DDL 레벨에서 단일한 표준 문법으로 시계열 테이블을 선언하며, 엔진은 현재 기동 모드(WiredTiger vs Pure-Go Memory)에 맞춰 최적의 내부 LSM 경로를 자동 선택한다:

```sql
CREATE TABLE device_metrics (
    ts TIMESTAMP NOT NULL,
    device_id INT NOT NULL,
    temperature DOUBLE,
    cpu_usage DOUBLE
) WITH (
    TYPE = 'TIMESERIES',      -- 시계열 선언
    INTERVAL = '1 DAY',       -- 청크 분할 단위 (경로 B)
    RETENTION = '90 DAYS'     -- 자동 데이터 보존 만료 기한
);
```

#### 2) 경로 A 상세 구현 (WiredTiger 엔진 모드)
- LionSQL의 스토리지 생성 로직에서 `WITH (TYPE = 'TIMESERIES')` 옵션 감지 시:
  - 스토리지 URI를 `table:<name>` 대신 `lsm:<name>`으로 라우팅.
  - 내장 블록 압축기(Snappy/Zstd)를 SSTable 블록에 직결.
  - 인서트 시 WiredTiger 내부 메모리 버퍼 ➔ L0~Ln 계층 머지가 자동 수행되어 쓰기 증폭(Write Amplification) 최소화.

#### 3) 경로 B 상세 구현 (Pure-Go / Memory 엔진 모드)
- **수집 단계 (MemTable)**:
  - 현재 시계열 윈도우(예: 당일)는 `In-Memory OCC Flat Record` 메모리에 초당 수십만 건 속도로 무지터 적재.
- **플러시 단계 (Zero-Compaction SSTable)**:
  - 청크 윈도우가 마감되면(예: 자정 00:00) 백그라운드 워커가 메모리 데이터를 타임스탬프 순으로 정렬.
  - 헤더에 `[MinTS, MaxTS, MinValue, MaxValue, BloomFilter]` 메타데이터를 포함한 단일 압축 바이너리 파일(`.sstable` 또는 `.chunk`)로 디스크에 원자적(Atomic) 기록.
  - 시계열 데이터는 과거 변경이 없으므로(Append-Only), 후속 백그라운드 컴팩션 루프가 일절 필요 없음 (I/O 스파이크 제로).
- **조회 단계 (Partition Pruning)**:
  - `SELECT * FROM device_metrics WHERE ts >= '2026-11-01'` 쿼리 발생 시, 플래너가 메타데이터 헤더의 `MinTS/MaxTS`를 검사하여 관련 없는 날짜의 청크 파일은 디스크 I/O 자체를 100% 생략.
- **만료 단계 (Instant Drop)**:
  - `RETENTION = '90 DAYS'` 초과 청크는 OS 파일 삭제(`os.Remove`)로 1ms 내에 $O(1)$ 공간 즉각 반환 (VACUUM 및 B+Tree 재구성 오버헤드 0).

---

## 4. 쿼리 및 분석 편의 기능 (`v1.2.0`)

시계열 데이터 집계 및 다운샘플링을 위한 내장 분석 함수 제공:
1. **`time_bucket(interval, timestamp)`**:
   ```sql
   SELECT time_bucket('5 minutes', ts) AS five_min,
          device_id,
          AVG(temperature) AS avg_temp,
          MAX(cpu_usage) AS max_cpu
   FROM device_metrics
   WHERE ts >= NOW() - INTERVAL '1 hour'
   GROUP BY five_min, device_id
   ORDER BY five_min DESC;
   ```
2. **`locf(column)`** (Last Observation Carried Forward):
   - 센서 네트워크 단절로 결측치가 발생했을 때 직전 관측치로 자동 보간.
3. **단순 지리 유틸리티 (절제된 2D 하버사인)**:
   - 복잡한 PostGIS는 배제하되, 모바일/센서 시계열 위치 추적을 위한 가벼운 내장 함수 지원:
   - `ST_Distance(lat1, lon1, lat2, lon2)` ➔ 두 위경도 좌표 간의 구면 미터 거리 계산.

---

## 5. 실행 로드맵 및 단계별 계획

```
[ 1.0 LTS Release ] ───► [ v1.1.0: Vector & AI ] ───► [ v1.2.0: Time-Series with LSM ]
  - OCC-HYPER-2            - VECTOR(dim) 데이터 타입       - DDL: WITH (TYPE='TIMESERIES')
  - WEB-SPA-1              - L2 / Cosine / Dot 거리 연산자  - 경로 A: WiredTiger lsm: 핸들러
  - PKG-OFFLINE-DOCS       - 병렬 Flat KNN (Exact Scan)   - 경로 B: Pure-Go SSTable 청크
  - 6 Quality Gates        - LangChain / pgvector 호환    - time_bucket(), Retention Auto-Drop
```

1. **1.0 릴리스 직후 (`v1.1.0`)**:
   - `FEAT-VECTOR-1` 구현: 순수 Go 기반 `VECTOR(dim)` 타입, `<->`, `<=>` 거리 연산자, 멀티코어 병렬 전수 Flat KNN 탑재.
2. **차기 메이저 피처 (`v1.2.0`)**:
   - `FEAT-TIMESERIES-LSM-1` 구현:
     - DDL 파서에 `TYPE = 'TIMESERIES'` 및 `RETENTION` 구문 지원.
     - WiredTiger `lsm:` 스토리지 핸들러 연동 (경로 A).
     - Pure-Go 메모리 버퍼 ➔ 불변 일별 청크 플러시 및 $O(1)$ 파일 삭제 구현 (경로 B).
     - `time_bucket()` 분석 함수 및 Volcano CBO 타임스탬프 파티션 프루닝 통합.
