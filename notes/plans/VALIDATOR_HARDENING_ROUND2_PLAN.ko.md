# Semantic Validator Gatekeeper Hardening Round 2 Plan (`notes/plans/VALIDATOR_HARDENING_ROUND2_PLAN.ko.md`)

## 1. 개요 및 목적
LionSQL의 SQL 쿼리 파이프라인에서 **Validator (`pkg/planner/validator.go`)**는 실행기(Volcano CBO / VDBE)로 진입하기 전 비정상적인 시맨틱 쿼리를 컴파일 타임에 원천 차단하는 최전선 방어선(Gatekeeper)입니다.
본 계획은 1차 게이트키퍼 강화에 이어, ANSI SQL / PostgreSQL / SQL Server 표준에 부합하는 **4대 핵심 시맨틱 방어 규칙 (Round 2)**을 전면 구축하여 런타임 패닉, 결과 왜곡, 잘못된 서브쿼리 실행을 방지합니다.

---

## 2. 4대 추가 방어 규칙 및 불변식

### Invariant 6: HAVING 절 비집계 컬럼의 GROUP BY 불일치 차단 (SQLSTATE 42803)
- **규칙**:
  `GROUP BY`가 존재하는 쿼리의 `HAVING` 절에 명시된 컬럼 참조는 반드시 `GROUP BY` 목록에 포함되거나, 집계 함수(`SUM`, `COUNT` 등)의 인자여야 함.
  `GROUP BY`가 없는 쿼리의 `HAVING` 절에 명시된 컬럼 참조는 반드시 집계 함수의 인자여야 함.
- **에러 규격**:
  `column "%s" must appear in the GROUP BY clause or be used in an aggregate function (SQLSTATE 42803)`

### Invariant 7: 상수식 0으로 나누기 및 나머지 연산 컴파일 타임 차단 (SQLSTATE 22012)
- **규칙**:
  `BinaryExpr`에서 연산자가 `/` 또는 `%`이고 우측 피연산자가 상수 리터럴 `0` 또는 `0.0`인 경우 즉시 거부.
- **에러 규격**:
  `division by zero (SQLSTATE 22012)`

### Invariant 8: 스칼라 서브쿼리 와일드카드(*) 다중 컬럼 arity 검출 (SQLSTATE 42601)
- **규칙**:
  스칼라/비교 서브쿼리에서 프로젝션이 단일 `*` 또는 `tbl.*`인 경우, `schemas` 메타데이터를 조회하여 대상 테이블의 컬럼 수가 1개를 초과하면 arity mismatch로 즉각 거부.
- **에러 규격**:
  `subquery must return only one column, returned %d (SQLSTATE 42601)`

### Invariant 9: LIKE ... ESCAPE 단일 문자 제약 검증 (SQLSTATE 22019 / 2200C)
- **규칙**:
  `LikeExpr`의 `Escape` 절이 문자열 리터럴일 때, 길이가 정확히 1글자가 아니면 컴파일 타임에 거부.
- **에러 규격**:
  - 0글자: `invalid escape string: escape character must be a single character (SQLSTATE 2200C)`
  - 2글자 이상: `invalid escape string: escape character must be a single character (SQLSTATE 22019)`

---

## 3. 검증 계획
1. `pkg/planner/binder_test.go`에 4대 불변식에 대한 TDD Red 테스트 케이스 추가 및 실패 확인.
2. `pkg/planner/validator.go`에 4대 검증기 구현 및 Green 검증.
3. `go build` 및 `go test -race` 3단계 파이프라인 검증 통과 후 커밋.
