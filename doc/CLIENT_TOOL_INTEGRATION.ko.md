# LionSQL 클라이언트 및 GUI 도구 연동 가이드 (`doc/CLIENT_TOOL_INTEGRATION.ko.md`)

> - **최초 작성일**: 2026-10-08
> - **최종 갱신일**: 2026-11-12

---

이 문서는 PostgreSQL Wire Protocol v3(`:5433`) 및 Microsoft SQL Server TDS Protocol(`:1433`)을 통해 LionSQL에 연결하는 GUI 데이터베이스 관리 도구, 명령줄 인터페이스(CLI), 그리고 공식 프로그래밍 언어 드라이버의 연동 설정 및 기술 세부사항을 제공합니다.

---

## 1. 멀티 와이어 프로토콜 개요 및 기본 포트

LionSQL은 외부 의존성이 전혀 없는 독립형 데이터베이스 엔진으로, 단일 정적 실행 파일(`bin/lionsql`)에서 PostgreSQL 및 Microsoft SQL Server 클라이언트를 동시에 수용합니다.

| 프로토콜 | 기본 포트 | 기본 관리자 계정 | 비밀번호 | 기본 데이터베이스명 | 표준 클라이언트 생태계 |
|---|---|---|---|---|---|
| **PostgreSQL v3** | `5433` (TCP) | `postgres` (또는 `admin`) | 임의 / 빈값 (개발 모드) | `postgres` | Beekeeper Studio, DBeaver, Navicat, pgAdmin, psql, lib/pq, pgx |
| **Microsoft SQL Server (TDS 7.x/8.0)** | `1433` (TCP) | `sa` (또는 `admin`) | 임의 / 빈값 (개발 모드) | `master` | SSMS, Beekeeper Studio, DBeaver, Navicat, sqlcmd, go-mssqldb, pymssql |
| **HTTP REST & Web Studio** | `8090` (TCP) | `admin@lionsql.local` | `1234567890` | `lionsql` | 내장 Web Studio (`/_/`), curl, OpenAPI, fetch |

> **인스턴스 기반 데이터베이스 격리 (GEMINI.md 불변식 2)**:
> LionSQL 프로세스당 하나의 데이터 디렉터리(`--dir`)에 정확히 하나의 독립된 데이터베이스 카탈로그를 호스팅합니다.

### 1.1. 권장 프로토콜 선택 전략

> [!TIP]
> **기본 애플리케이션 프로토콜로 PostgreSQL Wire (`:5433`) 권장**  
> **Microsoft T-SQL 저장 프로시저**(`EXEC sp_name @p1=$1;`)를 실행하는 경우에도, 애플리케이션 주 통신 프로토콜로 **PostgreSQL Wire (`:5433`)가 강력히 권장**됩니다:
>
> 1. **더 높은 처리량 및 낮은 지연시간**: PGWire는 경량 프레임 구조(1바이트 Opcode + 4바이트 길이)를 채택하며 확장 쿼리 메시지(`Bind` + `Execute` + `Sync`)를 단일 TCP 패킷으로 파이프라이닝합니다. 실측 벤치마크 결과 PGWire는 **352,000 ~ 376,000+ tpmC / 13,000 ~ 13,900+ TPS**를 기록하여 TDS(**228,000 ~ 315,000+ tpmC / 8,460 ~ 11,660+ TPS**) 대비 높은 효율을 입증했습니다.
> 2. **드라이버 성숙도 및 단순한 연결**: PGWire 드라이버(`pgx/v5`, `lib/pq`, `psycopg3`, `node-postgres`, `asyncpg`, `npgsql`)는 전 플랫폼에서 광범위하게 검증되어 있으며, 복잡한 인증서 신뢰 협상(`TrustServerCertificate=true`)이 불필요합니다.
> 3. **완전한 T-SQL 패리티**: LionSQL은 PGWire 연결을 통해서도 T-SQL 문법, 저장 프로시저, 제어 흐름(`IF`, `WHILE`), 배치 스크립트를 완벽하게 실행합니다. 저장 프로시저 실행이 TDS에만 국한되지 않습니다.
> 4. **TDS (`:1433`) 사용 권장 시나리오**: SSMS(SQL Server Management Studio), 기존 레거시 .NET `System.Data.SqlClient`, MSSQL 전용 연결 문자열에 의존하는 워크플로우에 최적입니다.

---

## 2. GUI 클라이언트 설정 가이드

### 2.1. Microsoft SQL Server Management Studio (SSMS)

LionSQL은 SSMS 18.x / 19.x / 20.x 버전과의 완벽한 상호운용성을 지원합니다.

1. **연결 대화상자 설정**:
   - **서버 유형**: 데이터베이스 엔진 (Database Engine)
   - **서버 이름**: `127.0.0.1,1433` (또는 `localhost,1433`)
   - **인증**: SQL Server 인증 (SQL Server Authentication)
   - **로그인**: `sa`
   - **비밀번호**: (임의의 문자열 또는 빈칸)
   - **연결 속성**:
     - 암호화(Encrypt): `선택 사항(Optional)` 또는 `끄기`
     - 서버 인증서 신뢰(Trust server certificate): `체크(True)`
2. **Object Explorer(개체 탐색기) 트리 지원**:
   - 데이터베이스 목록 및 기본 `master` 데이터베이스 탐색.
   - 테이블 목록 및 3-Part 이름(`[master].[dbo].[테이블명]`) 정상 매핑.
   - **하위 폴더 완전 지원**:
     - **Columns (열)**: 데이터 타입, 길이, Null 허용 여부, 기본값 표시.
     - **Keys (키)**: 기본키(PK) 및 외래키(FK) 식별.
     - **Indexes (인덱스)**: 클러스터드/논클러스터드 인덱스 목록.
     - **Triggers (트리거)**: 테이블 바인딩 트리거 객체.
     - **Statistics (통계)**: CBO 옵티마이저용 테이블/컬럼 통계.
3. **우클릭 컨텍스트 메뉴 동작**:
   - **상위 1000개 행 선택 (Select Top 1000 Rows)**: `SELECT TOP (1000) ... FROM [master].[dbo].[테이블명]` 정상 쿼리 및 데이터 그리드 출력.
   - **상위 200개 행 편집 (Edit Top 200 Rows)**: `SET ROWCOUNT 200` 정규화를 통한 상위 행 편집 모드 지원.
   - **스크립트 생성 (Script Table as CREATE to)**: 표준 CREATE TABLE DDL 스크립팅 지원.

---

### 2.2. Beekeeper Studio

Beekeeper Studio는 **PostgreSQL** 또는 **SQL Server** 연결 모드를 모두 지원합니다.

#### 옵션 A: PostgreSQL 연결 모드 (권장)
1. 새 연결 메뉴에서 **Postgres** 선택.
2. 연결 정보:
   - **Host**: `127.0.0.1` (또는 `localhost`)
   - **Port**: `5433`
   - **User**: `postgres`
   - **Password**: 빈칸 또는 임의 입력.
   - **Default Database**: `postgres`
   - **SSL**: `Disable`
3. 지원 기능:
   - 테이블 스키마, 컬럼 타입, 기본키, Null 허용 여부 브라우징.
   - 외래키 관계 ERD 다이어그램 렌더링.
   - 대화형 데이터 수정 및 인플레이스 편집.

#### 옵션 B: SQL Server 연결 모드
1. 새 연결 메뉴에서 **SQL Server** 선택.
2. 연결 정보:
   - **Host**: `127.0.0.1`
   - **Port**: `1433`
   - **User**: `sa`
   - **Database**: `master`
   - **Encrypt**: `Disable` (Trust Server Certificate 체크)

---

### 2.3. DBeaver Universal Database Tool

#### PostgreSQL 드라이버 연결
1. 새 데이터베이스 연결 ➔ **PostgreSQL**.
2. 호스트: `localhost`, 포트: `5433`, 데이터베이스: `postgres`, 계정: `postgres`.
3. 드라이버 속성: `ssl = false`.

#### Microsoft SQL Server 드라이버 연결
1. 새 데이터베이스 연결 ➔ **Microsoft SQL Server** ➔ **MS SQL Server / Azure**.
2. 호스트: `localhost`, 포트: `1433`, 데이터베이스: `master`, 계정: `sa`.
3. 드라이버 속성: `trustServerCertificate = true`, `encrypt = false`.

---

### 2.4. DbGate Universal Database Manager

1. **연결 설정**:
   - **PostgreSQL**: 포트 `5433`, 사용자 `postgres`, 데이터베이스 `postgres`.
   - **SQL Server**: 포트 `1433`, 사용자 `sa`, 데이터베이스 `master`, Encrypt `No`.
2. **동작 검증**:
   - 9단계 스키마 탐색 파이프라인(`modifications` ➔ `tables` ➔ `columns` ➔ `primaryKeys` ➔ `indexes` ➔ `foreignKeys` ➔ `tableSizes`) 100% 정상 통과.
   - `OFFSET ... ROWS FETCH NEXT ... ROWS ONLY` 기반 대용량 데이터 그리드 페이징 지원.

---

## 3. 명령줄 인터페이스 (CLI) 연결

### 3.1. PostgreSQL CLI (`psql`)

```bash
# 대화형 터미널 셸 접속
psql -h 127.0.0.1 -p 5433 -U postgres -d postgres

# 단일 쿼리 실행
psql -h 127.0.0.1 -p 5433 -U postgres -d postgres -c "SELECT * FROM customers;"
```

지원 메타 명령어:
- `\dt`: 사용자 테이블 및 뷰 목록 출력.
- `\d <테이블명>`: 테이블 스키마, 컬럼, 인덱스 제약조건 상세 출력.
- `\l`: 데이터베이스 목록 출력.
- `\timing`: 밀리초 단위 쿼리 실행 시간 표시.

### 3.2. Microsoft SQL Server CLI (`sqlcmd`)

```bash
# 대화형 sqlcmd 세션 접속
sqlcmd -S 127.0.0.1,1433 -U sa -P 12345678 -d master

# 단일 쿼리 일괄 실행
sqlcmd -S 127.0.0.1,1433 -U sa -d master -Q "SELECT @@VERSION; SELECT * FROM customers;"
```

---

## 4. 공식 프로그래밍 언어 드라이버 예제

### 4.1. Go (`pgx/v5` - 최신 PostgreSQL 드라이버)

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/jackc/pgx/v5"
)

func main() {
    ctx := context.Background()
    conn, err := pgx.Connect(ctx, "postgres://postgres:secret@127.0.0.1:5433/postgres?sslmode=disable")
    if err != nil {
        log.Fatalf("연결 실패: %v", err)
    }
    defer conn.Close(ctx)

    var id int64
    var name string
    var balance float64

    err = conn.QueryRow(ctx, "SELECT customer_id, name, balance FROM customers WHERE customer_id = $1", 1).
        Scan(&id, &name, &balance)
    if err != nil {
        log.Fatalf("쿼리 실패: %v", err)
    }

    fmt.Printf("고객 #%d: %s (잔액: $%.2f)\n", id, name, balance)
}
```

### 4.2. Go (`github.com/microsoft/go-mssqldb` - 공식 MSSQL 드라이버)

```go
package main

import (
    "database/sql"
    "fmt"
    "log"

    _ "github.com/microsoft/go-mssqldb"
)

func main() {
    connStr := "sqlserver://sa:secret@127.0.0.1:1433?database=master&encrypt=disable"
    db, err := sql.Open("sqlserver", connStr)
    if err != nil {
        log.Fatalf("연결 실패: %v", err)
    }
    defer db.Close()

    tx, err := db.Begin()
    if err != nil {
        log.Fatalf("트랜잭션 시작 실패: %v", err)
    }

    _, err = tx.Exec("UPDATE customers SET balance = balance - 100 WHERE customer_id = @p1", 1)
    if err != nil {
        tx.Rollback()
        log.Fatalf("실행 실패: %v", err)
    }

    if err := tx.Commit(); err != nil {
        log.Fatalf("커밋 실패: %v", err)
    }
    fmt.Println("트랜잭션이 성공적으로 커밋되었습니다.")
}
```

### 4.3. Python (`psycopg2` & `asyncpg`)

```python
import psycopg2

conn = psycopg2.connect(
    host="127.0.0.1",
    port=5433,
    user="postgres",
    password="",
    dbname="postgres",
    sslmode="disable"
)
cur = conn.cursor()
cur.execute("SELECT customer_id, name FROM customers;")
for row in cur.fetchall():
    print(row)
cur.close()
conn.close()
```

### 4.4. Python (`pymssql`)

```python
import pymssql

conn = pymssql.connect(
    server="127.0.0.1",
    port=1433,
    user="sa",
    password="",
    database="master"
)
cursor = conn.cursor(as_dict=True)
cursor.execute("SELECT customer_id, name, balance FROM customers;")
for row in cursor:
    print(f"Customer: {row['name']}, Balance: {row['balance']}")
conn.close()
```

---

## 5. 아키텍처 메커니즘: 가상 카탈로그 인터셉터

GUI 관리 도구는 좌측 개체 트리, 컬럼 목록, 외래키 관계선 등을 렌더링하기 위해 방대한 양의 메타데이터 카탈로그 쿼리를 전송합니다. 수백 개의 물리 카탈로그 테이블을 유지하는 대신, LionSQL은 **제로 알로케이션 가상 카탈로그 인터셉터(`pkg/catalog/virtual/`)**를 통해 즉시 가상 튜플을 생성합니다.

### 5.1. 선형 1패스 절 분할기 (`sqlutil.SplitSelectClauses`)
최신 LionSQL 1.0 코어는 정규식이나 중복 문자열 분할 없이 단일 $O(N)$ 선형 스캔으로 `SELECT`, `FROM`, `WHERE`, `ORDER BY` 절을 분리하여 서브 마이크로초 지연시간과 제로 힙 할당을 보장합니다.

### 5.2. SSMS SMO 하위 노드 및 XPath URN 프로젝션
SSMS Object Explorer가 요청하는 복합 URN 쿼리(`Server[@Name=...]/Database[@Name=...]/Table[@Name=...]/Column[@Name=...]`)를 해석하여 컬럼, 기본키, 인덱스, 외래키, 트리거 하위 노드를 정밀하게 매핑합니다.

### 5.3. 결정론적 Object ID 불변식
Go 언어의 `map` 순회 무작위성에 의해 테이블 `objectId`가 쿼리마다 변경되는 문제를 원천 차단하기 위해, 모든 카탈로그 테이블 목록은 이름 기준 사전순 정렬(`sort.Slice`)을 강제하여 100% 결정론적 ID 안정성을 보장합니다.

---

## 6. 트러블슈팅 및 빈출 문제 해결

| 증상 및 에러 메시지 | 발생 원인 | 해결 방안 |
|---|---|---|
| `connection refused on :5433 or :1433` | 서버 프로세스가 실행 중이지 않거나 포트가 열려있지 않음. | `./bin/lionsql` 또는 데몬(`lionsql start -dev`) 실행 후 `lsof -i :5433 -i :1433` 확인. |
| `encryption required but server does not support TLS` | 클라이언트 드라이버가 TLS 암호화를 요구했으나 서버가 일반 모드로 구동됨. | 연결 옵션에서 `sslmode=disable`(PG) 또는 `encrypt=disable`(TDS) 지정, 혹은 서버를 `-tls` 옵션으로 구동. |
| `USE <dbname> syntax in client migration scripts` | 단일 인스턴스 단일 카탈로그 격리 정책. | LionSQL은 `USE <dbname>`을 관용적 가상 No-Op으로 처리하므로 스크립트 수정 없이 정상 동작합니다. |
| `table not found: dbo.mytable` | `dbo.` 또는 `public.` 네임스페이스 파싱 누락. | 최신 LionSQL은 식별자 정리 함수에서 스키마 접두사를 자동으로 정규화합니다. |
| `SSMS 하위 노드가 비어있음` | 구버전 캐시 또는 가상 카탈로그 미갱신. | 최신 1.0 빌드에서 SSMS SMO URN 가상 카탈로그가 완비되었습니다. 개체 탐색기에서 새로고침(F5)을 실행하십시오. |
