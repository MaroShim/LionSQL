# SQL Parser/Rewriter Hardening Plan (`SQL_PARSER_HARDENING_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **대상 과제**: `FIX-SQLUTIL-PARSER-1` (`pkg/sqlutil` 내 SQL 파서 및 재작성기 결함 보강)
> - **상위 목표**: GA-REGEX-FINAL-1 사후 품질 강화 및 안전한 SQL 정규화
> - **적용 불변식**:
>   - `[RULE:ModularAbstraction]` (Rule of Two: 따옴표/주석/괄호 상태머신 단일 렉서/스캐너 모듈화)
>   - `[RULE:SafeDefaults]` & `[RULE:ExplicitError]` (리터럴 파괴 방지 및 미지원 구문 무음 왜곡 방지)
>   - `[RULE:CharsetInvariance]` (UTF-8 ToUpper 바이트 오프셋 불일치 원천 차단)
>   - `[RULE:PipelineVerification]` (Build ➔ Test ➔ Commit 3단계 검증)

---

## 1. 개요 및 배경

`GA-REGEX-FINAL-1`을 통해 정규식을 제거하고 손수 구현한 `pkg/sqlutil`의 ad-hoc 파서/재작성기에서 발견된 9대 결함을 수정하고, 산재된 토크나이저 상태머신을 일원화합니다:

1. **문자열 리터럴/주석 파괴 방지**:
   - `StripMSSQLNolock`이 따옴표 내부 `WHERE n = 'a with (nolock) b'`를 침묵 변조하던 결함 수정.
2. **SQL 문법 보존**:
   - `SELECT TOP 5 a FROM t;` 변환 시 세미콜론(`;`)이 `LIMIT` 앞에 위치해 Syntax Error를 유발하던 문제 해결.
   - `TOP ... PERCENT`, `TOP ... WITH TIES` 등 미지원 구문을 섣불리 `LIMIT`으로 변환하지 않고 원본 보존(바이패스).
   - `TOP` 뒤 `UNION` 존재 시 전체 쿼리에 `LIMIT`이 걸리는 스코프 오염 방지.
3. **`ANY`, `IN`, `IS NULL`, `BETWEEN` 述語 파서 결함 수정**:
   - `>= ANY` 등 다중 바이트 연산자 정상 분리.
   - `ANY(ARRAY['a','b'])`에서 쉼표 분리 전 따옴표 일괄 Trim으로 인한 원소 뭉개짐 해결.
   - `price = 1.5` 실수 및 `id = -5` 음수 추출 누락 해결.
4. **UTF-8 ToUpper 바이트 오프셋 불일치 해결**:
   - `strings.ToUpper` 결과 인덱스를 원본 바이트 인덱스로 직접 대입하여 다국어/특수문자가 깨지던 문제 제거.

---

## 2. 세부 구현 단계

### Phase 1: `pkg/sqlutil` 공통 안전 스캐너/토크나이저 구축
- 따옴표(`'`, `"`, `[]`), 주석(`--`, `/* */`), 괄호 깊이(`()`)를 안전하게 추적하는 공통 스캐너 함수 구현.
- ASCII 대소문자 무시 매칭을 UTF-8 길이 왜곡 없이 수행하는 토큰 검색기 제공.

### Phase 2: `pkg/sqlutil/pagination.go` 결함 수정
- `StripMSSQLNolock`: 공통 스캐너를 통해 리터럴/주석 밖의 `WITH (NOLOCK)`만 정확히 제거. CTE `WITH` 구문과의 충돌 방지.
- `NormalizeMSSQLPagination`:
  - 끝 세미콜론(`;`) 보존 처리 (`TrimRight(";")` 후 변환 뒤 세미콜론 복원).
  - `PERCENT`, `WITH TIES` 감지 시 임의 변환 금지 및 원본 보존.
  - `UNION` 구문 감지 시 단순 후미 부착 금지 및 서브쿼리 격리 또는 안전 바이패스.

### Phase 3: `pkg/sqlutil/predicate.go` 결함 수정
- `ParseAnyPredicate`: `>= ANY`, `<= ANY`, `<> ANY` 다중 바이트 연산자 정확 매핑. `ARRAY[...]` 내부 원소 파싱 로직 정교화.
- `ExtractEqualityFilter`: 실수(`.`), 음수(`-`) 리터럴 지원.
- `ParseIsNullPredicate`: 줄바꿈(`\n`), 다중 공백 관용 처리 및 `AND` 복합절 오인 방지.

### Phase 4: 정밀 회귀 테스트 스위트 (`predicate_test.go`, `pagination_test.go`)
- 지적받은 14대 probe 케이스를 단위 테스트로 영구 자산화.

---

## 3. 검증 기준
1. `go build -tags wiredtiger -buildvcs=false ./...` 컴파일 100% 통과
2. `go test -tags wiredtiger -count=1 -v -race ./pkg/sqlutil` 100% 통과
3. `go test -tags wiredtiger -count=1 -v ./...` 전체 테스트 회귀 0건
