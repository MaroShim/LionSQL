# Session Temporary & Ephemeral Objects Lifecycle SSOT Plan (`SESSION_OBJECTS_LIFECYCLE_SSOT_PLAN.ko.md`)

## 1. 개요 및 배경 (Context & Goals)
- **대상**: `ClientSession`의 세션 스코프 임시/휘발성 객체(`TableVars`, `TempTableMap`, `EphemeralTables`, `PreparedStmts`, `Cursors`) 및 `Coordinator`의 라이프사이클 정리 루틴(`CloseSession`, `ResetSession`).
- **목표**:
  - `CloseSession`과 `ResetSession` 간 중복되어 존재하는 임시 객체 수거 및 물리 테이블 드롭 목록 추출 로직을 `ClientSession`의 단일 메서드(`DrainStorageTablesToDrop() []string`)로 캡슐화.
  - 세션 스코프 물리 테이블(`TableVars`, `TempTableMap`) 및 메모리 임시 객체(`EphemeralTables`, `PreparedStmts`, `Cursors`)의 수거 및 초기화 책임을 세션 객체 본체로 이전하여 **단일 진실 원천(SSOT)** 및 응집도 극대화.
  - 향후 세션 임시 객체 유형이 추가되더라도 `Coordinator` 코드 수정 없이 세션 레벨에서 자동으로 일괄 정리되는 Open-Closed 구조 확립.
- **핵심 원칙**:
  - `[RULE:RadicalKISS]`: 중복된 맵 순회 및 nil 할당 코드 전면 제거 및 단일 헬퍼로 통합.
  - `[RULE:ModularAbstraction]`: 세션 내부 상태 조작 로직을 캡슐화하고 외부 호출부는 단순한 인터페이스만 소비.
  - `[RULE:LockHierarchy]`: `cs.Mu.Lock()` 내부에서는 이름 스냅샷 및 맵 클리어만 수행하고, 실제 스토리지 엔진 물리 테이블 제거(`c.engine.DropTable`)는 락 해제 후 안전하게 수행.
  - `[RULE:PipelineVerification]`: 3단계 검증 파이프라인 (Build ➔ Test ➔ Commit) 철저 준수.

---

## 2. 현행 구조 분석 및 문제점 (Current Defect Architecture)

### 2.1 중복된 임시 객체 수집 및 클리어 로직
`pkg/engine/coordinator_impl.go`의 `CloseSession`과 `ResetSession`에 완전히 동일한 블록이 중복 존재:
```go
// CloseSession (라인 616-631) 및 ResetSession (라인 706-721):
var toDropLocal []string
for _, physTable := range cs.TableVars {
    toDropLocal = append(toDropLocal, physTable)
}
for _, phys := range cs.TempTableMap {
    toDropLocal = append(toDropLocal, phys)
}

cs.TableVars = nil
cs.TempTableMap = nil
cs.EphemeralTables = nil
cs.PreparedStmts = nil
if cs.Cursors != nil {
    clear(cs.Cursors)
}
```
1. **DRY 위반 및 변경 누락 위험 (`[RULE:ModularAbstraction]`)**:
   - 세션에 새로운 임시 리소스(예: 임시 시퀀스, 세션 캐시 등)가 추가될 때 `CloseSession`과 `ResetSession` 양쪽에 일일이 수동 추가해야 하며, 한쪽 누락 시 리소스 누수 발생.
2. **테이블 변수(`TableVars`) 캡슐화 부재**:
   - `TempTableMap`은 `RegisterTempTable`, `GetTempTablePhysical` 등의 메서드를 통해 접근되나, `TableVars`는 외부(`sqlexec.go`, `coordinator_impl.go`)에서 `cs.TableVars` 맵을 직접 조작하고 있음.

---

## 3. 목표 아키텍처 설계 (Target SSOT Architecture)

```
[ ClientSession ]
   ├── TableVars map[string]string
   ├── TempTableMap map[string]string
   ├── EphemeralTables map[string]*EphemeralTable
   ├── PreparedStmts map[string]*PreparedStatement
   └── Cursors map[string]*SessionCursor
         │
         ▼
   func (cs *ClientSession) DrainStorageTablesToDrop() []string
   - cs.Mu.Lock() 보호 하에:
     1. TableVars와 TempTableMap의 물리 테이블 이름을 중복 없이 수집
     2. TableVars, TempTableMap, EphemeralTables, PreparedStmts = nil 초기화
     3. Cursors 맵 clear
     4. 수집된 물리 테이블 목록([]string) 반환
         │
         ▼
[ Coordinator ] (CloseSession / ResetSession)
   toDropLocal := cs.DrainStorageTablesToDrop()
   for _, phys := range toDropLocal {
       _ = c.engine.DropTable(phys)
   }
```

### 3.1 세부 메서드 설계
1. `func (cs *ClientSession) DrainStorageTablesToDrop() []string`:
   - `cs.Mu.Lock()`을 획득하고 다음 작업을 원자적으로 수행:
     - `seen := make(map[string]struct{})`을 통해 `TableVars`와 `TempTableMap`의 모든 물리 테이블명을 중복 없이 `toDrop` 슬라이스에 추가.
     - `cs.TableVars = nil`
     - `cs.TempTableMap = nil`
     - `cs.EphemeralTables = nil`
     - `cs.PreparedStmts = nil`
     - `if cs.Cursors != nil { clear(cs.Cursors) }`
     - `return toDrop`
   - 주의: `CloseSession`에서 이미 락을 잡고 있는 경우와 `ResetSession`의 경우를 고려하여, 락 획득 상태(`drainStorageTablesToDropLocked()`)와 공개 메서드(`DrainStorageTablesToDrop()`)를 명확히 분리.
2. `TableVars` 접근 캡슐화:
   - `func (cs *ClientSession) RegisterTableVar(name, phys string)`
   - `func (cs *ClientSession) GetTableVarPhysical(name string) (string, bool)`

---

## 4. 단계별 실행 계획 (Step-by-Step Execution Plan)

### Step 1: `ClientSession` 수거 메서드 구현 (`pkg/engine/session.go`)
- `drainStorageTablesToDropLocked() []string` 내부 헬퍼 작성.
- `DrainStorageTablesToDrop() []string` 공개 메서드 작성 (외부 호출용).
- `RegisterTableVar`, `GetTableVarPhysical` 헬퍼 메서드 추가.

### Step 2: `Coordinator` 리팩토링 (`pkg/engine/coordinator_impl.go`)
- `CloseSession`: 중복 루프 및 nil 할당을 `cs.drainStorageTablesToDropLocked()` 호출로 단일화.
- `ResetSession`: 중복 루프 및 nil 할당을 `cs.drainStorageTablesToDropLocked()` 호출로 단일화.

### Step 3: `sqlexec.go`의 `TableVars` 조작부 리팩토링
- 직접 맵 할당(`cs.TableVars[varName] = physTable`)을 `cs.RegisterTableVar(varName, physTable)`로 변경.

### Step 4: 3단계 검증 파이프라인 수행
- 1. Build: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
- 2. Test: `go test -v -race ./pkg/engine/...`
- 3. Commit: Conventional Commits 형식 준수
