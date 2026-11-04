# Babelfish-Inspired T-SQL Stored Procedure Logic Test Suite Plan
> - **최종 갱신일**: 2026-11-04
> - **최초 작성일**: 2026-11-04
> - **상태**: Completed (100% Pass, 0 Data Race)
> - **목표**: SQLite Logic Test(SLT) 규격을 확장/특화하여 Babelfish for PostgreSQL의 T-SQL 테스트 스위트 패턴을 수용하는 독립 Stored Procedure 로직 테스트 스위트(`test/slt_proc/`)를 구축하고, 1.0 정식 릴리즈 전 T-SQL 프로시저 회귀를 0건으로 보장.

---

## 1. 배경 및 동기

1. **기존 SLT의 한계**:
   - `test/slt/`는 SQLite 공식 SQL Logic Test를 기반으로 하므로, ANSI SQL 표현식과 DDL/DML, 조인, 집계 함수 검증에는 탁월하지만 **Stored Procedure(변수, 제어문, 트랜잭션 경계, 예외 처리 등)**에 대한 테스트가 전무함.
2. **프로시저 회귀 위험**:
   - LionSQL은 T-SQL VDBE VM(`pkg/proc`)을 통해 고성능 프로시저 실행을 지원하고 있으나, 기존 테스트는 Go 단위 테스트(`*_test.go`) 내 하드코딩된 케이스에 집중되어 있어 선언적인 대규모 회귀 테스트 스위트가 부재함.
3. **AWS Babelfish T-SQL 스위트의 가치**:
   - Babelfish for PostgreSQL은 수년간 MS SQL Server 호환성을 입증한 방대한 T-SQL 테스트 자산을 보유하고 있음.
   - 이 패턴을 SLT 문법(`statement ok`, `query`, `----`)으로 정규화하면 **유지보수가 극히 쉽고 빠른 독립 테스트 스위트**를 구축할 수 있음.

---

## 2. 아키텍처 및 구현 설계

### 2.1 독립 디렉토리 구조
```
test/slt_proc/
├── slt_proc_test.go              # Go 테스트 러너 (독립 실행: go test -v ./test/slt_proc/...)
├── 01_variables_and_types.slt    # 로컬 변수(DECLARE/SET/SELECT), 스코프, 테이블 변수
├── 02_control_flow.slt           # IF...ELSE, WHILE, BREAK, CONTINUE, GOTO/라벨
├── 03_try_catch_error.slt        # BEGIN TRY...BEGIN CATCH, THROW, RAISERROR, ERROR_NUMBER()
├── 04_nested_transactions.slt    # BEGIN TRAN, COMMIT, ROLLBACK, @@TRANCOUNT 중첩
├── 05_cursors.slt                # DECLARE CURSOR, OPEN, FETCH NEXT, @@FETCH_STATUS, CLOSE, DEALLOCATE
├── 06_dynamic_sql.slt            # EXEC(@str), sp_executesql 파라미터 바인딩 및 OUTPUT
├── 07_return_codes.slt           # RETURN <code/expr> 및 중첩 조건절 조기 탈출
├── 08_table_variables.slt        # DECLARE @table TABLE(...) 및 세션 격리/DML
└── 09_goto_and_labels.slt        # GOTO 전방 건너뛰기 및 후방 루프 에뮬레이션
```

### 2.2 SLT 러너 호환성 점검 및 보완
- `pkg/slt` 파서는 기본적으로 빈 줄(`\n\n`)을 기준으로 문장을 분리함.
- `CREATE PROCEDURE ... AS BEGIN ... END` 블록 내부에 세미콜론이나 줄바꿈이 다수 포함되더라도 전체 블록을 단일 `statement ok` 단위로 안전하게 캡슐화할 수 있도록 보완.
- `EXEC proc_name [args]` 및 `SELECT`를 통해 프로시저 반환값과 테이블 변경 상태를 `query I / query T`로 정밀 비교.

### 2.3 3-Stage Pipeline 준수
- Build: 컴파일 무결성 검증
- Test: `go test -tags wiredtiger -v -race ./test/slt_proc/...` 100% 통과
- Commit: `feat(test): add babelfish-inspired t-sql stored procedure logic test suite`

---

## 3. 세부 테스트 커버리지 매트릭스

| 번호 | 테스트 스위트 파일 | 프로시저 | Statement | Query | 핵심 검증 T-SQL 시나리오 | Babelfish 참조 패턴 |
|:---:|:---|:---:|:---:|:---:|:---|:---|
| 1 | `01_variables_and_types.slt` | 3개 | 5개 | 3개 | `DECLARE @a INT = 10`, `SELECT @a = col`, `@tbl TABLE (...)` | Variable scoping, inline assign |
| 2 | `02_control_flow.slt` | 4개 | 6개 | 9개 | `IF...ELSE` 중첩, `WHILE` 조기 탈출, `BREAK`, `CONTINUE` | Loop branching, short-circuit |
| 3 | `03_try_catch_error.slt` | 2개 | 4개 | 4개 | `BEGIN TRY...CATCH`, `ERROR_NUMBER()`, `THROW 50001`, `RAISERROR` | Exception frame unwind |
| 4 | `04_nested_transactions.slt` | 2개 | 7개 | 3개 | 중첩 `BEGIN TRAN`, 부분 롤백 방지, 세이브포인트, `@@TRANCOUNT` | Transaction state stack |
| 5 | `05_cursors.slt` | 2개 | 4개 | 2개 | 커서 순회, `FETCH NEXT INTO`, `@@FETCH_STATUS = 0` 탈출 | Cursor lifecycle |
| 6 | `06_dynamic_sql.slt` | 2개 | 6개 | 4개 | `sp_executesql` 동적 SQL, 파라미터 매핑, OUTPUT 변수 회수 | Dynamic SQL execution |
| 7 | `07_return_codes.slt` | 2개 | 7개 | 2개 | `RETURN <val>`, 유효성 검증 실패 조기 탈출, 산술 반환 | Return code and early exit |
| 8 | `08_table_variables.slt` | 0개 | 4개 | 3개 | `@table TABLE(...)` 선언, 인메모리 DML (`INSERT/UPDATE/DELETE`), 세션 격리 | Table variable lifecycle |
| 9 | `09_goto_and_labels.slt` | 2개 | 4개 | 3개 | `GOTO label`, 전방 점프 구문 건너뛰기, 후방 점프 루프 에뮬레이션 | Unconditional branching |
| **합계** | **9개 파일** | **19개** | **47개** | **33개** | **총 80개 지시문 (9대 도메인 전수 검증)** | **Full Parity** |

---

## 4. 검증 결과 및 엔진 결함 보완 내역 (Verification & Fixes)

### 4.1 엔진 결함 보완: VDBE VM `OpPointSelect` 순수 변수 대입(Variable-Only Assign) 레코드셋 억제
- **현상**: `SELECT @bal = balance FROM acc WHERE id = @target;` 실행 시 내부 변수 `@bal`에는 값이 정상 대입되지만, 클라이언트로도 `[@bal: 200]` 레코드 1행이 반환되어 후속 `SELECT @bal;`과 합쳐져 결과 행 수가 2행으로 증가하는 현상 발생.
- **원인**: `pkg/proc/runtime/vm_dispatcher.go`의 `OpPointSelect` 핸들러가 프로젝션 목록에 `@var`만 존재하는 순수 변수 대입식일 때도 `f.rows`에 결과 행을 추가하고 있었음.
- **수정**: `data.Projections`의 모든 항목이 `@` 접두사를 가지는 변수 대입일 경우, 변수 슬롯만 갱신하고 `f.rows`, `f.columns`, `f.values`를 `nil` 및 `rowCount = 0`으로 정리하도록 보완 (`vm_dispatcher.go:1289`, `vm_dispatcher.go:1367`).

### 4.2 최종 3-Stage Pipeline 검증 결과
1. **Build**:
   - `bin/lionsql`, `bin/bench`, `bin/tpcc-runtime` 정상 컴파일 확인
2. **Test (`-race`)**:
   ```bash
   go test -tags wiredtiger -count=1 -race ./test/slt_proc/... ./pkg/proc/...
   ```
   - `lionsql/test/slt_proc`: **PASS (1.573s, 9개 파일 80개 지시문 100% 통과)**
   - `lionsql/pkg/proc/runtime`: **PASS (6.188s, 0 data race)**
   - `lionsql/pkg/proc/parser/antlr`: **PASS (8.208s)**
3. **결론**:
   - LionSQL 1.0 LTS 출항 전, T-SQL 절차적 프로시저 엔진 전반에 대한 선언적 블랙박스 회귀 검증망 완전 구축 완료.
