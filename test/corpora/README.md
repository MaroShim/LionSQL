# LionSQL External T-SQL & TDS Compatibility Corpora (`test/corpora/`)

이 디렉토리는 LionSQL의 Microsoft SQL Server (T-SQL) 문법/런타임/카탈로그 및 TDS 와이어 프로토콜(`:1433`) 호환성을 교차 검증하기 위해 확보한 공식 클라이언트 드라이버, 트랜스파일러, ORM 테스트 스위트 원본 코퍼스입니다.

| 서브디렉토리 | 원본 오픈소스 저장소 | 라이선스 | 규모 | 핵심 검증 영역 |
| :--- | :--- | :---: | :--- | :--- |
| [`pymssql/`](pymssql/) | `pymssql/pymssql` (`tests/`) | LGPL-2.1 | 27개 `.py`, 199개 테스트 | 17종 고정/가변 타입 `OUTPUT` 저장 프로시저(`test_sprocs.py`), `DECIMAL`/`MONEY`/`UNIQUEIDENTIFIER` 라운드트립, Collation |
| [`mssql-jdbc/`](mssql-jdbc/) | `microsoft/mssql-jdbc` (`src/test/.../jdbc/`) | MIT | 200개 `*Test.java`, 2,641개 `@Test` | `JSON` 네이티브 타입 및 `JSON_OBJECT`/`JSON_ARRAY`/`JSON_PATH_EXISTS`, `CallableStatement`, `PreparedStatement` `#temp` 테이블, `DatabaseMetaData` (`sp_tables`, `sp_columns`, `sys.*`) |
| [`sqlglot/`](sqlglot/) | `tobymao/sqlglot` (`tests/dialects/test_tsql.py`) | MIT | 3,274줄, 63개 도메인, 526+ 검증 | T-SQL 표현식, 날짜/시간 약어(`DATEADD`, `DATEDIFF`, `DATETRUNC`), `IIF`, `TRIM`, `OPENJSON`, `FOR JSON/XML PATH`, 집합 연산 페이징(`OFFSET ... FETCH`) |
| [`tedious/`](tedious/) | `tediousjs/tedious` (`test/integration/`, `test/unit/`) | MIT | 86개 `.ts`, 971개 `it()` 케이스 | Node.js TDS 프로토콜, 데이터 타입 정밀도(`DATETIME` 밀리초 반올림 등), `sp_executesql` 입출력 파라미터, `sp_prepare`/`sp_execute`, 중첩 트랜잭션 |
| [`gorm-sqlserver/`](gorm-sqlserver/) | `go-gorm/sqlserver` (`*.go.txt`) | MIT | 9개 파일 (`create`, `migrator`, `error_translator` 등) | Go GORM SQL Server ORM 실전 쿼리: `MERGE USING (VALUES ...)` Upsert, `OUTPUT INSERTED.*`, `SET IDENTITY_INSERT`, 에러 번호(`2627`, `2601`, `547`), 카탈로그 3중 조인 |
| [`adventureworks/`](adventureworks/) | `microsoft/sql-server-samples` (`AdventureWorksLT`) | MS-PL / MIT | 11개 테이블, DDL/Seed/Queries | Microsoft 공식 AdventureWorksLT 경량 OLTP 스키마: 외래 키(FK), IDENTITY 채번, 다중 조인, 집계, 상관 서브쿼리, CTE 실전 쿼리 검증 |

> **참고**: `gorm-sqlserver/` 내부의 Go 소스 파일들은 루트 `go.mod`와의 외부 모듈(`gorm.io/gorm`) 의존성 충돌 및 `go test ./...` 오염을 방지하기 위해 `.go.txt` 확장자로 보관합니다.
