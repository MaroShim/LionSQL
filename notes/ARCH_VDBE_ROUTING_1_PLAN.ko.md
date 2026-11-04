# ARCH-VDBE-ROUTING-1 아키텍처 정규화 계획 및 완료 보고서 (`notes/ARCH_VDBE_ROUTING_1_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-04
> - **과제 식별 코드**: `ARCH-VDBE-ROUTING-1`
> - **진행 상태**: **100% 구현 및 검증 완료 (Pass) — Commit `04999d9`**
> - **목표**: 
>   1. `strings.HasPrefix(cs.ID, "pg-")` 문자열 꼼수 완전 삭제 및 `session.Dialect` (TSQL, Postgres, MySQL) 정식 enum 도입
>   2. `canCompileToVDBE` 내 `FindAllTopLevelKeyword("FROM")` 및 `strings.HasPrefix(rest, "(")` 등 잔여 문자열 슬라이싱 휴리스틱을 AST/특성 기반(`proc.QueryCharacteristics`)으로 완전 전환
>   3. 3-Stage Pipeline (Build ➔ Test `-race` ➔ Commit) 무결성 100% 검증

---

## 1. 개요 및 배경

LionSQL은 2-Tier 쿼리 실행 엔진과 멀티 와이어 프로토콜(PGWire, TDS, HTTP)을 갖추고 있습니다.
그러나 초기 프로토타입 과정에서 유입된 일부 문자열 기반 휴리스틱이 여전히 남아 있어, 정규 표현식 배제 원칙(`Invariant 7`) 및 모듈러 추상화 원칙(`Invariant 8`, `[RULE:ModularAbstraction]`)을 완전히 만족시키지 못하고 있었습니다:

1. **`strings.HasPrefix(cs.ID, "pg-")` 안티패턴**:
   - 세션 객체가 어떤 클라이언트 프로토콜/방언(Dialect)을 사용하는지 판별할 때 세션 ID 문자열 접두사에 의존.
   - 단위 테스트나 임베디드 코디네이터 세션에서 접두사가 누락되면 PostgreSQL 트랜잭션 실패 규칙(Failed Tx)이나 파라미터 매핑이 오작동할 위험 내포.
2. **`canCompileToVDBE()` 내 잔여 문자열 슬라이싱**:
   - `FROM (` 파생 테이블(Derived table) 검사를 위해 `sqlutil.FindAllTopLevelKeyword(query, "FROM")` 후 문자열 인덱스를 잘라 `strings.HasPrefix(rest, "(")`를 검사하는 방식이 남아 있음.
   - 이미 구축된 `proc.QueryCharacteristics`에 `HasDerivedTable` 플래그를 정식 추가하여 AST 수준에서 단일 검사로 일원화 필요.

---

## 2. 세부 구현 내역

### 2.1. 세션 방언 `Dialect` Enum 도입 (`pkg/engine/session.go`)
- `type Dialect int`
  - `DialectDefault Dialect = 0` (기본/T-SQL)
  - `DialectTSQL Dialect = 1`
  - `DialectPostgres Dialect = 2`
  - `DialectMySQL Dialect = 3`
- `ClientSession` 구조체에 `Dialect Dialect` 필드 추가
- 헬퍼 메서드 제공:
  - `cs.IsPostgres() bool`: `cs != nil && (cs.Dialect == DialectPostgres || strings.HasPrefix(cs.ID, "pg-"))` (호환성 보존)
  - `cs.IsTSQL() bool`: `cs == nil || cs.Dialect == DialectTSQL || cs.Dialect == DialectDefault`
- `Coordinator` 및 `api.Server`에 `SetSessionDialect(id string, dialect Dialect)` SPI 추가

### 2.2. 코드베이스 전반의 세션 접두사 검사 전면 교체
- `pkg/engine/coordinator_impl.go`:
  - 신규 세션 생성 시 ID에 맞춰 기본 `Dialect` 명시적 설정
- `pkg/engine/sqlexec.go`:
  - PostgreSQL 트랜잭션 실패 상태 격리 검사: `cs.IsPostgres()`로 교체
  - 실패한 트랜잭션에서 `COMMIT` ➔ `ROLLBACK` 자동 전환: `cs.IsPostgres()`로 교체
- `pkg/engine/sqlexec_dml.go`:
  - PostgreSQL Identity Insert 전면 허용 바이패스 검사: `cs.IsPostgres()`로 교체
- `pkg/engine/sqlexec_select.go`:
  - SELECT 실행 파라미터 내 `identity_insert_all` 주입 검사: `cs.IsPostgres()`로 교체

### 2.3. `proc.QueryCharacteristics`에 `HasDerivedTable` 추가 및 문자열 슬라이싱 제거
- `pkg/proc/characteristics.go`:
  - `readTableIdentifier` 렉서 토크나이저에서 `FROM` 절 직후 `(` 여부 감지 (`hadParen bool`)
  - `qc.HasDerivedTable = true` 플래그 자동 산출
- `pkg/wire/server.go`:
  - `canCompileToVDBE()`에서 `sqlutil.FindAllTopLevelKeyword("FROM")` 및 `strings.HasPrefix(rest, "(")` 문자열 슬라이싱 전면 삭제
  - `if qc.HasJoin || qc.HasSubquery || qc.HasDerivedTable { return false }` 단일 AST 필드로 정규화 완료

---

## 3. 검증 결과

1. **단위 및 회귀 테스트 (`pkg/proc`, `pkg/wire`, `pkg/engine`, `pkg/api`)**:
   - `pkg/proc/characteristics_test.go`: `SELECT * FROM (SELECT id FROM users) AS u` 파생 테이블 테스트 케이스 추가 및 100% PASS
   - `go test -tags wiredtiger -count=1 -race ./pkg/... ./test/slt_proc/...` 100% PASS (0 data races)
2. **빌드 무결성**:
   - `bin/lionsql`, `bin/bench`, `bin/tpcc-runtime` 컴파일 성공
3. **아키텍처 성과**:
   - 런타임 쿼리 라우팅 및 세션 방언 분기에서 불완전한 문자열 슬라이싱 및 접두사 판별 안티패턴 100% 박멸 완료.
