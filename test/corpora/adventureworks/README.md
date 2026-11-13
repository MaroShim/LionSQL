# Microsoft AdventureWorksLT Corpus (`test/corpora/adventureworks/`)

## 1. 개요 (Overview)
`AdventureWorksLT`는 Microsoft SQL Server 공식 샘플 데이터베이스인 `AdventureWorks`의 경량(Lightweight) OLTP 버전입니다. 복잡한 XML 인덱스, UDT, CLR 객체, Spatial 타입 없이 순수 관계형 OLTP 스키마(11개 테이블)로 구성되어 있어, LionSQL의 Microsoft SQL Server T-SQL DDL/DML, Foreign Key 무결성, IDENTITY 컬럼 자동 채번, 제약 조건(`CHECK`, `DEFAULT`), 그리고 표준 복합 조인/집계 쿼리 호환성을 검증하는 최적의 오라클 코퍼스입니다.

---

## 2. 파일 구성 (File Structure)
* **`adventureworks_lt_schema.sql`**: 11개 테이블의 DDL 정의 (`dbo.ErrorLog` 및 `SalesLT.*` 10개 테이블), 기본키(`PK`), 외래키(`FK`), `IDENTITY(1,1)`, `DEFAULT`, `CHECK` 제약 조건 포함.
* **`adventureworks_lt_data.sql`**: 고객, 주소, 제품 카테고리, 제품 모델, 제품, 판매 주문 헤더 및 상세 라인 아이템을 아우르는 현실적인 비즈니스 샘플 데이터.
* **`adventureworks_lt_queries.sql`**: 실전 T-SQL OLTP/분석 쿼리 8종 (다중 조인, 자기 참조 계층 조인, 집계 함수, EXISTS 상관 서브쿼리, CASE WHEN 디코더, 공통 테이블 식 CTE).

---

## 3. 스키마 구성 (Table Catalog)

| 스키마 / 테이블명 | 컬럼 수 | 주요 역할 | 주요 제약 조건 |
| :--- | :---: | :--- | :--- |
| `[dbo].[ErrorLog]` | 9 | 시스템/프로시저 런타임 에러 감사 로그 | `PK`, `DEFAULT (GETDATE())` |
| `[SalesLT].[Address]` | 9 | 고객 배송지 및 청구지 물리 주소 | `PK`, `IDENTITY`, `DEFAULT` |
| `[SalesLT].[Customer]` | 15 | B2B 및 개인 고객 계정 마스터 | `PK`, `IDENTITY`, `DEFAULT` |
| `[SalesLT].[CustomerAddress]` | 5 | 고객-주소 M:N 매핑 및 주소 유형(Main/Shipping) | `PK(복합)`, `FK(Customer, Address)` |
| `[SalesLT].[ProductCategory]` | 5 | 자전거/의류 등 제품 분류 계층 | `PK`, `IDENTITY`, `FK(자체 계층 참조)` |
| `[SalesLT].[ProductModel]` | 5 | 제품 모델 및 카탈로그 설명 | `PK`, `IDENTITY` |
| `[SalesLT].[ProductDescription]` | 4 | 제품 다국어 텍스트 설명 | `PK`, `IDENTITY` |
| `[SalesLT].[Product]` | 16 | 판매 제품 마스터 (가격, 원가, 규격, 색상) | `PK`, `IDENTITY`, `FK(Category, Model)`, `CHECK` |
| `[SalesLT].[ProductModelProductDescription]` | 5 | 모델별 다국어 설명 매핑 | `PK(복합)`, `FK(Model, Description)` |
| `[SalesLT].[SalesOrderHeader]` | 21 | 판매 주문 헤더 (주문일, 마감일, 상태, 총액, 배송지) | `PK`, `IDENTITY`, `FK(Customer, Address)` |
| `[SalesLT].[SalesOrderDetail]` | 9 | 주문 상세 라인 아이템 (수량, 단가, 할인율, 라인합계) | `PK(복합)`, `FK(OrderHeader, Product)`, `CHECK` |

---

## 4. 라이선스 및 출처
* **출처**: [Microsoft SQL Server Samples (microsoft/sql-server-samples)](https://github.com/microsoft/sql-server-samples)
* **라이선스**: Microsoft Public License (MS-PL) / MIT License
