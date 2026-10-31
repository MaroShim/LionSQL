# Metabase BI 연동 가이드 (`METABASE_INTEGRATION_GUIDE.ko.md`)

이 문서는 엔터프라이즈 오픈소스 BI 대시보드 도구인 **[Metabase](https://www.metabase.com/)**를 LionSQL에 연결하여 비주얼 쿼리 빌더 및 SQL 분석 환경을 구성하는 공식 운영 가이드입니다.

LionSQL은 **PostgreSQL Wire 프로토콜(TCP :5433)**과 **Microsoft SQL Server TDS 프로토콜(TCP :1433)**을 동시에 지원하므로, 사용 환경과 기호에 따라 원하는 드라이버를 선택하여 연결할 수 있습니다.

---

## 1. 사전 요구사항

- **LionSQL 바이너리** (v0.90.0+)
- **Docker** 및 Docker Compose (Metabase 컨테이너 구동용)

---

## 2. LionSQL 서버 기동

LionSQL 서버를 기동하여 PostgreSQL Wire 및 MSSQL TDS 리스너를 함께 활성화합니다.

```bash
# WiredTiger 영속 엔진 모드로 기동 (PGWire :5433, TDS :1433)
./bin/lionsql \
  -dir data_metabase \
  -engine wiredtiger \
  -pg 0.0.0.0:5433 \
  -tds 0.0.0.0:1433 \
  -admin-pass="YourSecurePassword2026!"
```

---

## 3. Metabase 컨테이너 실행

공식 Metabase Docker 이미지를 실행합니다.

```bash
docker run -d --name metabase \
  -p 3000:3000 \
  metabase/metabase:latest
```

기동 후 브라우저에서 `http://localhost:3000`에 접속하여 최초 관리자 계정을 생성합니다.

---

## 4. 데이터베이스 연결 설정

Metabase 관리자 화면 (`Settings` ➔ `Admin Settings` ➔ `Databases` ➔ `Add database`)에서 아래 두 가지 방식 중 하나를 선택합니다:

### 방법 A: PostgreSQL 드라이버로 연결 (권장)
- **Database type**: `PostgreSQL`
- **Display name**: `LionSQL-PG`
- **Host**: `host.docker.internal` (또는 LionSQL 서버 IP)
- **Port**: `5433`
- **Database name**: `master` (또는 임의 DB명, 가상 USE 지원)
- **Username**: `admin` (또는 `admin@lionsql.local`)
- **Password**: `YourSecurePassword2026!`
- **Use a secure connection (SSL)**: 미체크

### 방법 B: SQL Server 드라이버로 연결 (TDS)
- **Database type**: `SQL Server`
- **Display name**: `LionSQL-MSSQL`
- **Host**: `host.docker.internal` (또는 LionSQL 서버 IP)
- **Port**: `1433`
- **Database name**: `master`
- **Username**: `admin`
- **Password**: `YourSecurePassword2026!`
- **Use a secure connection (SSL)**: 미체크

`Save` 버튼을 누르면 연결 검증 및 스키마 인트로스펙션이 즉시 수행됩니다.

---

## 5. 비주얼 쿼리 빌더(MBQL) 및 SQL 질의 활용

연결이 완료되면 Metabase의 모든 시각화 기능을 즉시 활용할 수 있습니다:

1. **자동 테이블/필드 탐색**: 관계형 테이블(`customers`, `products`, `orders`)의 모든 컬럼과 데이터 타입이 자동으로 감지됩니다.
2. **비주얼 집계 질의**:
   - `orders` 테이블 선택 ➔ `Summarize` ➔ `Count of rows` by `status`
   - `orders` 테이블 선택 ➔ `Summarize` ➔ `Sum of total_amount` by `status`
3. **다중 테이블 조인**:
   - `orders` 테이블 ➔ `Join data` ➔ `customers` (조건: `customer_id = id`) ➔ `Summarize` by `country`
4. **시계열 일자 분석**:
   - `order_date` 필드를 기준으로 월별/주별/일별 트래픽 및 매출 추이 차트 시각화.
5. **원시 SQL 질의**:
   - `+ New` ➔ `SQL query`를 선택하여 복잡한 서브쿼리 및 CTE, 윈도우 함수 질의 실행.

---

## 6. 문제 해결 및 팁 (Troubleshooting)

- **Docker 컨테이너에서 로컬 호스트 접근 불가 시**: macOS/Windows Docker 환경에서는 `localhost` 대신 `host.docker.internal`을 호스트 주소로 입력하십시오. Linux 환경에서는 Docker 실행 시 `--add-host=host.docker.internal:host-gateway` 옵션을 추가합니다.
- **스키마 변경 후 즉시 반영**: 새 테이블을 생성했거나 DDL을 변경한 경우, Metabase 관리자 페이지의 `Databases` ➔ 해당 DB 선택 ➔ `Sync database schema now`를 클릭하면 1초 이내에 새 스키마가 동기화됩니다.
