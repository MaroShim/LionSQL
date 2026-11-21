# 스펙 기반 쿼리 생성·불일치 발견·원인 추적 및 TDD Red 등록 방법론 계획서 (`SPEC-DRIVEN-TEST-DISCOVERY-METHODOLOGY-1`)

## 1. 개요 및 패러다임 전환 (Executive Summary)

### 1.1 배경 및 한계
LionSQL은 그동안 광범위한 검증 체계를 구축하여 안정성을 증명해 왔습니다:
1. **단위 및 통합 테스트**: 엔진 내부 서브시스템별 기능 및 에러 처리 검증.
2. **크래시 복구 테스트 (`cmd/crashtest`)**: 비정상 강제 종료(`kill -9`), WAL torn frame 복구 및 원자성 검증.
3. **코드 리뷰 기반 결함 회귀 테스트**: P1/P2 감사 항목 및 보안/동시성 결함 1:1 회귀 방지.
4. **외부 코퍼스 테스트 (`test/corpora/`, `test/slt/`)**: SQLite SQLLogicTest 107개 전수 검증, DuckDB/PostgreSQL 오픈소스 코퍼스 검증.

그러나 이러한 방식은 **"이미 작성되어 고정된 테스트 스위트"**의 범위를 벗어나지 못합니다. RDBMS 엔진의 가장 치명적인 결함(Silent Data Corruption, 잘못된 3가 논리 평가, 서브쿼리 비상관 강제화 오류, 가변 길이 인라인 컬럼 확장 불일치 등)은 **SQL 스펙의 직교적 조합(Orthogonal Combinations)**에서 발생합니다.

### 1.2 핵심 방법론 정의
본 계획서는 **"스펙 기반 쿼리 자동 생성 ➔ 기대값과의 불일치(Discrepancy) 감지 ➔ 소스코드 파이프라인 원인 추적(RCA) ➔ 실패하는 TDD 테스트(Red Phase) 공식 등록"**을 지속적으로 순환 수행하는 엔지니어링 방법론을 정립합니다.

> **핵심 원칙: Red(발견 및 등록)와 Green(해결)의 완전한 라이프사이클 분리**
> - **발견자(Discovery Runner)**: 쿼리 생성, 최소화, 원인 분석, 실패 테스트 및 스프린트 결함 등록까지만 수행하며, 절대로 발견 시점에 코드를 임의 수정하지 않습니다.
> - **해결자(Resolution Sprint)**: 등록된 결함 백로그를 바탕으로 아키텍처 일관성을 유지하며 집중적으로 코드 수정 및 Green을 달성합니다.

```
+─────────────────────────────────────────────────────────────────────────────+
|               스펙 기반 지속적 결함 탐색 루프 (Spec Discovery Loop)            |
+─────────────────────────────────────────────────────────────────────────────+
|                                                                             |
|  [Step 1: Spec Modeling & Generation]                                       |
|   Formal SQL Specs (ANSI SQL-92/99, T-SQL, PGWire/Catalog)                  |
|   ├── Grammar Rules & AST Production Rules                                  |
|   ├── Type Matrix (Boundary, Nullability, Collation)                        |
|   └── Algebraic Identities & Logic Rules                                    |
|          │                                                                  |
|          ▼                                                                  |
|  [Step 2: Dual-Engine & Metamorphic Oracle Execution]                       |
|   ├── In-Memory OCC vs WiredTiger MVCC Parity (Cross-Engine Differential)   |
|   ├── Metamorphic Relations (NoREC, Non-optimizing Reference Equality)      |
|   └── External Truth Oracle (psql / SQLite / DuckDB AST Parity)             |
|          │                                                                  |
|          ▼ Discrepancy Found (결함/불일치 발견)                             |
|  [Step 3: Delta Debugging & Root Cause Analysis (RCA)]                      |
|   ├── 쿼리 최소화 (Minimal Reproducible Query)                               |
|   └── LionSQL 파이프라인 정밀 진단                                           |
|       (Lexer ➔ Parser ➔ AST ➔ VDBE Compiler ➔ VM Opcode ➔ Storage)         |
|          │                                                                  |
|          ▼                                                                  |
|  [Step 4: TDD Red Phase Registration (No Immediate Code Fix)]               |
|   ├── 독립 격리 단위 테스트 생성 (`pkg/engine/*_spec_defect_test.go`)       |
|   ├── SQLite Sprint DB & TASKS.md Dual-Sync 등록 (`DEFECT-<DOMAIN>-<ID>`)   |
|   └── 재현 쿼리, 스펙 위반 근거, 원인 코드 위치(라인) 완전 문서화             |
|                                                                             |
+─────────────────────────────────────────────────────────────────────────────+
                                   │
                                   ▼ (별도 일정으로 이관)
              [Resolution Sprint: TDD Green 코드 수정 및 회귀 고정]
```

---

## 2. 4단계 순환 라이프사이클 상세 설계

### 2.1 Step 1: 스펙 모델링 및 쿼리 생성 엔진 (Spec-Driven Generative Engine)

단순 랜덤 문자열 퍼징이 아닌, **SQL 문법 사양(Context-Free Grammar)과 의미론적 제약(Semantics)을 모델링한 생성기**를 구축합니다.

#### 5대 집중 생성 도메인
1. **식 및 3가 논리 (Expressions & 3-Valued Logic - 3VL)**:
   - `AND`, `OR`, `NOT`, `IS NULL`, `IS NOT NULL`, `UNKNOWN` 결합.
   - 단락 평가(Short-circuit evaluation) 시 부작용 함수 및 서브쿼리 평가 순서.
   - 연산자 우선순위(Precedence) 및 괄호 중첩(Nested Dyck Language).
2. **타입 경계 및 암시적/명시적 변환 (Type System & CAST Matrix)**:
   - 정수 경계: `TINYINT` (0~255), `SMALLINT` (-32,768~32,767), `INT` (-2^31~2^31-1), `BIGINT`.
   - 부동소수점 및 정밀도: `DECIMAL(p, s)` 반올림/잘림, `REAL`, `FLOAT(53)`, `NaN`/`Infinity` 거부.
   - 날짜 및 시간: 윤년(Leap Year), 세기말(2000-02-29 vs 1900-02-28), 월말 `DATEADD` 일자 클램핑, 타임존 오프셋.
   - 가변 길이 문자열: `VARCHAR(n)` 초과 시 엄격 모드 잘림 에러 vs 허용, 공백 패딩.
3. **복합 조인 및 CBO 옵티마이저 경로 (Join & CBO Plan Selection)**:
   - `INNER`, `LEFT OUTER`, `RIGHT OUTER`, `FULL OUTER`, `CROSS JOIN`, `CROSS APPLY`, `OUTER APPLY`.
   - `WHERE` 조건에 의한 Outer Join의 Inner Join 단순화(Simplification) 정확성.
   - 조인 조건 내 서브쿼리 및 복합 표현식 (`ON a.id = b.id + 1 AND (b.val IS NULL OR ...)`).
4. **상호연관 서브쿼리 및 집계 (Correlated Subqueries & Aggregates)**:
   - 상관 서브쿼리(`WHERE x = (SELECT max(y) FROM t2 WHERE t2.k = t1.k)`).
   - `EXISTS` 및 `NOT EXISTS`의 3VL 무관성(NULL 존재 시 참 판정).
   - `IN (SELECT ...)`의 NULL 포함 행 처리 (SQL-92 Invariant).
   - `GROUP BY`, `HAVING`, `DISTINCT`와 윈도우 함수(`OVER (PARTITION BY ... ORDER BY ...)`)의 조합.
5. **DDL 변경 및 트랜잭션 불변성 (DDL Mutation & Transaction Isolation)**:
   - `ALTER TABLE ADD/DROP COLUMN` 직후의 튜플 인코딩/디코딩 정합성.
   - 트랜잭션 내 DDL 롤백 시 스키마 카탈로그와 스토리지 동기화 원자성.
   - VDBE 저장 프로시저 내부에서의 임시 테이블 및 테이블 변수 상호작용.

---

### 2.2 Step 2: 오라클 판정 및 불일치 감지 (Oracle Verification & Discrepancy Detection)

생성된 쿼리가 올바른 결과를 내는지 검증하기 위한 4중 오라클 체계를 운용합니다.

#### 오라클 1: 이중 스토리지 엔진 차등 검증 (In-Memory OCC vs WiredTiger MVCC)
- **원리**: LionSQL은 동일한 Volcano 실행기 및 VDBE 가상머신 하에서 두 개의 독립 스토리지 커널(In-Memory OCC / WiredTiger B+Tree)을 제공합니다.
- **판정 기준**: 동일한 DDL/DML 시퀀스를 두 엔진에 각각 실행했을 때, 반환되는 행의 개수, 컬럼 타입, 값, 정렬 순서가 1비트의 오차도 없이 100% 동일해야 합니다.
- **효과**: 어느 한쪽 스토리지의 디코딩 버그, 인덱스 커서 경계 오류, 트랜잭션 롤백 누락을 즉시 감지.

#### 오라클 2: 비최적화 참조 등가성 (NoREC - Non-optimizing Reference Engine Construction)
- **원리**: 옵티마이저가 최적화(Index Scan, Predicate Pushdown)를 수행하는 쿼리와, 인덱스를 타지 않고 순수 전체 스캔으로 1행씩 조건을 직접 평가하는 등가 쿼리를 비교.
- **등가 관계식**:
  $$\text{Query A: } \text{SELECT * FROM T WHERE } \langle\text{Expr}\rangle;$$
  $$\text{Query B: } \text{SELECT * FROM T WHERE } (\text{CASE WHEN } \langle\text{Expr}\rangle \text{ THEN 1 ELSE 0 END}) = 1;$$
- **판정 기준**: Query A와 Query B의 결과셋 크기와 내용이 반드시 일치해야 함. 다르면 CBO 옵티마이저의 조건식 단순화나 인덱스 푸시다운 결함.

#### 오라클 3: 대수적 불변성 검증 (Metamorphic Invariants)
- **교환법칙**: $A \bowtie B$와 $B \bowtie A$의 결과셋(정렬 무관)은 동일해야 함.
- **분배법칙 & 여집합**:
  $$\text{COUNT}(*) = \text{COUNT}(\text{WHERE } P) + \text{COUNT}(\text{WHERE NOT } P) + \text{COUNT}(\text{WHERE } P \text{ IS NULL})$$
- 총 행수와 분할 조건 카운트의 합이 일치하지 않으면 3가 논리 연산자 결함.

#### 오라클 4: 외부 레퍼런스 차등 검증 (External Reference DB Oracle)
- ANSI/PostgreSQL 표준 쿼리에 대해 SQLite / PostgreSQL / DuckDB를 오라클로 활용하여 결과셋 비교.

---

### 2.3 Step 3: 쿼리 최소화 및 엔진 원인 추적 (Delta Debugging & Engine RCA)

불일치가 발견되면 수십 줄의 거대한 쿼리로는 원인을 찾기 어렵습니다. 따라서 **자동 최소화(Delta Debugging)** 후 엔진 내부 파이프라인을 체계적으로 추적합니다.

#### 1) 델타 디버깅 (Query Minimization)
- 결함을 재현하는 최소한의 쿼리로 축소:
  1. 테이블 컬럼 수 제거 ($10 \rightarrow 2$).
  2. 불필요한 `JOIN`, `ORDER BY`, `GROUP BY` 제거.
  3. `WHERE` 조건절의 복합 연산자를 단일 연산자로 축소.
  4. 최소 1~2행의 데이터로 재현 가능한 독립 스크립트 도출.

#### 2) LionSQL 7단계 파이프라인 정밀 진단
추약된 쿼리를 엔진 디버깅 모드에서 단계별로 추적합니다:
```
[Phase 1] Tokenizer/Lexer (pkg/proc/lexer.go)
          - 토큰 분리 오류, 리터럴 이스케이프 미처리, 괄호 쌍 불일치
              │
[Phase 2] Parser & Grammar (pkg/proc/parser*.go)
          - 연산자 결합 우선순위 오류, 절(Clause) 경계 파싱 오류
              │
[Phase 3] AST Lowering & Catalog Semantic Binding (pkg/proc/ast.go, pkg/engine/sqlexec.go)
          - 컬럼/테이블 식별자 대소문자 매핑, 타입 검증 누락, 암시적 CAST 누락
              │
[Phase 4] VDBE Bytecode Compiler / CBO Planner (pkg/proc/compiler.go, pkg/execution/planner.go)
          - 잘못된 OpCode 생성, 레지스터 할당 충돌, 조인 조건 푸시다운 왜곡
              │
[Phase 5] VDBE Bytecode Execution Loop (pkg/proc/runtime/vm*.go)
          - 가상머신 OpCode 연산 오작동, 슬롯 인덱싱 오버플로우, NULL 비트맵 처리 오류
              │
[Phase 6] Volcano Execution Iterators (pkg/execution/*.go)
          - HashJoin, NestedLoop, Aggregate, Filter 이터레이터 경계 조건 결함
              │
[Phase 7] Storage Kernel Layout (pkg/storage/memengine, wiredtiger)
          - V2 바이너리 튜플 디코딩 오프셋 오류, 보조 인덱스 키 정렬 순서 오류
```

---

### 2.4 Step 4: TDD Red Phase 공식 등록 (Defect Registration)

원인이 규명된 결함은 **절대로 즉시 코드를 수정하지 않고**, TDD의 'Red' 상태로 공식 등록하여 안전한 백로그로 고정합니다.

#### 1) 격리된 실패 단위 테스트 작성 (`pkg/engine/spec_defect_<domain>_test.go`)
- 기존 빌드와 CI를 깨뜨리지 않으면서 결함을 증명하는 정규 Go 테스트 작성.
- 표준 등록 템플릿:
```go
// TestSpecDefect_3VL_NullInComparison verifies that NULL IN (1, NULL) evaluates to UNKNOWN.
// Spec: ANSI SQL-92 Section 8.4 <in predicate>
// Root Cause: pkg/proc/eval.go:124 evaluates IN list with strict equality instead of 3-valued logic.
func TestSpecDefect_3VL_NullInComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("TDD RED: DEFECT-ENGINE-3VL-IN-1 - known issue pending resolution sprint")
	}
	// 최소 재현 쿼리 실행 및 기대값 단언 (현재 상태에서는 실패함)
	...
}
```

#### 2) SQLite Sprint DB & `TASKS.md` Dual-Sync 등록 (`[RULE:ContinuousBacklog]`)
- 식별자 규칙: `DEFECT-<DOMAIN>-<NAME>-<SEQ>`
  - 예: `DEFECT-ENGINE-3VL-NOT-IN-1`, `DEFECT-STORAGE-V2-TUPLE-OFFSET-1`, `DEFECT-WIRE-TDS-DATETIME-SCALE-1`
- 명령어 실행:
  ```bash
  ./bin/sprint add DEFECT-ENGINE-3VL-NOT-IN-1 "T-SQL 3VL: NOT IN 리스트에 NULL 포함 시 행 필터링 평가 결함" Engine 1 "notes/plans/TDS_PERFORMANCE_OPTIMIZATION_PLAN.ko.md" "1.0-GA"
  ```
- 메타데이터 필수 항목:
  - 스펙 근거 (ANSI SQL-92 / T-SQL MSDN / PG Docs 조항)
  - 최소 재현 SQL (Minimal Reproducible Query)
  - 관측된 결과 vs 기대 결과 (Actual vs Expected)
  - 원인 코드 위치 (`file:line`) 및 결함 메커니즘
  - 등록된 TDD Red 테스트 함수명

---

## 3. 역할 분담 및 워크플로우 (Red vs Green Separation)

결함 탐색과 결함 수정이 뒤섞이면 다음과 같은 치명적 안티패턴이 발생합니다:
- 한 가지 버그를 수정하느라 탐색 루프가 중단됨.
- 임시방편(Ad-hoc patch)으로 코드를 건드려 다른 스펙 회귀를 유발.
- 아키텍처 관점의 일괄 리팩토링 기회를 상실.

따라서 본 방법론은 두 역할을 철저히 분리합니다:

```
[Phase A: Red Discovery Cycle (탐색 및 등록)]
  1. Spec Fuzzer/Generator 가동
  2. 불일치(Discrepancy) 감지
  3. 최소 쿼리로 Delta 축약
  4. 원인 코드 파일/라인 식별
  5. Failing Test 작성 & sprint DB 등록 (Status: PENDING)
  6. 코드 수정 없이 다음 탐색으로 즉시 복귀! (반복)

                     ▼ (충분한 백로그 확보 후)

[Phase B: Green Resolution Sprint (집중 해결 스프린트)]
  1. sprint DB에서 연관된 DEFECT 항목들을 도메인별로 그룹핑 (예: 3VL 결함 3건 묶음)
  2. 근본 아키텍처 관점에서 단일 원칙(Radical KISS, DRY)으로 정밀 수정
  3. failing test의 t.Skip 해제 및 PASS 확인
  4. 3-Stage Pipeline (Build ➔ Test ➔ Commit) 수행
  5. sprint DB 상태 DONE 업데이트
```

---

## 4. 툴링 및 구현 아키텍처 (`cmd/spec_fuzz` & `pkg/speckit`)

### 4.1 패키지 레이아웃 제안
```
pkg/speckit/
├── grammar/          # SQL 구문 문법 규칙 정의 (CFG, AST 노드 생성기)
│   ├── expr.go       # 식, 연산자, 3가 논리 생성기
│   ├── types.go      # 타입 경계값 및 리터럴 생성기
│   ├── select.go     # SELECT, JOIN, GROUP BY, HAVING 생성기
│   └── ddl.go        # CREATE, ALTER, DROP 생성기
├── oracle/           # 오라클 판정기
│   ├── norec.go      # NoREC 비최적화 등가식 변환기
│   ├── dualengine.go # OCC vs WiredTiger 차등 비교기
│   └── external.go   # SQLite / PGWire 외부 차등 검증기
├── minimizer/        # 쿼리 축약기 (Delta Debugger)
│   └── reduce.go     # AST 트리 프루닝 및 최소 재현 쿼리 축소
└── runner/           # 탐색 러너 및 리포터
    └── loop.go       # 자동 순환 실행 및 TDD Red 템플릿 코드 생성기

cmd/spec_fuzz/
└── main.go           # CLI 실행 진입점 (옵션: -workers, -duration, -domain, -oracle)
```

### 4.2 CLI 실행 명령어 인터페이스
```bash
# 1. 3가 논리 및 식 도메인 대상 NoREC 오라클 집중 탐색
go run ./cmd/spec_fuzz -domain=expr -oracle=norec -duration=10m

# 2. In-Memory OCC vs WiredTiger 이중 스토리지 차등 비교 탐색
go run ./cmd/spec_fuzz -domain=join -oracle=dualengine -workers=8 -duration=30m

# 3. 발견된 결함에 대한 자동 쿼리 최소화 및 TDD 코드 생성
go run ./cmd/spec_fuzz -minimize -input="failing_raw.sql" -output="pkg/engine/spec_defect_3vl_test.go"
```

---

## 5. 단계별 도입 로드맵 (Execution Milestones)

| 단계 | 마일스톤 | 작업 내용 | 완료 산출물 |
|---|---|---|---|
| **Phase 1** | 아키텍처 계획 및 규칙 확립 | 본 계획서 확정, Sprint DB 등록, Red/Green 분리 원칙 공표 | `SPEC-DRIVEN-TEST-DISCOVERY-METHODOLOGY-1` |
| **Phase 2** | 스펙 코어 및 생성기 구축 | `pkg/speckit/grammar` 식/타입/조인 문법 생성기 구현 | `pkg/speckit/grammar/*.go` |
| **Phase 3** | 차등 오라클 및 델타 축약기 구현 | NoREC 변환기 및 OCC vs WiredTiger 듀얼 엔진 비교기, AST 쿼리 최소화기 구현 | `pkg/speckit/oracle/`, `pkg/speckit/minimizer/` |
| **Phase 4** | CLI 도구 및 자동 TDD 등록기 결합 | `cmd/spec_fuzz` CLI 구현, sprint DB 및 `TASKS.md` 자동 템플릿 연동 | `cmd/spec_fuzz/main.go` |
| **Phase 5** | 1차 전면 가동 및 결함 등록 스프린트 | 1차 24시간 가동, 발견된 결함 10~20건 TDD Red 등록 (코드 수정 없이 백로그 축적) | `DEFECT-*` 백로그 큐 형성 |
| **Phase 6** | Green 해결 스프린트 연계 | 축적된 백로그 도메인별 일괄 수정 및 회귀 방지 고정 | 엔진 품질 1.0-GA 수준 격상 |

---

## 6. 결론

본 방법론은 "우리가 알고 있는 지식에 의존한 수동 테스트 작성"에서 벗어나, **"형식적 스펙을 기반으로 한 자율적 결함 탐색기(Autonomous Defect Discoverer)"**를 시스템화하는 전환점입니다.

특히 **결함 탐색(Red 등록)과 결함 수정(Green)을 물리적으로 분리**함으로써, 엔진 개발자가 문제 해결에 매몰되어 테스트 범위를 좁히는 인지적 편향을 완벽히 차단하고, 엔진의 모든 모서리(Edge cases)를 전수 스캔하는 강력한 품질 방파제를 확보합니다.
