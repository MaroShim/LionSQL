# LionSQL 플래너 슬림화 및 순수 AST 전환 설계 계획서 (`notes/PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01  
> - **상태**: 완료 (Completed) — 1.1 및 1.2 구현 및 전수 회귀 테스트 통과  
> - **과제 식별 코드**: `PLANNER-SLIM-1` (1.1: Sort-Merge Join Lazy) & `PLANNER-AST-1` (1.2: 순수 AST 기본화 및 정규식 옵션 격리)  
> - **목표**: 1.0 정식 릴리즈 대비 플래너 핫패스 불필요 연산 완전 제거 및 레거시 정규식 의존성 영구 폐기를 위한 단계적 옵션화  

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
