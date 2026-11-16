# Knex.js MSSQL Dialect Corpus (`test/corpora/knex-mssql/`)

## 1. 개요 (Overview)
본 디렉토리는 Node.js 생태계의 대표적인 SQL 쿼리 빌더인 [Knex.js (knex/knex)](https://github.com/knex/knex)의 Microsoft SQL Server (`mssql`) dialect 테스트 스위트 코퍼스입니다. Knex.js는 **Wiki.js**, Ghost 등 수많은 엔터프라이즈 Node.js 애플리케이션의 핵심 데이터 계층으로 사용되며, `OUTPUT INSERTED.*`, `OFFSET...FETCH` 페이징, 대괄호 식별자(`[schema].[table]`), `IDENTITY` 반환, Knex 마이그레이션 테이블 관리 등 실전 ORM 쿼리 패턴을 제공합니다.

---

## 2. 파일 구성 (File Structure)
* **`mssql_dialect_test.js`**: Knex.js `test/unit/dialects/mssql.js`에서 추출한 핵심 쿼리 빌더 검증 케이스.

---

## 3. 라이선스 및 출처
* **출처**: [Knex.js (knex/knex)](https://github.com/knex/knex)
* **라이선스**: MIT License
