# ANTLR4 Microsoft SQL Server (T-SQL) Grammar Corpus (`test/corpora/antlr-tsql/`)

## 1. 개요 (Overview)
본 디렉토리는 [antlr/grammars-v4 (sql/tsql)](https://github.com/antlr/grammars-v4/tree/master/sql/tsql) 공식 리포지토리의 T-SQL 테스트 예제 코퍼스입니다. Microsoft SQL Server의 공식 EBNF 구문 정의에 기반하여, 복합 `MERGE`, 테이블 힌트(`WITH (NOLOCK)`), 윈도우 프레임 절(`ROWS BETWEEN ...`), `PIVOT/UNPIVOT`, 공통 테이블 식(`CTE`), `TRY...CATCH` 등 고급 T-SQL 구문의 어휘(Lexer) 및 구문 분석(Parser) 무결성을 교차 검증합니다.

---

## 2. 파일 구성 (File Structure)
* **`examples/merge.sql`**: `MERGE INTO ... USING ... WHEN MATCHED / NOT MATCHED` 구문 예제.
* **`examples/table_hints.sql`**: `WITH (NOLOCK, READUNCOMMITTED, INDEX(...))` 테이블 힌트 예제.
* **`examples/window.sql`**: `OVER (PARTITION BY ... ORDER BY ... ROWS BETWEEN ...)` 윈도우 함수 예제.
* **`examples/pivot.sql`**: `PIVOT` 및 `UNPIVOT` 집계 변환 구문 예제.
* **`examples/try_catch.sql`**: 중첩 `TRY...CATCH` 및 에러 핸들링 구문 예제.

---

## 3. 라이선스 및 출처
* **출처**: [antlr/grammars-v4 (sql/tsql)](https://github.com/antlr/grammars-v4/tree/master/sql/tsql)
* **라이선스**: BSD 3-Clause License
