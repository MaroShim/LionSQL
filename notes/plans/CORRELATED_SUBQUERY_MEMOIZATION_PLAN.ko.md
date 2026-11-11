# 상관 서브쿼리 유계 메모이제이션 캐시 아키텍처 및 구현 계획서 (`notes/plans/CORRELATED_SUBQUERY_MEMOIZATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-11
> - **최종 갱신일**: 2026-11-11
> - **과제 식별 코드**: `PERF-SUBQUERY-MEMO-1`
> - **적용 마일스톤**: `1.0-GA` (즉시 구현 및 포함)
> - **핵심 불변식**: `[RULE:RadicalKISS]`, `[RULE:BoundedBuffers]`, `[RULE:PlanPersistence]`, [`GEMINI.md`](../../GEMINI.md) Invariant 17 (Kim's AST Decorrelation Spec-Out & Memoization Primacy)
> - **대체 대상**: 기존 복합 집계 AST 재작성 및 미니 TPC-H 스위트(`BENCH-TPCH-1`, Count Bug 위험으로 영구 Spec-Out 폐기)

---

## 1. 개요 및 배경 (Background & Problem Definition)

### 1.1. 해결하려는 문제: $O(N^2)$ 상관 서브쿼리 중첩 루프 병목
실무 비즈니스 로직(쇼핑몰, 인사/급여, 게시판 권한 등)에서 빈번하게 사용되는 상관 스칼라 서브쿼리(Correlated Scalar Subquery)는 외부 테이블의 각 행마다 서브쿼리를 반복 실행합니다:

```sql
SELECT e.emp_id, e.name, e.salary
FROM employee e
WHERE e.salary > (
    SELECT AVG(salary) 
    FROM employee 
    WHERE dept_id = e.dept_id  -- 외부 행의 dept_id 참조
);
```

- **현상**: 직원 수가 10,000명이고 부서 수가 10개인 경우, 실제로 필요한 서브쿼리 계산은 10종류뿐입니다.
- **기존 동작**: 외부 테이블 10,000행 순회마다 `SELECT AVG(salary) WHERE dept_id = ?`를 매번 실제로 실행하여 **총 10,000회 중첩 루프 실행($O(N^2)$)**이 발생하고, 쿼리가 수 초 이상 지연됩니다.

---

## 2. 왜 AST 재작성(Decorrelation) 대신 메모이제이션인가?

전통적인 옵티마이저 기법(Kim's Decorrelation)은 서브쿼리를 억지로 풀어서 `LEFT OUTER JOIN` + `GROUP BY`로 AST를 재작성합니다. 그러나 이 방식은 다음과 같은 치명적 결함을 동반합니다:

1. **치명적인 Count Bug (SQL 3VL 왜곡 결함)**:
   - 서브쿼리가 `SELECT COUNT(*) FROM ...`일 때, 매칭 행이 0개이면 원본 서브쿼리는 정수 **`0`**을 반환해야 합니다.
   - 그러나 이를 `LEFT JOIN`으로 AST 재작성하면 결과가 **`NULL`**이 되어버려, `COUNT = 0` 조건의 참/거짓 평가가 정반대로 뒤집히는 조용한 데이터 오염(Silent Data Corruption)이 발생합니다.
2. **복잡성과 버그 위험**:
   - 다중 상관 변수, 널 허용 여부, 중첩 서브쿼리 조건에 따라 수많은 예외 처리 분기가 생기며 엔진 코드가 급격히 비대해집니다.
3. **결론 (`GEMINI.md Invariant 17`)**:
   - LionSQL은 무리한 AST Decorrelation을 **영구 Spec-Out(폐기)**하고, SQL 의미론(Semantics)을 100% 보존하면서 동일한 성능 효과를 내는 **유계 메모이제이션(Bounded Memoization)**을 정석으로 채택합니다.

---

## 3. 글로벌 상용 DBMS 구현 사례 및 업계 벤치마크

이 기법은 검증된 현대 최신 RDBMS의 핵심 최적화 엔진 아키텍처와 정확히 일치합니다:

1. **PostgreSQL 14+ (`Memoize` 실행 노드)**:
   - PostgreSQL 14(2021년)의 대표 킬러 피처로 도입됨.
   - 중첩 루프 조인 및 상관 서브쿼리 경로에 `Memoize` 노드를 배치하여, 동일한 외부 파라미터가 들어올 때 하위 플랜 실행을 생략하고 캐시된 결과를 즉시 반환함 (`Hits: 9990, Misses: 10`).
2. **Microsoft SQL Server (`Lazy Spool / Index Spool`)**:
   - SQL Server 쿼리 실행 계획에서 상관 서브쿼리나 복합 스칼라 함수 평가 시 `Table Spool (Lazy Spool)` 노드를 자동으로 배치하여 중복 계산을 인메모리 버퍼에 캐싱 및 재사용함.
3. **CockroachDB (`Memoized ApplyJoin`)**:
   - 분산 환경에서 매 행마다 상관 서브쿼리를 원격 실행하는 지연시간을 방지하기 위해 `ApplyJoin` 노드 컨텍스트에 메모이제이션 캐시를 장착함.
4. **학술적 기반 (Goetz Graefe, Volcano 옵티마이저 창시자)**:
   - *"Query Evaluation Techniques for Large Databases"* (ACM Computing Surveys, 1993): 상관 서브쿼리 평가 시 쿼리 범위 내의 해시 기반 메모이제이션(Per-Query Hash Memoization)이 복잡한 쿼리 변환의 부작용을 피하는 가장 실용적이고 안전한 해법으로 규정됨.

---

## 4. Phase 1: 1.0-GA 단일 쿼리 수명주기 유계 캐시 설계

### 4.1. 3대 핵심 안전장치 (Guardrails)

1. **단일 쿼리 수명주기 완전 격리 (Per-Query Lifecycle)**:
   - 전역(Global) 캐시가 아니며, `ExecuteVolcanoSelect` 내부에서 단일 최상위 쿼리가 시작될 때 생성되고 **쿼리 완료 즉시 GC로 소멸**합니다.
   - 다른 세션/트랜잭션의 DML(UPDATE/INSERT)에 의해 캐시가 오염되는 Stale Cache 문제가 원천적으로 발생하지 않습니다.
2. **유계 버퍼 상한 (`[RULE:BoundedBuffers]`)**:
   - 고유 키(PK) 카디널리티가 높은 극단적 쿼리에서 메모리 폭주를 막기 위해 최대 엔트리 수를 **`maxSubqueryMemoEntries = 4096`**으로 엄격히 제한합니다.
   - 4,096개를 초과하면 캐시 등록만 중단하고 원본 미캐싱 실행으로 안전하게 폴백합니다.
3. **비결정적(Volatile) 함수 바이패스 가드**:
   - 매 호출마다 결과가 달라져야 하는 함수(`RAND`, `RANDOM`, `GETDATE`, `NOW`, `CURRENT_TIMESTAMP`, `SYSDATETIME`, `CLOCK_TIMESTAMP`, `NEWID`, `UUID`)가 포함된 서브쿼리 문자열은 캐싱을 건너뛰고 매번 실행합니다.

### 4.2. 상세 구현 구조 (`pkg/engine/sqlexec_select.go`)

```go
const maxSubqueryMemoEntries = 4096

type subqueryMemoEntry struct {
	rows []map[string]any
	cols []string
	err  error
}

func isVolatileSubquerySQL(sqlUpper string) bool {
	return strings.Contains(sqlUpper, "RAND(") ||
		strings.Contains(sqlUpper, "RANDOM(") ||
		strings.Contains(sqlUpper, "NEWID(") ||
		strings.Contains(sqlUpper, "UUID(") ||
		strings.Contains(sqlUpper, "GETDATE(") ||
		strings.Contains(sqlUpper, "NOW(") ||
		strings.Contains(sqlUpper, "CURRENT_TIMESTAMP") ||
		strings.Contains(sqlUpper, "SYSDATETIME(") ||
		strings.Contains(sqlUpper, "CLOCK_TIMESTAMP(")
}

// ExecuteVolcanoSelect 내부 서브쿼리 실행 래퍼
var subMemo map[string]subqueryMemoEntry

opts.SubqueryExecutor = func(paramSQL string) ([]map[string]any, []string, error) {
	sqlUpper := strings.ToUpper(paramSQL)
	if isVolatileSubquerySQL(sqlUpper) {
		return rawSubqueryExecutor(paramSQL)
	}

	if subMemo != nil {
		if hit, ok := subMemo[paramSQL]; ok {
			return hit.rows, hit.cols, hit.err
		}
	}

	rows, cols, err := rawSubqueryExecutor(paramSQL)
	if err == nil {
		if subMemo == nil {
			subMemo = make(map[string]subqueryMemoEntry, 64)
		}
		if len(subMemo) < maxSubqueryMemoEntries {
			subMemo[paramSQL] = subqueryMemoEntry{rows: rows, cols: cols, err: err}
		}
	}
	return rows, cols, err
}
```

---

## 5. Phase 2: Post-1.0 LRU Eviction & Dynamic WorkMem Spill 확장 계획 (Future Extension)

현재 1.0 사양은 극단적 단순성(`[RULE:RadicalKISS]`)을 위해 4,096개 상한 초과 시 추가 캐싱을 중단하는 정책을 채택했습니다. 

향후 엔터프라이즈 환경에서 고유 키 카디널리티(10만~100만 건 이상)를 가진 초대형 분석 상관 쿼리를 처리해야 할 경우, 다음과 같이 인터페이스를 확장할 수 있도록 설계 여지를 둡니다:

1. **Doubly-Linked List + Hash Map 기반 LRU Eviction**:
   - 4,096개 상한 도달 시 가장 오래 참조되지 않은(Least Recently Used) 엔트리를 $O(1)$로 방출하고 신규 엔트리를 수용.
2. **동적 작업 메모리(`work_mem`) 바운딩**:
   - 단순 엔트리 개수뿐만 아니라 결과셋 바이트 크기(예: 최대 4MB)를 추적하여 동적 방출 제어.
3. **선택적 디스크 스필(Spill)**:
   - 초거대 결과셋에 대해 임시 SQLite/Flat 파일 기반 스풀링 연계.

---

## 6. TDD 검증 계획 (RED ➔ GREEN ➔ `-race`)

새로 작성할 회귀 테스트 파일: `pkg/engine/subquery_memo_test.go`

1. **기본 기능 및 1,000배 가속 검증**:
   - 1,000명 직원, 5개 부서 테이블 구축.
   - `WHERE salary > (SELECT AVG(salary) FROM emp WHERE dept_id = e.dept_id)` 실행.
   - 서브쿼리 실제 실행 카운터가 1,000회가 아닌 **5회**로 급감하는지 단언(Assert).
2. **Count Bug 원천 차단 검증 (`COUNT(*) = 0` vs `NULL`)**:
   - 매칭 행이 0개인 상관 서브쿼리 `WHERE (SELECT COUNT(*) FROM child WHERE p_id = p.id) = 0` 실행.
   - `0`이 정확히 반환되어 조건이 올바르게 평가되는지 검증.
3. **비결정적 함수(Volatile) 바이패스 검증**:
   - `WHERE val > (SELECT RAND())` 실행 시 매 행마다 새로운 난수가 평가되는지 검증.
4. **4,096 상한선 유계 버퍼 검증**:
   - 고유 상관 값 5,000개 주입 후 메모리가 4,096개로 제한되고 panic/OOM 없이 완주하는지 검증.
5. **동시성 및 무결성 검증**:
   - `go test -tags wiredtiger -v -race -run TestSubqueryMemo_ ./pkg/engine/...` 100% PASS 확인.