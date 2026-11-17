# 엔진 내부 조잡한 문자열 파싱 레거시 전수 제거 및 `pkg/proc` Pratt AST 전면 전환 계획서
(`ENGINE_AST_PARSER_MIGRATION_PLAN.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-11-16
> - **과제 식별 코드**: `REFACTOR-ENGINE-AST-PARSER-1` & `REFACTOR-TRANSACTIONAL-DDL-ISOLATION-1`
> - **대상 마일스톤**: `v0.94.0` (최우선 Priority 1 과제)
> - **핵심 목표**: `pkg/engine` 전역의 35+개 수동 문자열 슬라이싱/검색 레거시 함수를 전수 폐기하고, `pkg/proc`의 재귀 하강 Pratt 파서 AST로 전면 일원화
> - **연계 규칙**: `[RULE:RadicalKISS]`, `[RULE:ModularAbstraction]`, `[RULE:LexicalSafety]`, `[RULE:PipelineVerification]`, `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`
> - **연계 문서**: [`notes/plans/TRANSACTIONAL_DDL_MODULARIZATION_PLAN.ko.md`](TRANSACTIONAL_DDL_MODULARIZATION_PLAN.ko.md), [`notes/ENGINEERING_CONTEXT.ko.md`](../ENGINEERING_CONTEXT.ko.md)

---

## 1. 개요 및 배경 (Executive Summary)

LionSQL의 쿼리 실행 엔진(`pkg/engine`)은 프로시저 엔진(`pkg/proc`)과 독립적으로 발전해 오는 과정에서, DDL 및 일부 복합 DML 구문 파싱에 수동 `strings.Index`, `strings.Split`, `strings.TrimSpace` 기반의 임의 슬라이싱 휴리스틱을 대거 도입하였습니다.

이러한 조잡한 파싱은 다음과 같은 치명적인 한계를 발생시켰습니다:
1. **괄호 및 따옴표 중첩 파괴 (Context-Blind Slicing)**: 기본값 수식(`DEFAULT (CONCAT('A', ', B'))`), CHECK 제약조건, 복합 기본키 등에서 쉼표나 괄호가 포함될 때 잘못 잘려 구문 오류가 발생하거나 조건이 탈락됨.
2. **코드 중복 및 스파게티 결합**: `sqlexec_ddl.go`(2,300+ 라인) 하나에 파싱, 유효성 검사, 물리 파일 조작, 세션 Undo 롤백 스택 관리가 한데 엉켜 유지보수 불가능.
3. **트랜잭셔널 DDL 격리 실패**: 파싱과 실행이 분리되지 않아 미커밋 DDL 상태가 물리 스토리지에 즉시 반영되는 Dirty DDL Read 유발.

따라서 **"엔진 내부 35+개 조잡한 문자열 파싱 레거시 전수 제거"**와 **"DDL 트랜잭션 전담 모듈화(`txddl`)"**를 쌍둥이 최우선 과제로 삼아, 파싱 계층을 `pkg/proc` AST로 완전히 정규화하고 실행 계층을 `txddl`로 분리합니다.

---

## 2. 엔진 내부 수동 파싱 레거시 목록 (35+ 대상 조사)

### 2.1 `pkg/engine/sqlexec_ddl.go` 파싱 레거시
| 레거시 함수명 | 기존 방식의 문제점 | 전환 대상 `pkg/proc` AST / 파서 메서드 |
| :--- | :--- | :--- |
| `parseCreateTablePrefix` | `CREATE TABLE [IF NOT EXISTS] <table>` 수동 대소문자 탐색 | `proc.Parser.ParseCreateTable()` (`*CreateTableStmt`) |
| `parseTableWithClause` | `WITH (...)` 절을 콤마/이퀄로 단순 슬라이싱하여 중첩 옵션 무시 | `proc.Parser.parseTableOptions()` (`TableOption` AST) |
| `splitColumnDefs` | 괄호 카운팅 기반 수동 콤마 분리 (따옴표 내 콤마 취약) | `proc.Parser.parseColumnDefList()` (`[]ColumnDef`) |
| `parseColumnDefinition` | 컬럼명, 타입, 제약조건을 공백 분리로 임의 파싱 | `proc.Parser.parseColumnDef()` (`ColumnDef` AST) |
| `parseColumnDefault` | `DEFAULT` 키워드 이후를 수동 슬라이싱 (복합 수식 파싱 불가) | `proc.Parser.parseExpression()` (`Expression` AST) |
| `parseForeignKeyClause` | `FOREIGN KEY ... REFERENCES ...` 수동 문자열 패턴 매칭 | `proc.Parser.parseForeignKeyConstraint()` |
| `parseCheckConstraint` | `CHECK (...)` 괄호 수동 추출 | `proc.Parser.parseCheckConstraint()` |
| `parseDecimalPrecisionScale` | `DECIMAL(p, s)` 문자열 자르기 | `proc.Parser.parseTypeSpec()` |
| `parseAlterTablePrefix` | `ALTER TABLE <table> [ADD/DROP/ALTER]` 수동 토큰 분리 | `proc.Parser.ParseAlterTable()` (`*AlterTableStmt`) |
| `parseDropTablePrefix` | `DROP TABLE [IF EXISTS] <t1>, <t2>` 단순 콤마 분리 | `proc.Parser.ParseDropTable()` (`*DropTableStmt`) |
| `parseCreateIndexPrefix` | `CREATE [UNIQUE] INDEX <idx> ON <tbl> (<cols>)` 수동 분리 | `proc.Parser.ParseCreateIndex()` (`*CreateIndexStmt`) |
| `parseDropIndexPrefix` | `DROP INDEX [IF EXISTS] <idx> ON <tbl>` 수동 분리 | `proc.Parser.ParseDropIndex()` (`*DropIndexStmt`) |
| `parseTruncateTablePrefix` | `TRUNCATE TABLE <tbl>` 수동 자르기 | `proc.Parser.ParseTruncate()` (`*TruncateStmt`) |

### 2.2 `pkg/engine/` 기타 실행 모듈 파싱 레거시
| 모듈 파일 | 레거시 함수 / 로직 | 전환 대상 `pkg/proc` / `pkg/sqlutil` |
| :--- | :--- | :--- |
| `sqlexec_cursor.go` | `DECLARE <name> CURSOR FOR <select>` 수동 분리 | `proc.Parser.ParseDeclareCursor()` |
| `dml_parser.go` | `INSERT INTO ... VALUES / SELECT` 수동 슬라이싱 | `proc.Parser.ParseInsert()` (`*InsertStmt`) |
| `sqlexec_conditional_ddl.go` | `IF NOT EXISTS (SELECT ...) CREATE TABLE ...` 분리 | `proc.Parser.ParseIfStmt()` |
| `sqlexec_derived_values.go` | `(VALUES (...), (...)) AS t(c1, c2)` 수동 추출 | `proc.Parser.ParseValuesTable()` |
| `query_utils.go` | `FindWhereClause`, `FindOrderByClause` 수동 탐색 | `proc.Parser` AST 노드 필드 접근 |

---

## 3. 이중 파이프라인 협력 아키텍처 (Parsing ➔ Isolation)

```
[SQL Input Batch]
       │
       ▼
┌────────────────────────────────────────────────────────┐
│ Phase 1: pkg/proc Lexer & Pratt Recursive-Descent AST  │
│ - Zero-Allocation Lexer Tokens                         │
│ - Strict Grammar Validation (CREATE/ALTER/DROP/INDEX)  │
│ - Typed AST Generation (e.g. *CreateTableStmt)         │
└────────────────────────────────────────────────────────┘
       │
       ▼
┌────────────────────────────────────────────────────────┐
│ Phase 2: pkg/engine/sqlexec_ddl (Lightweight Dispatch) │
│ - Semantic Validation against Catalog Metadata         │
│ - Conversion of AST to storage.TableSchema / IndexDef  │
└────────────────────────────────────────────────────────┘
       │
       ▼
┌────────────────────────────────────────────────────────┐
│ Phase 3: pkg/catalog/txddl (Transactional DDL Manager) │
│ - In-Tx: Apply Schema Changes to ShadowCatalog (CoW)   │
│ - In-Tx: Register Drops to DeferredActionQueue         │
│ - Zero Dirty DDL Read for Concurrent Sessions          │
│ - On Commit: Atomic Catalog Swap + Deferred WT Drops   │
│ - On Rollback: Discard Shadow Overlay in O(1) Time     │
└────────────────────────────────────────────────────────┘
```

---

## 4. 단계별 실행 계획 (Execution Phases)

### Phase 0: `pkg/proc/lexer.go` 구조적 정규화 및 캡슐화 리팩토링 (선행 기반 정비 — 완료 `[x]`)
- [x] **전진 불변성(Forward-Progress Invariant) 확립**: `NextToken()`의 거대 `switch` 문에서 일부 분기는 조기 `return`하고 일부 분기는 함수 하단의 `l.readChar()`로 떨어지는(Fallthrough) 비일관적 구조를 전면 개편. 모든 토큰 리더 헬퍼(`readDollarToken`, `readBangComparison`, `readDelimitedIdentifier`, `consumeRawDotChain`)가 자신의 소비 범위를 명확히 책임지고, 미인식/단일 문자 토큰도 100% 포인터를 전진시키도록 구조적으로 보장하여 무한 루프 가능성을 원천 차단.
- [x] **내부 상태 캡슐화 강화 (`l.ch`, `l.readChar()` 외부 노출 차단)**: `pkg/proc/characteristics.go`(`readTableIdentifier`) 등 외부 파일에서 Lexer 내부 필드(`l.ch`)를 직접 읽거나 `l.readChar()`를 수동 호출하는 캡슐화 위반을 제거하고, `TestArch_LexerEncapsulation`(`go/ast` 정적 가드)을 통해 `lexer.go` 외부 접근을 영구 봉쇄.

### Phase 1: `pkg/proc` DDL AST 및 파서 확장 (완료 `[x]`)
- [x] `pkg/proc/ast.go`: `CreateTableStmt`, `AlterTableStmt` (`AlterTableAction`), `DropTableStmt`, `CreateIndexStmt`, `DropIndexStmt`, `TruncateStmt`, `CreateViewStmt`, `DropViewStmt`, `CreateStatisticsStmt`, `DropStatisticsStmt`, `ReindexStmt` AST 완전 정의.
- [x] `pkg/proc/parser_ddl.go`: 모든 DDL 문법의 정식 재귀 하강 파싱 구현 및 100% 토큰 단위 검증 (`CleanDefaultExpr`, 복합 `ALTER TABLE`, 인라인/테이블 레벨 제약조건, `WITH` 및 `PARTITION BY` 옵션 포함).
- [x] 단위 테스트: 복합 제약조건, 따옴표 내 특수문자, `WITH (...)` 옵션 등을 포함한 회귀 테스트 작성 (`pkg/proc/ddl_ast_test.go`).

### Phase 2: `sqlexec_ddl.go` 전면 리팩토링 (레거시 파싱 폐기 — 완료 `[x]`)
- [x] `sqlexec_ddl.go` 내의 10개 수동 슬라이싱 헬퍼 함수(`parseColumnDefault`, `parseColumnCheckExpr`, `parseDecimalPrecisionScale`, `parseCreateTablePrefix`, `parseAlterTablePrefix`, `parseTruncateClause`, `parseSQLDataType`, `splitColumnDefs`, `cleanDefaultExpr`, `parseForeignKeyClause`) 완전 삭제 (`[RULE:RadicalKISS]`, 약 900줄 감소).
- [x] `execCreateTable`, `execAlterTable`, `execDropTable`, `execTruncateTable`, `execCreateIndex`, `execDropIndex`, `execDropView`, `execCreateStatistics`, `execDropStatistics`, `execReindex`가 `proc.Parser`를 호출하여 AST를 직접 수신하도록 전환.

### Phase 2.5 (`ARCH-AST-GUARD-1`): `go/ast` 정적 분석 아키텍처 가드를 통한 수동 문자열 파싱 재유입 영구 차단 (완료 `[x]`)
- [x] `pkg/engine/arch_guard_test.go` (`TestArch_NoManualSQLParsingInDDL`) 신설.
- [x] `pkg/engine/sqlexec_ddl.go` 프로덕션 코드의 Go AST를 스캔하여 금지된 `strings.*` 슬라이싱 함수(`strings.Index`, `strings.LastIndex`, `strings.Split`, `strings.SplitN`, `strings.Fields`), `regexp` import, 및 로컬 `func parse*` / `func split*` 수동 파서 정의가 재유입되면 즉시 `go test`를 실패시켜 Invariant 12를 물리적으로 강제.

### Phase 3: `txddl` 트랜잭션 격리 계층 연동
- `pkg/catalog/txddl` 구현 (`ShadowCatalog` + `DeferredActionQueue`).
- `sqlexec_ddl.go`의 수동 톰스톤 리네임 및 언도 스택 조작 로직을 `txddl.Manager` 호출로 대체.
- Dirty DDL Read 방지 및 동시성 격리 테스트 작성 (`coordinator_concurrency_regression_test.go`).

### Phase 4: 기타 엔진 모듈(`sqlexec_cursor`, `sqlexec_conditional_ddl`) 정규화 (완료 `[x]`)
- [x] `sqlexec_cursor.go`, `sqlexec_conditional_ddl.go`의 수동 문자열 분리 로직(`parseCursorParams`, `strings.Fields`, `strings.Index` 기반 주석 스킵)을 `pkg/proc` 파서(`ParseServerCursorDeclare`, `ParseServerCursorFetchOrMove`, `ParseServerCursorOpenOrClose`, `proc.NewLexer`) 및 `pkg/sqlutil`로 이관하고 `TestArch_NoManualSQLParsingInDDL` 정적 가드 감시 대상에 편입 완료.

---

## 5. 검증 기준 (Verification Criteria)
1. **컴파일 및 무결성**: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql` 무경고 빌드.
2. **테스트 100% PASS**: `go test -v -race ./pkg/proc ./pkg/catalog ./pkg/engine` 데이터 레이스 0건 및 100% 성공.
3. **오라클 불변성 유지**: `[RULE:ZeroOracleTampering]` 준수 (기존 SLT 및 T-SQL 회귀 테스트 전원 통과).
4. **코드 라인 감소**: `sqlexec_ddl.go`가 2,300줄에서 800줄 이하의 간결한 디스패처로 축소.
