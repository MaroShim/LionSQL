# Bound AST & O(1) 정수 슬롯 바인딩 구현 계획서 (`notes/BOUND_AST_AND_INTEGER_SLOT_BINDING_PLAN.ko.md`)

> **문서 상태**: Approved  
> **관련 마일스톤**: `v0.96.0`  
> **관련 태스크**: `BINDER-EVOL-PHASE-5`  
> **불변식 원칙**: `[RULE:PlanPersistence]`, `[RULE:PipelineVerification]`, `[RULE:RadicalKISS]`, `[RULE:ModularAbstraction]`, `[RULE:ZeroAllocation]`

---

## 1. 배경 및 문제 정의

현재 LionSQL의 쿼리 실행 파이프라인에서 컬럼 참조(`proc.ColumnRefExpr`)는 매 튜플 평가마다 다음 오버헤드를 발생시킵니다:
1. `EvalTupleExpr`에서 `col.Name` 문자열 획득
2. 대소문자 변환(`strings.ToLower`), 트림(`strings.Trim`), 닷(`.`) 인덱스 분할 등 문자열 조작
3. `ColumnIndexMap.Index(name)` 해시 맵 조회
4. 매 행/매 컬럼마다 수십 나노초의 런타임 룩업 오버헤드 누적

`BINDER-EVOL-PHASE-5`의 목표는 컴파일 타임에 카탈로그 스키마를 기반으로 컬럼의 물리적 오프셋(`ColumnIdx`), 소속 테이블(`TableID`), 정적 데이터 타입(`Type`)을 결정하여 `BoundColumnRef` 및 `BoundSelectStmt`에 영구 바인딩함으로써, 실행 엔진(Volcano / VDBE)이 런타임 문자열 룩업을 완전 제거하고 $O(1)$ 정수 슬롯 인덱싱으로 즉시 접근하도록 최적화하는 것입니다.

---

## 2. 핵심 아키텍처 설계

### 2.1 Bound AST 노드 정의 (`pkg/planner/bound_ast.go`)

```go
package planner

import (
    "lionsql/pkg/proc"
    "lionsql/pkg/storage"
)

// BoundExpr represents an expression node whose identifiers, types, and offsets have been statically resolved.
type BoundExpr interface {
    proc.Expr
    BoundType() storage.DataType
}

// BoundColumnRef represents a column reference bound to a specific table slot and column index.
type BoundColumnRef struct {
    Name       string           // Original column identifier (e.g. "age", "e.salary")
    TableName  string           // Resolved table name or alias
    TableID    int              // 0 = base relation (FROM), 1..N = joined relations
    ColumnIdx  int              // 0-based index in TableSchema.Columns (0 <= ColumnIdx < 1024)
    TupleSlot  int              // Flattened index in Volcano joined Tuple.Values (TableID offset + ColumnIdx)
    DataType   storage.DataType // Resolved storage data type
    OuterLevel int              // 0 = current scope, 1 = parent scope (correlated subquery)
}

func (b *BoundColumnRef) exprNode()                  {}
func (b *BoundColumnRef) String() string             { return b.Name }
func (b *BoundColumnRef) BoundType() storage.DataType { return b.DataType }
```

### 2.2 Dedicated Binder Pass (`pkg/planner/binder.go`)

`BindSelect(stmt *proc.GeneralSelectStmt, schemas map[string]*storage.TableSchema) (*BoundSelectStmt, error)`:
1. 기존 `TableScope`를 확장하여 테이블별 `TableID` 할당 (FROM = 0, JOINs = 1..N).
2. AST를 재귀 순회하며 모든 `ColumnRefExpr`을 `BoundColumnRef`로 치환:
   - 식별자 해석 (`FindColumn`)
   - `TableID`, `ColumnIdx`, `DataType` 확정
   - 결합 튜플 내 물리적 `TupleSlot` 계산
3. `BoundSelectStmt` 산출 및 `QueryInfo.BoundAST`에 탑재.

### 2.3 Volcano Tuple $O(1)$ 정수 슬롯 평가 직결 (`pkg/execution/eval.go`, `iterator.go`)

1. `Tuple.GetIdx(idx int) (any, bool)`:
   - `idx`가 범위 내에 있고 `t.Values[idx] != nil`이면 $O(1)$ 즉시 반환.
   - Raw 바이너리인 경우 `ExtractColumnFromTuple(t.Schema, t.Raw, idx)` 호출 (문자열 검색 0).
2. `EvalTupleExprWithContext`:
   - `case *planner.BoundColumnRef`:
     - `slot := e.TupleSlot`
     - `val, ok := t.GetIdx(slot)` ➔ $O(1)$ 즉시 반환!

---

## 3. 3단계 구현 로드맵

1. **Step 1: Bound AST 타입 정의 (`pkg/planner/bound_ast.go`)**:
   - `BoundExpr`, `BoundColumnRef`, `BoundLiteral`, `BoundBinaryExpr`, `BoundSelectStmt`
2. **Step 2: 바인더 패스 구현 및 Scope 바인딩 연계 (`pkg/planner/binder.go`, `validator.go`)**:
   - `BindExpr`, `BindSelect`, `ResolveBoundColumn`
   - `ValidateQueryAST` 검증과 바인딩의 원자적 통합
3. **Step 3: 실행기(Volcano / Eval) 직결 및 벤치마크/테스트 (`pkg/execution/eval.go`, `pkg/planner/binder_test.go`)**:
   - `Tuple.GetIdx` 구현
   - `EvalTupleExpr`에 `BoundColumnRef` fast-path 추가
   - 단위 테스트 및 TDD 검증 (단일 테이블, 멀티 조인, 모호성/에러 전파)
