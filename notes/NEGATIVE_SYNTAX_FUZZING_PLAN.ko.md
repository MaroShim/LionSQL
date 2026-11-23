# Architecture Plan: Negative Syntax Fuzzing & Core Parser Fail-Closed Overhaul

> **Document Primacy & Rule Adherence**:
> - Rule Reference Policy: `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`, `[RULE:PipelineVerification]`, `[RULE:ExplicitError]`, `[RULE:RadicalKISS]`
> - File: `notes/NEGATIVE_SYNTAX_FUZZING_PLAN.ko.md`
> - Task ID: `FIX-NEGATIVE-SYNTAX-FUZZER-1` (sprint_tasks / `.sprint.db`)
> - Date: 2026-11-23

---

## 1. 개요 및 배경 (Context & Problem Statement)

### 1.1 배경: 데이터베이스 엔진 성숙기 전이와 Fail-Open 결함
LionSQL은 복잡한 DDL/DML, T-SQL 저장 프로시저 VDBE 가상 머신, Volcano CBO 플래너, 인메모리 OCC 및 WiredTiger 이중 스토리지 엔진을 구축하여 정상 쿼리(Happy Path) 처리 능력에서 엔터프라이즈급 안정성을 확보했습니다.

그러나 최근 2중 적대적 감사(Adversarial Audit)를 통해, **"문법 오류나 기형적 토큰이 주어졌을 때 에러를 발생시키지 않고 조용히 성공(Silent Drop / Unhandled Success)하는 치명적 관용성 결함"**이 다수 발견되었습니다:
- `DELETE FROM t WHERE;` ➔ 에러 없이 **전체 테이블 행 영구 삭제**
- `SELECT * FROM t ORDER BY;` ➔ 에러 없이 **정렬 누락된 채 원본 순서 반환**
- `UPDATE t SET val = ;` ➔ 에러 없이 **업데이트 완료 통과**

### 1.2 15,177개 대량 역문법 퍼징(Negative Syntax Fuzzing) 결과
기형 쿼리 15,177개를 기계적으로 생성·주입한 1차 10x Fuzzing 결과:
- **총 쿼리**: 15,177건 (26.07초 소요)
- **정상 거부 (Syntax Error 차단)**: 9,370건 (61.74%)
- **비정상 무단 성공 (Silent Drop 결함)**: **5,807건 (38.26%)**

데이터베이스 신뢰성 공학에서 "크래시(Panic)가 나는 DB"보다 **"에러가 나야 하는데 엉뚱한 결과를 내면서 조용히 성공하는 DB"가 비교할 수 없을 정도로 데이터 무결성에 치명적**입니다. 따라서 이 방법론을 정식 검증 파이프라인으로 승격하고 코어 파서를 전수 개조합니다.

---

## 2. 방법론: 6대 변이 벡터 기반 Negative Syntax Fuzzing

역문법 퍼징(Negative Syntax Fuzzing)은 **오라클(Ground Truth) 정의가 극도로 단순하고 명확**합니다:
$$\forall Q \in \text{NegativeQueries}, \quad \text{Exec}(Q) \implies \text{Error} \neq \text{nil} \quad (\text{Fail-Closed})$$
만약 `err == nil`로 성공하면 결과값 비교 없이도 **100% 엔진 결함(Defect)**으로 즉각 판정됩니다.

### 6대 변이 벡터 (Mutation Vectors)

| 벡터 번호 | 분류명 | 변이 메커니즘 | 대표 쿼리 패턴 |
|:---:|:---|:---|:---|
| **V1** | **Isolated Clause Keywords** | SQL 주요 절 키워드 뒤에 본문을 비우거나 EOF/세미콜론 배치 | `SELECT * FROM t WHERE;`<br>`SELECT * FROM t GROUP BY;`<br>`SELECT * FROM t ORDER BY;` |
| **V2** | **Dangling Operators & Separators** | 쉼표(`,`), 논리 연산자, 산술 연산자, 대입 연산자를 문장 말단에 고립 | `SELECT a, FROM t;`<br>`SELECT a FROM t WHERE id = 1 AND;`<br>`UPDATE t SET a = 1,;` |
| **V3** | **Empty Dyck / Broken Parens** | 괄호 필수 연산자/함수/절 내부에 빈 괄호 또는 불완전 인자 주입 | `WHERE id IN ();`<br>`LIMIT ();`<br>`CAST( AS INT);`<br>`COALESCE(,);` |
| **V4** | **Type-Confusion & Lexical Fragments** | 렉서 및 숫자/문자열 파서 파괴 토큰 주입 | `LIMIT abc;`<br>`LIMIT -1;`<br>`LIMIT 1e+;`<br>`SELECT 'unclosed string;` |
| **V5** | **Trailing Garbage Tokens** | 정상 절 뒤에 문맥에 맞지 않는 불법 키워드/토큰 결합 | `SELECT * FROM t SET ;`<br>`SELECT * FROM t VALUES ;`<br>`SELECT * FROM t ON ;` |
| **V6** | **Combinatorial Clause Permutations** | 유효 절 체인 사이에 기형 토큰 무작위 주입 | `SELECT a FROM t WHERE id > 0 GROUP BY %%% ORDER BY a;` |

---

## 3. 5,807건 결함의 근본 원인 및 파서 전수 개조 아키텍처

5,807건의 결함은 파서 계층의 4가지 근본 결함으로 수렴되며, 다음과 같이 전수 개조를 시행합니다:

### 3.1 개조 과제 1: Trailing Garbage Token Firewall (조기 파싱 종료 방지)
- **근본 원인**: `parseGeneralSelect` 및 `parseGeneralSelectClauses`가 `FROM`, `WHERE`, `ORDER BY` 등을 파싱한 후, 뒤에 남아있는 미파싱 토큰(`SET`, `VALUES`, `ON`, 가비지 식별자)이 있는지 검사하지 않고 조기 리턴함.
- **해결책**:
  - `ParseSQLStatement()` 및 `parseGeneralSelectClauses()` 종료 시점에 `p.curToken`이 `TokenEOF` 또는 `TokenSemicolon`인지 반드시 단언(`Assert`).
  - 세미콜론이나 EOF가 아닌 토큰이 남아있으면 즉시 `syntax error: unexpected token after query: %s`로 Fail-Closed 차단.

### 3.2 개조 과제 2: Expression Parsing Fail-Closed Enforcement (표현식 nil 승격)
- **근본 원인**: `LIMIT`, `OFFSET`, `FETCH`, `HAVING`, `WHERE`, `GROUP BY`, `ORDER BY` 등에서 서브 표현식을 파싱할 때 `p.parseExpression(...)`이 `nil`을 반환하거나 `len(p.errors) > 0`임에도 이를 무시하고 기본값으로 넘어감.
- **해결책**:
  - `p.parseExpression(precLowest)` 호출 후 반환된 `Expr == nil`이거나 `len(p.errors) > 0`인 경우 예외 없이 즉각 에러 반환.
  - 특히 빈 괄호 `()`, `(,)` 처리 시 빈 서브쿼리나 빈 표현식을 즉시 문법 에러로 거부.

### 3.3 개조 과제 3: Lexer Fragment & Scientific Notation Robustness
- **근본 원인**: `1e`, `1e+`, `0b`, `123.` 등 완성되지 않은 숫자 접두어가 들어왔을 때 렉서가 이를 불완전 토큰으로 반환하고 파서가 0/1 또는 빈 값으로 처리.
- **해결책**:
  - 렉서에서 과학적 표기법 지수부(`e`, `e+`, `e-`) 뒤에 숫자가 없는 경우 `TokenIllegal` 또는 문법 오류를 명시적으로 방출.

### 3.4 개조 과제 4: DML Body Truncation Fail-Closed
- **근본 원인**: `INSERT INTO t;`, `INSERT INTO t (a, b);`와 같이 `VALUES`나 `SELECT`가 완전히 결여된 DML을 파서가 에러 없이 넘겨 `0 rows affected`로 처리.
- **해결책**:
  - `parseInsertBody`에서 `VALUES`, `SELECT`, `EXEC`, `DEFAULT VALUES` 중 하나가 반드시 존재함을 검증하고 결여 시 즉시 Fail-Closed 차단.

---

## 4. 실행 계획 (3-Stage Execution Roadmap)

1. **Phase 1: 플랜 문서화 및 스프린트 등록 (`[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`)**
   - 본 계획서를 `notes/NEGATIVE_SYNTAX_FUZZING_PLAN.ko.md`로 물리 디스크에 영구 보존.
   - 스프린트 DB(`sprint_tasks`)에 `FIX-NEGATIVE-SYNTAX-FUZZER-1` 등록 및 상태 동기화.
2. **Phase 2: 코어 파서 전수 개조 구현**
   - `pkg/proc/parser_dml.go`: Trailing token firewall (`TokenEOF` 검사) 도입.
   - `pkg/proc/parser_dml.go`: `LIMIT`/`OFFSET` 빈 괄호 및 표현식 nil fail-closed 강화.
   - `pkg/proc/lexer.go`: 불완전 지수부 및 숫자 파편 렉서 방어.
   - `pkg/engine/dml_parser.go`: 결여된 INSERT 본문 fail-closed 검증.
3. **Phase 3: 15,177건 10x Fuzzer 재검증 및 3-Stage Pipeline 통과**
   - `go test -v ./pkg/engine -run TestNegative_SyntaxFuzzer` 실행하여 5,807건 결함이 0건으로 소멸하는지 확인 (Defects = 0).
   - 기존 전체 회귀 테스트 통과 확인 (`go test -v -race ./...`).
   - Conventional Commits 형식으로 단일 원자적 커밋 (`[RULE:PipelineVerification]`).
