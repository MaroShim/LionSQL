# CBO Planner & VDBE Zero-Regex Finalization Plan (`CBO_PLANNER_ZERO_REGEX_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **대상 과제**: `GA-REGEX-FINAL-2` (CBO Planner 및 VDBE 잔여 정규식 100% 완전 제거)
> - **핵심 불변식**:
>   - `[RULE:Invariant 7]` (Zero-Regex SQL Parsing in Production Paths)
>   - `[RULE:ModularAbstraction]` (pkg/sqlutil 토크나이저 및 MatchLike 표준 라이브러리 사용)
>   - `[RULE:ZeroAllocation]` (쿼리 실행마다 정규식 MustCompile 컴파일 오버헤드 0화)
>   - `[RULE:PipelineVerification]` (Build ➔ Test ➔ Commit 3단계 검증)

---

## 1. 개요 및 배경

`GA-REGEX-FINAL-1`을 통해 엔진 진입로와 가상 카탈로그의 정규식은 제거되었으나, Volcano CBO 플래너(`pkg/planner/planner.go`)와 프로시저 VDBE 가상머신(`pkg/proc/runtime/vm.go`) 내부에 4개의 정규식이 여전히 잔존하고 있었습니다.

본 과제를 통해 프로덕션 쿼리 실행 경로에 남은 **모든 정규식을 100% 제거**하고 결정론적 상태머신 및 `pkg/sqlutil` 표준 모듈로 대체합니다:

1. **`planner.go:438` (`selectRe`)**:
   - `SELECT ... FROM ... WHERE ... GROUP BY ... ORDER BY ... LIMIT ...` 9개 그룹 거대 정규식.
   - `splitSelectClauses` 실패 시 폴백으로 동작하나, 복잡한 서브쿼리나 괄호가 포함되면 탐욕 매칭으로 쿼리 본문을 훼손할 위험.
   - ➔ `pkg/sqlutil`의 `SplitTopLevelTokens`, `FindTopLevelKeyword` 기반 결정론적 절(Clause) 파서로 완전 대체.
2. **`planner.go:826` (`joinPattern`)**:
   - `(?is)(?:(INNER|LEFT|RIGHT|FULL)\s+)?JOIN ... ON ... = ...` 복잡 정규식.
   - 괄호나 서브쿼리가 포함된 조인 구문에서 매칭 실패 및 오분할 위험.
   - ➔ 토큰 스트림 기반 `parseJoinClausesDeterministic` 렉서로 완전 대체.
3. **`vm.go:785` (`execRelQuery` 바인딩 치환)**:
   - 프로시저 VM 프레임에서 관계형 쿼리 실행 시마다 `regexp.MustCompile`로 파라미터를 치환하는 고비용 GC 오버헤드.
   - ➔ 단어 경계(`\b`)를 지원하는 결정론적 토큰 치환 헬퍼로 대체.
4. **`vm.go:2509` (`likeMatch`)**:
   - LIKE 연산 시마다 정규식을 빌드하고 컴파일하는 레거시 구현.
   - ➔ 이미 `pkg/sqlutil/like.go`에 구현되어 있는 무정규식 고속 `sqlutil.MatchLike(pattern, str, true)`로 100% 통합.

---

## 2. 작업 단계

### Phase 1: `pkg/proc/runtime/vm.go` 정규식 2건 제거
- `vm.go:2509` `likeMatch`: `sqlutil.MatchLike` 호출로 교체.
- `vm.go:785` `bindVariables`: 리터럴/주석을 보존하는 결정론적 파라미터 치환기로 교체.

### Phase 2: `pkg/planner/planner.go` 정규식 2건 제거
- `planner.go:438` `selectRe`: `splitSelectClauses`를 정교화하여 정규식 폴백 블록을 영구 삭제하고 명시적 구문 에러 전파.
- `planner.go:826` `joinPattern`: `sqlutil.FindAllTopLevelKeyword(fromClause, "JOIN")` 기반의 결정론적 조인 파서로 교체.

### Phase 3: 회귀 검증 및 테스트
- `go test -tags wiredtiger -v ./pkg/planner ./pkg/proc/...`
- TPC-C 및 복합 JOIN/SELECT 쿼리 회귀 0건 확인.

---

## 3. 검증 기준
1. `grep -rn "regexp\." pkg/planner/planner.go pkg/proc/runtime/vm.go` 결과 **0건 (완전 소멸)**
2. 3단계 파이프라인(Build ➔ Test ➔ Commit) 100% 통과
