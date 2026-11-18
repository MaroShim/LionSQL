# Session Temporary Tables SSOT Refactoring Plan (`TEMPORARY_TABLES_SSOT_PLAN.ko.md`)

## 1. 개요 및 배경 (Context & Goals)
- **대상**: `ClientSession`의 임시 테이블 추적 이중 구조 (`TempTables []string` 슬라이스 및 `TempTableMap map[string]string`)
- **목표**:
  - `TempTables []string` 슬라이스를 완전히 제거
  - `TempTableMap`의 중복 키(`#foo`와 `foo` 2중 저장)를 표준화된 정규화 키(`cleanKey`) 단일 엔트리로 단일화
  - 등록, 조회, 삭제를 $O(1)$ 연산으로 일원화하여 **단일 진실 원천(SSOT)** 확립
- **핵심 원칙**:
  - `[RULE:RadicalKISS]`: 불필요한 슬라이스 선형 탐색 및 이중 상태 갱신 코드 완전 제거
  - `[RULE:ModularAbstraction]`: 세션 임시 테이블 등록/해제/물리이름 조회를 캡슐화된 메서드로 제공
  - `[RULE:PipelineVerification]`: 3단계 검증 파이프라인 (Build ➔ Test ➔ Commit)

---

## 2. 현행 구조 분석 및 문제점 (Current Defect Architecture)

### 2.1 이중 상태 및 $O(N)$ 슬라이스 탐색
현재 `ClientSession` 구조체:
```go
type ClientSession struct {
    ...
    TempTables   []string          // session-scoped #temp_tables
    TempTableMap map[string]string // user #temp_table name -> physical __temp_<sessionID>_tableName
}
```
1. **이중 갱신 비용**:
   - `RegisterTempTable`: `cs.TempTableMap["#"+userKey] = phys`, `cs.TempTableMap[userKey] = phys` 2회 저장 후, `cs.TempTables` 슬라이스를 선형 탐색하여 중복이 아닐 때 `append`.
2. **$O(N)$ 삭제 오버헤드 & 슬라이스 재할당 버그 위험**:
   - `UnregisterTempTable`: 맵에서 삭제한 후 `cs.TempTables`를 `for i, t := range cs.TempTables`로 순회하며 `cs.TempTables = append(cs.TempTables[:i], cs.TempTables[i+1:]...)`로 요소를 잘라냄.
3. **키 중복 저장**:
   - 동일한 테이블 하나에 대해 `#tbl`과 `tbl` 2개의 키를 맵에 중복 보관하여 메모리 낭비 및 값 순회 시 중복 발생.

---

## 3. 목표 아키텍처 설계 (Target SSOT Architecture)

```
[ ClientSession ]
   └── tempTableMap map[string]string (key: canonical "#" + lowerName, value: physicalName)
        │
        ├── RegisterTempTable(tbl) ──────► map["#"+clean] = phys ($O(1)$)
        ├── UnregisterTempTable(tbl) ────► delete(map, "#"+clean) ($O(1)$)
        ├── GetTempTablePhysical(tbl) ───► return map["#"+clean] ($O(1)$)
        └── TempTablePhysicalNames() ────► return values ($O(K)$ deduplicated)
```

### 3.1 `ClientSession` 내부 정리
1. `TempTables []string` 필드 완전 삭제.
2. `TempTableMap map[string]string`의 키를 항상 `"#"` 접두사가 붙은 소문자 표준 키(`strings.ToLower(tbl)` 보정)로 단일화.
3. 물리 테이블명 목록 반환 메서드 제공:
   - `func (cs *ClientSession) TempTablePhysicalNames() []string`: 세션에 등록된 고유 물리 테이블 이름 목록 반환
4. `CloseSession` 및 `ResetSession`에서 `cs.TempTables` 대신 `cs.TempTablePhysicalNames()`를 호출하여 물리 테이블 드롭 목록 생성.

---

## 4. 단계별 실행 계획 (Step-by-Step Execution Plan)

### Step 1: `ClientSession` 임시 테이블 관리 메서드 단일화
- `pkg/engine/session.go`:
  - `RegisterTempTable`: 단일 canonical 키(`"#"+lowerName`)로 `TempTableMap`에 저장 ($O(1)$)
  - `UnregisterTempTable`: canonical 키로 `delete(TempTableMap, clean)` ($O(1)$)
  - `GetTempTablePhysical`: canonical 키로 조회 ($O(1)$)
  - `TempTablePhysicalNames()`: `TempTableMap`의 값들을 슬라이스로 반환
  - `TempTables []string` 구조체 필드 삭제

### Step 2: 호출부 리팩토링
- `pkg/engine/coordinator_impl.go` (`CloseSession`, `ResetSession`):
  - `cs.TempTables` 순회 ➔ `toDropLocal = append(toDropLocal, cs.TempTablePhysicalNames()...)`
- `pkg/engine/session_reset_test.go`:
  - `len(sess.TempTables) == 0` ➔ `len(sess.TempTablePhysicalNames()) == 0`

### Step 3: 3단계 검증 파이프라인 수행
- Build: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
- Test: `go test -v -race ./pkg/engine/...`
- Commit: Conventional Commits 형식으로 커밋
