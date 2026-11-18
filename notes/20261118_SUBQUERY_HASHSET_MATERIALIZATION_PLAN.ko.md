# 비상관 IN (SELECT) 서브쿼리 In-Memory HashSet 구체화 아키텍처 설계서 (`notes/20261118_SUBQUERY_HASHSET_MATERIALIZATION_PLAN.ko.md`)

## 1. 개요 및 배경
- **태스크 ID**: `OPT-SUBQUERY-HASHSET-MATERIALIZATION-1`
- **목적**: `WHERE col IN (SELECT ...)` 및 `WHERE col NOT IN (SELECT ...)` 비상관 서브쿼리 실행 시, 기존의 $O(N \times M)$ 문자열 직렬화 및 선형 풀스캔 병목을 제거하고, **1회 In-Memory HashSet 구체화 + $O(1)$ 상수 시간 조회**로 전환하여 서브쿼리 실행 속도를 수백~수천 배 가속.
- **배경 및 현행 병목**:
  - 현재 `sqlexec_explain.go:850-920`의 `resolveSubqueriesWithSession`은 비상관 서브쿼리를 실행한 후, 결과 행들을 `col IN ('val1', 'val2', ...)` 형태의 거대한 문자열로 직렬화하여 SQL 쿼리 텍스트에 다시 인라인 삽입함.
  - 이로 인해:
    1. 수천~수만 건의 문자열 결합 및 렉서/파서 재파싱 오버헤드 발생.
    2. `sqlexec_eval.go:210-230`에서 외부 테이블의 매 행마다 $M$개의 문자열과 순차 선형 비교(`strings.EqualFold`)를 수행 ($N \times M$ 연산, 예: $10,000 \times 5,000 = 50,000,000$회 비교!).
    3. `NOT IN` 평가 시 타입 불일치 및 3-Valued Logic(3VL) 처리 불안정.

---

## 2. 3대 핵심 아키텍처 불변식

### 불변식 1: 10,000건 Bounded Resource Cap (`[RULE:BoundedBuffers]`)
- 서브쿼리 구체화 대상 행 수가 **최대 10,000건**으로 엄격히 제한됨 (`MaxSubqueryHashSetCapacity = 10000`).
- 서브쿼리 결과가 10,000건을 초과할 경우:
  - 메모리 고갈(OOM) 방지를 위해 해시셋 생성을 즉시 차단하고, CBO 플래너의 일반 조인 경로로 이관하거나 명시적 용량 한도 에러(`ErrSubqueryExceedsCapacity`)를 반환하여 엔진 안정성을 영구 수호.

### 불변식 2: `hasNull` 플래그를 통한 SQL-92 3-Valued Logic 완전 보장
- SQL-92 표준 3VL에 따라, 서브쿼리 결과 집합에 `NULL`이 단 하나라도 포함되어 있으면:
  - `x NOT IN (1, 2, NULL)`은 어떤 $x$에 대해서도 절대 `TRUE`가 될 수 없음 (일치 시 `FALSE`, 미일치 시 `UNKNOWN` $\rightarrow$ `WHERE` 절에서 무조건 탈락).
- 해시셋에 `HasNull bool` 플래그를 보관하여:
  - `IN`: 값 일치 시 즉시 `TRUE`, 미일치 시 `HasNull` 여부에 따라 `UNKNOWN/FALSE`.
  - `NOT IN`: 값 일치 시 즉시 `FALSE`, 미일치 시 `HasNull`이면 즉시 `UNKNOWN` (행 탈락!), `HasNull == false`일 때만 비로소 `TRUE` 반환.

### 불변식 3: 정규화 키 매핑 (Canonical Scalar Normalization)
- Go 언어의 `any`(`interface{}`) 동등성 비교 규칙(`int(1) != int64(1)`) 및 `[]byte` 맵 키 패닉을 원천 차단하기 위해, 모든 스칼라 값을 3대 표준형으로 정규화:
  1. **정수형 (`int`, `int8`, `int16`, `int32`, `int64`, `uint*`)** $\rightarrow$ `int64`
  2. **부동소수점 (`float32`, `float64`)** $\rightarrow$ `float64`
  3. **문자열/바이트 슬라이스 (`string`, `[]byte`)** $\rightarrow$ `string`
  4. **불리언 (`bool`)** $\rightarrow$ `bool`
  5. **`nil`** $\rightarrow$ `HasNull = true` 플래그 설정 및 값 셋 제외

---

## 3. 세부 설계 및 자료구조

### 3.1 `SubqueryHashSet` 구조체
```go
package engine

// SubqueryHashSet represents an in-memory materialized result of an uncorrelated subquery.
type SubqueryHashSet struct {
	Values  map[any]struct{} // Set of non-null normalized scalar values
	HasNull bool             // True if the subquery returned at least one NULL value
	Count   int              // Total count of processed rows
}

// NewSubqueryHashSet initializes a bounded subquery hash set with pre-allocated capacity.
func NewSubqueryHashSet(capacity int) *SubqueryHashSet {
	if capacity <= 0 {
		capacity = 16
	}
	if capacity > MaxSubqueryHashSetCapacity {
		capacity = MaxSubqueryHashSetCapacity
	}
	return &SubqueryHashSet{
		Values: make(map[any]struct{}, capacity),
	}
}
```

### 3.2 `NormalizeSubqueryKey` 함수
```go
// NormalizeSubqueryKey converts any runtime value into its canonical hashable type:
// int64, float64, string, bool, or marks isNull = true.
func NormalizeSubqueryKey(val any) (any, bool) {
	if val == nil {
		return nil, true
	}
	switch v := val.(type) {
	case int:
		return int64(v), false
	case int64:
		return v, false
	case int32:
		return int64(v), false
	case int16:
		return int64(v), false
	case int8:
		return int64(v), false
	case uint:
		return int64(v), false
	case uint64:
		return int64(v), false
	case uint32:
		return int64(v), false
	case uint16:
		return int64(v), false
	case uint8:
		return int64(v), false
	case float64:
		return v, false
	case float32:
		return float64(v), false
	case string:
		return v, false
	case []byte:
		return string(v), false
	case bool:
		return v, false
	default:
		return fmt.Sprintf("%v", v), false
	}
}
```

### 3.3 쿼리 실행 컨텍스트 연동 (`QuerySubqueryContext`)
1. **서브쿼리 1회 실행 및 해시셋 생성**:
   - `resolveSubqueriesWithSession` 단계에서 비상관 `IN (SELECT ...)` 발견 시:
   - `execSubquery(innerSQL)`을 1회 호출하여 결과 행들을 `SubqueryHashSet`으로 빌드.
   - 만약 `len(innerRes.Rows) > 10000`이면 Bounded Cap 규칙에 따라 안전하게 처리.
2. **쿼리 스코프 토큰 발급 (`__subq_hashset_<id>__`)**:
   - 거대한 문자열 `(val1, val2, ...)` 대신 고유 심볼 `__subq_hashset_<id>__`를 `WHERE` 조건 문자열에 삽입:
     `col IN (__subq_hashset_1__)`
   - `cs.subqueryHashSets[token] = hashSet`에 보관.
3. **`evalPredicate`의 $O(1)$ 초고속 평가**:
   - 행 평가 시 `valList`가 `__subq_hashset_*__` 토큰이면:
     1. `rowVal, ok := row.Get(colName)`
     2. `normKey, isNull := NormalizeSubqueryKey(rowVal)`
     3. 해시셋 `hashSet.Evaluate(normKey, isNull, isNot)`를 호출하여 $O(1)$로 즉시 참/거짓 판정!

---

## 4. 검증 계획 (3-Stage Verification Pipeline)

1. **단위 및 기능 회귀 테스트 (`pkg/engine/subquery_hashset_test.go`)**:
   - **기본 IN 룩업**: 5,000건의 비상관 서브쿼리 결과에 대해 $O(1)$ 정상 필터링 검증.
   - **`NOT IN` + `NULL` 3VL 검증**: 서브쿼리에 `NULL`이 포함된 경우 `NOT IN` 결과가 공집합(0건)으로 정확히 탈락하는지 검증.
   - **정규화 타입 정합성**: `int32` 컬럼 vs `int64` 서브쿼리, `[]byte` 컬럼 vs `string` 서브쿼리가 패닉 없이 정확히 일치하는지 검증.
   - **10,000건 Bounded Cap 방어선 검증**: 10,001건 생성 시 한도 제한 가드가 작동하는지 검증.
2. **동시성 및 0 Data Race 검증**:
   - `go test -v -race ./pkg/engine -run "TestSubqueryHashSet_"`
3. **컴파일 및 빌드 검증**:
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
4. **커밋 및 원격 동기화**:
   - Conventional Commits (`perf(engine): optimize uncorrelated IN subqueries via in-memory hashset with 10k cap and 3VL hasNull support`)
