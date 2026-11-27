# LionSQL 심층 코드 감사 안정화 종합 계획 (Round 3)

> **문서 식별자**: `GATEKEEPER_AND_EXECUTION_STABILIZATION_PLAN.ko.md`  
> **기준 커밋**: `d2656a3f` (`feat(planner): harden validator gatekeeper with Round 2 semantic invariants`)  
> **작성 일자**: 2026-11-27  
> **관련 규칙**: `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`, `[RULE:PipelineVerification]`, `[RULE:RadicalKISS]`, `[RULE:ExplicitError]`

---

## 1. 개요 및 배경

최신 HEAD 커밋(`d2656a3f`)에 대한 심층 정적 코드 리뷰 결과, Round 2에서 강화된 Validator의 호출 범위 누락, 서브쿼리 와일드카드 전개 사각지대, 유니코드 ESCAPE 바이트 길이 판정 오류, DML/MERGE의 게이트키퍼 우회, 그리고 실행 엔진(External Sort Decimal 직렬화, Hash Join 메모리 예산 추정)에서 런타임 데이터 오염 및 크래시를 유발할 수 있는 결함들이 식별되었습니다.

본 계획은 식별된 모든 결함을 P1(치명적 무결성 결함) 및 P2(시맨틱/아키텍처 불일치)로 분류하고, 5단계 페이즈로 나누어 완벽하게 해결하는 것을 목표로 합니다.

---

## 2. 결함 분류 및 심각도 매트릭스

| ID | 심각도 | 영역 | 결함 내용 | 영향 |
| :--- | :---: | :--- | :--- | :--- |
| **P1-1** | 🔴 P1 | Execution | External Sort에서 `storage.Decimal` 타입 직렬화 누락으로 디스크 스필 시 문자열로 변환되어 정렬 순서 왜곡 | **Silent Data Corruption (정렬 왜곡)** |
| **P1-2** | 🔴 P1 | Validator | Division by zero 검사가 `GROUP BY`, `JOIN ON`에 적용되지 않고 누락 | 런타임 VDBE 평가 중 패닉 또는 비정상 결과 |
| **P1-3** | 🔴 P1 | Validator | Subquery Wildcard Arity(`SELECT *`)가 JOIN 및 파생 테이블(Derived Table)의 출력 컬럼수를 합산하지 못함 | 다중 컬럼 서브쿼리가 스칼라 위치에 잘못 통과 (`SQLSTATE 42601` 우회) |
| **P1-4** | 🔴 P1 | Validator | `MERGE` 구문이 DML 시맨틱 게이트키퍼를 우회하여 잘못된 컬럼/조건이 사전 검증되지 않음 | 미존재 유령 컬럼이 행 조건에 따라 런타임 침묵 통과 |
| **P1-5** | 🔴 P1 | Validator | DML(`UPDATE`, `DELETE`) 내부 상관 서브쿼리 검증 시 DML 대상 테이블의 부모 스코프가 전달되지 않음 | `users.id` 등 상위 DML 컬럼 참조 시 잘못된 거부 발생 |
| **P2-1** | 🟡 P2 | Validator | `LIKE ... ESCAPE` 검사에서 바이트 길이(`len(s)`)를 사용하여 멀티바이트 유니코드 1글자(`§`)가 거부됨 | 비ASCII 유니코드 이스케이프 문자 오탐 거부 |
| **P2-2** | 🟡 P2 | Validator | `HAVING` 절 별칭 해석 시 실제 테이블 컬럼과의 우선순위 불일치 (Table Column Primacy 위반) | 컬럼명과 별칭이 동일할 때 실제 컬럼 대신 집계 별칭으로 잘못 매핑 |
| **P2-3** | 🟡 P2 | Validator | 중첩 파생 테이블에서 Qualified Wildcard(`x.*`) 사용 시 Open 상태가 유실되어 바깥 쿼리가 오탐 거부 | 동적 파생 테이블 조인 시 컬럼 미존재 오판 |
| **P2-4** | 🟡 P2 | Validator | 부모 스코프가 존재하는 빈 자식 스코프(`SELECT (SELECT ghost_col) FROM users`)에서 컬럼 검증 건너뜀 | 미존재 컬럼이 부모 스코프 미확인으로 통과 |
| **P2-5** | 🟡 P2 | Validator | `UPDATE users SET age = users.age + 1` 등 DML에서 테이블 한정 컬럼명이 정규화 오류로 거부될 위험 | 테이블 한정 컬럼 문법 오탐 거부 |
| **P2-6** | 🟡 P2 | Binder | 파생 테이블(`FROM (SELECT ...) d`)이 Binder에서 바인딩되지 않아 내부 스키마 및 슬롯 오프셋이 -1/Unknown으로 방치 | Bound AST 파이프라인 단절 |
| **P2-7** | 🟡 P2 | Binder | 동일 `Binder` 인스턴스 재사용 시 `ScopeContext` 상태가 리셋되지 않고 누적 | 다중 쿼리 실행 시 이전 쿼리의 테이블/별칭 오염 |
| **P2-8** | 🟡 P2 | Execution | Hash Join Build 메모리 추정 시 Go Map 및 버킷 오버헤드 미반영으로 `work_mem` 초과 가능 | OOM 발생 위험 |
| **P2-9** | 🟡 P2 | Execution | Top-N Sort에서 `work_mem` 초과 시 메모리 제한 및 스필 전환 부재 | 대용량 LIMIT 쿼리 시 메모리 급증 |

---

## 3. 단계별 실행 계획 (5-Phase Roadmap)

### Phase 1: Validator Semantic Gatekeeper 완성 (P1-2, P1-3, P2-1, P2-2)
1. **Division by zero 호출 통일 (P1-2)**:
   - `ValidateCommonFilterExpr` 내부에 `ValidateConstantDomainBounds`를 기본 포함하여 `WHERE`, `HAVING`, `JOIN ON`에 단일 일관 적용.
   - `validateGroupByClause` 내부 표현식에 `ValidateConstantDomainBounds` 호출 추가.
   - `Projections`, `WHERE`, `HAVING`, `ORDER BY`의 중복 호출 정리.
2. **Subquery Wildcard Arity 계산 정밀화 (P1-3)**:
   - `ValidateSubqueryExprs`에서 단순히 `From.Name` 단일 테이블만 조회하던 로직을 개선.
   - 서브쿼리의 `From` 및 `Joins`를 포함한 전체 프로젝션 컬럼 수를 `deriveSubquerySchema` 또는 테이블 스코프를 활용해 전수 합산.
   - `SELECT (SELECT * FROM t1 JOIN t2 ON t1.id = t2.id)` 케이스에서 실제 출력 컬럼 수(1 + 1 = 2)를 정확히 계산하여 1개 초과 시 `SQLSTATE 42601` 차단.
3. **Unicode ESCAPE 문자열 판정 수정 (P2-1)**:
   - `len(s) != 1` ➔ `utf8.RuneCountInString(s) != 1`로 교체 (`unicode/utf8` 활용).
   - 0개 룬 ➔ `SQLSTATE 2200C`, 2개 이상 룬 ➔ `SQLSTATE 22019`.
   - `ValidateCommonFilterExpr`와 `ValidateConstantDomainBounds`의 중복 검증 로직 단일화.
4. **HAVING 절 Table Column Primacy 강제 (P2-2)**:
   - `HAVING` 절 검증 시 `projections` 별칭을 먼저 검색하지 않고, 현재 스코프의 물리 테이블 컬럼을 먼저 확인.
   - 물리 컬럼이 존재하면 해당 컬럼으로 확정(비집계 상태면 `SQLSTATE 42803` 발생).
   - 물리 컬럼이 존재하지 않는 경우에만 프로젝션 별칭으로 폴백하여 집계 여부 검증.

---

### Phase 2: DML & 스코프 정합성 완성 (P1-4, P1-5, P2-3, P2-4, P2-5)
1. **MERGE 구문 시맨틱 사전 검증 구현 (P1-4)**:
   - `pkg/planner/validator.go`의 `validateMergeStmt()` 완성:
     - Target 테이블 및 Source(테이블/서브쿼리) 존재성 확인.
     - `ON` 조건 표현식 스코프 및 컬럼 유효성 검증.
     - `WHEN MATCHED THEN UPDATE SET col = val` 대상 컬럼 존재 및 우변 표현식 검증.
     - `WHEN NOT MATCHED THEN INSERT (cols...) VALUES (vals...)` 컬럼 수 일치 및 타입 검증.
   - `execMerge()` 진입 전 `ValidateDMLStmt()`를 호출하여 데이터 유무와 무관하게 컴파일 타임 거부.
2. **DML 상관 서브쿼리 부모 스코프 연결 (P1-5)**:
   - DML 타깃 테이블(`s.Table`, `s.Alias`)을 `parentScope`로 등록하는 `NewTableScope()` 인스턴스 생성.
   - `ValidateSubqueryExprsWithParent(expr, schemas, parentScope)`를 호출하여 `UPDATE users SET age = (SELECT ... WHERE o.user_id = users.id)` 정상 바인딩.
3. **중첩 파생 테이블 Qualified Wildcard(`x.*`) Open 전파 (P2-3)**:
   - `deriveSubquerySchema`에서 `alias.*` 처리 시 소스 파생 테이블이 `IsOpen` 상태이면 결과 스키마도 `IsOpen = true`를 보존.
   - JOIN 파생 테이블 등록 시에도 `IsOpen` 상태를 누락 없이 상위 스코프로 전파.
4. **부모 스코프 기반 빈 자식 스코프 컬럼 검증 (P2-4)**:
   - `HasSchemas()`가 `s.parent != nil && s.parent.HasSchemas()`까지 재귀 확인하도록 보강.
   - `SELECT (SELECT ghost_col) FROM users`에서 내부 서브쿼리에 FROM이 없더라도 부모 스코프를 조회하여 `ghost_col` 부존재 시 `SQLSTATE 42703` 차단.
5. **DML 한정 컬럼명(`users.age`) 정상 해석 (P2-5)**:
   - DML 표현식 검증 시 ad-hoc 문자열 접두사 제거 대신, 타깃 테이블이 등록된 `TableScope`를 통해 Qualified Column(`users.age`)을 정규 해석.

---

### Phase 3: Binder 무결성 및 인스턴스 격리 (P2-6, P2-7)
1. **Binder 파생 테이블(Derived Table) 출력 스키마 바인딩 (P2-6)**:
   - `stmt.From.Subquery != nil` 또는 `j.Table.Subquery != nil`인 경우:
     - 내부 서브쿼리를 `b.bindSelectInternal(subquery)`로 재귀 바인딩.
     - 반환된 `BoundSelectStmt.Projections`의 타입과 별칭을 추출하여 파생 테이블의 `storage.TableSchema`를 동적 생성.
     - 부모 스코프의 `BoundTableRef.Schema`에 주입하고, 부모 쿼리의 `d.id`가 정상적인 `ColumnIdx`, `TupleSlot`, `DataType`을 갖도록 확정.
2. **Binder 인스턴스 재사용 격리 (P2-7)**:
   - `b.BindSelect(stmt)` 최상위 진입 시 항상 `b.current = newScopeContext(nil)`로 루트 스코프를 초기화하여 이전 쿼리의 상태 누수 완전 방지.

---

### Phase 4: 실행 엔진 안정화 (P1-1, P2-8, P2-9, Subquery)
1. **External Sort의 `storage.Decimal` 직렬화 코덱 구현 (P1-1 - Critical)**:
   - `pkg/execution/sort.go`의 `encodeSpillTuple()` / `decodeSpillTuple()`에 `storage.TypeDecimal` 전용 태그(`0x07` 등) 추가.
   - Decimal의 `UnscaledValue` (BigInt 바이트 배열) 및 `Scale` (int32)을 바이너리로 정확히 보존하여 복원.
   - 디스크 스필 시에도 소수점/정밀도 손실이나 문자열 왜곡 없이 완전한 숫자 비교 보장.
2. **Hash Join 메모리 추정치 현실화 (P2-8)**:
   - `EstimateTupleSize` 외에 Map 엔트리 오버헤드, 키 문자열, 슬라이스 헤더 등을 포함하는 현실적 가중치(오버헤드 팩터 또는 기본 엔트리 바이트) 추가.
3. **Top-N Sort 메모리 가드레일 (P2-9)**:
   - `topNHeap` 크기가 `workMem`을 초과할 경우 안전하게 외부 정렬로 전환하거나 방어적 에러 반환.

---

### Phase 5: 종합 검증 및 회귀 테스트 (Verification Pipeline)
1. **신규 TDD 단위 테스트 구축**:
   - `TestValidator_DivisionByZero_GroupByAndJoinOn`
   - `TestValidator_SubqueryWildcardArity_JoinAndDerivedTable`
   - `TestValidator_LikeEscape_UnicodeRune`
   - `TestValidator_Having_TableColumnPrimacyCollision`
   - `TestValidator_Merge_PreExecutionValidation`
   - `TestValidator_DML_CorrelatedSubqueryParentScope`
   - `TestBinder_DerivedTable_OutputSchemaAndSlots`
   - `TestBinder_InstanceReuse_Isolation`
   - `TestExecution_ExternalSort_DecimalSpillIntegrity`
2. **3-Stage Pipeline 검증**:
   - Stage 1 (Build): `go build -buildvcs=false -tags wiredtiger -o bin/lionsql ./cmd/lionsql`
   - Stage 2 (Race Test): `go test -v -race -tags wiredtiger ./pkg/planner/... ./pkg/execution/... ./pkg/engine/...`
   - Stage 3 (Commit & Push): Conventional Commits 준수, 이모지 없는 정직한 엔지니어링 커밋.

---

## 4. 백로그 동기화 계획

- **Sprint DB Task ID**: `AUDIT-STABILIZATION-ROUND3-1`
- **우선순위**: P1
- **마일스톤**: `v0.95.0`
- **도메인**: `Integrity`
- **추적 파일**: `TASKS.md`, `./bin/sprint`
