# Microsoft SQL Server ➔ LionSQL 마이그레이션 실무 체크리스트 (`doc/MSSQL_MIGRATION_CHECKLIST.ko.md`)

> - **최초 작성일**: 2026-11-12
> - **최종 갱신일**: 2026-11-12
> - **대상 릴리즈**: LionSQL v1.0-GA
> - **영문 버전**: [doc/MSSQL_MIGRATION_CHECKLIST.md](MSSQL_MIGRATION_CHECKLIST.md)

---

이 문서는 기존 Microsoft SQL Server(2016, 2017, 2019, 2022)에서 운영 중인 데이터베이스 스키마, 저장 프로시저, 뷰, 인덱스 및 비즈니스 데이터를 LionSQL로 원활하게 마이그레이션하기 위한 실무 체크리스트와 가이드를 제공합니다.

---

## 1. 데이터 타입 호환성 및 매핑 가이드

LionSQL은 Microsoft SQL Server의 핵심 데이터 타입을 네이티브하게 지원합니다.

| SQL Server 데이터 타입 | LionSQL 지원 여부 | LionSQL 내부 스토리지 타입 | 마이그레이션 주의사항 및 권장안 |
|---|:---:|---|---|
| `INT` / `INTEGER` | O | `int4` (4-byte signed int) | 100% 호환 |
| `BIGINT` | O | `int8` (8-byte signed int) | 100% 호환 |
| `SMALLINT` | O | `int2` (2-byte signed int) | 100% 호환 |
| `TINYINT` | O | `uint8` (1-byte unsigned) | 100% 호환 |
| `BIT` | O | `bool` (1-bit boolean) | `0`/`1` 정수 리터럴 및 boolean 자동 캐스팅 지원 |
| `DECIMAL(p, s)` / `NUMERIC` | O | `numeric` (Fixed precision) | 100% 고정 소수점 연산 호환 |
| `FLOAT` / `REAL` | O | `float8` / `float4` | IEEE 754 부동소수점 호환 |
| `VARCHAR(n)` / `CHAR(n)` | O | `varchar` (UTF-8 문자열) | LionSQL은 내부적으로 UTF-8로 저장 |
| `NVARCHAR(n)` / `NCHAR(n)` | O | `varchar` (UTF-8 유니코드) | 다국어 및 한글 100% 지원 |
| `DATETIME` / `DATETIME2` | O | `timestamp` (마이크로초) | `GETDATE()`, `CURRENT_TIMESTAMP` 호환 |
| `DATE` / `TIME` | O | `date` / `time` | 100% 호환 |
| `VARBINARY(n)` / `BINARY` | O | `varbinary` (바이트 슬라이스) | `COMPRESS()`, `DECOMPRESS()` 지원 |
| `UNIQUEIDENTIFIER` | O | `uuid` / `varchar(36)` | `NEWID()` 함수 호환 |
| `ROWVERSION` / `TIMESTAMP` | O | `varbinary(8)` | `@@DBTS` 전역 단조 증가 시퀀스 제공 |
| `XML` | X | `JSON` 또는 `VARCHAR` 권장 | **JSON으로 전환 권장** (`ISJSON`, `JSON_VALUE`) |
| `HIERARCHYID` / `GEOGRAPHY` | X | `VARCHAR` 또는 정규화 권장 | 공간 좌표는 위도/경도 숫자 컬럼으로 분리 권장 |

---

## 2. DDL 스키마 마이그레이션 체크리스트

### 2.1. 테이블 및 컬럼 제약조건
- [x] **`IDENTITY(1, 1)` 자동 증가 컬럼**:
  - LionSQL은 T-SQL 표준 `IDENTITY` 구문을 완벽 지원합니다.
  - 삽입 후 `@@IDENTITY`를 통해 발급된 ID 값을 조회할 수 있습니다.
- [x] **`PRIMARY KEY` 및 `UNIQUE` 인덱스**:
  - 기본키와 고유키 인덱스는 생성 즉시 스토리지 엔진의 인덱스로 자동 등록됩니다.
- [x] **`FOREIGN KEY` (외래키)**:
  - 참조 무결성 검증 및 `ON DELETE CASCADE`를 지원합니다.
- [x] **`DEFAULT` 제약조건**:
  - 상수 리터럴 및 내장 함수(`GETDATE()`, `0`, `''`) 기본값을 지원합니다.
- [x] **인스턴스 격리 및 `USE <dbname>`**:
  - LionSQL은 단일 디렉터리당 하나의 카탈로그를 관리합니다.
  - 마이그레이션 스크립트에 포함된 `USE master` 또는 `USE [dbname]` 구문은 에러 없이 허용(No-Op)되므로 스크립트 수정이 불필요합니다.

---

## 3. T-SQL 프로시저 및 비즈니스 로직 변환 체크리스트

### 3.1. 완벽 호환 구문 (수정 불필요)
1. **변수 선언 및 할당**: `DECLARE @name VARCHAR(50) = 'Lion'; SET @id = 1;`
2. **조건 분기**: `IF ... ELSE ... BEGIN ... END`
3. **반복문**: `WHILE ... BEGIN ... BREAK; CONTINUE; END`
4. **예외 처리**: `BEGIN TRY ... COMMIT TRANSACTION; END TRY BEGIN CATCH ... ROLLBACK; END CATCH`
5. **트랜잭션 제어**: `BEGIN TRAN`, `COMMIT TRAN`, `ROLLBACK TRAN`, `@@TRANCOUNT`
6. **내장 함수**: `LEN`, `SUBSTRING`, `CHARINDEX`, `REPLACE`, `CONCAT`, `ISNULL`, `COALESCE`, `GETDATE()`, `DATEDIFF`, `DATEADD`

### 3.2. 변환 권장 패턴
1. **커서 (CURSOR) ➔ 집합 기반 쿼리 또는 선형 순회**:
   - LionSQL은 기본 전방 전용(FORWARD-ONLY) 커서를 지원하지만, 고성능 OLTP를 위해 커서 루프를 집합 기반 `UPDATE ... FROM` 또는 `INSERT ... SELECT`로 재작성하는 것을 권장합니다.
2. **`THROW` ➔ `RAISERROR`**:
   - `THROW 50001, 'Error message', 1;` 대신 `RAISERROR('Error message', 16, 1);` 사용 권장.
3. **`SELECT * INTO #TempTable` ➔ 일반 테이블 또는 CTE**:
   - 임시 테이블 대신 공통 테이블 식(`WITH cte AS (...)`) 또는 명시적 테이블 선언을 권장합니다.

---

## 4. 데이터 대량 이전 (Data Bulk Migration) 절차

### 4.1. MS SQL Server에서 데이터 추출 (BCP / CSV)
기존 SQL Server에서 `bcp` 도구를 사용하여 데이터를 CSV로 추출합니다:
```bash
bcp "SalesDB.dbo.Customers" out "customers.csv" -c -t"," -r"\n" -S mssql-host -U sa -P password
```

### 4.2. LionSQL로 초고속 적재 (`psql \copy`)
LionSQL의 PostgreSQL 와이어 프로토콜 리스너(`:5433`)로 초고속 대량 복사를 수행합니다:
```bash
psql -h 127.0.0.1 -p 5433 -U postgres -d postgres -c "\copy customers FROM 'customers.csv' WITH (FORMAT csv, HEADER false);"
```
*Note: 대량 적재 시 VDBE 트랜잭션 파이프라인을 통해 초당 50,000~100,000행 이상으로 고속 삽입됩니다.*

---

## 5. 애플리케이션 연결 문자열 (Connection String) 변경

| 환경 | 기존 SQL Server 연결 문자열 | LionSQL 권장 연결 문자열 |
|---|---|---|
| **Go (`go-mssqldb`)** | `sqlserver://sa:pwd@host:1433?database=SalesDB` | `sqlserver://sa:@127.0.0.1:1433?database=master&encrypt=disable` |
| **Go (`pgx/v5`) [권장]** | - | `postgres://postgres:@127.0.0.1:5433/postgres?sslmode=disable` |
| **C# / .NET (ADO.NET)**| `Server=host;Database=SalesDB;User Id=sa;Password=pwd;` | `Server=127.0.0.1,1433;Database=master;User Id=sa;Password=;TrustServerCertificate=True;Encrypt=False;` |
| **Python (`psycopg2`)** | `DRIVER={ODBC};SERVER=host;DATABASE=SalesDB;...` | `host='127.0.0.1' port=5433 dbname='postgres' user='postgres' sslmode='disable'` |
| **Node.js (`node-postgres`)** | `mssql://sa:pwd@host:1433/SalesDB` | `postgresql://postgres:@127.0.0.1:5433/postgres` |

---

## 6. 마이그레이션 후 검증 절차

1. **테이블 및 행 수 검증**:
   ```sql
   SELECT t.name, COUNT(*) AS row_count FROM sys.tables t ...
   ```
2. **저장 프로시저 실행 테스트**:
   ```sql
   EXEC sp_TransferFunds @from=1, @to=2, @amount=500;
   ```
3. **CBO 통계 갱신**:
   모든 테이블에 대해 `ANALYZE TABLE <tablename>;`을 실행하여 최적의 인덱스 실행 계획을 수립합니다.
