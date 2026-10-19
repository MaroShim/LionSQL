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

LionSQL은 서브커맨드 패턴을 채택하여 다양한 운영 작업을 단일 실행 파일로 수행합니다:

```bash
lionsql [command] [flags...]
```

| 서브커맨드 | 설명 | 주요 플래그 |
| :--- | :--- | :--- |
| `[serve]` (기본) | 서버를 포그라운드 대화형 모드로 기동 (명령어 생략 시 기본 동작) | `-dir`, `-http`, `-pg`, `-tds`, `-dev`, `-max-memory` |
| `start` | 서버를 백그라운드 데몬 프로세스로 기동 (PID/Log 파일 관리) | `-dev`, `-dir` |
| `stop` | 실행 중인 백그라운드 데몬 프로세스를 안전하게 종료 (`SIGTERM`) | `-dir` |
| `restart` | 백그라운드 데몬을 무중단으로 안전하게 재기동 | `-dev`, `-dir` |
| `status` | 현재 실행 중인 데몬의 PID, 포트, 구동 상태를 출력 | `-dir` |
| `doctor` | 데이터 디렉토리 무결성, WAL LSN, 포트 충돌, 디스크 여유량 진단 | `-dir` |
| `init-config` | 기본값이 설정된 `lionsql.json` 설정 파일 템플릿 생성 | `-out` (기본값: `lionsql.json`) |
| `migrate` | 선언적 스키마 마이그레이션 도구 (`new`, `up`, `down`, `status`) | `-dir`, `-steps` |
| `sql` | CLI 터미널에서 즉시 SQL 쿼리를 실행하는 경량 쿼리 도구 | `-query`, `-dir` |
| `backup` | 가동 중인 데이터베이스의 온라인 무중단 스냅샷 백업 아카이브 생성 | `-dir`, `-out` (e.g. `backup.tar.gz`) |
| `restore` | 백업 아카이브(`.tar.gz`)를 지정된 디렉토리에 복원 | `-dir`, `-in` (e.g. `backup.tar.gz`) |
| `version` | 엔진 릴리즈 버전(`0.90.0`), 컴파일 환경, Git 커밋 해시 출력 | - |

---

### 1.2. 전역 CLI 플래그 사양

모든 플래그는 `-flag` 또는 `--flag` 형태로 전달할 수 있습니다:

```
NETWORK FLAGS:
  -http <addr>           HTTP REST 엔진 및 Web Studio 리스너 주소 (기본값: :8090, 비활성화: "")
  -pg <addr>             PostgreSQL Wire Protocol v3 리스너 주소 (기본값: :5433, 비활성화: "")
  -tds <addr>            MS SQL Server TDS 7.x/8.0 리스너 주소 (기본값: :1433, 비활성화: "")
  -tls                   전송 계층 보안(TLS/SSL) 암호화 활성화 (기본값: false)
  -tls-cert <path>       TLS 공개 인증서 파일 경로 (e.g. /etc/ssl/server.crt)
  -tls-key <path>        TLS 개인키 파일 경로 (e.g. /etc/ssl/server.key)
  -require-ssl           비암호화 일반 텍스트 연결 차단 및 SSL 연결 강제 (기본값: false)

STORAGE FLAGS:
  -dir <path>            데이터베이스 물리 저장소 디렉토리 경로 (기본값: data)
  -engine <name>         스토리지 백엔드 선택: wiredtiger, memory (기본값: wiredtiger)
  -ephemeral             디스크 I/O를 100% 차단하는 순수 인메모리 휘발성 모드 (기본값: false)
  -auto-seed             데이터 디렉토리가 비어있을 때 샘플 E-Commerce 시드 데이터 자동 주입 (기본값: false)

RESOURCE & GOVERNOR FLAGS:
  -max-memory <size>     글로벌 힙 메모리 상한선 (단위: MB/GB, e.g. 2GB, 4096MB)
                         (기본값: 호스트 물리 RAM의 70% 자동 감지)
  -work-mem <size>       연산자당 정렬/해시 조인 작업 메모리 예산 (기본값: 64MB)
  -max-workers <int>     동시 클라이언트 요청을 처리할 고루틴 워커 풀 상한 (기본값: 256)
  -max-idle-sessions <n> 유휴 세션 커넥션 풀 유지 상한 (기본값: 64)

SECURITY & AUTH FLAGS:
  -auth                  인증 및 권한 인가 활성화 (기본값: true, 개발 시 비활성화: -auth=false)
  -admin-email <email>   기본 관리자 이메일 계정 (기본값: admin@lionsql.local)
  -admin-pass <secret>   기본 관리자 로그인 비밀번호 (기본값: 1234567890)
  -jwt-secret <secret>   REST API 인증 토큰 서명용 HMAC-SHA256 시크릿 (미지정 시 자동 생성)

LOGGING FLAGS:
  -dev                   개발자 실시간 컬러풀 쿼리 트레이싱 모드 활성화 (기본값: false)
  -slow-log <path>       슬로우 쿼리 전용 로그 파일 경로 (기본값: data/slow_queries.log)
  -slow-threshold-ms <n> 슬로우 쿼리 판정 실행 시간 임계치 (기본값: 100.0 ms)
  -general-log <path>    모든 인바운드 SQL 문장을 기록할 감사 로그 파일 경로 (기본값: "")

REPLICATION FLAGS:
  -replica-of <url>      지정된 Primary 노드에 접속하여 WAL을 수신받는 Replica 모드 기동 (e.g. http://10.0.0.1:8090)
  -node-id <id>          클러스터 내 고유 노드 식별자 (기본값: node-1)
  -auto-failover         Primary 장애 시 Replica 노드의 자동 승격 활성화 (기본값: false)
  -peers <urls>          하트비트 교환을 위한 피어 노드 목록 (쉼표 구분)
  -heartbeat-timeout-ms  리더 장애 판단 타임아웃 (기본값: 3000 ms)
```

---

### 1.3. 벤치마크 도구 CLI 사양 (`bin/bench`, `bin/tpcc_vdbe`)

* **`bin/bench` (고동시성 OLTP 마이크로 벤치마크)**:
  - `-accounts <int>`: 초기 고객 계좌 수 (기본값: `10000`)
  - `-workers <int>`: 동시 클라이언트 워커 수 (기본값: `100`)
  - `-ops <int>`: 실행할 총 원자적 이체 트랜잭션 수 (기본값: `50000`)
  - `-engine <name>`: 스토리지 엔진 (`wiredtiger`, `memory`)
* **`bin/tpcc_vdbe` (공식 TPC-C T-SQL VDBE 벤치마크)**:
  - `-warehouses <int>`: TPC-C 규모 계수 (Warehouse 수, 기본값: `2`)
  - `-workers <int>`: 동시 클라이언트 워커 수 (기본값: `20`)
  - `-duration <time>`: 벤치마크 지속 시간 (e.g. `10s`, `1m`, `5m`)
  - `-fsync <policy>`: WAL 디스크 동기화 정책 (`everysec`, `always`, `off`)

---

### 1.4. 종료 코드 및 프로세스 시그널

* **종료 코드 (Exit Codes)**:
  - `0`: 정상 종료 (`SIGINT`, `SIGTERM`, 또는 `./bin/lionsql stop` 수신 완결)
  - `1`: 일반 런타임 오류 (포트 바인딩 실패, 스토리지 파일 잠금 실패)
  - `2`: 잘못된 CLI 플래그 또는 문법 오류
* **POSIX 시그널 처리 규약 (골든 룰 4)**:
  - `SIGINT` / `SIGTERM`: 즉시 수락 후 신규 인바운드 연결 차단 ➔ 실행 중인 쿼리 드레인 ➔ WiredTiger/AOF 플러시 ➔ 1.5초 이내에 안전 종료. 1.5초 초과 시 자체 `SIGKILL` 에스컬레이션.
  - `SIGHUP`: 로그 파일 디스크립터 재오픈 및 설정 파일 리로드.

---

## 제2장. 설정 파일 사양 (Configuration File Reference)

### 2.1. `lionsql.json` 스키마 및 키 상세 정의

```json
{
  "server": {
    "engine": "wiredtiger",           // "wiredtiger" | "memory"
    "data_dir": "data",               // 데이터 파일 디렉토리 경로
    "ephemeral": false,               // 디스크 I/O 배제 순수 인메모리 여부
    "auto_seed": false,               // 빈 DB 자동 시드 여부
    "pid_file": "data/lionsql.pid"    // 데몬 프로세스 ID 파일 경로
  },
  "network": {
    "http_addr": ":8090",             // HTTP REST & Web Studio 바인딩 주소
    "pg_addr": ":5433",               // PostgreSQL v3 바인딩 주소
    "tds_addr": ":1433",              // MS SQL Server TDS 바인딩 주소
    "tls": {
      "enabled": false,               // TLS 활성화 여부
      "cert_file": "",                // X.509 인증서 PEM 파일
      "key_file": "",                 // 개인키 PEM 파일
      "require_ssl": false            // 비암호화 연결 거부 여부
    }
  },
  "auth": {
    "enabled": true,                  // 보안 인증 활성화 여부
    "admin_email": "admin@lionsql.local", // 기본 관리자 이메일
    "admin_pass": "",                 // 관리자 비밀번호 (미지정 시 자동 유지)
    "jwt_secret": ""                  // JWT 서명 키 (미지정 시 난수 생성)
  },
  "resources": {
    "max_memory": "",                 // 글로벌 힙 메모리 상한 (예: "2GB", "4096MB", 빈값: 70% RAM)
    "work_mem": "64MB",               // 연산자당 메모리 예산 (기본값: "64MB")
    "max_workers": 256,               // 고루틴 워커 풀 상한
    "max_idle_sessions": 64           // 유휴 세션 커넥션 풀 상한
  },
  "logging": {
    "dev_mode": false,                // 개발자 콘솔 실시간 로깅 모드
    "slow_query": {
      "enabled": true,                // 슬로우 쿼리 로깅 활성화
      "threshold_ms": 100.0,          // 슬로우 쿼리 판정 기준 (100.0ms)
      "file": "data/slow_queries.log" // 슬로우 쿼리 로그 파일
    },
    "general_query": {
      "enabled": false,               // 전수 쿼리 감사 로깅 활성화
      "file": "data/general_query.log"// 감사 로그 파일 경로
    }
  },
  "replication": {
    "enabled": false,                 // 복제 서브시스템 활성화
    "role": "PRIMARY",                // "PRIMARY" | "REPLICA"
    "node_id": "node-1",              // 클러스터 노드 고유 식별자
    "primary_url": "",                // Replica일 때 대상 Primary HTTP URL
    "auto_failover": false,           // 자동 장애 조치 승격 활성화
    "peers": [],                      // Raft/Gossip 피어 URL 목록
    "self_url": "",                   // 노드 본인의 공용 주소
    "heartbeat_timeout_ms": 3000      // 하트비트 타임아웃
  }
}
```

---

## 제3장. 데이터 타입 및 리터럴 (Data Types & Literals Reference)

LionSQL은 Microsoft SQL Server T-SQL 및 ANSI/PostgreSQL 표준 데이터 타입을 광범위하게 지원합니다:

| 데이터 타입 | 물리 저장 바이트 | 유효 범위 및 기술 사양 | 리터럴 표기 예시 |
| :--- | :---: | :--- | :--- |
| **`TINYINT`** | 1 Byte | $0 \sim 255$ (부호 없는 8비트 정수) | `255`, `0` |
| **`SMALLINT`** | 2 Bytes | $-32,768 \sim 32,767$ (부호 있는 16비트 정수) | `-100`, `32000` |
| **`INT` / `INTEGER`** | 4 Bytes | $-2,147,483,648 \sim 2,147,483,647$ (32비트 정수) | `1001`, `0` |
| **`BIGINT`** | 8 Bytes | $-9.22 \times 10^{18} \sim 9.22 \times 10^{18}$ (64비트 정수) | `9223372036854775807` |
| **`FLOAT` / `REAL`** | 8 Bytes | 64비트 IEEE 754 배정밀도 부동소수점 | `3.141592`, `-0.001` |
| **`DECIMAL(p, s)`** | 가변 길이 | 최대 38자리 고정밀 수치 ($p$: 전체 자리수, $s$: 소수점 자리수) | `12345.67`, `CAST('99.9' AS DECIMAL(10,2))` |
| **`VARCHAR(n)`** | 가변 길이 | 최대 8,000자 가변 길이 ASCII/UTF-8 문자열 | `'Hello World'`, `'LionSQL'` |
| **`NVARCHAR(n)`** | 가변 길이 | 유니코드 UTF-16LE / UTF-8 문자열 (N-prefix 지원) | `N'안녕하세요'`, `N'LionSQL'` |
| **`TEXT`** | 가변 길이 | 최대 2GB 무제한 대용량 텍스트 (PLP 청킹 스트리밍) | `'Very long text...'` |
| **`DATE`** | 4 Bytes | `0001-01-01` ~ `9999-12-31` (년-월-일) | `'2026-10-19'` |
| **`TIME`** | 8 Bytes | `00:00:00.000` ~ `23:59:59.999` (밀리초 정밀도 시:분:초) | `'17:30:00'` |
| **`DATETIME`** | 8 Bytes | `1753-01-01` ~ `9999-12-31` (밀리초 정밀도 일시) | `'2026-10-19 17:30:00'` |
| **`BIT` / `BOOLEAN`** | 1 Byte | `0` (FALSE) 또는 `1` (TRUE) | `1`, `0`, `TRUE`, `FALSE` |
| **`UUID`** | 16 Bytes | 128비트 범용 고유 식별자 (RFC 4122 / UUIDv7 지원) | `'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'` |
| **`JSON`** | 가변 길이 | 유효한 JSON 형식 텍스트 검증 및 B-Tree 함수형 인덱싱 | `'{"name": "Alice", "score": 95}'` |

---

## 제4장. SQL 구문 및 명령문 사양 (SQL Statement Reference)

### 4.1. 데이터 정의어 (DDL)

```sql
-- 1. 테이블 생성 (IDENTITY, CHECK, FOREIGN KEY, NOT NULL, DEFAULT 지원)
CREATE TABLE schema_name.table_name (
    id INT IDENTITY(1,1) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    age INT CHECK (age >= 0),
    balance DECIMAL(18,2) DEFAULT 0.00,
    created_at DATETIME DEFAULT GETDATE(),
    CONSTRAINT fk_ref FOREIGN KEY (parent_id) REFERENCES parent(id) ON DELETE CASCADE
);

-- 2. 컬럼 추가 (비파괴적 원자적 DDL)
ALTER TABLE table_name ADD column_name VARCHAR(200) DEFAULT 'N/A';

-- 3. 테이블 완전 삭제 및 절단
DROP TABLE [IF EXISTS] table_name;
TRUNCATE TABLE table_name;

-- 4. 인덱스 생성 및 삭제 (커버링 인덱스 INCLUDE 지원)
CREATE [UNIQUE] INDEX index_name ON table_name(col1 [ASC|DESC], col2) [INCLUDE (col3, col4)];
DROP INDEX index_name [ON table_name];

-- 5. T-SQL 저장 프로시저 정의 및 삭제
CREATE PROCEDURE proc_name
    @param1 INT,
    @param2 VARCHAR(50) OUTPUT
AS
BEGIN
    SET NOCOUNT ON;
    -- 비즈니스 로직
END;
DROP PROCEDURE [IF EXISTS] proc_name;
```

---

### 4.2. 데이터 조작어 (DML)

```sql
-- 1. 기본 SELECT (TOP, DISTINCT, 별칭 지원)
SELECT [TOP (n) [PERCENT]] [DISTINCT] col1 AS alias1, col2
FROM table_name [WITH (INDEX(index_name))]
WHERE condition
GROUP BY col1 HAVING aggregate_condition
ORDER BY col1 [ASC|DESC]
OFFSET n ROWS FETCH NEXT m ROWS ONLY;

-- 2. INSERT (단일 행, 다중 행, DEFAULT VALUES)
INSERT INTO table_name (col1, col2) VALUES ('Val1', 100), ('Val2', 200);
INSERT INTO table_name DEFAULT VALUES;

-- 3. UPDATE (조인 갱신 및 수식 연산 지원)
UPDATE table_name
SET col1 = 'NewVal', balance = balance + 500
WHERE id = 10;

-- 4. DELETE
DELETE FROM table_name WHERE created_at < '2026-01-01';

-- 5. SELECT INTO (변수 바인딩)
SELECT balance INTO @user_bal FROM accounts WHERE id = 1;
```

---

### 4.3. 세션 및 트랜잭션 제어어 (TCL / SCL)

```sql
-- 트랜잭션
BEGIN TRANSACTION [transaction_name];
COMMIT TRANSACTION;
ROLLBACK TRANSACTION;

-- 세션 환경 설정
USE <database>;                         -- 관용적 가상 No-Op 수용 (master / postgres 동기화)
SET NOCOUNT ON;                         -- 행 영향 카운트 토큰 방출 억제
SET work_mem = '128MB';                 -- 세션별 정렬/조인 메모리 예산 상향
SET TRANSACTION ISOLATION LEVEL READ COMMITTED;
```

---

## 제5장. 연산자 및 내장 함수 사양 (Functions & Operators Reference)

### 5.1. 연산자 요약
* **산술**: `+`, `-`, `*`, `/`, `%`
* **비교**: `=`, `<>`, `!=`, `<`, `<=`, `>`, `>=`
* **논리**: `AND`, `OR`, `NOT`, `IS NULL`, `IS NOT NULL`, `IN (...)`, `BETWEEN ... AND ...`
* **문자열 연결**: `+` (T-SQL 호환), `||` (ANSI 호환)
* **패턴 매칭**: `LIKE 'prefix%'`, `LIKE '%contain%'`, `NOT LIKE '_a%'`

---

### 5.2. 핵심 내장 함수 목록

| 함수 서명 | 분류 | 설명 | 반환 예시 |
| :--- | :---: | :--- | :--- |
| `COALESCE(v1, v2, ...)` | 조건부 | 첫 번째 Non-NULL 값을 반환 | `COALESCE(NULL, 'Default')` ➔ `'Default'` |
| `ISNULL(check, repl)` | T-SQL | check가 NULL이면 repl을 반환 | `ISNULL(val, 0)` |
| `NULLIF(v1, v2)` | 조건부 | v1과 v2가 같으면 NULL 반환 | `NULLIF(10, 10)` ➔ `NULL` |
| `COUNT(*)`, `SUM(c)` | 집계 | 건수 및 합계 계산 | `COUNT(*)`, `SUM(amount)` |
| `ROW_NUMBER() OVER (...)` | 윈도우 | 파티션/정렬 기준 단조 증가 일련번호 부여 | `1, 2, 3...` |
| `RANK() OVER (...)` | 윈도우 | 동순위 건너뛰기 순위 부여 | `1, 2, 2, 4...` |
| `PERCENTILE_CONT(p)` | 분석 | T-Digest 기반 스트리밍 연속 분위수 계산 | `PERCENTILE_CONT(0.99) WITHIN GROUP (...)` |
| `LEN(str)` | 문자열 | 문자열의 글자 수 반환 (후행 공백 제외) | `LEN('LionSQL ')` ➔ `7` |
| `SUBSTRING(s, start, len)` | 문자열 | 1-based 인덱스 기준 부분 문자열 추출 | `SUBSTRING('LionSQL', 1, 4)` ➔ `'Lion'` |
| `STRING_SPLIT(str, sep)` | 테이블 | 구분자 기준 분할 테이블 반환 함수 (TVF) | `SELECT value FROM STRING_SPLIT('a,b,c', ',')` |
| `GETDATE()` | 일시 | 현재 데이터베이스 서버 로컬 일시 | `2026-10-19 17:30:00` |
| `DATEADD(part, n, date)`| 일시 | 지정된 날짜 부분(year, month, day) 가감 | `DATEADD(day, 7, GETDATE())` |
| `DATEDIFF(part, d1, d2)`| 일시 | 두 일시 간의 차이 계산 | `DATEDIFF(day, '2026-01-01', '2026-01-10')` ➔ `9` |
| `CAST(expr AS type)` | 변환 | 명시적 데이터 타입 캐스팅 | `CAST('100' AS INT)` ➔ `100` |
| `CONVERT(type, expr)` | 변환 | T-SQL 호환 데이터 변환 | `CONVERT(VARCHAR, GETDATE(), 120)` |
| `NEWID7()` | 식별자 | 시간 순 정렬 가능한 IETF RFC 9562 Monotonic UUIDv7 | `0192a5b6-7c8d-7e9f-8a0b-1c2d3e4f5a6b` |
| `DB_NAME()` | 메타 | 현재 세션의 가상 데이터베이스명 반환 | `'postgres'` 또는 `'master'` |

---

## 제6장. 가상 카탈로그 뷰 및 메타데이터 사양 (System Catalogs Reference)

LionSQL은 외부 GUI 관리 도구(DBeaver, Beekeeper Studio, Navicat, SSMS)와의 100% 매끄러운 스키마 탐색을 위해 3대 주요 시스템 카탈로그 뷰를 메모리 상에서 가상화(Virtual Catalog)하여 제공합니다:

### 6.1. MS SQL Server 호환 카탈로그 (`sys.*`)

* **`sys.databases`**:
  - `name`: 가상 기본 데이터베이스명 (`master`)
  - `database_id`: 고정값 `1`
  - `compatibility_level`: `150` (SQL Server 2019 호환)
* **`sys.master_files`**:
  - `database_id`: `1`, `type_desc`: `'ROWS'`, `name`: `'master'`, `physical_name`: `'data/master.mdf'`
* **`sys.tables`**:
  - `name`: 테이블 이름, `object_id`: 고유 테이블 ID, `schema_id`: `1` (`dbo`), `type`: `'U'` (User Table)
* **`sys.columns`**:
  - `object_id`: 테이블 ID, `name`: 컬럼명, `column_id`: 1-based 순번, `system_type_id`: 데이터 타입 코드, `max_length`: 최대 바이트 길이, `is_nullable`: Null 허용 여부, `is_identity`: 자동증가 Identity 여부
* **`sys.indexes` & `sys.index_columns`**:
  - `object_id`: 테이블 ID, `name`: 인덱스명, `index_id`: 1(PK/Clustered) 또는 2+(Secondary), `is_unique`: 고유 인덱스 여부, `is_primary_key`: 기본키 여부

---

### 6.2. ANSI / ISO 호환 카탈로그 (`INFORMATION_SCHEMA.*`)

* **`INFORMATION_SCHEMA.TABLES`**:
  - `TABLE_CATALOG`: `'postgres'` (TDS에서는 `'master'`)
  - `TABLE_SCHEMA`: `'public'` (TDS에서는 `'dbo'`)
  - `TABLE_NAME`: 테이블 명칭
  - `TABLE_TYPE`: `'BASE TABLE'` 또는 `'VIEW'`
* **`INFORMATION_SCHEMA.COLUMNS`**:
  - `COLUMN_NAME`, `ORDINAL_POSITION`, `COLUMN_DEFAULT`, `IS_NULLABLE`, `DATA_TYPE`, `CHARACTER_MAXIMUM_LENGTH`, `NUMERIC_PRECISION`, `NUMERIC_SCALE`
* **`INFORMATION_SCHEMA.KEY_COLUMN_USAGE` & `REFERENTIAL_CONSTRAINTS`**:
  - 외래키 참조 관계 및 기본키 컬럼 목록을 외부 ORM/도구에 정확히 제공

---

### 6.3. PostgreSQL 호환 카탈로그 (`pg_catalog.*`)

* **`pg_catalog.pg_database`**:
  - `datname`: `'postgres'` (레거시 `sqlion`을 완전 제거하고 단일 DB 정립)
  - `datdba`: `10`, `encoding`: `6` (UTF-8), `datcollate`: `'C'`, `datctype`: `'C'`
* **`pg_catalog.pg_class`**:
  - `relname`: 테이블/인덱스 명칭, `relnamespace`: `2200` (`public`), `relkind`: `'r'`(Table) 또는 `'i'`(Index)
* **`pg_catalog.pg_attribute`**:
  - `attrelid`: 테이블 OID, `attname`: 컬럼명, `atttypid`: PostgreSQL 내부 OID(int4: 23, varchar: 1043 등), `attnum`: 컬럼 번호, `attnotnull`: Not Null 여부
* **`pg_catalog.pg_stat_activity`**:
  - `pid`: 세션 ID, `datname`: `'postgres'`, `usename`: `'admin'`, `state`: `'idle'` 또는 `'active'`, `query`: 현재 실행 쿼리

---

## 제7장. REST API 및 웹 스튜디오 사양 (REST API Reference)

LionSQL의 모든 HTTP 엔드포인트는 기본 포트 `:8090`에서 서비스되며, JSON 기반 페이로드를 사용합니다.

### 7.1. 시스템 및 실시간 관제

| 메소드 & 엔드포인트 | 인증 필요 | 설명 및 반환 포맷 |
| :--- | :---: | :--- |
| `GET /api/health` | No | 서버 헬스체크. `{"status":"ok","version":"0.90.0","engine":"wiredtiger"}` |
| `GET /api/version` | No | 엔진 릴리즈 버전 및 컴파일 메타데이터 반환 |
| `GET /api/metrics` | No | Prometheus 스크랩 규격 텍스트 포맷 (QPS, 메모리 힙, 연결 수) |
| `GET /api/realtime` | No | Server-Sent Events (SSE) 실시간 성능/활동 모니터링 영구 스트림 |

### 7.2. 인증 및 세션 관리

| 메소드 & 엔드포인트 | 설명 | 요청 본문 및 반환값 |
| :--- | :--- | :--- |
| `POST /api/auth/login` | 관리자 로그인 및 JWT 발급 | **Body**: `{"email":"admin@lionsql.local","password":"secret"}`<br>**Response**: `{"token":"ey...", "expires_in": 86400}` |
| `POST /api/auth/refresh` | 만료 전 JWT 토큰 갱신 | **Header**: `Authorization: Bearer <TOKEN>` |

### 7.3. SQL 및 테이블/레코드 CRUD

| 메소드 & 엔드포인트 | 인증 필요 | 설명 및 사용 예시 |
| :--- | :---: | :--- |
| `POST /api/sql` | Yes | 임의 T-SQL/SQL 쿼리 실행. `{"query":"SELECT * FROM accounts"}` |
| `GET /api/tables` | No | 모든 테이블 목록 및 스키마 메타데이터 반환 |
| `POST /api/tables/:table/columns` | Yes | 비파괴적 컬럼 추가 DDL API. `{"name":"phone","type":"VARCHAR(50)"}` |
| `GET /api/records/:table` | No | 테이블 데이터 페이지네이션 및 필터링 (`?page=1&perPage=20&sort=-id`) |
| `POST /api/records/:table` | Yes | 단일 레코드 생성. `{"holder_name":"John", "balance": 1000}` |
| `PUT /api/records/:table` | Yes | 레코드 갱신 (기본키 기준 수정). `{"id": 1, "balance": 2000}` |
| `DELETE /api/records/:table` | Yes | 레코드 삭제. `{"id": 1}` |

### 7.4. 활동 감사 및 슬로우 쿼리 로그

* **`GET /api/logs/requests`**:
  - 최근 500건의 인바운드 HTTP 및 SQL 활동 로그 링 버퍼 조회.
  - 응답 필드: `timestamp`, `method`, `path`, `client_ip`, `status`, `latency_us`, `query`
* **`GET /api/slow-queries`**:
  - 임계치(`100ms`)를 초과한 모든 슬로우 쿼리 내역 및 실행 통계 반환.

### 7.5. 백업, 복제 및 프로시저 원격 호출

* **`POST /api/procs/exec`**: 저장 프로시저 원격 RPC 호출 (`{"name":"TransferMoney","params":{"from_id":1,"to_id":2,"amount":500}}`).
* **`POST /api/backup`**: 온라인 무중단 핫 백업 `.tar.gz` 바이너리 스트리밍 다운로드.
* **`POST /api/restore`**: 멀티파트 폼 데이터로 `.tar.gz` 백업 파일 업로드 및 전체 복원.
* **`GET /api/replication/status`**: 노드 역할(`PRIMARY` / `REPLICA`), LSN 상태, 복제 지연 시간 반환.
* **`POST /api/replication/promote`**: Replica 노드를 즉시 새 Primary로 수동 승격.

---

## 제8장. 와이어 프로토콜 기술 사양 (Wire Protocol Specifications)

### 8.1. PostgreSQL Wire Protocol v3 사양 (`TCP :5433`)

LionSQL은 공식 PostgreSQL 클라이언트 라이브러리와 바이너리 수준에서 프레임을 교환합니다:

1. **연결 및 인증 단계 (Startup & Authentication)**:
   - 클라이언트 `StartupMessage` (포맷: 패킷 길이, 프로토콜 버전 `196608` [3.0], 파라미터 쌍 `user`, `database`).
   - 서버가 `AuthenticationCleartextPassword` (`'R'`, code `3`) 또는 `AuthenticationOk` (`'R'`, code `0`) 방출.
   - 서버 환경 파라미터 송출: `server_version=14.0 (LionSQL v0.90.0)`, `client_encoding=UTF8`.
   - `ReadyForQuery` (`'Z'`) 방출로 세션 준비 완료.
2. **단순 쿼리 프로토콜 (Simple Query Protocol)**:
   - 클라이언트 `Query` (`'Q'`) ➔ 서버 `RowDescription` (`'T'`) ➔ 다중 `DataRow` (`'D'`) ➔ `CommandComplete` (`'C'`) ➔ `ReadyForQuery` (`'Z'`).
3. **확장 쿼리 프로토콜 (Extended Query Protocol)**:
   - `Parse` (`'P'`): SQL 문장 컴파일 및 Statement 캐싱.
   - `Bind` (`'B'`): 매개변수($1, $2) 바이너리 디코딩 및 Portal 생성.
   - `Execute` (`'E'`): 지정된 최대 행 수(`max_rows`)만큼 Volcano Iterator 반복 실행.
   - `Sync` (`'S'`): 트랜잭션 경계 확정 및 `ReadyForQuery` 반환.

---

### 8.2. Microsoft SQL Server TDS 7.x / 8.0 Protocol 사양 (`TCP :1433`)

1. **사전 로그인 협상 (Pre-Login Handshake)**:
   - 패킷 타입: `0x12` (`Pre-Login`).
   - 서버 버전(`Version: 15.0.2000`), 암호화 미지원 플래그(`Encryption: ENCRYPT_NOT_SUP`), 인스턴스 토큰 교환.
2. **로그인 단계 (Login7)**:
   - 패킷 타입: `0x10` (`Login7`).
   - 클라이언트 호스트명, 사용자명(`sa`), 암호(TDS 고유 비트 시프트 스크램블), 데이터베이스(`master`) 수신.
   - 검증 성공 시 `ENVCHANGE (0xE3)` 토큰으로 데이터베이스 컨텍스트 동기화 후 `LOGINACK (0xAD)` 및 `DONE (0xFD)` 방출.
3. **토큰 스트림 구조 (Tabular Data Stream)**:
   - `COLMETADATA (0x81)`: 결과셋의 컬럼 수, 컬럼명, 데이터 타입(INT4, VARCHAR, DATETIME) 메타데이터.
   - `ROW (0xD1)`: 일반 행 데이터 바이너리 직렬화.
   - `NBCROW (0xD2)`: Null Bitmap 기반 압축 행 (Null 컬럼의 데이터 페이로드를 완전히 생략하여 네트워크 대역폭 최적화).
   - `DONE (0xFD)`: 배치 명령 완료 및 영향받은 행 수(Row Count) 방출.
   - `ERROR (0xAA)`: 예외 발생 시 에러 번호, 심각도(Severity), 상태 코드, 에러 메시지 방출.

---

## 제9장. 에러 코드 및 진단 레퍼런스 (Error Codes & Diagnostics)

### 9.1. LionSQL 표준 에러 및 ANSI SQLSTATE 매핑표

| 내부 에러 코드 | ANSI SQLSTATE | 표준 영문 에러 메시지 | 원인 및 해결 조치 |
| :--- | :---: | :--- | :--- |
| `ERR_TABLE_NOT_FOUND` | `42P01` | `table does not exist` | 지정한 테이블이 존재하지 않음. 테이블명 대소문자 확인 |
| `ERR_COLUMN_NOT_FOUND` | `42703` | `column does not exist` | 컬럼명이 올바르지 않음. 스키마 확인 |
| `ERR_UNIQUE_VIOLATION` | `23505` | `unique constraint violation: duplicate key` | PRIMARY KEY 또는 UNIQUE 인덱스 중복 삽입 시 발생 |
| `ERR_WRITE_CONFLICT` | `40001` | `serialization failure: write-write conflict` | OCC 동시성 충돌. 트랜잭션 재시도(Retry) 필요 |
| `ERR_MEMORY_BUDGET` | `53200` | `hash join memory budget exceeded: ... > work_mem` | 조인 연산자가 `work_mem` 초과. 필터 조건 추가 또는 work_mem 상향 |
| `ERR_STORAGE_FULL` | `53200` | `table data limit exceeded: memory limit exceeded` | 인메모리 엔진 힙 상한(`--max-memory`) 도달. 오래된 데이터 정리 |
| `ERR_FOREIGN_KEY` | `23503` | `foreign key violation: referenced key not found` | 외래키 무결성 위반. 부모 테이블의 대상 행 존재 여부 확인 |
| `ERR_SYNTAX_ERROR` | `42601` | `syntax error in SQL statement` | SQL/T-SQL 구문 문법 오류 |
| `ERR_CONNECTION_LOST` | `08006` | `connection failure: client aborted` | 클라이언트 소켓 강제 단절 또는 타임아웃 |

---

### 9.2. TDS 에러 심각도 레벨 (TDS Severity Levels)

* **Severity 1 ~ 10 (정보 및 경고)**: `PRINT` 문장 출력, 비정상적이지 않은 알림 (클라이언트 연결 유지).
* **Severity 11 ~ 16 (사용자 실행 오류)**: 제약조건 위반, 문법 오류, `RAISERROR` 사용자 예외 (해당 배치만 중단).
* **Severity 17 ~ 19 (리소스 부족 오류)**: `work_mem` 초과, 메모리 거버너 차단 (세션 롤백).
* **Severity 20 ~ 25 (시스템 치명 오류)**: 스토리지 I/O 장애, 프로세스 손상 (연결 즉시 강제 종료).

---

### 9.3. PostgreSQL 오류 심각도 (PG Error Severity)

* **`NOTICE`**: DDL 자동 캐스케이드 알림, 정보성 메시지.
* **`WARNING`**: 트랜잭션 블록 외부에서의 롤백 시도 등 경고.
* **`ERROR`**: 쿼리 실패, 제약조건 위반 (트랜잭션이 Aborted 상태로 전이되며 `ROLLBACK` 요구).
* **`FATAL`**: 인증 실패, 관리자 강제 종료 (클라이언트 연결 닫힘).
* **`PANIC`**: 시스템 비정상 종료.

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


