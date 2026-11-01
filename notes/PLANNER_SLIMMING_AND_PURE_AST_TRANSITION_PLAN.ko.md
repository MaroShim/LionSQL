# LionSQL 플래너 슬림화 및 순수 AST 전환 설계 계획서 (`notes/PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01  
> - **상태**: 완료 (Completed) — Phase 1 (`PLANNER-SLIM-1`), Phase 2 (`PLANNER-AST-1`), Phase 3 (`PLANNER-AST-2`), Phase 4 (`ENGINE-AST-3`) 전량 구현 및 검증 완료  
> - **과제 식별 코드**: `PLANNER-SLIM-1` & `PLANNER-AST-1` & `PLANNER-AST-2` & `ENGINE-AST-3`  
> - **목표**: 1.0 정식 릴리즈 대비 플래너 핫패스 불필요 연산 제거 및 플래너·엔진 전반의 정규식(`regexp`) 의존성 완전 박멸 (100% Lexer/Pratt AST 전환)  

---

## 1. 배경 및 문제 정의

LionSQL은 SQLite 수준의 고속 인메모리 핫패스(sub-microsecond)와 완전한 T-SQL/PostgreSQL 호환성을 지향합니다.
하지만 과거 아키텍처 진화 과정에서 축적된 다음 두 가지 비효율이 1.0 릴리즈 게이트 통과를 위해 정리되어야 합니다:

1. **Sort-Merge Join 비용의 무조건적 상시 계산 (1.1)**:
   - 플래너의 `buildSingleJoinNode`는 매 조인마다 힌트(`OPTION (MERGE JOIN)`) 유무와 상관없이 수많은 문자열 슬라이싱(`strings.Index`, `cleanIdent`, `strings.ToLower`)과 부동소수점(`math.Log2`) 연산을 항상 수행합니다.
   - 하지만 인메모리 Hash Join이 $O(N + M)$인 반면 Merge Join은 정렬 비용($O(N \log N + M \log M)$)이 추가되어 양쪽이 이미 인덱스로 정렬되어 있지 않은 한 수학적으로 Hash Join을 절대 이길 수 없습니다.
   - 따라서 99.9%의 쿼리에서 쓸데없는 CPU 사이클만 낭비되고 있습니다.

2. **플래너 내 레거시 정규식 파서의 상주 (1.2)**:
   - `pkg/proc/parser.go`의 Pratt AST 파서가 완비되었음에도, 과거 땜질용으로 사용되던 거대한 정규식(`selectRe = regexp.MustCompile`) 및 문자열 분해 로직(`splitSelectClauses`, `maskSubqueries`)이 여전히 상주하고 있습니다.
   - 이는 쿼리 문자열 리터럴이나 주석 내에 키워드가 있을 때 오작동(False Positive)을 유발하고, 불필요한 GC 힙 할당을 발생시킵니다.
   - 1.0 프로덕션을 향한 안전한 마이그레이션을 위해, **기본 동작(Default)을 순수 AST 모드로 설정하고, 레거시 정규식 폴백은 CLI/설정 플래그(`--enable-legacy-regex-planner`)를 통해 명시적으로 활성화할 때만 동작**하도록 격리합니다.

---

## 2. 상세 기술 설계

### 2.1. 제1과제: Sort-Merge Join 비용 지연 평가 (`PLANNER-SLIM-1`)

#### 핵심 메커니즘
`pkg/planner/planner.go`의 `buildSingleJoinNode`에서 Merge Join 비용 계산을 지연(Lazy) 처리합니다:

```go
// 1. Merge Join 평가 필요 여부 사전 검사
needMergeEval := forceMergeJoin
if !needMergeEval {
    // 힌트가 없더라도 양쪽 릴레이션이 이미 조인 키로 물리적 정렬되어 있는 경우에만 CBO 평가 가치 존재
    if isOuterSorted && isInnerSorted {
        needMergeEval = true
    }
}

// 2. 불필요한 경우 최대 비용으로 즉시 단락 (수십 줄의 문자열/로그 연산 완전 바이패스)
mergeJoinTotalCost := math.MaxFloat64
if needMergeEval {
    // 실제 Sort-Merge Join 비용 계산 (outerSortCost, innerSortCost, mergeStreamingCost)
    ...
}
```

#### 기대 효과
- 일반 OLTP 단일/다중 조인 쿼리 플래닝 시 수십 줄의 불필요한 CPU 부동소수점 및 문자열 조작 오버헤드 0으로 감축.
- T-SQL `OPTION (MERGE JOIN)` 및 `INNER MERGE JOIN` 힌트 쿼리는 100% 기존 호환성 유지.

---

### 2.2. 제2과제: 순수 AST 플래너 기본화 및 정규식 옵션 격리 (`PLANNER-AST-1`)

#### 설정 및 플래그 아키텍처
1. **설정 모델 (`pkg/config/config.go`)**:
   - `EnableLegacyRegexPlanner bool` 설정 필드 추가 (기본값: `false`).
2. **CLI 옵션 (`cmd/lionsql/main.go`, `cmd/lionsql/flags.go`)**:
   - `--enable-legacy-regex-planner`: 기본 `false`. 구형 정규식 폴백이 반드시 필요한 특수 환경에서만 활성화 가능.
3. **플래너 런타임 제어 (`pkg/planner/planner.go`)**:
   - `Planner` 구조체에 `EnableLegacyRegexFallback bool` 필드 제공.
   - `ParseSelect`:
     - **Default (`EnableLegacyRegexFallback == false`)**:
       - Pratt AST 파서(`proc.ParseSQL`)로 파싱 후 `FromAST` 변환.
       - AST 파싱 실패 시, 위험한 정규식으로 땜질하지 않고 즉시 명확한 구문 분석 에러(Syntax Error)를 반환하여 예측 가능성과 정합성 보장.
     - **Opt-in (`EnableLegacyRegexFallback == true`)**:
       - AST 실패 시 기존 레거시 정규식/문자열 분해(`splitSelectClauses`, `selectRe`) 경로를 폴백으로 허용.

---

### 2.3. 제3과제: 플래너 하위 조건식·통계·서브쿼리 정규식 완전 제거 (`PLANNER-AST-2`)

#### 현황 및 문제점
`ParseSelect`는 이미 순수 AST(`proc.GeneralSelectStmt`)로 전환되었으나, 플래너 하위 분석 모듈들이 여전히 문자열로 역변환된 조건식(`.String()`)을 받아 정규식으로 다시 분해하고 있습니다:
1. **`pkg/planner/bounds.go`**: `eqBoundRe`, `gteBoundRe`, `gtBoundRe`, `lteBoundRe`, `ltBoundRe`, `betweenBoundRe` (총 6개 정규식)
2. **`pkg/planner/stats.go`**: `cboEqRe`, `cboIsNullRe` (총 2개 정규식)
3. **`pkg/planner/pruning.go`**: `condEqRe`, `condGteRe`, `condGtRe`, `condLteRe`, `condLtRe`, `condBetweenRe`, `condInRe` (총 7개 정규식)
4. **`pkg/planner/planner.go`**: `eqRe`, `onlyEqRe`, `withinRe` (총 4개 정규식)
5. **`pkg/planner/decorrelate.go` & `explain.go`**: `existsRe`, `inRe`, `eqPattern` (총 3개 정규식)

#### 개선 설계 (Pure AST / Lexer 전환)
- **단일 조건식 AST 파서 헬퍼 도입**:
  - `proc.NewParser(proc.NewLexer(cond)).ParseExpr()`를 통해 조건식을 즉시 `proc.BinaryExpr`, `proc.BetweenExpr`, `proc.InExpr`, `proc.IsNullExpr`, `proc.WithinGroupExpr`로 구조적 분석합니다.
  - `bounds.go`, `stats.go`, `pruning.go`, `planner.go`, `explain.go`에서 `regexp.MustCompile`을 **전면 제거**하고 AST 노드 타입 스위칭으로 100% 대체합니다.
- **서브쿼리 Decorrelation 렉서 전환 (`decorrelate.go`)**:
  - `existsRe`, `inRe` 정규식 대신 `proc.NewLexer` 토큰 스트림을 순회하여 `[NOT] EXISTS (` 및 `col [NOT] IN (` 패턴과 괄호 경계를 정확히 추출합니다 (문자열 리터럴/주석 내 오탐 원천 차단).

---

### 2.4. 제4과제: 엔진 관리·부가 명령어 정규식 제거 및 렉서/AST 전환 (`ENGINE-AST-3`)

#### 현황 및 문제점
`pkg/engine` 내부의 관리 및 확장 SQL 처리기들이 입구에서 정규식 그룹 캡처에 의존하고 있습니다:
1. **`pkg/engine/sqlexec.go`**: `reTblVarDecl`, `reKillCmd`, `reUseCmd`, `reSetConfig`, `reShowParts`, `reShowTables`, `reShowDatabases`, `reDescTable`, `reShowParam`, `reSelectSysVar`, `reTblVarCreate`, `reSetVar`, `reSelectVar` (총 14개 정규식)
2. **`pkg/engine/sqlexec_auth.go`**: `reCreateLogin`, `reAlterLogin`, `reDropLogin`, `reCreateUser`, `reDropUser`, `reGrant`, `reRevoke`, `reDeny`, `reSetUser`, `reExecAs` (총 10개 정규식)
3. **`pkg/engine/sqlexec_pivot.go`, `sqlexec_apply.go`, `sqlexec_string_split.go`**: `PIVOT`/`UNPIVOT`/`FOR JSON`/`APPLY`/`STRING_SPLIT` 정규식 (총 15개 정규식)

#### 개선 설계 (Lexer 토큰 스트림 파서 전환)
- `proc.NewLexer` 기반의 경량 토큰 소비 헬퍼를 도입하여 `SHOW`, `DESC`, `USE`, `KILL`, `SET`, `DECLARE @t TABLE`, `CREATE/ALTER/DROP LOGIN/USER`, `GRANT/REVOKE/DENY`, `PIVOT/UNPIVOT/FOR JSON`, `CROSS/OUTER APPLY`, `STRING_SPLIT` 구문을 정규식 없이 100% 렉서 토큰 단위로 파싱합니다.
- 이를 통해 `pkg/planner`(레거시 옵션 제외)와 `pkg/engine`에서 구문 파싱용 정규식(`regexp`)을 완전히 퇴출합니다.

---

## 3. 단계별 구현 및 검증 파이프라인

### Phase 1: Sort-Merge Join 비용 지연 평가 구현 (`PLANNER-SLIM-1`)
1. `pkg/planner/planner.go`의 `buildSingleJoinNode` 리팩토링.
2. 힌트 및 양쪽 정렬 조건이 없을 때 Merge 비용 계산 완전 생략 확인.
3. 기존 `cbo_join_test.go` 및 SLT 조인 스위트 전수 회귀 테스트.

### Phase 2: 순수 AST 기본화 및 정규식 옵션화 구현 (`PLANNER-AST-1`)
1. `pkg/config`에 `EnableLegacyRegexPlanner` 필드 및 기본값(`false`) 추가.
2. `cmd/lionsql` CLI 플래그 `--enable-legacy-regex-planner` 연동.
3. `pkg/planner/planner.go`에 `EnableLegacyRegexFallback` 주입 및 `ParseSelect` 분기 처리.
4. 단위 테스트 작성:
   - 디폴트 모드(순수 AST)에서 표준 쿼리 정상 동작 및 정규식 바이패스 검증.
   - 플래그 활성화 시 레거시 정규식 폴백 활성화 검증.

### Phase 3: 3단계 검증 파이프라인 (Build ➔ Test ➔ Commit)
1. **Build**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
   go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
   ```
2. **Test (`-race`)**:
   ```bash
   go test -tags wiredtiger -count=1 -race ./pkg/...
   ```
3. **Commit**:
   - `perf(planner): defer sort-merge join cost calculation to lazy evaluation (PLANNER-SLIM-1)`
   - `feat(planner): default to pure AST planner and isolate legacy regex fallback behind option (PLANNER-AST-1)`
