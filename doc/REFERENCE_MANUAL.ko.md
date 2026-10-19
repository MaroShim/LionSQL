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

### 제10장. 부록: 세부 기술 문서 및 참조 안내 (Appendix: Technical Documentation Index)

본 레퍼런스 매뉴얼에서 다루는 각 서브시스템의 심층 설계, 내부 구조, 구현 알고리즘 및 벤치마크 결과는 아래의 세부 기술 문서(`doc/`)에서 상세히 확인하실 수 있습니다.

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


