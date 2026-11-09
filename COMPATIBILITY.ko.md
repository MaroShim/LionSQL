# LionSQL 호환성 사양 및 기준 매트릭스 (`COMPATIBILITY.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **최종 갱신일**: 2026-11-06
> [English Version](COMPATIBILITY.md)

LionSQL은 Microsoft SQL Server T-SQL 생태계와 PostgreSQL 클라이언트/드라이버 생태계를 단일 정적 바이너리 엔진 내에서 완벽하게 융합 서비스합니다.  
본 문서는 개발자, DBA, 아키텍트가 프로덕션 연동 전에 즉시 파악할 수 있도록 **호환성 공식 타깃 버전, 프로토콜 사양, 시스템 식별 응답값 및 검증 매트릭스**를 제공합니다.

---

## 1. 공식 호환성 기준 (Target Baseline)

| 생태계 (Ecosystem) | 공식 호환성 기준 버전 | 지원 프로토콜 규격 | 비고 |
| :--- | :--- | :--- | :--- |
| **Microsoft SQL Server** | **SQL Server 2019 (Version 15.0)** | **TDS 7.4** (Default)<br>**TDS 8.0** (Direct TLS) | SQL Server 2012 ~ 2022 클라이언트 완전 호환 |
| **PostgreSQL** | **PostgreSQL 14 ~ 16 대응** | **PostgreSQL Wire v3** | `pgx/v5` 엄격한 바이너리 포맷 100% 관통 |
| **REST & BaaS API** | **OpenAPI / JSON 표준** | **HTTP/1.1 REST & SSE** | Same-Origin 내장 SPA 웹 서버 (`--app`) 지원 |

---

## 2. 시스템 버전 및 인트로스펙션 식별값

DBeaver, SSMS, Azure Data Studio, Beekeeper Studio 등 주요 GUI 도구가 접속 시 질의하는 버전 핸드셰이크 응답값입니다:

```sql
-- 1. SQL Server 버전 확인
SELECT @@VERSION;
-- 응답: Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) / LionSQL Embedded Engine

-- 2. SQL Server 시스템 프로퍼티
SELECT 
    SERVERPROPERTY('ProductVersion') AS ProductVersion,     -- '15.0.2000.5'
    SERVERPROPERTY('ProductMajorVersion') AS MajorVersion,  -- '15'
    SERVERPROPERTY('EngineEdition') AS EngineEdition,       -- 3 (Enterprise / Developer)
    SERVERPROPERTY('Collation') AS Collation;               -- 'SQL_Latin1_General_CP1_CI_AS'

-- 3. PostgreSQL 버전 확인 (포트 :5433)
SELECT version();
-- 응답: PostgreSQL 15.0 (LionSQL Dual-Engine OLTP)
```

---

## 3. 핵심 SQL 및 T-SQL 기능 호환 매트릭스

### 3.1. T-SQL 현대 내장 함수 (SQL Server 2016~2019 규격)
* **현대 문자열 함수**: `STRING_SPLIT()`, `STRING_AGG() [WITHIN GROUP]`, `TRIM()`, `CONCAT()`, `CONCAT_WS()`, `TRANSLATE()`, `FORMAT()`.
* **네이티브 JSON 함수**: `ISJSON()`, `JSON_VALUE()`, `JSON_QUERY()`, `JSON_MODIFY()`.
* **날짜 및 시간 함수**: `DATEADD()`, `DATEDIFF()`, `DATEPART()`, `EOMONTH()`, `GETDATE()`, `SYSDATETIME()`.
* **타입 변환 및 방어 함수**: `CONVERT()`, `TRY_CONVERT()`, `CAST()`, `TRY_CAST()`, `PARSE()`.
* **윈도우 및 분석 함수**: `ROW_NUMBER()`, `RANK()`, `DENSE_RANK()`, `NTILE()`, `OVER (PARTITION BY ... ORDER BY ...)`.

### 3.2. 절차적 프로시저 및 제어 흐름 (VDBE Bytecode Engine)
* **제어 흐름 구문**: `IF...ELSE`, `WHILE`, `BEGIN...END`, `RETURN`, `GOTO`.
* **예외 처리 블록**: `BEGIN TRY ... END TRY BEGIN CATCH ... END CATCH` (`ERROR_NUMBER()`, `ERROR_MESSAGE()`, `ERROR_SEVERITY()` 완비).
* **변수 및 커서**: 로컬 변수(`DECLARE @var`), 테이블 변수(`DECLARE @tbl TABLE`), 인라인 커서(`DECLARE CURSOR FOR ... OPEN ... FETCH ... CLOSE ... DEALLOCATE`).
* **동적 쿼리 실행**: `sp_executesql` RPC 호출 (Ordinal Positional 및 Named 매핑 완전 지원).

---

## 4. 검증된 클라이언트 도구 & ORM 프레임워크

### 4.1. 공식 검증 완료 GUI 클라이언트
- **SQL Server 도구 (포트 :1433)**: SQL Server Management Studio (SSMS 19/20), Azure Data Studio, `sqlcmd`, DBeaver, Beekeeper Studio, DbGate.
- **PostgreSQL 도구 (포트 :5433)**: `psql`, pgAdmin 4, DBeaver, Metabase BI.

### 4.2. 프로덕션 검증 완료 드라이버 & ORM
- **Go**: `database/sql`, `github.com/microsoft/go-mssqldb` (v1.11+), `jackc/pgx/v5`, `lib/pq`, GORM, Ent, SQLBoiler.
- **.NET (C#)**: Entity Framework Core (EF Core 8/9), Dapper, `Microsoft.Data.SqlClient`.
- **Node.js / TypeScript**: Prisma ORM, TypeORM, Drizzle ORM, `mssql`, `pg`.
- **Rust**: Diesel ORM, SQLx.
- **Python**: SQLAlchemy, `psycopg2`, `pyodbc`.

---

## 5. 실전 애플리케이션 E2E 검증 통과 (100% Pass)

실제 오픈소스 프로덕션 서비스의 마이그레이션 스크립트와 복잡한 ORM 기계 생성 쿼리를 듀얼 프로토콜로 실측 검증하였습니다:

1. **Metabase BI**: 듀얼 프로토콜 연동, 스키마 인트로스펙션, MBQL 비주얼 쿼리 빌더 및 집계/시계열 100% 무오류.
2. **Vaultwarden (Rust Diesel)**: 47개 순차 마이그레이션 완주, 복합 PK Upsert 및 Bitwarden REST API 100% 통과.
3. **Apache Answer (Go)**: 35개 테이블 자동 생성, Identity 자동 발급 및 Q&A 커뮤니티 E2E 검증.
4. **Gitea (Go XORM)**: 듀얼 프로토콜(TDS :1433 & PGWire :5433) 연동 및 116개 테이블 풀스택 관통.
5. **Woodpecker CI & Vikunja**: 재귀 CTE(Recursive CTE), 복합 OR 인덱스 분기 및 DML 커맨드 태그 완비.
6. **Grafana**: 785개 마이그레이션 완주, 봉투 암호화(`BYTEA`) 및 듀얼 데이터 소스 대시보드 렌더링.
7. **Casdoor**: 44개 테이블 자동 생성, 복합 기본키 및 OIDC/OAuth2/RBAC 실전 검증.
8. **IOTA SDK ERP**: `pgx/v5` 엄격한 바이너리 프로토콜 및 47개 회계 스키마 마이그레이션 순차 통과.
9. **SQLite SQLLogicTest (SLT)**: 공식 107개 풀세트 (19,211 문장, **551,217개 쿼리**) 100% 무결점 통과.

---

## 6. 추가 참조 문서

- 상세 시스템 규격 및 전체 CLI/설정: **[`doc/REFERENCE_MANUAL.ko.md`](doc/REFERENCE_MANUAL.ko.md)**
- TDS 프로토콜 실측 보고서: **[`notes/TDS_COMPATIBILITY_REPORT.md`](notes/reports/TDS_COMPATIBILITY_REPORT.md)**
- PGWire 프로토콜 실측 보고서: **[`notes/PGWIRE_COMPATIBILITY_REPORT.md`](notes/reports/PGWIRE_COMPATIBILITY_REPORT.md)**
- 실전 10대 오픈소스 연동 가이드: **[`guide/README.ko.md`](guide/README.ko.md)**
