# P2 그룹 A/B 초저난도 신뢰성 및 경계값 가드 안정화 계획서

> - **최초 작성일**: 2026-11-10
> - **상태**: 진행 중 (`IN_PROGRESS`)
> - **관련 백로그**: `P2-STABILIZE-AB-1` (SQLite Sprint DB)

---

## 1. 개요 및 목적

LionSQL 1.0 전 소스코드 135단계 듀얼 AI(Gemini × Claude) 감사에서 식별된 P2 백로그 중, **코드 변경량이 1~5줄 내외로 극히 단순하면서 다른 모듈에 부작용(Side-effect)이 없고 즉각적인 신뢰성/경계값 방어 효과를 제공하는 알짜 항목 10건(그룹 A 6건, 그룹 B 4건)**을 선제적으로 구현하여 1.0 코어 신뢰성을 강화한다.

---

## 2. 세부 대상 항목 및 개선 사양

### 그룹 A: 경계값 클램핑 및 오버플로우 방어 (6건)

1. **`Scope-017` `pkg/storage/engine.go` (`FastRandN`)**
   - **현상**: `FastRandN(n)` 난수 생성 시 `UnixNano() % int64(n)` 연산에서 극단적인 음수 나노초 처리 중 `math.MinInt64` 부호 반전 오버플로우로 음수 반환 가능성.
   - **조치**: `int(uint64(time.Now().UnixNano()) % uint64(n))` 비부호 정수 변환으로 음수 모듈로 원천 차단.

2. **`Scope-056` `pkg/proc/lexer.go` (`NextToken`)**
   - **현상**: 렉서 시작 또는 개행 직후 토큰 위치 기록 시 컬럼 번호가 0 이하로 방출될 수 있는 경계 케이스.
   - **조치**: `if tokCol < 1 { tokCol = 1 }`, `if tokLine < 1 { tokLine = 1 }` 방어적 1-based 클램핑 (`[RULE:DefensiveIndexing]`).

3. **`Scope-071` `pkg/proc/builtins_math.go` (`MOD`)**
   - **현상**: `MOD(MinInt64, -1)` 평가 시 Go 런타임 정수 오버플로우 패닉 발생.
   - **조치**: `if args[0].IVal == math.MinInt64 && args[1].IVal == -1 { return storage.IntValue(0) }` 오버플로우 방어.

4. **`Scope-095` `pkg/execution/governor.go` (`ParseMemSize`)**
   - **현상**: 메모리 거버너 파싱 시 초대형 바이트 수치 입력 시 정수 오버플로우 래핑 가능성.
   - **조치**: `val * multiplier` 오버플로우 감지 시 `math.MaxInt64` 클램핑 및 `bytes <= 0` 방어.

5. **`Scope-114` `pkg/sqlutil/tokenizer.go` (`FindMatchingParen`)**
   - **현상**: 비정상적인 닫는 괄호 먼저 출현 시 `depth`가 음수로 떨어져 계속 순회하는 현상.
   - **조치**: `depth < 0` 즉시 `-1` 반환하여 불필요한 음수 깊이 스캔 차단.

6. **`Scope-052` `pkg/engine/sqlexec_string_split.go` (`STRING_SPLIT`)**
   - **현상**: `STRING_SPLIT`에 빈 문자열 구분자(`''`) 입력 시 무한 분할 또는 패닉 가능성.
   - **조치**: `sep == ""` 시 `STRING_SPLIT separator cannot be empty` 명시적 에러 반환 (기존 구현 상태 검증 및 통합 테스트 보강).

---

### 그룹 B: 내장 함수 T-SQL 표준 정합성 보정 (4건)

7. **`Scope-070` `pkg/proc/builtins_string.go` (`SUBSTRING`)**
   - **현상**: `SUBSTRING('12345', -2, 5)`처럼 음수 시작 위치와 길이가 주어질 때 T-SQL 표준(1-based 음수 오프셋) 대신 전체 문자열이 반환되는 현상.
   - **조치**: `rawStart < 1`일 때 유효 시작 지점을 0으로 하고 유효 문자 수를 `rawStart + length - 1`로 클램핑하는 T-SQL 표준 공식 적용.

8. **`Scope-073` `pkg/proc/builtins_sys.go` (`SERVERPROPERTY`)**
   - **현상**: 미지원 서버 속성 조회 시 `NULL` 대신 빈 문자열(`""`)을 반환하여 `IS NULL` 조건 분기가 실패하는 문제.
   - **조치**: `default:` 분기에서 `storage.NullValue(storage.TypeVarChar)` 반환.

9. **`Scope-051` `pkg/engine/evaluator.go` (`EvalBinaryOp`)**
   - **현상**: 산술 연산기에서 0 나누기 및 `MinInt64 / -1`, `MinInt64 % -1` 발생 시 런타임 패닉 가능성.
   - **조치**: 0 나누기 및 `MinInt64 / -1` 정수 오버플로우 가드 추가로 패닉 방지.

10. **`Scope-021` `pkg/storage/types_extended.go` & `coerce.go` (`DateValueFromString`)**
    - **현상**: 파싱 불가능한 잘못된 날짜 문자열 입력 시 `0001-01-01` 제로 타임을 반환하여 유효한 날짜로 잘못 비교되는 문제.
    - **조치**: `DateValueFromString` 파싱 실패 시 `NullValue(TypeDate)` 반환.

---

## 3. 3단계 검증 파이프라인 계획 (`[RULE:PipelineVerification]`)

1. **Build**: `go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **Test**: `go test -tags wiredtiger -count=1 -race ./pkg/...` 100% PASS
3. **Commit**: `fix(storage,proc,engine): apply Group A and B edge-case guards and T-SQL parity fixes`
