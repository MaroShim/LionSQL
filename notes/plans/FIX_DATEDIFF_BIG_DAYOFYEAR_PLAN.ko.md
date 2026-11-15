# DATEDIFF / DATEDIFF_BIG의 dayofyear, weekday 및 서브세컨드 datepart 지원 계획서
(`notes/plans/FIX_DATEDIFF_BIG_DAYOFYEAR_PLAN.ko.md`)

> **문서 상태**: Completed (구현 완료 및 검증 통과)  
> **태스크 ID**: `FIX-DATEDIFF-BIG-DAYOFYEAR-1`  
> **마일스톤**: `1.0-GA` (T-SQL Function Parity)  
> **관련 규칙**: `[RULE:SafeDefaults]`, `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`

---

## 1. 추진 배경 및 결함 분석 (Problem Statement)

### 1.1 결함 현상
1. Microsoft SQL Server T-SQL 표준에서 `DATEDIFF` 및 `DATEDIFF_BIG` 함수는 `day`, `dayofyear`, `weekday`를 날짜 단위 datepart로 동등하게 취급합니다. 그러나 기존 구현에서는 `dayofyear`와 `weekday`가 누락되어 `NULL`을 반환하는 비호환 결함이 존재했습니다.
2. 기존 `day` 계산은 단순 시간 차이 `t2.Sub(t1).Hours() / 24`로 구현되어 있어, **자정(00:00:00) 날짜 경계를 넘는 T-SQL 표준 의미(Boundary Crossing Count)**를 올바르게 계산하지 못하고 24시간 미만 차이 시 0일로 잘못 계산되는 오류가 있었습니다 (예: 23:59:00에서 익일 00:01:00은 1일 차이여야 하나 0일로 계산).
3. `microsecond`(`mcs`) 및 `nanosecond`(`ns`) 서브세컨드 단위 지원 누락.

---

## 2. 해결 목표 (Target Architecture)

1. **`day`, `dayofyear`, `weekday` 통합 및 자정 날짜 경계 계산**:
   - `t1`, `t2`의 시간을 자정(`00:00:00 UTC`)으로 정규화한 날짜 간격을 계산하여 T-SQL 날짜 경계 교차 규격을 100% 충족.
2. **datepart 별칭 및 서브세컨드 단위 확장**:
   - `normalizeDatePart`에 `microsecond`(`mcs`), `nanosecond`(`ns`) 추가.
   - `evalDateDiff`에서 `diff.Microseconds()`, `diff.Nanoseconds()` 지원.

---

## 3. 세부 구현 내용 (`pkg/proc/builtins_date.go`)

```go
func evalDateDiff(part string, t1, t2 time.Time) storage.Value {
	switch part {
	...
	case "day", "dayofyear", "weekday":
		d1 := time.Date(t1.Year(), t1.Month(), t1.Day(), 0, 0, 0, 0, time.UTC)
		d2 := time.Date(t2.Year(), t2.Month(), t2.Day(), 0, 0, 0, 0, time.UTC)
		return storage.IntValue(int64(d2.Sub(d1) / (24 * time.Hour)))
	case "microsecond":
		diff := t2.Sub(t1)
		return storage.IntValue(diff.Microseconds())
	case "nanosecond":
		diff := t2.Sub(t1)
		return storage.IntValue(diff.Nanoseconds())
	...
	}
}
```

---

## 4. 검증 결과 (`pkg/proc/builtins_test.go`)

- `TestDateDiff_DayOfYear_And_Weekday`:
  - `DATEDIFF(dayofyear, ...)` 및 `DATEDIFF(weekday, ...)` 정상 계산 확인.
  - 23:59:00에서 익일 00:01:00 간의 날짜 경계 계산(1일) 정합성 입증.
  - `DATEDIFF_BIG` 및 서브세컨드(`microsecond`, `nanosecond`) 정밀도 검증 완료.
- `pkg/proc` 전체 단위 테스트 100% PASS.
