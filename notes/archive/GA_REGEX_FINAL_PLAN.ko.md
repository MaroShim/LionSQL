# GA-REGEX-FINAL-1 잔여 정규식 4대 영역 완전 제거 및 100% Zero-Regex 아키텍처 완성 계획서 (`notes/GA_REGEX_FINAL_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-04  
> - **최종 갱신일**: 2026-11-04  
> - **문서 상태**: [x] Completed (완료)  
> - **과제 식별 코드**: `GA-REGEX-FINAL-1`  
> - **목표**: 엔진 프로덕션 경로에 남아있던 마지막 4대 영역의 정규식을 `proc.Lexer` 및 `sqlutil` 구조적 토크나이저로 100% 전환하여, [Invariant 7: Zero-Regex SQL Parsing in Production Paths]를 완전무결하게 달성.  
> - **연계 문서**:  
>   - 액티브 대시보드: [`TODO.md`](../TODO.md)  
>   - 아키텍처 불변식: [`notes/ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md)  
>   - 사전 정규식 제거 계획: [`notes/GA_REGEX_NESTED_REFACTORING_PLAN.ko.md`](GA_REGEX_NESTED_REFACTORING_PLAN.ko.md)  

---

## 1. 개요 및 대상 영역 (Scope of Work)

현재 코드베이스 전수 조사 결과, SQL 쿼리 문자열 조작 및 가상 카탈로그 필터링에 남아있는 정규식은 총 4개 영역(13개 정규식)입니다:

1. **영역 1: `pkg/engine/sqlexec_select.go` T-SQL 페이징 치환기**
   - `mssqlTopRe`, `mssqlDistinctTopRe`, `mssqlOffsetFetchRe`, `mssqlOffsetOnlyRe`, `mssqlFetchOnlyRe`
   - 문제점: `SELECT TOP <N>` 및 `OFFSET ... FETCH` 구문을 정규식으로 문자열 치환하여 서브쿼리나 복합 프로젝션 오염 위험.
2. **영역 2: `pkg/engine/sqlexec_select.go` SQLite 호환 테이블 IN 구문 (`desugarBareTableIn`)**
   - `reBareTableIn` (`WHERE x IN tbl`)
   - 문제점: 정규식 `\b((?:NOT\s+)?IN)\s+([a-zA-Z_]...)`으로 단어를 치환하여 함수 인자나 문자열 리터럴 오탐 위험.
3. **영역 3: `pkg/engine/sqlexec_jdbc_metadata.go` JDBC 임시 테이블 메타데이터 배치**
   - `reJdbcTempSpExec`, `reJdbcSelectFrom`
   - 문제점: DBeaver/JDBC의 배치 쿼리를 정규식 `(?is)INSERT\s+INTO\s+@...`으로 매칭.
4. **영역 4: `pkg/catalog/virtual/mssql.go`, `pg.go` 가상 카탈로그 GUI 필터링**
   - `reMssqlObjectIDEq`, `reMssqlObjectIdCallEq`, `reMssqlTableNameEq`, `reMssqlSchemaNameEq`
   - `reGuiNspnameEq`, `reGuiRelnameEq`, `reGuiSrcRelnameEq`, `reGuiSrcNspnameEq`, `reGuiTableSchemaEq`
   - 문제점: GUI 툴이 전달하는 `WHERE c.relname = '...'` 및 `object_id = ...` 필터를 정규식으로 추출.

---

## 2. 세부 구현 전략 (Step-by-Step Execution)

### Step 1: `sqlexec_select.go` 페이징 치환기 (`normalizeMSSQLPagination`) 전환
- `sqlutil.FindTopLevelKeyword` 및 `proc.Lexer`를 활용:
  - 최상위 `SELECT` 토큰 직후의 `TOP` 토큰 파싱:
    - `SELECT TOP 10 ...` 또는 `SELECT TOP (10) ...`, `SELECT DISTINCT TOP 10 ...`
    - 정규식 없이 토큰 스트림 상에서 `TOP`과 숫자, 괄호, `DISTINCT`를 추출하고 문맥 안전하게 `LIMIT <N>`으로 변환.
  - 최상위 `OFFSET <N> ROWS FETCH NEXT <M> ROWS ONLY` 파싱:
    - 문자열 후미의 `OFFSET` 및 `FETCH` 키워드 토큰 인덱스를 기반으로 `LIMIT <M> OFFSET <N>` 변환.
- `sqlexec_select.go`에서 `mssqlTopRe`, `mssqlDistinctTopRe`, `mssqlOffsetFetchRe`, `mssqlOffsetOnlyRe`, `mssqlFetchOnlyRe` 전량 삭제.

### Step 2: `sqlexec_select.go` Bare Table IN (`desugarBareTableIn`) 전환
- `proc.Lexer` 토크나이저를 순회하면서:
  - `IN` 토큰을 만났을 때, 다음 유효 토큰이 `(`가 아니고 식별자(Identifier)인 경우만 정밀 포착.
  - 다음 토큰이 키워드(`SELECT`, `WHERE` 등)인 경우 스킵.
  - 문자열 리터럴(`'...'`)이나 주석 내부의 `IN`은 토크나이저가 자동으로 분리하므로 오탐 원천 차단.
- `reBareTableIn` 전량 삭제.

### Step 3: `sqlexec_jdbc_metadata.go` 메타데이터 배치 파서 전환
- `sqlutil.HasPrefixKeyword(query, "INSERT")`:
  - `proc.Lexer`로 `INSERT INTO @temp EXEC sp_name ... ; SELECT ...` 토큰 파싱.
  - 세미콜론(`;`) 위치를 기준으로 첫 번째 문장과 두 번째 `SELECT` 문장을 안전 분리.
- `reJdbcTempSpExec`, `reJdbcSelectFrom` 전량 삭제 및 `"regexp"` import 완전 제거.

### Step 4: `pkg/catalog/virtual/mssql.go`, `pg.go` 가상 카탈로그 필터링 전환
- 9개 정규식을 `sqlutil.ParseBinaryComparison` 및 `sqlutil.ExtractEqualityFilters`로 전면 교체.
- `virtual/mssql.go`, `virtual/pg.go`에서 `"regexp"` import 완전 제거.

---

## 3. 검증 파이프라인 (Verification)
1. `go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql` 컴파일 검증.
2. `go test -tags wiredtiger -count=1 -v -race ./pkg/engine/... ./pkg/catalog/...` 회귀 테스트.
3. 25개 전체 패키지 `go test -tags wiredtiger -count=1 ./...` 100% PASS 확인.
4. 커밋 및 TODO 완료 체크.
