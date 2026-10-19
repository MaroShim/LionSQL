# LionSQL User Manual

이 문서는 LionSQL 데이터베이스 엔진을 처음 설치하고 실행하는 방법부터, 클라이언트 연결, 애플리케이션 개발, 그리고 운영 및 유지보수까지 전 과정을 단계별로 안내하는 **사용자 매뉴얼(User Manual)** 목차 및 골격입니다.

---

## 목차 (Table of Contents)

### 제1장. LionSQL 소개 (Introduction)
* **1.1. 개요 및 핵심 특징**
  * 단일 바이너리(Zero-External-Dependency) 아키텍처
  * 고동시성 임베디드 OLTP 엔진과 내장 Backend-as-a-Service (BaaS)
  * 다중 와이어 프로토콜 동시 지원 (PostgreSQL v3, MS SQL Server TDS, HTTP/REST)
* **1.2. 시스템 아키텍처 개관**
  * 듀얼 스토리지 엔진: 순수 Go OCC 인메모리 엔진 vs WiredTiger B+Tree MVCC 디스크 엔진
  * SQLite 스타일 T-SQL VDBE 바이트코드 가상 머신
  * Volcano 관계형 쿼리 실행 엔진 및 CBO(Cost-Based Optimizer)
* **1.3. 시스템 요구사항 및 플랫폼 지원**
  * 지원 운영체제: macOS (Apple Silicon / Intel), Linux (x86_64 / arm64), Windows
  * 빌드 및 런타임 전제조건 (Go 1.22+, 선택적 `libwiredtiger-dev`)

---

### 제2장. 설치 및 빠른 시작 (Installation & Getting Started)
* **2.1. 사전 빌드 바이너리 설치**
  * 플랫폼별 릴리즈 아카이브 다운로드 및 압축 해제
  * 실행 권한 부여 및 시스템 경로 등록
* **2.2. 소스 코드에서 빌드하기**
  * 순수 Go 정적 바이너리 빌드 (`go build ./cmd/lionsql`)
  * WiredTiger Cgo 활성화 빌드 (`go build -tags wiredtiger ./cmd/lionsql`)
* **2.3. 첫 번째 서버 기동 및 상태 확인**
  * 기본 설정으로 기동하기 (`./bin/lionsql`)
  * 기동 배너 확인 및 헬스체크 엔드포인트 검증 (`http://127.0.0.1:8090/api/health`)
* **2.4. 빠른 둘러보기 (Quick Tour)**
  * 테이블 생성, 데이터 삽입 및 조회
  * T-SQL 저장 프로시저 생성 및 실행
  * 내장 웹 관리 스튜디오(Web Studio) 둘러보기

---

### 제3장. 서버 운영 및 설정 관리 (Configuration & Operation)
* **3.1. 설정 우선순위 계층 구조**
  * CLI 플래그 > JSON 설정 파일(`lionsql.json`) > 내장 기본값
* **3.2. 설정 파일(`lionsql.json`) 구성 및 예시**
  * `server`, `network`, `auth`, `resources`, `logging`, `replication` 섹션 가이드
* **3.3. 네트워크 리스너 및 포트 관리**
  * 웹 관리 스튜디오 & REST API (기본 `:8090`)
  * PostgreSQL 와이어 프로토콜 (기본 `:5433`)
  * MS SQL Server TDS 프로토콜 (기본 `:1433`)
  * 포트 충돌 방지 및 비활성화(Opt-out) 방법
* **3.4. 데몬 프로세스 수명주기 관리**
  * 백그라운드 데몬 기동 (`-daemon`)
  * 상태 조회 (`-status`), 정상 종료 (`-stop`), 무중단 재기동 (`-restart`)
  * PID 파일 관리 및 안전한 종료
* **3.5. 스토리지 엔진 선택 및 데이터 디렉토리**
  * 영속 디스크 스토리지 (`--engine=wiredtiger`, `--dir=data`)
  * 고속 인메모리 스토리지 (`--engine=memory`, `--ephemeral`)
* **3.6. 리소스 거버너 및 메모리 튜닝**
  * 고루틴 커넥션 풀러: Max Workers, Max Idle Sessions
  * 정렬 및 해시 조인 메모리 한도 설정 (`--work-mem=64MB`)
  * 디스크 스필(Spill-to-Disk) 외부 머지 소트 동작 방식
* **3.7. 로깅 및 진단 모드**
  * 개발자 실시간 트레이싱 모드 (`--dev`)
  * 느린 쿼리 로그(Slow Query Log) 임계값 설정
  * 전체 쿼리 로그(General Query Log) 활성화 및 로테이션

---

### 제4장. 클라이언트 도구 및 생태계 연동 (Client Tool Integration)
* **4.1. PostgreSQL 클라이언트 연결**
  * CLI: `psql -h 127.0.0.1 -p 5433 -U admin`
  * GUI 도구: DBeaver, Beekeeper Studio, pgAdmin 연결 가이드
* **4.2. MS SQL Server 클라이언트 연결**
  * CLI: `sqlcmd -S 127.0.0.1,1433 -U sa -P secret`
  * GUI 도구: DbGate, Azure Data Studio, SSMS 연결 가이드
  * 연결 시 스키마 및 가상 카탈로그(`dbo`, `sys.*`) 연동 주의사항
* **4.3. 내장 웹 스튜디오(Web Studio) 활용** ([상세 사용 설명서](file:///Users/maro/Projects/SQLion/doc/WEB_STUDIO_GUIDE.ko.md))
  * 브라우저 기반 GUI 접속 (`http://127.0.0.1:8090/_/`)
  * 실시간 모니터링, 테이블 스키마 및 DDL 뷰어, 데이터 가져오기/내보내기 (CSV/JSON)
  * 실시간 쿼리 및 감사 로그 뷰어, Developer API 프리뷰 (cURL/Fetch/SDK)
  * 테이블 뷰어, 실시간 쿼리 에디터, 실행 계획(EXPLAIN) 시각화, 원클릭 핫 백업/복원 인터페이스
* **4.4. 프로그래밍 언어별 드라이버 연동 레시피**
  * Go: `database/sql` (`lib/pq`, `pgx/v5`, `go-mssqldb`)
  * Python: `psycopg2`, `psycopg3`, `pymssql`, SQLAlchemy
  * Node.js: `pg`, `tedious`, TypeORM, Prisma
  * Java / Kotlin: PostgreSQL JDBC, Microsoft JDBC
  * .NET / C#: `Npgsql`, `Microsoft.Data.SqlClient`

---

### 제5장. 데이터베이스 개발자 가이드 (Database Development Guide)
* **5.1. 스키마 설계 및 데이터 타입**
  * 테이블 생성(`CREATE TABLE`)과 지원 데이터 타입 매핑
  * 기본키(Primary Key)와 자동 증가 Identity 컬럼
  * 외래키(Foreign Key)와 참조 무결성(Cascade, Restrict, Set Null)
* **5.2. 인덱스 설계 및 전략**
  * 보조 인덱스(`CREATE INDEX`)와 복합 인덱스
  * 유니크 인덱스(Unique Index)를 통한 데이터 무결성 보장
  * 인덱스 스캔(Index Scan)과 풀 테이블 스캔(SeqScan) 이해하기
* **5.3. 트랜잭션과 동시성 제어 (ACID & Concurrency)**
  * 트랜잭션 시작(`BEGIN`), 커밋(`COMMIT`), 롤백(`ROLLBACK`)
  * 스냅샷 격리수준(Snapshot Isolation)과 락 프리(Lock-free) 읽기
  * 낙관적 동시성 제어(OCC)와 충돌 시 재시도 패턴
* **5.4. 쿼리 작성 가이드**
  * 데이터 필터링, 정렬, 그룹화(`WHERE`, `ORDER BY`, `GROUP BY`, `HAVING`)
  * 다중 테이블 조인(`INNER JOIN`, `LEFT JOIN`, `CROSS JOIN`)
  * 페이징 처리(`OFFSET ... FETCH NEXT ... ROWS ONLY`, `LIMIT / OFFSET`)
  * 서브쿼리 및 공통 테이블 표현식(`WITH CTE`)
* **5.5. T-SQL 저장 프로시저 및 비즈니스 로직 작성**
  * 프로시저 정의(`CREATE PROCEDURE`) 및 매개변수
  * 제어문 흐름(`IF...ELSE`, `WHILE`, `RETURN`)
  * 예외 처리(`BEGIN TRY...BEGIN CATCH`) 및 `RAISERROR`
  * 커서(Cursor)를 이용한 행 단위 처리
* **5.6. 웹 Backend-as-a-Service (BaaS) 기능 활용**
  * 자동 생성 REST API (`/api/tables`, `/api/records`) CRUD
  * 관리자 인증, JWT 토큰 발급 및 보안 헤더
  * 실시간 변경 스트리밍(Server-Sent Events)

---

### 제6장. 데이터베이스 운영 및 유지보수 (Operations & Administration)
* **6.1. 쿼리 성능 분석 및 최적화**
  * 실행 계획 분석: `EXPLAIN` 및 `EXPLAIN ANALYZE` 결과 해석
  * 비용 기반 옵티마이저(CBO) 통계 수집: `ANALYZE TABLE <name>`
  * 인덱스 힌트 및 쿼리 튜닝 모범 사례
* **6.2. 온라인 핫 백업 및 재해 복구 (Backup & PITR)**
  * 무중단 온라인 핫 백업 수행 (`-backup`, `/api/backup`)
  * 백업 아카이브 검증 및 전체 복원 (`-restore`, `/api/restore`)
* **6.3. 고가용성(HA) 및 읽기 분산 복제 (Replication)**
  * 1-Writer Multi-Reader 토폴로지 구성
  * Primary 노드 설정 및 Replica 노드 LSN 스트리밍 연동
  * 장애 발생 시 Replica 노드의 Primary 무중단 수동/자동 승격(`Promote`)
* **6.4. 보안 강화 및 TLS/SSL 통신 암호화**
  * TLS 인증서 및 개인키 설정 (`--tls`, `--tls-cert`, `--tls-key`)
  * SSL 연결 강제 모드 (`--require-ssl`)
  * 인스턴스 격리 원칙 및 포트/방화벽 바인딩 지침
* **6.5. 장애 조치 및 문제 해결 (Troubleshooting)**
  * 프로세스 락 충돌(`Resource busy`) 해결
  * 포트 바인딩 실패 및 좀비 프로세스 정리
  * 데이터 손상 검사 및 진단 툴(`--doctor`)
