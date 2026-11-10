# T-SQL 테이블 변수(`DECLARE @tbl TABLE`) 지원 구현 계획 (TABLE_VARIABLE_SUPPORT_PLAN)

> - **최초 작성일**: 2026-11-10
> - **상태**: 계획 수립 및 구현 진행 (In Progress)
> - **대상 버전**: v0.93.0 / v1.0 GA
> - **관련 이슈 / 태스크**: `FEAT-TABLE-VAR-1` (`ARCH-LOGICAL-NAMESPACE-1` 연계)

---

## 1. 개요 및 배경

T-SQL(Microsoft SQL Server)에서 **테이블 변수(`Table Variable`)**는 임시 데이터셋을 로컬 변수 형태로 선언하여 활용하는 필수적인 객체입니다:
```sql
DECLARE @items TABLE (
    id INT PRIMARY KEY,
    name VARCHAR(50),
    price DECIMAL(10, 2)
);

INSERT INTO @items VALUES (1, 'Notebook', 1200.00);
INSERT INTO @items (id, name, price) SELECT id, name, price FROM products WHERE active = 1;

SELECT * FROM @items WHERE price >= 1000.00;
UPDATE @items SET price = price * 0.9 WHERE id = 1;
DELETE FROM @items WHERE id = 1;
```

### 현황 및 문제점
1. **파서 레벨 무시**: 현재 `pkg/proc/parser_stmt.go`에서 `DECLARE @var TABLE (...)` 구문 파싱 시 괄호 내부를 무시하고 더미 `VARCHAR` 타입으로 파싱하여 실제 테이블 생성이 이루어지지 않았습니다.
2. **DML 파서의 `@var` 거부**: `pkg/proc/parser_dml.go`의 `INSERT INTO`, `SELECT ... FROM`, `UPDATE`, `DELETE`, `JOIN` 구문에서 테이블명 토큰으로 `TokenIdent`만 허용하고 `TokenVariable`(`@var`)을 허용하지 않아 `expected table name in INSERT, got @items` 구문 에러가 발생했습니다.
3. **배치 및 프로시저 VM 라이프사이클 미연동**: 세션 스크립트 실행 및 VDBE 바이트코드 VM 내부에서 프로시저/배치 종료 시 테이블 변수를 자동으로 파기(Cleanup)하는 수명주기 관리가 일원화되지 않았습니다.

---

## 2. 아키텍처 및 구현 설계

### 2.1 3-Layer 통합 아키텍처

```
+-------------------------------------------------------------------------------+
|  1. 파서 계층 (pkg/proc)                                                       |
|     - parser_stmt.go: DECLARE @var TABLE (colDefs) -> DeclareTableStmt 생성   |
|     - parser_dml.go: FROM/JOIN/INSERT/UPDATE/DELETE에 TokenVariable 허용      |
+-------------------------------------------------------------------------------+
                                    |
                                    v
+-------------------------------------------------------------------------------+
|  2. 세션/배치 리라이팅 계층 (pkg/engine)                                       |
|     - sqlexec.go: ClientSession.TableVars에 @tbl -> __tv_<sessID>_<name> 매핑 |
|     - 배치 쿼리 실행 전 @tbl 식별자를 __tv 물리 테이블로 자동 치환             |
|     - 세션 종료 / DISCARD ALL / 배치 완료 시 동적 DropTable 자동 회수          |
+-------------------------------------------------------------------------------+
                                    |
                                    v
+-------------------------------------------------------------------------------+
|  3. VDBE VM 런타임 계층 (pkg/proc/runtime)                                     |
|     - VMFrame / ExecutionContext 레벨 tableVars 격리                          |
|     - OpDeclareTable 바이트코드 오프코드 또는 프로시저 초기화 시 동적 생성     |
|     - 프로시저 프레임 종료(Frame.Close) 시 자동 드롭으로 무누수 보장          |
+-------------------------------------------------------------------------------+
```

---

## 3. 세부 구현 계획

### Step 1: AST 및 DDL/DML 파서 확장 (`pkg/proc`)
1. **AST 구조체 신설 (`pkg/proc/ast.go`)**:
   ```go
   type DeclareTableStmt struct {
       Name       string           // e.g. "@items"
       Columns    []storage.Column // 파싱된 컬럼 목록
       RawColDefs string           // e.g. "id INT PRIMARY KEY, name VARCHAR(50)"
   }
   ```
2. **`parseDeclare()` 파서 보강 (`pkg/proc/parser_stmt.go`)**:
   - `DECLARE @name TABLE (` 감지 시 컬럼 정의부(컬럼명, 데이터타입, PK, NULL/NOT NULL)를 파싱하여 `DeclareTableStmt` 생성.
3. **DML 파서 `TokenVariable` 허용 (`pkg/proc/parser_dml.go`)**:
   - `parseGeneralSelectClauses` (FROM 및 JOIN): `TokenIdent` 외에 `TokenVariable` 허용.
   - `parseInsert` & `parseGeneralInsert`: `INSERT INTO @var` 허용.
   - `parseUpdate`: `UPDATE @var` 허용.
   - `parseDelete`: `DELETE FROM @var` 허용.

### Step 2: 쿼리 코디네이터 및 세션 계층 연동 (`pkg/engine`)
1. **`parseTableVarDecl` 정밀화 (`pkg/engine/sqlexec.go`)**:
   - 단일 구문 및 복합 배치에서 `DECLARE @var TABLE (...)` 완전 인식.
   - `__tv_<sessID>_<varName>` 물리 테이블 생성 및 `cs.TableVars` 등록.
2. **`substituteParamOutsideQuotes` 치환 확장**:
   - FROM, JOIN, INSERT INTO, UPDATE, DELETE 구문의 `@var` 테이블 식별자를 `[__tv_...]`로 안전하게 치환.
3. **자원 해제 및 무누수 보장 ([`RULE:CleanTeardown`])**:
   - 배치 완료 및 세션 닫힘 시 등록된 테이블 변수를 `DropTable`하여 메모리/스토리지 누수 방지.

### Step 3: VDBE 바이트코드 VM 연동 (`pkg/proc/runtime`)
1. Stored Procedure 내부 실행 시 `DeclareTableStmt` 컴파일 및 바이트코드 실행 지원.
2. 프로시저 로컬 프레임(`VMFrame`)에서 프로시저 종료 시 임시 테이블 자동 드롭.

### Step 4: 검증 및 테스트
1. `pkg/proc/parser_test.go`: `DECLARE @tbl TABLE (...)` 파싱 및 DML 구문 파싱 단위 테스트.
2. `pkg/engine/table_variable_test.go`:
   - `DECLARE @tbl TABLE (...)` 선언 후 `INSERT`, `SELECT`, `UPDATE`, `DELETE` 전체 사이클 검증.
   - 서브쿼리 및 JOIN(`users JOIN @tbl`) 연동 검증.
   - 세션 격리(동일한 이름의 `@tbl`을 서로 다른 세션에서 독립적으로 사용) 검증.
   - 세션 종료 후 물리 테이블 완전 회수 검증.
3. 3단계 검증 파이프라인 (Build ➔ Test ➔ Commit).

---

## 4. 기대 효과 및 호환성 향상
- T-SQL 호환성 매트릭스([`notes/TSQL_COMPATIBILITY_MATRIX.ko.md`](../TSQL_COMPATIBILITY_MATRIX.ko.md))에서 테이블 변수(`@tbl`)를 **"완전 지원"**으로 승격.
- 복잡한 ETL 프로시저, 계산 중간 결과 버퍼링, 복수 행 처리 배치 스크립트의 100% 네이티브 호환성 달성.
