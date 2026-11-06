# Runtime LIKE Zero-Regex Elimination Plan (`RUNTIME_LIKE_ZERO_REGEX_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **상태**: [x] Completed (완료)
> - **대상 과제**: `GA-REGEX-FINAL-3` (쿼리 실행기 런타임 LIKE 정규식 100% 완전 제거)
> - **대상 파일**:
>   - `pkg/execution/filter.go` (Volcano Seq Scan 필터 노드)
>   - `pkg/catalog/virtual/result.go` (가상 카탈로그 결과 필터기)
> - **핵심 불변식**:
>   - `[RULE:ZeroAllocation]` (행마다 정규식 MustCompile/MatchString 제거, 고속 선형 탐색화)
>   - `[RULE:ModularAbstraction]` (`pkg/sqlutil.MatchLike` 표준화)
>   - `[RULE:PipelineVerification]` (Build ➔ Test ➔ Commit 3단계 검증)

---

## 1. 개요 및 목적
앞선 과제에서 SQL 문법 파싱 경로의 정규식은 모두 제거되었으나, Volcano 실행 엔진의 쿼리 실행 런타임에서 각 데이터 행(Row)을 순회하며 `LIKE` / `NOT LIKE`를 비교할 때 매 행마다 `regexp.MatchString` 또는 `regexp.Compile`을 호출하는 비효율이 남아 있었습니다.

이를 `pkg/sqlutil.MatchLike` (정규식 없는 선형 매칭 알고리즘)로 전면 교체하여 핫패스 GC 부하를 0으로 만들고 속도를 개선합니다. 가상 카탈로그(`pkg/catalog/virtual/result.go`)는 비-핫패스 메타데이터 조회이므로 기존 RE 매칭 방식을 유지하되, XORM 및 T-SQL 대괄호 이스케이프(`[_]`, `[%]`) 누락 결함을 보정하여 정합성을 수호합니다.

---

## 2. 작업 완료 내역
1. **`pkg/execution/filter.go:328-333`**:
   - `pattern := strings.ReplaceAll(regexp.QuoteMeta(rightVal), "%", ".*")` 제거
   - `return sqlutil.MatchLike(rightVal, leftStr, true)` 로 교체
   - `execution/filter.go`에서 `"regexp"` import 완전 제거 (핫패스 0-할당 달성)
2. **`pkg/catalog/virtual/result.go:576-592`**:
   - T-SQL 와일드카드 이스케이프(`[_]`, `[%]`)가 `regexp.QuoteMeta`에 의해 `\[_\]`로 오변환되던 결함 수정
   - `\x00LITERALUNDERSCORE\x00` 사전 치환을 통해 `SELECT * FROM sys.fn_helpcollations() WHERE name LIKE '%[_]CS[_]AS%'` 등 XORM 메타데이터 필터링 100% 정상 매칭
   - POSIX 정규식 연산자 `~`, `~*` 등은 표준 기능으로 안전하게 보존
3. **`pkg/sqlutil/predicate.go`**:
   - 대괄호 내부 상태머신 보강 (`!inBracket`)

---

## 3. 검증 결과
1. `go build -tags wiredtiger -buildvcs=false ./...` 100% PASS
2. `go test -tags wiredtiger -count=1 -race ./pkg/execution ./pkg/catalog/virtual ./pkg/sqlutil ./pkg/proc/...` 100% PASS (0 data races)
3. `TestXORMMSSQL_GetColumnsExact_AndCollation` 및 `TestXORM_CollationAndIntrospectionE2E` 100% PASS
