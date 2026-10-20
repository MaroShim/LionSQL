# LionSQL 아키텍처 추상화 및 구조적 리팩토링 명세 (REFACTORING.ko.md)

본 문서는 단순 대증적 패치(Whack-a-Mole)를 지양하고, 문제의 근본 원인을 제거하기 위해 추진되는 **아키텍처 추상화 계층 도입(Structural Refactoring & Deletion-First)**의 실행 계획과 검증 현황을 관리하는 전용 대시보드입니다.

---

## 1. 아키텍처 개편 핵심 원칙

1. **Radical KISS, DRY & Deletion-First (골든 룰 #15)**:
   - 파일 곳곳에 복사-붙여넣기된 수동 파싱 루프와 임의의 정규식을 단일 추상화 계층으로 흡수하고, 중복 코드를 과감히 삭제합니다.
2. **Modular Interface Abstraction (골든 룰 #19)**:
   - Lexer, DML Parser, Expression Evaluator를 독립적이고 재사용 가능한 모듈 계약(Contract)으로 분리합니다.
3. **Hermetic Zero-Panic Invariance (골든 룰 #2, #11)**:
   - 제로 나눗셈, 쿼테이션 이스케이프 왜곡, 정규식 매칭 실패를 추상화 계층 수준에서 100% 원천 방어합니다.
4. **3단계 검증 파이프라인 (3-Stage Verification Pipeline)**:
   - 모든 추상화 작업은 반드시 **Build ➔ Race-Free Test (`./...`) ➔ Conventional Commit**을 완수한 후 완료 처리합니다.

---

## 2. 상태 범례

- `[ ]` : 아키텍처 설계 완료 (착수 대기)
- `[~]` : 추상화 계층 구현 및 중복 코드 제거 진행 중
- `[x]` : 3단계 검증 통과 및 커밋 완료

---

## 3. 핵심 3대 구조적 추상화 과제

| ID | 우선순위 | 영역 | 목표 추상화 계층 | 제거 대상 (Deletion-First) | 상태 | 세부 내용 |
| :---: | :---: | :---: | :--- | :--- | :---: | :--- |
| **`ARCH-LEX-1`** | **P1** | Lexical | `pkg/sqlutil/tokenizer.go`<br>• `SplitTopLevelKeyword`<br>• `FindTopLevelArithmeticOp`<br>• `ExtractTopLevelTuples`<br>• `FindTopLevelKeyword`<br>• `SplitTopLevel` | • `sqlexec_eval.go:splitLogicalConditions`<br>• `sqlexec_eval.go:parseValueTuples`<br>• `sqlexec_eval.go:findArithmeticOp`<br>• `sqlexec_select.go:splitScalarProjections`<br>• `sqlexec_select.go:hasFromClause`<br>• `sqlexec_dml.go:findOnConflictIndex` | **`[x]`** | 작은따옴표(`''`), 백슬래시(`\'`), 큰따옴표(`""`), 대괄호(`[...]`), 중첩 괄호(`()`)를 완벽히 지원하는 단일 고속 상태 머신 스캐너 도입 및 300여 라인 수동 파싱 루프 전면 제거 완료 |
| **`ARCH-DML-1`** | **P1** | Parser | `pkg/engine/dml_parser.go`<br>• `DMLStatement`<br>• `ParseDMLCommand` | • `reDmlInsertValues`<br>• `reDmlInsertCheck`<br>• `reDmlInsertSelect`<br>• `reDmlUpsertPrefix`<br>• `reDmlUpdate`<br>• `reDmlUpdateCheck`<br>• `reDmlDelete`<br>• `reDmlDeleteCheck` | `[ ]` | 10여 개의 복잡하고 취약한 DML 정규식을 전면 폐기하고, 토큰 기반 경량 1-Pass DML 구문 파서 및 디스패처로 일원화 |
| **`ARCH-EVAL-1`** | **P1** | Evaluator | `pkg/engine/evaluator.go`<br>• `EvalBinaryOp`<br>• `CoerceAndCalculate` | • `sqlexec_eval.go:evalUpdateExpr`<br>• `sqlexec_eval.go:toNumeric`<br>• `sqlexec_eval.go:resolveOperand`<br>• 산술 if-switch 분기 80+ 라인 | `[ ]` | `pkg/storage/coerce.go` (Type Promotion Lattice)와 연동하여 float/int 강제 변환 왜곡 및 정수 0 나누기 패닉을 원천 봉쇄하는 통합 연산 엔진 구축 |

---

## 4. 실행 로드맵 및 진행 기록

1. **Phase 1: `ARCH-LEX-1` (SQL Scanner 추상화)** ➔ **완료 (`[x]`)**
   - `pkg/sqlutil/tokenizer.go`: 범용 고속 토큰 스캐너 상태 머신 구현 (`SplitTopLevelKeyword`, `FindTopLevelArithmeticOp`, `ExtractTopLevelTuples`, `FindTopLevelKeyword`, `SplitTopLevel`).
   - `sqlexec_eval.go`, `sqlexec_dml.go`, `sqlexec_select.go`, `sqlexec_dbcc.go`, `sqlexec_merge.go` 내 6개소 수동 루프 300여 라인 전면 제거 및 대체.
   - 단위 테스트(`tokenizer_test.go`, `rev_eng_test.go`) 및 100% 레이스 프리 통과 검증 완수.
2. **Phase 2: `ARCH-DML-1` (DML Parser & Dispatcher 추상화)**
   - `pkg/engine/dml_parser.go` 신설: 키워드 생략, 공백 변형에 면역인 1-Pass DML 디스패처 구축.
   - 복잡한 10여 개 DML 정규식 전면 삭제.
3. **Phase 3: `ARCH-EVAL-1` (통합 산술 및 연산자 평가 계층)**
   - `pkg/engine/evaluator.go` 신설: Type Promotion Lattice 기반 연산자 평가 일원화.
