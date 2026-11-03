# LionSQL 아키텍처 추상화 및 구조적 리팩토링 명세 (REFACTORING.ko.md)

본 문서는 단순 대증적 패치(Whack-a-Mole)를 지양하고, 문제의 근본 원인을 제거하기 위해 추진되는 **아키텍처 추상화 계층 도입(Structural Refactoring & Deletion-First)**의 실행 계획과 검증 현황을 관리하는 전용 대시보드입니다.

---

## 1. 아키텍처 개편 핵심 원칙

1. **Radical KISS, DRY & Deletion-First ([RULE:RadicalKISS])**:
   - 파일 곳곳에 복사-붙여넣기된 수동 파싱 루프와 임의의 정규식을 단일 추상화 계층으로 흡수하고, 중복 코드를 과감히 삭제합니다.
2. **Modular Interface Abstraction ([RULE:ModularInterface])**:
   - Lexer, DML Parser, Expression Evaluator를 독립적이고 재사용 가능한 모듈 계약(Contract)으로 분리합니다.
3. **Proactive Modular Abstraction & Rule of Two ([RULE:ModularAbstraction])**:
   - 조건식 추출, 식별자 정규화, 괄호 매칭 등 2개소 이상 반복되는 로직은 결코 인라인 패치로 방치하지 않고 `pkg/sqlutil` 등 표준 도메인 패키지로 즉시 승격합니다.
4. **Hermetic Zero-Panic Invariance ([RULE:PanicRecovery], [RULE:DefensiveIndexing])**:
   - 제로 나눗셈, 쿼테이션 이스케이프 왜곡, 정규식 매칭 실패를 추상화 계층 수준에서 100% 원천 방어합니다.
5. **3단계 검증 파이프라인 (3-Stage Verification Pipeline: [RULE:PipelineVerification])**:
   - 모든 추상화 작업은 반드시 **Build ➔ Race-Free Test (`./...`) ➔ Conventional Commit**을 완수한 후 완료 처리합니다.

---

## 2. 상태 범례

- `[ ]` : 아키텍처 설계 완료 (착수 대기)
- `[~]` : 추상화 계층 구현 및 중복 코드 제거 진행 중
- `[x]` : 3단계 검증 통과 및 커밋 완료

---

## 3. 핵심 구조적 추상화 과제

| ID | 우선순위 | 영역 | 목표 추상화 계층 | 제거 대상 (Deletion-First) | 상태 | 세부 내용 |
| :---: | :---: | :---: | :--- | :--- | :---: | :--- |
| **`ARCH-LEX-1`** | **P1** | Lexical | `pkg/sqlutil/tokenizer.go`<br>• `SplitTopLevelKeyword`<br>• `FindTopLevelArithmeticOp`<br>• `ExtractTopLevelTuples`<br>• `FindTopLevelKeyword`<br>• `SplitTopLevel` | • `sqlexec_eval.go:splitLogicalConditions`<br>• `sqlexec_eval.go:parseValueTuples`<br>• `sqlexec_eval.go:findArithmeticOp`<br>• `sqlexec_select.go:splitScalarProjections`<br>• `sqlexec_select.go:hasFromClause`<br>• `sqlexec_dml.go:findOnConflictIndex` | **`[x]`** | 작은따옴표(`''`), 백슬래시(`\'`), 큰따옴표(`""`), 대괄호(`[...]`), 중첩 괄호(`()`)를 완벽히 지원하는 단일 고속 상태 머신 스캐너 도입 및 300여 라인 수동 파싱 루프 전면 제거 완료 |
| **`ARCH-DML-1`** | **P1** | Parser | `pkg/engine/dml_parser.go`<br>• `DMLStatement`<br>• `ParseDMLCommand` | • `reDmlInsertValues`<br>• `reDmlInsertCheck`<br>• `reDmlInsertSelect`<br>• `reDmlUpsertPrefix`<br>• `reDmlUpdate`<br>• `reDmlUpdateCheck`<br>• `reDmlDelete`<br>• `reDmlDeleteCheck` | **`[x]`** | 8개의 취약한 DML 정규식을 전면 폐기하고, 토큰 기반 경량 1-Pass DML 구문 파서(`ParseDMLCommand`)로 일원화 완료 |
| **`ARCH-EVAL-1`** | **P1** | Evaluator | `pkg/engine/evaluator.go`<br>• `EvalBinaryOp`<br>• `CoerceAndCalculate`<br>• `EvalUpdateExpr`<br>• `ResolveOperand`<br>• `ToNumeric` | • `sqlexec_eval.go:evalUpdateExpr`<br>• `sqlexec_eval.go:toNumeric`<br>• `sqlexec_eval.go:resolveOperand`<br>• 산술 if-switch 분기 80+ 라인<br>• 미사용 `reEvalArithExpr` 정규식 | **`[x]`** | `pkg/storage/coerce.go` (Type Promotion Lattice)와 연동하여 float/int 강제 변환 왜곡 및 정수 0 나누기 패닉을 원천 봉쇄하는 통합 연산 엔진 구축 완료 |
| **`ARCH-LEX-2`** | **P2** | Lexical | `pkg/sqlutil/tokenizer.go`<br>• `FindAllTopLevelKeyword`<br>• `FindTopLevelChar`<br>• `FindMatchingParen` | • `sqlexec_merge.go:findTopLevelKeyword`<br>• `sqlexec_merge.go:findAllTopLevelKeyword`<br>• `sqlexec_string_split.go:findMatchingParen`<br>• `sqlexec_string_split.go:parseStringSplitArgs`<br>• `sqlexec_procs.go:splitExecParams`<br>• `sqlexec_procs.go:splitConcatenationOutsideQuotes`<br>• `sqlexec_procs.go:findTopLevelEquals`<br>• `query_utils.go:SplitDeclarations` | **`[x]`** | MERGE, STRING_SPLIT, PROCS, QUERY_UTILS 내 잔여 수동 분할 루프 및 인용부호 스캐너 200여 라인 전면 삭제 및 `sqlutil.Tokenizer` 표준 API 일원화 완료 |
| **`ARCH-PRED-1`** | **P1** | Predicate | `pkg/sqlutil/predicate.go`<br>• `StripParentheses`<br>• `ParseInPredicate`<br>• `ParseIsNullPredicate`<br>• `ParseAnyPredicate`<br>• `ParseBetweenPredicate` | • 다중 인라인 `strings.HasPrefix` 및 수동 괄호 벗기기 루프<br>• `BETWEEN ... AND` 인라인 분할 로직 | **`[x]`** | 중첩 괄호(`Dyck Language`)와 리터럴/이스케이프를 완벽 지원하는 도메인 표준 술어 파서 구축 및 단위 테스트 완비 |
| **`ARCH-PRED-2`** | **P1** | Predicate | `pkg/sqlutil/predicate.go`<br>• `ParseBinaryComparison` | • `pkg/catalog/virtual/result.go:reEvalBinaryOp` 정규식 | **`[x]`** | 가상 카탈로그 내 잔여 이항 비교 정규식을 완전 폐기하고, PostgreSQL JSON 연산자(`->`, `->>`) 충돌 방어가 내장된 결정론적 연산자 파서로 일원화 |
| **`ARCH-RULE-SLUG`** | **P1** | Governance | `~/.gemini/config/GEMINI.md`<br>• `GEMINI.md`<br>• `notes/ENGINEERING_CONTEXT.ko.md` | • 코드베이스 및 문서 전반의 휘발성 순번(Rule 15, Rule 23 등) | **`[x]`** | 규칙 삽입/삭제 시마다 번호가 뒤바뀌는 부채를 청산하고 불변 슬러그(`[RULE:xxx]`) 및 캐노니컬 명칭 체계(`Rule Reference Policy`) 수립 완료 |

---

## 4. 실행 로드맵 및 진행 기록

1. **Phase 1: `ARCH-LEX-1` (SQL Scanner 추상화)** ➔ **완료 (`[x]`)**
   - `pkg/sqlutil/tokenizer.go`: 범용 고속 토큰 스캐너 상태 머신 구현 (`SplitTopLevelKeyword`, `FindTopLevelArithmeticOp`, `ExtractTopLevelTuples`, `FindTopLevelKeyword`, `SplitTopLevel`).
   - `sqlexec_eval.go`, `sqlexec_dml.go`, `sqlexec_select.go`, `sqlexec_dbcc.go`, `sqlexec_merge.go` 내 6개소 수동 루프 300여 라인 전면 제거 및 대체.
   - 단위 테스트(`tokenizer_test.go`, `rev_eng_test.go`) 및 100% 레이스 프리 통과 검증 완수.
2. **Phase 2: `ARCH-DML-1` (DML Parser & Dispatcher 추상화)** ➔ **완료 (`[x]`)**
   - **사전 점검 (완료)**: 리팩토링 전 현재 동작을 고정하는 특성화 테스트 `pkg/engine/dml_characterization_test.go` 추가 (INSERT 25건, UPDATE 17건, DELETE 13건). 키워드 생략(`INTO`/`FROM`), 대소문자, 탭/개행, 대괄호/큰따옴표 식별자, 리터럴 내부 키워드/괄호/쉼표, 다중 행 VALUES, `OUTPUT`/`UPSERT`/`ON CONFLICT`/`INSERT...SELECT`(정규식 전용 경로) 및 에러 케이스를 포함합니다. 새 파서는 이 테스트를 그대로 통과해야 합니다.
   - **사전 점검 발견 사항**: `reDmlUpdate`가 `SET` 절을 지연 매칭(`.*?`)하여 문자열 리터럴 내부의 `WHERE`에서 절을 분할하고, 에러 없이 0건 갱신 후 성공을 반환함 (`REV-ENG-006`, `OUTPUT` 경로 한정). 해당 케이스는 `knownBug`로 추적 중이며 `ARCH-DML-1` 완료 시 마커를 제거해야 테스트가 통과합니다.
   - `pkg/engine/dml_parser.go` 신설: 키워드 생략, 공백 변형에 면역인 1-Pass DML 디스패처 구축.
   - 복잡한 10여 개 DML 정규식 전면 삭제.
3. **Phase 3: `ARCH-EVAL-1` (통합 산술 및 연산자 평가 계층)** ➔ **완료 (`[x]`)**
   - `pkg/engine/evaluator.go` 신설: `EvalBinaryOp`, `CoerceAndCalculate`, `ToNumeric`, `ResolveOperand`, `EvalUpdateExpr` 구축.
   - `pkg/storage/coerce.go` Type Promotion Lattice(Decimal > Float > Int) 연동 및 0 나누기 방어 가드 일원화.
   - `sqlexec_eval.go` 내 산재해 있던 중복 연산자 분기 120여 라인 및 미사용 정규식 `reEvalArithExpr` 전면 삭제.
   - 단위 테스트 `evaluator_test.go` 작성 및 100% 검증 통과.
4. **Phase 4: `ARCH-LEX-2` (잔여 파서 `tokenizer.go` 전면 일원화 및 중복 루프 제거)** ➔ **완료 (`[x]`)**
   - `pkg/sqlutil/tokenizer.go`: `FindAllTopLevelKeyword`, `FindTopLevelChar`, `FindMatchingParen` 공통 API 확충 및 단위 테스트 완비.
   - `sqlexec_merge.go`: 자체 구현되었던 `findTopLevelKeyword`, `findAllTopLevelKeyword`, `isWordBoundary` 등 50여 라인 삭제 및 `sqlutil` 대체.
   - `sqlexec_string_split.go`: 40여 라인 수동 파싱 루프 삭제 및 `sqlutil.SplitTopLevel`, `sqlutil.FindMatchingParen` 연동.
   - `sqlexec_procs.go`: `splitExecParams`, `splitConcatenationOutsideQuotes`, `findTopLevelEquals` 등 120여 라인 수동 인용부호 스캐너 삭제 및 `sqlutil` 일원화.
   - `query_utils.go`: `SplitDeclarations` 50여 라인 루프 삭제 및 `sqlutil.SplitTopLevel` 일원화.
   - 총 250여 라인의 취약한 중복 인용부호/괄호 분할 루프를 전면 제거하고 단일 토크나이저 상태머신으로 통일.
5. **Phase 5: `ARCH-PRED-1` & `ARCH-PRED-2` (조건식·비교 연산자 모듈화 및 잔여 정규식 제거)** ➔ **완료 (`[x]`)**
   - `pkg/sqlutil/predicate.go`: 중첩 괄호(`StripParentheses`), IN-list(`ParseInPredicate`), IS NULL(`ParseIsNullPredicate`), ANY(`ParseAnyPredicate`), BETWEEN(`ParseBetweenPredicate`), 이항 비교(`ParseBinaryComparison`)를 전담하는 표준 술어 파서 패키지 신설.
   - PostgreSQL JSON 연산자(`->`, `->>`) 내 `>` 기호 오작동 방지 가드 내장.
   - `pkg/catalog/virtual/result.go`: 패키지 레벨 `reEvalBinaryOp` 정규식 완전 삭제 및 `sqlutil.ParseBinaryComparison` 연동.
   - `pkg/sqlutil/predicate_test.go`: 100% 통과 단위 테스트 완비.
6. **Phase 6: `ARCH-RULE-SLUG` (골든 룰 불변 슬러그 체계 정립)** ➔ **완료 (`[x]`)**
   - 글로벌/프로젝트 `GEMINI.md` 및 `notes/ENGINEERING_CONTEXT.ko.md` 내 규칙 순번 의존성 완전 청산.
   - `[RULE:xxx]` 불변 식별자 및 캐노니컬 명칭 원칙(`Rule Reference Policy`) 공식 확립.
   - Go 소스코드 주석 및 감사 문서 내 과거 순번 일괄 정리.
