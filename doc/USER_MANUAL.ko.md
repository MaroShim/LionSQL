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
