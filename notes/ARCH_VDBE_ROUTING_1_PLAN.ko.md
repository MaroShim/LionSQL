# ARCH-VDBE-ROUTING-1 아키텍처 정규화 계획서 (`notes/ARCH_VDBE_ROUTING_1_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-04
> - **과제 식별 코드**: `ARCH-VDBE-ROUTING-1`
> - **진행 상태**: 구현 착수 (In Progress)
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

## 2. 작업 상세 계획

### Step 1: 세션 방언 `Dialect` Enum 도입 (`pkg/engine/session.go`)
- `type Dialect int`
  - `DialectDefault Dialect = 0` (기본/T-SQL)
  - `DialectTSQL Dialect = 1`
  - `DialectPostgres Dialect = 2`
  - `DialectMySQL Dialect = 3`
- `ClientSession` 구조체에 `Dialect Dialect` 필드 추가
- 헬퍼 메서드 제공:
  - `cs.IsPostgres() bool` -> `cs != nil && cs.Dialect == DialectPostgres`
  - `cs.IsTSQL() bool` -> `cs == nil || cs.Dialect == DialectTSQL || cs.Dialect == DialectDefault`

### Step 2: 코드베이스 전반의 `strings.HasPrefix(cs.ID, "pg-")` 전면 교체
- `pkg/wire/server.go`: 새 세션 생성 시 `sess.Dialect = engine.DialectPostgres` 지정
- `pkg/wire/tds/server.go`: 새 세션 생성 시 `sess.Dialect = engine.DialectTSQL` 지정
- `pkg/engine/sqlexec.go`, `sqlexec_dml.go`, `sqlexec_select.go`, `session.go`:
  - `strings.HasPrefix(cs.ID, "pg-")`를 `cs.IsPostgres()`로 전면 교체

### Step 3: `proc.QueryCharacteristics`에 `HasDerivedTable` 추가 및 문자열 슬라이싱 제거
- `pkg/proc/characteristics.go`: AST 검사 시 `FROM (SELECT ...)` 파생 테이블 감지 필드 `HasDerivedTable bool` 추가
- `pkg/wire/server.go`:
  - `canCompileToVDBE()`에서 `sqlutil.FindAllTopLevelKeyword` 문자열 슬라이싱 코드 삭제
  - `if qc.HasJoin || qc.HasSubquery || qc.HasDerivedTable { return false }` 단일 AST 필드로 정규화

### Step 4: 3-Stage 검증 파이프라인
1. `go build` 컴파일 무결성 검증
2. `go test -race ./...` 100% 통과 검증
3. `TODO.md`에 `ARCH-VDBE-ROUTING-1` 완료(`[x]`) 반영 및 커밋
