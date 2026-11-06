# Runtime LIKE Zero-Regex Elimination Plan (`RUNTIME_LIKE_ZERO_REGEX_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **대상 과제**: `GA-REGEX-FINAL-3` (쿼리 실행기 런타임 LIKE 정규식 100% 완전 제거)
> - **대상 파일**:
>   - `pkg/execution/filter.go` (Volcano Seq Scan 필터 노드)
>   - `pkg/proc/eval.go` (VDBE 표현식 평가기)
>   - `pkg/catalog/virtual/result.go` (가상 카탈로그 결과 필터기)
> - **핵심 불변식**:
>   - `[RULE:ZeroAllocation]` (행마다 정규식 MustCompile/MatchString 제거, 고속 선형 탐색화)
>   - `[RULE:ModularAbstraction]` (`pkg/sqlutil.MatchLike` 표준화)
>   - `[RULE:PipelineVerification]` (Build ➔ Test ➔ Commit 3단계 검증)

---

## 1. 개요 및 목적
앞선 과제에서 SQL 문법 파싱 경로의 정규식은 모두 제거되었으나, 쿼리 실행 런타임에서 각 데이터 행(Row)을 순회하며 `LIKE` / `NOT LIKE`를 비교할 때 매 행마다 `regexp.MatchString` 또는 `regexp.Compile`을 호출하는 비효율이 남아 있었습니다.

이를 `pkg/sqlutil.MatchLike` (정규식 없는 선형 매칭 알고리즘)로 전면 교체하여 GC 부하를 0으로 만들고 속도를 개선합니다. (POSIX 정규식 연산자인 `~`, `!~`는 의도된 기능이므로 유지)

---

## 2. 작업 단계
1. **`pkg/execution/filter.go:328-333`**:
   - `pattern := strings.ReplaceAll(regexp.QuoteMeta(rightVal), "%", ".*")` 제거
   - `return sqlutil.MatchLike(rightVal, leftStr, true)` 로 교체
   - `execution/filter.go`에서 `"regexp"` import 제거
2. **`pkg/proc/eval.go:340-360`**:
   - `pat = strings.ReplaceAll(regexp.QuoteMeta(pat)...` 제거
   - `matched := sqlutil.MatchLike(pat, fmt.Sprintf("%v", val), true)` 로 교체
   - `proc/eval.go`에서 `"regexp"` import 제거
3. **`pkg/catalog/virtual/result.go:576-592`**:
   - LIKE, ILIKE, NOT LIKE, NOT ILIKE 매칭부를 `sqlutil.MatchLike` 로 교체
   - POSIX 정규식 연산자 `~`, `~*` 등은 안전하게 보존

---

## 3. 검증 기준
1. `go build -tags wiredtiger -buildvcs=false ./...` 100% PASS
2. `go test -tags wiredtiger -count=1 -v ./pkg/execution ./pkg/proc ./pkg/catalog/virtual` 100% PASS
3. `pkg/execution/filter.go` 및 `pkg/proc/eval.go`에서 `regexp` import 완전 소멸
