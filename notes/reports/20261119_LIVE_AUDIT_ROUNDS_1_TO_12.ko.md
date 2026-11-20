# LionSQL 라이브 엔진 감사 Round 1 ~ 12 통합 근본 결함 및 해결 백서 (`notes/reports/20261119_LIVE_AUDIT_ROUNDS_1_TO_12.ko.md`)

> - **작성일자**: 2026-11-19 (통합 및 완결: 2026-11-20)
> - **감사 대상**: LionSQL Database Server v0.93.0 ~ v0.94.0 (In-Memory OCC / WiredTiger B+Tree, Port 5433 / 1433)
> - **감사 방법론**: psql 16.15 및 Go 드라이버 기반 실시간 적대적 라이브 프로브 (총 12개 라운드)
> - **상태**: Round 1 ~ 12 전건 결함 해결 완료 (100% 회귀 방지 테스트 통과)
> - **보존 정책**: 개별 12개 원시 리포트는 단일 압축 본 문서로 영구 단일화(SSOT)하고 원본은 Git 히스토리로 관리하여 디렉토리 경량화 달성

---

## 1. 감사 개요 및 구조적 집계

LionSQL v0.94.0 및 1.0-GA 출시에 앞서 라이브 엔진의 회귀 방지 및 ANSI SQL / T-SQL 융합 실행 정밀도를 검증하기 위해 12차례의 집중 실시간 감사를 단행하였습니다.

개별 쿼리 실패 증상(Symptom) 나열을 배제하고, **결함의 근본 원인이 된 핵심 소스 코드 모듈**, **원인 분석**, **해결 조치**, 그리고 **회귀 검증 테스트**를 중심으로 고밀도 압축 정리합니다.

| 라운드 | 감사 중점 영역 | 핵심 원인 모듈 | 식별/해결 결함 요약 | 회귀 검증 테스트 |
| :---: | :--- | :--- | :--- | :--- |
| **Round 1** | T-SQL 프로시저 런타임 & 수명주기 | `pkg/proc/runtime/vm.go`<br>`pkg/engine/session.go` | OUTPUT INTO 테이블 변수 유실, 프로시저 내 VIEW/CTE 참조 실패, 로컬 `#temp` 수명주기 충돌, IDENTITY 시드/증가분 무시 | `proc_view_query_test.go`<br>`proc_temp_table_lifecycle_test.go`<br>`output_into_table_var_test.go` |
| **Round 2** | 수치 연산, 날짜 산술, 윈도우 함수 | `pkg/proc/eval.go`<br>`pkg/execution/window.go` | 부동소수점 리터럴 정밀도 오차, DATEADD 월말 클램핑 누락, INT 산술 오버플로 사일런트 확장, NTILE/CUME_DIST 윈도우 구현 | `audit_round1_to_6_supplemental_test.go`<br>`easy_defects_test.go` |
| **Round 3** | 프로젝션 별칭, PK DML, 제약조건 | `pkg/execution/project.go`<br>`pkg/engine/sqlexec_dml.go` | SELECT expr AS col 별칭 섀도잉 무시, PK 다중행 UPDATE 시프트 충돌, 인라인 UNIQUE 인덱스 무시, 상관 서브쿼리 UPDATE | `audit_round1_to_6_supplemental_test.go`<br>`spec_driven_batch3_test.go` |
| **Round 4** | 복합 쿼리, OUTER JOIN, DML 서브쿼리 | `pkg/engine/sqlexec_select.go`<br>`pkg/execution/hash_join.go` | `SELECT *, expr` 스타 확장 누락, 비등가 LEFT/RIGHT JOIN 외곽행 보존, DML(`UPDATE`/`DELETE`) `IN (SELECT)` 서브쿼리 바인딩 | `audit_round4_regression_test.go`<br>`audit_round1_to_6_supplemental_test.go` |
| **Round 5** | 방어적 파싱, 엔진 패닉 방지, NULL 무결성 | `pkg/proc/fold.go`<br>`pkg/engine/evaluator.go` | WHERE 절 `1/0` 단락 평가 타입 단언 패닉 방지(`R5-001`), NOT NULL 생략 INSERT 차단, PK 컬럼 NULL 갱신 방지 | `audit_round1_to_6_supplemental_test.go`<br>`spec_driven_batch1_test.go` |
| **Round 6** | 동시성 제어, DDL 스키마 파서 | `pkg/proc/parser_ddl.go`<br>`pkg/engine/sqlexec_select.go` | `SELECT ... FOR UPDATE` 행 잠금 구문 파싱, 다중 `DROP TABLE` 지원, CHECK/DEFAULT 내 서브쿼리 차단 (Msg 1046) | `audit_round1_to_6_supplemental_test.go` |
| **Round 7** | 내장 함수 Arity 및 날짜 생성자 | `pkg/proc/builtins_sys.go`<br>`pkg/proc/eval.go` | LEN, GETDATE, COUNT, SUM 등 내장 함수 인자수 검증(`ValidateBuiltinArity`), 비정상 날짜 변환 실패 차단 (Msg 241) | `audit_round7_round8_test.go` |
| **Round 8** | 윈도우 프레임 경계 & OVER 구문 규칙 | `pkg/execution/window.go`<br>`pkg/proc/parser_expr.go` | ROWS BETWEEN 프레임 경계 오류 검증, ROW_NUMBER/RANK 등 정렬 필수 윈도우 함수 ORDER BY 누락 차단 (Msg 4112) | `audit_round7_round8_test.go` |
| **Round 9** | 보안, RBAC/DCL 권한, 트랜잭션 격리 | `pkg/engine/sqlexec_auth.go`<br>`pkg/engine/tx_manager.go` | SEC-001~009 (REVERT 보안 컨텍스트 탈출 차단, 중첩 EXECUTE AS 스택, DCL 권한 검사), TX-001~008 (세이브포인트 롤백 DDL 복원, READ ONLY DDL 차단) | `audit_round9_test.go` |
| **Round 10** | 실행기 커서 상태, 가상 뷰, CBO 통계 | `pkg/engine/session.go`<br>`pkg/engine/sqlexec_select.go` | ENG-001~010 (@@NESTLEVEL 반환, sp_executesql OUTPUT 변수 전파, FORWARD_ONLY 커서 역방향 FETCH 차단 Msg 16911, NTILE 버킷 수 검증 Msg 7812) | `audit_round10_test.go` |
| **Round 11** | 시퀀스 엔진, Option B 인덱스, 플랜 캐시 | `pkg/catalog/sequence.go`<br>`pkg/engine/sqlexec_sequence.go`<br>`pkg/storage/wiredtiger/wt_engine.go` | ENG-011~017 (원자적 시퀀스 엔진 `NEXTVAL`, 32컬럼 초과 인덱스 차단 Msg 1904, DROP COLUMN 시 Prepared Stmt 무효화 Msg 207, 세션 설정 음수 검증 22023, 중복 제약명 차단 Msg 2714, 동시 쓰기-쓰기 직렬화 충돌 감지) | `audit_round11_test.go` |
| **Round 12** | JSON / XML / 표현식 포맷팅 / 컬처 파싱 | `pkg/storage/json.go`<br>`pkg/proc/eval.go`<br>`pkg/engine/sqlexec_select.go` | ENG-018~022 (JSON strict 경로 모드 누락 키 Msg 13608 오류 반환, JSON_MODIFY append 배열 추가, XML 메소드 `@x.value()` 원문 노출 차단 Msg 2812, CONVERT 유효하지 않은 스타일 Msg 281 차단, PARSE/TRY_PARSE 평가) | `audit_round12_test.go` |

---

## 2. 라운드별 핵심 원인 코드 & 조치 분석 (Code-Level Post-Mortem)

### Round 1: T-SQL Stored Procedure 런타임 수명주기 및 가상화
- **결함 현상**:
  - 프로시저 내부에서 `OUTPUT ... INTO @table_var` 실행 시 데이터 유실.
  - 프로시저 내에서 가상 뷰(`VIEW`)나 CTE(`WITH`) 질의 시 `table not found` 오류 발생.
  - 프로시저 내 `#temp` 테이블 생성 후 동일 세션에서 2회차 재호출 시 테이블 중복 충돌.
  - `IDENTITY(seed, increment)` 생성 시 사용자 지정 초기값(예: 100, 10)이 무시되고 1, 1로 하드코딩.
- **원인 코드 위치**:
  - `pkg/proc/runtime/vm.go`: VDBE 가상 머신이 실행 시 세션의 뷰 카탈로그 확장(View Expansion) 및 CTE 프레임을 우회하고 물리 카탈로그만 직접 조회함.
  - `pkg/engine/session.go`: 프로시저 종료 스코프 훅에서 프로시저 로컬 임시 테이블 파기 누락.
  - `pkg/storage/memengine/ddl.go`, `pkg/storage/wiredtiger/wt_engine.go`: 시퀀스 채번 생성기에서 seed/increment 파라미터 미전달.
- **해결 조치**:
  - VDBE VM 쿼리 디스패처에 세션 뷰 확장 및 임시 테이블 해석기 연결.
  - 프로시저 호출 프레임 종료 시 생성된 로컬 `#temp` 테이블 자동 드롭 훅 등록 (`defer cs.cleanupProcTempTables()`).
  - 시퀀스 할당기에 사용자 지정 `seed`, `increment` 저장 및 반영.

---

### Round 2: 수치 연산, 날짜 산술 및 윈도우 함수 정합성
- **결함 현상**:
  - 소수 리터럴 `0.1 + 0.2` 연산 시 `float64` 부동소수점 오차 노출 (`0.30000000000000004`).
  - `DATEADD(month, 1, '2023-01-31')` 연산 시 2월 말일(28일)로 클램핑되지 않고 3월 3일로 오버플로.
  - 32비트 INT 덧셈 오버플로(`2147483647 + 1`) 시 에러 없이 int64로 사일런트 확장.
  - `NTILE`, `PERCENT_RANK`, `CUME_DIST` 윈도우 함수가 NULL 반환.
- **원인 코드 위치**:
  - `pkg/proc/lexer.go`, `eval.go`: 소수 리터럴을 즉시 `float64`로 파싱하여 정밀도 손실.
  - `pkg/proc/eval.go`: `evalDateAdd` 함수에서 Go `time.AddDate(0, months, 0)` 호출 시 월말 클램핑 미처리.
  - `pkg/proc/eval.go`: 32비트 정수 산술 오버플로 감지 로직 부재.
- **해결 조치**:
  - 소수 리터럴 파싱 시 고정소수점 `DECIMAL` 보존 파이프라인 도입.
  - `evalDateAdd`에 타깃 월의 일수(`daysInMonth`)를 계산하여 말일 초과 시 클램핑하는 로직 구현.
  - `math.MaxInt32` 초과 시 `Msg 8115` (`Arithmetic overflow error`) Fail-Closed 반환.

---

### Round 3: 프로젝션 별칭 섀도잉, PK 원자적 갱신 및 제약조건
- **결함 현상**:
  - `SELECT val + 100 AS val` 실행 시 수식이 무시되고 원본 컬럼 `val` 값이 반환됨.
  - PK 컬럼 다중행 UPDATE(`UPDATE t SET id = id + 10`) 시 중간 키 충돌로 행 소실.
  - 컬럼 인라인 `UNIQUE` 제약조건이 무시되어 중복값 삽입 허용.
- **원인 코드 위치**:
  - `pkg/execution/project.go`: `projectTuple`에서 표현식 AST를 평가하기 전에 `tuple.GetOK(alias)`를 먼저 조회하여 기존 컬럼명과 별칭이 같으면 원본 값을 반환함.
  - `pkg/engine/sqlexec_dml.go`: UPDATE 실행 시 각 행을 즉시 순차 반영하여 아직 갱신되지 않은 후속 행의 PK와 일시적 충돌 발생.
- **해결 조치**:
  - 프로젝션 시 표현식 AST 평가 우선 원칙 적용 (별칭 맵 조회 제거).
  - 다중행 UPDATE 시 2단계 스왑/원자적 적용(Staged Mutation) 파이프라인 적용.

---

### Round 4: 복합 질의, OUTER JOIN 외곽 행 보존 및 DML 서브쿼리
- **결함 현상**:
  - `SELECT *, val * 2 AS d` 질의 시 `*`가 확장되지 않고 컬럼명 `*`에 NULL 출력.
  - 비등가 조건(예: `ON a.id < b.id`) 또는 좌측 필터가 포함된 LEFT JOIN에서 매칭되지 않은 좌측 행이 증발.
  - `UPDATE ... WHERE id IN (SELECT ...)` 또는 `DELETE ... WHERE id IN (...)`이 0건 처리됨.
- **원인 코드 위치**:
  - `pkg/engine/sqlexec_select.go`: 프로젝션 리스트 파싱 시 `*` 단독 케이스만 분기하고, `*`와 다른 식이 혼합된 경우 와일드카드 전개를 수행하지 않음.
  - `pkg/execution/hash_join.go`: 해시 조인 탐색 실패 시 외곽 행(Outer Row)을 NULL 패딩하여 방출하는 로직 누락.
  - `pkg/engine/sqlexec_dml.go`: VDBE Fast-Path 실행기가 서브쿼리 컨텍스트를 넘겨받지 못하고 평가하여 미매칭 처리.
- **해결 조치**:
  - 복합 프로젝션에서 테이블 카탈로그 기반의 `*` 인라인 전개(ExpandStar) 적용.
  - `HashJoinIterator` 및 중첩 루프 조인에서 좌/우 외곽 보존 테이블의 미매칭 행 NULL 패딩 방출 보장.
  - DML 실행기에 `SubqueryExecutor` 바인딩 및 VDBE 우회 안전망 구축.

---

### Round 5: 엔진 패닉 방지 (단락 평가 방어) 및 DDL/DML 무결성
- **결함 현상**:
  - `SELECT id FROM t WHERE id = 99 AND 1/0 = 1` 실행 시 엔진 패닉 발생 (`proc.Expr is *proc.BinaryExpr, not *proc.LiteralExpr`).
  - INSERT 시 NOT NULL(기본값 없음) 컬럼 생략 시 NULL 삽입 허용.
  - PRIMARY KEY 컬럼을 NULL로 UPDATE 허용.
- **원인 코드 위치**:
  - `pkg/proc/fold.go`, `pkg/engine/evaluator.go`: 인덱스 추출기 및 상수 폴딩 시 우변 표현식을 방어 검사 없이 `*proc.LiteralExpr`로 단언(type assertion)함.
  - `pkg/engine/sqlexec_dml.go`: 레코드 생성 시 누락된 컬럼에 대해 스키마의 NOT NULL 제약 검사를 건너뜀.
- **해결 조치**:
  - 모든 표현식 타입 단언에 comma-ok 방어 패턴 적용 및 단락 평가 보장 (`[RULE:PanicRecovery]`, `[RULE:DefensiveIndexing]`).
  - INSERT 레코드 조립 시 생략된 컬럼의 NOT NULL 위반 검사 의무화.

---

### Round 6: 동시성 락 구문, 다중 DROP TABLE 및 제약조건 서브쿼리 차단
- **결함 현상**:
  - `SELECT ... FOR UPDATE` 구문 파싱 거부 또는 행 락 미적용.
  - `CHECK (id IN (SELECT 1))` 또는 `DEFAULT (SELECT 1)` 제약조건에 서브쿼리가 허용되어 스키마 오염.
- **원인 코드 위치**:
  - `pkg/proc/parser_ddl.go`: CHECK/DEFAULT 표현식 파싱 시 서브쿼리(`SELECT`, `EXISTS`) 포함 여부 검증 부재.
- **해결 조치**:
  - DDL 제약조건 표현식 검증기(`ValidateConstraintExpr`)에서 서브쿼리 토큰 탐지 시 `Msg 1046` 에러로 즉시 거부 (`Fail-Closed`).

---

### Round 7 & 8: 내장 함수 Arity 검증, 날짜 생성자 및 윈도우 프레임 경계
- **결함 현상**:
  - `LEN()`, `GETDATE(1)`, `COUNT()`, `SUM(1, 2)` 등 인자 수가 맞지 않는 호출이 에러 없이 무시되거나 비정상 동작.
  - `ROW_NUMBER() OVER ()` 처럼 정렬(ORDER BY)이 필수적인 랭킹 함수에서 ORDER BY 누락 허용.
  - `ROWS BETWEEN 5 PRECEDING AND 10 PRECEDING` 등 시작 오프셋이 종료 오프셋보다 큰 비정상 프레임 허용.
- **원인 코드 위치**:
  - `pkg/proc/builtins_sys.go`: 각 함수별 최소/최대 허용 인자 수 테이블 부재.
  - `pkg/proc/parser_expr.go`: 윈도우 함수 OVER 절 검증에서 함수별 필수 절 규칙 미검사.
- **해결 조치**:
  - `ValidateBuiltinArity` 중앙 검증기 구축 및 모든 내장 스칼라/집계 함수에 적용.
  - 윈도우 OVER 절 컴파일러에서 필수 `ORDER BY` 부재 시 `Msg 4112` 반환.
  - 윈도우 프레임 경계 순서 유효성 검사 로직 추가.

---

### Round 9: 보안, RBAC/DCL 권한 체계 및 트랜잭션 격리 (SEC-001 ~ TX-008)
- **결함 현상**:
  - `SEC-001`: 일반 세션에서 무단 `REVERT` 실행 시 오류 없이 `dbo` 최고 권한으로 권한 상승(Privilege Escalation).
  - `SEC-003`: 일반 유저가 `CREATE LOGIN`, `GRANT` 등 DCL 명령 수행 가능.
  - `TX-003`: `BEGIN TRANSACTION READ ONLY` 세션에서 DDL(`CREATE TABLE`) 실행 허용.
- **원인 코드 위치**:
  - `pkg/engine/sqlexec_auth.go`: `REVERT` 핸들러가 현재 세션의 가장 스택 깊이를 확인하지 않고 최상위 원본 사용자로 리셋함.
  - `pkg/engine/coordinator_impl.go`: DCL 및 관리 명령 실행 전 호출자 권한 검증 누락.
  - `pkg/engine/tx_manager.go`: 읽기 전용 트랜잭션 플래그가 DDL 실행 경로에서 확인되지 않음.
- **해결 조치**:
  - 중첩 가장 스택(`impersonationStack`) 도입 및 빈 스택 `REVERT` 시 `Msg 15151` Fail-Closed 반환.
  - `checkDCLPermission`을 통해 SA/sysadmin 권한 강제 검증.
  - 읽기 전용 트랜잭션에서 DDL 감지 시 `Msg 3923` 차단.

---

### Round 10: 프로시저 가상 머신, 커서 및 CBO 통계 (ENG-001 ~ ENG-010)
- **결함 현상**:
  - `ENG-001`: `@@NESTLEVEL`이 프로시저 중첩 깊이를 반영하지 않고 하드코딩 0 반환.
  - `ENG-002`: `sp_executesql` 내부에서 수정된 OUTPUT 파라미터가 호출자의 로컬 변수에 반영되지 않음.
  - `ENG-004`: `FORWARD_ONLY` 커서에서 `FETCH LAST` 역방향 호출 허용.
  - `ENG-006`: `NTILE(0)` 버킷 0 이하 호출 허용.
- **원인 코드 위치**:
  - `pkg/engine/sqlexec_select.go`: `@@NESTLEVEL` 평가 시 세션 프레임 스택 길이 미조회.
  - `pkg/proc/runtime/vm_dispatcher.go`: `sp_executesql` 반환 시 OUTPUT 바인딩 역전파 누락.
  - `pkg/engine/session.go`: 커서 타입 검증 누락.
- **해결 조치**:
  - 세션 프레임 스택 기반 `@@NESTLEVEL` 동적 평가.
  - `sp_executesql` 종료 시 호출자 스택 프레임으로 OUTPUT 변수 역매핑.
  - `FORWARD_ONLY` 커서 역방향 FETCH 시 `Msg 16911` 차단, NTILE(0) 시 `Msg 7812` 차단.

---

### Round 11: 시퀀스 엔진, Option B 인덱스 및 플랜 무효화 (ENG-011 ~ ENG-017)
- **결함 현상**:
  - `ENG-011`: 시퀀스 생성이 스텁 상태이며, `NEXTVAL`이 항상 고정값 1만 반환.
  - `ENG-012`: Option B 물리 인덱스 제약(최대 32개 컬럼) 검증 누락으로 35개 컬럼 인덱스 생성 통과.
  - `ENG-013`: `DROP COLUMN` 후 기존 Prepared Statement가 무효화되지 않고 오래된 컬럼 오프셋을 읽어 빈 문자열 반환.
  - `ENG-014`: 스냅샷 격리에서 동일 행에 대한 동시 쓰기-쓰기 충돌(Write-Write Conflict) 발생 시 직렬화 실패 없이 덮어씀.
  - `ENG-016`: 동일 테이블 내 동일한 이름의 제약조건 중복 등록 허용.
- **원인 코드 위치**:
  - `pkg/catalog/sequence.go`, `pkg/engine/sqlexec_sequence.go`: 시퀀스 메타데이터 저장소 및 원자적 CAS 증감 엔진 부재.
  - `pkg/storage/wiredtiger/wt_engine.go`: `CreateIndex` 호출 시 `IndexSchema.Validate` 누락.
  - `pkg/engine/sqlexec_prepare.go`: DDL 스키마 변경 시 관련 테이블의 Prepared Statement 플랜 캐시 무효화(`InvalidatePlanCache`) 누락.
- **해결 조치**:
  - 완전한 영속 시퀀스 카탈로그 및 `NEXTVAL` / `NEXT VALUE FOR` 파이프라인 구현 (`pkg/engine/sqlexec_sequence.go`).
  - Option B 최대 32컬럼 초과 시 `Msg 1904` 차단.
  - `DROP COLUMN` 시 Prepared Statement 버전 무효화 및 `Msg 207` 반환.
  - 동시 쓰기-쓰기 충돌 시 `serialization failure: concurrent update conflict` Fail-Fast 차단.

---

### Round 12: JSON, XML, 포맷 스타일 및 컬처 파싱 (ENG-018 ~ ENG-022)
- **결함 현상**:
  - `ENG-018`: `JSON_VALUE('{"a":1}', 'strict $.b')` 실행 시 `strict` 경로 모드를 무시하고 NULL 반환.
  - `ENG-019`: `JSON_MODIFY('{"a":100}', 'append $.a', 200)` 실행 시 리터럴 `"append $"` 키를 생성하여 JSON 오염.
  - `ENG-020`: `@x.value(...)` 변수 메소드 호출 시 파싱하지 않고 원본 XML 문자열 그대로 반환.
  - `ENG-021`: `CONVERT(VARCHAR, dt, 99999)` 잘못된 스타일에 대해 에러 없이 기본 포맷 출력.
  - `ENG-022`: `PARSE('123.45' AS DECIMAL(10,2) USING 'en-US')` 실행 시 원본 SQL 텍스트 그대로 반환.
- **원인 코드 위치**:
  - `pkg/storage/json.go`: JSON 경로 탐색기에서 `strict` 접두사 제거 시 모드 플래그 미설정.
  - `pkg/proc/builtins_json.go`: `append` 키워드를 수식어가 아닌 경로의 일부로 파싱.
  - `pkg/engine/sqlexec_select.go`: 변수 뒤의 메소드 호출(`.value()`)을 단일 식별자로 취급하여 변수값 반환.
  - `pkg/proc/eval.go`: `formatDateTimeByStyle`의 default 분기에서 에러 없이 ISO 문자열 반환.
- **해결 조치**:
  - `strict` 모드 누락 키에 대해 `Msg 13608` 오류 즉시 발생.
  - `append` 수식어 지원으로 배열 요소 추가(`[100, 200]`) 정상 처리.
  - 변수 메소드 호출 감지 시 원본 노출 차단 및 `Msg 2812` 반환.
  - 유효하지 않은 `CONVERT` 스타일에 대해 `Msg 281` 에러 반환.
  - `PARSE` / `TRY_PARSE` 표현식 파서 및 평가기 구현.

---

## 3. 영구 아키텍처 불변식 (Invariants) 확립 내역

본 감사를 통해 LionSQL 엔지니어링 가이드라인(`GEMINI.md`)에 확립된 영구 핵심 아키텍처 불변식은 다음과 같습니다:

1. **`[Invariant 7]` 생산 쿼리 경로 Zero-Regex 원칙**:
   - 정규표현식은 Context-Free Grammar 파싱이 수학적으로 불가능하므로, 모든 DDL/DML 및 표현식 처리는 `proc.Lexer` 및 `sqlutil` 결정론적 토크나이저만 사용.
2. **`[Invariant 12]` Context-Blind `strings.*` 금지 및 `sqlutil` 일원화**:
   - SQL 문자열 슬라이싱에 raw Go `strings.*` 사용을 금지하고, 리터럴/주석/괄호 인지형 `sqlutil.FindTopLevelKeyword`, `sqlutil.SplitTopLevel`로 일원화. AST Architecture Guard(`arch_guard_test.go`)를 통해 CI에서 원천 감시.
3. **`[Invariant 13]` 단일 표준 V2 바이너리 튜플 코덱 (Option B)**:
   - 4바이트 32비트 정렬 헤더, 널 비트맵, 최대 1,024개 컬럼 상한 강제.
4. **`[Invariant 15]` Option B 물리 스토리지 제약**:
   - 단일 테이블당 인덱스 최대 999개, 인덱스 복합 컬럼 최대 32개, 인덱스 키 크기 최대 1,700바이트 강제.

---

## 4. 통합 회귀 방지 테스트 스위트 매핑

모든 라운드의 시정 조치는 영구 회귀 방지 테스트로 코드베이스에 각인되어 있으며, `go test -v -race` 파이프라인에서 100% 통과합니다:

- `pkg/engine/audit_round1_to_6_supplemental_test.go`: Round 1 ~ 6 핵심 보강 스위트 (12개 전 서브테스트 PASS)
- `pkg/engine/audit_round4_regression_test.go`: Round 4 복합 질의/3VL 스위트 (4개 테스트 PASS)
- `pkg/engine/audit_round7_round8_test.go`: Round 7 & 8 내장 함수 Arity 및 윈도우 프레임 스위트 (PASS)
- `pkg/engine/audit_round9_test.go`: Round 9 보안(SEC-001~009) 및 트랜잭션(TX-001~008) 스위트 (17개 테스트 PASS)
- `pkg/engine/audit_round10_test.go`: Round 10 CBO/커서/프로시저(ENG-001~010) 스위트 (10개 테스트 PASS)
- `pkg/engine/audit_round11_test.go`: Round 11 시퀀스/Option B 인덱스/충돌(ENG-011~017) 스위트 (7개 테스트 PASS)
- `pkg/engine/audit_round12_test.go`: Round 12 JSON/XML/CONVERT(ENG-018~022) 스위트 (5개 테스트 PASS)
- `pkg/engine/spec_driven_batch1_test.go` ~ `batch3_test.go`: SQL 표준 및 ACID 불변식 스위트 (79개 테스트 PASS)

---
*본 문서는 LionSQL 라이브 감사 Round 1 ~ 12의 단일 진실 공급원(SSOT)이며, 향후 동일 결함의 재발을 원천 차단하는 엔지니어링 기준선으로 기능합니다.*
