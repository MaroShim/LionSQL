# LionSQL 0.95 및 Post-1.0 구조적 리팩토링 및 엣지 결함 완치 계획서

> **문서 ID**: `PLAN-POST-0-94-STRUCTURAL-REFACTORING-1`  
> **최초 작성일**: 2026-11-22  
> **책임자**: LionSQL Core Architecture & Execution Team  
> **상태**: `APPROVED / IN_PROGRESS`  
> **적용 마일스톤**: `v0.95.0` (Pre-1.0 긴급 보정 3건) 및 `v1.10.0` (Post-1.0 구조 개편 1건)  
> **연관 문서**:  
> - 불변식 및 아키텍처 헌법: [`GEMINI.md`](../../GEMINI.md)  
> - 전사 엔지니어링 가이드: [`notes/ENGINEERING_CONTEXT.ko.md`](../ENGINEERING_CONTEXT.ko.md)  
> - 액티브 태스크 레지스터: [`TASKS.md`](../../TASKS.md)  
> - 전수 감사 이관 목록: [`notes/POST_1.0_CANDIDATES.ko.md`](../POST_1.0_CANDIDATES.ko.md)  

---

## 1. 배경 및 추진 목적

LionSQL은 `ARCH-UNIFIED-TX-MANAGER`를 통해 DDL 보상 트랜잭션과 DML 스토리지 트랜잭션을 `TxContext` / `ShadowCatalog`라는 단일 책임 추상화로 일원화함으로써, 트랜잭션 수명주기 전반의 상태 불일치와 고스트 테이블 결함을 원천 차단했습니다.

v0.94.0 릴리스 게이트(Gate 0.5 ~ Gate 6)를 100% 통과한 현시점에서, 과거 전수 감사에서 Post-1.0으로 이관되었던 92개 후보 결함 중 **"이미 엔진에 구현되어 있으나 사소한 파이프라인 누락으로 오작동하는 엣지 케이스"**와 **"향후 시스템 안정성을 위해 구조적으로 정리해야 하는 아키텍처 마찰선"**을 식별하였습니다.

본 문서는 대수술적 변경이 필요한 구조 개편(타입 타워 일원화)과 1.0 직전 안전하게 완치할 수 있는 국소 결함(세션 변수 강제 형변환, TOP PERCENT, 대형 Decimal 오버플로 가드, LIKE 브래킷)을 명확한 마일스톤 경계(`v0.95.0` vs `v1.10.0`)로 나누어 체계적으로 해결하는 상세 설계를 정의합니다.

---

## 2. 태스크 분류 및 마일스톤 배정 (Taxonomy Matrix)

| 태스크 ID | 도메인 | 마일스톤 | 우선순위 | 핵심 과제명 | 위험도 |
| :--- | :---: | :---: | :---: | :--- | :---: |
| **`FIX-PROC-OUTPUT-VAR-COERCE-1`** | Engine | `v0.95.0` | P1 | 세션 변수 및 프로시저 OUTPUT 파라미터 정적 타입 CoerceValue 강제 파이프라인 | 낮음 |
| **`FIX-PAGINATION-TOP-PERCENT-PIPELINE-1`** | Engine | `v0.95.0` | P1 | TOP / LIMIT / OFFSET 단일 파이프라인 및 TOP n PERCENT 동적 행 계산 지원 | 낮음 |
| **`FIX-DECIMAL-OVERFLOW-AND-LIKE-BRACKET-1`** | Storage | `v0.95.0` | P1 | 대형 Decimal ➔ BigInt 변환 시 Overflow Fail-Closed 가드 및 LIKE '[]...]' 브래킷 이스케이프 정합성 | 낮음 |
| **`ARCH-UNIFIED-TUPLE-PROTOCOL-1`** | Architecture | `v1.10.0` | P2 | 타입 타워 3중화 해소: `storage.Row` 기반 Positional Tuple 결과셋 및 Wire 직결 아키텍처 | 높음 (코어 개편) |

---

## 3. 상세 기술 명세 및 구현 설계

### 3.1. [v0.95.0] `FIX-PROC-OUTPUT-VAR-COERCE-1`: 세션 변수 & OUTPUT 파라미터 강제 형변환 파이프라인

#### 문제 현상 (P2-29 재현 확인)
- T-SQL에서 `DECLARE @res INT = 0`으로 정수형 변수를 선언한 후, `DECIMAL(10,2)` 값을 반환하는 프로시저의 `OUTPUT` 매개변수를 `@res`에 바인딩(`EXEC sp @val = @res OUTPUT`)하면:
- 호출자의 원래 변수 타입(`TypeInt`)이 무시되고 `12.34` (실수 또는 문자열)가 그대로 변수에 대입됨.
- 이후 해당 변수에 비트 연산(`@res & 1`)이나 정수 산술 연산을 수행하면 `NULL`을 반환하거나 타입 불일치 런타임 에러가 발생함.

#### 해결 아키텍처
1. `ClientSession`의 세션 변수 저장소에 변수 생성 시 선언된 `storage.DataType` 메타데이터를 유지하거나, 기존 변수 덮어쓰기 시 원래 변수의 타입을 보존.
2. `pkg/engine/proc_bind.go`의 `BindOutputParams` 및 대화형 `SET @var = expr` 할당 경로에서:
   ```go
   if existingVar, exists := cs.GetVariable(varName); exists && !existingVar.IsNull {
       targetType := existingVar.Type
       coercedVal, err := storage.CoerceValue(targetType, outputVal)
       if err == nil {
           outputVal = coercedVal
       }
   }
   cs.SetVariable(varName, outputVal)
   ```
3. 정수 변수에 소수점이 반환되면 SQL Server 표준에 따라 반올림/버림 정수(`12`)로 안전하게 변환 대입되어 후속 연산이 정상 수행되도록 보장.

---

### 3.2. [v0.95.0] `FIX-PAGINATION-TOP-PERCENT-PIPELINE-1`: TOP / LIMIT / OFFSET 단일 파이프라인

#### 문제 현상 (P2-26 재현 확인)
- 4개 행을 가진 테이블에서 `SELECT TOP 50 PERCENT id FROM t_pct ORDER BY id;` 실행 시:
- `TOP 50 PERCENT`가 단순 `TOP 50`으로 처리되어 4개 행 전체가 반환됨 (`PERCENT` 키워드 침묵 무시).
- 기대 결과: 전체 4행의 50%인 $\lceil 4 \times 0.5 \rceil = 2$개 행만 반환되어야 함.

#### 해결 아키텍처
1. `PaginationSpec` 공용 도메인 구조체 확립:
   ```go
   type PaginationSpec struct {
       Limit      int64
       Offset     int64
       IsPercent  bool
       WithTies   bool
   }
   ```
2. Pratt 파서(`pkg/proc/parser.go`)에서 `PERCENT` 토큰 감지 시 `IsPercent: true`를 플래너 노드(`planner.LimitNode` 또는 `engine.SelectStatement`)로 전파.
3. 쿼리 실행기 슬라이싱 단계:
   - 만약 `IsPercent == true`인 경우:
     - 결과 행 집합의 총 개수 $N$을 기준으로 `effectiveLimit = int64(math.Ceil(float64(N) * float64(spec.Limit) / 100.0))` 계산.
     - 슬라이싱: `rows = rows[:effectiveLimit]` 적용.

---

### 3.3. [v0.95.0] `FIX-DECIMAL-OVERFLOW-AND-LIKE-BRACKET-1`: Decimal Overflow 가드 및 LIKE 브래킷 정합성

#### 문제 현상 1: Decimal to BigInt Cast 시 Silent Saturation (P2-04 재현 확인)
- `SELECT CAST(CAST('100000000000000000000' AS DECIMAL(38,0)) AS BIGINT);` 실행 시:
- 64비트 정수 범위를 초과하는 $10^{20}$ 값임에도 에러 없이 `math.MaxInt64` (`9223372036854775807`)로 조용히 Saturation되어 반환됨 (`[RULE:ExplicitError]` 위반).
- 기대 결과: `arithmetic overflow error converting numeric to data type bigint` 에러 반환.

#### 해결 아키텍처 1
- `pkg/storage/value.go`의 `storage.Value.Int64()` 및 `pkg/engine/evaluator.go`의 `castValue`에서:
  - `Decimal` 값이 `math.MinInt64 ~ math.MaxInt64` 범위를 초과하면 조용히 클램핑하지 않고 즉시 `ErrArithmeticOverflow` 반환.

#### 문제 현상 2: LIKE 패턴의 선두 닫는 브래킷 `[]abc]` 파싱 오류 (P2-80 재현 확인)
- `SELECT 1 WHERE 'a' LIKE '[]abc]';` 실행 시 0개 행 반환.
- SQL Server 표준: 브래킷 집합의 첫 글자가 `]`인 경우(`[]...]`), 이는 빈 브래킷이 아니라 리터럴 `]`를 포함하는 집합(`]` 또는 `a` 또는 `b` 또는 `c`)으로 해석되어야 함.

#### 해결 아키텍처 2
- `pkg/proc/like.go` (또는 `pkg/sqlutil/like.go`)의 패턴 파서에서:
  - `[` 바로 다음 인덱스에 `]`가 나타나는 경우, 브래킷 종료로 판정하지 않고 집합의 첫 번째 원소 문자로 소비하도록 토크나이저 상태 머신 수정.

---

### 3.4. [v1.10.0] `ARCH-UNIFIED-TUPLE-PROTOCOL-1`: 타입 타워 일원화 및 Positional Tuple 결과셋

#### 현행 문제점 및 아키텍처 부채
- 스토리지(`storage.Row`), 실행기(`[]any`), 코디네이터(`[]map[string]any`), 와이어 프로토콜(직렬화 포맷터) 간에 4단계의 데이터 형태 변환이 발생.
- `map[string]any`로 인해 동일 컬럼명 충돌 및 고부하 쿼리에서 수십만 개의 맵 할당으로 인한 GC 압력 유발.

#### v1.10.0 목표 아키텍처
1. **`QueryResult`의 포지셔널 슬라이스 전환**:
   - `Rows []map[string]any` 의존성을 점진적으로 폐지하고, `Columns []ColumnMeta` + `Rows [][]storage.Value` (또는 `[]storage.Row`) 기반 포지셔널 구조체로 전환.
2. **와이어 프로토콜 제로-카피 직결**:
   - PGWire 및 TDS 토큰 인코더가 맵 키 탐색(`row[colName]`)을 완전히 우회하고, 컬럼 인덱스 순서대로 즉시 바이너리 프레임을 인코딩하는 제로-할당 파이프라인 확립.
3. **실행기 반복자(Iterator)의 완전한 `storage.Value` 타입 단일화**:
   - Volcano 엔진 내부에서 `any` 박싱/언박싱을 전면 폐지하고 `storage.Value` 구조체 슬라이스 단일 포맷으로 100% 통합.

---

## 4. TDD Red 테스트 스위트 설계

모든 결함은 수정 전에 반드시 실패하는 단위 테스트(TDD Red)로 영속화되어야 합니다 (`[RULE:PipelineVerification]`).

- **테스트 파일 위치**: `test/spec_defects/spec_defect_p2_candidates_red_test.go`
- **테스트 케이스 구성**:
  1. `TestDefect_P2_04_DecimalToBigIntOverflowFailClosed`: $10^{20}$ Decimal ➔ BigInt 캐스팅 시 오버플로 에러 발생 검증.
  2. `TestDefect_P2_80_LikeLeadingBracketPatternMatching`: `'a' LIKE '[]abc]'` 및 `']' LIKE '[]abc]'` 정상 매칭(1행 반환) 검증.
  3. `TestDefect_P2_26_SelectTopPercentCalculation`: 4행 테이블에서 `TOP 50 PERCENT` 조회 시 정확히 2행 반환 검증.
  4. `TestDefect_P2_29_ProcOutputVariableTypeCoercion`: `INT` 세션 변수에 소수점 OUTPUT 파라미터 반환 시 정수형으로 Coerce되어 비트 연산이 성공하는지 검증.

---

## 5. 단계별 실행 계획 및 완료 조건

1. **Phase 1: TDD Red 테스트 추가 (`v0.95.0`)**
   - 위 4대 결함에 대한 재현 테스트를 `test/spec_defects/`에 작성하고 현 상태에서 정상 실패(Red)함을 확인.
2. **Phase 2: v0.95.0 국소 패치 및 Green 달성**
   - `evaluator.go`, `like.go`, `sqlexec_select.go`, `proc_bind.go`를 국소 수정하여 4대 테스트 100% 통과(Green) 달성.
3. **Phase 3: 스프린트 DB 및 태스크 레지스터 동기화**
   - `./bin/sprint add`를 통해 정식 등록 및 `TASKS.md` 듀얼 싱크 완료.
