# Metabase BI 연동 검증 보고서 (`METABASE_INTEGRATION_REPORT.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **과제 식별 코드**: `INTEG-METABASE-1`
> - **연동 대상**: [Metabase](https://github.com/metabase/metabase) (공식 Docker 이미지 `metabase/metabase:latest`, Clojure/JVM + JDBC)
> - **연동 프로토콜**: 
>   - PostgreSQL Wire Protocol v3 (`TCP :5433`, `org.postgresql.Driver`)
>   - Microsoft SQL Server TDS 7.x/8.0 (`TCP :1433`, `com.microsoft.sqlserver.jdbc.SQLServerDriver`)
> - **스토리지 엔진**: WiredTiger Lock-Free B+Tree MVCC Engine
> - **검증 결과**: **100% 통과 (Pass)** (Metabase 소스 코드 변경: **0줄**)

---

## 1. 개요 및 연동 목표

Metabase는 전 세계 수많은 엔터프라이즈에서 활용하는 대표적인 오픈소스 비즈니스 인텔리전스(BI) 대시보드 도구입니다. 복잡한 SQL 코딩 없이 마우스 클릭만으로 데이터를 탐색하는 Visual Query Builder(MBQL)와 원시 SQL 쿼리 에디터를 함께 제공하며, JDBC 드라이버의 메타데이터 인터페이스(`DatabaseMetaData`)를 극한까지 활용하여 스키마 인트로스펙션 및 필드 핑거프린팅을 수행합니다.

LionSQL의 듀얼 와이어 프로토콜 호환성 검증 트랙(`INTEG-METABASE-1`)의 핵심 목표는 다음과 같습니다:
1. **Zero Source Modification**: 공식 Metabase Docker 컨테이너(`metabase/metabase:latest`)를 단 한 줄의 소스 수정 없이 LionSQL에 연결.
2. **Dual-Protocol Parity**: PostgreSQL Wire Protocol(`:5433`)과 Microsoft SQL Server TDS(`:1433`) 2개 채널 모두에서 데이터 소스 등록 및 인증 통과.
3. **Full Schema Introspection**: `sync-tables`, `sync-fields`, `sync-fks`, `sync-indexes`, `fingerprint-fields` 전 단계 무오류 완료 및 관계형 테이블(`customers`, `products`, `orders`) 컬럼 타입 정확한 인식.
4. **Visual Query Builder & Native SQL**: 그룹별 집계(`COUNT`, `SUM`), 다중 테이블 파생 조인(Derived JOIN), 시계열 일자 절단(`date_trunc`), 원시 SQL 질의 E2E 100% 무오류 완주.

---

## 2. 연동 아키텍처

```
+-----------------------------------------------------------+
|             Metabase BI Server (:3000)                    |
|          (Clojure / JVM + pgjdbc + mssql-jdbc)            |
+-----------------------------------------------------------+
               │                             │
    (PostgreSQL Wire :5433)         (MSSQL TDS :1433)
               ▼                             ▼
+-----------------------------------------------------------+
|                   LionSQL Kernel Server                   |
|  - PGWire v3 Listener         - TDS 7.x/8.0 Listener      |
|  - Dynamic RowDescription     - JDBC Batch Interceptor    |
|  - Virtual Catalog Engine     - T-SQL Stored Procs (pkeys)|
|  - Volcano CBO Query Engine   - Derived Subquery Rewriter |
|  - WiredTiger Lock-Free B+Tree MVCC Storage Engine        |
+-----------------------------------------------------------+
```

### 테스트 데이터셋 (`customers`, `products`, `orders`)
- **customers** (10건): `id` (PK), `name`, `email`, `country`, `created_at`
- **products** (10건): `id` (PK), `name`, `category`, `price`, `stock_quantity`
- **orders** (20건): `id` (PK), `customer_id` (FK), `product_id` (FK), `quantity`, `total_amount`, `status`, `order_date`

---

## 3. 발견된 호환성 이슈 및 LionSQL 해결 내역

Metabase JDBC 드라이버의 스키마 동기화 및 쿼리 빌더 실행 과정에서 발견된 주요 이슈와 해결 내역은 다음과 같습니다:

### 3.1. 파생 테이블(Derived Table) 따옴표 에일리어스 파싱 정규화 (`pkg/engine/sqlexec_derived.go`)
- **현상**: Metabase 비주얼 쿼리 빌더가 `(SELECT ...) AS "Customers"`와 같이 서브쿼리 에일리어스를 큰따옴표로 감쌀 때, 파생 테이블 재작성기가 `"AS"` 키워드를 별칭으로 오인하여 `... LEFT JOIN #__dt_1_AS AS AS "Customers"`로 SQL이 변조되며 쿼리 오류 발생.
- **해결**: `reDerivedAlias` 정규표현식을 확장하여 큰따옴표(`"[^"]+"`), 백틱(`` `[^`]+` ``), 대괄호(`\[[^\]]+\]`)를 모두 인식하도록 정규화 완료.

### 3.2. 가상 카탈로그 하이픈 에일리어스 프로젝션 매핑 (`pkg/catalog/virtual/result.go`)
- **현상**: Metabase의 Clojure 필드 동기화 모듈(`sync-fields`)이 `table-name`, `database-type`, `database-position`, `pk?` 등 하이픈이 포함된 별칭으로 가상 카탈로그를 조회할 때, `database-type`이 null로 반환되어 Clojure 런타임에서 `NullPointerException: Cannot invoke "Object.toString()" because "s" is null` 예외 발생.
- **해결**: `FormatResult` 프로젝션 단계에서 하이픈 에일리어스를 언더스코어 필드명(`table_name`, `data_type`, `ordinal_position - 1`, `is_primary_key`)으로 자동 정규화 매핑하여 완전 해결.

### 3.3. TDS sys.objects 대괄호 별칭 및 권한 함수 지원 (`pkg/catalog/virtual/`)
- **현상**: Metabase MSSQL 드라이버가 테이블 탐색 시 `SELECT NULL AS [role], s.name AS [schema], o.name AS [table], HAS_PERMS_BY_NAME(...) AS [select] ... FROM sys.objects o JOIN sys.schemas s ON o.schema_id = s.schema_id`를 실행할 때, 컬럼 헤더가 `[role]` 등의 대괄호를 파싱하지 못해 `s.name as [schema` 통째로 반환되어 Clojure가 `Cannot invoke "Object.getClass()" because "x" is null` 경고 출력.
- **해결**: `reProjAs`에 대괄호 별칭(`\[[^\]]+\]`) 인식을 추가하고, `execSysObjectsMetadata` 결과셋에 `schema`, `table`, `s.name`, `o.name` 및 `HAS_PERMS_BY_NAME` 결과값 `1`을 제공하도록 보강.

### 3.4. Microsoft JDBC 드라이버 전용 메타데이터 임시 테이블 배치 인터셉터 (`pkg/engine/sqlexec_jdbc_metadata.go`)
- **현상**: 공식 Microsoft JDBC 드라이버는 `DatabaseMetaData.getColumns()` 및 `getImportedKeys()` 호출 시 `DECLARE @mssqljdbc_temp_sp_columns_result TABLE(...); INSERT INTO @... EXEC sp_columns_170 ...; SELECT ... FROM @...;` 형태의 다중 구문 배치를 전송. LionSQL의 일반 DML 파서가 `INSERT INTO @temp` 구문에서 `invalid INSERT INTO syntax` 에러 반환.
- **해결**: `isJdbcMetadataBatch` 고속 탐지기를 도입하여 해당 배치를 인터셉트하고, `sp_columns`, `sp_fkeys`, `sp_pkeys` 호출을 LionSQL 엔진 카탈로그와 연계해 `virtual.FormatResultWithSQL`을 통해 JDBC 표준 컬럼(`TABLE_CAT`, `TABLE_SCHEM`, `COLUMN_SIZE`, `KEY_SEQ` 등)으로 즉시 변환 반환하는 전용 인터셉터 구현.

### 3.5. T-SQL 시스템 저장 프로시저 `sp_pkeys`, `sp_fkeys` 추가 (`pkg/engine/sqlexec_procs.go`)
- **현상**: Metabase가 테이블 기본 키 조회를 위해 `EXEC sp_pkeys 'products', 'dbo', NULL`을 직접 호출할 때 미등록 프로시저 에러 발생.
- **해결**: `sqlexec_procs.go`에 `sp_pkeys` 및 `sp_fkeys` 핸들러를 정식 구현하여 테이블의 기본 키 및 외래 키 제약 조건 메타데이터를 표준 T-SQL 결과셋으로 반환하도록 지원.

---

## 4. 라이브 E2E 검증 결과

공식 Metabase Docker 컨테이너(`localhost:3000`)에서 수행된 라이브 API 검증 결과:

| 검증 항목 | 대상 프로토콜 | Metabase API / 기능 | 결과 | 세부 내용 |
| :--- | :---: | :--- | :---: | :--- |
| **데이터소스 등록** | PGWire (`:5433`) | `POST /api/database` | **PASS** | `LionSQL-PG` 즉시 연결 성공 |
| **데이터소스 등록** | TDS (`:1433`) | `POST /api/database` | **PASS** | `LionSQL-MSSQL` 즉시 연결 성공 |
| **스키마 인트로스펙션** | PGWire (`:5433`) | `GET /api/database/3/metadata` | **PASS** | 3개 테이블, 17개 필드 타입 및 메타데이터 100% 동기화 |
| **스키마 인트로스펙션** | TDS (`:1433`) | `GET /api/database/4/metadata` | **PASS** | 3개 테이블, 17개 필드 타입 및 메타데이터 100% 동기화 |
| **MBQL 집계 질의** | PG & TDS | `orders` GROUP BY `status` COUNT | **PASS** | Completed: 15, Processing: 2, Shipped: 3 (양 프로토콜 일치) |
| **MBQL 합계 질의** | PG & TDS | `orders` GROUP BY `status` SUM(`total_amount`) | **PASS** | Completed: $6,152.31, Processing: $737.00, Shipped: $1,077.00 |
| **다중 테이블 조인** | PG & TDS | `orders` LEFT JOIN `customers` GROUP BY `country` | **PASS** | 8개 국가별 주문 집계 100% 정상 산출 (미국 4, 독일 4 등) |
| **시계열 일자 집계** | PGWire (`:5433`) | `order_date` 월별 `date_trunc` 집계 | **PASS** | 6개월 시계열 주문 추이 정상 그룹핑 |
| **Native SQL 질의** | TDS (`:1433`) | 복합 JOIN + GROUP BY + ORDER BY | **PASS** | 국가별 매출액 내림차순 정렬 쿼리 서브밀리초 응답 |

---

## 5. 결론 및 성과

- **듀얼 프로토콜 완벽 지원**: Metabase가 제공하는 양대 엔터프라이즈 드라이버(`org.postgresql.Driver` 및 `com.microsoft.sqlserver.jdbc.SQLServerDriver`) 모두에서 LionSQL이 완벽한 표준 RDBMS로 투명하게 동작함을 실증하였습니다.
- **Zero-Patch 원칙 완수**: 공식 배포 Docker 이미지(`metabase/metabase:latest`)에 어떠한 패치나 설정 우회 없이 순정 상태 그대로 연동을 완료하였습니다.
- **회귀 방지 테스트 내재화**: `pkg/engine/metabase_compat_test.go`에 Metabase의 PG 카탈로그 질의, MSSQL 파생 테이블 조인, `sys.objects` 대괄호 별칭, JDBC 메타데이터 임시 테이블 배치, `sp_pkeys` 호출을 망라하는 자동화 테스트를 구축하여 영구적 호환성을 보장하였습니다.
