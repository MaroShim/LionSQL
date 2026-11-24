# [PERF-VECTOR-PRIMITIVE-ACCUM-1] Vectorized Primitive Accumulator: Interface 언박싱 제거 및 타입 특화 집계 루프

- **작성일**: 2026-11-24
- **상태**: IN_PROGRESS
- **태스크 ID**: `PERF-VECTOR-PRIMITIVE-ACCUM-1` (Priority: P1, Target: v0.95.0)
- **대상 패키지**: `pkg/execution`

> **규칙 준수**:
> - `[RULE:PlanPersistence]`: 설계 및 구현 명세를 물리 파일로 영구 보존.
> - `[RULE:RadicalKISS]`: 복잡한 동적 코드 생성 대신 정적 타입 스위치 및 전용 원시 누적 슬롯(`intSums`, `sums`, `decSums`) 구축.
> - `[RULE:ZeroAllocation]`: 행마다 발생하는 `tryDecimal`, `toFloat64`, `CompareValues` 오버헤드 및 힙 할당 0 달성.
> - `[RULE:PipelineVerification]`: 3단계 파이프라인 (Build ➔ Test `-race` ➔ Commit).
> - `[RULE:StyleIntegrity]`: 장식용 이모지 일체 배제.

---

## 1. 배경 및 문제점 분석

1. **`interface{}` 동적 언박싱 및 분기 예측 실패(Branch Misprediction)**:
   - `AggregateIterator.accumulateTuple()`은 매 튜플마다 집계 인자 `val any`를 추출한 후:
     - `isDecValue(val)`, `tryDecimal(val)`을 호출하여 문자열 파싱/타입 판별 수행.
     - `toFloat64(val)`을 호출하여 10개 이상의 타입(`int`, `int32`, `int64`, `uint`, `float32`, `float64`, `string`)을 동적 스위치.
     - `int64` 합산임에도 `float64` 부동소수점 누적으로 변환 후 매 행 덧셈.
     - 결과 구체화(Materialize) 시 매 그룹마다 `math.Trunc(s)`를 호출하여 정수 여부를 판별.
   - 이로 인해 CPU 파이프라인 스톨 및 불필요한 연산 낭비가 심각함.

2. **MIN/MAX 비교 시 범용 `CompareValues` 오버헤드**:
   - `aggMin`, `aggMax` 연산 시 매 행마다 리플렉션과 동적 타입 매칭을 수행하는 `CompareValues(val, acc.mins[fIdx])`를 호출함.
   - 단일 원시 정수(`int64`)나 실수(`float64`)인 경우 단 1개의 CPU 비교 명령어(`v < cur`)로 끝날 수 있는 연산에 수십 나노초의 지연 발생.

---

## 2. 핵심 아키텍처 및 해결 방안

### 2.1 전용 원시 누적 슬롯 (`intSums`, `hasIntSum`) 도입
- `groupAccumulator` 구조체에 64비트 정수 직접 누적을 위한 `intSums []int64` 및 `hasIntSum []bool` 추가.
- `int64`, `int`, `storage.TypeInt`인 경우 즉시 `acc.intSums[fIdx] += v`로 CPU 레지스터 직접 누적.
- 부동소수점 변환 및 `math.Trunc` 판별 완전 제거.

### 2.2 원시 타입 Fast-Path 타입 스위치 인라이닝
- `val`에 대해 `switch v := val.(type)`를 통해 `int64`, `float64`, `storage.Value`를 최우선 1회 분기.
- `aggMin` / `aggMax`: 현재 최솟값/최댓값이 `int64` 또는 `float64`인 경우 `CompareValues`를 완전히 바이패스하고 `v < cur` / `v > cur` 직접 비교.

---

## 3. 세부 구현 단계 (Action Items)

1. `pkg/execution/aggregate.go`:
   - `groupAccumulator`에 `intSums []int64`, `hasIntSum []bool` 필드 추가 및 `newGroupAccumulator` 초기화.
   - `accumulateTuple`: `aggSum`, `aggTotal`, `aggAvg`, `aggMin`, `aggMax`에 타입 특화 Fast-Path 적용.
   - 결과 구체화: `acc.hasIntSum[fIdx]`인 경우 정수 직접 반환.
2. 벤치마크 및 검증:
   - `pkg/execution/aggregate_bench_test.go` 실행하여 레이턴시 및 CPU 사이클 측정.
   - SQLLogicTest Core 및 단위 테스트 100% 통과 확인.
