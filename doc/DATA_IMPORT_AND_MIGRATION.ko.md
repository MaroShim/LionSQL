# LionSQL 데이터 가져오기, 대용량 벌크 적재 및 마이그레이션 가이드

> - **최초 작성일**: 2026-09-22
> - **최종 갱신일**: 2026-11-24
> - **엔진 릴리즈**: v0.93.0 (대용량 벌크 인서트 파이프라인 및 1.0-GA 준비)
> - **English**: [doc/DATA_IMPORT_AND_MIGRATION.md](DATA_IMPORT_AND_MIGRATION.md)

이 문서는 PostgreSQL, Microsoft SQL Server, SQLite 및 MySQL 데이터베이스로부터 스키마, 대용량 데이터 및 저장 프로시저를 LionSQL로 원활하게 마이그레이션하는 방법과 고속 대량 데이터 적재(Bulk Ingestion) 아키텍처를 안내합니다.

---

## 1. 통합 대용량 적재(Bulk Ingestion) 아키텍처

LionSQL은 외부 ETL 도구, 프록시 데몬, 또는 디스크 중간 임시 파일 없이 표준 SQL, PostgreSQL Wire Protocol v3 및 Microsoft SQL Server TDS 7.x/8.0 전반에 걸쳐 네이티브 고속 대량 데이터 적재 파이프라인을 제공합니다.

```
 [PostgreSQL 생태계]                [표준 SQL / T-SQL]                [SQL Server 생태계]
  psql \copy / pg_dump               INSERT INTO t VALUES (..)              bcp.exe / SqlBulkCopy
         │                                       │                                     │
         ▼                                       ▼                                     ▼
   [PGWire :5433]                         [SQL Exec DML]                         [TDS :1433]
  COPY FROM STDIN                         sqlexec_dml.go                       0x07 BCP Packet
         │                                       │                                     │
         └─────────────────┬─────────────────────┴─────────────────────────────────────┘
                           │
                           ▼ 배치 청크 슬라이스 ([]storage.Row, ChunkSize = 1,000)
             ┌───────────────────────────────────────────┐
             │   tx.BatchInsertRows(tableName, rows)     │
             │   (pkg/storage: memengine & wiredtiger)   │
             └─────────────────────┬─────────────────────┘
                                   │
         ┌─────────────────────────┴─────────────────────────┐
         ▼                                                   ▼
[Primary B-Tree 순차 고속 삽입]                 [Secondary Indexes 사전 정렬 배치 주입]
- 배치당 기본 쓰기 커서 1회 획득                - 각 인덱스별 (IndexKey, PK) 메모리 수집
- IDENTITY / PK 순차 리프 노드 패킹             - 인메모리 키 정렬 (sort.Slice) in O(N log N)
- 커서 재오픈 및 루트 노드 경합 0               - 인덱스 커서 단일 패스 전진 순차 적재 in O(N)
```

### 벌크 파이프라인의 핵심 불변식(Invariants):
1. **보조 인덱스 사전 정렬(Pre-Sorted) 배치 주입**:
   - $K$개의 보조 인덱스가 존재하는 테이블에 $N$개의 행을 단순 반복문으로 삽입하면, 매 건마다 $N \times K$회의 무작위 B-Tree 루트 탐색 및 극심한 페이지 분할(Page Split)이 발생합니다 ($O(N \cdot K \log M)$).
   - LionSQL은 배치 청크 내 인덱스 키 쌍을 아레나 버퍼에 수집한 뒤 `storage.CompareIndexKeys`로 인메모리 정렬하고, 인덱스 커서를 단 한 번만 열어 순차 전진 삽입합니다. 이로써 디스크 B-Tree 탐색 오버헤드가 선형 $O(N)$으로 축소되고 페이지 스플릿이 완전히 방지됩니다.
2. **기본 테이블 커서 라이프사이클 재활용**:
   - 행마다 B-Tree 커서를 열고 닫는 오버헤드를 제거하고, 1,000행 청크 단위로 단일 쓰기 커서를 획득하여 리프 노드에 연속 패킹합니다.
3. **유계 버퍼 스트리밍 (`[RULE:BoundedBuffers]`)**:
   - PGWire `COPY` 및 TDS `BCP` 프로토콜은 수백만 행의 테이블 전체를 메모리에 올리지 않고, 정확히 1,000행 단위 청크 버퍼를 통해 즉시 스토리지 엔진으로 흘려보냅니다. 따라서 테라바이트 급 대용량 데이터 이관 중에도 서버 메모리 사용량은 32MB 이하로 엄격히 유계 격리됩니다.

---

## 2. PostgreSQL에서 마이그레이션

LionSQL은 포트 `5433`에서 PostgreSQL Wire Protocol v3을 네이티브로 지원하며 고성능 `COPY` 스트리밍을 구현하므로, 기존 PostgreSQL 환경에서의 마이그레이션이 매우 간편합니다.

### 2.1. 데이터 타입 매핑

| PostgreSQL 타입 | LionSQL 타입 | 스토리지 표현 및 제약 조건 |
|---|---|---|
| `INT2`, `SMALLINT` | `INT` | 64비트 정수 (정규화) |
| `INT4`, `INTEGER` | `INT` | 64비트 정수 (정규화) |
| `INT8`, `BIGINT` | `INT` | 64비트 부호 있는 정수 |
| `SERIAL`, `BIGSERIAL` | `INT IDENTITY(1, 1)` | 자동 증가 64비트 정수 기본 키 |
| `VARCHAR(n)`, `CHAR(n)` | `VARCHAR` | 가변 길이 UTF-8 문자열 |
| `TEXT` | `VARCHAR` | 임의 길이 UTF-8 문자열 |
| `FLOAT4`, `REAL` | `FLOAT` | IEEE 754 64비트 부동소수점 |
| `FLOAT8`, `DOUBLE PRECISION` | `FLOAT` | IEEE 754 64비트 부동소수점 |
| `NUMERIC`, `DECIMAL` | `FLOAT` | 64비트 부동소수점 |
| `BOOLEAN`, `BOOL` | `BOOL` | 1바이트 불리언 플래그 (`TRUE` / `FALSE`) |
| `TIMESTAMP`, `TIMESTAMPTZ` | `DATETIME` | RFC3339 UTC 타임스탬프 |
| `DATE` | `DATETIME` | RFC3339 UTC 타임스탬프 (00:00:00) |
| `BYTEA` | `VARBINARY` | 바이너리 블롭 (16진수 `\x...`) |
| `JSON`, `JSONB` | `VARCHAR` | 내장 JSON 인덱싱 및 경로 쿼리 지원 |

### 2.2. 스키마 추출 및 DDL 적용

1. **PostgreSQL에서 DDL 스키마 추출**:
   ```bash
   pg_dump -h pg.example.com -U postgres -d production_db --schema-only --no-owner --no-privileges > schema.sql
   ```
2. **LionSQL에 DDL 적용**:
   ```bash
   psql -h 127.0.0.1 -p 5433 -U admin -d master -f schema.sql
   ```

### 2.3. `psql \copy`를 통한 직결 스트리밍 적재

LionSQL의 `pgCopyReader`는 수신되는 `CopyData` (`'d'`) 와이어 패킷을 제로 할당 토큰 분할로 파싱하고, 1,000행 슬라이스로 청킹하여 `tx.BatchInsertRows`로 고속 삽입합니다.

#### 유닉스 파이프 직결 마이그레이션 (중간 파일 0):
디스크에 중간 CSV 파일을 쓰지 않고, 원본 PostgreSQL 서버에서 LionSQL로 유닉스 파이프라인을 통해 직접 스트리밍합니다:
```bash
psql -h pg.example.com -U postgres -d production_db -c "\copy accounts to stdout with (format csv, header)" | \
psql -h 127.0.0.1 -p 5433 -U admin -d master -c "\copy accounts from stdin with (format csv, header)"
```

#### 로컬 CSV 파일 가져오기:
```bash
psql -h 127.0.0.1 -p 5433 -U admin -d master -c "\copy users from 'users.csv' with (format csv, header)"
```

#### 탭 구분자 텍스트 파일 가져오기:
```bash
psql -h 127.0.0.1 -p 5433 -U admin -d master -c "\copy logs from 'access.log' with (format text, delimiter E'\t')"
```

---

## 3. Microsoft SQL Server (MSSQL)에서 마이그레이션

LionSQL은 포트 `1433`에서 Microsoft SQL Server TDS 7.x/8.0 와이어 프로토콜, 공식 T-SQL 저장 프로시저 문법 및 **TDS 0x07 BCP (Bulk Copy Protocol)**을 완벽히 지원합니다.

### 3.1. 저장 프로시저 및 제어 흐름 호환성

T-SQL 저장 프로시저는 코드 수정 없이 LionSQL의 VDBE 바이트코드 가상 머신에서 네이티브로 실행됩니다:
- `DECLARE @var TYPE`, `SET @var = ...`, `SELECT @var = ...`
- 제어 흐름: `IF...ELSE`, `WHILE`, `BREAK`, `CONTINUE`
- 에러 처리: `BEGIN TRY ... END TRY`, `BEGIN CATCH ... END CATCH`, `ERROR_MESSAGE()`, `RAISERROR`
- 시스템 변수: `@@ROWCOUNT`, `@@ERROR`, `@@TRANCOUNT`, `@@IDENTITY`, `@@FETCH_STATUS`
- Forward-Only 커서: `DECLARE cur CURSOR FOR SELECT ...`, `OPEN cur`, `FETCH NEXT FROM cur INTO ...`, `CLOSE cur`, `DEALLOCATE cur`

프로시저는 `sqlcmd`, SSMS 또는 HTTP API를 통해 등록합니다:
```bash
sqlcmd -S 127.0.0.1,1433 -U admin -P password -i procedures.sql
```

### 3.2. 네이티브 TDS BCP 스트리밍 (`bcp.exe`)

LionSQL은 TDS `0x07` BCP 스트림 프로토콜 및 `INSERT BULK` 쿼리를 네이티브로 수신 처리합니다. 별도의 전용 드라이버 없이 Microsoft 공식 `bcp.exe` 유틸리티를 그대로 사용할 수 있습니다.

#### 문자 모드 적재 (CSV / 텍스트 구분자):
MSSQL에서 데이터 추출:
```bash
bcp "SELECT * FROM SalesDB.dbo.Orders" queryout orders.csv -c -t, -S mssql.prod -U sa -P secret
```

LionSQL로 직접 적재:
```bash
bcp [SalesDB].[dbo].[Orders] in orders.csv -S 127.0.0.1,1433 -U admin -P password -c -t, -b 1000
```

#### 네이티브 바이너리 모드 적재 (초고속 압축 포맷):
최고의 처리량이 요구되는 경우 SQL Server 네이티브 바이너리 포맷(`-n`)을 사용합니다:
```bash
# MSSQL에서 네이티브 바이너리 포맷으로 추출
bcp SalesDB.dbo.Customers out customers.dat -n -S mssql.prod -U sa -P secret

# LionSQL로 네이티브 바이너리 적재 (5,000행 배치 단위)
bcp [SalesDB].[dbo].[Customers] in customers.dat -n -b 5000 -S 127.0.0.1,1433 -U admin -P password
```

### 3.3. C# / .NET `SqlBulkCopy` 직접 스트리밍

LionSQL의 TDS BCP 구현체는 .NET 공식 `Microsoft.Data.SqlClient.SqlBulkCopy`와 100% 호환됩니다:

```csharp
using System;
using System.Data;
using Microsoft.Data.SqlClient;

class Program
{
    static void Main()
    {
        string connStr = "Server=127.0.0.1,1433;Database=SalesDB;User Id=admin;Password=password;TrustServerCertificate=True;";
        
        using (var connection = new SqlConnection(connStr))
        {
            connection.Open();
            
            using (var bulkCopy = new SqlBulkCopy(connection))
            {
                bulkCopy.DestinationTableName = "dbo.Transactions";
                bulkCopy.BatchSize = 1000;
                bulkCopy.BulkCopyTimeout = 60;
                
                // DataTable, IDataReader, 또는 DataRow 배열 스트리밍
                DataTable dataTable = FetchSourceData();
                bulkCopy.WriteToServer(dataTable);
            }
        }
        Console.WriteLine("대용량 벌크 적재가 성공적으로 완료되었습니다.");
    }
}
```

---

## 4. 멀티로우 고속 배치 삽입 (`INSERT INTO ... VALUES`)

LionSQL은 표준 SQL 다중 행 `INSERT` 구문을 파싱할 때 값 목록을 자동으로 감지하여 벡터화된 `tx.BatchInsertRows` 파이프라인으로 직결 라우팅합니다:

```sql
INSERT INTO bulk_items (sku, price, category)
VALUES 
    ('SKU-0001', 100, 'Electronics'),
    ('SKU-0002', 250, 'Hardware'),
    ('SKU-0003', 499, 'Audio'),
    -- ... 단일 쿼리로 수천 행 일괄 전송
    ('SKU-1000', 890, 'Accessories');
```

- **실행 경로**: `sqlexec_dml.go`가 모든 튜플을 검증한 후 단일 스토리지 트랜잭션 경계 내에서 `tx.BatchInsertRows`를 일괄 호출합니다.
- **실측 성능**: SQL 파싱 및 AST 토큰화 오버헤드를 모두 포함하고도 WiredTiger 디스크 엔진에서 **초당 195,000행 이상의 처리량**을 달성합니다.

---

## 5. SQLite에서 마이그레이션

SQLite 데이터베이스는 SQL 덤프 또는 CSV 내보내기를 통해 손쉽게 전환할 수 있습니다.

### 5.1. 구문 변환 주의사항
1. **자동 증가 기본 키**:
   - SQLite: `id INTEGER PRIMARY KEY AUTOINCREMENT`.
   - LionSQL: `id INT IDENTITY(1, 1) PRIMARY KEY` 또는 `id INT PRIMARY KEY` (LionSQL은 ID 생략 시 64비트 정수 ID를 자동 생성).
2. **타입 엄격성**:
   - SQLite는 동적 타입(Type Affinity)을 허용하여 컬럼 타입과 무관한 값을 저장할 수 있습니다. LionSQL에 테이블을 생성할 때는 각 컬럼의 용도에 맞는 명확한 타입(`INT`, `VARCHAR`, `FLOAT`, `BOOL`, `DATETIME`)을 지정해야 합니다.

### 5.2. 마이그레이션 단계
1. **SQLite 테이블을 CSV로 내보내기**:
   ```bash
   sqlite3 mydatabase.db -header -csv "SELECT * FROM users;" > users.csv
   ```
2. **LionSQL에 일치하는 테이블 생성**:
   ```sql
   CREATE TABLE users (
       id INT IDENTITY(1, 1) PRIMARY KEY,
       name VARCHAR NOT NULL,
       score FLOAT,
       created_at DATETIME NOT NULL
   );
   ```
3. **`psql` 또는 `bcp`를 통한 CSV 적재**:
   ```bash
   psql -h 127.0.0.1 -p 5433 -U admin -c "\copy users from 'users.csv' with (format csv, header)"
   ```

---

## 6. MySQL / MariaDB에서 마이그레이션

1. **식별자 따옴표 변환**:
   MySQL의 백틱(`` `col` ``)을 표준 큰따옴표(`"col"`) 또는 T-SQL 대괄호(`[col]`)로 변환합니다.
2. **데이터 타입 변환**:
   - `TINYINT(1)` ➔ `BOOL`
   - `DATETIME`, `TIMESTAMP` ➔ `DATETIME`
   - `VARCHAR(...)`, `LONGTEXT` ➔ `VARCHAR`
   - `BIGINT UNSIGNED` ➔ `INT` (64비트 정수)
3. **저장 프로시저 문법**:
   MySQL의 절차적 구문(`DELIMITER //`, `DECLARE CONTINUE HANDLER`)을 LionSQL의 표준 T-SQL 다이얼렉트(`BEGIN TRY...END TRY`, `@@FETCH_STATUS`)로 변환합니다.
4. **데이터 적재**:
   `mysqldump` 또는 `SELECT INTO OUTFILE`로 추출 후 `psql \copy` 또는 멀티로우 `INSERT`를 통해 고속 적재합니다.

---

## 7. 대용량 벌크 적재 성능 튜닝 및 베스트 프랙티스

| 권장 항목 | 최적 권장값 | 아키텍처적 근거 |
|---|---|---|
| **배치 크기 (`-b` / 청크)** | `1,000` ~ `5,000` 행 | CPU 캐시 효율 및 아레나 재활용을 극대화하면서 메모리 사용량을 32MB 이하로 유계 격리 |
| **보조 인덱스 유지** | 인덱스 활성 상태 유지 | 사전 정렬 배치 주입으로 페이지 분할이 억제되므로 적재 전 인덱스를 드롭할 필요가 없음 |
| **트랜잭션 경계 설정** | 배치 단위 (1,000 ~ 5,000행) | 수천만 건을 단일 거대 트랜잭션으로 묶어 락 리소스가 고갈되는 것을 방지 |
| **소켓 쿨다운 간격** | 대규모 배치 사이 5초 | 호스트 OS의 일시적 포트 고갈(`TIME_WAIT` 소켓 드레인) 방지 |
| **스토리지 엔진 선택** | `wiredtiger` (디스크) / `memory` (RAM) | WiredTiger NVMe 환경에서 초당 24.4만 행, In-Memory OCC에서 초당 21만 행 처리 |
