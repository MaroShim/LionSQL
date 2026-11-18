# 단일 통합 슈퍼셋 1.0 사전 구현 계획서 (`notes/plans/UNIFIED_SQL_SUPERSET_PRE_1_0_IMPLEMENTATION_PLAN.ko.md`)

> **문서 상태**: 공식 승인 및 착수 대기  
> **기준 문서**: [`doc/UNIFIED_SQL_SUPERSET_GUIDE.ko.md`](file:///Users/maro/Projects/SQLion/doc/UNIFIED_SQL_SUPERSET_GUIDE.ko.md)  
> **대상 마일스톤**: `v0.94.0` (1.0-GA 이전 완결)  
> **관련 규칙**: `[RULE:RadicalKISS]`, `[RULE:StyleIntegrity]`, `[RULE:PipelineVerification]`, `[RULE:DeterministicTesting]`

---

## 1. 추진 배경 및 목표

[`doc/UNIFIED_SQL_SUPERSET_GUIDE.ko.md`](file:///Users/maro/Projects/SQLion/doc/UNIFIED_SQL_SUPERSET_GUIDE.ko.md)에서 공식화된 LionSQL의 핵심 정체성은 **"단일 통합 슈퍼셋(Unified Superset) + SQL Server 단단한 닻(Default Preference)"**입니다.

문서에 기술된 개발자 가이드 및 시맨틱 충돌 해법이 1.0 릴리스 시점에 한 치의 거짓 없이 100% 작동하기 위해, 현재 구현 상태를 전수 감사하고 부족한 핵심 기능 4가지를 선별하여 1.0-GA 이전에 구현 및 등록합니다.

### 현황 갭(Gap) 분석 결과
1. **`ILIKE` 구문**: `pkg/sqlutil` 및 `sqlexec_eval`에는 존재하나, T-SQL 및 저장 프로시저를 컴파일하는 네이티브 Pratt 파서(`pkg/proc/token.go`, `parser_expr.go`)에 `TokenILike`가 없어 프로시저/배치 실행 시 파싱 에러 발생.
2. **`COLLATE` 인라인 식**: `ALTER DATABASE CURRENT COLLATE`는 수용되나, `WHERE col COLLATE Latin1_General_CS_AS LIKE ...`와 같은 식 레벨 `COLLATE` 평가기가 없어 대소문자 구별 검색이 미작동.
3. **`dialect_preference` 설정**: `pkg/config/`에 서버 레벨 SSOT 필드가 누락되어 있어 향후 확장 대비 및 시맨틱 닻 명시화 필요.
4. **듀얼 프로토콜 E2E 회귀 테스트**: 가이드 문서의 10개 문법 표, 6대 실전 예시, 복합 실전 쿼리가 TDS(`:1433`)와 PGWire(`:5433`) 양쪽에서 100% 동작함을 보증하는 E2E 검증 테스트 세트 필요.

---

## 2. 세부 구현 과제 및 아키텍처 명세

### 과제 1: `FEAT-SUPERSET-ILIKE-SYNTAX-1`
- **제목**: `pkg/proc/` Native Pratt Parser 및 VDBE `ILIKE` 연산자 전역 지원
- **우선순위**: P2 | **도메인**: Engine | **마일스톤**: `v0.94.0`
- **목표**:
  - `pkg/proc/token.go`에 `TokenILike` ("ILIKE") 키워드 등록.
  - `pkg/proc/parser_expr.go`에서 `expr [NOT] ILIKE pattern [ESCAPE esc]` 구문 파싱.
  - `pkg/proc/ast.go`의 `LikeExpr`에 `CaseInsensitive: true` 플래그 연동.
  - `pkg/proc/eval.go`, `compiler.go`, `vm_dispatcher.go`에서 `OpLike`에 CI 플래그를 유지하여 대소문자 무시 패턴 매칭 실행.
  - ANTLR Fallback 파서에서도 `ILIKE` 수용 확인.

### 과제 2: `FEAT-SUPERSET-EXPR-COLLATE-1`
- **제목**: 인라인 `COLLATE <collation_name>` 식 파싱 및 대소문자 구분(CS/BIN) 동적 평가
- **우선순위**: P2 | **도메인**: Engine | **마일스톤**: `v0.94.0`
- **목표**:
  - `pkg/proc/token.go`에 `TokenCollate` ("COLLATE") 등록.
  - `pkg/proc/parser_expr.go`에 `CollateExpr` 후치 연산자 파싱 (`precCollate`).
  - 식 평가 시 Collation 이름 파싱:
    - `_CS_` (Case-Sensitive) 또는 `_BIN` (Binary): `caseInsensitive = false`로 전환.
    - `_CI_` (Case-Insensitive): `caseInsensitive = true` 유지.
  - `OpLike` 및 문자열 등호/부등호 비교(`OpEq`, `OpNe`) 시 지정된 Collation 대소문자 플래그 적용.

### 과제 3: `FEAT-SUPERSET-DIALECT-PREFERENCE-CONFIG-1`
- **제목**: 서버 전역 `dialect_preference` 설정 체계 및 CLI/환경변수 바인딩
- **우선순위**: P2 | **도메인**: Engine | **마일스톤**: `v0.94.0`
- **목표**:
  - `pkg/config/config.go` 내 `ServerConfig`에 `DialectPreference string` (`json:"dialect_preference,omitempty"`) 필드 추가.
  - 기본값: `"sqlserver"` (`DefaultDialectPreference = "sqlserver"`).
  - CLI 플래그 `--dialect-preference` 및 환경변수 `LIONSQL_DIALECT_PREFERENCE` 바인딩.
  - 유효성 검증: `"sqlserver"`, `"postgres"` 이외의 값 입력 시 fast-fail 진단 에러 출력.

### 과제 4: `TEST-SUPERSET-GUIDE-E2E-PARITY-1`
- **제목**: 단일 통합 슈퍼셋 가이드 문서(Cheat Sheet & 예시) E2E 듀얼 프로토콜 검증 테스트
- **우선순위**: P2 | **도메인**: Test | **마일스톤**: `v0.94.0`
- **목표**:
  - `test/e2e/superset_parity_test.go` 신설.
  - TDS(`:1433`) 클라이언트(go-mssqldb)와 PGWire(`:5433`) 클라이언트(pgx/pq)로 동시 접속.
  - 가이드 문서 Section 4의 10개 호환 문법 항목 전수 쿼리 실행 검증:
    1. `TOP` vs `LIMIT / OFFSET`
    2. `CAST` vs `::type`
    3. `GETDATE()` vs `NOW()`
    4. `ISNULL()` vs `COALESCE()`
    5. `LIKE` vs `ILIKE`
    6. `COLLATE Latin1_General_CS_AS`
    7. `JSON_VALUE` vs `->>`
    8. `[col]` vs `"col"` vs `` `col` ``
    9. `IDENTITY` vs `SERIAL`
    10. `BEGIN TRANSACTION` vs `BEGIN`
  - Section 6의 복합 실전 쿼리(한 쿼리 안에 섞어 쓰기) 양쪽 포트에서 동시 무결성 검증.

---

## 3. 검증 파이프라인

모든 구현은 `[RULE:PipelineVerification]`에 따라 3단계 파이프라인을 엄격히 통과합니다:
1. **Build**: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **Test**: `go test -v -race ./pkg/proc/... ./pkg/engine/... ./test/e2e/...` (100% PASS)
3. **Commit**: Conventional Commits 포맷 및 제로 데코레이티브 이모지 (`[RULE:StyleIntegrity]`)
