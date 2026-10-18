# LionSQL

고동시성 임베디드 OLTP 데이터베이스 엔진 및 다중 프로토콜 저장 프로시저 서버

(English documentation is available at [README.md](README.md).)

---

## 1. 개요 (Overview)

LionSQL은 Go 언어와 Cgo(WiredTiger)로 작성된 엔터프라이즈급 고동시성 임베디드 관계형 데이터베이스 엔진입니다. PocketBase의 운영 단순성과 엔터프라이즈 관계형 엔진의 강력한 처리 성능을 결합하여 설계되었으며, 외부 런타임 종속성 없이 **단일 독립 실행형 정적 바이너리(Single Standalone Executable)**로 제공됩니다.

LionSQL은 다중 와이어 프로토콜(PostgreSQL v3 및 Microsoft SQL Server TDS), 내장 웹 관리 대시보드(Web Studio), 네이티브 T-SQL 저장 프로시저 바이트코드 가상 머신(VDBE), 비용 기반 쿼리 최적화기(CBO), 그리고 PgBouncer와 같은 외부 커넥션 풀러를 완전히 대체하는 내장 Goroutine 커넥션 풀러를 하나의 프로세스에 유기적으로 통합했습니다.

```
                    +------------------------------------------+
                    |                 LionSQL                  |
                    |   (단일 독립 실행형 정적 Go 바이너리)    |
                    +------------------------------------------+
                                         │
        ┌────────────────────────────────┼────────────────────────────────┐
        │                                │                                │
        ▼                                ▼                                ▼
[Postgres Wire v3]             [MSSQL TDS 7.x/8.0]             [Web Studio & BaaS API]
  포트 :5433                      포트 :1433                      포트 :8090 /_/
 (psql, DBeaver,                  (sqlcmd, SSMS,                  (브라우저 대시보드,
  GORM, Prisma)                    Azure Data Studio)              REST API, 실시간 SSE,
        │                                │                         웹훅, RLS, 크론 잡,
        │                                │                         메일러, JS/TS SDK)
        └────────────────────────────────┼────────────────────────────────┘
                                         │
                                         ▼
                    +------------------------------------------+
                    |        Goroutine 커넥션 풀러             |
                    |   (PgBouncer 대체, 10,000+ 동시 연결)    |
                    +------------------------------------------+
                                         │
                                         ▼
                    +------------------------------------------+
                    |    이중 파서 & 비용 기반 최적화기(CBO)   |
                    | (Native Pratt + ANTLR4 Fallback + CBO)   |
                    +------------------------------------------+
                                         │
                                         ▼
                    +------------------------------------------+
                    |      통합 네이티브 VDBE 바이트코드 VM    |
                    |  - 대화형 DQL & DML (SELECT, DML)        |
                    |  - T-SQL 프로시저, 트리거, UDF, TVF      |
                    |  - sync.Pool 레지스터 및 앰비언트 트랜잭션|
                    +--------------------+---------------------+
                                         │
                    ┌────────────────────┴────────────────────┐
                    ▼                                         ▼
      [Volcano 관계형 실행 파이프라인]          [인덱스 및 스토리지 직접 연산]
      (Hash Join, work_mem 외부 정렬,           (OpIndexScan SkipList/B+Tree,
       집계 연산, OpRelQuery 반복자)             OpInsert, OpUpdate, OpDelete)
                    │                                         │
                    └────────────────────┬────────────────────┘
                                         │
                                         ▼
                    +------------------------------------------+
                    |          통합 스토리지 엔진              |
                    | (WiredTiger MVCC & 순수 Go 인메모리 OCC) |
                    +------------------------------------------+
```

---

## 2. 핵심 기능 (Key Features)

LionSQL의 핵심 기능은 5대 아키텍처 축으로 체계화되어 있습니다:

### 2.1. 코어 엔진 & 쿼리 가상 머신 (Core Engine & Virtual Machine Architecture)
- **단일 VDBE 가상 머신 아키텍처 통합 (Unified VDBE Virtual Machine)**:
  대화형 독립 쿼리(`SELECT`, `INSERT`, `UPDATE`, `DELETE`)와 저장 프로시저가 정확히 동일한 레지스터 기반 VDBE 가상 머신에서 통합 실행됩니다 (`proc.CompileQuery`, `proc.CompileStatement` ➔ `s.executor.ExecuteInTx`). 관계형 반복자(`OpRelQuery`), 인덱스 스캔(`OpIndexScan`), DML 연산(`OpInsert/Update/Delete`)을 단일 가상 머신으로 일원화하고 `EXPLAIN BYTECODE <sql>` 디스어셈블러를 지원합니다.
- **프리페어드 스테이트먼트 VDBE 바이트코드 캐싱**:
  PGWire 및 TDS 리스너 모두 파라미터화된 쿼리(`$1`, `$2` 및 `@p1`, `@p2`)를 준비(Prepare) 단계에서 VDBE `*proc.Program` 바이트코드로 직접 컴파일하여 쿼리 재파싱 및 문자열 조작 오버헤드를 100% 바이패스합니다.
- **이중 파서 아키텍처 및 자동 폴백 (Dual-Parser Strategy)**:
  - **1차 고속 경로**: 고주파 쿼리를 위한 무할당(Zero-alloc) Native Pratt 파서 (마이크로초 미만 파싱 지연).
  - **2차 전체 스펙 폴백**: 대괄호 이스케이프(`[dbo].[Table]`), `WITH RECOMPILE` 등 벤더 고유 고급 구문 인입 시 공식 Microsoft T-SQL ANTLR4 파서가 자동 호출되어 100% 호환성을 보장합니다.
- **Volcano 관계형 파이프라인 & 메모리 거버너**:
  조합 가능한 반복자 파이프라인(`SeqScan`, `IndexScan`, `Filter`, `Project`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`, `WindowIterator`, `CTE WITH`)과 1,024건 단위 청크 스트리밍을 제공하며, `work_mem` 초과 시 N-way Min-Heap 외부 머지 소트(External Merge Sort)로 디스크 자동 스필을 수행합니다.
- **비용 기반 최적화기 (Cost-Based Optimizer, CBO)**:
  `ANALYZE TABLE <name>` 통계 수집(카디널리티, NDV, 64-버킷 균등 깊이 히스토그램), 다중 컬럼 확장 통계(`C-5`), MCV 최빈값 목록(`C-7`), 그리고 5개 이상 다중 테이블 조인을 서브밀리초로 최적화하는 Selinger DP 조인 순서 최적화기(`B-4`)를 내장했습니다.

### 2.2. 초고성능 스토리지 & ACID 트랜잭션 (Storage Engines & ACID Concurrency)
- **초고성능 이중 스토리지 백엔드 & TPC-C 벤치마크**:
  - **In-Memory OCC 엔진**: 세밀한 테이블 단위 스트라이프 락, 순수 Go 동시성 SkipList 보조 인덱스, 무할당 트랜잭션/청크 풀링을 통해 Go Native API 기준 **28,000+ TPS** (763,000+ tpmC), 컴파일된 T-SQL VDBE 바이트코드 VM 기준 **20,000+ TPS** (541,000+ tpmC)를 달성.
  - **WiredTiger (기본 스토리지 엔진)**: 해저드 포인터 기반 Cgo 락프리 B+Tree, 스키마 인식 컴팩트 바이너리 튜플 코덱, 커서 풀링(P-8)을 통해 컴파일된 T-SQL VDBE 환경에서 완전한 ACID 내구성을 유지하며 **16,800+ TPS** (457,000+ tpmC) 달성.
- **WiredTiger 기본 엔진 채택 배경 및 차별점 (Zero-Vacuum & Lock-Free MVCC)**:
  - **Zero-Vacuum & 팽창 방지 MVCC (No Table Bloat)**: 다중 버전(MVCC) 업데이트 체인(`WT_UPDATE`)이 메모리(RAM)에서만 유지되며, 체크포인트 및 캐시 방출(Reconciliation) 시점에 최신 커밋 버전만 디스크에 기록합니다. PostgreSQL과 같은 별도의 `VACUUM` 프로세스, Autovacuum I/O 폭풍, 테이블 팽창(Table Bloat)이 원천적으로 발생하지 않습니다. (상세 원리는 [doc/SNAPSHOT_ISOLATION_AND_DUCKDB.md](doc/SNAPSHOT_ISOLATION_AND_DUCKDB.md) 및 [doc/INTERNALS_STORAGE.md](doc/INTERNALS_STORAGE.md) 참조).
  - **예측 가능한 초저지연 (Predictable Low-Latency)**: RocksDB/Pebble과 같은 LSM-Tree 엔진에서 발생하는 대규모 백그라운드 Compaction 정체 현상 없이, Hazard Pointer 기반 락프리 B+Tree로 일관된 서브 밀리초 응답성을 보장합니다.
  - **비차단 스냅샷 격리 (Non-Blocking Snapshot Isolation)**: 읽기가 쓰기를 결코 차단하지 않고, 쓰기 역시 읽기를 차단하지 않습니다. 장시간 소요되는 백업, 대용량 분석(DuckDB 연동, 덤프) 중에도 초당 16,000건 이상의 OLTP 트랜잭션 쓰기가 무중단 처리됩니다.
  - **듀얼 백엔드 시너지**: 프로덕션에서는 WiredTiger의 견고한 NVMe ACID 내구성을 활용하고, 빠른 CI/CD 테스트 및 경량 로컬 환경에서는 Cgo 의존성 없는 Pure-Go In-Memory OCC 엔진(`-engine=memory`)을 자유롭게 전환하여 사용할 수 있습니다.
- **비차단 온라인 스냅샷 핫 백업 (Hot Backup)**:
  활성 OLTP 트래픽을 차단하거나 쓰기 락을 걸지 않고, 읽기 전용 스냅샷 트랜잭션을 통해 실시간 `.tar.gz` 백업 아카이브 생성. CLI(`-backup`, `-restore`), REST API(`/api/backup`, `/api/restore`), Web Studio UI 전반에서 원클릭으로 지원됩니다.
- **1-Writer 다중 리더 고가용성 복제 (HA Replication)**:
  단일 Primary 노드에서 복제본으로 선형 WAL 스트리밍(`GET /api/replication/stream`)을 통한 클러스터 확장, 엄격한 읽기 전용 모드 강제, 무중단 승격 API(`POST /api/replication/promote`) 및 Always On 스타일 자동 장애 조치(Auto-Failover) 지원.

### 2.3. 다중 와이어 프로토콜 & 연결 관리 (Wire Protocols & Connection Pooling)
- **동시 다중 와이어 프로토콜 (Dual Wire Protocols)**:
  - **PostgreSQL v3 프로토콜 (포트 5433)**: `psql`, PostgreSQL JDBC (`pgjdbc`), `psqlODBC`, DBeaver, GORM, Prisma, asyncpg, pgx와 완벽 호환. SSL/TLS 암호화 연결, 바이너리 바인드 파라미터 직접 디코딩(P-14), Extended Query Protocol(`Parse`, `Bind`, `Execute`, `Sync`)을 지원합니다.
  - **Microsoft SQL Server TDS 7.x/8.0 프로토콜 (포트 1433)**: `sqlcmd`, SSMS, Azure Data Studio, `pymssql`, `go-mssqldb`와 완벽 호환. RPC 프리페어드 스테이트먼트 파이프라인(`sp_prepare`, `sp_execute`, `sp_prepexec`), TDS 7.3 특수 타입 바이너리 직렬화, 단일 프레임 버퍼 풀링(시스템 콜 50% 절감)을 지원합니다.
- **내장 Goroutine 커넥션 풀러**:
  가벼운 Go 고루틴 기반으로 10,000개 이상의 동시 클라이언트 연결을 효율적으로 처리하며, 내부 세션 재사용 메커니즘을 통해 PgBouncer 등의 외부 풀러가 불필요합니다.

### 2.4. 엔터프라이즈 T-SQL 절차적 런타임 (Enterprise T-SQL Procedural Runtime)
- **엔터프라이즈 T-SQL 저장 프로시저 및 UDF**:
  `CREATE PROCEDURE`, `CREATE FUNCTION` (스칼라 및 테이블 반환 함수 TVF), `CREATE TRIGGER` (`AFTER`, `INSTEAD OF`)를 완벽 지원합니다.
- **가상 델타 테이블 & 제어 흐름문**:
  트리거 실행을 위한 가상 델타 테이블(`INSERTED`, `DELETED`), 제어 흐름문(`IF...ELSE`, `WHILE`, `TRY...CATCH`, `GOTO` 및 라벨, `PRINT`, `RAISERROR`), 지역 변수(`@var`), 테이블 변수(`DECLARE @table TABLE`), 세션 변수, 커서(Cursor) 지원.
- **풍부한 SQL 확장 기능**:
  윈도우 함수(`ROW_NUMBER`, `RANK`, `LEAD`, `LAG`), JSON/JSONB 추출 연산자(`->`, `->>`, `JSON_VALUE`), 외래키 및 CASCADE 연쇄 동작, `TRUNCATE TABLE`, `SELECT ... INTO`, `STRING_SPLIT` TVF, `DBCC` 진단 명령.

### 2.5. 풀스택 BaaS & 모던 웹 개발자 환경 (Backend-as-a-Service & Modern Web Stack)
- **단일 독립 실행형 정적 바이너리 (Single Standalone Executable)**:
  데이터베이스 커널, 다중 와이어 프로토콜 서버, HTTP REST API, 내장 웹 관리 스튜디오(Go `embed.FS`)가 단일 정적 실행 파일로 빌드되어 외부 의존성(JVM, Node.js 등)이 전혀 없습니다.
- **자동 생성 RESTful Records CRUD API**:
  테이블 생성 즉시 무코드 RESTful 엔드포인트(`/api/records/:table`) 자동 활성화, 정교한 URL 불리언 필터 엔진(`filter`), 다중 컬럼 정렬(`sort`), 페이지네이션, JSON 직렬화 지원.
- **내장 웹 관리 스튜디오 (Web Studio, 포트 8090)**:
  브라우저 관리자 대시보드 (`/_/`), 스프레드시트 인라인 셀 편집기, SQL 쿼리 에디터, VDBE 바이트코드 디스어셈블러, 실시간 Server-Sent Events (SSE) 동기화, 슈퍼유저 및 API Key 관리.
- **공식 범용 JavaScript & TypeScript 클라이언트 SDK**:
  서버 바이너리가 직접 호스팅하는 PocketBase 스타일 SDK (`GET /sdk/sqlion.js`, `sqlion.d.ts`), 웹 브라우저, Node.js, Bun, Deno, Edge 런타임 지원.
- **선언적 행 수준 보안 (RLS) & 비동기 웹훅**:
  동적 `@auth` 컨텍스트 치환 기반 테이블 접근 제어, HMAC-SHA256 무결성 서명 및 지수 백오프 재시도를 지원하는 비차단 비동기 웹훅 디스패처.
- **내장 크론 잡 스케줄러 & SMTP 메일러**:
  표준 5필드 크론 자동화 스케줄러, TLS/STARTTLS 지원 순수 Go SMTP 메일러 및 트랜잭셔널 HTML 이메일 템플릿 내장.

---

## 3. 빠른 시작 가이드 (Quick Start Guide)

### 3.1. 소스코드 빌드

#### 사전 요구 사항
- Go 1.22 이상
- (WiredTiger 백엔드 사용 시 선택 사항) `wiredtiger` 개발 헤더 및 라이브러리:
  - macOS: `brew install wiredtiger`
  - Linux (Ubuntu/Debian): `sudo apt-get install libwiredtiger-dev`

#### 빌드 커맨드

```bash
# WiredTiger 스토리지를 지원하는 프로덕션 서버 바이너리 빌드
go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server

# 순수 Go 인메모리 엔진 전용 바이너리 빌드 (Cgo 의존성 제로, 어디서나 실행 가능)
go build -buildvcs=false -o bin/server ./cmd/server

# 선언적 스키마 마이그레이션 CLI 도구 빌드
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
```

### 3.2. 서버 기동

```bash
# 기본 설정(포트 5433, 1433, 8090) 및 WiredTiger 스토리지로 기동 ('data' 디렉터리 자동 생성)
./bin/server

# 또는 순수 Go 인메모리 엔진으로 기동 (Cgo 라이브러리 불필요)
./bin/server -engine=memory
```

서버 기동 시 콘솔에 접속 엔드포인트 정보가 출력됩니다:

```text
------------------------------------------------------------
 LionSQL - High-Concurrency OLTP & Stored Procedure Server
------------------------------------------------------------
 [Web Dashboard]  http://127.0.0.1:8090/_/
 [HTTP API]       http://127.0.0.1:8090/api/health
 [Superuser Auth] admin@sqlion.local (JWT Auth: false)
 [Postgres Wire]  psql -h 127.0.0.1 -p 5433 (Beekeeper / DBeaver / DbGate / psql)
 [TDS / MSSQL]    sqlcmd -S 127.0.0.1,1433 (Beekeeper / DBeaver / DbGate / Azure Data Studio / pymssql)
 [Data Directory] data
 [Storage Engine] wiredtiger
 [Session Pooler] Max Workers: 32, Max Idle Sessions: 64 (No PgBouncer required)
 [Memory Governor] work_mem: 64MB (External Merge Sort spill-to-disk enabled)
------------------------------------------------------------
```

### 3.3. 웹 스튜디오 접속

웹 브라우저를 열고 다음 주소로 이동합니다:
```text
http://127.0.0.1:8090/_/
```
웹 스튜디오를 통해 테이블 스키마 시각적 조회, 실시간 쿼리 실행, 저장 프로시저 디스어셈블리 검증, 원클릭 데이터베이스 백업/복원을 즉각 수행할 수 있습니다.

---

## 4. 클라이언트 도구 접속 방법 (Client Connectivity)

### `psql` (PostgreSQL 공식 클라이언트) 접속

```bash
psql -h 127.0.0.1 -p 5433 -U admin
```

```sql
-- 계좌 데이터 조회
SELECT * FROM accounts;

-- 실행 계획 및 비용 추정치 검증
EXPLAIN ANALYZE SELECT * FROM accounts WHERE balance > 600 ORDER BY balance DESC;

-- 세션 쿼리 메모리 예산 설정
SET work_mem = '128MB';
SHOW work_mem;
```

### `sqlcmd` (Microsoft SQL Server 클라이언트) 접속

```bash
sqlcmd -S 127.0.0.1,1433 -U admin
```

```sql
-- T-SQL 저장 프로시저 호출
EXEC TransferFunds @from = 1, @to = 2, @amount = 100;
GO
```

### `psql \copy`를 통한 대용량 스트리밍 복사

```bash
-- CSV 파일로 초고속 데이터 내보내기 (Export)
psql -h 127.0.0.1 -p 5433 -c "\copy accounts to 'accounts.csv' with (format csv, header)"

-- CSV 파일에서 초고속 데이터 가져오기 (Import)
psql -h 127.0.0.1 -p 5433 -c "\copy accounts from 'accounts.csv' with (format csv, header)"
```

---

## 5. 프로그래밍 언어 및 ORM 연동 가이드

### Go (`pgx` / `database/sql`)

```go
package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	dsn := "postgres://admin:1234567890@127.0.0.1:5433/lionsql?sslmode=disable"
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, holder, balance FROM accounts")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var holder string
		var balance int
		if err := rows.Scan(&id, &holder, &balance); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Account: %d | %s | %d\n", id, holder, balance)
	}
}
```

### JavaScript / TypeScript (공식 범용 SDK)

```javascript
import LionSQL from './sdk/sqlion.js';

const client = new LionSQL('http://127.0.0.1:8090', { auth: 'YOUR_API_KEY' });

// 1. 선언적 필터링 및 정렬을 통한 레코드 조회
const res = await client.collection('accounts').getList(1, 20, {
  filter: 'balance > 500',
  sort: '-balance'
});
console.log(res.items);

// 2. 실시간 SSE 데이터 변경 스트림 구독
const unsubscribe = client.collection('accounts').subscribe((event) => {
  console.log('Account mutated:', event.action, event.record);
});

// 3. T-SQL 저장 프로시저 호출
await client.exec('TransferFunds', { from: 1, to: 2, amount: 50 });
```

### Python (`psycopg2` / `SQLAlchemy`)

```python
import psycopg2

conn = psycopg2.connect(
    host="127.0.0.1",
    port=5433,
    user="admin",
    password="",
    dbname="lionsql"
)

with conn.cursor() as cur:
    cur.execute("SELECT holder, balance FROM accounts WHERE balance > %s", (500,))
    for row in cur.fetchall():
        print(f"Holder: {row[0]}, Balance: {row[1]}")

conn.close()
```

### Node.js (`pg` / `Prisma`)

```javascript
const { Client } = require('pg');

const client = new Client({
  host: '127.0.0.1',
  port: 5433,
  user: 'admin',
  database: 'lionsql',
});

async function run() {
  await client.connect();
  const res = await client.query('SELECT * FROM accounts');
  console.log(res.rows);
  await client.end();
}

run();
```

---

## 6. CLI 및 운영 레퍼런스 (CLI Reference)

### 서버 구동 옵션 플래그

| 플래그 | 기본값 | 설명 |
|---|---|---|
| `-http` | `127.0.0.1:8090` | HTTP 서비스 및 내장 Web Studio 바인딩 주소 |
| `-pg` | `127.0.0.1:5433` | PostgreSQL 와이어 프로토콜 리스닝 주소 |
| `-tds` | `127.0.0.1:1433` | Microsoft SQL Server TDS 프로토콜 리스닝 주소 |
| `-dir` | `data` | 데이터베이스 파일 저장 디렉터리 경로 |
| `-engine` | `wiredtiger` | 스토리지 엔진 백엔드 선택 (`wiredtiger` 또는 `memory`) |
| `-ephemeral` | `false` | 인메모리 엔진의 디스크 WAL/AOF 영속성 비활성화 (순수 RAM 모드) |
| `-replica-of` | `""` | WAL을 스트리밍받을 Primary 노드 URL (읽기 전용 복제본 모드) |
| `-node-id` | `"node-1"` | 복제 추적을 위한 고유 클러스터 노드 식별자 |
| `-work-mem` | `64MB` | 연산자 단위 디스크 스필 기준 메모리 상한선 |
| `-max-workers` | `NumCPU * 4` | 최대 동시 쿼리 실행 워커 스레드 수 |
| `-max-idle-sessions` | `64` | 풀에 유지할 최대 유휴 스토리지 세션 수 |
| `-auth` | `false` | 보호된 REST 엔드포인트에 대한 JWT 인증 강제 여부 |
| `-seed` | `true` | 기동 시 데모 계좌 데이터 및 `TransferFunds` 프로시저 시드 생성 |

### 백그라운드 데몬 제어

별도의 외부 데몬 래퍼 없이 단일 명령어로 백그라운드 구동이 가능합니다:

```bash
# 백그라운드 데몬으로 기동
./bin/server -d

# 데몬 실행 상태 및 PID 확인
./bin/server -status

# 실행 중인 데몬 무중단 재기동
./bin/server -restart

# 데몬 정상 종료 (Graceful Shutdown)
./bin/server -stop
```

### 스키마 마이그레이션 CLI (`lionsql migrate`)

```bash
# 새로운 타임스탬프 마이그레이션 파일 쌍 생성 (.up.sql / .down.sql)
./bin/lionsql migrate new create_users_table

# 대기 중인 모든 마이그레이션 원자적 적용
./bin/lionsql migrate up -data=./data

# 가장 최근 마이그레이션 1단계 롤백
./bin/lionsql migrate down -steps=1 -data=./data

# 마이그레이션 배포 상태 테이블 조회
./bin/lionsql migrate status -data=./data
```

### 백업 및 복원 작업

```bash
# 지정된 아카이브 파일로 오프라인/CLI 스냅샷 핫 백업 수행
./bin/server -dir=data -backup=/backups/lionsql-snapshot.tar.gz

# 아카이브로부터 데이터베이스 복원
./bin/server -dir=data -restore=/backups/lionsql-snapshot.tar.gz

# 서버 기동 중 curl 명령어를 통한 온라인 라이브 백업 스트림 다운로드
curl -OJ http://127.0.0.1:8090/api/backup

# HTTP POST multipart 업로드를 통한 온라인 라이브 복원
curl -X POST -F "backup=@/backups/lionsql-snapshot.tar.gz" http://127.0.0.1:8090/api/restore
```

---

## 7. HTTP REST API 엔드포인트 요약

LionSQL은 `:8090` 포트를 통해 완전한 RESTful API를 제공합니다:

| HTTP 메서드 | 엔드포인트 | 설명 |
|---|---|---|
| `GET` | `/api/health` | 서비스 헬스체크 및 버전 확인 |
| `GET` | `/api/pool/stats` | 실시간 워커 풀 및 커넥션 풀러 지표 조회 |
| `GET` | `/api/realtime` | Server-Sent Events (SSE) 실시간 데이터 변경 스트림 |
| `POST` | `/api/sql` | SQL/T-SQL 구문 직접 실행 (`{"sql": "..."}`) |
| `GET` | `/api/tables` | 전체 테이블 및 스키마 목록 조회 |
| `POST` | `/api/tables` | 신규 테이블 생성 |
| `GET` | `/api/tables/{name}/records` | 페이지네이션, 정렬, 검색 조건을 통한 레코드 목록 조회 |
| `POST` | `/api/tables/{name}/records` | 신규 레코드 삽입 |
| `PATCH` | `/api/tables/{name}/records/{pk}` | 기본키 기반 레코드 수정 |
| `DELETE` | `/api/tables/{name}/records/{pk}` | 기본키 기반 레코드 삭제 |
| `GET` | `/api/procs` | 등록된 전체 저장 프로시저 목록 조회 |
| `POST` | `/api/procs` | 신규 저장 프로시저 등록 |
| `POST` | `/api/procs/{name}/exec` | JSON 파라미터를 통한 저장 프로시저 실행 |
| `GET` | `/api/backup` | 실시간 `.tar.gz` 스냅샷 핫 백업 다운로드 스트리밍 |
| `POST` | `/api/backup` | 서버 로컬 경로에 백업 파일 생성 (`{"target": "path"}`) |
| `POST` | `/api/restore` | 아카이브 파일 경로 또는 업로드를 통한 복원 |
| `GET` | `/api/replication/status` | 노드 복제 역할, LSN, 지연 시간, 복제본 목록 조회 |
| `GET` | `/api/replication/stream` | 연결된 복제본으로 청크 분할된 WAL 프레임 스트리밍 |
| `POST` | `/api/replication/ack` | 복제본 하트비트 보고 및 재생 완료 LSN 동기화 |
| `POST` | `/api/replication/promote` | 복제본을 활성 Primary 노드로 승격 |
| `POST` | `/api/replication/demote` | Primary 노드를 신규 Primary 하위의 복제본으로 강등 |

---

## 8. 기술 문서 전체 모음 (Documentation Suite)

LionSQL은 아키텍처 청사진, 내부 엔진 구현 명세서, 사용자 가이드를 포괄하는 방대한 기술 문서 스위트를 제공합니다:

### [기술 문서 전체 색인 디렉터리 (doc/README.md)](doc/README.md)
LionSQL의 모든 내부 문서, 아키텍처 결정 기록(ADR), 프로토콜 명세서에 대한 주제별 통합 색인입니다.

### 아키텍처 및 핵심 엔진 명세서
- **[초기 설계 결정 및 아키텍처 철학](doc/FOUNDATIONAL_DESIGN_DECISIONS.md)**: WiredTiger MVCC, Go 언어 선택, T-SQL VDBE 가상 머신, PGWire 호환성, Antigravity AI 협업 방법론의 기술적 배경.
- **[Zero-Allocation 메모리 아키텍처](doc/ZERO_ALLOCATION_ARCHITECTURE.md)**: 20k TPS를 달성한 무할당 버퍼 풀링, TDS 프레임 풀링, CGO 커서 재사용, 가상 카탈로그 인터셉터 최적화.
- **[시스템 아키텍처 종합 명세서](doc/ARCHITECTURE.md)**: Volcano 실행기, 이중 파서 디커플링, 이중 스토리지 백엔드 종합 설계.
- **[아키텍처 결정 기록 (ADR)](doc/ADR.md)**: 프로젝트의 22대 핵심 아키텍처 결정 기록 (ADR-001 ~ ADR-022).
- **[VDBE 바이트코드 가상 머신 내부 구조](doc/INTERNALS_VDBE.md)**: 레지스터 할당, ISA 명령어 셋, 절차적 런타임 실행 메커니즘.
- **[스토리지 엔진 내부 구조 (WiredTiger & Memory OCC)](doc/INTERNALS_STORAGE.md)**: 락프리 B+Tree, 해저드 포인터, OCC 검증, AOF 로그 압축.
- **[비용 기반 최적화기 (CBO) & Volcano 파이프라인](doc/INTERNALS_CBO_VOLCANO.md)**: 균등 깊이 히스토그램, 비용 모델, 외부 머지 소트.
- **[다중 와이어 프로토콜 내부 구조](doc/INTERNALS_WIRE_PROTOCOLS.md)**: PostgreSQL v3, TDS 7.x/8.0 바이너리 프레이밍 및 고루틴 커넥션 풀러.
- **[가상 시스템 카탈로그 및 데이터 딕셔너리](doc/DATA_DICTIONARY.md)**: 시스템 뷰(`sys.*`, `information_schema.*`), 데이터 타입, 클라이언트 도구 인터셉터.
- **[고가용성 및 재해 복구 아키텍처](doc/HIGH_AVAILABILITY.md)**: 1-Writer 다중 리더 WAL 스트리밍, 장애 조치(Failover) 및 복구 SLA.

### BaaS 및 개발자 API
- **[공식 Web SDK 및 HTML 임베딩 가이드](doc/WEB_SDK.md)**: 웹 브라우저, Node.js, Bun, Deno 환경을 위한 무의존성 범용 SDK 가이드.
- **[자동 생성 RESTful CRUD API](doc/AUTO_REST_API.md)**: 풍부한 불리언 필터 표현식을 지원하는 `/api/records/:table` 명세.
- **[웹훅 및 행 수준 보안 (RLS)](doc/WEBHOOKS_AND_RLS.md)**: 비차단 웹훅 디스패처, HMAC 서명, 선언적 `@auth` 보안 규칙.
- **[크론 잡 스케줄러 및 SMTP 메일러](doc/CRON_AND_MAILER.md)**: 5필드 크론 자동화 및 HTML 이메일 발송 엔진.
- **[HTTP REST API 및 실시간 SSE 명세서](doc/API_REFERENCE.md)**: OpenAPI 스타일의 전체 엔드포인트 명세 및 SSE 예제.

### 운영 및 사용자 가이드
- **[공식 사용자 매뉴얼](doc/USER_MANUAL.ko.md)**: 설치, 기동, 클라이언트 연동, 개발 및 운영 전 과정을 안내하는 공식 사용자 매뉴얼.
- **[공식 레퍼런스 매뉴얼](doc/REFERENCE_MANUAL.ko.md)**: CLI 플래그, 설정 파일, SQL 문법, 함수, API 전체를 총망라한 레퍼런스 매뉴얼.
- **[선언적 스키마 마이그레이션 CLI 가이드](doc/SCHEMA_MIGRATIONS_CLI.md)**: `lionsql migrate` 명령어, 버전 관리 규약, CI/CD 및 쿠버네티스 패턴.
- **[엔터프라이즈 인증 SPI 및 디렉터리 연동](doc/ENTERPRISE_AUTHENTICATION_SPI.md)**: `AuthProvider` SPI, LDAP/Active Directory 연동, 경계 보안 아키텍처.
- **[불변 감사 로그 아키텍처 및 구현 계획](notes/IMMUTABLE_AUDIT_LOG_PLAN.md)**: PCI-DSS v4.0, SOC 2 Type II 대응 SHA-256 해시 체이닝 감사 로그.
- **[T-SQL 및 SQL 구문 레퍼런스 가이드](doc/TSQL_REFERENCE.md)**: 프로시저, 함수, 트리거, 제어 흐름문 전체 언어 레퍼런스.
- **[클라이언트 도구 연동 가이드](doc/CLIENT_TOOL_INTEGRATION.md)**: DBeaver, SSMS, Azure Data Studio, Beekeeper, JDBC, ODBC 연동 가이드.
- **[성능 튜닝 및 벤치마크 가이드](doc/BENCHMARK.md)**: 고동시성 뱅킹 벤치마크, TPS 측정치, 메모리 튜닝 가이드.
- **[데이터베이스 마이그레이션 가이드](doc/MIGRATION.md)**: PostgreSQL, Microsoft SQL Server, SQLite, MySQL로부터의 데이터 이전 절차.
- **[보안 정책 및 위협 모델](doc/SECURITY.md)**: RBAC 접근 제어, SQL 인젝션 방어, 취약점 대응 정책.
- **[프로덕션 운영 및 배포 런북](doc/OPERATIONS.md)**: Systemd 서비스 등록, Docker 배포, 고가용성 복제 운용.
- **[모니터링 및 메트릭 가이드](doc/METRICS.md)**: Prometheus 시계열 메트릭, 슬로우 쿼리 추적, Grafana 대시보드 연동.
- **[문제 해결 및 에러 진단 FAQ](doc/TROUBLESHOOTING.md)**: 포트 충돌, 직렬화 충돌, 성능 지연 튜닝.
- **[엔지니어링 로드맵 (ROADMAP.md)](ROADMAP.md)**: 단일 노드 OLTP부터 Always On HA 및 HTAP 분리 분석까지의 마일스톤.
- **[실행 중인 엔지니어링 백로그 (TODO.ko.md)](TODO.ko.md)**: 일 단위 활성 개발 과제 및 우선순위 트래커.
- **[변경 이력 (CHANGELOG.md)](CHANGELOG.md)**: Keep a Changelog 표준을 준수하는 버전별 릴리스 노트.

---

## 9. 엔지니어링 품질 및 3단계 검증 파이프라인

LionSQL은 모든 코드 변경, 리팩토링, 기능 추가 시 엄격한 **3단계 검증 파이프라인 (Build ➔ Test ➔ Commit)**을 강제합니다:

1. **컴파일 무결성 검증 (Build)**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
   go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
   ```
2. **결정론적 데이터 레이스 무결성 검증 (Test)**:
   ```bash
   go test -tags wiredtiger -count=1 -v -race ./...
   ```
   모든 패키지에서 100% PASS를 달성해야 하며, **데이터 레이스(Data Race)가 0건**이어야 합니다.
3. **커밋 무결성 규약 (Commit)**:
   - Conventional Commits 형식 준수 (`feat`, `fix`, `docs`, `refactor`, `test`, `perf`, `chore`).
   - 모든 코드, 로그, 주석, 문서, 커밋 메시지에서 장식용 이모지를 엄격히 배제하고 엔지니어링 톤을 유지합니다.

---

## 10. 라이선스 및 저작권 (License & Third-Party Notices)

### 오픈 소스 라이선스
LionSQL은 **GNU General Public License v3 (GPL-3.0-or-later)** 하에 배포되는 자유 오픈 소스 소프트웨어입니다. 전문은 [LICENSE](LICENSE) 파일을 참조하십시오.

### 서드파티 소프트웨어 저작권 고지 (Third-Party Software & Copyright Attribution)
LionSQL은 다음 오픈 소스 소프트웨어를 통합하거나 동적 링크하여 사용하며, 해당 라이선스를 철저히 준수합니다:
- **WiredTiger Storage Engine**:
  - Copyright (c) MongoDB, Inc.
  - Copyright (c) Keith Bostic and Michael Cahill.
  - 라이선스: **GNU General Public License (GPLv2 또는 GPLv3)**.
  - `-tags wiredtiger` 플래그로 빌드된 LionSQL 바이너리는 `libwiredtiger`에 링크되며 GPL 카피레프트 조건이 적용됩니다.

### 상용 듀얼 라이선스 및 기업 임베딩 (Commercial Dual Licensing)
GPL 카피레프트 의무 없이 자체 소프트웨어에 LionSQL을 내장하거나 독점 라이선스가 필요한 기업 고객을 위한 안내:
- LionSQL의 독자적 코어 엔진 코드에 대한 상용 듀얼 라이선스 계약을 지원합니다.
- Cgo 의존성이 없는 순수 Go 인메모리 엔진(`-engine=memory`)은 외부 C 라이브러리 카피레프트 제약 없이 상용 임베딩이 가능합니다. WiredTiger 백엔드를 포함한 상용 임베딩의 경우 MongoDB, Inc.의 상용 WiredTiger 라이선스를 취득하거나 GPL 조항을 준수해야 합니다.
