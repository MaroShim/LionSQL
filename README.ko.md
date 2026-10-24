# LionSQL

단일 바이너리 고동시성 OLTP 데이터베이스 서버 (T-SQL 및 저장 프로시저 지원) / Single-binary, high-concurrency OLTP database server supporting T-SQL and stored procedures

[![Version](https://img.shields.io/badge/version-v0.90.0--beta3-blue.svg)](CHANGELOG.md)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey.svg)](README.ko.md)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-green.svg)](LICENSE)
[![Go Report](https://img.shields.io/badge/go-1.22+-00ADD8.svg)](https://go.dev)

(English documentation is available at [README.md](README.md).)

---

## 1. 개요 (Overview)

LionSQL은 Microsoft SQL Server의 T-SQL & 저장 프로시저(Stored Procedure)와 복잡한 비즈니스 로직을 네이티브 VDBE 가상 머신에서 마이크로초 단위로 고속 실행하며, 동시에 광범위한 PostgreSQL SQL 방언 및 와이어 프로토콜(PGWire v3) 호환성을 제공하는 고성능 임베디드 OLTP 데이터베이스 엔진입니다. 시스템 모니터링과 데이터 조작을 위한 실시간 웹 관리 어드민(Web Studio)과 수만 개 동시 연결을 감당하는 커넥션 풀러까지 내장하고 있어, 추가 소프트웨어 없이도 완전한 데이터베이스 운영 환경을 갖출 수 있습니다. 이 모든 엔터프라이즈 기능을 100MB 미만(약 50MB)의 극히 가벼운 단일 독립 정적 바이너리 하나에 집약하여, 초당 수만 건의 빠른 트랜잭션을 비차단 스냅샷 격리와 예측 가능한 초저지연으로 처리합니다.


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

### 2.1. 다중 와이어 프로토콜 & 연결 관리
- **동시 다중 와이어 프로토콜**:
  - **PostgreSQL v3 프로토콜 (포트 5433)**: `psql`, DBeaver, GORM, Prisma, pgx 등 표준 PostgreSQL 도구 및 ORM과 완벽 호환되며, SSL/TLS 보안 연결과 파라미터 바인딩을 지원합니다.
  - **Microsoft SQL Server TDS 프로토콜 (포트 1433)**: `sqlcmd`, SSMS, Azure Data Studio 등 기존 MSSQL 생태계 도구와 즉시 연동되며, RPC 프리페어드 파이프라인을 지원합니다.
- **내장 Goroutine 커넥션 풀러**:
  경량 Go 고루틴 기반으로 10,000개 이상의 동시 클라이언트 연결을 효율적으로 처리하여 외부 커넥션 풀러 없이도 연결 병목을 해소합니다.

### 2.2. 엔터프라이즈 T-SQL 절차적 런타임
- **저장 프로시저, 함수 및 트리거**:
  `CREATE PROCEDURE`, 스칼라 및 테이블 반환 함수(TVF), `AFTER` 및 `INSTEAD OF` 트리거를 지원합니다.
- **제어 흐름문 & 변수**:
  조건문(`IF...ELSE`), 반복문(`WHILE`), 예외 처리(`TRY...CATCH`), 로컬 변수(`@var`), 테이블 변수, 세션 변수, 커서(Cursor)를 폭넓게 지원합니다.
- **풍부한 SQL 확장 기능**:
  윈도우 함수(`ROW_NUMBER`, `RANK` 등), JSON 추출 함수, 외래키 및 CASCADE 연쇄 동작, `TRUNCATE TABLE`, `SELECT ... INTO`를 완벽 지원합니다.

### 2.3. 코어 엔진 & 쿼리 가상 머신
- **통합 VDBE 가상 머신 아키텍처**:
  단순 쿼리(`SELECT`, `INSERT`, `UPDATE`, `DELETE`)와 복잡한 저장 프로시저를 동일한 레지스터 기반 VDBE 가상 머신에서 통합 실행합니다. 쿼리 실행 계획 디스어셈블러(`EXPLAIN BYTECODE`)를 지원합니다.
- **프리페어드 스테이트먼트 바이트코드 캐싱**:
  파라미터화된 쿼리를 준비 단계에서 바이트코드로 직접 컴파일 및 캐싱하여, 반복 실행 시 쿼리 재파싱 오버헤드를 완전히 제거합니다.
- **이중 파서 아키텍처 및 자동 폴백**:
  - **초고속 경로**: 고주파 쿼리를 위한 마이크로초 미만 지연의 자체 Pratt 파서
  - **전체 스펙 폴백**: 대괄호 이스케이프, 벤더 확장 옵션 등 복잡한 구문 인입 시 공식 Microsoft T-SQL 파서로 자동 폴백하여 완벽한 호환성을 보장
- **Volcano 관계형 파이프라인 & 메모리 거버너**:
  Hash Join, Index Scan, GroupBy 등 조합 가능한 반복자 파이프라인과 스트리밍 배치를 제공하며, 대용량 정렬 시 메모리 한도를 초과하면 외부 머지 소트로 안전하게 디스크에 스필합니다.
- **비용 기반 최적화기 (CBO)**:
  테이블 통계(카디널리티, 고유값 수, 히스토그램)와 다중 컬럼 확장 통계를 기반으로 다중 테이블 조인 순서를 최적화하여 가장 효율적인 실행 계획을 수립합니다.

### 2.4. 고성능 스토리지 & ACID 트랜잭션
- **이중 스토리지 백엔드**:
  - **In-Memory OCC 엔진**: 동시성 SkipList 인덱스와 낙관적 동시성 제어(OCC)를 기반으로 초당 186,000건 이상의 초고속 트랜잭션을 처리합니다 (TPC-C 최대 779,000+ tpmC, 3.6% 초저 Abort율).
  - **WiredTiger MVCC 디스크 엔진**: 락프리 B+Tree 구조로 완전한 ACID 내구성을 보장하며 초당 120,000건 이상의 트랜잭션을 안정적으로 처리합니다 (TPC-C 최대 833,000+ tpmC).
- **테이블 팽창 없는 무중단 MVCC**:
  다중 버전 업데이트 체인을 메모리에서 관리하여 최신 커밋 버전만 디스크에 기록하므로, 별도의 Vacuum 프로세스나 테이블 팽창(Bloat)으로 인한 성능 저하가 발생하지 않습니다.
- **비차단 스냅샷 격리 (Snapshot Isolation)**:
  읽기와 쓰기가 서로를 차단하지 않아, 장시간 소요되는 분석 쿼리나 백업 작업 중에도 온라인 트랜잭션 쓰기가 중단 없이 처리됩니다.
- **비차단 온라인 스냅샷 핫 백업**:
  활성 트랜잭션을 중단하지 않고 실시간 압축 백업 아카이브를 생성합니다. CLI, REST API, 웹 어드민에서 원클릭으로 지원됩니다.
- **고가용성 복제 (HA Replication)**:
  Primary 노드에서 복제본으로의 실시간 WAL 스트리밍 복제 및 무중단 장애 조치(Failover)를 지원합니다.

### 2.5. 웹 관리 스튜디오 & 개발자 환경
- **단일 독립 실행형 바이너리**:
  DB 엔진, 프로토콜 리스너, REST API, 웹 관리 도구가 단 하나의 정적 파일로 빌드되어 외부 런타임 의존성이 전혀 없습니다 (공식 지원 OS: macOS, Linux).
- **내장 웹 관리 스튜디오 (포트 8090)**:
  브라우저 대시보드(`/_/`), 테이블 스키마 탐색기, 데이터 그리드 편집기, SQL 쿼리 콘솔, 실시간 스트림 뷰어, 요청 활동 로그를 기본 제공합니다.
- **자동 생성 RESTful CRUD API**:
  테이블 생성 즉시 REST API 엔드포인트가 자동 활성화되며, 불리언 필터링, 정렬, 페이지네이션을 지원합니다.
- **JavaScript & TypeScript 클라이언트 SDK**:
  서버 바이너리에서 직접 제공하는 경량 SDK를 통해 브라우저, Node.js, Bun, Deno 등에서 간편하게 연동할 수 있습니다.
- **행 수준 보안 (RLS) & 자동화 도구**:
  사용자 인증 기반 데이터 접근 제어(RLS), 이벤트 웹훅, 크론 자동화 스케줄러, SMTP 이메일 발송 기능을 내장합니다.

---

## 3. 빠른 시작 가이드 (Quick Start Guide)

### 3.1. 소스코드 빌드

#### 사전 요구 사항
- **지원 운영체제 (OS)**: **macOS**, **Linux** (당분간 macOS 및 Linux x86_64/arm64 환경을 공식 지원하며, Windows 환경은 향후 마일스톤에서 지원 예정)
- Go 1.22 이상
- (WiredTiger 백엔드 사용 시 선택 사항) `wiredtiger` 개발 헤더 및 라이브러리:
  - macOS: `brew install wiredtiger`
  - Linux (Ubuntu/Debian): `sudo apt-get install libwiredtiger-dev`

#### 빌드 커맨드

```bash
# WiredTiger 스토리지를 지원하는 프로덕션 단일 바이너리 빌드
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql

# 순수 Go 인메모리 엔진 전용 단일 바이너리 빌드 (Cgo 의존성 제로, 어디서나 실행 가능)
go build -buildvcs=false -o bin/lionsql ./cmd/lionsql
```

### 3.2. 서버 기동

```bash
# 기본 설정(포트 5433, 1433, 8090)으로 포그라운드 기동 ('data' 디렉터리 자동 생성)
./bin/lionsql

# 또는 순수 Go 인메모리 엔진으로 기동 (Cgo 라이브러리 불필요)
./bin/lionsql -engine=memory

# 또는 실시간 로깅 지원 백그라운드 데몬으로 기동
./bin/lionsql start -dev

# 데몬 상태 확인, 재기동 또는 정상 종료
./bin/lionsql status
./bin/lionsql restart -dev
./bin/lionsql stop
```

서버 기동 시 콘솔에 접속 엔드포인트 정보가 출력됩니다:

```text
--------------------------------------------------------------------------------
 LionSQL - Single binary High-Concurrency OLTP Database Server with T-SQL & Stored Procedure (v0.90.0)
--------------------------------------------------------------------------------
 [Configuration]  Zero-Config (Built-in Defaults)
 [Web Dashboard]  http://127.0.0.1:8090/_/
 [HTTP API]       http://127.0.0.1:8090/api/health
 [CDC Stream]     http://127.0.0.1:8090/api/cdc/stream (NDJSON / SSE)
 [Superuser Auth] admin@lionsql.local (JWT Auth: true)
 [Supported GUI]  DBeaver / Beekeeper Studio / DbGate / Azure Data Studio
 [Postgres Wire]  psql -h 127.0.0.1 -p 5433 (PostgreSQL v3 protocol, psql / pgx)
 [TDS / MSSQL]    sqlcmd -S 127.0.0.1,1433 (TDS 7.x/8.0 protocol, sqlcmd / SSMS)
 [Data Directory] data
 [Storage Engine] wiredtiger
 [Session Pooler] Max Workers: 32, Max Idle Sessions: 64 (No PgBouncer required)
 [Memory Governor] max_memory: 2867MB (Go runtime GC soft limit), work_mem: 64MB (External Merge Sort & HashJoin guard)
 [Replication]    Role: PRIMARY, Node:  (HistoryCap: 10000)
--------------------------------------------------------------------------------
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
psql -h 127.0.0.1 -p 5433 -U admin -d postgres
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
sqlcmd -S 127.0.0.1,1433 -U admin -d master
```

```sql
-- T-SQL 저장 프로시저 호출
EXEC TransferFunds @from = 1, @to = 2, @amount = 100;
GO
```

### `psql \copy`를 통한 대용량 스트리밍 복사

```bash
-- CSV 파일로 초고속 데이터 내보내기 (Export)
psql -h 127.0.0.1 -p 5433 -d postgres -c "\copy accounts to 'accounts.csv' with (format csv, header)"

-- CSV 파일에서 초고속 데이터 가져오기 (Import)
psql -h 127.0.0.1 -p 5433 -d postgres -c "\copy accounts from 'accounts.csv' with (format csv, header)"
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
	dsn := "postgres://admin:1234567890@127.0.0.1:5433/postgres?sslmode=disable"
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
import LionSQL from './sdk/lionsql.js';

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

### 통합 CLI 및 백그라운드 데몬 제어

LionSQL은 일급 라이프사이클 서브커맨드를 지원하는 현대적 통합 CLI를 제공합니다:

```bash
# 기본 포그라운드 실행
./bin/lionsql                   # ./data 디렉터리를 사용하여 즉시 실행
./bin/lionsql serve -dir=./data # 명시적 서버 실행 서브커맨드

# 백그라운드 데몬 관리
./bin/lionsql start -dev        # 백그라운드 데몬 기동 (lionsql.pid 및 lionsql.log 자동 생성)
./bin/lionsql status            # 데몬 구동 상태 및 PID 확인
./bin/lionsql restart -dev      # 데몬 무중단 재기동
./bin/lionsql stop              # 데몬 정상 종료 (Graceful Shutdown)

# 운영 및 진단 서브커맨드
./bin/lionsql version           # 엔진 버전 및 빌드 태그 확인
./bin/lionsql doctor            # 환경, 네트워크 포트, 디렉터리 권한 사전 진단
./bin/lionsql init-config       # 기본 lionsql.json 설정 템플릿 생성
./bin/lionsql sql "SELECT 1"    # 빠른 대화형 원라인 쿼리 실행
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
./bin/lionsql backup -dir=data -target=/backups/lionsql-snapshot.tar.gz

# 아카이브로부터 데이터베이스 복원
./bin/lionsql restore -dir=data -source=/backups/lionsql-snapshot.tar.gz

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

### [기술 문서 전체 색인 디렉터리 (doc/README.ko.md)](doc/README.ko.md)
LionSQL의 모든 내부 문서, 아키텍처 결정 기록(ADR), 프로토콜 명세서에 대한 주제별 통합 색인입니다.

### 아키텍처 및 핵심 엔진 명세서
- **[초기 설계 결정 및 아키텍처 철학](doc/FOUNDATIONAL_DESIGN_DECISIONS.md)**: WiredTiger MVCC, Go 언어 선택, T-SQL VDBE 가상 머신, PGWire 호환성, Antigravity AI 협업 방법론의 기술적 배경.
- **[Zero-Allocation 메모리 아키텍처](doc/internal/ZERO_ALLOCATION_ARCHITECTURE.md)**: 20k TPS를 달성한 무할당 버퍼 풀링, TDS 프레임 풀링, CGO 커서 재사용, 가상 카탈로그 인터셉터 최적화.
- **[시스템 아키텍처 종합 명세서](doc/ARCHITECTURE.md)**: Volcano 실행기, 이중 파서 디커플링, 이중 스토리지 백엔드 종합 설계.
- **[아키텍처 결정 기록 (ADR)](doc/ADR.md)**: 프로젝트의 22대 핵심 아키텍처 결정 기록 (ADR-001 ~ ADR-022).
- **[VDBE 바이트코드 가상 머신 내부 구조](doc/internal/VDBE.md)**: 레지스터 할당, ISA 명령어 셋, 절차적 런타임 실행 메커니즘.
- **[스토리지 엔진 내부 구조 (WiredTiger & Memory OCC)](doc/internal/STORAGE.md)**: 락프리 B+Tree, 해저드 포인터, OCC 검증, AOF 로그 압축.
- **[비용 기반 최적화기 (CBO) & Volcano 파이프라인](doc/internal/CBO_VOLCANO.md)**: 균등 깊이 히스토그램, 비용 모델, 외부 머지 소트.
- **[다중 와이어 프로토콜 내부 구조](doc/internal/WIRE_PROTOCOLS.md)**: PostgreSQL v3, TDS 7.x/8.0 바이너리 프레이밍 및 고루틴 커넥션 풀러.
- **[가상 시스템 카탈로그 및 데이터 딕셔너리](doc/DATA_DICTIONARY.md)**: 시스템 뷰(`sys.*`, `information_schema.*`), 데이터 타입, 클라이언트 도구 인터셉터.
- **[고가용성 및 재해 복구 아키텍처](doc/HIGH_AVAILABILITY.md)**: 1-Writer 다중 리더 WAL 스트리밍, 장애 조치(Failover) 및 복구 SLA.

### BaaS 및 개발자 API
- **[공식 Web SDK 및 HTML 임베딩 가이드](doc/WEB_SDK.md)**: 웹 브라우저, Node.js, Bun, Deno 환경을 위한 무의존성 범용 SDK 가이드.
- **[자동 생성 RESTful CRUD API](doc/REST_API_AUTO_GENERATED.md)**: 풍부한 불리언 필터 표현식을 지원하는 `/api/records/:table` 명세.
- **[웹훅 및 행 수준 보안 (RLS)](doc/WEBHOOKS_AND_RLS.md)**: 비차단 웹훅 디스패처, HMAC 서명, 선언적 `@auth` 보안 규칙.
- **[크론 잡 스케줄러 및 SMTP 메일러](doc/CRON_AND_MAILER.md)**: 5필드 크론 자동화 및 HTML 이메일 발송 엔진.
- **[HTTP REST API 및 실시간 SSE 명세서](doc/REST_API_REFERENCE.md)**: OpenAPI 스타일의 전체 엔드포인트 명세 및 SSE 예제.

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
   go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql && \
   go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
   go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
   ```
2. **결정론적 데이터 레이스 무결성 검증 (Test)**:
   ```bash
   go test -tags wiredtiger -count=1 -v -race ./...
   ```
   모든 패키지에서 100% PASS를 달성해야 하며, **데이터 레이스(Data Race)가 0건**이어야 합니다.

   - **확장 통합 스모크 테스트 (Extended Smoke Test)**:
     [`notes/TEST_VERIFICATION_REPORT.ko.md`](notes/TEST_VERIFICATION_REPORT.ko.md)의 13대 핵심 아키텍처 도메인을 약 3초 만에 일괄 자동 검증합니다 ([`scripts/README.md`](scripts/README.md) 참조):
     ```bash
     # 1. 기본 실행 (In-Memory OCC 초고속 검증)
     ./scripts/verify_report.sh

     # 2. WiredTiger B+Tree 영속 엔진 기반 전체 검증
     ./scripts/verify_report.sh --engine=wiredtiger
     ```
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
