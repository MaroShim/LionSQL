# LionSQL 전역 타입 임의 추측(Heuristic Type Guessing) 안티패턴 근본 제거 계획서
## Architecture Decision Record & Remediation Plan: Banning Arbitrary String Coercion

- **문서 ID**: `PLAN-TYPE-GUESS-ELIM-1`
- **상태**: Draft ➔ Review Pending
- **작성일자**: 2026-10-24
- **대상 패키지**: `pkg/engine`, `pkg/wire`, `pkg/catalog/virtual`, `pkg/api`, `pkg/sqlutil`
- **관련 규칙**: Golden Rule 14 (Platform Primitives First), Golden Rule 15 (Radical KISS & DRY), Golden Rule 7 (Explicit Error Visibility), [`notes/BUG_ARCHIVE.ko.md`](notes/BUG_ARCHIVE.ko.md)

---

## 1. 배경 및 문제 의식 (Problem Statement)

LionSQL은 107대 결함 조치(`BUG-107`)를 통해 DML(UPDATE/DELETE/TRUNCATE), 기본키(PK) 식별, Go 맵 순회 등 핵심 데이터 무결성 경로에서 **"임의 추측(Heuristic Guessing) 안티패턴"**을 공식 퇴출한 바 있습니다.

그러나 최근 TPC-C 네트워크 클라이언트 연동 과정에서 `New-Order` 프로시저의 `'1_1_1001'`(Warehouse_District_OrderId) 문자열 복합키 인자가 `strconv.ParseFloat`에 의해 숫자(`111001`)로 강제 변환되어 `orders` 테이블 PK 삽입 에러(`BUG-PROC-VARCHAR-NUM-COERCE-1`)를 유발하는 결함이 발견되었습니다.

코드베이스 전수 정적 분석 결과, 이 문제는 `sqlexec_procs.go`에 국한된 단발성 실수가 아니라, **과거 레거시 파서/실행기 곳곳에 복사-붙여넣기 형태로 잔존한 5대 "임의 추측 및 문자열 강제 변환(Heuristic String Coercion)" 안티패턴**에서 비롯된 구조적 문제임이 확인되었습니다:

1. **따옴표 문맥 선제거(Loss of Literal Context)**: `'...'`로 명시된 문자열을 `unwrapSQLString`으로 벗겨낸 후 `strconv.ParseInt/ParseFloat`를 무차별 실행.
2. **정렬 및 집계의 무조건 수치 변환(Blind Numeric Sorting)**: `fmt.Sprintf("%v", v)`로 문자열화한 뒤 `ParseFloat`로 정렬하거나 MIN/MAX를 계산하여, 우편번호(`"012"` vs `"2"` vs `"10"`)나 문자열 코드가 사전순이 아닌 숫자 크기로 오작동.
3. **와이어 파라미터 치환 시 따옴표 탈락**: PGWire 텍스트 모드 치환기(`substituteParams`)에서 숫자로 파싱되면 따옴표를 제거하여 `$1`에 바인딩.
4. **DRY 위반 4중 중복 코드**: 슬라이스 정렬 및 비교 코드가 4개 파일에 동일하게 중복 복사됨.
5. **엄격한 린트/가드 부재**: 메타데이터나 리터럴 문맥을 무시하고 `strconv`를 사용하는 코드를 컴파일/테스트 단계에서 잡아내지 못함.

---

## 2. 전수 감사 결과: 5대 위험 지점 및 영향 분석

```
[클라이언트 쿼리 / 와이어 파라미터]
        │
        ├─ 1. sqlexec_prepare.go: parseArgValue() ────────► 따옴표 지운 후 ParseInt/Float 시도 (위험도: 치명적)
        ├─ 2. wire/server.go: substituteParams() ─────────► ParseFloat 성공 시 따옴표 탈락 (위험도: 치명적)
        ├─ 3. sqlexec_select.go: compareAggValues() ──────► MIN/MAX 시 문자열 컬럼을 수치로 왜곡 (위험도: 높음)
        ├─ 4. sqlexec_select/pivot/dbcc/result 정렬 ─────► ORDER BY 시 사전순 대신 숫자 크기로 정렬 (위험도: 높음)
        ├─ 5. sqlexec_eval.go: evaluateCondition() ───────► WHERE 비교(>, <) 시 무조건 ParseFloat (위험도: 보통)
        └─ 6. api/records_filter.go: toFloat64() ─────────► string 타입을 무조건 float64 변환 (위험도: 보통)
```

### 상세 분석표

| 번호 | 파일 및 함수 위치 | 현행 코드 패턴 | 결함 영향 및 발생 시나리오 | 수정 방안 |
| :---: | :--- | :--- | :--- | :--- |
| **G-1** | [`pkg/engine/sqlexec_prepare.go`](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_prepare.go#L540-L557)<br>`parseArgValue()` | `raw := unwrapSQLString(arg)` 후 `strconv.ParseInt/ParseFloat` 무차별 시도 | `sp_executesql`, `sp_prepare` 호출 시 `'00123'`, `'1_1_1001'`, `'2026-10-24'`가 정수/실수로 왜곡됨 | 따옴표(`'...'`, `N'...'`, `"..."`)가 있던 리터럴은 즉시 `string`으로 반환하고 숫자 파싱 원천 차단 |
| **G-2** | [`pkg/wire/server.go`](file:///Users/maro/Projects/SQLion/pkg/wire/server.go#L1804-L1815)<br>`substituteParams()` | `if _, err := strconv.ParseFloat(val, 64); err == nil` 따옴표 없이 치환 | PGWire에서 `$1 = "00123"` 전달 시 `WHERE code = 00123`으로 치환되어 문자열 컬럼 비교 실패 | `portal.Statement.ParamOIDs`를 참조하여 문자열/UUID/날짜 타입은 반드시 따옴표 인용 보장 |
| **G-3** | [`pkg/engine/sqlexec_select.go`](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_select.go#L1576-L1606)<br>`compareAggValues()` | `fmt.Sprintf` 후 `strconv.ParseFloat` 시도하여 MIN/MAX 계산 | `MIN('012', '2')` 실행 시 문자열 사전순(`'012'`)이 아닌 수치 크기(`'2'`)가 반환되는 SQL 표준 위반 | Go 타입 기반 다형적 비교기 `sqlutil.CompareAny`로 일원화 (문자열은 사전순 비교) |
| **G-4** | [`pkg/engine/sqlexec_select.go:1865`](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_select.go#L1865)<br>[`pkg/engine/sqlexec_pivot.go:438,973`](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_pivot.go#L438)<br>[`pkg/engine/sqlexec_dbcc.go:320`](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_dbcc.go#L320)<br>[`pkg/catalog/virtual/result.go:47`](file:///Users/maro/Projects/SQLion/pkg/catalog/virtual/result.go#L47) | 4개 파일에 중복된 슬라이스 정렬 코드 (`fmt.Sprintf` + `ParseFloat`) | 1) 문자열 컬럼 정렬 왜곡<br>2) 매 정렬 루프마다 힙 할당 및 부동소수점 파싱으로 GC 압박 폭증 | `sqlutil.CompareAny(v1, v2)` 단일 함수로 완전 통합 (DRY 준수, 무할당 비교) |
| **G-5** | [`pkg/engine/sqlexec_eval.go`](file:///Users/maro/Projects/SQLion/pkg/engine/sqlexec_eval.go#L248-L275)<br>`evaluateCondition()` | `>`, `>=`, `<`, `<=` 비교 시 좌우 값을 `strconv.ParseFloat`로 변환 | 문자열 컬럼에 대한 `WHERE col > '012'` 비교 시 수치로 변환되어 사전식 비교 실패 | 좌측 컬럼의 실제 타입(`val`)이 `string` 또는 `TypeVarChar`인 경우 문자열 사전식 비교 수행 |
| **G-6** | [`pkg/api/records_filter.go`](file:///Users/maro/Projects/SQLion/pkg/api/records_filter.go#L168-L172)<br>`toFloat64()` | `case string: strconv.ParseFloat(...)`로 문자열을 숫자로 취급 | Web Studio 필터에서 `zip_code = "123"` 검색 시 `"00123"`, `"123.0"`이 일치해 버리는 결함 | `toFloat64`에서 암묵적 문자열 변환 제거 (수치 타입만 수치로 취급) |

---

## 3. 핵심 아키텍처 원칙 (Immutable Architectural Invariants)

1. **리터럴 문맥 보존 (Literal Quoting Context Invariance)**:
   - SQL에서 작은따옴표(`'...'`), 유니코드 접두사(`N'...'`), 큰따옴표(`"..."`)로 감싸진 리터럴은 **작성자가 명시적으로 선언한 강력한 문자열 타입 표식**입니다.
   - 파라미터 파서나 바인딩 계층은 절대로 따옴표 정보를 지운 채 내부 내용만으로 숫자인지 추측해서는 안 됩니다.
2. **메타데이터 우선의 법칙 (Metadata Supremacy)**:
   - 프로시저 시그니처(`procMeta.Params[i].Type`), 테이블 스키마 컬럼 타입(`col.Type`), 또는 와이어 프로토콜 파라미터 OID(`ParamOIDs[i]`)가 존재한다면, **선언된 정적 타입이 100% 최우선 진실의 원천(Source of Truth)**입니다.
   - 정적 타입이 `VARCHAR`, `TEXT`, `UUID`, `DATETIME`이면 수치 파싱 시도를 일절 금지합니다.
3. **단일 다형적 비교기 표준화 (Zero-Allocation DRY Comparator)**:
   - 값을 비교하거나 정렬할 때 `fmt.Sprintf("%v", v)`로 문자열화한 뒤 `ParseFloat`를 돌리는 안티패턴을 전면 금지합니다.
   - `pkg/sqlutil`에 제로-할당 다형적 비교 함수 `CompareAny(a, b any) int`를 정의하고 엔진 전체가 이를 단일 공유합니다.

---

## 4. 구체적 실행 계획 (Actionable Roadmap)

### Phase 1: `pkg/sqlutil` 표준 비교기 신설 및 정렬/집계 중복 해소 (`G-3`, `G-4`)
* **목표**: 4개 파일에 분산된 `fmt.Sprintf` + `strconv.ParseFloat` 정렬/집계 복사-붙여넣기 코드를 완전 삭제하고 표준 비교기로 교체.
* **작업 내용**:
  1. `pkg/sqlutil/comparator.go` 신설:
     ```go
     // CompareAny compares two arbitrary database values according to strict SQL semantics:
     // - Nil/NULL handling (NULLs first/last)
     // - Typed numeric comparison (int, float, decimal) without string allocations
     // - Typed string comparison (lexicographical / case-insensitive)
     // - Time comparison
     func CompareAny(a, b any) int
     ```
  2. `sqlexec_select.go`의 `compareAggValues`, `sqlexec_pivot.go`, `sqlexec_dbcc.go`, `catalog/virtual/result.go` 정렬 슬라이스를 `CompareAny`로 일원화.

### Phase 2: 파라미터 바인딩 및 프로시저/준비된 문장 리터럴 문맥 보존 (`G-1`, `G-2`)
* **목표**: `sp_executesql`, `sp_prepare`, PGWire에서 문자열 파라미터가 숫자로 오변환되거나 따옴표가 누락되는 문제 원천 차단.
* **작업 내용**:
  1. `pkg/engine/sqlexec_prepare.go` (`parseArgValue`):
     - 따옴표 검사를 최우선 수행 (`strings.HasPrefix(arg, "'")` 등). 따옴표 리터럴은 언래핑 후 즉시 `string` 반환하고 `ParseInt/ParseFloat` 바이패스.
  2. `pkg/wire/server.go` (`substituteParams`):
     - `portal.Statement.ParamOIDs` 정보를 전달받아 타입이 식별 가능한 경우 OID에 따라 정규 따옴표 바인딩.
     - OID가 없는 경우에도 단순 수치 파싱으로 따옴표를 생략하지 않고, 명시적 안전 규칙 적용.

### Phase 3: WHERE 조건 평가 및 Web Studio 필터 휴리스틱 제거 (`G-5`, `G-6`)
* **목표**: 레거시 인터프리터(`sqlexec_eval.go`) 및 API 필터(`records_filter.go`)의 무차별 문자열 부동소수점 변환 제거.
* **작업 내용**:
  1. `pkg/engine/sqlexec_eval.go` (`evaluateCondition`):
     - 좌측 컬럼 `val`이 문자열이면 `ParseFloat`를 건너뛰고 표준 문자열 비교 수행.
  2. `pkg/api/records_filter.go` (`toFloat64`):
     - `case string:` 블록을 안전하게 격리/수정하여 문자열 컬럼이 숫자로 오인되지 않도록 보장.

### Phase 4: 전수 회귀 테스트 및 영구 방어 가드레일 구축
* **목표**: 문자열 식별자(`'00123'`, `'1_1_1001'`, `'010-1234'`, `'2026-10-24'`)에 대한 종합 회귀 테스트 스위트 작성 및 3단계 파이프라인 검증.
* **작업 내용**:
  1. 단위 테스트:
     - `pkg/engine/sqlexec_prepare_test.go`: `sp_executesql` 문자열 인자 보존 검증.
     - `pkg/engine/comparator_test.go`: `CompareAny`의 문자열 vs 수치 정렬 정합성 검증 (`"012"` < `"2"` < `"10"`).
     - `pkg/wire/pgwire_test.go`: PGWire 텍스트 파라미터 문자열 따옴표 보존 검증.
     - `pkg/api/records_filter_test.go`: API 필터 문자열 컬럼 정확도 검증.
  2. `go test -tags wiredtiger -v -race ./...` 100% PASS 검증.
  3. `TODO.ko.md` 및 `CHANGELOG.md` 공식 동기화.

---

## 5. 기대 효과 및 불변 보장

1. **무결성 100% 보장**: 복합 문자열 키(`'1_1_1001'`), 전화번호, 우편번호, 코드값이 어떤 경로(EXEC, SP, PGWire, WHERE, ORDER BY)를 거치더라도 숫자로 오변환되지 않음.
2. **성능 향상 및 GC 압박 급감**: ORDER BY 정렬 루프에서 매번 실행되던 불필요한 `fmt.Sprintf` 힙 할당과 `strconv.ParseFloat` 중복 파싱이 제거되어 CPU/메모리 오버헤드 대폭 감소.
3. **코드 중복 70줄+ 영구 삭제 (DRY)**: 4개 파일에 걸친 조잡한 정렬 코드가 단일 `sqlutil.CompareAny`로 통합되어 유지보수성 극대화.
