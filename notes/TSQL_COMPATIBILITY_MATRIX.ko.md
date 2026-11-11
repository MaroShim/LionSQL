# LionSQL T-SQL 호환성 전수 매트릭스 (`TSQL_COMPATIBILITY_MATRIX.ko.md`)

> - **최초 작성일**: 2026-11-10
> - **최종 갱신일**: 2026-11-11
> - **상태**: 활성 참조 및 지속 동기화 (`COMPAT-TSQL-MATRIX-1` `IN_PROGRESS`)
> - **기준 버전**: Microsoft SQL Server 2019 / 2022 호환성 기준
> - **참조 소스**: `pkg/proc/parser/antlr/TSqlParser.g4`, `pkg/proc/lexer.go`, `pkg/proc/builtins_*.go`, `pkg/proc/compiler.go`, `pkg/engine/sqlexec_*.go`, `TASKS.md`

---

## 1. 개요 및 분석 목적

LionSQL은 엔터프라이즈 마이크로소프트 SQL Server(T-SQL)와의 원활한 상호운용성을 제공하기 위해 Native Pratt 파서(Tier 1) 및 ANTLR4 전체 문법 파서(Tier 2), 그리고 VDBE 가상 머신 기반의 스토어드 프로시저 실행기를 내장하고 있습니다.

본 문서는 Microsoft T-SQL의 전 영역(연산자, 제어 흐름문, 커서, 내장 함수, 시스템 변수, 트랜잭션 옵션, 고급 쿼리 기능)을 전수 조사하여 다음 4가지 상태로 분류한 **호환성 전수 단일 진실 공급원(SSOT)**입니다:
1. **`[완전 지원 (Supported)]`**: Native 파서와 VDBE VM/실행기에서 100% 동작 및 테스트 검증 완료.
2. **`[부분 지원 (Partial)]`**: 파싱은 가능하나 일부 옵션이 누락되었거나 가상 No-op으로 처리되는 영역.
3. **`[미구현 (Candidate)]`**: SQL Server의 표준 기능이나 현재 LionSQL에 미구현되어 향후 구현 논의가 필요한 대상.
4. **`[영구 Spec-Out]`**: LionSQL의 아키텍처 원칙(단일 바이너리, 서브마이크로초 지연시간, KISS)에 따라 의도적으로 제외된 기능.

---

## 2. 연산자 (Operators) 호환성 매트릭스

| 연산자 분류 | T-SQL 연산자 | 상태 | LionSQL 현황 및 구현 세부사항 |
| :--- | :--- | :--- | :--- |
| **산술 연산자** | `+`, `-`, `*`, `/`, `%` | **완전 지원** | 정수/부동소수점 오버플로우 방어 및 0 나누기 에러 처리 |
| **비트 연산자** | `&`, `\|`, `^`, `~` | **완전 지원** | 비트 AND, OR, XOR, NOT VDBE Opcode(`OpBitAnd`, `OpBitOr`, `OpBitXor`, `OpBitNot`) 지원 |
| **비트 시프트 함수** | `LEFT_SHIFT`, `RIGHT_SHIFT`, `BIT_COUNT`, `GET_BIT`, `SET_BIT` | **완전 지원** | SQL Server 2022 신규 비트 스칼라 함수 표준 지원 |
| **비교 연산자** | `=`, `<>`, `!=`, `<`, `<=`, `>`, `>=` | **완전 지원** | 인덱스 범위 탐색 및 VM 분기 점프 완전 연동 |
| **레거시 비교** | `!<`, `!>` | **완전 지원** | `!<`(`>=`) 및 `!>`(`<=`) 파스 타임 Desugaring 및 공백 허용(`! <`, `! >`, `! =`, BABEL-3379/4114) 완전 지원 |
| **복합 대입 연산자** | `+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `^=`, `\|=` | **완전 지원** | `SET @i += 1` 변수 대입 및 `UPDATE tbl SET col += 5` 테이블 갱신 파스 타임 Desugaring 및 VDBE 연산 완전 지원 |
| **문자열 연결** | `+` | **완전 지원** | 피연산자 중 하나가 문자열일 경우 암묵적 문자열 결합 처리 |
| **논리 연산자** | `AND`, `OR`, `NOT` | **완전 지원** | 단락 평가(Short-circuit evaluation) 지원 |
| **집합/소속 연산자** | `IN (...)`, `BETWEEN ... AND ...`, `LIKE`, `EXISTS` | **완전 지원** | 인덱스 스캔 및 서브쿼리 구체화 지원 |
| **정량 연산자** | `ALL`, `ANY`, `SOME` | **완전 지원** | `col op (ALL\|ANY\|SOME) (subquery\|list)` 파서, VDBE VM, 술어 리라이터, 공집합 공허 참(Vacuous Truth) 완전 지원 |
| **범위/형변환** | `::` | **완전 지원** | PostgreSQL 스타일 캐스팅 및 JSON 경로 탐색 지원 |

---

## 3. 프로시저 제어 구문 및 실행 흐름 (Control Flow)

| 기능 분류 | T-SQL 문법 | 상태 | LionSQL 현황 및 세부사항 |
| :--- | :--- | :--- | :--- |
| **변수 선언** | `DECLARE @x INT [= default];` | **완전 지원** | 초기값 선언 및 다중 변수 선언 지원 |
| **변수 대입** | `SET @x = expr;` | **완전 지원** | 단일 표현식 대입 지원 |
| **SELECT 대입** | `SELECT @x = col FROM tbl;` | **완전 지원** | 쿼리 결과 변수 바인딩 지원 |
| **조건 분기** | `IF ... ELSE ...` | **완전 지원** | 중첩 IF/ELSE 블록 및 VM 조건 점프(`OpJumpIfFalse`) 지원 |
| **반복 제어** | `WHILE ... BEGIN ... BREAK ... CONTINUE ... END` | **완전 지원** | 루프 탈출 및 다음 루프 점프 완전 지원 |
| **블록 래핑** | `BEGIN ... END` | **완전 지원** | 프로시저 및 익명 블록 라우팅 지원 (M3에서 BEGIN TRAN 오인식 해소) |
| **예외 처리** | `BEGIN TRY ... END TRY BEGIN CATCH ... END CATCH` | **완전 지원** | 런타임 VM 에러 트랩 및 CATCH 블록 점프 핸들러 지원 |
| **에러 발생** | `THROW [num, msg, state]` | **완전 지원** | 사용자 정의 에러 또는 활성 에러 리스로잉 지원 |
| **레거시 에러** | `RAISERROR(msg, severity, state)` | **완전 지원** | 매개변수 포맷팅 및 심각도 전파 지원 |
| **무조건 분기** | `GOTO label; label:` | **완전 지원** | 전방/후방 라벨 심볼 테이블 바인딩 및 점프 지원 |
| **반환문** | `RETURN [expr];` | **완전 지원** | 프로시저 정수 종료 코드 반환 및 호출자 전달 지원 |
| **동적 SQL** | `EXEC sp_executesql @stmt, @params, ...` | **완전 지원** | 매개변수화된 동적 SQL, OUTPUT 매개변수, 세션 격리 완전 지원 |
| **문자열 실행** | `EXEC(@sql)` | **완전 지원** | 문자열 직접 실행 경로 지원 |
| **시간 지연** | `WAITFOR DELAY '00:00:01' / WAITFOR TIME '...'` | **완전 지원** | `hh:mm:ss[.fff]` 지연 및 지정 시각 대기 지원, VDBE `OpWaitfor` 및 컨텍스트 협조적 중단 연동 |
| **OUTPUT 매개변수** | `CREATE PROC ... (@out INT OUTPUT)` | **완전 지원** | 프로시저 호출 후 호출자 변수로 값 역전파(Back-propagation) 지원 |

---

## 4. 커서 (Cursors) 수명주기

| 커서 명령 | T-SQL 문법 | 상태 | LionSQL 현황 및 세부사항 |
| :--- | :--- | :--- | :--- |
| **커서 선언** | `DECLARE cur CURSOR [FORWARD_ONLY] FOR SELECT ...` | **완전 지원** | `FORWARD_ONLY` 전방 전용 스트리밍 커서 지원 |
| **커서 열기** | `OPEN cur;` | **완전 지원** | 쿼리 실행 및 결과셋 메모리 버퍼/슬롯 바인딩 |
| **행 인출** | `FETCH NEXT FROM cur INTO @v1, @v2;` | **완전 지원** | 다음 행 인출 및 대상 변수 매핑 지원 |
| **커서 닫기** | `CLOSE cur;` | **완전 지원** | 슬롯 초기화 (`OPEN` 재사용 가능, M3 자동 닫기 완료) |
| **커서 해제** | `DEALLOCATE cur;` | **완전 지원** | 커서 정의 및 세션 레지스터 완전 소거 |
| **인출 상태** | `@@FETCH_STATUS` | **완전 지원** | 0(성공), -1(EOF), -2(누락) 표준 반환 |
| **커서 행수** | `@@CURSOR_ROWS` | **완전 지원** | 열린 커서의 적격 행 수 반환, 커서 닫힘 시 0 반환 |
| **스크롤 커서** | `FETCH PRIOR`, `FIRST`, `LAST`, `ABSOLUTE`, `RELATIVE` | **영구 Spec-Out** | 양방향 스크롤 커서는 버퍼 부하로 인해 `FORWARD_ONLY`만 지원 (ADR 합의 완료) |

---

## 5. 변수, 임시 테이블, 테이블 타입 및 논리 네임스페이스

| 개체 / 컨텍스트 분류 | T-SQL 문법 | 상태 | LionSQL 현황 및 세부사항 |
| :--- | :--- | :--- | :--- |
| **로컬 변수** | `DECLARE @x INT, @name VARCHAR(50);` | **완전 지원** | 모든 기본 SQL 데이터 타입 매핑 지원 |
| **세션 임시 테이블** | `CREATE TABLE #temp (id INT, val VARCHAR(50));` | **완전 지원** | 세션 전용 네임스페이스 격리(`__temp_<sessionID>_<name>`), `SELECT ... INTO #temp` 지원 (`PERF-HYBRID-TEMPMEM-1` 인메모리 바이패스 `DONE`) |
| **전역 임시 테이블** | `CREATE TABLE ##global_temp (...)` | **부분 지원** | 세션 간 공유되나 `##global` 전용 전역 수명주기 및 크로스 DB 격리(`__gtemp_`) 정합성 점검 필요 |
| **테이블 변수** | `DECLARE @tbl TABLE (id INT, val VARCHAR(50));` | **완전 지원** | 세션 배치, 익명 블록, 저장 프로시저 완벽 지원(`__tv_*`), `INSERT`/`SELECT`/`UPDATE`/`DELETE`/`JOIN` 및 자동 정리 (`PERF-HYBRID-TEMPMEM-1` 인메모리 바이패스 `DONE`) |
| **DB 컨텍스트 전환** | `USE <dbname>;` | **부분 지원 (가상 No-op)** | `DB_NAME()` 및 TDS `0xE3 ENVCHANGE` 토큰은 전환되나, 현재 단일 카탈로그 공유 (`__db_<dbname>_*` 논리 격리 및 3-Part 크로스 DB 조인은 `ARCH-LOGICAL-NAMESPACE-1` `PENDING`) |
| **사용자 정의 테이블 타입** | `CREATE TYPE MyTableType AS TABLE (...)` | **미구현 (후보)** | Stored Procedure에 테이블을 매개변수로 넘기는 TVP 전제 조건 |
| **테이블 반환 함수 (TVF)** | `CREATE FUNCTION fn() RETURNS TABLE AS RETURN ...` | **부분 지원 (인라인 TVF)** | 인라인 TVF(`RETURNS TABLE AS RETURN (SELECT ...)`) 등록 및 호출 지원 완료 (`pkg/engine/sqlexec_ddl.go`); 멀티-스테이트먼트 TVF는 미구현 |

---

## 6. 내장 함수 (Built-in Functions) 호환성

### 6.1 문자열 함수 (String Functions)
- **완전 지원 (36개)**: `LEN`, `DATALENGTH`, `SUBSTRING`, `CHARINDEX`, `PATINDEX`, `REPLACE`, `REVERSE`, `LEFT`, `RIGHT`, `UPPER`, `LOWER`, `LTRIM`, `RTRIM`, `TRIM`, `CONCAT`, `CONCAT_WS`, `STUFF`, `SPACE`, `REPLICATE`, `PARSENAME`, `STRING_ESCAPE`, `FORMATMESSAGE`, `ASCII`, `CHAR`, `UNICODE`, `NCHAR`, `SOUNDEX`, `DIFFERENCE`, `TRANSLATE`, `FORMAT`, `LPAD`, `RPAD`, `INITCAP`, `TO_CHAR`, `SPLIT_PART`, `STRING_SPLIT` (`enable_ordinal`, `@var` 매개변수, `CROSS APPLY` 포함)
- **미구현 / 부분 지원 (후보)**:
  - `TRIM(characters FROM string)` (SQL Server 2022): `TRIM(cutset, target)` 2인자 컷셋 트리밍 내장 함수는 지원하나, `FROM` 키워드 구문 파싱은 보강 필요
  - `COMPRESS(string)` / `DECOMPRESS(binary)`: Gzip 압축/해제 바이너리 함수

### 6.2 날짜 및 시간 함수 (Date & Time Functions)
- **완전 지원 (28개)**: `GETDATE`, `GETUTCDATE`, `SYSDATETIME`, `SYSUTCDATETIME`, `SYSDATETIMEOFFSET`, `YEAR`, `MONTH`, `DAY`, `DATEPART`, `DATENAME`, `DATEDIFF`, `DATEDIFF_BIG`, `DATEADD`, `EOMONTH`, `ISDATE`, `DATEFROMPARTS`, `TIMEFROMPARTS`, `DATETIMEFROMPARTS`, `SMALLDATETIMEFROMPARTS`, `DATETRUNC`, `DATE_BUCKET`, `SWITCHOFFSET`, `TODATETIMEOFFSET`, `CURRENT_TIMESTAMP`, `NOW`, `CURDATE`, `CURTIME`
- **미구현 (후보)**:
  - `CURRENT_TIMEZONE()`, `CURRENT_TIMEZONE_ID()` (SQL Server 2022)
  - `AT TIME ZONE` 시간대 변환 절

### 6.3 수치 및 수학 함수 (Mathematical Functions)
- **완전 지원 (28개)**: `ABS`, `ROUND`, `CEILING`, `FLOOR`, `POWER`, `SQRT`, `SQUARE`, `SIGN`, `PI`, `EXP`, `LOG`, `LOG10`, `SIN`, `COS`, `TAN`, `COT`, `ASIN`, `ACOS`, `ATAN`, `ATN2`, `DEGREES`, `RADIANS`, `RAND`, `MOD`, `TRUNC`, `BIT_COUNT`, `GET_BIT`, `SET_BIT`
- **미구현**: 복소수 및 특수 통계 전용 함수

### 6.4 조건 및 형변환 함수 (Conditional & Conversion)
- **완전 지원 (12개)**: `ISNULL`, `COALESCE`, `NULLIF`, `IIF`, `CHOOSE`, `CAST`, `CONVERT`, `TRY_CAST`, `TRY_CONVERT`, `CASE WHEN`, `GREATEST`, `LEAST`, `ISNUMERIC`
- **미구현 (후보)**:
  - `PARSE(string AS data_type [USING culture])`, `TRY_PARSE`

### 6.5 시스템 메타데이터 및 에러 함수
- **완전 지원 (38개)**: `DB_NAME`, `DB_ID`, `SCHEMA_NAME`, `SCHEMA_ID`, `OBJECT_ID`, `OBJECT_NAME`, `OBJECT_SCHEMA_NAME`, `COL_NAME`, `COL_LENGTH`, `TYPE_ID`, `TYPE_NAME`, `SERVERPROPERTY`, `CONNECTIONPROPERTY`, `COLLATIONPROPERTY`, `DATABASEPROPERTYEX`, `OBJECTPROPERTY`, `COLUMNPROPERTY`, `INDEXPROPERTY`, `FILEPROPERTY`, `FILEGROUPPROPERTY`, `FULLTEXTSERVICEPROPERTY`, `IDENT_CURRENT`, `SCOPE_IDENTITY`, `HOST_NAME`, `HOST_ID`, `CURRENT_USER`, `USER_NAME`, `SUSER_NAME`, `SUSER_SNAME`, `USER_ID`, `SUSER_ID`, `ORIGINAL_LOGIN`, `SYSTEM_USER`, `APP_NAME`, `HASHBYTES`, `CHECKSUM`, `BINARY_CHECKSUM`, `NEWID`, `NEWID7`
- **에러 컨텍스트 함수 (완전 지원)**: `ERROR_NUMBER()`, `ERROR_MESSAGE()`, `ERROR_LINE()`, `ERROR_SEVERITY()`, `ERROR_STATE()`, `ERROR_PROCEDURE()`

---

## 7. 특수 전역 세션 변수 (Special System Variables)

| 시스템 변수 | 지원 여부 | 동작 상세 |
| :--- | :--- | :--- |
| `@@ROWCOUNT` | **완전 지원** | 직전 실행된 DML/DQL 문장의 영향 받은 행 수 반환 |
| `@@ERROR` | **완전 지원** | 직전 실행 에러 번호 (성공 시 0) 반환 |
| `@@TRANCOUNT` | **완전 지원** | 현재 세션의 중첩 활성 트랜잭션 수(Depth) 반환 |
| `@@FETCH_STATUS`| **완전 지원** | 커서 FETCH 결과 (0, -1, -2) |
| `@@SPID` | **완전 지원** | 현재 클라이언트 연결 세션 ID |
| `@@SERVERNAME` | **완전 지원** | 현재 LionSQL 인스턴스 서버명 |
| `@@VERSION` | **완전 지원** | Microsoft SQL Server 호환 빌드 버전 문자열 |
| `@@IDENTITY` | **부분 지원** | 현재 `SCOPE_IDENTITY()`와 동일하게 동작 (트리거 발생 시 스코프 분리 미구현) |
| `@@CURSOR_ROWS` | **완전 지원** | 현재 열린 커서의 적격 행 수 반환 (닫힘 시 0) |
| `@@DATEFIRST` | **미구현 (후보)**| 주의 시작 요일 (기본 7 = 일요일) |
| `@@DBTS` | **미구현** | 전역 증가 rowversion 타임스탬프 |

---

## 8. 트랜잭션 제어 및 SET 옵션

| 구문 / 옵션 | 지원 여부 | 동작 상세 |
| :--- | :--- | :--- |
| `BEGIN TRAN [name]` | **완전 지원** | 명시적 ACID 트랜잭션 경계 시작 |
| `COMMIT TRAN [name]` | **완전 지원** | 변경사항 디스크/스토리지 확정 반영 |
| `ROLLBACK TRAN [name]`| **완전 지원** | 모든 변경사항 롤백 및 작업공간 폐기 |
| `SAVE TRAN savepoint` | **완전 지원** | 세이브포인트 생성 및 부분 롤백 지원 |
| `XACT_STATE()` | **완전 지원** | 1(활성), 0(없음), -1(커밋 불가 롤백 전용) |
| `SET NOCOUNT ON / OFF`| **완전 지원** | DONE 패킷 토큰 행 수 메시지 억제/출력 토글 |
| `SET XACT_ABORT ON/OFF`| **부분 지원** | 구문 인식 완료, 런타임 강제 즉시 롤백 연계 고도화 필요 |
| `SET ANSI_NULLS` | **완전 지원** | NULL 비교 동작 제어 |
| `SET QUOTED_IDENTIFIER`| **완전 지원** | 큰따옴표 식별자 허용 여부 |

---

## 9. 구현 완료 마일스톤 및 잔여 갭(Gap) 요약

### 우선순위 그룹 A: 실무 체감도 '극상' [구현 완료]
1. **복합 대입 연산자 (`+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `^=`, `|=`)** [`완전 지원`]:
   - `SET @i += 1`, `UPDATE tbl SET qty += 5` 등 모든 SQL Server 실무 프로시저 루프 지원 완료.
   - 파서에서 `Target = Target Op Expr`로 Desugaring하여 완벽 지원 (`TokenPlusAssign` ~ `TokenBitOrAssign`).
2. **`WAITFOR DELAY '00:00:01'` / `WAITFOR TIME '...'`** [`완전 지원`]:
   - 배치 스크립트, 재시도 대기 루프 시간 지연 지원. VDBE `OpWaitfor` 바이트코드 및 컨텍스트 협조적 중단 연동 완료.
3. **`@@CURSOR_ROWS`** [`완전 지원`]:
   - 커서가 열릴 때 적격 행 수를 실시간 추적하고 닫힐 때 0으로 리셋하는 세션 카운터 지원 완료.

### 우선순위 그룹 B: 실무 체감도 '중~상' [구현 완료]
4. **테이블 변수 (`DECLARE @tbl TABLE (...)`)** [`완전 지원`]:
   - 세션 배치(`ExecuteSessionSQL`), 익명 블록, 저장 프로시저(`CREATE PROCEDURE`) 전반에 걸친 Microsoft SQL Server T-SQL 테이블 변수 지원 완료.
   - 격리된 물리 테이블(`__tv_<frame>_<time>_<name>`) 동적 생성 및 프레임 소멸 시 자동 드롭, DML(`INSERT INTO @tbl VALUES / SELECT`, `SELECT FROM @tbl`, `UPDATE @tbl`, `DELETE FROM @tbl`, `JOIN @tbl`) 완벽 연동.
5. **`STRING_SPLIT(str, sep [, enable_ordinal])`** [`완전 지원`]:
   - 문자열 구분자 분할 내장 테이블 반환 연산(`enable_ordinal`, `@var`, `CROSS APPLY`) 지원 완료 (`pkg/engine/sqlexec_string_split.go`).
6. **인라인 테이블 반환 함수 (`CREATE FUNCTION ... RETURNS TABLE`)** [`부분 지원`]:
   - 단일 `SELECT` 인라인 TVF 등록/조회 지원 완료 (`멀티-스테이트먼트 TVF`는 미구현).
7. **임시 테이블/테이블 변수 인메모리 하이브리드 바이패스 (`PERF-HYBRID-TEMPMEM-1`)** [`DONE`]:
   - WiredTiger 모드에서 `__temp_*`, `__tv_*`, `__var_*`, `#*` 테이블의 `.wt` 디스크 파일 생성 및 CGo I/O를 생략하고 내장 `memengine`으로 라우팅 완료 (`pkg/storage/wiredtiger/wt_engine.go`).

### 진행 중 및 잔여 확장 후보 (Active & Candidate Gaps)
8. **논리 네임스페이스 DB 프리픽스 & 크로스 DB 조인 (`ARCH-LOGICAL-NAMESPACE-1`)** [`PENDING`]:
   - 현재 가상 No-op 상태인 `USE <dbname>`을 `__db_<dbname>_*` 물리 접두사 격리 및 3-Part Name 크로스 DB 조인으로 확장 ([`LOGICAL_NAMESPACE_DATABASE_PREFIX_PLAN.ko.md`](plans/LOGICAL_NAMESPACE_DATABASE_PREFIX_PLAN.ko.md)).
9. **사용자 정의 테이블 타입 (`CREATE TYPE ... AS TABLE`) 및 TVP (`TSQL-TVP-TYPE-1`)** [`v1.6.0 로드맵`]:
   - 클라이언트에서 DataTable을 매개변수로 넘기는 기능 (`0xF3 TVPTYPE` 토큰 및 임시 테이블 바인딩).
10. **멀티-스테이트먼트 TVF (`TSQL-MULTI-TVF-1`)** [`v1.6.0 로드맵`]:
    - `CREATE FUNCTION ... RETURNS @table TABLE (...) AS BEGIN ... END` 형태의 VDBE VM 절차적 재진입 및 결과 테이블 반환.
11. **T-SQL 확장 내장 스칼라 함수군** [`v1.1.0 로드맵`]:
    - `AT TIME ZONE` 시간대 변환 절 및 함수 (`TSQL-FUNC-AT-TIME-ZONE`)
    - `COMPRESS` 및 `DECOMPRESS` Gzip 바이너리 압축/해제 함수 (`TSQL-FUNC-COMPRESS`)
    - `PARSE` 및 `TRY_PARSE` 문화권(Culture) 기반 문자열 파싱 함수 (`TSQL-FUNC-PARSE`)
    - `@@DATEFIRST` 및 `@@DBTS` 시스템 세션 및 글로벌 타임스탬프 변수 (`TSQL-SYSVAR-DATEFIRST-DBTS`)
12. **양방향 스크롤 커서 (`SCROLL`, `FETCH PRIOR`)** [`영구 Spec-Out`]:
    - 메모리 과점유 방지를 위해 `FORWARD_ONLY`만 지원 결정됨.

---

## 10. 결론 및 다음 단계

LionSQL은 T-SQL의 **핵심 제어 흐름(TRY/CATCH, WHILE, IF, GOTO, RETURN, sp_executesql, FORWARD_ONLY CURSOR), 복합 대입·레거시/정량 연산자(`+=`, `!<`, `!>`, `ALL`/`ANY`/`SOME`), 테이블 변수(`DECLARE @tbl TABLE`), 임시 테이블(`#temp`), 인라인 TVF/`STRING_SPLIT`, 140종 이상의 내장 함수 및 트랜잭션**을 갖추고 있습니다.

현재 시점의 실무 연계 우선순위는 다음과 같습니다:
- **완료 (`DONE`)**: `PERF-HYBRID-TEMPMEM-1` 구현 완료 — WiredTiger 모드에서도 `#temp`와 `@tbl`이 `.wt` 디스크 파일 생성 0개, 혼합 트랜잭션/세이브포인트 호환으로 동작합니다.
- **차기 확장 (`PENDING`)**: 멀티 DB 워크로드 요구 시 `ARCH-LOGICAL-NAMESPACE-1`을 통해 `USE <dbname>` 가상 No-op을 `__db_<dbname>_*` 논리 네임스페이스 격리로 승격합니다.
