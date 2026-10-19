# LionSQL Reference Manual

이 문서는 LionSQL 데이터베이스 엔진의 모든 명령어, 설정값, SQL 구문 및 문법 규격, 데이터 타입, 시스템 내장 함수, 가상 카탈로그 뷰, REST API 엔드포인트 및 와이어 프로토콜 사양을 총망라하는 **공식 레퍼런스 매뉴얼(Reference Manual)**입니다.

---

## 목차 (Table of Contents)

### 제1장. 명령행 인터페이스 (CLI Reference)
* **1.1. 단일 바이너리(`bin/lionsql`) 명령 구조**
  * 포그라운드 대화형 기동: `lionsql [serve] [flags...]`
  * 백그라운드 데몬 제어: `lionsql start [-dev]`, `stop`, `restart [-dev]`, `status`
  * 관리 및 도구 서브커맨드: `doctor`, `init-config`, `migrate`, `sql`, `backup`, `restore`, `version`
* **1.2. 전역 CLI 플래그 사양**
  * 네트워크 리스너: `-http`, `-pg`, `-tds`, `-tls`, `-tls-cert`, `-tls-key`, `-require-ssl`
  * 스토리지 백엔드: `-dir`, `-engine` (`wiredtiger`, `memory`), `-ephemeral`, `-auto-seed`
  * 리소스 거버너: `-max-memory` (`<size>` e.g. `2GB`, `4096MB`, 기본값: 물리 RAM의 70%), `-work-mem` (기본값: `64MB`), `-max-workers`, `-max-idle-sessions`
  * 보안 및 인증: `-auth` (기본값: `true`), `-admin-email`, `-admin-pass`, `-jwt-secret`
  * 로깅 및 트레이싱: `-dev`, `-slow-log`, `-slow-threshold-ms` (기본값: `100.0ms`), `-general-log`
  * 고가용성 복제: `-replica-of`, `-node-id`, `-peers`, `-self-url`, `-auto-failover`, `-heartbeat-timeout-ms`
* **1.3. 벤치마크 및 스트레스 테스트 도구 (`bin/bench`, `bin/tpcc_vdbe`)**
  * TPC-C VDBE 하네스: `-warehouses`, `-workers`, `-duration`, `-engine`, `-fsync` (`everysec`, `always`, `off`)
  * OLTP 마이크로 벤치마크: `-accounts`, `-workers`, `-ops`, `-engine`
* **1.4. 종료 코드 및 프로세스 시그널 (Exit Codes & POSIX Signals)**
  * 표준 종료 코드 정의 (0: 정상 종료, 1: 일반 오류, 2: 설정 오류)
  * POSIX 시그널 처리: `SIGINT`, `SIGTERM` (1.5초 이내 단계별 Graceful Shutdown), `SIGHUP`

---

### 제2장. 설정 파일 사양 (Configuration File Reference)
* **2.1. `lionsql.json` 전체 스키마 명세**
* **2.2. 섹션별 상세 설정 키**
  * `Server`: `engine`, `data_dir`, `ephemeral`, `auto_seed`, `pid_file`
  * `Network`: `http_addr`, `pg_addr`, `tds_addr`, `tls` (`enabled`, `cert_file`, `key_file`, `require_ssl`)
  * `Auth`: `enabled`, `admin_email` (기본: `admin@lionsql.local`), `admin_pass`, `jwt_secret`
  * `Resources`: `max_memory` (기본: 70% RAM), `work_mem` (기본: `64MB`), `max_workers`, `max_idle_sessions`
  * `Logging`: `dev_mode`, `slow_query` (`enabled`, `threshold_ms`: `100.0`, `file`), `general_query` (`enabled`, `file`)
  * `Replication`: `enabled`, `role`, `node_id`, `primary_url`, `auto_failover`, `peers`, `self_url`, `heartbeat_timeout_ms`
* **2.3. 옵션 조합별 트랜잭션 성능 및 워크로드 프로파일 요약**
  * 초저지연 인메모리, 표준 프로덕션 OLTP, 무손실 금융 코어 원장, 대용량 배치 프로파일

---

### 제3장. 데이터 타입 및 리터럴 (Data Types & Literals Reference)
* **3.1. 수치형 데이터 타입 (Numeric Types)**
  * 정수형: `TINYINT` (8-bit), `SMALLINT` (16-bit), `INT` / `INTEGER` (32-bit), `BIGINT` (64-bit)
  * 부동소수점: `FLOAT` / `REAL` (64-bit IEEE 754)
  * 고정소수점 정밀 수치: `DECIMAL(p, s)` / `NUMERIC(p, s)` (최대 precision 38)
* **3.2. 문자열 및 텍스트 데이터 타입 (Character Types)**
  * `VARCHAR(n)`: 가변 길이 문자열 (최대 8,000자)
  * `NVARCHAR(n)`: 유니코드 가변 길이 문자열 (UTF-16LE / UTF-8)
  * `TEXT`: 무제한 대용량 텍스트
* **3.3. 날짜 및 시간 데이터 타입 (Temporal Types)**
  * `DATE`: 년-월-일 (YYYY-MM-DD)
  * `TIME`: 시:분:초 (HH:MM:SS)
  * `DATETIME`: 밀리초 정밀도 일시 (1900-01-01 ~ 9999-12-31)
* **3.4. 논리 및 비트 데이터 타입 (Boolean Types)**
  * `BIT` / `BOOLEAN`: 0 (FALSE) 또는 1 (TRUE)
* **3.5. 바이너리 및 식별자 데이터 타입 (Binary & UUID Types)**
  * `VARBINARY(n)` / `BYTEA`: 가변 길이 원시 바이트 열
  * `UNIQUEIDENTIFIER` / `UUID`: 128비트 범용 고유 식별자 (RFC 4122 / UUIDv7)
* **3.6. 반정형 데이터 타입 (Semi-Structured Types)**
  * `JSON`: JSON 텍스트 검증 및 B-Tree 인덱싱 지원

---

### 제4장. SQL 구문 및 명령문 사양 (SQL Statement Reference)
* **4.1. 데이터 정의어 (DDL: Data Definition Language)**
  * `CREATE TABLE`, `ALTER TABLE`, `DROP TABLE`, `TRUNCATE TABLE`
  * `CREATE INDEX`, `DROP INDEX`
  * `CREATE VIEW`, `DROP VIEW`
  * `CREATE PROCEDURE`, `ALTER PROCEDURE`, `DROP PROCEDURE`
  * `CREATE FUNCTION`, `ALTER FUNCTION`, `DROP FUNCTION`
  * `CREATE TRIGGER`, `ALTER TRIGGER`, `DROP TRIGGER`
* **4.2. 데이터 조작어 (DML: Data Manipulation Language)**
  * `SELECT`: 프로젝션, 별칭(`AS`), 집계, 조건절
  * `INSERT INTO`: 단일/다중 행 삽입, `DEFAULT VALUES`
  * `UPDATE`: 단일/다중 컬럼 수정, 조건부 갱신
  * `DELETE`: 조건부 행 삭제
  * `MERGE`: 조건부 INSERT/UPDATE/DELETE 원자적 동기화
  * `SELECT INTO`: 쿼리 결과를 새 테이블 또는 로컬 변수로 바인딩
* **4.3. 쿼리 절 및 수정자 (Query Clauses & Modifiers)**
  * `FROM`: 테이블, 뷰, TVF, 파생 테이블(Subquery in FROM)
  * `JOIN`: `INNER JOIN`, `LEFT [OUTER] JOIN`, `RIGHT JOIN`, `CROSS JOIN`, `CROSS APPLY`
  * `WHERE`: 조건 술어, `AND`, `OR`, `NOT`, `IN`, `BETWEEN`, `LIKE`, `EXISTS`
  * `GROUP BY` & `HAVING`: 그룹화 요약, `ROLLUP`, `CUBE`, `GROUPING SETS`
  * `ORDER BY`: 정렬 순서(`ASC`/`DESC`), Null 정렬 순서
  * `LIMIT` / `OFFSET` 및 `OFFSET ... FETCH NEXT ... ROWS ONLY`
  * `TOP (n) [PERCENT]`
* **4.4. 트랜잭션 제어어 (TCL: Transaction Control Language)**
  * `BEGIN TRANSACTION` / `BEGIN TRAN`
  * `COMMIT TRANSACTION` / `COMMIT TRAN`
  * `ROLLBACK TRANSACTION` / `ROLLBACK TRAN`
  * `SAVE TRANSACTION <savepoint>` / `ROLLBACK TO <savepoint>`
* **4.5. 세션 및 환경 제어어 (SCL: Session Control Language)**
  * `USE <database>`: 관용적 가상 No-Op 처리 및 TDS 환경 동기화
  * `SET NOCOUNT ON/OFF`
  * `SET ANSI_NULLS ON/OFF`
  * `SET QUOTED_IDENTIFIER ON/OFF`
  * `SET TRANSACTION ISOLATION LEVEL`
  * `SET work_mem = '...'`
  * `SHOW <variable>`
* **4.6. 데이터베이스 관리 및 진단 명령어 (Administrative Statements)**
  * `ANALYZE TABLE <table_name>`: CBO 비용 통계 및 히스토그램 생성
  * `EXPLAIN [ANALYZE] <query>`: 쿼리 실행 계획 및 실제 프로파일링 출력
  * `DBCC CHECKDB`: 데이터베이스 무결성 검증

---

### 제5장. 연산자 및 내장 함수 사양 (Functions & Operators Reference)
* **5.1. 연산자 (Operators)**
  * 산술 연산자: `+`, `-`, `*`, `/`, `%`
  * 비교 연산자: `=`, `<>`, `!=`, `<`, `<=`, `>`, `>=`
  * 논리 연산자: `AND`, `OR`, `NOT`, `IS NULL`, `IS NOT NULL`
  * 문자열 연결: `+` (T-SQL), `||` (ANSI/PostgreSQL)
  * 패턴 매칭: `LIKE`, `NOT LIKE` (`%`, `_` 와일드카드)
* **5.2. 조건부 수식 (Conditional Expressions)**
  * `CASE WHEN ... THEN ... [ELSE ...] END` (단순형 및 검색형)
  * `COALESCE(val1, val2, ...)`
  * `ISNULL(check_expr, replacement_value)`
  * `NULLIF(expr1, expr2)`
* **5.3. 집계 및 분석 함수 (Aggregate & Analytical Functions)**
  * `COUNT(*)`, `COUNT(col)`, `COUNT(DISTINCT col)`
  * `SUM(col)`, `AVG(col)`, `MIN(col)`, `MAX(col)`
  * 윈도우 함수: `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `LEAD()`, `LAG()`
  * 백분위수 및 분위수 분석 함수: `PERCENTILE_CONT`, `PERCENTILE_DISC`
* **5.4. 문자열 함수 (String Functions)**
  * `LEN()`, `DATALENGTH()`, `SUBSTRING()`, `LEFT()`, `RIGHT()`
  * `LTRIM()`, `RTRIM()`, `TRIM()`, `UPPER()`, `LOWER()`
  * `REPLACE()`, `CONCAT()`, `QUOTENAME()`, `CHARINDEX()`, `STRING_SPLIT()`
* **5.5. 수치 및 수학 함수 (Math Functions)**
  * `ABS()`, `ROUND()`, `CEILING()`, `FLOOR()`, `POWER()`, `SQRT()`, `SIGN()`
* **5.6. 날짜 및 시간 함수 (Date/Time Functions)**
  * `GETDATE()`, `CURRENT_TIMESTAMP`, `SYSDATETIME()`
  * `DATEADD()`, `DATEDIFF()`, `DATEPART()`, `YEAR()`, `MONTH()`, `DAY()`
* **5.7. 형변환 함수 (Type Conversion Functions)**
  * `CAST(expression AS data_type)`
  * `CONVERT(data_type, expression [, style])`
* **5.8. 시스템 및 메타데이터 함수 (System & Metadata Functions)**
  * `@@VERSION`, `@@SPID`, `@@TRANCOUNT`, `@@IDENTITY`, `@@ROWCOUNT`
  * `SCOPE_IDENTITY()`, `SCHEMA_NAME([id])`, `SCHEMA_ID([name])`
  * `OBJECT_ID([name])`, `OBJECT_NAME([id])`, `SERVERPROPERTY([property])`
  * `DB_NAME()`, `CURRENT_DATABASE()`

---

### 제6장. 가상 카탈로그 뷰 및 메타데이터 사양 (System Catalogs Reference)
* **6.1. MS SQL Server 호환 카탈로그 (`sys.*`)**
  * `sys.databases`: `master` 단일 카탈로그 및 `master.mdf` 메타데이터
  * `sys.tables`, `sys.columns`, `sys.indexes`, `sys.index_columns`
  * `sys.objects`, `sys.schemas`, `sys.partitions`, `sys.master_files`
  * `sys.types`, `sys.default_constraints`, `sys.computed_columns`, `sys.extended_properties`
* **6.2. ANSI / ISO 호환 카탈로그 (`INFORMATION_SCHEMA.*`)**
  * `INFORMATION_SCHEMA.TABLES` (기본 카탈로그 `postgres`)
  * `INFORMATION_SCHEMA.COLUMNS`
  * `INFORMATION_SCHEMA.TABLE_CONSTRAINTS`
  * `INFORMATION_SCHEMA.KEY_COLUMN_USAGE`
  * `INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS`
* **6.3. PostgreSQL 호환 카탈로그 (`pg_catalog.*`)**
  * `pg_catalog.pg_database`: `postgres` 단일 데이터베이스 노출
  * `pg_catalog.pg_class`, `pg_attribute`, `pg_type`, `pg_index`
  * `pg_catalog.pg_constraint`, `pg_namespace`, `pg_proc`, `pg_stat_activity`
  * 가상 시스템 함수: `current_schema()`, `current_setting()`, `version()`

---

### 제7장. REST API 및 웹 스튜디오 사양 (REST API Reference)
* **7.1. 시스템 및 실시간 관제 엔드포인트**
  * `GET /api/health`: 서버 상태 및 헬스체크 핑
  * `GET /api/version`: 엔진 릴리즈 버전(`0.90.0`) 및 빌드 정보
  * `GET /api/metrics`: Prometheus 호환 실시간 동시성, QPS, 힙 메모리 지표
  * `GET /api/realtime`: Server-Sent Events (SSE) 실시간 성능 및 쿼리 스트림
* **7.2. 인증 및 세션 관리 엔드포인트**
  * `POST /api/auth/login`: 관리자 이메일/비밀번호 인증 및 JWT 발급
  * `POST /api/auth/refresh`: JWT 갱신
* **7.3. SQL 및 테이블/레코드 CRUD 엔드포인트**
  * `POST /api/sql`: 임의 T-SQL/SQL 쿼리 실행
  * `GET /api/tables`: 테이블 목록 및 컬럼 메타데이터
  * `POST /api/tables/:table/columns`: 비파괴적 컬럼 추가 DDL API
  * `GET /api/records/:table`: 테이블 레코드 페이지네이션, 검색 및 필터링
  * `POST /api/records/:table`, `PUT /api/records/:table`, `DELETE /api/records/:table`: 단건/벌크 CRUD
* **7.4. 활동 감사 및 슬로우 쿼리 로깅 엔드포인트**
  * `GET /api/logs/requests`: 최근 500건의 HTTP/SQL 활동 요청 감사 로그
  * `GET /api/slow-queries`: 임계치(`100ms`) 초과 슬로우 쿼리 내역
* **7.5. 저장 프로시저 호출 엔드포인트**
  * `POST /api/procs/exec`: 매개변수 바인딩 기반 저장 프로시저 원격 RPC 호출
* **7.6. 백업 및 재해 복구 엔드포인트**
  * `POST /api/backup`: 온라인 핫 백업 `.tar.gz` 스트리밍 다운로드
  * `POST /api/restore`: 백업 아카이브 업로드 및 전체 복원
* **7.7. 고가용성 복제 관리 엔드포인트**
  * `GET /api/replication/status`: 복제 역할(Primary/Replica) 및 LSN 상태
  * `GET /api/replication/stream`: Replica 대상 WAL 스트리밍 연결 (Server-Sent Events)
  * `POST /api/replication/promote`: Replica 노드의 Primary 무중단 승격

---

### 제8장. 와이어 프로토콜 기술 사양 (Wire Protocol Specifications)
* **8.1. PostgreSQL Wire Protocol v3 사양**
  * 포트: `5433` (기본 데이터베이스 `postgres`)
  * 지원 패킷: StartupMessage, SSLRequest, PasswordMessage, Query, Parse, Bind, Execute, Describe, Sync, Flush, Terminate
  * 지원 응답: AuthenticationOk, ReadyForQuery, RowDescription, DataRow, CommandComplete, ErrorResponse, NoticeResponse
* **8.2. Microsoft SQL Server TDS 7.x / 8.0 Protocol 사양**
  * 포트: `1433` (기본 데이터베이스 `master`)
  * 패킷 타입: `0x01` (SQLBatch), `0x02` (PreTDS7Login), `0x03` (RPC), `0x04` (Tabular), `0x06` (Attention), `0x0E` (TransactionManager), `0x10` (Login7)
  * 데이터 토큰: `COLMETADATA` (`0x81`), `ROW` (`0xD1`), `NBCROW` (`0xD2`), `DONE` (`0xFD`), `DONEPROC` (`0xFE`), `ENVCHANGE` (`0xE3`), `ERROR` (`0xAA`), `INFO` (`0xAB`), `RETURNSTATUS` (`0x79`), `RETURNVALUE` (`0xAC`)

---

### 제9장. 에러 코드 및 진단 레퍼런스 (Error Codes & Diagnostics)
* **9.1. LionSQL 표준 에러 코드**
  * 내부 에러 코드, 영문 메시지, ANSI SQLSTATE 5자리 매핑 테이블
* **9.2. TDS 에러 코드 및 심각도 (TDS Error Numbers & Severity)**
  * Severity 1~10 (Informational / Warnings), Severity 11~16 (User Execution Errors), Severity 17~25 (System Fatal Errors)
* **9.3. PostgreSQL 에러 코드 및 오류 수준 (PG Error Severity & SQLSTATE)**
  * `08000` (Connection Exception), `23505` (Unique Violation), `40001` (Serialization Failure / Write Conflict), `42P01` (Undefined Table), `53200` (Out of Memory / Storage Capacity Limit Exceeded)

---

## 제1장. 명령행 인터페이스 (CLI Reference)

### 1.1. 단일 바이너리(`bin/lionsql`) 명령 구조

LionSQL은 서브커맨드 패턴(Subcommand Pattern)을 채택하여 서버 구동, 백그라운드 데몬 제어, 시스템 무결성 진단, 스키마 마이그레이션, 터미널 쿼리 실행, 물리 백업 및 복원에 이르는 모든 운영 작업을 단일 실행 바이너리(`bin/lionsql`)로 수행합니다.

```bash
lionsql [command] [flags...]
```

* 명령어를 생략하고 플래그만 전달하거나 인자 없이 실행할 경우 기본 서브커맨드인 `serve`가 자동으로 호출됩니다.
* 모든 플래그는 단일 대시(`-flag`) 및 이중 대시(`--flag`)를 상호 호환하여 사용할 수 있습니다.
* 부울(Boolean) 플래그의 경우 `-flag` 형태로 명시하면 `true`로 설정되며, 비활성화 시에는 `-flag=false` 형태로 명시합니다.

---

### 1.2. 서브커맨드별 상세 명세

#### 1. `serve` (기본 대화형 포그라운드 서버 구동)
서버를 현재 터미널 세션의 포그라운드 프로세스로 기동합니다. 표준 출력(`stdout`)과 표준 에러(`stderr`)를 통해 실시간 엔진 로그가 출력되며, `Ctrl+C`(`SIGINT`) 또는 `SIGTERM` 수신 시 안전한 그레이스풀 셧다운(Graceful Shutdown)을 수행합니다.

* **구문(Syntax)**:
  ```bash
  lionsql [serve] [flags...]
  ```
* **지원 플래그**:
  | 플래그 | 타입 | 기본값 | 설명 |
  | :--- | :---: | :---: | :--- |
  | `-dir` | string | `data` | 데이터 파일 및 트랜잭션 로그(WAL/AOF)가 저장될 물리 디렉토리 경로 |
  | `-http` | string | `:8090` | REST API 및 내장 Web Studio 리스너 TCP 주소 (비활성화: `""`) |
  | `-pg` | string | `:5433` | PostgreSQL Wire Protocol v3 호환 리스너 TCP 주소 (비활성화: `""`) |
  | `-tds` | string | `:1433` | MS SQL Server TDS 7.x/8.0 호환 리스너 TCP 주소 (비활성화: `""`) |
  | `-engine` | string | `wiredtiger` | 기본 스토리지 백엔드 선택 (`wiredtiger` 또는 `memory`) |
  | `-ephemeral` | bool | `false` | 디스크 파일 I/O를 100% 배제하는 초고속 순수 인메모리 휘발성 모드 |
  | `-dev` | bool | `false` | 개발자 모드 활성화 (SQL 쿼리 파싱/실행 계획 상세 콘솔 컬러 출력) |
  | `-max-memory` | string | `""` | 엔진 글로벌 메모리 상한 (예: `2GB`, `4096MB`). 빈 값일 경우 호스트 물리 RAM의 70%로 자동 제한 |
  | `-work-mem` | string | `64MB` | 연산자당 정렬(External Sort) 및 해시 조인 작업 메모리 예산 |
  | `-max-workers` | int | `256` | 클라이언트 요청을 병렬 처리할 고루틴 워커 풀(Worker Pool)의 최대 크기 |
  | `-max-idle-sessions` | int | `64` | 유휴 상태로 유지할 세션 커넥션 풀의 최대 허용 개수 |
  | `-auth` | bool | `true` | 사용자 인증 및 RBAC 인가 계층 활성화 여부 |
  | `-admin-email` | string | `admin@lionsql.local` | 최초 부트스트랩 시 생성될 최고 관리자 계정 이메일 |
  | `-admin-pass` | string | `1234567890` | 최고 관리자 계정 비밀번호 (최초 실행 후 변경 권장) |
  | `-jwt-secret` | string | `""` | REST API 토큰 서명용 HMAC-SHA256 시크릿 (미지정 시 32바이트 난수 자동 발급) |
  | `-auto-seed` | bool | `false` | 데이터 디렉토리가 비어있을 때 E-Commerce 샘플 테이블/데이터 자동 주입 |
  | `-slow-log` | string | `data/slow_queries.log` | 슬로우 쿼리 전용 진단 로그 파일 저장 경로 |
  | `-slow-threshold-ms` | float | `100.0` | 슬로우 쿼리로 판정할 쿼리 실행 소요 시간 기준(밀리초) |
  | `-general-log` | string | `""` | 모든 인바운드 SQL 문장을 실시간 기록할 감사 로그 파일 경로 |
* **실행 예시**:
  ```bash
  # 프로덕션 권장 기동 (2GB 메모리 제한, WiredTiger 엔진, 50ms 슬로우 쿼리 감지)
  lionsql serve -dir /var/lib/lionsql/data -max-memory 2GB -slow-threshold-ms 50.0
  ```

#### 2. `start` (백그라운드 데몬 프로세스 기동)
LionSQL 서버를 운영체제 백그라운드 데몬 프로세스로 분기(Fork/Detached)하여 기동합니다. 표준 출력과 표준 에러는 자동으로 `$DATA_DIR/lionsql.log` 파일로 리다이렉션되며, 프로세스 ID는 `$DATA_DIR/lionsql.pid` 파일에 원자적으로 기록됩니다.

* **구문(Syntax)**:
  ```bash
  lionsql start [-dir <path>] [-dev] [serve flags...]
  ```
* **동작 원리**:
  1. 지정된 데이터 디렉토리 내에 기존 `lionsql.pid` 파일이 존재하는지 검사합니다.
  2. 해당 PID가 실제로 살아있는 프로세스인지 POSIX `kill(pid, 0)` 시그널로 확인하여 중복 구동을 방지합니다.
  3. 백그라운드 프로세스를 생성하고 터미널 표준 입출력과의 연결을 분리합니다.
  4. 데몬 프로세스가 정상적으로 포트를 바인딩하고 리스너를 오픈할 때까지 최대 5초간 헬스체크를 수행한 후 CLI 제어권을 반환합니다.
* **실행 예시 및 출력**:
  ```bash
  $ lionsql start -dir data
  LionSQL daemon started successfully. (PID: 99777)
  Logs redirected to: data/lionsql.log
  Web Studio URL: http://localhost:8090
  PostgreSQL v3: localhost:5433
  MS SQL Server TDS: localhost:1433
  ```

#### 3. `stop` (백그라운드 데몬 안전 종료)
`$DATA_DIR/lionsql.pid` 파일에 기록된 프로세스 ID를 읽어와 서버에 `SIGTERM` 시그널을 전달하고, 안전한 그레이스풀 셧다운 절차를 감시합니다.

* **구문(Syntax)**:
  ```bash
  lionsql stop [-dir <path>]
  ```
* **동작 원리**:
  1. PID 파일을 읽어 프로세스 존재 유무를 확인합니다.
  2. 대상 프로세스에 `SIGTERM` 시그널을 전송합니다.
  3. 최대 1.5초(골든 룰 4 준수) 동안 100ms 주기로 프로세스 종료 상태를 폴링합니다.
  4. 1.5초 이내에 프로세스가 종료되지 않을 경우 `SIGKILL` 시그널로 강제 종료(Escalation)하여 리소스 누수를 방지합니다.
  5. 종료 완료 후 `lionsql.pid` 파일을 안전하게 삭제합니다.
* **실행 예시 및 출력**:
  ```bash
  $ lionsql stop -dir data
  Stopping LionSQL daemon (PID: 99777)...
  LionSQL daemon (PID: 99777) stopped cleanly.
  ```

#### 4. `restart` (백그라운드 데몬 무중단 재기동)
실행 중인 데몬을 안전하게 종료한 후, 동일한 설정 및 디렉토리 인자로 백그라운드 데몬을 즉시 재기동합니다.

* **구문(Syntax)**:
  ```bash
  lionsql restart [-dir <path>] [-dev]
  ```
* **실행 예시 및 출력**:
  ```bash
  $ lionsql restart -dir data
  Stopping LionSQL daemon (PID: 99777)... Done.
  LionSQL daemon started successfully. (PID: 101234)
  ```

#### 5. `status` (데몬 프로세스 상태 조회)
데이터 디렉토리의 PID 파일 및 실제 OS 프로세스 테이블, TCP 포트 바인딩 상태를 검사하여 데몬의 가동 여부와 리소스 사용량을 터미널에 요약 출력합니다.

* **구문(Syntax)**:
  ```bash
  lionsql status [-dir <path>]
  ```
* **출력 예시**:
  ```bash
  $ lionsql status -dir data
  LionSQL Server Status: RUNNING
  ----------------------------------------
  PID               : 99777
  Data Directory    : /Users/maro/Projects/SQLion/data
  Storage Backend   : wiredtiger (Disk B+Tree MVCC)
  HTTP / Studio     : 127.0.0.1:8090 (HEALTHY)
  PostgreSQL v3     : 127.0.0.1:5433 (LISTENING)
  MS SQL Server TDS : 127.0.0.1:1433 (LISTENING)
  Uptime            : 4h 12m 35s
  Heap Allocated    : 142.5 MB / Max: 2048.0 MB (6.9%)
  Active Workers    : 8 / 256
  ```

#### 6. `doctor` (시스템 환경 및 무결성 정밀 진단)
서버 기동 전 또는 운영 중에 데이터 디렉토리 무결성, WAL/AOF LSN 시퀀스, OS 파일 디스크립터 한도, 포트 충돌 여부, 디스크 여유 공간을 5개 영역으로 정밀 진단합니다.

* **구문(Syntax)**:
  ```bash
  lionsql doctor [-dir <path>]
  ```
* **진단 항목**:
  - `[FILESYSTEM]`: 데이터 디렉토리 접근 권한(읽기/쓰기 0755), 남은 디스크 용량 (최소 1GB 이상 권장).
  - `[NETWORKING]`: `:8090`, `:5433`, `:1433` 포트의 점유 상태 및 루프백 바인딩 가능 여부.
  - `[CATALOG & METADATA]`: `_schema.dat`, 카탈로그 파일의 헤더 매직 넘버(`LION_V1`) 및 체크섬 검증.
  - `[WAL / AOF]`: WAL 세그먼트 로그 시퀀스 번호(LSN)의 단조 증가성 및 크래시 복구 로그 유효성.
  - `[RESOURCE GOVERNOR]`: OS `ulimit -n` 파일 디스크립터 한도(권장 65535), 커널 가상 메모리 설정.
* **출력 예시**:
  ```bash
  $ lionsql doctor -dir data
  Checking LionSQL Environment & Storage Integrity...
  [OK] Data directory permissions: rwxr-xr-x (data)
  [OK] Free disk space: 42.8 GB available (Threshold: > 1.0 GB)
  [OK] TCP Port 8090 (HTTP/Studio): AVAILABLE
  [OK] TCP Port 5433 (PGWire): AVAILABLE
  [OK] TCP Port 1433 (TDS): AVAILABLE
  [OK] Catalog schema integrity: PASSED (14 tables, 8 procedures registered)
  [OK] WAL LSN continuity check: LSN=1849201 PASSED
  [OK] OS File Descriptor Limit: 10240 (Recommended: >= 4096)
  Overall Result: HEALTHY (0 errors, 0 warnings)
  ```

#### 7. `init-config` (표준 설정 템플릿 생성)
모든 설정 키에 주석과 기본 권장값이 채워진 `lionsql.json` 설정 파일 템플릿을 생성합니다.

* **구문(Syntax)**:
  ```bash
  lionsql init-config [-out <filepath>]
  ```
  - `-out`: 생성할 설정 파일 경로 (기본값: `lionsql.json`). 이미 파일이 존재하는 경우 덮어쓰지 않고 에러를 반환합니다.

#### 8. `migrate` (선언적 스키마 마이그레이션 도구)
데이터베이스 스키마 변경 버전을 코드화하여 관리하는 CLI 도구입니다.

* **구문(Syntax)**:
  ```bash
  lionsql migrate <action> [flags...]
  ```
  | 액션 | 설명 |
  | :--- | :--- |
  | `new <name>` | 타임스탬프 기반 마이그레이션 파일 쌍(`V{timestamp}__{name}.up.sql`, `.down.sql`) 생성 |
  | `up` | 아직 적용되지 않은 모든 상향 마이그레이션 파일을 순차적으로 트랜잭션 적용 |
  | `down` | 직전에 적용된 마이그레이션을 롤백 (`-steps <n>`으로 단계 지정 가능) |
  | `status` | 현재 적용된 마이그레이션 버전과 대기 중인 파일 목록 출력 |

#### 9. `sql` (경량 인라인 쿼리 실행기)
별도의 `psql`이나 `sqlcmd` 설치 없이 로컬 LionSQL 서버에 즉시 SQL 구문을 전송하여 결과를 터미널 표 형태로 조회합니다.

* **구문(Syntax)**:
  ```bash
  lionsql sql -query "<SQL_STATEMENT>" [-dir <path>]
  ```
* **실행 예시**:
  ```bash
  $ lionsql sql -query "SELECT table_name, row_count FROM sys.tables WHERE is_user_table = 1;"
  +--------------+-----------+
  | table_name   | row_count |
  +--------------+-----------+
  | customers    |      1250 |
  | orders       |      4820 |
  | order_items  |     15200 |
  +--------------+-----------+
  (3 rows affected in 0.82ms)
  ```

#### 10. `backup` (온라인 무중단 핫 백업)
엔진이 동작 중인 상태에서 트랜잭션 일관성을 보장하며 데이터 디렉토리와 WAL 체크포인트를 압축 아카이브(`.tar.gz`)로 패키징합니다.

* **구문(Syntax)**:
  ```bash
  lionsql backup -out <archive_path.tar.gz> [-dir <path>]
  ```
* **실행 예시**:
  ```bash
  $ lionsql backup -out /backups/lionsql_20261019.tar.gz -dir data
  Initiating online storage checkpoint... Done.
  Archiving data directory to /backups/lionsql_20261019.tar.gz...
  Backup completed successfully. (Size: 45.2 MB, Checkpoint LSN: 1849300)
  ```

#### 11. `restore` (물리 백업 아카이브 복원)
생성된 백업 아카이브를 지정된 데이터 디렉토리에 압축 해제하고 무결성을 검증합니다.

* **구문(Syntax)**:
  ```bash
  lionsql restore -in <archive_path.tar.gz> -dir <target_data_dir>
  ```
  > [!WARNING] 대상 데이터 디렉토리가 이미 존재하는 경우 데이터 손실을 방지하기 위해 복원이 중단됩니다. 새로운 경로를 지정하거나 기존 디렉토리를 비워야 합니다.

#### 12. `version` (엔진 버전 및 빌드 메타데이터)
LionSQL 바이너리의 정식 릴리즈 버전, 빌드 태그, 대상 OS/아키텍처, Git 커밋 해시를 출력합니다.

* **구문(Syntax)**:
  ```bash
  lionsql version
  ```
* **출력 예시**:
  ```bash
  LionSQL v0.90.0 (darwin/arm64) [tags: wiredtiger]
  Git Commit: 477a4cb6
  Go Version: go1.24.0
  Build Date: 2026-10-19T08:30:00Z
  Storage Backends: WiredTiger MVCC, In-Memory OCC
  ```

---

### 1.3. 전역 CLI 플래그 분류별 사양

| 플래그 명칭 | 데이터 타입 | 기본값 | 환경 변수 매핑 | 상세 설명 |
| :--- | :---: | :---: | :--- | :--- |
| **네트워크 설정** | | | | |
| `-http` | string | `:8090` | `LION_HTTP_ADDR` | HTTP REST API 및 Web Studio 리스너 주소. 빈 문자열(`""`) 시 비활성화 |
| `-pg` | string | `:5433` | `LION_PG_ADDR` | PostgreSQL Wire Protocol v3 호환 TCP 리스너 주소. 빈 문자열 시 비활성화 |
| `-tds` | string | `:1433` | `LION_TDS_ADDR` | Microsoft SQL Server TDS 7.x/8.0 호환 TCP 리스너 주소. 빈 문자열 시 비활성화 |
| `-tls` | bool | `false` | `LION_TLS_ENABLED` | 전송 계층 보안(TLS/SSL) 암호화 레이어 활성화 |
| `-tls-cert` | string | `""` | `LION_TLS_CERT` | X.509 공개 키 인증서 파일 경로 (PEM 포맷) |
| `-tls-key` | string | `""` | `LION_TLS_KEY` | 개인 키 파일 경로 (PEM 포맷) |
| `-require-ssl` | bool | `false` | `LION_REQUIRE_SSL` | 비암호화 일반 텍스트 연결을 강제 거부하고 SSL 연결만 허용 |
| **스토리지 설정** | | | | |
| `-dir` | string | `data` | `LION_DATA_DIR` | 데이터 파일, 카탈로그, WAL 로그가 위치할 물리 디렉토리 경로 |
| `-engine` | string | `wiredtiger` | `LION_ENGINE` | 기본 스토리지 백엔드 선택 (`wiredtiger`: 디스크 B+Tree MVCC, `memory`: 인메모리 OCC) |
| `-ephemeral` | bool | `false` | `LION_EPHEMERAL` | 디스크 파일 I/O를 배제하는 순수 인메모리 휘발성 모드 |
| `-auto-seed` | bool | `false` | `LION_AUTO_SEED` | 최초 실행 시 E-Commerce 샘플 데이터셋 자동 생성 |
| **자원 및 거버너** | | | | |
| `-max-memory` | string | `""` | `LION_MAX_MEMORY` | 엔진 글로벌 메모리 상한 (예: `2GB`, `4096MB`). 미지정 시 시스템 가용 RAM의 70% |
| `-work-mem` | string | `64MB` | `LION_WORK_MEM` | 단일 연산자(External Merge Sort, Hash Join)의 메모리 예산 |
| `-max-workers` | int | `256` | `LION_MAX_WORKERS` | 비동기 고루틴 워커 풀의 최대 동시 워커 수 |
| `-max-idle-sessions` | int | `64` | `LION_MAX_IDLE_SESSIONS` | 풀에 보관할 유휴 세션 연결 수 |
| **보안 및 인증** | | | | |
| `-auth` | bool | `true` | `LION_AUTH_ENABLED` | 보안 인증 및 권한 인가 계층 활성화 여부 |
| `-admin-email` | string | `admin@lionsql.local`| `LION_ADMIN_EMAIL` | 부트스트랩 시 생성될 관리자 이메일 계정 |
| `-admin-pass` | string | `1234567890` | `LION_ADMIN_PASS` | 관리자 로그인 비밀번호 |
| `-jwt-secret` | string | `""` | `LION_JWT_SECRET` | REST API JWT 서명 시크릿 (미지정 시 자동 생성) |
| **로깅 및 감사** | | | | |
| `-dev` | bool | `false` | `LION_DEV_MODE` | 실시간 컬러풀 쿼리 트레이싱 및 디버그 로깅 활성화 |
| `-slow-log` | string | `data/slow_queries.log` | `LION_SLOW_LOG` | 지연 쿼리 전용 로그 파일 경로 |
| `-slow-threshold-ms` | float | `100.0` | `LION_SLOW_THRESHOLD_MS` | 슬로우 쿼리 판정 기준 시간(밀리초) |
| `-general-log` | string | `""` | `LION_GENERAL_LOG` | 전수 쿼리 감사 로그 파일 경로 |
| **고가용성 및 복제** | | | | |
| `-replica-of` | string | `""` | `LION_REPLICA_OF` | WAL 스트리밍을 수신할 Primary 노드 URL (예: `http://10.0.0.1:8090`) |
| `-node-id` | string | `node-1` | `LION_NODE_ID` | 클러스터 내 고유 노드 식별자 |
| `-auto-failover` | bool | `false` | `LION_AUTO_FAILOVER` | Primary 노드 장애 시 Replica 자동 무중단 승격 활성화 |
| `-peers` | string | `""` | `LION_PEERS` | 헬스체크 및 투표를 위한 피어 노드 URL 목록 (쉼표 구분) |
| `-heartbeat-timeout-ms` | int | `3000` | `LION_HEARTBEAT_TIMEOUT_MS` | 리더 하트비트 장애 판단 타임아웃(밀리초) |

---

### 1.4. 프로세스 라이프사이클 및 시그널 처리 규약

LionSQL은 골든 룰 2(Panic Recovery) 및 골든 룰 4(Clean Teardown)를 준수하여 모든 런타임 종료 및 시그널을 안전하게 처리합니다.

* **표준 종료 코드 (Exit Codes)**:
  - `0 (EX_OK)`: 정상 종료 (`SIGINT`, `SIGTERM` 수신 후 정상 드레인 완료).
  - `1 (EX_GENERAL)`: 일반 런타임 오류 (TCP 포트 바인딩 실패, 스토리지 파일 잠금 실패, 디스크 I/O 오류).
  - `2 (EX_USAGE)`: 잘못된 CLI 플래그, 인식할 수 없는 파라미터 값, 잘못된 서브커맨드.
  - `3 (EX_DATAERR)`: 스키마 카탈로그 무결성 실패, 크래시 복구 불가능한 트랜잭션 로그 손상.
* **POSIX 시그널 처리 규약**:
  - `SIGINT` (`Ctrl+C`) / `SIGTERM`:
    1. 인바운드 TCP 리스너(`:8090`, `:5433`, `:1433`)를 즉시 닫아 신규 연결 유입을 차단합니다.
    2. 현재 실행 중인 활성 쿼리 및 트랜잭션이 완료될 때까지 최대 1.0초간 기다립니다(드레인 단계).
    3. 메모리 버퍼의 더티 페이지와 트랜잭션 WAL/AOF를 디스크로 강제 플러시(`fsync`)합니다.
    4. WiredTiger 및 메모리 스토리지 엔진 핸들을 닫고 프로세스를 종료합니다.
    5. 전체 절차가 1.5초를 초과할 경우 백그라운드 워처 고루틴이 자체적으로 `SIGKILL`을 발생시켜 안전하게 프로세스를 강제 종료합니다.
  - `SIGHUP`:
    - 로그 파일 디스크립터(`lionsql.log`, `slow_queries.log`)를 재오픈(Log Rotate 지원)하고, 변경된 `lionsql.json` 설정을 핫 리로드(Hot Reload)합니다.

---

### 1.5. 벤치마크 및 부하 검증 CLI 사양 (`bin/bench`, `bin/tpcc_vdbe`)

LionSQL 소스 트리는 고동시성 OLTP 성능과 TPC-C 트랜잭션 적합성을 검증하기 위한 2개의 독립 벤치마크 바이너리를 제공합니다.

#### 1. `bin/bench` (고동시성 계좌 이체 OLTP 마이크로 벤치마크)
* **목적**: 10,000개 이상의 계좌 간 무작위 자금 이체 트랜잭션을 수만 번 동시 실행하여 OCC/MVCC 충돌 처리량과 TPS를 측정합니다.
* **주요 플래그**:
  - `-accounts <int>`: 초기 생성할 계좌 수 (기본값: `10000`)
  - `-workers <int>`: 병렬로 트랜잭션을 전송할 클라이언트 고루틴 워커 수 (기본값: `100`)
  - `-ops <int>`: 벤치마크 실행 총 트랜잭션 횟수 (기본값: `50000`)
  - `-engine <name>`: 대상 스토리지 백엔드 (`wiredtiger` 또는 `memory`)
* **실행 예시**:
  ```bash
  go build -tags wiredtiger -o bin/bench ./cmd/bench
  ./bin/bench -workers 200 -ops 100000 -engine wiredtiger
  ```

#### 2. `bin/tpcc_vdbe` (공식 TPC-C T-SQL VDBE 벤치마크)
* **목적**: New-Order, Payment, Order-Status 등 표준 TPC-C 비즈니스 트랜잭션을 단일 VDBE 바이트코드 프로그램(`exec.RunInTx`)으로 구동하여 완전한 ACID 원자성을 검증합니다.
* **주요 플래그**:
  - `-warehouses <int>`: TPC-C 규모 계수 (Warehouse 수, 기본값: `2`)
  - `-workers <int>`: 동시 클라이언트 워커 수 (기본값: `20`)
  - `-duration <duration>`: 벤치마크 실행 시간 (예: `30s`, `2m`, 기본값: `10s`)
  - `-fsync <mode>`: WAL 디스크 동기화 모드 (`everysec`: 1초마다 동기화, `always`: 커밋마다 fsync, `off`: OS 버퍼링)
* **실행 예시**:
  ```bash
  go build -tags wiredtiger -o bin/tpcc_vdbe ./cmd/tpcc_vdbe
  ./bin/tpcc_vdbe -warehouses 5 -workers 50 -duration 1m -fsync everysec
  ```

---

## 제2장. 설정 파일 사양 (Configuration File Reference)

### 2.1. 설정 우선순위 및 계층 구조

LionSQL은 4단계의 결정론적 계층 구조(Deterministic Hierarchy, 골든 룰 37 준수)를 통해 설정을 최종 확정합니다:

$$\text{CLI Flags} > \text{Environment Variables} > \text{Config File (lionsql.json)} > \text{Built-in Defaults}$$

1. **CLI 플래그**: 명령행에서 명시된 인자가 최우선 적용됩니다.
2. **환경 변수**: `LION_` 접두사가 붙은 환경 변수가 설정 파일의 값을 오버라이드합니다.
3. **설정 파일 (`lionsql.json`)**: 작업 디렉토리 또는 `-config`로 지정된 JSON 파일에서 설정을 파싱합니다.
4. **내장 기본값**: 상위 설정이 모두 누락되었을 때 엔진 내부에 정의된 안전 기본값(Safe Defaults)이 적용됩니다.

---

### 2.2. `lionsql.json` 완벽한 스키마 정의

다음은 LionSQL의 모든 구성 섹션을 포함하는 정규 `lionsql.json` 파일의 전체 스키마입니다:

```json
{
  "server": {
    "engine": "wiredtiger",
    "data_dir": "data",
    "ephemeral": false,
    "auto_seed": false,
    "pid_file": "data/lionsql.pid",
    "graceful_timeout_seconds": 1.5
  },
  "network": {
    "http_addr": ":8090",
    "pg_addr": ":5433",
    "tds_addr": ":1433",
    "tls": {
      "enabled": false,
      "cert_file": "",
      "key_file": "",
      "require_ssl": false
    }
  },
  "auth": {
    "enabled": true,
    "admin_email": "admin@lionsql.local",
    "admin_pass": "",
    "jwt_secret": "",
    "jwt_expiry_hours": 72
  },
  "resources": {
    "max_memory": "",
    "work_mem": "64MB",
    "max_workers": 256,
    "max_idle_sessions": 64,
    "memory_pressure_threshold_pct": 85
  },
  "logging": {
    "dev_mode": false,
    "slow_query": {
      "enabled": true,
      "threshold_ms": 100.0,
      "file": "data/slow_queries.log"
    },
    "general_query": {
      "enabled": false,
      "file": "data/general_query.log"
    }
  },
  "replication": {
    "enabled": false,
    "role": "PRIMARY",
    "node_id": "node-1",
    "primary_url": "",
    "auto_failover": false,
    "peers": [],
    "self_url": "",
    "heartbeat_interval_ms": 1000,
    "heartbeat_timeout_ms": 3000
  },
  "smtp": {
    "host": "",
    "port": 587,
    "username": "",
    "password": "",
    "from": "",
    "tls": true
  }
}
```

---

### 2.3. 섹션별 상세 파라미터 규격

#### 1. `server` 섹션
* **`engine`** (string): 스토리지 백엔드 엔진 식별자.
  - `"wiredtiger"`: 디스크 기반 Lock-free B+Tree 및 MVCC 엔진 (기본값).
  - `"memory"`: 인메모리 낙관적 동시성 제어(OCC) 엔진 (초저지연 워크로드).
* **`data_dir`** (string): 데이터베이스 데이터 파일, WAL 로그, 카탈로그가 위치할 경로 (기본값: `"data"`).
* **`ephemeral`** (bool): `true` 설정 시 디스크 기록을 완전히 중단하고 휘발성 RAM에서만 구동 (테스트 환경 권장).
* **`auto_seed`** (bool): `true`일 경우 데이터베이스 초기화 시 E-Commerce 데모 테이블을 자동 생성 (기본값: `false`).
* **`pid_file`** (string): 데몬 모드 구동 시 프로세스 ID를 기록할 파일 경로 (기본값: `"data/lionsql.pid"`).
* **`graceful_timeout_seconds`** (float): 종료 신호 수신 시 활성 트랜잭션 완료를 대기할 최대 시간 (기본값: `1.5`).

#### 2. `network` 섹션
* **`http_addr`** (string): REST API 및 내장 Web Studio 리스너 TCP 주소 (기본값: `":8090"`).
* **`pg_addr`** (string): PostgreSQL Wire Protocol v3 호환 TCP 리스너 주소 (기본값: `":5433"`).
* **`tds_addr`** (string): MS SQL Server TDS 7.x/8.0 호환 TCP 리스너 주소 (기본값: `":1433"`).
* **`tls.enabled`** (bool): 전송 계층 보안 암호화 활성화 여부 (기본값: `false`).
* **`tls.cert_file`** (string): PEM 포맷 X.509 공개키 인증서 파일 경로.
* **`tls.key_file`** (string): PEM 포맷 개인키 파일 경로.
* **`tls.require_ssl`** (bool): 클라이언트의 일반 텍스트 연결 시도를 강제 차단할지 여부 (기본값: `false`).

#### 3. `auth` 섹션
* **`enabled`** (bool): 보안 인증 및 인가 레이어 가동 여부 (기본값: `true`).
* **`admin_email`** (string): 최고 관리자 계정 이메일 주소 (기본값: `"admin@lionsql.local"`).
* **`admin_pass`** (string): 관리자 로그인 비밀번호 (비어 있을 경우 기존 비밀번호를 유지하거나 기본값 적용).
* **`jwt_secret`** (string): JWT 토큰 서명에 사용되는 HMAC-SHA256 시크릿 키.
* **`jwt_expiry_hours`** (int): 발급된 JWT 세션 토큰의 유효 기간(시간 단위, 기본값: `72`).

#### 4. `resources` 섹션 (메모리 거버너 및 커넥션 풀링)
* **`max_memory`** (string): 엔진 전체 프로세스가 사용할 수 있는 힙 메모리의 절대 상한선 (예: `"2GB"`, `"4096MB"`). 비어 있을 경우 호스트 물리 메모리의 70%를 상한선으로 자동 산정합니다.
* **`work_mem`** (string): 정렬(External Sort), 인덱스 빌드, 해시 조인 시 단일 쿼리 연산자가 사용할 수 있는 인메모리 버퍼 예산 (기본값: `"64MB"`).
* **`max_workers`** (int): 클라이언트 요청을 처리하기 위해 동시에 가동할 수 있는 고루틴 워커 풀의 크기 (기본값: `256`).
* **`max_idle_sessions`** (int): 유휴 상태로 대기할 수 있는 클라이언트 세션 풀의 최대 크기 (기본값: `64`).
* **`memory_pressure_threshold_pct`** (int): 메모리 압박 경고 임계치 백분율 (기본값: `85`). 이 임계치를 초과하면 저우선순위 캐시가 강제 제거(Eviction)되며 신규 대규모 쿼리의 버퍼 할당이 제한됩니다.

#### 5. `logging` 섹션
* **`dev_mode`** (bool): `true` 설정 시 SQL 파싱 트리, VDBE 바이트코드 디스어셈블리, CBO 실행 계획이 터미널에 상세 출력됩니다.
* **`slow_query.enabled`** (bool): 임계치를 초과하는 슬로우 쿼리의 파일 로깅 활성화 여부 (기본값: `true`).
* **`slow_query.threshold_ms`** (float): 지연 쿼리 기록 판정 시간 (기본값: `100.0` ms).
* **`slow_query.file`** (string): 슬로우 쿼리가 기록될 파일 경로 (기본값: `"data/slow_queries.log"`).
* **`general_query.enabled`** (bool): 인바운드 모든 쿼리를 기록할 전수 감사 로깅 활성화 여부 (기본값: `false`).
* **`general_query.file`** (string): 전수 감사 로그 파일 경로.

#### 6. `replication` 섹션 (고가용성 및 복제)
* **`enabled`** (bool): 복제 서브시스템 가동 여부 (기본값: `false`).
* **`role`** (string): 노드의 복제 역할 (`"PRIMARY"` 또는 `"REPLICA"`).
* **`node_id`** (string): 클러스터 내에서 노드를 구분하는 고유 식별자 (예: `"node-1"`).
* **`primary_url`** (string): 노드가 `REPLICA`일 때 WAL 스트리밍을 수신할 Primary 노드의 HTTP URL.
* **`auto_failover`** (bool): Primary 노드 응답 실패 시 Replica가 스스로 Primary로 승격할지 여부 (기본값: `false`).
* **`peers`** (array of string): 클러스터 내 다른 피어 노드들의 HTTP URL 목록.
* **`heartbeat_interval_ms`** (int): 피어 간 하트비트 전송 주기(밀리초, 기본값: `1000`).
* **`heartbeat_timeout_ms`** (int): 리더 노드가 무응답일 때 장애로 판단할 시간(밀리초, 기본값: `3000`).

#### 7. `smtp` 섹션 (내장 트랜잭션 메일러)
* **`host`** (string): SMTP 서버 호스트명 또는 IP (예: `"smtp.gmail.com"`).
* **`port`** (int): SMTP 포트 번호 (기본값: `587`).
* **`username`** (string): SMTP 인증 사용자 이름.
* **`password`** (string): SMTP 인증 비밀번호 또는 앱 전용 패스워드.
* **`from`** (string): 발신자 표시 이메일 주소 (예: `"no-reply@lionsql.local"`).
* **`tls`** (bool): STARTTLS 연결 암호화 강제 여부 (기본값: `true`).

---

### 2.4. 옵션 조합별 트랜잭션 성능 및 워크로드 프로파일 요약

LionSQL은 스토리지 엔진(`-engine`)과 WAL 동기화 모드(`-fsync`), 작업 메모리(`-work-mem`)의 조합에 따라 극단적인 인메모리 초저지연부터 절대 무손실 금융 원장까지 최적화된 트랜잭션 프로파일을 제공합니다.

| 워크로드 프로파일 | 권장 옵션 조합 | 트랜잭션 처리량 (TPS) | P99 지연 시간 | 데이터 내구성 (RPO) | 주요 적용 유스케이스 |
| :--- | :--- | :---: | :---: | :---: | :--- |
| **초저지연 인메모리** | `-engine memory`<br>`-ephemeral`<br>`-fsync off` | **20,000 ~ 25,000+ TPS**<br>(> 540k tpmC) | **< 1.0 ms** | 휘발성 (N/A) | 게임 세션, 캐시 스토어, CI 고속 테스트 |
| **표준 프로덕션 OLTP**<br>(권장 기본값) | `-engine wiredtiger`<br>`-fsync everysec`<br>`-max-memory 4GB` | **12,000 ~ 16,800+ TPS**<br>(~ 457k tpmC) | **< 10 ms** | 최대 1초 (1s Fsync) | 일반 SaaS, 이커머스 주문/고객 관리 |
| **무손실 금융 원장** | `-engine wiredtiger`<br>`-fsync always`<br>`-max-workers 128` | **1,700 ~ 1,860+ TPS**<br>(Group Commit 적용) | **15 ~ 25 ms** | **RPO = 0 (무손실)** | 금융 이체, 결제 승인, 감사 원장 |
| **대용량 분석/배치** | `-engine wiredtiger`<br>`-work-mem 256MB` | **디스크 스필 배제** | 배치 최적화 | 최대 1초 | 대규모 ETL, 마감 정산 집계 |

> [!TIP]
> 하드웨어 스펙별 배포 매트릭스, Group Commit 동작 원리 및 지연 쿼리 튜닝에 관한 상세 분석은 [`TRANSACTION_PERFORMANCE_GUIDE.ko.md`](TRANSACTION_PERFORMANCE_GUIDE.ko.md) 문서를 참조하십시오.

---

## 제3장. 데이터 타입 및 리터럴 (Data Types & Literals Reference)

### 3.1. 원시 데이터 타입 상세 사양

LionSQL은 Microsoft SQL Server T-SQL 및 ANSI/PostgreSQL 표준 데이터 타입을 광범위하게 지원하며, 메모리 및 디스크 직렬화 시 가변 길이 정수 인코딩과 컴팩트 압축 레이아웃을 사용합니다.

#### 1. 정수형 및 수치 데이터 타입
| 데이터 타입 | 물리 저장 바이트 | 유효 범위 및 기술 사양 | 리터럴 표기 예시 |
| :--- | :---: | :--- | :--- |
| **`TINYINT`** | 1 Byte | $0 \sim 255$ (부호 없는 8비트 정수) | `0`, `255` |
| **`SMALLINT`** | 2 Bytes | $-32,768 \sim 32,767$ (부호 있는 16비트 정수, ZigZag 인코딩) | `-100`, `32767` |
| **`INT` / `INTEGER`**| 4 Bytes | $-2,147,483,648 \sim 2,147,483,647$ (부호 있는 32비트 정수) | `1001`, `-42` |
| **`BIGINT`** | 8 Bytes | $-9,223,372,036,854,775,808 \sim 9,223,372,036,854,775,807$ (64비트) | `9223372036854775807` |
| **`FLOAT` / `REAL`** | 8 Bytes | IEEE 754 64비트 배정밀도 부동소수점 ($1.7 \times 10^{-308} \sim 1.7 \times 10^{308}$) | `3.141592`, `-0.001` |
| **`DECIMAL(p, s)`** | 가변 길이 | 최대 38자리 고정소수점 정밀 수치 ($p$: 전체 자릿수 $1 \sim 38$, $s$: 소수점 자릿수 $0 \sim p$) | `12345.67`, `0.0001` |
| **`NUMERIC(p, s)`** | 가변 길이 | `DECIMAL(p, s)`과 동일한 사양 및 정밀도 | `99.99` |

* **`DECIMAL` 내부 구조**: 부호 비트(1비트), 스케일(1바이트), 그리고 128비트 부호 없는 정수(비트 배열)로 구성되어 부동소수점 오차 없는 완벽한 금융 회계 계산을 보장합니다.

#### 2. 문자열 및 대용량 텍스트 타입
| 데이터 타입 | 물리 저장 바이트 | 유효 범위 및 기술 사양 | 리터럴 표기 예시 |
| :--- | :---: | :--- | :--- |
| **`CHAR(n)`** | 고정 $n$ Bytes | 최대 8,000바이트 고정 길이 문자열 (공백 패딩 유지) | `'KR  '`, `'USD '` |
| **`VARCHAR(n)`** | 가변 ($2 + L$) Bytes | 최대 8,000바이트 가변 길이 UTF-8 문자열 ($L \le n$) | `'Hello World'`, `'LionSQL'` |
| **`NVARCHAR(n)`** | 가변 ($2 + 2L$) Bytes| 최대 4,000자 유니코드 문자열 (T-SQL 호환 N-prefix 지원) | `N'안녕하세요'`, `N'데이터베이스'` |
| **`TEXT`** | 가변 (PLP 청크) | 최대 2GB 무제한 대용량 텍스트 (부분 청크 스트리밍 지원) | `'Very long description...'` |

#### 3. 일시(Date & Time) 데이터 타입
| 데이터 타입 | 물리 저장 바이트 | 유효 범위 및 정밀도 | 리터럴 표기 예시 |
| :--- | :---: | :--- | :--- |
| **`DATE`** | 4 Bytes | `0001-01-01` ~ `9999-12-31` (1일 단위 정밀도) | `'2026-10-19'` |
| **`TIME`** | 8 Bytes | `00:00:00.000000` ~ `23:59:59.999999` (마이크로초 정밀도) | `'17:30:00.123456'` |
| **`DATETIME`** | 8 Bytes | `1753-01-01 00:00:00` ~ `9999-12-31 23:59:59` (밀리초 정밀도) | `'2026-10-19 17:30:00'` |
| **`TIMESTAMP`** | 8 Bytes | Unix 에포크 마이크로초 정수 또는 행 버전 토큰 | `'2026-10-19 17:30:00.000'` |

#### 4. 논리 및 고유 식별자 타입
| 데이터 타입 | 물리 저장 바이트 | 유효 범위 및 기술 사양 | 리터럴 표기 예시 |
| :--- | :---: | :--- | :--- |
| **`BIT` / `BOOLEAN`**| 1 Byte | `0` (FALSE) 또는 `1` (TRUE) | `1`, `0`, `TRUE`, `FALSE` |
| **`UUID`** | 16 Bytes | 128비트 범용 고유 식별자 (RFC 9562 UUIDv7 시간순 정렬 최적화) | `'0192a5b6-7c8d-7e9f-8a0b-1c2d3e4f5a6b'` |
| **`JSON`** | 가변 길이 | 유효한 JSON 문자열 검증 및 파싱, B-Tree 가상 함수 인덱싱 지원 | `'{"user_id": 101, "role": "admin"}'` |

---

### 3.2. 물리 바이너리 직렬화 규격 (`pkg/storage/tuple.go`)

LionSQL의 스토리지 엔진(인메모리 OCC 및 WiredTiger)은 디스크 용량 절감과 CPU 직렬화 오버헤드 최소화를 위해 다음의 고효율 압축 바이너리 포맷을 공유합니다:

```
+---------------+------------------------+--------------------------+-----------------------+
| Version (1B)  | Null Bitmap (ceil(N/8))| Fixed Columns (Byte-pad) | Variable-length Data  |
+---------------+------------------------+--------------------------+-----------------------+
```

1. **Header Byte (`0x01`)**: 튜플 인코더 버전 식별자.
2. **Null Bitmap**: 컬럼 수 $N$에 대해 $\lceil N/8 \rceil$ 바이트 할당. 각 비트가 `1`이면 해당 위치의 컬럼이 `NULL`임을 의미하며, 데이터 페이로드 영역에는 0바이트만 점유합니다.
3. **Fixed Columns**: `TINYINT`, `INT`, `BIGINT`, `FLOAT`, `DATETIME` 등 고정 크기 데이터는 ZigZag 가변 길이 정수 인코딩을 통해 작은 정수(예: `1`, `42`)는 1~2바이트로 대폭 압축 저장됩니다.
4. **Variable Data**: `VARCHAR`, `TEXT`, `DECIMAL`, `JSON` 등은 2바이트 길이 프리픽스와 원시 바이트 슬라이스로 연속 저장되어 포인터 추적 오버헤드를 배제합니다.

---

### 3.3. 데이터 타입 변환 매트릭스 (Type Conversion Matrix)

| 출발 타입 \ 목표 타입 | `INT` | `BIGINT` | `DECIMAL` | `FLOAT` | `VARCHAR` | `DATETIME` | `BIT` |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **`TINYINT` / `INT`** | - | 암시적 | 암시적 | 암시적 | `CAST` | 오류 | 암시적 ($0/1$) |
| **`BIGINT`** | 오버플로 주의 | - | 암시적 | 암시적 | `CAST` | 오류 | 암시적 |
| **`DECIMAL`** | 절삭 | 절삭 | - | 암시적 | `CAST` | 오류 | 오류 |
| **`FLOAT`** | 절삭 | 절삭 | `CAST` | - | `CAST` | 오류 | 오류 |
| **`VARCHAR`** | 파싱 변환 | 파싱 변환 | 파싱 변환 | 파싱 변환 | - | ISO 파싱 | `'1'`/`'true'` |
| **`DATETIME`** | 오류 | Unix Epoch | 오류 | 오류 | `CAST`/`CONVERT` | - | 오류 |

---

## 제4장. SQL 구문 및 명령문 사양 (SQL Statement Reference)

### 4.1. 데이터 정의어 (DDL)

#### 1. `CREATE TABLE` (테이블 정의)
데이터베이스에 새로운 물리 테이블 스키마를 정의합니다. 기본 키(PRIMARY KEY), 단조 증가 자동 번호(IDENTITY), 체크 제약조건(CHECK), 외래 키(FOREIGN KEY) 연쇄 동작을 완벽히 지원합니다.

* **구문(BNF)**:
  ```sql
  CREATE TABLE [schema_name.]table_name (
      column_name data_type [NOT NULL | NULL]
          [IDENTITY [(seed, increment)]]
          [PRIMARY KEY]
          [DEFAULT default_expression]
          [CHECK (boolean_expression)],
      ...,
      [CONSTRAINT constraint_name PRIMARY KEY (col1, col2, ...)],
      [CONSTRAINT constraint_name FOREIGN KEY (col1, ...) REFERENCES ref_table(ref_col1, ...)
          [ON DELETE {CASCADE | SET NULL | NO ACTION}]
          [ON UPDATE {CASCADE | NO ACTION}]]
  );
  ```
* **실행 예시**:
  ```sql
  CREATE TABLE orders (
      order_id INT IDENTITY(1,1) PRIMARY KEY,
      customer_id INT NOT NULL,
      order_date DATETIME DEFAULT GETDATE(),
      total_amount DECIMAL(18,2) NOT NULL CHECK (total_amount >= 0.00),
      status VARCHAR(20) DEFAULT 'PENDING',
      CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) 
          REFERENCES customers(customer_id) ON DELETE CASCADE
  );
  ```

#### 2. `ALTER TABLE` (테이블 스키마 변경)
기존 테이블에 신규 컬럼을 추가하거나 기존 컬럼을 제거합니다. LionSQL의 메타데이터 카탈로그는 원자적 카탈로그 스왑을 통해 락 경합 없이 서브밀리초 단위로 컬럼을 추가합니다.

* **구문**:
  ```sql
  ALTER TABLE table_name ADD column_name data_type [DEFAULT default_value] [NOT NULL];
  ALTER TABLE table_name DROP COLUMN column_name;
  ```
* **실행 예시**:
  ```sql
  ALTER TABLE customers ADD phone_number VARCHAR(30) DEFAULT 'N/A';
  ALTER TABLE customers DROP COLUMN legacy_fax;
  ```

#### 3. `DROP TABLE` / `TRUNCATE TABLE` (테이블 삭제 및 절단)
* **`DROP TABLE`**: 테이블 메타데이터와 물리 데이터 파일을 완전히 파기합니다.
  ```sql
  DROP TABLE [IF EXISTS] table_name;
  ```
* **`TRUNCATE TABLE`**: 테이블 스키마 구조는 보존하면서 모든 행 데이터를 즉시 삭제하고 `IDENTITY` 카운터를 초기 시드 값으로 리셋합니다.
  ```sql
  TRUNCATE TABLE table_name;
  ```

#### 4. `CREATE INDEX` / `DROP INDEX` (B+Tree 인덱스 관리)
단일 컬럼, 복합 컬럼, 유니크 인덱스 및 인덱스 리프 노드에 비키 컬럼을 탑재하는 커버링 인덱스(`INCLUDE`)를 지원합니다.

* **구문**:
  ```sql
  CREATE [UNIQUE] INDEX index_name ON table_name (col1 [ASC|DESC], col2, ...)
      [INCLUDE (col3, col4, ...)];
  
  DROP INDEX index_name [ON table_name];
  ```
* **실행 예시**:
  ```sql
  -- 커버링 인덱스를 통한 테이블 룩업(Bookmark Lookup) 제거 최적화
  CREATE INDEX idx_orders_cust_date ON orders (customer_id ASC, order_date DESC)
      INCLUDE (total_amount, status);
  ```

#### 5. `CREATE PROCEDURE` / `ALTER PROCEDURE` (T-SQL 저장 프로시저 정의)
T-SQL 저장 프로시저를 선언하고 VDBE 바이트코드로 컴파일하여 카탈로그에 등록합니다. 매개변수 선언 및 `OUTPUT` 파라미터를 지원합니다.

* **구문**:
  ```sql
  CREATE [OR ALTER] PROCEDURE [schema_name.]proc_name
      [@param1 data_type [= default_val] [OUTPUT], ...]
      [WITH RECOMPILE]
  AS
  BEGIN
      SET NOCOUNT ON;
      -- 프로시저 실행 본문 (변수 선언, IF/WHILE 제어문, DML, 커서 등)
  END;
  ```

---

### 4.2. 데이터 조작어 (DML)

#### 1. `SELECT` (데이터 질의)
복잡한 필터링, 그룹 집계, 윈도우 분석, 다중 테이블 조인 및 페이지네이션을 지원합니다.

* **구문**:
  ```sql
  SELECT [TOP (n) [PERCENT]] [DISTINCT] select_list
  FROM table_source [WITH (INDEX(index_name))]
  [[INNER | LEFT | RIGHT | FULL] JOIN join_table ON join_condition ...]
  [WHERE search_condition]
  [GROUP BY group_by_list [HAVING having_condition]]
  [ORDER BY order_expression [ASC | DESC], ...]
  [OFFSET offset_value ROWS FETCH NEXT fetch_value ROWS ONLY];
  ```
* **실행 예시**:
  ```sql
  SELECT TOP 10 
      c.customer_id,
      c.name,
      COUNT(o.order_id) AS total_orders,
      SUM(o.total_amount) AS gross_spend
  FROM customers c
  INNER JOIN orders o ON c.customer_id = o.customer_id
  WHERE o.status = 'COMPLETED' AND o.order_date >= '2026-01-01'
  GROUP BY c.customer_id, c.name
  HAVING SUM(o.total_amount) >= 1000.00
  ORDER BY gross_spend DESC
  OFFSET 0 ROWS FETCH NEXT 10 ROWS ONLY;
  ```

#### 2. `INSERT` (레코드 삽입)
단일 행, 다중 행, 하위 쿼리 결과 삽입(`INSERT INTO ... SELECT`), 그리고 기본값 삽입을 지원합니다.

* **구문**:
  ```sql
  INSERT INTO table_name [(column_list)] VALUES (value_list), (value_list_2), ...;
  INSERT INTO table_name [(column_list)] SELECT select_list FROM ...;
  INSERT INTO table_name DEFAULT VALUES;
  ```

#### 3. `UPDATE` (레코드 수정)
조건절 기반 단일 테이블 수정 및 다른 테이블과의 조인 결과를 참조하여 갱신하는 ANSI 확장 구문을 지원합니다.

* **구문**:
  ```sql
  UPDATE table_name
  SET col1 = expr1, col2 = expr2, ...
  [FROM table_name t1 JOIN other_table t2 ON t1.id = t2.id]
  [WHERE condition];
  ```
* **실행 예시**:
  ```sql
  UPDATE accounts
  SET balance = balance - 500.00, updated_at = GETDATE()
  WHERE account_id = 101 AND balance >= 500.00;
  ```

#### 4. `DELETE` (레코드 삭제)
* **구문**:
  ```sql
  DELETE FROM table_name [WHERE condition];
  ```

#### 5. `SELECT ... INTO @variable` (변수 바인딩)
T-SQL 저장 프로시저 내부에서 단일 행 쿼리 결과를 선언된 로컬 스칼라 변수에 즉시 바인딩합니다.
```sql
DECLARE @cust_name VARCHAR(100), @credit DECIMAL(18,2);
SELECT name, balance INTO @cust_name, @credit FROM customers WHERE customer_id = @target_id;
```

---

### 4.3. 세션 및 트랜잭션 제어어 (TCL / SCL)

#### 1. 명시적 트랜잭션 제어 (TCL)
LionSQL은 ACID 트랜잭션 원자성을 완벽히 보장하며, 다중 명령문이 단일 트랜잭션 문맥(`exec.RunInTx`)에서 실행되도록 관리합니다.

* **`BEGIN TRANSACTION [tran_name]`**: 신규 트랜잭션을 시작합니다. 세션 전역 변수 `@@TRANCOUNT`가 1 증가합니다.
* **`COMMIT TRANSACTION`**: 트랜잭션 작업 메모리의 변경 사항을 WAL에 원자적으로 기록하고 커밋합니다. 충돌 발생 시 `40001 (ErrWriteConflict)`를 반환하고 롤백합니다.
* **`ROLLBACK TRANSACTION`**: 현재 트랜잭션의 모든 미커밋 변경 사항을 즉시 폐기하고 작업 메모리를 초기화합니다.

#### 2. 세션 제어 및 관용적 가상 No-Op (SCL)
* **`USE <database_name>`**:
  LionSQL의 인스턴스 격리 아키텍처(Instance-Level Isolation)에 따라 프로세스당 단일 카탈로그(`postgres` 또는 `master`)가 서비스됩니다. DBeaver, SSMS, sqlcmd 및 기존 MSSQL 스크립트와의 100% 호환성을 보장하기 위해 `USE <db>` 구문은 **관용적 가상 No-Op(Tolerant Virtual No-Op)**으로 정상 수락되며, TDS 프로토콜 환경 변경 토큰(`0xE3 ENVCHANGE`)을 클라이언트에 방출하여 문법 오류 없이 세션 상태를 동기화합니다.
* **`SET NOCOUNT {ON | OFF}`**:
  DML 수행 시 영향받은 행 수(Done Token) 방출을 제어하여 네트워크 왕복 트래픽을 절감합니다.
* **`SET work_mem = 'value'`**:
  현재 클라이언트 세션에 한하여 대규모 정렬 및 조인 메모리 예산을 동적으로 상향 조정합니다.
* **`SET TRANSACTION ISOLATION LEVEL READ COMMITTED`**:
  세션의 트랜잭션 격리 수준을 명시적으로 설정합니다.

---

## 제5장. 연산자 및 내장 함수 사양 (Functions & Operators Reference)

### 5.1. 연산자 사양 및 우선순위 (Operator Precedence)

LionSQL의 수식 계산 엔진은 ANSI SQL 및 T-SQL 표준 연산자 우선순위를 엄격하게 준수합니다 (위에서 아래로 우선순위 감소):

1. **괄호**: `( )` (그룹화)
2. **단항 연산자**: `+` (양수), `-` (음수), `~` (비트 반전)
3. **곱셈/나눗셈**: `*`, `/`, `%` (정수 나머지)
4. **덧셈/뺄셈/문자열 연결**: `+` (산술 덧셈 또는 T-SQL 문자열 결합), `-` (뺄셈), `||` (ANSI 표준 문자열 결합)
5. **비교 연산자**: `=`, `<>`, `!=`, `<`, `<=`, `>`, `>=`
6. **범위 및 포함 연산자**: `LIKE`, `NOT LIKE`, `BETWEEN ... AND ...`, `IN (...)`, `IS NULL`, `IS NOT NULL`
7. **논리 부정**: `NOT`
8. **논리곱**: `AND`
9. **논리합**: `OR`

---

### 5.2. 범용 내장 함수 상세 레퍼런스

#### 1. 문자열 조작 함수 (String Functions)

##### `LEN(string_expression)`
* **구문**: `LEN(str)`
* **인수**: `str` (`VARCHAR` 또는 `NVARCHAR`)
* **반환값**: `INT` (후행 공백을 제외한 문자열의 실제 문자 수)
* **예제**:
  ```sql
  SELECT LEN('LionSQL Engine   '); -- 반환값: 14
  ```

##### `SUBSTRING(string_expression, start, length)`
* **구문**: `SUBSTRING(str, start, len)`
* **인수**: `str` (대상 문자열), `start` (1부터 시작하는 시작 위치), `len` (추출할 문자 수)
* **반환값**: `VARCHAR` (추출된 부분 문자열)
* **예제**:
  ```sql
  SELECT SUBSTRING('2026-10-19', 1, 4); -- 반환값: '2026'
  ```

##### `CHARINDEX(substring, string_expression [, start_location])`
* **구문**: `CHARINDEX(sub, str [, start])`
* **인수**: `sub` (검색할 부분 문자열), `str` (검색 대상), `start` (선택적 시작 위치, 기본값: 1)
* **반환값**: `INT` (발견된 1-based 시작 인덱스, 미발견 시 `0`)
* **예제**:
  ```sql
  SELECT CHARINDEX('@', 'admin@lionsql.local'); -- 반환값: 6
  ```

##### `REPLACE(string_expression, string_pattern, string_replacement)`
* **구문**: `REPLACE(str, from_str, to_str)`
* **인수**: 대상 문자열과 치환할 원본/목표 문자열
* **반환값**: 치환 완료된 `VARCHAR`
* **예제**:
  ```sql
  SELECT REPLACE('v1.0-alpha', 'alpha', 'release'); -- 반환값: 'v1.0-release'
  ```

##### `UPPER(str)` / `LOWER(str)`
* **구문**: `UPPER(str)`, `LOWER(str)`
* **반환값**: 대문자 또는 소문자로 변환된 문자열
* **예제**:
  ```sql
  SELECT UPPER('lionsql'), LOWER('HELLO'); -- 반환값: 'LIONSQL', 'hello'
  ```

##### `LTRIM(str)` / `RTRIM(str)`
* **구문**: `LTRIM(str)`, `RTRIM(str)`
* **반환값**: 선행(Left) 또는 후행(Right) 공백이 제거된 문자열
* **예제**:
  ```sql
  SELECT LTRIM('  spaced  '); -- 반환값: 'spaced  '
  ```

##### `CONCAT(string_val1, string_val2 [, string_valN ...])`
* **구문**: `CONCAT(v1, v2, ...)`
* **반환값**: 전달된 모든 문자열을 하나로 연결 (`NULL`은 빈 문자열로 안전하게 간주)
* **예제**:
  ```sql
  SELECT CONCAT('User #', 101, ' (', NULL, 'active)'); -- 반환값: 'User #101 (active)'
  ```

##### `STRING_SPLIT(string_expression, delimiter)` (테이블 반환 함수, TVF)
* **구문**: `SELECT value FROM STRING_SPLIT(str, sep)`
* **인수**: `str` (분할할 문자열), `sep` (단일 문자 구분자)
* **반환값**: 분할된 문자열 행 집합 (컬럼명: `value`)
* **예제**:
  ```sql
  SELECT value FROM STRING_SPLIT('apple,banana,cherry', ',');
  -- 반환 결과:
  -- value
  -- ------
  -- apple
  -- banana
  -- cherry
  ```

---

#### 2. 일시 및 타임스탬프 함수 (Date & Time Functions)

##### `GETDATE()` / `CURRENT_TIMESTAMP`
* **구문**: `GETDATE()`, `CURRENT_TIMESTAMP`
* **반환값**: 데이터베이스 서버의 현재 로컬 일시 (`DATETIME`, 밀리초 정밀도)
* **예제**:
  ```sql
  SELECT GETDATE(); -- 반환값: 2026-10-19 17:30:00.123
  ```

##### `DATEADD(datepart, number, date)`
* **구문**: `DATEADD(part, n, date)`
* **인수**: `part` (`year`, `month`, `day`, `hour`, `minute`, `second`), `n` (가감할 정수), `date` (대상 일시)
* **반환값**: 연산된 일시 (`DATETIME`)
* **예제**:
  ```sql
  SELECT DATEADD(day, 30, '2026-01-01'); -- 반환값: 2026-01-31 00:00:00
  ```

##### `DATEDIFF(datepart, startdate, enddate)`
* **구문**: `DATEDIFF(part, start, end)`
* **인수**: 지정된 `part` 단위 기준 두 일시 간의 차이(`end - start`)
* **반환값**: `BIGINT`
* **예제**:
  ```sql
  SELECT DATEDIFF(day, '2026-01-01', '2026-01-10'); -- 반환값: 9
  ```

##### `DATEPART(datepart, date)` / `YEAR(date)` / `MONTH(date)` / `DAY(date)`
* **구문**: `DATEPART(part, date)`, `YEAR(date)`, `MONTH(date)`, `DAY(date)`
* **반환값**: 일시의 특정 부분을 정수(`INT`)로 추출
* **예제**:
  ```sql
  SELECT YEAR('2026-10-19'), MONTH('2026-10-19'), DAY('2026-10-19'); -- 2026, 10, 19
  ```

---

#### 3. 조건부 평가 및 NULL 제어 함수 (Conditional & Control Flow)

##### `COALESCE(val1, val2 [, valN ...])`
* **구문**: `COALESCE(v1, v2, ...)`
* **반환값**: 인수 목록 중 첫 번째로 `NULL`이 아닌 값 반환 (모두 NULL이면 NULL)
* **예제**:
  ```sql
  SELECT COALESCE(NULL, NULL, 'Default Value'); -- 반환값: 'Default Value'
  ```

##### `ISNULL(check_expression, replacement_value)` (T-SQL)
* **구문**: `ISNULL(check_expr, repl_val)`
* **반환값**: `check_expr`이 NULL이면 `repl_val` 반환, 아니면 원본 유지
* **예제**:
  ```sql
  SELECT ISNULL(balance, 0.00) FROM accounts;
  ```

##### `NULLIF(expression1, expression2)`
* **구문**: `NULLIF(v1, v2)`
* **반환값**: 두 값이 같으면 `NULL`, 다르면 `expression1` 반환
* **예제**:
  ```sql
  SELECT 100.0 / NULLIF(total_items, 0) FROM cart; -- 0으로 나누기 예외 방지
  ```

##### `IIF(boolean_expression, true_value, false_value)`
* **구문**: `IIF(condition, val_true, val_false)`
* **반환값**: 조건이 참이면 `val_true`, 거짓이면 `val_false`
* **예제**:
  ```sql
  SELECT IIF(score >= 60, 'PASS', 'FAIL') FROM exams;
  ```

##### `CASE WHEN ... THEN ... ELSE ... END`
* **구문**:
  ```sql
  CASE 
      WHEN condition1 THEN result1
      WHEN condition2 THEN result2
      ELSE default_result
  END
  ```

---

#### 4. 수학 및 수치 연산 함수 (Mathematical Functions)

* **`ABS(numeric_expr)`**: 절댓값 반환. `ABS(-42)` ➔ `42`
* **`ROUND(numeric_expr, decimals)`**: 지정 소수점 자릿수 기준 반올림. `ROUND(123.456, 2)` ➔ `123.46`
* **`CEILING(numeric_expr)`**: 천장값(크거나 같은 최소 정수) 반환. `CEILING(4.1)` ➔ `5`
* **`FLOOR(numeric_expr)`**: 바닥값(작거나 같은 최대 정수) 반환. `FLOOR(4.9)` ➔ `4`
* **`POWER(base, exponent)`**: 거듭제곱 계산. `POWER(2, 10)` ➔ `1024`
* **`SQRT(numeric_expr)`**: 양의 제곱근 계산. `SQRT(144)` ➔ `12`

---

#### 5. 명시적 형변환 및 고유 식별자 함수 (Conversion & ID Functions)

##### `CAST(expression AS data_type)`
* **구문**: `CAST(expr AS target_type)`
* **예제**:
  ```sql
  SELECT CAST('42' AS INT), CAST(GETDATE() AS DATE);
  ```

##### `CONVERT(data_type, expression [, style])` (T-SQL)
* **구문**: `CONVERT(target_type, expr [, style])`
* **예제**:
  ```sql
  SELECT CONVERT(VARCHAR(20), GETDATE(), 120); -- ODBC 정규 포맷 'yyyy-mm-dd hh:mi:ss'
  ```

##### `NEWID7()` (RFC 9562 타임스탬프 기반 순차 UUIDv7)
* **구문**: `NEWID7()`
* **반환값**: 밀리초 에포크 타임스탬프 48비트가 상위에 배치되어 B+Tree 인덱스 분할(Page Split)을 원천 방지하는 128비트 단조 증가 UUID.
* **예제**:
  ```sql
  SELECT NEWID7(); -- 반환값: '0192a5b6-7c8d-7e9f-8a0b-1c2d3e4f5a6b'
  ```

---

#### 6. 집계 및 분석 윈도우 함수 (Aggregates & Window Functions)

##### 집계 함수
* **`COUNT(*)` / `COUNT(col)`**: 전체 행 수 또는 Non-NULL 컬럼 값 개수 집계
* **`SUM(col)`**: 수치 컬럼 합계
* **`AVG(col)`**: 수치 컬럼 산술 평균
* **`MIN(col)` / `MAX(col)`**: 최솟값 및 최댓값

##### 윈도우 순위 및 오프셋 함수 (`OVER (PARTITION BY ... ORDER BY ...)`)
* **`ROW_NUMBER()`**: 파티션 내 정렬 순서대로 1부터 단조 증가하는 일련번호 할당.
* **`RANK()`**: 동순위 발생 시 동일 순위를 부여하고 다음 순위를 건너뜀 (`1, 2, 2, 4`).
* **`DENSE_RANK()`**: 동순위 발생 시 동일 순위를 부여하되 다음 순위를 건너뛰지 않음 (`1, 2, 2, 3`).
* **`LEAD(col [, offset [, default]])`**: 정렬 기준 현재 행보다 뒤에 위치한 행의 값 참조.
* **`LAG(col [, offset [, default]])`**: 정렬 기준 현재 행보다 앞에 위치한 행의 값 참조.

##### 분위수 및 백분위 분석 함수 (`PERCENTILE_CONT`, `PERCENTILE_DISC`)
* **`PERCENTILE_CONT(percentile) WITHIN GROUP (ORDER BY sort_expression)`**:
  - T-Digest 스트리밍 알고리즘을 사용하여 선형 보간 연속 백분위수(0.0 ~ 1.0)를 산출합니다.
  - 예제: `PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY response_time_ms)`

---

#### 7. 시스템 상태 및 메타데이터 함수 (System State Functions)

* **`DB_NAME()`**: 현재 세션의 가상 데이터베이스명(`'postgres'` 또는 `'master'`) 반환.
* **`CURRENT_DATABASE()`**: ANSI 표준 현재 데이터베이스명 반환.
* **`USER_NAME()`**: 현재 세션에 인증된 사용자 계정명 반환.
* **`@@IDENTITY`**: 현재 세션에서 가장 최근에 `IDENTITY` 컬럼에 자동 생성된 64비트 정수 ID.
* **`@@ROWCOUNT`**: 직전에 실행된 SQL 명령문이 영향을 준 행 수.
* **`@@TRANCOUNT`**: 현재 세션에서 열려 있는 중첩 트랜잭션의 깊이 (`BEGIN TRAN` 시 +1, `COMMIT` 시 -1).
* **`@@ERROR`**: 직전에 실행된 문의 내부 에러 코드 (정상 완료 시 `0`).

---

## 제6장. 가상 카탈로그 뷰 및 메타데이터 사양 (System Catalogs Reference)

LionSQL은 인스턴스 격리(Single Database per Process) 원칙을 유지하면서도 DBeaver, Beekeeper Studio, Navicat, SSMS, psql, sqlcmd 등 외부 생태계 GUI/CLI 도구와의 100% 투명한 호환성을 보장하기 위해 3대 주요 시스템 카탈로그 뷰를 메모리 상에서 가상화(Virtual Catalog Projection)하여 제공합니다.

---

### 6.1. MS SQL Server 호환 카탈로그 (`sys.*`)

SSMS, Azure Data Studio, sqlcmd가 데이터베이스 구조 및 프로시저 목록을 탐색할 때 쿼리하는 가상 뷰입니다.

#### 1. `sys.databases`
현재 가동 중인 데이터베이스 인스턴스 메타데이터를 제공합니다.
| 컬럼명 | 데이터 타입 | 설명 |
| :--- | :---: | :--- |
| `database_id` | `INT` | 고유 데이터베이스 ID (기본값: `1`) |
| `name` | `VARCHAR(128)` | 데이터베이스 명칭 (`'master'`) |
| `compatibility_level`| `TINYINT` | 호환성 레벨 (`150`: SQL Server 2019 호환) |
| `collation_name` | `VARCHAR(128)` | 데이터 정렬 규칙 (`'Korean_Wansung_CI_AS'`) |
| `user_access_desc` | `VARCHAR(60)` | 사용자 접근 모드 (`'MULTI_USER'`) |
| `is_read_only` | `BIT` | 읽기 전용 여부 (`0`: 읽기/쓰기 가능) |

#### 2. `sys.tables`
데이터베이스에 등록된 모든 사용자 물리 테이블 목록을 제공합니다.
| 컬럼명 | 데이터 타입 | 설명 |
| :--- | :---: | :--- |
| `object_id` | `INT` | 테이블의 고유 객체 식별 번호 |
| `name` | `VARCHAR(128)` | 테이블 물리 명칭 |
| `schema_id` | `INT` | 스키마 ID (`1`: `dbo`) |
| `type` | `CHAR(2)` | 객체 타입 (`'U '`: 사용자 테이블) |
| `type_desc` | `VARCHAR(60)` | 객체 설명 (`'USER_TABLE'`) |
| `create_date` | `DATETIME` | 테이블 생성 일시 |
| `modify_date` | `DATETIME` | 테이블 최근 스키마 수정 일시 |
| `is_user_table` | `BIT` | 시스템 내부 테이블 여부 (`1`: 사용자 테이블) |

#### 3. `sys.columns`
테이블에 속한 모든 컬럼의 데이터 타입과 속성 정의를 제공합니다.
| 컬럼명 | 데이터 타입 | 설명 |
| :--- | :---: | :--- |
| `object_id` | `INT` | 소속 테이블의 객체 ID |
| `name` | `VARCHAR(128)` | 컬럼 명칭 |
| `column_id` | `INT` | 1부터 시작하는 컬럼 정렬 순번 |
| `system_type_id` | `TINYINT` | TDS 시스템 데이터 타입 코드 (e.g. 56: INT, 167: VARCHAR) |
| `max_length` | `SMALLINT` | 물리 최대 바이트 길이 |
| `precision` | `TINYINT` | 숫자형 전체 자릿수 |
| `scale` | `TINYINT` | 숫자형 소수점 자릿수 |
| `is_nullable` | `BIT` | `NULL` 값 허용 여부 (`1`: 허용, `0`: NOT NULL) |
| `is_identity` | `BIT` | 단조 증가 `IDENTITY` 속성 여부 (`1`: 자동증가) |

#### 4. `sys.indexes` 및 `sys.index_columns`
테이블의 기본 키(Primary Key) 및 보조 B+Tree 인덱스 구조를 제공합니다.
* `object_id`: 대상 테이블 객체 ID
* `name`: 인덱스 명칭 (e.g. `PK_orders`, `idx_orders_date`)
* `index_id`: 인덱스 식별 번호 (`1`: Clustered/PK, `2+`: Non-Clustered)
* `is_unique`: 유니크 인덱스 여부
* `is_primary_key`: 기본 키 인덱스 여부
* `has_filter`: 부분 인덱스(Filtered Index) 여부

#### 5. `sys.procedures`
엔진에 등록된 모든 T-SQL 저장 프로시저의 객체 ID, 이름, 파라미터 시그니처 정보를 제공합니다.

---

### 6.2. ANSI / ISO 표준 카탈로그 (`INFORMATION_SCHEMA.*`)

표준 ORM 프레임워크(Prisma, Hibernate, Dapper, GORM) 및 데이터베이스 클라이언트가 스키마를 리플렉션할 때 쿼리하는 표준 뷰입니다.

#### 1. `INFORMATION_SCHEMA.TABLES`
* `TABLE_CATALOG`: `'postgres'` (PGWire) 또는 `'master'` (TDS)
* `TABLE_SCHEMA`: `'public'` (PGWire) 또는 `'dbo'` (TDS)
* `TABLE_NAME`: 테이블 이름
* `TABLE_TYPE`: `'BASE TABLE'` (일반 물리 테이블) 또는 `'VIEW'` (가상 뷰)

#### 2. `INFORMATION_SCHEMA.COLUMNS`
* `TABLE_CATALOG`, `TABLE_SCHEMA`, `TABLE_NAME`: 소속 테이블 식별자
* `COLUMN_NAME`: 컬럼 명칭
* `ORDINAL_POSITION`: 컬럼 정의 순서 (1-based)
* `COLUMN_DEFAULT`: 기본값 수식 문자열
* `IS_NULLABLE`: `'YES'` 또는 `'NO'`
* `DATA_TYPE`: 표준 데이터 타입 명칭 (`int`, `varchar`, `datetime` 등)
* `CHARACTER_MAXIMUM_LENGTH`: 문자열 최대 허용 자릿수
* `NUMERIC_PRECISION`, `NUMERIC_SCALE`: 숫자 정밀도

#### 3. `INFORMATION_SCHEMA.KEY_COLUMN_USAGE` & `REFERENTIAL_CONSTRAINTS`
테이블의 기본 키 컬럼 및 외래 키(Foreign Key) 부모-자식 참조 관계 명세를 정확하게 제공합니다.

---

### 6.3. PostgreSQL 호환 카탈로그 (`pg_catalog.*`)

`psql`, pgAdmin, Beekeeper Studio의 PostgreSQL 드라이버가 연결 초기화 시 세션 환경 및 스키마를 조회하는 내부 카탈로그 뷰입니다.

#### 1. `pg_catalog.pg_database`
* `oid`: 고유 데이터베이스 OID (`1`)
* `datname`: 데이터베이스 명칭 (`'postgres'`)
* `datdba`: 소유자 OID (`10`)
* `encoding`: 인코딩 방식 (`6`: UTF-8)
* `datcollate`, `datctype`: 정렬 규약 (`'C'`)

#### 2. `pg_catalog.pg_class`
* `oid`: 객체 OID
* `relname`: 테이블, 인덱스, 시퀀스 명칭
* `relnamespace`: 스키마 네임스페이스 OID (`2200`: `public`)
* `relkind`: 객체 분류 (`'r'`: 일반 릴레이션 테이블, `'i'`: 인덱스, `'v'`: 뷰)
* `relpages`: 볼케이노 옵티마이저 산정 디스크 페이지 수
* `reltuples`: 테이블 내 총 레코드 건수 통계

#### 3. `pg_catalog.pg_attribute`
* `attrelid`: 테이블 OID
* `attname`: 컬럼 명칭
* `atttypid`: PostgreSQL 타입 OID (`23`: int4, `20`: int8, `1043`: varchar, `1114`: timestamp 등)
* `attnum`: 컬럼 번호 (1-based, 음수는 시스템 히든 컬럼)
* `attnotnull`: `NOT NULL` 제약조건 플래그

#### 4. `pg_catalog.pg_stat_activity`
현재 서버에 접속 중인 모든 활성 및 유휴 세션의 프로세스 상태(PID, 사용자명, 클라이언트 IP, 연결 시간, 현재 실행 쿼리 텍스트)를 실시간으로 출력합니다.

---

## 제7장. REST API 및 웹 스튜디오 사양 (REST API Reference)

LionSQL은 기본 HTTP 포트 `:8090`을 통해 시스템 메트릭 관제, 관리자 인증, 원시 SQL 및 저장 프로시저 원격 호출, 무설정 자동 RESTful CRUD, 온라인 백업 및 복제 제어를 지원하는 고성능 내장 웹 서버를 제공합니다.

* **기본 Base URL**: `http://localhost:8090`
* **공통 요청 헤더**:
  - `Content-Type: application/json`
  - `Authorization: Bearer <JWT_TOKEN>` (보안 엔드포인트 호출 시 필수)

---

### 7.1. 시스템 및 실시간 관제 API

#### 1. `GET /api/health` (헬스체크)
엔진 상태, 가동 버전, 활성 스토리지 백엔드를 반환합니다. 로드 밸런서 및 쿠버네티스 Liveness Probe로 사용됩니다.
* **인증 필요**: 없음 (`No`)
* **응답 (200 OK)**:
  ```json
  {
    "status": "ok",
    "version": "0.90.0",
    "engine": "wiredtiger",
    "uptime_seconds": 15155,
    "timestamp": 1729326600
  }
  ```

#### 2. `GET /api/version` (버전 및 빌드 정보)
* **인증 필요**: 없음 (`No`)
* **응답 (200 OK)**:
  ```json
  {
    "name": "LionSQL",
    "version": "0.90.0",
    "go_version": "go1.24.0",
    "git_commit": "477a4cb6",
    "build_tags": ["wiredtiger"],
    "os_arch": "darwin/arm64"
  }
  ```

#### 3. `GET /api/metrics` (프로메테우스 지표 수집)
Prometheus 표준 텍스트 포맷으로 QPS, 메모리 힙, 연결 수, 트랜잭션 커밋/충돌 카운트를 방출합니다.
* **인증 필요**: 없음 (`No`)
* **응답 헤더**: `Content-Type: text/plain; version=0.0.4`
* **출력 예시**:
  ```text
  # HELP sqlion_qps_current Current queries per second.
  # TYPE sqlion_qps_current gauge
  sqlion_qps_current 14250.00
  # HELP sqlion_active_connections Number of active client connections.
  # TYPE sqlion_active_connections gauge
  sqlion_active_connections 18
  # HELP sqlion_heap_bytes Current Go runtime heap alloc bytes.
  # TYPE sqlion_heap_bytes gauge
  sqlion_heap_bytes 149422080
  # HELP sqlion_transactions_committed_total Total committed ACID transactions.
  # TYPE sqlion_transactions_committed_total counter
  sqlion_transactions_committed_total 850420
  ```

#### 4. `GET /api/realtime` (Server-Sent Events 실시간 스트림)
Web Studio 대시보드가 1초 주기로 CPU 사용량, QPS, 메모리 거버너 현황을 실시간 수신하는 영구 단방향 SSE 스트림입니다.
* **인증 필요**: 없음 (`No`)
* **응답 헤더**: `Content-Type: text/event-stream`, `Cache-Control: no-cache`
* **이벤트 포맷**:
  ```text
  data: {"qps":12450,"heap_mb":142.5,"active_workers":8,"total_queries":184920}

  ```

---

### 7.2. 인증 및 보안 토큰 API

#### 1. `POST /api/auth/login` (관리자 로그인)
관리자 이메일과 비밀번호를 검증하고 HMAC-SHA256 서명된 세션 JWT를 발급합니다.
* **인증 필요**: 없음 (`No`)
* **요청 본문**:
  ```json
  {
    "email": "admin@lionsql.local",
    "password": "your_password"
  }
  ```
* **응답 (200 OK)**:
  ```json
  {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_in": 259200,
    "user": {
      "email": "admin@lionsql.local",
      "role": "admin"
    }
  }
  ```
* **실패 응답 (401 Unauthorized)**:
  ```json
  {
    "error": "invalid email or password"
  }
  ```

#### 2. `POST /api/auth/refresh` (세션 토큰 갱신)
만료 기한이 다가온 JWT를 무중단으로 연장 발급합니다.
* **헤더**: `Authorization: Bearer <VALID_JWT>`
* **응답 (200 OK)**: `{"token": "new_jwt_string...", "expires_in": 259200}`

---

### 7.3. 원시 SQL 및 저장 프로시저 호출 API

#### 1. `POST /api/sql` (임의 SQL / T-SQL 실행)
파라미터화된 SQL 문장 또는 다중 쿼리 배치를 실행하고 열 메타데이터 및 결과 행 배열을 반환합니다.
* **인증 필요**: 필수 (`Bearer Token`)
* **요청 본문**:
  ```json
  {
    "query": "SELECT customer_id, name, balance FROM customers WHERE balance >= @min_bal ORDER BY balance DESC;",
    "params": {
      "min_bal": 1000.00
    }
  }
  ```
* **응답 (200 OK)**:
  ```json
  {
    "columns": ["customer_id", "name", "balance"],
    "column_types": ["INT", "VARCHAR", "DECIMAL"],
    "rows": [
      [101, "Acme Corp", 15420.50],
      [104, "Global Tech", 2400.00]
    ],
    "rows_affected": 2,
    "execution_time_ms": 0.42
  }
  ```

#### 2. `POST /api/procs/exec` (T-SQL 저장 프로시저 원격 RPC)
엔진에 컴파일되어 캐싱된 VDBE 저장 프로시저를 원격 실행합니다.
* **인증 필요**: 필수 (`Bearer Token`)
* **요청 본문**:
  ```json
  {
    "name": "TransferMoney",
    "params": {
      "from_id": 101,
      "to_id": 102,
      "amount": 500.00
    }
  }
  ```
* **응답 (200 OK)**:
  ```json
  {
    "procedure": "TransferMoney",
    "return_status": 0,
    "outputs": {
      "new_balance": 14920.50
    },
    "execution_time_ms": 0.85
  }
  ```

---

### 7.4. 무설정 자동 REST API (`/api/records/:table`)

테이블이 생성되면 즉시 활성화되는 PocketBase 스타일의 리소스 CRUD 엔드포인트입니다.

#### 1. `GET /api/records/:table` (목록 조회 및 페이징)
* **쿼리 파라미터**:
  - `page`: 페이지 번호 (기본값: `1`)
  - `perPage`: 페이지당 레코드 수 (기본값: `30`, 최대: `500`)
  - `sort`: 정렬 기준 컬럼 (오름차순 `col`, 내림차순 `-col`)
  - `filter`: 조건절 수식 (예: `status = 'ACTIVE' AND balance > 500`)
* **응답 (200 OK)**:
  ```json
  {
    "page": 1,
    "perPage": 30,
    "totalItems": 1250,
    "totalPages": 42,
    "items": [
      {"id": 1, "name": "Alice", "balance": 1500.00, "status": "ACTIVE"}
    ]
  }
  ```

#### 2. `POST /api/records/:table` (레코드 생성)
* **요청 본문**:
  ```json
  {
    "name": "Bob",
    "balance": 2500.00,
    "status": "ACTIVE"
  }
  ```
* **응답 (201 Created)**: 생성된 레코드 전체 객체 (자동 발급된 `id` 포함) 반환.

#### 3. `PUT /api/records/:table` (레코드 갱신)
* **요청 본문**: 기본 키 필드(`id`)와 수정할 필드를 담은 JSON 객체 전송.

#### 4. `DELETE /api/records/:table` (레코드 삭제)
* **요청 본문**: `{"id": 1}`

---

### 7.5. 운영 관리, 감사 로그 및 복제 제어 API

* **`GET /api/logs/requests`**: 인바운드 활동 로그 링 버퍼 최근 500건 반환.
* **`GET /api/slow-queries`**: 임계치(`-slow-threshold-ms`)를 초과한 슬로우 쿼리 내역 및 실행 시간 반환.
* **`POST /api/backup`**: 가동 중인 데이터베이스 스냅샷 압축 아카이브(`.tar.gz`) 스트리밍 다운로드.
* **`POST /api/restore`**: `multipart/form-data`로 `.tar.gz` 백업 파일을 업로드하여 스토리지 복원 수행.
* **`GET /api/replication/status`**: 클러스터 노드 역할(`PRIMARY` 또는 `REPLICA`), 현재 LSN, 지연 시간 반환.
* **`POST /api/replication/promote`**: Replica 노드를 Primary로 즉시 승격.

---

## 제8장. 와이어 프로토콜 기술 사양 (Wire Protocol Specifications)

LionSQL은 외부 클라이언트 라이브러리와 통신하기 위해 두 가지 상용 데이터베이스 표준 와이어 프로토콜을 단일 프로세스 내에서 동시에 구동합니다.

---

### 8.1. PostgreSQL Wire Protocol v3 사양 (`TCP :5433`)

PostgreSQL 프레임은 첫 바이트(1B 식별자 문자)와 4바이트 빅엔디안 패킷 길이 필드로 시작합니다:
```
+-------------+----------------+-------------------------------+
| Type (1B)   | Length (4B)    | Payload Data (Length - 4)     |
+-------------+----------------+-------------------------------+
```

#### 1. 연결 수립 단계 (Connection Handshake)
1. **`SSLRequest` (`code: 80877103`)**: 클라이언트가 SSL 암호화 지원 여부를 질의. 서버는 비암호화 시 `'N'`, SSL 지원 시 `'S'`를 1바이트 응답.
2. **`StartupMessage` (`proto: 196608` [v3.0])**: 클라이언트가 `user`, `database`(`'postgres'`), 인코딩(`'UTF8'`) 파라미터 전달.
3. **`AuthenticationCleartextPassword` (`'R'`, code `3`)** 또는 **`AuthenticationOk` (`'R'`, code `0`)**: 비밀번호 검증 완료.
4. **`ParameterStatus` (`'S'`)**: `server_version=14.0 (LionSQL v0.90.0)`, `client_encoding=UTF8`, `standard_conforming_strings=on` 송출.
5. **`ReadyForQuery` (`'Z'`, status `'I'`)**: 유휴 트랜잭션 대기 상태 선언.

#### 2. 단순 쿼리 프로토콜 (Simple Query Protocol)
* **`Query` (`'Q'`)**: 일반 문자열 SQL 질의 전송.
* **`RowDescription` (`'T'`)**: 컬럼 수, 컬럼명, 테이블 OID, 타입 OID(INT4: 23, VARCHAR: 1043), 포맷 코드(0: Text).
* **`DataRow` (`'D'`)**: 컬럼별 바이트 길이(4B)와 원시 텍스트 데이터.
* **`CommandComplete` (`'C'`)**: 태그 문자열 (`SELECT 10`, `INSERT 0 1`).
* **`ReadyForQuery` (`'Z'`)**: 질의 완료 및 다음 명령 준비.

#### 3. 확장 쿼리 프로토콜 (Extended Query Protocol)
PreparedStatement 및 캐시된 실행 계획을 재사용합니다:
* **`Parse` (`'P'`)**: SQL 문장을 VDBE 바이트코드로 컴파일하고 구문 객체 캐시에 등록.
* **`Bind` (`'B'`)**: 바인드 매개변수($1, $2)를 포털(Portal)에 주입.
* **`Describe` (`'D'`)**: 결과셋 메타데이터 요청.
* **`Execute` (`'E'`)**: 지정된 최대 행 수(`max_rows`)만큼 Volcano 반복자 실행.
* **`Sync` (`'S'`)**: 트랜잭션 경계 커밋 검증 및 `ReadyForQuery` 방출.

---

### 8.2. Microsoft SQL Server TDS 7.x / 8.0 Protocol 사양 (`TCP :1433`)

TDS는 모든 패킷 상단에 **8바이트 공통 헤더(Common Header)**를 유지합니다:

```
+-----------+------------+-------------+--------------+--------------+-------------+
| Type (1B) | Status (1B)| Length (2B) | Channel (2B) | PacketID (1B)| Window (1B) |
+-----------+------------+-------------+--------------+--------------+-------------+
```

* **주요 패킷 타입 (Packet Types)**:
  - `0x01` (`SQLBatch`): 일반 T-SQL 명령문 배치.
  - `0x03` (`RPC`): 원격 저장 프로시저 호출 (`sp_prepexec`, `sp_executesql`).
  - `0x04` (`TabularResult`): 서버가 결과셋 토큰 스트림을 클라이언트에 방출.
  - `0x06` (`Attention`): 클라이언트의 실행 취소(Cancel) 요청.
  - `0x0E` (`TransactionManager`): 분산 트랜잭션 또는 격리 수준 동기화.
  - `0x10` (`Login7`): 사용자 인증 및 세션 초기화 정보.
  - `0x12` (`PreLogin`): 클라이언트와 서버 간의 TLS/암호화/버전 사전 협상.

* **주요 토큰 스트림 규격 (Data Stream Tokens)**:
  - **`COLMETADATA (0x81)`**: 결과셋 컬럼 정의 (사용자 타입 ID, Flags, 데이터 타입 코드, 컬럼명).
  - **`ROW (0xD1)`**: 일반 행 데이터 (각 컬럼 값의 길이와 원시 바이트).
  - **`NBCROW (0xD2, Null Bitmap Compressed Row)`**:
    행 앞단에 $\lceil N/8 \rceil$ 바이트 널 비트맵을 탑재하여 `NULL` 컬럼의 바이트 전송을 완전히 생략하는 고효율 압축 토큰.
  - **`ENVCHANGE (0xE3)`**:
    세션 환경 변경 알림 (데이터베이스 컨텍스트 `master` 동기화, 패킷 사이즈 조정).
  - **`DONE (0xFD)` / `DONEPROC (0xFE)`**:
    배치 명령 또는 프로시저 실행 완료 및 영향받은 행 수(Row Count 64비트) 방출.
  - **`ERROR (0xAA)` / `INFO (0xAB)`**:
    에러 번호, 심각도(Severity), 상태(State), 메시지 문자열 방출.

---

## 제9장. 에러 코드 및 진단 레퍼런스 (Error Codes & Diagnostics)

### 9.1. LionSQL 표준 내부 에러 및 ANSI SQLSTATE 매핑

| 내부 에러 코드 | ANSI SQLSTATE | TDS Error | 표준 영문 메시지 | 원인 및 해결 조치 |
| :--- | :---: | :---: | :--- | :--- |
| `ERR_TABLE_NOT_FOUND` | `42P01` | `208` | `table does not exist` | 지정한 테이블이 존재하지 않음. 테이블명 대소문자 확인 |
| `ERR_COLUMN_NOT_FOUND` | `42703` | `207` | `column does not exist` | 잘못된 컬럼명 참조. 스키마 확인 |
| `ERR_UNIQUE_VIOLATION` | `23505` | `2627` | `unique constraint violation: duplicate key` | PRIMARY KEY 또는 UNIQUE 인덱스 중복 키 삽입 |
| `ERR_WRITE_CONFLICT` | `40001` | `1205` | `serialization failure: write-write conflict` | OCC 또는 MVCC 동시 쓰기 충돌. 클라이언트 재시도 필요 |
| `ERR_FOREIGN_KEY` | `23503` | `547` | `foreign key violation: referenced key not found` | 외래 키 무결성 위반. 부모 행 부재 |
| `ERR_MEMORY_BUDGET` | `53200` | `701` | `memory budget exceeded: ... > work_mem` | 조인/정렬 연산자가 `work_mem` 초과. 필터 추가 또는 work_mem 상향 |
| `ERR_STORAGE_FULL` | `53200` | `1105` | `table data limit exceeded: memory limit exceeded` | 인메모리 엔진 힙 상한(`--max-memory`) 도달 |
| `ERR_SYNTAX_ERROR` | `42601` | `102` | `syntax error in SQL statement` | SQL 구문 문법 오류 |
| `ERR_AUTH_FAILED` | `28P01` | `18456` | `password authentication failed` | 관리자 이메일 또는 비밀번호 불일치 |
| `ERR_CONNECTION_LOST` | `08006` | `10054` | `connection failure: client aborted` | 클라이언트 소켓 강제 단절 또는 타임아웃 |

---

### 9.2. TDS 에러 심각도 레벨 (TDS Severity Levels)

* **Severity 1 ~ 10 (정보 및 경고)**:
  - `PRINT` 문장 출력, 비정상적이지 않은 알림. 클라이언트 연결 유지.
* **Severity 11 ~ 16 (사용자 실행 오류)**:
  - 제약조건 위반, 문법 오류, `RAISERROR` 사용자 예외. 해당 SQL 배치만 중단되고 연결은 유지.
* **Severity 17 ~ 19 (리소스 부족 오류)**:
  - `work_mem` 초과, 메모리 거버너 차단. 현재 트랜잭션이 롤백됨.
* **Severity 20 ~ 25 (시스템 치명 오류)**:
  - 스토리지 물리 I/O 장애, 프로세스 손상. 클라이언트 연결이 즉시 강제 종료됨.

---

### 9.3. PostgreSQL 오류 수준 (PG Error Severity)

* **`NOTICE`**: DDL 자동 캐스케이드 알림, 정보성 메시지.
* **`WARNING`**: 트랜잭션 블록 외부에서의 롤백 시도 등 경고.
* **`ERROR`**: 쿼리 실패, 제약조건 위반 (트랜잭션이 Aborted 상태로 전이되며 명시적 `ROLLBACK` 요구).
* **`FATAL`**: 인증 실패, 관리자 강제 종료 (클라이언트 연결 닫힘).
* **`PANIC`**: 시스템 비정상 종료 (전체 서버 프로세스 중단).

---

#### 10.1. 아키텍처 및 스토리지 엔진 내부 구조 (Architecture & Storage Internals)
* [`ARCHITECTURE.md`](file:///Users/maro/Projects/LionSQL/doc/ARCHITECTURE.md)
  * **전체 아키텍처 청사진**: 듀얼 스토리지 엔진(인메모리 OCC vs WiredTiger B+Tree), 듀얼 파서 전략(Pratt fast-path + ANTLR4 fallback), VDBE 바이트코드 가상 머신, 고성능 고루틴 연결 풀러, 다중 와이어 프로토콜 단일 바이너리 통합 아키텍처 개요.
* [`INTERNALS_STORAGE.md`](file:///Users/maro/Projects/LionSQL/doc/INTERNALS_STORAGE.md)
  * **스토리지 엔진 인터페이스 규격**: `Engine`, `Tx`, `Cursor` 핵심 계약 명세. Concurrent SkipList 인메모리 인덱싱 구조와 WiredTiger B+Tree 페이지 레이아웃, 체크포인트 및 WAL(Write-Ahead Logging) 내구성 설계.
* [`RECORD_BINARY_FORMAT.md`](file:///Users/maro/Projects/LionSQL/doc/RECORD_BINARY_FORMAT.md)
  * **바이너리 튜플 직렬화 규격**: 스키마 기반 압축 바이너리 튜플 포맷(`pkg/storage/tuple.go`). `0x01` 버전 헤더, $\lceil N/8 \rceil$ 널 비트맵, 지그재그 가변 길이 정수 인코딩 및 디스크/WAL 저장 공간 절감 설계.
* [`TRANSACTION_CONCURRENCY.md`](file:///Users/maro/Projects/LionSQL/doc/TRANSACTION_CONCURRENCY.md)
  * **트랜잭션 격리 및 동시성 제어**: Non-blocking Snapshot Isolation / Read Committed 모델. 인메모리 OCC 버전 충돌 검증, WiredTiger MVCC 해저드 포인터 및 VDBE 트랜잭션 커플링(`exec.RunInTx`) 규약.
* [`DATA_DICTIONARY.md`](file:///Users/maro/Projects/LionSQL/doc/DATA_DICTIONARY.md)
  * **데이터 딕셔너리 및 시스템 카탈로그**: 스키마 메타데이터 영속성 레이아웃, 시스템 테이블 및 가상 뷰(`sys.*`, `INFORMATION_SCHEMA.*`, `pg_catalog.*`)의 매핑 및 해석 원리.
* [`HIGH_AVAILABILITY.md`](file:///Users/maro/Projects/LionSQL/doc/HIGH_AVAILABILITY.md)
  * **고가용성 및 무중단 백업/복원**: 크래시 안전성 보장, LSN 기반 WAL 스트리밍 1-Writer Multi-Reader 복제, 무중단 Primary 승격, 온라인 핫 백업(`.tar.gz`) 및 복원 파이프라인.
* [`ADR.md`](file:///Users/maro/Projects/LionSQL/doc/ADR.md)
  * **아키텍처 결정 레코드 (Architectural Decision Records)**: VDBE 통합, 듀얼 파서 폴백, 단일 바이너리 배포, 단일 라이터 복제, 컴팩트 튜플 코덱, 다중 프로토콜 카탈로그 가로채기 등 핵심 설계 결정 및 트레이드오프 기록 (ADR-001 ~ ADR-021).

#### 10.2. 쿼리 컴파일러, 최적화 및 실행 엔진 (Query Engine & VM Internals)
* [`INTERNALS_VDBE.md`](file:///Users/maro/Projects/LionSQL/doc/INTERNALS_VDBE.md)
  * **바이트코드 가상 머신 (VDBE)**: SQLite 스타일의 레지스터 기반 VDBE 가상 머신 아키텍처. 핵심 바이트코드 Opcode 사양, `sync.Pool` 레지스터 재사용, 핫루프 최적화 및 컴파일된 플랜 캐시(`*runtime.PlanCache`) 운용.
* [`INTERNALS_CBO_VOLCANO.md`](file:///Users/maro/Projects/LionSQL/doc/INTERNALS_CBO_VOLCANO.md)
  * **비용 기반 최적화기 및 볼케이노 반복자**: Volcano Iterator 파이프라인, 비용 기반 쿼리 최적화기(CBO), 64-버킷 등고 히스토그램 기반 선택도 모델, 상수 폴딩, 조건절 푸시다운, `work_mem` 초과 시 디스크 외부 정렬(External Merge Sort) 스필 메커니즘.
* [`TSQL_REFERENCE.md`](file:///Users/maro/Projects/LionSQL/doc/TSQL_REFERENCE.md)
  * **T-SQL 구문 및 문법 종합 레퍼런스**: Microsoft SQL Server 호환 저장 프로시저, 스칼라 및 인라인 테이블 반환 함수(TVF), DML 트리거(`INSERTED`/`DELETED`), 제어문(`IF...ELSE`, `WHILE`, `TRY...CATCH`, `GOTO`), 커서 문법 가이드.
* [`EXTENSIONS.md`](file:///Users/maro/Projects/LionSQL/doc/EXTENSIONS.md)
  * **SQL 확장 및 UDF 아키텍처**: 윈도우 함수(`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`), CTE(Common Table Expressions), 외래 키 연쇄 동작, Go 네이티브 SPI 및 WebAssembly(Wasm) 샌드박스 기반 이원화 사용자 정의 함수(UDF) 확장 규격.
* [`PERCENTILE_AND_QUANTILE_ANALYTICS.md`](file:///Users/maro/Projects/LionSQL/doc/PERCENTILE_AND_QUANTILE_ANALYTICS.md)
  * **백분위수 및 스트리밍 분위수 분석 함수**: `PERCENTILE_CONT`, `PERCENTILE_DISC`, ANSI `WITHIN GROUP (ORDER BY ...)`, `MEDIAN`, T-Digest 기반 스트리밍 근사 분위수(`APPROX_PERCENTILE`) 알고리즘 명세.
* [`LOG_PROCESSING_GREP_AWK.md`](file:///Users/maro/Projects/LionSQL/doc/LOG_PROCESSING_GREP_AWK.md)
  * **엔진 내장 로그 및 텍스트 처리 연산자**: Unix 파이프라인 친화적인 `GREP`, `AWK`, `SPLIT_PART` 연산자, 제로카피 푸시다운 정규식 필터링 및 비정형 로그 분석 파이프라인.

#### 10.3. 와이어 프로토콜 및 클라이언트 호환성 (Protocols & Tool Integration)
* [`INTERNALS_WIRE_PROTOCOLS.md`](file:///Users/maro/Projects/LionSQL/doc/INTERNALS_WIRE_PROTOCOLS.md)
  * **와이어 프로토콜 구현 상세**: PostgreSQL Wire Protocol v3(포트 5433) Simple/Extended 프로토콜 패킷 핸들링과 MS SQL Server TDS 7.x/8.0(포트 1433) Prelogin, Login7, RPC, `sp_prepexec` 플랜 캐시 연동 구조.
* [`CLIENT_TOOL_INTEGRATION.md`](file:///Users/maro/Projects/LionSQL/doc/CLIENT_TOOL_INTEGRATION.md)
  * **GUI/CLI 도구 연동 및 카탈로그 가로채기**: Beekeeper Studio, DBeaver, DbGate, Azure Data Studio, SSMS, `psql`, `sqlcmd` 연동 가이드. 도구별 메타데이터 쿼리 가로채기(Catalog Interception), 페이지네이션 변환 및 프로그래밍 언어 드라이버 연결 문자열 가이드.

#### 10.4. BaaS 기능, 웹 SDK 및 REST API (BaaS, SDK & APIs)
* [`AUTO_REST_API.md`](file:///Users/maro/Projects/LionSQL/doc/AUTO_REST_API.md)
  * **무설정 자동 REST API**: PocketBase 스타일의 `/api/records/:table` CRUD 엔드포인트 자동 생성, URL 쿼리 연산자, 정렬, 페이징 필터링 사양.
* [`WEBHOOKS_AND_RLS.md`](file:///Users/maro/Projects/LionSQL/doc/WEBHOOKS_AND_RLS.md)
  * **웹훅 디스패처 및 행 수준 보안 (RLS)**: 비동기 워커 풀과 HMAC 서명 기반 웹훅 발송, 테이블별 선언적 행 수준 보안 규칙(`@auth.id`, `@auth.role` 토큰 치환) 적용 가이드.
* [`CRON_AND_MAILER.md`](file:///Users/maro/Projects/LionSQL/doc/CRON_AND_MAILER.md)
  * **임베디드 크론 스케줄러 및 트랜잭션 메일러**: 표준 5필드 크론 표현식을 통한 SQL/T-SQL 정기 배치 자동화와 링 버퍼 감사 로그를 갖춘 내장 트랜잭션 HTML SMTP 메일 발송기.
* [`WEB_SDK.md`](file:///Users/maro/Projects/LionSQL/doc/WEB_SDK.md)
  * **범용 웹 SDK 및 프론트엔드 연동 가이드**: 브라우저 임베디드 단일 파일 라이브러리(`sqlion.js`, `sqlion.d.ts`), 바닐라 JS, Server-Sent Events(SSE) 실시간 동기화, HTMX 연동 레시피.
* [`API_REFERENCE.md`](file:///Users/maro/Projects/LionSQL/doc/API_REFERENCE.md)
  * **HTTP REST & SSE API 사양서**: 시스템 헬스, 메트릭, 인증(JWT), 테이블 CRUD, 저장 프로시저 원격 호출, 백업/복원, 복제 스트리밍 엔드포인트 상세.

#### 10.5. 운영, 보안, 성능 및 문제 해결 (Operations, Security & Performance)
* [`TRANSACTION_PERFORMANCE_GUIDE.ko.md`](TRANSACTION_PERFORMANCE_GUIDE.ko.md)
  * **트랜잭션 성능 및 워크로드별 옵션 튜닝 가이드**: 인메모리 OCC부터 무손실 금융 원장까지 4대 워크로드 프로파일, Fsync Group Commit 원리, 하드웨어 스펙별 배포 레시피 및 실무 진단 체크리스트.
* [`OPERATIONS.md`](file:///Users/maro/Projects/LionSQL/doc/OPERATIONS.md)
  * **프로덕션 운영 및 배포 가이드**: 시스템 환경 요구사항, 데몬 구동, 무중단 리로드, 안전한 종료(Graceful Shutdown), 메모리 거버너 튜닝 가이드.
* [`METRICS.md`](file:///Users/maro/Projects/LionSQL/doc/METRICS.md)
  * **모니터링 및 메트릭스**: 프로메테우스 호환 `/metrics` 엔드포인트 지표 명세(QPS, 지연 시간, 연결 풀, 메모리 사용량) 및 슬로우 쿼리 로깅 진단.
* [`SECURITY.md`](file:///Users/maro/Projects/LionSQL/doc/SECURITY.md)
  * **보안 아키텍처 및 권한 제어**: 내장 관리자 계정, JWT 수명 주기, API 키 기반 역할 스코핑, TLS/SSL 전송 계층 암호화.
* [`BENCHMARK.md`](file:///Users/maro/Projects/LionSQL/doc/BENCHMARK.md)
  * **성능 벤치마크 및 검증 방법론**: TPC-C 벤치마크 측정 결과, pgbench 및 T-SQL 저장 프로시저 마이크로 벤치마크 처리량(TPS)과 지연 시간 프로파일.
* [`MIGRATION.md`](file:///Users/maro/Projects/LionSQL/doc/MIGRATION.md)
  * **이기종 데이터베이스 마이그레이션 가이드**: PostgreSQL, Microsoft SQL Server, SQLite, PocketBase로부터 데이터 스키마 및 레코드를 LionSQL으로 이전하는 절차.
* [`TROUBLESHOOTING.md`](file:///Users/maro/Projects/LionSQL/doc/TROUBLESHOOTING.md)
  * **장애 진단 및 문제 해결 가이드**: 포트 충돌, 권한 오류, 와이어 패킷 협상 실패, 트랜잭션 직렬화 충돌(`40001`), 클라이언트 캐시 무효화 등 빈발 오류 해결 방법.

#### 10.6. 개발자 엔지니어링 핸드오버 및 로드맵 문서 (Developer Context & Backlog)
* [`ROADMAP.md`](../ROADMAP.md)
  * 버전별 개발 마일스톤(Phase 1~6) 달성 현황 및 향후 중장기 핵심 로드맵.
* [`TODO.ko.md`](../TODO.ko.md)
  * 차기 착수 우선순위 과제(P1, P2, P3), CBO 및 성능 매트릭스, 도메인별 대기 백로그 및 최근 마일스톤 트래커.
* [`CHANGELOG.md`](../CHANGELOG.md)
  * 버전별 릴리즈 변경 내역 및 기능 추가/수정 기록 (Keep a Changelog 표준).
* [`notes/ENGINEERING_CONTEXT.md`](../notes/ENGINEERING_CONTEXT.md)
  * 아키텍처 불변 규칙, 대안 기술 Spec-Out 사유, 협업 엔지니어링 원칙.
* [`notes/PITFALLS_AND_TRICKS.md`](../notes/PITFALLS_AND_TRICKS.md)
  * 순환 참조 방지, wiredtiger 빌드 태그, 샌드박스 네트워크 격리, 소수점 JSON 직렬화 등 개발 시 주의점.


