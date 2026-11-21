# 스펙 기반 쿼리 생성·불일치 발견·원인 추적 및 TDD Red 등록 방법론 계획서 (`SPEC-DRIVEN-TEST-DISCOVERY-METHODOLOGY-1`)

## 1. 개요 및 패러다임 전환 (Executive Summary)

### 1.1 배경 및 한계
LionSQL은 그동안 다각도의 정적 검증 체계를 운용해 왔습니다:
1. **단위 및 통합 테스트**: 엔진 내부 서브시스템별 기능 및 에러 처리 검증.
2. **크래시 복구 테스트 (`cmd/crashtest`)**: 비정상 강제 종료(`kill -9`), WAL torn frame 복구 및 원자성 검증.
3. **외부 오라클 코퍼스 (`test/corpora/`, `test/slt/`)**: SQLite SQLLogicTest 107개 전수 검증, DuckDB/PostgreSQL 오픈소스 코퍼스 검증.

그러나 이러한 정적 테스트는 **"이미 작성되어 고정된 쿼리 스위트"**의 범위를 벗어나지 못합니다. RDBMS 엔진의 가장 치명적인 결함(Silent Data Corruption, 3VL 논리 누락, 부동소수점 단편화, VM 레지스터 왜곡 등)은 **SQL 스펙의 직교적 조합(Orthogonal Combinations)**에서 발생합니다.

### 1.2 핵심 방법론 정의
본 계획서는 **"스펙 기반 쿼리 자동 생성 ➔ 기대값과의 불일치(Discrepancy) 감지 ➔ 소스코드 파이프라인 원인 추적(RCA) ➔ 실패하는 TDD 테스트(Red Phase) 공식 등록"**을 지속적으로 순환 수행하는 엔지니어링 방법론을 정립합니다.

> **핵심 원칙: Red(발견 및 등록)와 Green(해결)의 완전한 라이프사이클 분리**
> - **발견자(Discovery Runner)**: 쿼리 생성, 최소화, 원인 분석, 실패 테스트 및 스프린트 결함 등록까지만 수행하며, 절대로 발견 시점에 코드를 임의 수정하지 않습니다.
> - **해결자(Resolution Sprint)**: 등록된 결함 백로그를 바탕으로 아키텍처 일관성을 유지하며 엔진 정규 타입 시스템과 표준 연산자를 통해 집중적으로 Green을 달성합니다.

```
+─────────────────────────────────────────────────────────────────────────────+
|               스펙 기반 지속적 결함 탐색 루프 (Spec Discovery Loop)            |
+─────────────────────────────────────────────────────────────────────────────+
|                                                                             |
|  [Step 1: 스펙 모델링 및 쿼리 생성]                                         |
|   - ANSI SQL-92/99, T-SQL 사양 CFG 문법 AST 합성                             |
|   - 타입 경계값, Collation, 3치 논리(3VL) 극한 조합 주입                    |
|          │                                                                  |
|          ▼                                                                  |
|  [Step 2: 오라클 판정 및 불일치 감지]                                       |
|   - NoREC 메타모픽 대사 동치성: Count(Opt) == Sum(Unopt)                   |
|   - Dual-Engine 차분 검증: In-Memory OCC vs WiredTiger MVCC Parity          |
|          │                                                                  |
|          ▼ Discrepancy Found (불일치 발견)                                  |
|  [Step 3: 델타 디버깅 및 파이프라인 원인 분석 (RCA)]                        |
|   - 1~2행 및 단일 술어로 최소 재현 쿼리 자동 축약 (Minimization)            |
|   - Lexer ➔ Parser ➔ AST ➔ VDBE Compiler ➔ VM ➔ Storage 7단계 정밀 진단    |
|          │                                                                  |
|          ▼                                                                  |
|  [Step 4: TDD Red Phase 공식 등록]                                          |
|   - 독립 격리 단위 테스트 생성 (`test/spec_defects/spec_defect_*_test.go`)   |
|   - SQLite Sprint DB & TASKS.md Dual-Sync 등록 (`DEFECT-*`)                 |
|                                                                             |
+─────────────────────────────────────────────────────────────────────────────+
                                   │
                                   ▼ (별도 일정으로 이관)
              [Resolution Sprint: TDD Green 아키텍처 정규 해결]
```

---

## 2. 핵심 이론 및 오라클 동작 원리 (Theoretical Foundations & Oracles)

### 2.1 오라클 문제(The Oracle Problem)의 극복
임의 생성 쿼리의 치명적 난제는 **"사전에 올바른 정답(Expected Ground Truth)을 어떻게 정의할 것인가"**입니다. 상용 DBMS(MSSQL, PG)를 매 쿼리마다 네트워크로 호출하는 방식은 고속 탐색(초당 10,000+ QPS)이 불가능합니다. 본 체계는 수학적 항등성과 듀얼 엔진 구조를 통해 외부 정답 소스 없이 100% 확정 판정을 내립니다.

```
+─────────────────────────────────────────────────────────────────────────────+
|               NoREC 등가성 판정 및 결함 도출 메커니즘                        |
+─────────────────────────────────────────────────────────────────────────────+
|                                                                             |
|      [무작위 술어 P 합성] ─── (3VL, Collation, 경계값, 타입 매트릭스)       |
|                 │                                                           |
|        ┌────────┴────────────────────────┐                                  |
|        ▼                                 ▼                                  |
|   [Query A: 최적화 필터 경로]     [Query B: 비최적화 프로젝션 경로]         |
|   SELECT count(*)                 SELECT sum(                               |
|   FROM T WHERE P;                   CASE WHEN P THEN 1 ELSE 0 END           |
|                                   ) FROM T;                                 |
|        │                                 │                                  |
|        │ (Volcano Filter,                │ (전체 테이블 순차 스캔,          |
|        │  Index Scan, CBO Pushdown)      │  Expression Evaluator 누적)      |
|        ▼                                 ▼                                  |
|     Count(A)                          Sum(B)                                |
|        │                                 │                                  |
|        └───────────────┬─────────────────┘                                  |
|                        ▼                                                    |
|           [수학적 항등식 검증: Count(A) == Sum(B)]                          |
|                        │                                                    |
|         ┌──────────────┴──────────────┐                                     |
|         ▼                             ▼                                     |
|       일치                          불일치 (Discrepancy Detected)           |
|    (정상 통과)                        │                                     |
|                                       ▼                                     |
|                        [Delta Debugging (1~2행 축약)]                       |
|                                       │                                     |
|                                       ▼                                     |
|                        [TDD Red 단위 테스트 자동 생성]                      |
|                                                                             |
+─────────────────────────────────────────────────────────────────────────────+
```

### 2.2 제1 오라클: NoREC (Non-optimizing Reference Engine Construction)
- **수학적 불변식**:
  $$\text{Query A: } \text{SELECT count(*) FROM T WHERE } P;$$
  $$\text{Query B: } \text{SELECT coalesce(sum(CASE WHEN } P \text{ THEN 1 ELSE 0 END), 0) FROM T;$$
- **원리**: Query A는 옵티마이저의 조건 단순화, 인덱스 푸시다운, 필터 이터레이터를 거칩니다. Query B는 필터 없이 전 행을 읽어 수식 평가기가 직접 1/0을 집계합니다.
- **판정 기준**: $\text{Count}(A) \equiv \text{Sum}(B)$가 불일치하면, 옵티마이저 조건식 왜곡, 필터 이터레이터의 타입 절삭, 3VL 단락 평가 오류가 연역적으로 입증됩니다.

### 2.3 제2 오라클: Dual-Engine Differential Oracle (듀얼 스토리지 차등 비교)
- **원리**: LionSQL의 단일 실행기 계층 아래 두 독립 스토리지 커널을 교차 검증합니다:
  1. `In-Memory OCC` (`pkg/storage/memengine`): 인메모리 슬라이스, 버전 기반 낙관적 동시성.
  2. `WiredTiger MVCC` (`pkg/storage/wiredtiger`): 디스크 B+Tree, 프리픽스 인덱스, Hazard Pointer.
- **판정 기준**: 동일 DDL/DML/SELECT 실행 시 행 개수, 컬럼 타입, 값, 정렬 순서가 비트 단위로 일치해야 함. 불일치 시 B-Tree 인덱스 프리픽스 인코딩(`EncodeIndexKeyPart`), V2 튜플 코덱 디코딩 오류로 즉시 격리.

### 2.4 제3 오라클: 대수적 불변성 (Metamorphic Invariants)
- **분배법칙 & 여집합 불변식**:
  $$\text{COUNT}(*) \equiv \text{COUNT}(\text{WHERE } P) + \text{COUNT}(\text{WHERE NOT } P) + \text{COUNT}(\text{WHERE } P \text{ IS NULL})$$
- 전체 행수와 3분할 조건 카운트의 합이 일치하지 않으면 3치 논리(3VL) 처리 결함.

---

## 3. 4단계 순환 라이프사이클 (The 4-Step Discovery Loop)

### 3.1 Step 1: 스펙 모델링 및 경계값 합성 생성
단순 텍스트 퍼징을 배제하고, SQL 문법 사양(Context-Free Grammar)과 타입 의미론을 모델링한 재귀 AST 생성기(`pkg/speckit/grammar`)를 운용합니다:
1. **타입 경계값 주입**: `0`, `-1`, `1`, `math.MinInt64`, `math.MaxInt64`, IEEE 754 부동소수점 한계값, 지수 표기법(`3.14159`).
2. **문자열 및 Collation 취약점 주입**: 빈 문자열 `''`, 공백 `' '`, `'NULL'`, `'0.0'`, 대소문자 혼합 `'HelloWorld'`, 유니코드.
3. **3VL 복합 논리 합성**: `NULL`, `UNKNOWN`, `COALESCE`, 복합 `AND/OR/NOT`, `IN (..., NULL)`, `BETWEEN`, `LIKE`.
4. **산술 및 연산자 결합**: 산술 연산자(`+`, `-`, `*`, `/`, `%`), 비트 연산자, 비교 연산자(`=`, `<>`, `<`, `>`, `<=`, `>=`).

### 3.2 Step 2: 오라클 실행 및 불일치 감지
- `pkg/speckit/runner`가 인메모리 세션을 구동하여 초당 12,000+ QPS 속도로 최적화 쿼리와 비최적화 쿼리를 대조합니다.
- 결과 레코드 집합 간 불일치 감지 시 즉시 축약 파이프라인으로 쿼리와 스키마를 전달합니다.

### 3.3 Step 3: 델타 디버깅 (Delta Debugging) 및 엔진 7단계 RCA
불일치가 발견된 쿼리는 최소화기(`pkg/speckit/minimizer`)에 의해 축약됩니다:
1. **행 소거 (Row Pruning)**: 이진 분할로 불일치가 유지되는 최소 데이터(1~2행)만 보존.
2. **컬럼 소거 (Column Pruning)**: 술어 평가와 무관한 컬럼을 DDL에서 제거.
3. **AST 술어 축약 (AST Minimization)**: 중첩 복합식을 최소 원자 조건식으로 축소.
4. **엔진 파이프라인 7단계 정밀 진단**:
   - `[Phase 1] Lexer` ➔ `[Phase 2] Parser` ➔ `[Phase 3] AST Lowering` ➔ `[Phase 4] VDBE Compiler/Planner` ➔ `[Phase 5] VDBE VM Execution` ➔ `[Phase 6] Volcano Filter Iterators` ➔ `[Phase 7] Storage Codec`

### 3.4 Step 4: TDD Red Phase 공식 등록
원인이 규명된 결함은 **발견 시점에 즉시 코드를 수정하지 않고**, TDD Red 상태로 등록하여 백로그로 자산화합니다:
1. **격리 실패 테스트 작성**: `test/spec_defects/spec_defect_<name>_test.go`에 독립 테스트 추가.
2. **Sprint DB & TASKS.md Dual-Sync**:
   ```bash
   ./bin/sprint add DEFECT-<DOMAIN>-<NAME>-1 "<요약 설명>" <도메인> 1 "<계획서 경로>" "1.0-GA"
   ```

---

## 4. 역할 분담 및 워크플로우 (Red vs Green Separation)

탐색과 수정을 동시에 진행하면 땜질식 임시방편(Ad-hoc patch)이 침투하여 다른 스펙을 깨뜨리는 안티패턴이 발생합니다. 두 역할을 물리적으로 엄격히 분리합니다:

```
[Phase A: Red Discovery Cycle (탐색 및 등록)]
  1. Spec Fuzzer/Generator 가동 (초당 12,000 QPS)
  2. 불일치(Discrepancy) 감지
  3. 최소 쿼리로 Delta 축약 (1~2행, 단일 조건)
  4. 원인 코드 파일/라인 식별 (RCA)
  5. Failing Test 작성 & sprint DB 등록 (Status: PENDING)
  6. 코드 수정 없이 다음 탐색으로 즉시 복귀! (반복)

                     ▼ (충분한 백로그 축적 후)

[Phase B: Green Resolution Sprint (집중 해결 스프린트)]
  1. sprint DB에서 결함 항목을 도메인별로 그룹핑 (Engine, VM, Execution)
  2. 엔진 표준 타입 시스템(`storage.Value`) 및 표준 연산자(`evalRowBinaryOp`)로 정규 구현
  3. failing test의 PASS 검증 (TDD Green 달성)
  4. 3-Stage Pipeline (Build ➔ Test ➔ Commit) 무결성 통과
  5. sprint DB 상태 DONE 업데이트
```

---

## 5. 툴링 및 구현 아키텍처 (`cmd/spec_fuzz` & `pkg/speckit`)

### 5.1 패키지 레이아웃
```
pkg/speckit/
├── grammar/          # SQL 문법 사양 및 AST 노드 합성기
│   ├── expr.go       # 식, 연산자, 3VL 논리 합성기
│   ├── types.go      # 타입 경계값 및 리터럴 생성기
│   └── query.go      # DDL 및 SELECT 쿼리 조합기
├── oracle/           # 오라클 동치성 판정기
│   ├── norec.go      # NoREC 비최적화 등가식 변환기
│   └── dualengine.go # OCC vs WiredTiger 차등 비교기
├── minimizer/        # 쿼리 축약기 (Delta Debugger)
│   └── reduce.go     # 데이터 행 및 AST 노드 축약
└── runner/           # 고성능 순환 러너 및 리포터
    └── loop.go       # 세션 풀 기반 초고속 실행 루프

cmd/spec_fuzz/
└── main.go           # CLI 진입점 (-workers, -duration, -domain, -oracle)
```

### 5.2 CLI 실행 명령어
```bash
# 1. 수식 및 3가 논리 대상 NoREC 오라클 집중 탐색
go run ./cmd/spec_fuzz -domain=expr -oracle=norec -duration=10m

# 2. In-Memory OCC vs WiredTiger 이중 스토리지 차등 비교 탐색
go run ./cmd/spec_fuzz -domain=join -oracle=dualengine -workers=8 -duration=30m
```

---

## 6. 실증 성과 매트릭스 (Empirical Discoveries)

본 도구를 LionSQL 코어 엔진에 실제 가동하여 61,000개 이상의 쿼리를 평가한 결과, 기존 단위 테스트가 놓쳤던 **7가지 심층 결함 클래스**를 최초로 발견하고 격리했습니다:

| 결함 식별자 (Task ID) | 원인 파일 및 위치 | 결함 발생 메커니즘 (RCA) | 등록된 TDD Red 테스트 | 상태 |
|---|---|---|---|---|
| `DEFECT-EXEC-FLOAT-DECIMAL-SCALE-TRUNCATION-1` | `pkg/execution/iterator.go` (`tryDecimal`) | FLOAT 컬럼 비교 시 `tryDecimal`이 4자리 스케일로 강제 절삭(`3.14159` ➔ `3.1416`)하여 WHERE 필터에서 행을 누락시킴. | `TestSpecDefect_FloatDecimalScaleTruncation` | **GREEN** |
| `DEFECT-PROC-EVAL-BIT-IN-LIST-1` | `pkg/proc/eval.go` (`InExpr`) | BIT 컬럼 IN (0, 1) 평가 시 `false`와 `"0"`을 단순 문자열 비교하여 불일치 발생. `evalRowBinaryOp` 위임으로 해결. | `TestSpecDefect_BitInListEval` | **GREEN** |
| `DEFECT-PROC-EVAL-BETWEEN-CASE-INSENSITIVE-COLLATION-1` | `pkg/proc/eval.go` (`BETWEEN`) | 문자열 BETWEEN 평가 시 Collation 대소문자 무시를 누락하고 Go 바이트 ASCII 비교(`'N' < 'a'`)를 수행. `evalRowBinaryOp` 위임으로 해결. | `TestSpecDefect_CollationBetweenDiscrepancy` | **GREEN** |
| `DEFECT-ENGINE-NOREC-3VL-IN-NULL-OR-1` | `pkg/execution/iterator.go` | 복합 OR 조건 내 NULL 포함 IN 리스트 평가 시 3VL과 부동소수점 스케일 절삭이 복합 작용하여 불일치 발생. | `TestSpecDefect_NoREC_3VL_NullOr` | **GREEN** |
| `DEFECT-ENGINE-BIT-FLOAT-LITERAL-EVAL-1` | `pkg/engine/sqlexec_eval.go:330` | BIT 컬럼과 `0.0`/`1.0` 부동소수점 리터럴 비교 시 엄격한 문자열 일치(`"1"`, `"0"`)만 검사하여 `"0.0"` 행을 누락시킴. | `TestSpecDefect_BitFloatEquality` | **RED** |
| `DEFECT-EXEC-ARITHMETIC-OVERFLOW-TRUTHY-EVAL-1` | `pkg/execution/eval.go:813` | WHERE 절 수식 평가 중 `math.MinInt64 - 3` 산술 오버플로우 에러가 발생했으나, `EvalTupleBoolExprWithContext`가 `default: return true`로 처리하여 에러를 참(매칭)으로 오평가. | `TestSpecDefect_ArithmeticOverflowTruthy` | **RED** |
| `DEFECT-VDBE-DECIMAL-FVAL-CORRUPTION-1` | `pkg/proc/runtime/vm.go:3841` | VDBE `toFloatNum`이 `storage.TypeDecimal` 컬럼에 대해 디코딩되지 않은 `v.FVal(0.0)`을 읽어 DECIMAL 비교식이 항상 0.0으로 왜곡됨. | `TestSpecDefect_DecimalFValCorruption` | **RED** |

---

## 7. 향후 확장 과제 (Roadmap)

1. **문법 커버리지 확장**:
   - 다중 테이블 `JOIN` (Inner, Left, Right, Full Outer, Cross)
   - `GROUP BY` / `HAVING` 및 집계 함수 연계
   - 서브쿼리 (`WHERE col IN (SELECT ...)` 및 Derived Tables)
2. **보조 인덱스 스캔 차분 오라클**:
   - `CREATE INDEX` 생성 후, B-Tree 인덱스 스캔 결과와 테이블 풀 스캔(SeqScan) 결과 간의 1:1 일치 오라클 구축.
3. **실패 코퍼스 자동 아카이빙 파이프라인**:
   - 불일치 발생 시 즉시 `test/corpora/failures/`에 독립 실행 가능한 `.sql` 재현 스크립트와 Go 테스트 코드를 자동 커밋/저장하도록 자동화.

---

## 8. 결론

본 방법론은 "인간의 직관과 고정된 테스트 스위트"의 한계를 넘어, **"스펙 기반 자율 탐색 ➔ 수학적 오라클 검증 ➔ 델타 축약 ➔ TDD 자산화"**라는 무결성 파이프라인을 구축한 것입니다.

특히 탐색 단계(Red)에서는 절대로 임시방편 코드를 작성하지 않고 정밀한 테스트 자산으로 백로그를 축적한 뒤, 해결 단계(Green)에서 엔진의 코어 아키텍처 원칙(`[RULE:RadicalKISS]`, `[RULE:ZeroAllocation]`, `[RULE:ModularAbstraction]`)을 준수하며 일괄 수정하는 엄격한 엔지니어링 규율을 확립합니다.
