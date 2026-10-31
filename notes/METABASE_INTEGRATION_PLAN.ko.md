# Metabase 연동 검증 계획서 (`METABASE_INTEGRATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **과제 식별 코드**: `INTEG-METABASE-1`
> - **연동 대상**: [Metabase](https://github.com/metabase/metabase) (Clojure / JVM + 공식 JDBC 드라이버)
> - **지원 유선 프로토콜**:
>   - **PostgreSQL Wire Protocol v3** (`TCP :5433`) via `org.postgresql.Driver`
>   - **Microsoft SQL Server TDS 7.x/8.0** (`TCP :1433`) via `com.microsoft.sqlserver.jdbc.SQLServerDriver`
> - **스토리지 엔진**: WiredTiger Lock-Free B+Tree MVCC Engine
> - **핵심 불변 원칙**:
>   1. **Zero Source Modification**: Metabase 공식 Docker 이미지(`metabase/metabase:latest`) 무수정 연동 (0줄 수정)
>   2. **Single Standalone Static Binary**: 외부 미들웨어 없이 LionSQL 단일 바이너리 연동
>   3. **3-Stage Pipeline 준수**: Build ➔ Test (`-race` 100% 통과, 0 Data Race) ➔ Commit (Conventional Commits, 이모지 금지)

---

## 1. 개요 및 연동 목표

Metabase는 전 세계 기업에서 널리 활용되는 오픈소스 비즈니스 인텔리전스(BI) 및 데이터 시각화 대시보드 플랫폼입니다. 백엔드는 Clojure 및 Java JVM 위에서 구동되며, 데이터베이스 통신에 표준 JDBC 드라이버를 사용합니다.

LionSQL의 듀얼 프로토콜(PGWire & TDS) 기반 BI 호환성 검증 트랙(`INTEG-METABASE-1`)의 핵심 목표는 다음과 같습니다:

1. **듀얼 프로토콜 데이터 소스(Data Source) 연동**:
   - Metabase에서 LionSQL을 **PostgreSQL 데이터 소스(`:5433`)** 및 **SQL Server 데이터 소스(`:1433`)**로 각각 등록.
2. **스키마 인트로스펙션 및 동기화 (Sync & Fingerprint)**:
   - Metabase의 백그라운드 스키마 스캐너가 JDBC `DatabaseMetaData` 및 카탈로그 쿼리를 실행하여 LionSQL에 저장된 테이블, 컬럼, 데이터 타입, 행 수(Fingerprint)를 자동 수집 완료.
3. **GUI 비주얼 질문 빌더 (Visual Query Builder)**:
   - 코딩 없이 마우스 클릭만으로 수행되는 집계(`COUNT`, `SUM`, `AVG`), 그룹화(`GROUP BY`), 조인(`JOIN`), 날짜 절단(`date_trunc`, `DATEPART`) 쿼리의 무오류 실행.
4. **네이티브 SQL 에디터 및 대시보드 시각화**:
   - 직접 작성한 ANSI SQL 및 T-SQL 질의를 실행하고 테이블 뷰, 바 차트, 파이 차트 카드로 렌더링.
5. **선택적 앱 데이터베이스(Application DB) 모드**:
   - `MB_DB_TYPE=postgres` 환경 변수로 Metabase 자체 메타데이터 저장소로서의 기동 및 Liquibase 마이그레이션 호환성 확인.

---

## 2. 연동 아키텍처 및 환경 구성

```
+-----------------------------------------------------------+
|               Metabase Server (:3000)                     |
|  (Clojure / Ring / Visual Query Builder / Dashboard UI)   |
+-----------------------------------------------------------+
            │                                 │
   (JDBC: org.postgresql)            (JDBC: mssql-jdbc)
            ▼                                 ▼
+-----------------------------------------------------------+
|                      LionSQL Server                       |
|   - PGWire Listener (:5433)        - TDS Listener (:1433) |
|   - AST & Catalog Symbol Router    - Virtual Catalog Engine|
|   - Zero-Allocation VDBE VM        - Volcano CBO Engine   |
|   - WiredTiger Lock-Free B+Tree MVCC Engine               |
+-----------------------------------------------------------+
```

### 테스트 데이터셋 시드 (Seed Dataset)
Metabase의 다양한 분석 기능을 실측하기 위해 LionSQL에 샘플 OLTP 데이터셋을 미리 준비합니다:
- `orders` (주문 일시, 고객 ID, 총액, 결제 상태)
- `customers` (고객명, 이메일, 가입일, 국가)
- `products` (상품명, 카테고리, 단가, 재고수량)

---

## 3. 단계별 실행 계획

### 1단계: LionSQL 데몬 기동 및 샘플 시드 데이터 구축
- 데이터 디렉토리: `data_metabase`
- 데몬 실행:
  ```bash
  ./bin/lionsql -dir data_metabase -engine wiredtiger -pg 0.0.0.0:5433 -tds 0.0.0.0:1433 -http 127.0.0.1:8090 -admin-pass="MetabaseSecurePass2026!" -dev
  ```
- 샘플 테이블 및 데이터 1,000건 삽입 (`customers`, `products`, `orders`).

### 2단계: Metabase Docker 컨테이너 구동
```bash
docker run -d --name metabase_test \
  -p 3000:3000 \
  -e MB_DB_TYPE=postgres \
  -e MB_DB_DBNAME=metabase_app \
  -e MB_DB_PORT=5433 \
  -e MB_DB_USER=admin \
  -e MB_DB_PASS="MetabaseSecurePass2026!" \
  -e MB_DB_HOST="host.docker.internal" \
  metabase/metabase:latest
```
*(참고: 만약 앱 DB 기동 시 복잡한 Liquibase 내부 전용 쿼리가 발생할 경우, 1차적으로 임베디드 H2 모드로 Metabase를 띄우고 데이터 소스 모드를 집중 검증한 뒤 단계적으로 확장)*

### 3단계: 데이터 소스 등록 및 스키마 동기화 (Sync)
- Metabase 관리자 초기 설정 완료
- 데이터베이스 추가:
  - 이름: `LionSQL-PG` (타입: PostgreSQL, 호스트: `host.docker.internal:5433`, DB: `lionsql`, 계정: `admin`)
  - 이름: `LionSQL-TDS` (타입: SQL Server, 호스트: `host.docker.internal:1433`, DB: `master`, 계정: `admin`)
- 테이블 메타데이터 동기화 로그 추적 및 발견된 쿼리 결함 디버깅.

### 4단계: 비주얼 쿼리 및 대시보드 E2E 검증
- **Q1**: `orders` 테이블 전체 행 수 및 총 매출액 (`SUM(total_amount)`, `COUNT(*)`)
- **Q2**: `orders`와 `customers` 조인 후 국가별 매출액 `GROUP BY`
- **Q3**: 날짜별 매출 추이 (`date_trunc('day', order_date)` 시계열 차트)
- **Q4**: 대시보드 생성 및 카드 3종 배치

### 5단계: 품질 검증 및 산출물 (3-Stage Pipeline)
- 신규 단위 회귀 테스트: `pkg/engine/metabase_compat_test.go`
- 전체 테스트: `go test -tags wiredtiger -count=1 -race ./...` (100% 통과, 0 Data Race)
- 연동 보고서 및 가이드 작성:
  - `notes/METABASE_INTEGRATION_REPORT.ko.md`
  - `doc/METABASE_INTEGRATION_GUIDE.ko.md` / `doc/METABASE_INTEGRATION_GUIDE.md`
- `TODO.md` 및 `CHANGELOG.md` 갱신
- Conventional Commit
