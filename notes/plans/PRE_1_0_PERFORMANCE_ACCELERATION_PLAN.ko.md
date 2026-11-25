# 1.0 GA 대비 3대 성능 가속 핵심 아키텍처 설계서 (Pre-1.0 Performance Acceleration Plan)

> **작성일자**: 2026-11-24  
> **상태**: 진행 중 (In Progress)  
> **문서 버전**: v1.0  
> **관련 마일스톤**: v0.96.0 (Pre-1.0)  
> **관련 태스크**: `ARCH-PARALLEL-SCAN-1`, `PERF-FLAT-ARRAY-HASH-JOIN-1`, `PERF-JOIN-AGG-OPERATOR-FUSION-1`  
> **기반 소스**: `/tmp/sql9.txt` 및 `notes/COMPLEX_QUERY_PERFORMANCE_ACCELERATION_PLAN.ko.md`

---

## 1. 개요 및 배경 (Context & Problem Statement)

현재 LionSQL의 대용량 분석 및 조인 쿼리 지연시간은 수많은 마이크로 최적화(WiredTiger Cursor Zero-Copy, Hash Join Small-Table Build-Side, Vectorized Primitive Accumulator)를 통해 44ms 수준까지 단축되었다.
그러나 상용 C 엔진(PostgreSQL, DuckDB) 대비 1.5배 수준(15~20ms대)으로 진입하기 위해서는 단일 코어의 한계를 돌파하고 CPU L1/L2 캐시 및 파이프라인 융합을 극대화하는 **3대 핵심 성능 급소**를 1.0 이전에 해결해야 한다.

---

## 2. 3대 핵심 성능 가속 아키텍처

```mermaid
flowchart TD
    Query[분석/조인 쿼리 요청] --> Planner[CBO 최적화 플래너]
    
    subgraph MultiCoreParallel [1. Intra-Query 멀티코어 병렬화: ARCH-PARALLEL-SCAN-1]
        Planner --> Partitioner[테이블 64,000행 / N-Partition 분할]
        Partitioner --> Worker1[Goroutine Worker 1<br/>Local Scan & Filter]
        Partitioner --> Worker2[Goroutine Worker 2<br/>Local Scan & Filter]
        Partitioner --> Worker3[Goroutine Worker 3<br/>Local Scan & Filter]
        Partitioner --> Worker4[Goroutine Worker 4<br/>Local Scan & Filter]
    end
    
    subgraph FlatHashJoin [2. Flat Array Hash Join: PERF-FLAT-ARRAY-HASH-JOIN-1]
        Worker1 & Worker2 & Worker3 & Worker4 --> BuildCheck{Build 테이블 <= 1,000건?}
        BuildCheck --Yes--> FlatArrayProbe[L1/L2 캐시 친화 Flat Slice<br/>1ns Direct Pointerless Probe]
        BuildCheck --No--> StandardHash[표준 버킷 해시 조인]
    end
    
    subgraph OperatorFusion [3. Join-Aggregate Operator Fusion: PERF-JOIN-AGG-OPERATOR-FUSION-1]
        FlatArrayProbe --> FusedCheck{조인키 == 그룹키?}
        FusedCheck --Yes--> DirectAccum[중간 튜플 생성 생략<br/>Direct Hash Slot Accumulate: sum += val]
        FusedCheck --No--> MaterializeJoin[중간 조인 튜플 생성]
    end
    
    DirectAccum --> Gather[Gather / Final Merge Node]
    MaterializeJoin --> Gather
    Gather --> Result[최종 클라이언트 결과 반환 (15~20ms 진입)]
```

---

## 3. 세부 설계 및 구현 전략

### 3.1 [급소 1] Intra-Query 멀티코어 병렬화 (`ARCH-PARALLEL-SCAN-1`)
- **문제점**: 10,000건 ~ 100,000건 이상의 스캔 시 머신의 16~36개 코어 중 단 1개의 Goroutine만 100% 사용하고 나머지 코어가 유휴 상태(Idle)로 낭비됨.
- **워커 수(DOP: Degree of Parallelism) 결정 계층 구조 (Override Hierarchy)**:
  1. **쿼리 힌트 (Query Hint, 최우선)**: `OPTION (MAXDOP n)` 힌트를 통해 쿼리 단위로 병렬도를 명시적 오버라이드 (예: `OPTION (MAXDOP 1)`로 단일 스레드 강제, `OPTION (MAXDOP 4)`로 특정 코어 수 고정).
  2. **세션/시스템 설정 옵션 (Session / Config Option, 차우선)**: 세션 변수 `SET maxdop = n` / `SET max_parallel_workers = n` 또는 CLI 시작 플래그 `--max-parallel-workers=n`을 통해 세션/인스턴스 기본 병렬도 오버라이드.
  3. **기본 엔진 자동 산출 불변식 (Engine Default Fallback)**:
     - 공식: `workers = max(1, min(8, runtime.NumCPU() / 2))`
     - **CPU 수의 절반 (`numCPU / 2`)**: 단일 대형 분석 쿼리가 머신의 CPU를 100% 독점하여 동시 실행 중인 OLTP 트랜잭션, WAL flush, 웹 스튜디오 요청이 기아(Starvation) 상태에 빠지는 것을 방지하고 충분한 시스템 여유 자원(Headroom) 확보.
     - **최소 1개 (`min = 1`)**: 1코어 환경/저사양 컨테이너에서 `1 / 2 = 0`이 되어 데드락이 발생하는 것을 방어 (`Defensive Minimum = 1`).
     - **최대 8개 (`max = 8`)**: 32~64코어 고사양 서버에서도 단일 쿼리의 과도한 스케줄링 오버헤드, L3 캐시 경합(캐시 트래싱), Gather 병합 병목(Amdahl's Law)에 의한 수확 체감을 차단하는 엄격한 상한선 캡 (`Upper Cap = 8`).
- **KISS 해결책 (Parallel Chunk Scan & Gather Merge)**:
  1. 테이블 레코드 범위를 `workers`개 파티션 청크로 분할 (`atomic.Uint64` 또는 고정 오프셋 인출).
  2. 산출된 `workers`개 고루틴이 병렬 스캔 및 로컬 필터/부분 집계(Partial Aggregation) 수행.
  3. 최종 `Gather` 노드가 워커들의 부분 집계 결과(카테고리별 부분 합)만 병합(Merge).
- **예상 절감 효과**: 스캔 및 집계 시간 2.5배 ~ 3배 단축 (약 25~30ms 단축 ➔ 16~18ms 진입).

### 3.2 [급소 2] Flat Array Direct Probe 해시 조인 (`PERF-FLAT-ARRAY-HASH-JOIN-1`)
- **문제점**: 소형 빌드 테이블(1,000건 이하, 예: `test_cat` 5건)에서도 Go `map[uint64]*Tuple` 또는 연결 리스트 버킷을 조회하면서 포인터 체이싱(Pointer Chasing)으로 인한 CPU L1/L2 캐시 미스 발생.
- **KISS 해결책 (Flat Array Direct Probe)**:
  1. 빌드 테이블 행 수가 1,000건 이하인 경우, 포인터 기반 해시 테이블 대신 **평탄화된 연속 메모리 슬라이스(`[]FlatEntry`)**에 데이터 상주.
  2. 프로브 행이 지나갈 때 포인터 역참조 없이 CPU L1 캐시 속도(~1ns)로 연속 인덱스 다이렉트 매칭.
- **예상 절감 효과**: 조인 프로브 루프 연산 지연시간 30~40% 감소 (약 5~8ms 절감).

### 3.3 [급소 3] Join-Aggregate Operator Fusion (`PERF-JOIN-AGG-OPERATOR-FUSION-1`)
- **문제점**: `HashJoinIterator`가 10,000건을 조인하여 중간 튜플(`Left+Right`)을 메모리에 만들었다가, 상위 `AggregateIterator`가 이를 즉시 풀어서 `SUM(val)`, `COUNT(id)`에 누적한 뒤 버리는 불필요한 중간 행 생성/GC 낭비 발생.
- **KISS 해결책 (Fused Hash Join-Agg)**:
  1. 조인 키와 그룹핑 키가 일치하거나 포함 관계인 경우, 조인과 집계 연산자를 하나로 융합(`FusedJoinAggIterator`).
  2. 프로브 매칭 성공 즉시 중간 튜플 조립을 건너뛰고, 해시 테이블의 집계 슬롯(`sum += val`, `count++`)에 다이렉트 누적.
- **예상 절감 효과**: 중간 조인 튜플 생성 및 파이프라인 전송 오버헤드 100% 제거 (약 5ms 절감).

---

## 4. 백로그 태스크 매핑 (1.0 GA 이전 완료 확정)

| 태스크 ID | 도메인 | 우선순위 | 마일스톤 | 작업 내용 |
| :--- | :---: | :---: | :---: | :--- |
| `ARCH-PARALLEL-SCAN-1` | Engine | **P1** | **v0.96.0** | Intra-Query 멀티코어 병렬 쿼리 실행기 (ParallelSeqScan & Gather Merge) |
| `PERF-FLAT-ARRAY-HASH-JOIN-1` | Execution | **P1** | **v0.96.0** | Flat Array Direct Probe 해시 조인 (소형 빌드 테이블 L1/L2 캐시 상주) |
| `PERF-JOIN-AGG-OPERATOR-FUSION-1` | Execution | **P1** | **v0.96.0** | Join-Aggregate Operator Fusion (중간 튜플 생략 및 다이렉트 슬롯 누적) |
| `PERF-TOP-N-SORT-FUSION-1` | Execution | **P1** | **v0.96.0** | Top-N Sort Fusion (ORDER BY ... LIMIT k 전체 정렬 생략 및 Bounded Min-Heap 인-플레이스 선별) |
| `PERF-SCAN-FILTER-FUSION-1` | Execution | **P2** | **v0.96.0** | Scan-Filter Operator Fusion (스캔 루프 내 조건 직결 평가 및 불일치 튜플 객체 생성 스킵) |
| `PERF-MIN-MAX-INDEX-SEEK-1` | Execution | **P1** | **v0.96.0** | MIN()/MAX() 집계 함수 인덱스 O(1) 다이렉트 단방향 시크 최적화 (Index First/Last Leaf Seek) |
| `PERF-RUNTIME-BLOOM-FILTER-1` | Execution | **P2** | **v0.96.0** | Runtime Bloom Filter 푸시다운 (해시 조인 빌드 측 비트셋 기반 대형 프로브 테이블 조기 필터링) |
| `PERF-LIMIT-EARLY-EXIT-1` | Execution | **P2** | **v0.96.0** | Early-Exit LIMIT 스토리지 조기 탈출 (WHERE EXISTS 및 LIMIT n 도달 시 스캔 즉시 중단) |

---

## 5. 완료 기준 (Definition of Done)
1. 3개 가속 기술 구현 및 벤치마크 검증 완료 (`cmd/bench`, `TestComplexQueryBenchmark`).
2. 10,000건 조인+집계 쿼리 처리 시간 44ms ➔ **15~20ms대 진입** 달성.
3. 3단계 파이프라인(Build ➔ Test `-race` ➔ Commit) 100% 무결성 통과.
