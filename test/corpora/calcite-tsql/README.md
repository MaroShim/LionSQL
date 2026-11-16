# Apache Calcite Microsoft SQL Server (T-SQL) Corpus (`test/corpora/calcite-tsql/`)

## 1. 개요 (Overview)
본 디렉토리는 관계형 대수 최적화 및 SQL 컴파일러 프레임워크인 [Apache Calcite (apache/calcite)](https://github.com/apache/calcite)의 T-SQL dialect 테스트 스위트(`tsql.iq`) 코퍼스입니다. `CHARINDEX`, `LEN`, `REVERSE`, `DATEADD`, `DATEDIFF`, `IIF`, `ISNULL` 등 Microsoft SQL Server 전용 빌트인 함수 및 연산자의 의미론적(Semantic) 동작을 검증합니다.

---

## 2. 파일 구성 (File Structure)
* **`tsql.iq`**: Apache Calcite `core/src/test/resources/sql/tsql.iq`에서 추출한 T-SQL 내장 함수 및 조건식 Quasiquote 테스트.

---

## 3. 라이선스 및 출처
* **출처**: [Apache Calcite (apache/calcite)](https://github.com/apache/calcite)
* **라이선스**: Apache License 2.0
