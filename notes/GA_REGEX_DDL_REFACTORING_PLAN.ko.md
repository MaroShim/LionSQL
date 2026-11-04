# LionSQL DDL 및 잔여 정규식 완전 제거(Lexer/AST 전환) 설계 계획서 (`notes/GA_REGEX_DDL_REFACTORING_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-02  
> - **상태**: 구현 및 검증 완료 ( - Commit , 정규식 35+종 완전 제거)  
> - **과제 식별 코드**: `GA-REGEX-DDL-1`  
> - **목표**: `sqlexec_ddl.go`, `sqlexec_eval.go`, `sqlexec_explain.go` 내의 모든 레거시 정규식(`regexp.MustCompile`)을 100% 제거하고, `proc.Lexer`, `sqlutil` 토크나이저 및 Pratt AST 파서로 전환하여 파싱 오류 원천 차단 및 무결성 확보  

---

## 1. 배경 및 문제 정의

LionSQL은 과거 플래너(`pkg/planner`)와 와이어(`pkg/wire`), DML 실행기(`sqlexec_dml.go`)에서 정규식을 전면 퇴출하고 순수 Pratt AST 및 Table-Driven 토크나이저로 성공적으로 전환하였습니다(`PLANNER-AST-1~3`, `ROUTING-AST-CATALOG-1`, `COMPAT-TABLE-DRIVEN-1`).

하지만 여전히 다음 3개 파일에 레거시 정규식이 잔존하여 실전 앱 마이그레이션 및 DDL 실행 시 잠재적 구문 파싱 오류를 유발하고 있습니다:

1. **`sqlexec_ddl.go` 내의 DDL 정규식 (35개 이상)**:
   - `reCreateTablePrefix`: `CREATE TABLE /* 주석 */ [dbo].[users] (`와 같은 T-SQL 주석이나 대괄호 스키마 접두사 파싱 시 매칭 실패 또는 오추출 위험.
   - `reAddColumn`, `reAlterColType`: `ALTER TABLE t ADD col VARCHAR(64) DEFAULT 'a, b'`처럼 문자열 리터럴 내에 쉼표(`,`)나 공백이 포함된 경우 엉뚱한 위치에서 컬럼 타입이 잘려 파싱 에러 발생.
   - `reColDefault`: `DEFAULT 'str'`뿐 아니라 `GENERATED ALWAYS AS IDENTITY` 뒤의 `AS`를 기본값으로 오인식하거나, 복합 표현식(`DEFAULT (getdate())`)을 불완전하게 추출하는 문제.
   - `reInlineFK`, `reTableFK`: 대괄호 및 멀티파트 식별자(`[schema].[table]([col])`) 처리 시 정규식 탐욕성(Greedy)으로 인한 괄호 불일치.
   - `reDropTable`, `reTruncateTable`, `reCreateView`, `reDropView`, `reCreateIndex`, `reDropIndex`: 단순한 DDL 라우팅마저 거대한 정규식 매칭에 의존.
2. **`sqlexec_eval.go` 내의 조건식 정규식 (`reEvalInClause`, `reEvalEqCondition`)**:
   - `WHERE col IN ('a', 'b')` 및 `WHERE col = val`을 파싱할 때 리터럴 내 따옴표나 괄호가 포함된 경우 정규식이 오작동.
3. **`sqlexec_explain.go` 내의 정규식 (`reExplainTvfCall`, `reExplainInsert` 등)**:
   - DML/서브쿼리 판별을 위한 정규식 휴리스틱.

---

## 2. 세부 전환 설계

### 2.1. Phase 1: `sqlexec_eval.go` & `sqlexec_explain.go` 정규식 완전 제거
- `reEvalInClause`, `reEvalEqCondition` 삭제:
  - `proc.Lexer` 또는 `sqlutil.FindTopLevelKeyword` 및 `sqlutil.SplitTopLevel`을 활용하여 문자열 따옴표/괄호 안전 토크나이징으로 대체.
- `reExplain...` 정규식 삭제:
  - `proc.ExtractQueryCharacteristics` 및 `sqlutil.HasPrefixKeyword`를 활용하여 구조적 AST 플래그 기반으로 대체.

### 2.2. Phase 2: `sqlexec_ddl.go` 단순 DDL 라우팅 및 기본문 렉서 전환
- 대상: `DROP TABLE`, `TRUNCATE TABLE`, `CREATE VIEW`, `DROP VIEW`, `CREATE INDEX`, `DROP INDEX`, `REINDEX`, `DROP TRIGGER`, `CREATE/DROP STATISTICS`
- 구현 방식:
  - 정규식을 완전 폐기하고 `sqlutil.HasPrefixKeyword` 및 `proc.Lexer` 기반의 선형 토큰 스캐너(`sqlutil.SplitTopLevelTokens`, `cleanIdentifier`) 도입.
  - 대괄호(`[t]`), 백틱(`` `t` ``), 큰따옴표(`"t"`), 스키마 접두사(`dbo.t`)를 안전하게 처리하는 토큰 소비 루프 적용.

### 2.3. Phase 3: `sqlexec_ddl.go` 복합 DDL(`CREATE TABLE`, `ALTER TABLE`) 파서 정규식 완전 박멸
- 대상:
  - `reCreateTablePrefix`, `reOnDelete`, `reOnUpdate`, `reTableFK`, `reInlineFK`, `reColDefault`, `reGeneratedIdentity`
  - `reAlterTablePrefix`, `reAddColumn`, `reAlterAddUnique`, `reAlterAddFK`, `reAlterAddCheck`, `reAlterDropConstraint`, `reAlterDropColumn`, `reAlterColumnNull`, `reAlterAddPK`, `reAlterColDropDef`, `reAlterColSetDef`, `reAlterColType`, `reAlterRenameTable`, `reAlterRenameCol`
- 구현 방식:
  - `splitColumnDefs`를 괄호/따옴표 중첩 깊이(`parenDepth`, `inQuote`) 기반 토크나이저로 고도화하여 쉼표/공백이 리터럴 내에 있어도 정확히 분리.
  - 컬럼 정의 해석기(`parseColumnDefinitionTokens`): 토큰 스트림을 순차 전진하며 컬럼명, 데이터타입(길이/정밀도 괄호 소비), 제약조건(`PRIMARY KEY`, `NOT NULL`, `DEFAULT <expr>`, `IDENTITY`, `REFERENCES <tbl>(<col>)`)을 상태 머신으로 정확히 파싱.

---

## 3. 검증 전략 (3-Stage Verification)

1. **Stage 1 (Build)**:
   - `bin/lionsql`, `bin/bench`, `bin/tpcc-runtime` 컴파일 통과.
2. **Stage 2 (Test & Race Detector)**:
   - `pkg/engine/...`의 모든 DDL 테스트 (`identity_ddl_test.go`, `sqlexec_ddl_test.go`, 실전 앱 마이그레이션 호환성 테스트) 전수 PASS.
   - `go test -tags wiredtiger -race ./pkg/engine/...` 0 data race 검증.
3. **Stage 3 (Commit & Documentation)**:
   - `refactor(engine): eliminate legacy regular expressions in DDL and eval execution paths`
   - `TODO.md` 및 `CHANGELOG.md` 갱신.
