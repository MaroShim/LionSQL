# 핫패스 3대 연산자(Sort, Join, GroupBy) 내부 슬롯 가속 플랜 (`notes/HOTPATH_TRIO_SLOT_ACCELERATION_PLAN.ko.md`)

> **문서 상태**: 완료 및 검증 (Completed & Verified)  
> **마일스톤**: `v0.95.0` (Core Execution Acceleration)  
> **관련 태스크**: `PERF-HOTPATH-TRIO-SLOT-ACCELERATION-1`  
> **불변식 원칙**: `[RULE:RadicalKISS]`, `[RULE:ZeroAllocation]`, `[RULE:LatencyBudget]`, `[RULE:PipelineVerification]`  
> **연계 문서**: [`doc/LIONSQL_PRAGMATIC_ARCHITECTURAL_CHOICES.md`](../doc/LIONSQL_PRAGMATIC_ARCHITECTURAL_CHOICES.md) (2-Tier Hybrid Execution)

---

## 1. 개요 및 배경

관계형 쿼리 처리기에서 전체 CPU 사이클의 95% 이상을 소비하는 병목은 항상 **3대 핫패스 연산자(The Big Three: Sort, HashJoin, Aggregate)**의 내부 반복 루프입니다.

전체 쿼리 파이프라인의 모든 연산자를 `BoundAST` 및 전면 정수 슬롯으로 치환하려는 학계식 접근(빅뱅 리팩토링)은 수만 줄의 코드 수정, 회귀 버그, 유지보수 비용 급증을 유발합니다(ROI 최악). 반면 LionSQL은 **"경계에서는 범용 튜플 계약을 유지하고, 반복 루프 내부에서만 사전 바인딩된 정수 슬롯(colIdx)으로 즉시 접근한다"**는 실용적 핫패스 국소 최적화 원칙을 확립했습니다.

---

## 2. 3대 연산자별 정수 슬롯 가속 설계

```
+-------------------------------------------------------------------------------+
|                        Volcano Tuple Stream (Boundary)                        |
|                               (map[string]any / Tuple)                        |
+-------------------------------------------------------------------------------+
                                      |
          +---------------------------+---------------------------+
          |                           |                           |
          v                           v                           v
+--------------------+      +--------------------+      +--------------------+
| 1. SortIterator    |      | 2. HashJoinIterator|      | 3. AggregateIterator|
| (quicksort O(NlogN)|      | (build/probe O(M+N)|      | (grouping/acc O(N))|
+--------------------+      +--------------------+      +--------------------+
| • Pre-bound colIdx |      | • Pre-bound colIdx |      | • Pre-bound colIdx |
| • Primitive Cast   |      | • Fast Int Hash    |      | • Flat Int Grouping|
|   (Int/Float/Str)  |      | • getTupleValFast  |      | • f.argColIdx 직결 |
| • Reflection 0회   |      | • Zero String Alloc|      | • makeKeyZeroAlloc |
+--------------------+      +--------------------+      +--------------------+
```

### 2.1 SortIterator (정렬 가속)
1. **사전 슬롯 바인딩**:
   - `Open()` 단계에서 정렬 대상 키의 물리 인덱스(`colIdx`)와 원시 타입(`sortType: Int, Float, String`)을 딱 1회만 확정.
2. **무반사(Reflection-Free) 고속 비교 루프**:
   - $O(N \log N)$ 퀵소트 루프에서 문자열 해시 맵 조회를 100% 제거하고 `tA.Values[colIdx]` 직접 슬롯 접근.
   - `int64`, `float64`, `string` 직결 비교기로 `CompareValues` 인터페이스 박싱 오버헤드 박멸.

### 2.2 HashJoinIterator (해시 조인 가속)
1. **Build & Probe 키 인덱스 사전 바인딩**:
   - `buildKeyIdx`, `probeKeyIdx`를 초기화 시 `resolveColIndex`를 통해 사전 확정.
2. **`getTupleValFast` & `flatIntTable` 직결**:
   - 단일 정수 조인 키(`int64`)인 경우, 문자열 변환 없이 원시 정수 해시 테이블(`map[int64][]hashMatch` / `flatIntTable`)에 바로 주입.
   - `t.Values[buildKeyIdx]` 슬롯 접근을 통해 튜플당 0-Alloc 달성.

### 2.3 AggregateIterator (GROUP BY / 집계 가속)
1. **그룹핑 키 및 집계 인자 슬롯 사전 바인딩**:
   - `a.groupKeyIdx []int` 배열에 GROUP BY 컬럼의 자식 연산자 물리 인덱스를 1회 캐싱.
   - 각 집계 함수 필드(`aggField`)의 `argColIdx`를 자식 컬럼 인덱스 맵에서 사전 바인딩.
2. **원시 슬롯 기반 그룹핑 및 누적**:
   - 행마다 컬럼 이름을 맵에서 찾지 않고 `tuple.Values[f.argColIdx]`를 통해 누적기(`accumulateNumeric`, `accumulateMinMax`)로 즉시 직결.

---

## 3. 백로그 태스크 매핑

| 태스크 ID | 도메인 | 마일스톤 | 우선순위 | 작업 내용 | 상태 |
| :--- | :---: | :---: | :---: | :--- | :---: |
| **`PERF-HOTPATH-TRIO-SLOT-ACCELERATION-1`** | Execution | **v0.95.0** | **P1** | Sort, HashJoin, Aggregate 3대 핫패스 연산자 내부 키 사전 바인딩 및 슬롯 직결 가속 체계 완성 | **완료** |

---

## 4. 완료 판정 및 벤치마크 효과

1. **CPU 캐시 히트율 극대화**: 반복 루프 내 해시 연산 및 동적 맵 버킷 조회 100% 제거.
2. **GC 압력 Zero**: 정렬/조인/집계 내부 핫패스에서 문자열 포맷팅 및 인터페이스 래핑 최소화.
3. **아키텍처 불변식 준수**: 복잡한 전면 바인더 구축 없이 2-Tier 하이브리드 아키텍처 원칙에 따라 실리적 성능 달성.
