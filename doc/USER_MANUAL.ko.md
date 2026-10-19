# LionSQL User Manual

이 문서는 LionSQL 데이터베이스 엔진을 처음 설치하고 실행하는 방법부터, 클라이언트 연결, 애플리케이션 개발, 그리고 운영 및 유지보수까지 전 과정을 단계별로 안내하는 **공식 사용자 매뉴얼(User Manual)**입니다.

---

## 목차 (Table of Contents)

### 제1장. LionSQL 소개 (Introduction)
* **1.1. 개요 및 핵심 특징**
  * 단일 정적 바이너리(Zero-External-Dependency) 자체 완비형 아키텍처
  * 고동시성 임베디드 OLTP 엔진과 내장 Backend-as-a-Service (BaaS)
  * 다중 와이어 프로토콜 동시 지원 (PostgreSQL v3, MS SQL Server TDS 7.x/8.0, HTTP/REST)
* **1.2. 시스템 아키텍처 개관**
  * 듀얼 스토리지 엔진: 순수 Go OCC 인메모리 엔진 vs WiredTiger B+Tree MVCC 디스크 엔진
  * SQLite 스타일 T-SQL VDBE 바이트코드 가상 머신 및 듀얼 파서 전략
  * Volcano 관계형 쿼리 실행 엔진 및 비용 기반 옵티마이저 (CBO)
* **1.3. 시스템 요구사항 및 플랫폼 지원**
  * 지원 운영체제: macOS (Apple Silicon / Intel), Linux (x86_64 / arm64), Windows
  * 빌드 및 런타임 전제조건 (Go 1.22+, 선택적 WiredTiger Cgo 라이브러리)

---

### 제2장. 설치 및 빠른 시작 (Installation & Getting Started)
* **2.1. 사전 빌드 바이너리 설치**
  * 플랫폼별 릴리즈 아카이브 다운로드 및 압축 해제
  * 실행 권한 부여 및 시스템 경로(`$PATH`) 등록
* **2.2. 소스 코드에서 빌드하기**
  * 순수 Go 인메모리 바이너리 빌드 (`go build ./cmd/lionsql`)
  * WiredTiger MVCC 디스크 엔진 포함 빌드 (`go build -tags wiredtiger ./cmd/lionsql`)
* **2.3. 첫 번째 서버 기동 및 상태 확인**
  * 포그라운드 대화형 기동 (`./bin/lionsql` 또는 `./bin/lionsql serve`)
  * 서버 기동 배너 확인 및 헬스체크 엔드포인트 검증 (`http://127.0.0.1:8090/api/health`)
* **2.4. 빠른 둘러보기 (Quick Tour)**
  * 기본 관리자 계정 로그인 및 Web Studio 접속 (`http://127.0.0.1:8090/_/`)
  * 첫 번째 테이블 생성, 데이터 삽입 및 SQL 쿼리 실행
  * T-SQL 저장 프로시저 정의 및 즉시 호출 테스트

---

### 제3장. 서버 운영 및 설정 관리 (Configuration & Operation)
* **3.1. 설정 우선순위 계층 구조**
  * CLI 플래그 > 환경 변수(`LIONSQL_*`) > JSON 설정 파일(`lionsql.json`) > 내장 기본값
* **3.2. 설정 파일(`lionsql.json`) 구성 및 템플릿 생성**
  * 기본 설정 파일 템플릿 자동 생성 (`./bin/lionsql init-config`)
  * `server`, `network`, `auth`, `resources`, `logging`, `replication` 섹션 가이드
* **3.3. 네트워크 리스너 및 포트 관리**
  * 웹 관리 스튜디오 & REST API (기본 포트 `:8090`)
  * PostgreSQL 와이어 프로토콜 (기본 포트 `:5433`, 기본 DB `postgres`)
  * MS SQL Server TDS 프로토콜 (기본 포트 `:1433`, 기본 DB `master`)
  * 포트 충돌 방지 및 특정 프로토콜 비활성화(Opt-out: `-pg=""`, `-tds=""`) 방법
* **3.4. 단일 바이너리 수명주기 관리 (Lifecycle Commands)**
  * 백그라운드 데몬 기동 (`./bin/lionsql start [-dev]`)
  * 데몬 상태 점검 (`./bin/lionsql status`)
  * 무중단 안전 재기동 (`./bin/lionsql restart [-dev]`)
  * 프로세스 정상 종료 (`./bin/lionsql stop`)
  * 데이터 무결성 및 시스템 진단 (`./bin/lionsql doctor`)
* **3.5. 스토리지 엔진 선택 및 데이터 디렉토리**
  * 영속 디스크 MVCC 스토리지 (`--engine=wiredtiger`, `--dir=data`)
  * 고속 인메모리 OCC 스토리지 (`--engine=memory`, `--ephemeral`)
  * WAL/AOF 영속성 및 자동 스냅샷 컴팩션 주기 설정
* **3.6. 글로벌 메모리 거버너 및 리소스 튜닝**
  * 시스템 물리 RAM 70% 자동 감지 및 런타임 상한제 (`--max-memory`)
  * 대용량 해시 조인 Fail-Fast 회로 차단기(Circuit Breaker) 동작 원리
  * 연산자별 작업 메모리 할당 (`--work-mem=64MB`)
  * 고루틴 연결 풀러: 최대 워커 수 및 유휴 세션 수 제한
* **3.7. 로깅 및 진단 모드**
  * 개발자 실시간 트레이싱 모드 (`--dev`)
  * 느린 쿼리 로그(Slow Query Log) 임계값 설정 (기본값: `100.0ms`)
  * 전체 쿼리 감사 로그(General Query Log) 활성화 및 파일 로테이션

---

### 제4장. 클라이언트 도구 및 생태계 연동 (Client Tool Integration)
* **4.1. PostgreSQL 표준 클라이언트 연결**
  * CLI 도구: `psql -h 127.0.0.1 -p 5433 -U admin dbname=postgres`
  * 연결 매개변수 및 SCRAM-SHA-256 인증 프로파일
* **4.2. MS SQL Server 표준 클라이언트 연결**
  * CLI 도구: `sqlcmd -S 127.0.0.1,1433 -U sa -P 1234567890 -d master`
  * TDS 7.x/8.0 패킷 협상 및 가상 카탈로그(`dbo`, `sys.*`) 연동 규칙
* **4.3. 검증된 그래픽(GUI) 데이터베이스 도구 연동**
  * Beekeeper Studio (PostgreSQL / SQL Server 드라이버 연결 완비)
  * DBeaver Universal Database Tool (메타데이터 트리 브라우징 가이드)
  * Navicat Premium / Navicat for PostgreSQL
  * Azure Data Studio & SQL Server Management Studio (SSMS)
* **4.4. 내장 웹 스튜디오(Web Studio) 완벽 활용**
  * 브라우저 기반 GUI 대시보드 (`http://127.0.0.1:8090/_/`)
  * 실시간 SSE 시스템 모니터링 스트림 (`/api/realtime`)
  * 실시간 HTTP 및 SQL 요청 활동 로그 뷰어 (`/api/logs/requests`)
  * 비파괴적 컬럼 추가 시각화 DDL 모달 (`+ Add Column`)
  * 테이블 뷰어, 셀 인라인 편집, 다중 행 벌크 내보내기/가져오기 (CSV/JSON), 외래키(FK) 점프 링크 (`↗`)
  * SQL Query Studio: 쿼리 실행, `EXPLAIN` 실행 계획 트리 시각화
  * Developer API Preview 모달 (동적 스키마 페이로드 및 공식 SDK 코드 탭)
  * 라이트/다크 테마 전환 및 고대비(High-Contrast) WCAG 시인성 모드
* **4.5. 프로그래밍 언어별 공식 드라이버 연동 레시피**
  * Go: `database/sql` (`lib/pq`, `pgx/v5`, `go-mssqldb`)
  * Python: `psycopg2`, `psycopg3`, `pymssql`, SQLAlchemy
  * Node.js / TypeScript: `pg`, `tedious`, TypeORM, Prisma
  * Java / Kotlin: PostgreSQL JDBC, Microsoft JDBC Driver for SQL Server
  * .NET / C#: `Npgsql`, `Microsoft.Data.SqlClient`

---

### 제5장. 데이터베이스 개발자 가이드 (Database Development Guide)
* **5.1. 스키마 설계 및 데이터 타입**
  * 테이블 생성(`CREATE TABLE`)과 컬럼 제약조건 (`NOT NULL`, `DEFAULT`)
  * 기본키(Primary Key)와 자동 증가 Identity 컬럼 (`INT IDENTITY(1,1)`)
  * 외래키(Foreign Key)와 참조 무결성 제약조건 (`ON DELETE CASCADE / RESTRICT`)
* **5.2. 인덱스 설계 및 검색 최적화 전략**
  * 보조 인덱스(`CREATE INDEX`) 및 복합 인덱스(Composite Index)
  * 고유 인덱스(`CREATE UNIQUE INDEX`)를 통한 데이터 무결성 보장
  * 커버링 인덱스(`INCLUDE (...)`)를 통한 메인 테이블 힙 조회 제거
  * 인덱스 스캔(IndexScan) vs 풀 테이블 스캔(SeqScan) 실행 계획 판별법
* **5.3. 트랜잭션과 동시성 제어 (ACID & Concurrency)**
  * 트랜잭션 기본 제어: `BEGIN TRANSACTION`, `COMMIT`, `ROLLBACK`
  * 스냅샷 격리수준(Snapshot Isolation)과 락 프리(Lock-free) 읽기
  * 낙관적 동시성 제어(OCC)와 직렬화 충돌(`40001`) 시 재시도 패턴
* **5.4. 관계형 쿼리 작성 가이드**
  * 데이터 필터링, 정렬, 집계 (`WHERE`, `ORDER BY`, `GROUP BY`, `HAVING`)
  * 다중 테이블 조인 (`INNER JOIN`, `LEFT JOIN`, `CROSS JOIN`, `CROSS APPLY`)
  * 페이징 처리 표준 (`OFFSET ... FETCH NEXT ... ROWS ONLY` 및 `LIMIT / OFFSET`)
  * 공통 테이블 표현식 (`WITH CTE AS (...)`) 및 상관 서브쿼리
* **5.5. T-SQL 저장 프로시저 및 비즈니스 로직 작성**
  * 프로시저 정의(`CREATE PROCEDURE`) 및 입출력 매개변수(`@param INT OUTPUT`)
  * 제어문 흐름 구조 (`IF...ELSE`, `WHILE`, `RETURN`)
  * 예외 처리 블록 (`BEGIN TRY...BEGIN CATCH`), `RAISERROR`, `THROW`
  * 커서(Cursor) 수명주기 (`DECLARE`, `OPEN`, `FETCH`, `CLOSE`, `DEALLOCATE`)
* **5.6. 선언적 스키마 마이그레이션 도구 (`lionsql migrate`)**
  * 마이그레이션 파일 템플릿 생성 (`./bin/lionsql migrate new <name>`)
  * 버전 적용 및 롤백 (`./bin/lionsql migrate up`, `down`, `status`)
  * 원자적 트랜잭션 DDL 안전성 및 마이그레이션 잠금 메커니즘
* **5.7. 웹 Backend-as-a-Service (BaaS) 기능 활용**
  * 무설정 자동 RESTful CRUD 엔드포인트 (`/api/tables`, `/api/records/:table`)
  * 관리자 인증, JWT 토큰 발급 및 Row-Level Security (RLS) 규칙 적용
  * 실시간 Server-Sent Events (SSE) 변경 감지 파이프라인

---

### 제6장. 데이터베이스 운영 및 유지보수 (Operations & Administration)
* **6.1. 쿼리 성능 분석 및 비용 기반 옵티마이저 (CBO)**
  * 실행 계획 분석: `EXPLAIN` 및 실측 프로파일링 `EXPLAIN ANALYZE` 결과 해석
  * 비용 기반 옵티마이저 통계 수집: `ANALYZE TABLE <name>`
  * 인덱스 힌트(`WITH (INDEX(...))`) 및 쿼리 플래너 유도
* **6.2. 온라인 핫 백업 및 재해 복구 (Backup & Restore)**
  * 단일 바이너리 무중단 온라인 백업 (`./bin/lionsql backup -out=backup.tar.gz`)
  * REST API 기반 원격 백업 다운로드 (`POST /api/backup`)
  * 전체 데이터베이스 복원 (`./bin/lionsql restore -in=backup.tar.gz`)
* **6.3. 고가용성(HA) 및 읽기 분산 복제 (Replication)**
  * 1-Writer Multi-Reader 비동기/반동기 WAL 복제 토폴로지 구성
  * Primary 노드 설정 및 Replica 노드 LSN 스트리밍 연동 (`-replica-of=...`)
  * 리더 하트비트 타임아웃 감지 및 자동 장애 조치(Auto-Failover)
  * 수동 승격 명령 (`POST /api/replication/promote`)
* **6.4. 엔터프라이즈 보안 및 네트워크 암호화**
  * TLS/SSL 인증서 및 개인키 설정 (`--tls`, `--tls-cert`, `--tls-key`)
  * 비암호화 연결 차단 및 SSL 연결 강제 (`--require-ssl`)
  * 프로세스 격리 원칙 및 단일 카탈로그 보안 경계
* **6.5. 장애 진단 및 문제 해결 (Troubleshooting)**
  * 포트 충돌(`bind: address already in use`) 및 좀비 프로세스 정리
  * 시스템 진단 도구 실행 (`./bin/lionsql doctor`)
  * 트랜잭션 락 경합 및 메모리 거버너 차단 에러 진단

---

## 제1장. LionSQL 소개 (Introduction)

### 1.1. 개요 및 핵심 특징

LionSQL은 외부 종속성 없이 단 하나의 독립 실행 파일(Single Standalone Executable)로 구동되는 **엔터프라이즈급 고동시성 임베디드 OLTP 데이터베이스 엔진**입니다.

전통적인 관계형 데이터베이스 시스템(PostgreSQL, MS SQL Server, Oracle)은 강력하지만, 무거운 데몬 프로세스, 복잡한 설치 스크립트, 거대한 런타임 메모리 상주, 그리고 별도의 커넥션 풀러(PgBouncer)나 캐시 레이어(Redis), 프록시 서버(Nginx)와 같은 외부 부가 소프트웨어를 필수적으로 요구합니다. 반면 경량 임베디드 엔진(SQLite, DuckDB)은 설치와 배포는 극도로 간편하지만, 동시 쓰기 잠금(Concurrent Write Locking), 다중 클라이언트 네트워크 리스너의 부재, 기업용 엔터프라이즈 프로시저(T-SQL) 미지원이라는 한계를 지닙니다.

LionSQL은 이러한 양극단의 장점을 단일 바이너리에 결합하여, **"SQLite의 극단적 배포 단순성"**과 **"MS SQL Server의 비즈니스 로직 표현력(T-SQL)"**, 그리고 **"PostgreSQL의 생태계 호환성(PGWire v3)"**을 동시에 제공합니다:

1. **단일 정적 바이너리 (Zero-External-Dependency Architecture)**:
   - CGO를 포함하더라도 정적 라이브러리 링크를 통해 외부 공유 라이브러리(`*.so`, `*.dylib`)나 런타임 인터프리터(Node, Python, Java) 없이 즉시 기동됩니다.
   - 단 하나의 실행 파일(`lionsql`) 내에 DB 커널, 3대 네트워크 리스너(PGWire, TDS, HTTP), 내장 웹 관리 콘솔(Web Studio), 백업/복원 엔진, 크론 스케줄러가 모두 내장되어 있습니다.
2. **다중 와이어 프로토콜 동시 지원 (Multi-Protocol Tri-Bridge)**:
   - **PostgreSQL Wire Protocol v3 (`TCP :5433`)**: 표준 `psql`, DBeaver, GORM, Prisma, `lib/pq`, `pgx` 등 방대한 PostgreSQL 클라이언트 도구 및 드라이버와 100% 호환됩니다 (기본 카탈로그: `postgres`).
   - **Microsoft SQL Server TDS 7.x/8.0 (`TCP :1433`)**: `sqlcmd`, Azure Data Studio, SSMS, Beekeeper Studio 등 표준 MSSQL 생태계 도구와 직결됩니다 (기본 카탈로그: `master`).
   - **HTTP REST & Server-Sent Events (`TCP :8090`)**: 무설정 자동 CRUD REST API, 실시간 SSE 스트림, 브라우저 기반 GUI 관리 스튜디오를 동시 제공합니다.
3. **고동시성 듀얼 스토리지 엔진 (Dual Storage Engine)**:
   - 영속성 중심의 디스크 B+Tree MVCC 스토리지(WiredTiger)와 초고속 마이크로초 단위 응답을 보장하는 인메모리 OCC 스토리지(Pure-Go SkipList)를 상황에 맞게 플래그 하나로 선택할 수 있습니다.
4. **마이크로초 T-SQL VDBE 바이트코드 가상 머신**:
   - T-SQL 저장 프로시저 및 복합 쿼리를 사전 컴파일하여 레지스터 기반 VDBE 가상 머신에서 서브마이크로초 단위로 실행합니다.
5. **글로벌 메모리 거버너 및 회로 차단기 (Memory Governor & Circuit Breaker)**:
   - 시스템 물리 RAM의 70%를 상한선으로 자동 책정하고, 대용량 해시 조인이나 카테시안 곱 쿼리 발생 시 75µs 이내에 Fail-Fast 차단하여 OS OOM Killer로부터 프로세스를 영구 보호합니다.

---

### 1.2. 시스템 아키텍처 개관

LionSQL은 다중 계층이 엄밀하게 분리된 파이프라인 아키텍처를 가집니다:

```
               [ Client Applications & Tools ]
    (DBeaver, psql, sqlcmd, SSMS, Web Browsers, REST API)
                             │
     ┌───────────────────────┼───────────────────────┐
     ▼                       ▼                       ▼
[ PGWire v3 ]           [ TDS 7.x/8.0 ]        [ HTTP / REST ]
 (Port 5433)             (Port 1433)            (Port 8090)
     │                       │                       │
     └───────────────────────┼───────────────────────┘
                             │
                             ▼
              [ Core Session Coordinator ]
            - Protocol Handshake & Auth
            - Virtual Single Catalog Interceptor
            - Goroutine Connection Pooler
                             │
              ┌──────────────┴──────────────┐
              ▼                             ▼
    [ Primary Pratt Parser ]       [ ANTLR4 Fallback Parser ]
    (Sub-microsecond Fast-Path)    (Full-Spec Legacy T-SQL)
              │                             │
              └──────────────┬──────────────┘
                             ▼
             [ Cost-Based Optimizer (CBO) ]
           - 64-Bucket Equi-Height Histograms
           - Selinger DP Multi-Join Reordering
           - Covering Index & Predicate Pushdown
                             │
                             ▼
              [ Execution Pipeline Engine ]
    ┌────────────────────────┴────────────────────────┐
    ▼                                                 ▼
[ T-SQL VDBE VM ]                           [ Volcano Iterator ]
- Stored Procedures                         - Hash / Merge Join
- Variables, Loops, Flow                    - Vectorized Chunks
- Register sync.Pool                        - Memory Circuit Breaker
    │                                                 │
    └────────────────────────┬────────────────────────┘
                             ▼
               [ Pluggable Storage SPI ]
              (storage.Engine / Tx / Cursor)
              ┌──────────────┴──────────────┐
              ▼                             ▼
    [ In-Memory OCC Engine ]      [ WiredTiger MVCC Engine ]
    - Concurrent SkipList         - Lock-Free Hazard Pointers
    - Striped Table Locks         - Durable Write-Ahead Log
    - Redis-Style AOF/RDB         - Zero-Vacuum B+Tree
```

* **듀얼 파서 전략 (Dual-Parser Strategy)**: 고빈도 일반 쿼리는 나노초 단위의 네이티브 Pratt 파서로 빠르게 소화하고, 복잡한 벤더 특화 문법(`WITH RECOMPILE`, 복합 스키마 수식)은 공식 T-SQL ANTLR4 파서로 투명하게 폴백(Fallback)합니다.
* **비용 기반 옵티마이저 (CBO)**: 64버킷 등고 히스토그램과 MCV(Most Common Values) 통계를 결합하여 조인 순서를 최적화하고 인덱스 스캔을 결정합니다.
* **볼케이노 벡터화 실행기**: 1,024건 단위의 청크 버퍼 풀링을 통해 CPU 캐시 지역성을 극대화합니다.

---

### 1.3. 시스템 요구사항 및 플랫폼 지원

LionSQL은 경량 임베디드 설계로 인해 매우 낮은 하드웨어 사양에서도 원활히 동작합니다:

| 구분 | 최소 요구 사양 | 권장 프로덕션 사양 |
| :--- | :--- | :--- |
| **CPU** | 1 Core (x86_64 또는 ARM64) | 4 Core 이상 (Apple Silicon M 시리즈, Intel/AMD 최신 멀티코어) |
| **RAM** | 512 MB (기본 work_mem 64MB) | 4 GB 이상 (물리 메모리의 70%를 글로벌 거버너 예산으로 자동 할당) |
| **디스크** | 100 MB 여유 공간 (바이너리 약 50MB) | NVMe SSD (초당 수만 건의 WAL Group Commit I/O 보장) |
| **운영체제** | macOS 12+ (Darwin ARM64 / x86_64)<br>Linux (Ubuntu 20.04+, RHEL 8+, Alpine/Debian)<br>Windows 10 / Server 2019+ | Linux LTS (Ubuntu 22.04 LTS / 24.04 LTS x86_64 or ARM64) |
| **Go 컴파일러** | Go 1.22 이상 (소스 빌드 시) | Go 1.24 이상 |

---

## 제2장. 설치 및 빠른 시작 (Installation & Getting Started)

### 2.1. 사전 빌드 바이너리 설치

공식 릴리즈 아카이브를 다운로드하여 즉시 실행할 수 있습니다:

```bash
# 1. 아카이브 다운로드 및 압축 해제 (예: macOS ARM64)
tar -xzf lionsql-v0.90.0-darwin-arm64.tar.gz

# 2. 실행 권한 부여
chmod +x lionsql

# 3. 시스템 전역 경로로 이동 (선택 사항)
sudo mv lionsql /usr/local/bin/

# 4. 버전 확인
lionsql version
```

출력 예시:
```
LionSQL Server v0.90.0 (darwin/arm64)
Built with Go: go1.24.0 (wiredtiger enabled)
Commit: a3b34a8 (Feature Freeze Milestone)
```

---

### 2.2. 소스 코드에서 빌드하기

LionSQL 저장소를 복제한 후 Go 도구 체인을 사용하여 1분 이내에 빌드할 수 있습니다:

```bash
# 저장소 복제
git clone https://github.com/your-org/lionsql.git
cd lionsql

# A. 영속 디스크 MVCC(WiredTiger) 지원 정적 바이너리 빌드 (권장 프로덕션)
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql

# B. C 컴파일러 없는 순수 Go 인메모리 바이너리 빌드
go build -buildvcs=false -o bin/lionsql ./cmd/lionsql

# C. 벤치마크 도구 일괄 빌드
go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench
go build -tags wiredtiger -buildvcs=false -o bin/tpcc_vdbe ./cmd/tpcc_vdbe
```

---

### 2.3. 첫 번째 서버 기동 및 상태 확인

설정 파일 없이 기본 플래그만으로 서버를 즉시 기동할 수 있습니다:

```bash
# 기본 데이터 디렉토리(./data)로 포그라운드 대화형 기동
./bin/lionsql
```

터미널에 아래와 같은 아름다운 기동 배너가 출력됩니다:

```
================================================================================
  LionSQL High-Concurrency Embedded OLTP Database Server (v0.90.0)
================================================================================
 [Engine Core]    WiredTiger Lock-Free B+Tree MVCC Storage
 [Data Directory] /Users/maro/Projects/SQLion/data
 [Memory Budget]  2867MB (70% of 4096MB Physical RAM), work_mem: 64MB
 [Web Studio]     http://127.0.0.1:8090/_/ (HTTP REST Engine ready)
 [PostgreSQL]     postgres://admin@127.0.0.1:5433/postgres (PGWire v3)
 [SQL Server]     sqlcmd -S 127.0.0.1,1433 -U sa -d master (TDS 7.x/8.0)
--------------------------------------------------------------------------------
 [Notice] Default admin credentials initialized:
          Email:    admin@lionsql.local
          Password: (configured via -admin-pass or lionsql.json)
================================================================================
```

별도의 터미널 창에서 헬스체크 엔드포인트를 호출하여 기동을 검증합니다:
```bash
curl -s http://127.0.0.1:8090/api/health
```
응답:
```json
{"status":"ok","version":"0.90.0","engine":"wiredtiger","uptime_seconds":5}
```

---

### 2.4. 빠른 둘러보기 (Quick Tour)

#### 1단계: 웹 스튜디오(Web Studio) 접속
웹 브라우저를 열고 `http://127.0.0.1:8090/_/`에 접속합니다.
- 기본 관리자 이메일: `admin@lionsql.local`
- 기본 비밀번호: `1234567890` (서버 배너에 안내된 초기값)
로그인하면 모던한 대시보드와 함께 테이블 탐색기, 실시간 Live Stream 모니터링, SQL 쿼리 에디터가 나타납니다.

#### 2단계: 첫 번째 테이블 생성 및 데이터 삽입 (SQL Query Studio)
SQL 에디터 창 또는 `psql` / `sqlcmd`에서 아래 쿼리를 실행합니다:

```sql
-- 1. 고객 계좌 테이블 생성
CREATE TABLE accounts (
    id INT IDENTITY(1,1) PRIMARY KEY,
    holder_name VARCHAR(100) NOT NULL,
    email VARCHAR(150) UNIQUE,
    balance DECIMAL(18,2) DEFAULT 0.00,
    created_at DATETIME DEFAULT GETDATE()
);

-- 2. 초기 데이터 삽입
INSERT INTO accounts (holder_name, email, balance) VALUES 
('Alice Smith', 'alice@example.com', 150000.00),
('Bob Jones', 'bob@example.com', 250000.00),
('Charlie Brown', 'charlie@example.com', 50000.00);

-- 3. 데이터 조회
SELECT * FROM accounts WHERE balance >= 100000.00;
```

#### 3단계: T-SQL 저장 프로시저 정의 및 원자적 이체 실행
```sql
CREATE PROCEDURE TransferMoney
    @from_id INT,
    @to_id INT,
    @amount DECIMAL(18,2)
AS
BEGIN
    DECLARE @current_bal DECIMAL(18,2);
    SELECT balance INTO @current_bal FROM accounts WHERE id = @from_id;

    IF @current_bal < @amount
    BEGIN
        RAISERROR('Insufficient funds in sender account', 16, 1);
        RETURN -1;
    END

    UPDATE accounts SET balance = balance - @amount WHERE id = @from_id;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to_id;

    RETURN 0;
END;
```

프로시저 실행:
```sql
EXEC TransferMoney @from_id = 1, @to_id = 2, @amount = 50000.00;

-- 잔액 변동 확인
SELECT id, holder_name, balance FROM accounts;
```

---

## 제3장. 서버 운영 및 설정 관리 (Configuration & Operation)

### 3.1. 설정 우선순위 계층 구조

LionSQL은 12-Factor App 원칙과 엔지니어링 골든 룰 37에 따라 엄격하고 결정론적인 4단계 설정 계층을 적용합니다:

$$\text{CLI Flags} \;>\; \text{Environment Variables} \;>\; \text{Config File (lionsql.json)} \;>\; \text{Built-in Defaults}$$

1. **CLI Flags**: 명령어 실행 시 명시적으로 지정한 옵션이 최우선순위를 가집니다 (예: `-http=:9000`).
2. **Environment Variables**: `LIONSQL_` 접두사를 가진 환경 변수가 두 번째 우선순위입니다 (예: `LIONSQL_HTTP_ADDR=:9000`).
3. **Config File (`lionsql.json`)**: 데이터 디렉토리 또는 `-config`로 지정된 JSON 설정 파일.
4. **Built-in Defaults**: 아무것도 지정하지 않았을 때 적용되는 안전한 하드코딩 기본값 (포트 8090/5433/1433, RAM 70% 메모리 상한).

---

### 3.2. 설정 파일(`lionsql.json`) 구성 및 템플릿 생성

기본 설정 파일 템플릿을 생성하려면 아래 명령을 실행합니다:

```bash
./bin/lionsql init-config -out=lionsql.json
```

생성된 `lionsql.json` 구조 및 상세 설명:

```json
{
  "server": {
    "engine": "wiredtiger",
    "data_dir": "data",
    "ephemeral": false,
    "auto_seed": false,
    "pid_file": "data/lionsql.pid"
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
    "jwt_secret": ""
  },
  "resources": {
    "max_memory": "",
    "work_mem": "64MB",
    "max_workers": 256,
    "max_idle_sessions": 64
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
    "heartbeat_timeout_ms": 3000
  }
}
```

---

### 3.3. 네트워크 리스너 및 포트 관리

LionSQL은 단일 프로세스 내에서 3개의 네트워크 리스너를 병렬로 관리합니다:

* **`:8090` (Web Studio & REST API)**: 관리자 웹 UI 및 자동 생성 REST API 서빙.
* **`:5433` (PostgreSQL Wire Protocol)**: 기존 5432 포트와의 충돌을 피하기 위해 기본 `5433`을 사용합니다.
* **`:1433` (MS SQL Server TDS)**: 표준 SQL Server 포트인 `1433`을 사용합니다.

#### 특정 프로토콜 비활성화 (Opt-Out):
보안 정책상 특정 프로토콜만 노출하고 싶다면 빈 문자열(`""`)을 지정하여 해당 리스너를 손쉽게 비활성화할 수 있습니다:

```bash
# TDS(1433)와 HTTP(8090)는 끄고 PostgreSQL(5433) 포트만 열기
./bin/lionsql -tds="" -http="" -pg=":5433"
```

---

### 3.4. 단일 바이너리 수명주기 관리 (Lifecycle Commands)

`lionsql`은 데몬화(Daemonization)를 위해 외부 도구(`systemd`, `supervisord`, `nohup`) 없이 자체 수명주기 제어 명령을 제공합니다:

```bash
# 1. 백그라운드 데몬으로 안전 기동 (data/lionsql.pid 및 data/lionsql.log 자동 생성)
./bin/lionsql start

# 2. 개발자 실시간 트레이싱 모드로 데몬 기동
./bin/lionsql start -dev

# 3. 현재 실행 중인 데몬 상태 점검
./bin/lionsql status

# 4. 무중단 안전 재기동 (기존 프로세스에 SIGTERM 전송 후 새 인스턴스 승격)
./bin/lionsql restart -dev

# 5. 데몬 안전 종료 (진행 중인 트랜잭션 드레인 후 1.5초 내 정상 종료)
./bin/lionsql stop

# 6. 데이터베이스 무결성 및 스토리지 진단
./bin/lionsql doctor -dir=./data
```

`status` 실행 시 출력 화면:
```
============================================================
  LionSQL Server Daemon Status
============================================================
 State:       RUNNING (Active)
 Process ID:  99777
 PID File:    data/lionsql.pid
 Log File:    data/lionsql.log
 Web UI:      http://127.0.0.1:8090/_/
 Postgres:    psql -h 127.0.0.1 -p 5433
 TDS / MSSQL: sqlcmd -S 127.0.0.1,1433
============================================================
```

---

### 3.5. 스토리지 엔진 선택 및 데이터 디렉토리

* **WiredTiger MVCC 디스크 엔진 (`-engine=wiredtiger`, 기본값)**:
  - 디스크 기반의 락프리 B+Tree와 WAL 저널링을 제공합니다.
  - 비정상적인 전원 차단이나 프로세스 강제 사살(`kill -9`) 시에도 ACID 복구와 데이터 무손실을 보장합니다.
  - 실무 운영 환경의 기본 선택지입니다.
* **Pure-Go In-Memory OCC 엔진 (`-engine=memory`)**:
  - 모든 데이터를 힙 메모리의 Concurrent SkipList에 유지하며, 168,000+ TPS의 극단적인 처리량을 달성합니다.
  - `-dir`를 지정하면 Redis 스타일로 `wal.aof`와 주기적 RDB 스냅샷(`tables.json`)을 디스크에 기록합니다.
  - `-ephemeral` 플래그를 추가하면 디스크 I/O를 100% 차단하는 순수 휘발성 모드로 동작하여 통합 테스트 및 CI 파이프라인에 최적입니다.

---

### 3.6. 글로벌 메모리 거버너 및 리소스 튜닝

LionSQL은 Go 애플리케이션의 고질적 문제인 무제한 메모리 증가로 인한 **OS OOM Killer 사살을 원천 차단**하기 위해 2단계 방어 체계를 가집니다:

1. **글로벌 힙 메모리 상한제 (`--max-memory`)**:
   - 설정하지 않을 경우 **호스트 물리 RAM의 70%**를 자동 계산하여 상한선으로 책정합니다 (예: 4GB RAM ➔ 2,867MB).
   - Go 1.19+의 `debug.SetMemoryLimit` 프리미티브와 직결되어, 메모리가 상한에 근접하면 런타임 GC가 사전에 능동적으로 메모리를 회수합니다.
2. **해시 조인 Fail-Fast 회로 차단기 (`--work-mem`)**:
   - 정렬 및 조인 연산자당 메모리 예산(기본값: `64MB`)을 초과하는 대용량 카테시안 곱이나 비정상 쿼리가 유입될 경우, 75µs 이내에 `hash join memory budget exceeded` 에러를 반환하며 즉각 중단합니다.
   - 단 하나의 악성 쿼리가 시스템 전체를 OOM으로 다운시키는 사태를 완벽히 격리 방어합니다.

---

### 3.7. 로깅 및 진단 모드

* **개발자 트레이싱 모드 (`-dev`)**:
  - 인바운드 쿼리, 실행 계획 비용, 파서 폴백 발생 여부, 연결 핸드셰이크를 콘솔에 실시간으로 컬러풀하게 출력합니다.
* **느린 쿼리 로깅 (`-slow-log=...`, `-slow-threshold-ms=100.0`)**:
  - 실행 시간이 100ms를 초과하는 모든 쿼리를 전용 로그 파일에 기록하고, Web Studio의 `/api/slow-queries` 대시보드에서 시각적으로 분석할 수 있습니다.
* **일반 쿼리 감사 로깅 (`-general-log=...`)**:
  - 감사 및 규제 준수를 위해 인입되는 모든 SQL 문장을 타임스탬프 및 클라이언트 IP와 함께 순차 기록합니다.

---

## 제4장. 클라이언트 도구 및 생태계 연동 (Client Tool Integration)

### 4.1. PostgreSQL 표준 클라이언트 연결

LionSQL은 PostgreSQL Wire Protocol v3 규격을 지원하므로, 기존 PostgreSQL CLI 도구로 완벽히 접속할 수 있습니다:

```bash
# 기본 데이터베이스 postgres, 포트 5433으로 접속
psql -h 127.0.0.1 -p 5433 -U admin dbname=postgres
```
* **인증 암호**: 서버 배너에 출력된 관리자 비밀번호 입력 (기본값: `1234567890`)
* **단일 카탈로그 표준**: 데이터베이스 이름은 반드시 `postgres`를 지정합니다 (`sys.databases`나 `pg_database`에 단일 DB로 노출됨).

주요 유용한 메타 커맨드:
```sql
-- 테이블 목록 확인 (\dt)
\dt

-- 테이블 상세 스키마 확인 (\d <table_name>)
\d accounts

-- 서버 버전 및 와이어 프로토콜 상태 확인
SELECT version();
```

---

### 4.2. MS SQL Server 표준 클라이언트 연결

LionSQL은 Microsoft SQL Server의 TDS 7.x/8.0 프로토콜을 네이티브 구현하고 있습니다:

```bash
# 기본 데이터베이스 master, 포트 1433으로 접속
sqlcmd -S 127.0.0.1,1433 -U sa -P 1234567890 -d master
```

주요 T-SQL 배치 쿼리 실행 예시:
```sql
1> SELECT name, database_id FROM sys.databases;
2> GO
name       database_id
---------- -----------
master               1

(1 rows affected)
1> EXEC sp_tables;
2> GO
TABLE_QUALIFIER TABLE_OWNER TABLE_NAME TABLE_TYPE
--------------- ----------- ---------- ----------
master          dbo         accounts   TABLE     

(1 rows affected)
```

---

### 4.3. 검증된 그래픽(GUI) 데이터베이스 도구 연동

LionSQL 코어 엔지니어링 팀에서 100% 정상 작동을 실측 검증한 6대 도구의 연결 가이드입니다:

#### 1. Beekeeper Studio (강력 추천 도구)
* **연결 방식 A (PostgreSQL Driver)**:
  - Connection Type: `PostgreSQL`
  - Host: `127.0.0.1`, Port: `5433`, User: `admin`, Password: `...`, Database: `postgres`
* **연결 방식 B (SQL Server Driver)**:
  - Connection Type: `SQL Server`
  - Host: `127.0.0.1`, Port: `1433`, User: `sa`, Password: `...`, Database: `master`
  - Encrypt: `false` (또는 Trust Server Certificate 활성화)

#### 2. DBeaver Universal Database Tool
* Driver: `PostgreSQL` 또는 `Microsoft SQL Server`
* Host: `localhost`, Port: `5433` (PG) 또는 `1433` (TDS)
* Database: `postgres` (PG) 또는 `master` (TDS)
* **주의**: DBeaver가 접속 시 실행하는 `USE <dbname>` 또는 `SET extra_float_digits` 구문은 LionSQL에서 관용적 가상 No-Op(Tolerant No-Op)로 무오류 즉시 수용되므로 에러 없이 스키마 트리가 펼쳐집니다.

#### 3. Navicat Premium / Navicat for PostgreSQL
* Connection: `PostgreSQL`
* Host: `localhost`, Port: `5433`, Initial Database: `postgres`, User: `admin`

#### 4. Azure Data Studio & SSMS
* Server: `127.0.0.1,1433` (포트 번호는 쉼표로 구분)
* Authentication: `SQL Server Authentication`
* User name: `sa`, Password: `...`
* Database: `master`
* Trust server certificate: `True`

---

### 4.4. 내장 웹 스튜디오(Web Studio) 완벽 활용

외부 GUI 도구를 설치할 필요 없이 웹 브라우저(`http://127.0.0.1:8090/_/`)만으로 전체 데이터베이스를 관리할 수 있습니다:

1. **실시간 SSE 라이브 스트림 (`/api/realtime`)**:
   - 상단 헤더의 `Live Sync` 배지를 통해 실시간 QPS, 힙 메모리 사용률, 활성 고루틴 워커 수를 1초 주기로 스트리밍 차트로 확인합니다.
2. **실시간 요청 활동 감사 로그 뷰어 (`/api/logs/requests`)**:
   - `Logs` 탭에서 인입되는 모든 HTTP REST 및 SQL 쿼리의 메소드, 엔드포인트, 클라이언트 IP, HTTP 상태 코드, 소요 시간(Latency)을 마이크로초 단위로 추적합니다.
   - 100ms 초과 슬로우 쿼리는 빨간색 뱃지(`#ef4444`)로 하이라이트됩니다.
3. **비파괴적 컬럼 추가 DDL 모달 (`+ Add Column`)**:
   - 테이블 뷰어에서 `+ Add Column` 버튼을 클릭하면, 서버 재시작 없이 실시간으로 테이블 스키마에 컬럼을 추가합니다.
   - 컬럼명, 데이터 타입(`INT`, `VARCHAR`, `DATETIME`, `DECIMAL`), Null 허용 여부, 기본값을 폼으로 입력하면 즉시 반영됩니다.
4. **테이블 데이터 뷰어 & 다중 행 벌크 내보내기/가져오기**:
   - 셀 더블 클릭으로 인라인 데이터 편집 및 실시간 저장.
   - 체크박스 다중 선택 후 `Export JSON` 또는 `Export CSV`로 원클릭 덤프.
   - 외래키 컬럼 옆의 점프 링크(`↗`)를 누르면 참조 중인 부모 테이블의 대상 행으로 즉시 화면이 전환됩니다.
5. **Developer API Preview 모달**:
   - 각 테이블 행의 `API` 버튼을 누르면 해당 테이블을 조회/생성/수정하기 위한 **cURL 명령어, 바닐라 JavaScript `fetch()`, 그리고 LionSQL 공식 Web SDK (`sqlion.js`)** 연동 코드가 동적으로 생성되어 복사할 수 있습니다.
6. **라이트 / 다크 테마 고대비(WCAG AAA) 지원**:
   - 우측 상단의 테마 토글(달/해 아이콘)로 즉시 전환되며, 라이트 모드에서도 높은 시인성의 보더와 배지가 적용됩니다.

---

### 4.5. 프로그래밍 언어별 공식 드라이버 연동 레시피

#### Go (`database/sql` + `lib/pq` / `go-mssqldb`)
```go
package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

func main() {
	connStr := "host=127.0.0.1 port=5433 user=admin password=secret dbname=postgres sslmode=disable"
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var balance float64
	err = db.QueryRow("SELECT balance FROM accounts WHERE id = $1", 1).Scan(&balance)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Account Balance: $%.2f\n", balance)
}
```

#### Python (`psycopg2` / `SQLAlchemy`)
```python
import psycopg2

conn = psycopg2.connect(
    host="127.0.0.1",
    port=5433,
    user="admin",
    password="secret",
    dbname="postgres"
)
cursor = conn.cursor()
cursor.execute("SELECT id, holder_name, balance FROM accounts WHERE balance > %s", (100000,))
for row in cursor.fetchall():
    print(f"ID: {row[0]}, Name: {row[1]}, Balance: {row[2]}")
conn.close()
```

#### Node.js / TypeScript (`pg`)
```typescript
import { Client } from 'pg';

const client = new Client({
  host: '127.0.0.1',
  port: 5433,
  user: 'admin',
  password: 'secret',
  database: 'postgres',
});

async function run() {
  await client.connect();
  const res = await client.query('SELECT * FROM accounts WHERE id = $1', [1]);
  console.log(res.rows[0]);
  await client.end();
}
run();
```

---

## 제5장. 데이터베이스 개발자 가이드 (Database Development Guide)

### 5.1. 스키마 설계 및 데이터 타입

LionSQL은 표준 SQL 및 T-SQL DDL을 완벽히 지원합니다:

```sql
CREATE TABLE orders (
    order_id INT IDENTITY(1001, 1) PRIMARY KEY,
    customer_id INT NOT NULL,
    order_date DATETIME DEFAULT GETDATE(),
    status VARCHAR(20) DEFAULT 'PENDING',
    total_amount DECIMAL(18, 2) NOT NULL,
    notes TEXT,
    CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) 
        REFERENCES accounts(id) ON DELETE RESTRICT
);
```
* **`IDENTITY(seed, increment)`**: 자동 증가 단조 수치 기본키를 정의합니다.
* **외래키 제약조건**: 부모 테이블 행 삭제 시 `RESTRICT`, `CASCADE`, `SET NULL` 정책을 원자적으로 강제합니다.

---

### 5.2. 인덱스 설계 및 검색 최적화 전략

LionSQL은 디스크 B+Tree 및 인메모리 SkipList 보조 인덱스를 지원합니다:

```sql
-- 1. 단일 컬럼 보조 인덱스
CREATE INDEX idx_orders_customer ON orders(customer_id);

-- 2. 고유(Unique) 인덱스 (중복 삽입 시 Unique Violation 23505 에러 발생)
CREATE UNIQUE INDEX uq_accounts_email ON accounts(email);

-- 3. 복합(Composite) 인덱스
CREATE INDEX idx_orders_status_date ON orders(status, order_date);

-- 4. 커버링 인덱스 (Covering Index with INCLUDE)
CREATE INDEX idx_orders_cust_inc ON orders(customer_id) INCLUDE (total_amount, status);
```
* **커버링 인덱스 (`INCLUDE`)**: 인덱스 리프 노드에 프로젝션 대상 컬럼을 함께 저장하여, 메인 테이블 힙(Heap) 조회를 완전히 생략(Index-Only Scan)함으로써 I/O 비용을 대폭 절감합니다.

---

### 5.3. 트랜잭션과 동시성 제어 (ACID & Concurrency)

LionSQL은 **Non-blocking Snapshot Isolation / Read Committed** 격리수준을 제공합니다:
* **읽기는 쓰기를 막지 않고, 쓰기는 읽기를 막지 않습니다**: 공유 읽기 락(S-Lock)이 없으므로 대용량 SELECT 쿼리가 실행 중이어도 INSERT/UPDATE가 지연 없이 실행됩니다.
* **원자적 트랜잭션 제어**:
  ```sql
  BEGIN TRANSACTION;
  
  UPDATE accounts SET balance = balance - 10000 WHERE id = 1;
  UPDATE accounts SET balance = balance + 10000 WHERE id = 2;
  
  COMMIT TRANSACTION; -- 또는 예외 시 ROLLBACK TRANSACTION;
  ```
* **직렬화 충돌(`40001`)과 낙관적 동시성 제어(OCC)**:
  - 인메모리 OCC 엔진에서 두 개 이상의 동시 트랜잭션이 동일한 행을 수정하려 할 경우, 먼저 커밋한 트랜잭션이 성공하고 뒤늦은 트랜잭션은 `40001 (write conflict)` 에러를 수신합니다.
  - 애플리케이션에서는 지수 백오프(Exponential Backoff)를 적용하여 2~3회 재시도(Retry)하는 패턴을 권장합니다.

---

### 5.4. 관계형 쿼리 작성 가이드

```sql
-- 1. 조인 및 집계 쿼리
SELECT 
    a.holder_name,
    COUNT(o.order_id) AS total_orders,
    COALESCE(SUM(o.total_amount), 0.00) AS grand_total
FROM accounts a
LEFT JOIN orders o ON a.id = o.customer_id
WHERE a.balance > 0
GROUP BY a.holder_name
HAVING COUNT(o.order_id) >= 1
ORDER BY grand_total DESC;

-- 2. T-SQL / ANSI 페이징 쿼리 (OFFSET ... FETCH)
SELECT order_id, order_date, total_amount
FROM orders
ORDER BY order_date DESC
OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY;

-- 3. 공통 테이블 표현식 (Common Table Expression, CTE)
WITH HighValueOrders AS (
    SELECT customer_id, total_amount
    FROM orders
    WHERE total_amount >= 100000.00
)
SELECT a.holder_name, h.total_amount
FROM HighValueOrders h
JOIN accounts a ON h.customer_id = a.id;
```

---

### 5.5. T-SQL 저장 프로시저 및 비즈니스 로직 작성

LionSQL은 SQLite 스타일의 VDBE 바이트코드 가상 머신을 통해 T-SQL 프로시저를 네이티브 속도로 컴파일 실행합니다:

```sql
CREATE PROCEDURE ProcessMonthlyFee
    @fee_rate DECIMAL(5,2)
AS
BEGIN
    SET NOCOUNT ON;
    
    DECLARE @acc_id INT;
    DECLARE @bal DECIMAL(18,2);
    DECLARE @fee DECIMAL(18,2);
    
    -- 커서를 이용한 행 단위 안전 순회
    DECLARE acc_cursor CURSOR FOR
        SELECT id, balance FROM accounts WHERE balance > 0;
        
    OPEN acc_cursor;
    FETCH NEXT FROM acc_cursor INTO @acc_id, @bal;
    
    WHILE @@FETCH_STATUS = 0
    BEGIN
        SET @fee = @bal * (@fee_rate / 100.0);
        
        BEGIN TRY
            UPDATE accounts SET balance = balance - @fee WHERE id = @acc_id;
        END TRY
        BEGIN CATCH
            PRINT 'Failed to deduct fee for account: ' + CAST(@acc_id AS VARCHAR);
        END CATCH
        
        FETCH NEXT FROM acc_cursor INTO @acc_id, @bal;
    END
    
    CLOSE acc_cursor;
    DEALLOCATE acc_cursor;
    
    RETURN 0;
END;
```

---

### 5.6. 선언적 스키마 마이그레이션 도구 (`lionsql migrate`)

LionSQL은 단일 실행 파일 내에 마이그레이션 툴체인을 내장하고 있습니다:

```bash
# 1. 새 마이그레이션 SQL 파일 생성
./bin/lionsql migrate new add_products_table
# -> migrations/20261019_173000_add_products_table.sql 생성

# 2. 미적용 마이그레이션 일괄 적용
./bin/lionsql migrate up -dir=./data

# 3. 현재 마이그레이션 적용 상태 점검
./bin/lionsql migrate status -dir=./data

# 4. 가장 최근 마이그레이션 1건 롤백
./bin/lionsql migrate down -dir=./data
```
마이그레이션 이력은 내부 `_migrations` 시스템 테이블에 원자적 트랜잭션으로 안전하게 기록됩니다.

---

### 5.7. 웹 Backend-as-a-Service (BaaS) 기능 활용

LionSQL은 웹/모바일 프론트엔드가 백엔드 서버 없이 직접 통신할 수 있는 PocketBase 스타일의 REST API를 자동 제공합니다:

```bash
# 1. 레코드 목록 조회 (필터링 및 페이징)
curl -s "http://127.0.0.1:8090/api/records/accounts?page=1&perPage=10&sort=-balance"

# 2. 새 레코드 생성 (JSON)
curl -X POST "http://127.0.0.1:8090/api/records/accounts" \
  -H "Content-Type: application/json" \
  -d '{"holder_name":"Diana Prince","email":"diana@amazon.com","balance":500000.00}'

# 3. 실시간 변경 감지 (Server-Sent Events)
curl -N "http://127.0.0.1:8090/api/realtime"
```

---

## 제6장. 데이터베이스 운영 및 유지보수 (Operations & Administration)

### 6.1. 쿼리 성능 분석 및 비용 기반 옵티마이저 (CBO)

쿼리가 느리거나 인덱스를 타지 않을 때는 `EXPLAIN` 및 `EXPLAIN ANALYZE`로 쿼리 실행 계획을 점검합니다:

```sql
-- 1. 논리적 실행 계획 및 추정 비용 확인
EXPLAIN SELECT * FROM orders WHERE customer_id = 1;

-- 2. 실제 실행 시간(Profiling) 및 튜플 수 실측 확인
EXPLAIN ANALYZE SELECT * FROM orders WHERE customer_id = 1;
```

출력 예시:
```
-> IndexScan on orders using idx_orders_customer (cost=0.15..8.20 rows=5) (actual time=0.042ms rows=5 loops=1)
   Filter: (customer_id = 1)
Planning Time: 0.125 ms
Execution Time: 0.058 ms
```

* **통계 수집**: 대량의 데이터 삽입/삭제 후에는 아래 명령으로 64버킷 등고 히스토그램을 갱신합니다:
  ```sql
  ANALYZE TABLE orders;
  ```

---

### 6.2. 온라인 핫 백업 및 재해 복구 (Backup & Restore)

LionSQL은 가동 중인 데이터베이스를 중단하지 않고 일관된 스냅샷 아카이브(`.tar.gz`)로 즉시 백업합니다:

```bash
# 1. CLI를 통한 로컬 온라인 백업
./bin/lionsql backup -dir=./data -out=backup_20261019.tar.gz

# 2. HTTP REST API를 통한 원격 스트리밍 백업 다운로드
curl -s -X POST "http://127.0.0.1:8090/api/backup" \
  -H "Authorization: Bearer <ADMIN_JWT_TOKEN>" \
  --output remote_backup.tar.gz

# 3. 데이터베이스 완전 복원 (Restore)
./bin/lionsql restore -dir=./data_restored -in=backup_20261019.tar.gz
```

---

### 6.3. 고가용성(HA) 및 읽기 분산 복제 (Replication)

LionSQL은 복잡한 분산 코디네이터(Zookeeper, etcd) 없이 **1-Writer Multi-Reader 비동기/반동기 WAL 복제**를 지원합니다:

```bash
# 1. Primary 노드 기동 (포트 8090/5433/1433)
./bin/lionsql start -dir=./data_primary -node-id=primary-1

# 2. Replica 노드 기동 (포트 8091/5434/1434, Primary에 연결)
./bin/lionsql start -dir=./data_replica \
  -http=:8091 -pg=:5434 -tds=:1434 \
  -node-id=replica-1 \
  -replica-of=http://127.0.0.1:8090 \
  -auto-failover=true
```
* **Auto-Failover**: Primary 노드가 3초(`heartbeat-timeout-ms`) 이상 무응답일 경우, 최신 LSN을 보유한 Replica가 새 Primary로 자동 승격되어 읽기/쓰기 서비스를 무중단 승계합니다.

---

### 6.4. 엔터프라이즈 보안 및 네트워크 암호화

운영 환경에서는 공용 인터넷 노출을 방지하고 전송 계층 암호화를 활성화해야 합니다:

```bash
# TLS 인증서를 적용하여 암호화 통신 강제
./bin/lionsql start -dir=./data \
  -tls=true \
  -tls-cert=/etc/ssl/lionsql.crt \
  -tls-key=/etc/ssl/lionsql.key \
  -require-ssl=true
```
* `-require-ssl=true` 설정 시 비암호화 일반 텍스트 연결 시도는 핸드셰이크 단계에서 즉시 거절됩니다.

---

### 6.5. 장애 진단 및 문제 해결 (Troubleshooting)

| 현상 | 원인 | 해결 방법 |
| :--- | :--- | :--- |
| `bind: address already in use` | 이전 프로세스가 정상 종료되지 않고 포트를 점유 중 | `./bin/lionsql status`로 기존 PID 확인 후 `./bin/lionsql stop` 실행. 필요 시 `kill $(cat data/lionsql.pid)` |
| `hash join memory budget exceeded` | 대용량 카테시안 곱 조인으로 메모리 거버너 발동 | 쿼리에 적절한 `WHERE` 필터를 부여하거나, 세션 레벨에서 `SET work_mem = '128MB'`로 상향 조정 |
| `write conflict (SQLSTATE 40001)` | 인메모리 OCC에서 동일 행 동시 수정 충돌 | 애플리케이션 드라이버 레벨에서 10~50ms 지수 백오프 재시도 로직 적용 |
| `Database connection failed` | 레거시 데이터베이스명(`sqlion`) 지정 오류 | 단일 카탈로그 표준에 맞추어 PGWire는 `postgres`, TDS는 `master`를 지정 |

시스템 전반의 상태를 한눈에 진단하려면:
```bash
./bin/lionsql doctor -dir=./data
```
데이터 파일 정합성, WAL 체크포인트 LSN, 포트 점유 여부, 디스크 여유 공간이 즉시 출력됩니다.
