# Microsoft Northwind & Pubs Corpus (`test/corpora/northwind/`)

## 1. 개요 (Overview)
`Northwind`와 `Pubs`는 Microsoft SQL Server 7.0 / 2000부터 SQL Server 2022에 이르기까지 25년 이상 사용되어 온 Microsoft의 공인 표준 샘플 데이터베이스입니다. 복합 다대다 관계(`Order Details`, `titleauthor`), 자기 참조 계층 외래키(`Employees.ReportsTo`), 단가/수량/할인 계산, 집계 뷰 및 실전 통계 쿼리를 모두 포함하여 LionSQL의 관계형 OLTP 쿼리 엔진의 무결성을 완벽하게 증명합니다.

---

## 2. 파일 구성 (File Structure)
* **`northwind_schema.sql`**: Categories, Customers, Employees, Shippers, Suppliers, Products, Orders, Order Details 8개 핵심 테이블 및 기본키/외래키 DDL.
* **`northwind_data.sql`**: 표준 Northwind 샘플 시드 데이터.
* **`northwind_queries.sql`**: 전형적인 Northwind 다중 조인, 집계, 서브쿼리, 뷰 시뮬레이션 T-SQL 쿼리.
* **`pubs_schema_and_data.sql`**: 출판/도서 판매 도메인의 Pubs 스키마(publishers, authors, titles, titleauthor, sales, stores) 및 시드 데이터.

---

## 3. 라이선스 및 출처
* **출처**: [Microsoft SQL Server Samples (microsoft/sql-server-samples)](https://github.com/microsoft/sql-server-samples)
* **라이선스**: Microsoft Public License (MS-PL) / MIT License
