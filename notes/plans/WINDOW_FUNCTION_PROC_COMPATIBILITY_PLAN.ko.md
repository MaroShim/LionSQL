# 분석 함수 LEAD/LAG 프로시저 런타임 호환성 점검 및 윈도우 프레임 독립 실행 검증 계획서 (`notes/plans/WINDOW_FUNCTION_PROC_COMPATIBILITY_PLAN.ko.md`)

> - **태스크 ID**: `FEAT-WIN-FUNC-PROC-COMPAT-1`
> - **도메인**: Engine | **우선순위**: P2 | **마일스톤**: `v0.99.0` (Pre-1.0)
> - **연계 규칙**: `[RULE:PipelineVerification]`, `[RULE:DeterministicTesting]`, `[RULE:BoundedBuffers]`, `[RULE:RadicalKISS]`

---

## 1. 개요 및 검증 목표

LionSQL의 Volcano 실행 엔진(`pkg/execution/window.go`)은 `ROW_NUMBER`, `RANK`, `DENSE_RANK`, `LEAD`, `LAG` 등 관계형 윈도우 함수를 지원합니다.
본 과제는 T-SQL 저장 프로시저(Stored Procedure), 커서(CURSOR), 및 `WHILE` 제어문 루프 런타임에서 분석 함수 `LEAD` 및 `LAG`가 다음 5가지 시나리오에서 상태 오염(State Pollution)이나 프레임 버퍼 누수 없이 독립적이고 결정론적으로 실행되는지 점검하고 호환성을 완결합니다:

1. **프로시저 내 직접 결과셋 반환 (`SELECT LEAD(...) / LAG(...) OVER (...)`)**:
   - `PARTITION BY` 및 `ORDER BY`를 동시 적용한 다중 파티션 정렬 윈도우 함수가 프로시저 실행 결과셋으로 완벽히 반환되는지 확인.
2. **커서(CURSOR) 바인딩 및 패치**:
   - `DECLARE cur CURSOR FOR SELECT ..., LAG(...) OVER (...), LEAD(...) OVER (...)` 선언 후 `FETCH NEXT INTO @var1, @var2` 순회 시 프레임 정합성 유지 확인.
3. **`WHILE` 제어문 반복 실행 간 윈도우 프레임 독립성**:
   - 프로시저 루프 내에서 동일 윈도우 쿼리를 반복 실행할 때 이전 루프의 프레임 상태/행 인덱스/메모리 버퍼가 잔존하지 않고 매 반복마다 독립 초기화되는지 확인.
4. **동일 세션 다회차 호출 및 플랜 캐시(Plan Cache) 재사용**:
   - 컴파일된 VDBE 바이트코드 플랜 캐시가 재사용될 때, 윈도우 파티션 맵(`partitions`) 및 결과 슬라이스가 독립적으로 생성되어 동시성/반복 호출 시 상호 간섭이 없는지 확인.
5. **음수 기본값 및 엣지 케이스 정합성**:
   - `LEAD(val, 1, -1)`, `LAG(val, 1, 0)` 등 기본값(default value)과 경계 조건(시작 행의 LAG, 마지막 행의 LEAD)의 NULL/기본값 반환 정합성 검증.

---

## 2. 검증 테스트 설계 (`pkg/proc/runtime/window_proc_test.go`)

- `TestWindowProc_DirectSelect`: 프로시저 내 `LEAD`/`LAG` 다중 컬럼 결과셋 조회 검증
- `TestWindowProc_CursorIteration`: 커서 기반 `LEAD`/`LAG` 행 단위 패치 및 누적 연산 검증
- `TestWindowProc_WhileLoopIsolation`: `WHILE` 루프 다회 반복 시 프레임 오염 제로 검증
- `TestWindowProc_PartitionAndOrder`: `PARTITION BY dept ORDER BY salary` 그룹별 경계 격리 검증
- `TestWindowProc_PlanCacheAndMultiRun`: 플랜 캐시 재사용 하에서 10회 연속 호출 결과 불변 검증

---

## 3. 완료 판정 기준 (Acceptance Criteria)

1. `pkg/proc/runtime/window_proc_test.go` 내 모든 테스트 100% PASS (0 race).
2. 전체 엔진 테스트 `go test -race ./...` 100% PASS.
3. 스프린트 태스크(`FEAT-WIN-FUNC-PROC-COMPAT-1`) `DONE` 동기화 및 `TASKS.md` 반영.
