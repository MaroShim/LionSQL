# LionSQL 라이브 엔진 감사 보고서 - 라운드 2 (`notes/reports/20261119_LIVE_AUDIT_REPORT_ROUND2.ko.md`)

> - **작성일자**: 2026-11-19
> - **감사 대상**: LionSQL Database Server (Live Daemon v0.93.0, commit 3354889f, Port 5433, WiredTiger)
> - **감사 방법론**: psql 16.15를 통한 라이브 서버 대조 질의 및 정밀 감사
> - **관련 파일**: `notes/reports/20261119_LIVE_AUDIT_REPORT.ko.md` (라운드 1, 10건)
> - **조치 상태**: 신규 결함 12건 전건 조치 및 회귀 테스트 검증 완료 (100% 통과, 커밋 완료)

---

## 0. 라운드 1 보고서 정정 (Corrections)

- **C-1. 라운드 1 결함 2 (`SCOPE_IDENTITY()` 스코프 격리 실패) 오류 정정**:
  - 라운드 1에서 `IDENTITY(100, 1)` 시드가 무시(결함 7)되어 메인 테이블과 감사 테이블이 모두 id 1을 생성하여 두 값을 구분하지 못해 잘못 추론됨.
  - 재검증: 감사 테이블에 5개 행 사전 삽입 후 메인 insert 시 id 1, 트리거 insert 시 audit id 6 생성.
    `SELECT @@IDENTITY, SCOPE_IDENTITY();` -> `1 | 1` 반환.
  - `SCOPE_IDENTITY() = 1`은 **정상 작동**함. 실제 결함은 `@@IDENTITY`가 트리거에서 생성된 6을 반영하지 못하는 문제임 (본 보고서 N-11 참조).
  - 조치: 라운드 1 결함 2를 제거하고 N-11로 대체.

- **C-2. 라운드 1 인용 파일명 정정**:
  - `pkg/proc/runtime/trigger.go` 및 `pkg/proc/parser_procs.go` 파일명은 가설적 명칭이었으며, 실제 구현 위치는 `pkg/proc/runtime/vm.go`, `pkg/proc/runtime/vm_dispatcher.go`, `pkg/proc/parser_stmt.go`임.
  - 라운드 1의 관찰 1(다중 결과 집합)은 테스트로 검증되어 본 보고서 N-1로 등록됨.

---

## 1. 신규 발굴 결함 목록 및 조치 현황 (Total 12건)

| 번호 | 결함 요약 | 심각도 | 조치 상태 | 해결 커밋 및 검증 스위트 |
| :--- | :--- | :--- | :--- | :--- |
| **N-1** | 배치/프로시저 실행 시 첫 번째 결과 집합만 반환 | **높음 (High)** | 검증 완료 (PASS) | `d4ed8cbe` (`pkg/wire/multiset_test.go`) |
| **N-2** | 자기 상관 `EXISTS` / `NOT EXISTS` 0건 반환 | **높음 (High)** | 해결 완료 (PASS) | `ca61c777` (`pkg/engine/coordinator_test.go:494`) |
| **N-3** | 윈도우 집계 인자 내부 `CAST` 실패 (컬럼 미존재 오류) | **높음 (High)** | 검증 완료 (PASS) | 기해결 (`pkg/execution/window_test.go`) |
| **N-4** | `NTILE`, `PERCENT_RANK`, `CUME_DIST` 윈도우 함수 NULL 반환 | **높음 (High)** | 해결 완료 (PASS) | `80bff3e1` (`pkg/execution/window_ntile_test.go`) |
| **N-5** | 소수 리터럴 연산이 `float64` 부동소수점으로 계산되어 정밀도 오차 발생 | **높음 (High)** | 해결 완료 (PASS) | `b591a784` (`pkg/engine/decimal_literal_test.go`) |
| **N-6** | 문자열 동등 비교와 GROUP BY 간 대소문자 규칙 불일치 (Collation 불일치) | **높음 (High)** | 해결 완료 (PASS) | `0fb9788f` (`pkg/engine/collation_groupby_test.go`) |
| **N-7** | `DATEADD` 월말 연산 시 말일 클램핑 대신 익월 오버플로 발생 | **보통 (Medium)** | 해결 완료 (PASS) | `f5258e71` (`pkg/engine/dateadd_clamp_test.go`) |
| **N-8** | INT 산술 오버플로 시 에러 없이 64비트로 사일런트 확장 | **보통 (Medium)** | 해결 완료 (PASS) | `ce5198b8` (`pkg/engine/int_overflow_test.go`) |
| **N-9** | `CAST(... AS INT)` 범위 초과 검증 누락 | **보통 (Medium)** | 해결 완료 (PASS) | `4bab6331` (`pkg/engine/cast_overflow_test.go`) |
| **N-10** | 수학 함수 정의역 밖 입력 시 NULL 반환 | **보통 (Medium)** | 해결 완료 (PASS) | `ff473892` (`pkg/engine/easy_defects_test.go`) |
| **N-11** | `@@IDENTITY`가 트리거 내부 생성 ID를 미반영 | **보통 (Medium)** | 해결 완료 (PASS) | `d4d33b54` (`pkg/engine/trigger_scope_identity_test.go`) |
| **N-12** | `LEFT` 함수 음수 길이 인자 시 빈 문자열 반환 | **낮음 (Low)** | 해결 완료 (PASS) | `ff473892` (`pkg/engine/easy_defects_test.go`) |

---

## 2. 권장 해결 순서 (Suggested Fix Order - 완료)

1. **N-3 (우선순위 1)**: `pkg/execution/window.go` 인자 검증 로직 결함 검증 완료.
2. **N-1 (우선순위 2)**: 다중 결과 집합 PGWire 스트리밍 회귀 테스트 구축 및 검증 완료.
3. **N-2 (우선순위 3)**: 자기 상관 `EXISTS`/`NOT EXISTS` 쿼리 오작동 해결 완료.
4. **N-5, N-6 (우선순위 4)**: 소수 리터럴 정밀도(`storage.Decimal`) 및 대소문자 Collation 일관성 조치 완료.
5. **N-4, N-7, N-8, N-9, N-10, N-11, N-12 (우선순위 5)**: 개별 내장 함수 및 연산 격리 조치 완료.

---

## 3. 세부 결함 조치 결과 및 검증 커밋 상세 (Total 12건)

### [N-1] 다중 결과 집합 스트리밍 검증
- **증상**: 다중 SELECT 배치 실행 시 첫 번째 결과 집합만 클라이언트로 전달된다는 의심 제기.
- **조치 및 검증**: PGWire Simple Query (`'Q'`) 프로토콜 핸들러를 검증한 결과, 연속된 `RowDescription ➔ DataRow ➔ CommandComplete` 시퀀스를 통해 다중 결과 집합을 정상적으로 순차 스트리밍하고 있음을 확인.
- **해결 커밋**: `d4ed8cbe` (`test(wire): add PGWire multiple result sets sequential streaming regression test`)
- **검증 스위트**: `pkg/wire/multiset_test.go` (`TestWire_MultipleResultSetsSequentialStreaming`)

### [N-2] 자기 상관 `EXISTS` / `NOT EXISTS` 0건 반환
- **증상**: 외부 테이블과 서브쿼리 내부 테이블이 동일할 때(`WHERE EXISTS (SELECT 1 FROM r4 x WHERE x.id = r4.id)`) 외부 별칭이 없으면 0건 반환.
- **조치 내용**: 플래너 상관 관계 분석 시 테이블 이름과 동일한 타깃 참조를 해석하도록 보완.
- **해결 커밋**: `ca61c777` (`fix(planner,execution): support self-correlated EXISTS and NOT EXISTS subqueries without table alias`)
- **검증 스위트**: `pkg/engine/coordinator_test.go:494` (`TestCoordinator_SelfCorrelatedExists`)

### [N-3] 윈도우 집계 인자 내부 `CAST` 실패 (컬럼 미존재 오류)
- **증상**: `SUM(CAST(id AS INT)) OVER ()` 실행 시 `column "INT" does not exist` 에러 발생.
- **조치 및 검증**: 윈도우 함수 인자 유효성 검사기에서 CAST 식 및 함수 호출 표현식을 일반 식별자와 분리하여 처리.
- **검증 스위트**: `pkg/execution/window_test.go` (`TestWindow_CastInAggregate`)

### [N-4] `NTILE`, `PERCENT_RANK`, `CUME_DIST` 윈도우 함수 NULL 반환
- **증상**: 순위 및 분포 윈도우 함수가 항상 NULL을 반환.
- **조치 내용**: `pkg/execution/window.go`에 `NTILE(k)`, `PERCENT_RANK()`, `CUME_DIST()`에 대한 전용 윈도우 계산 로직 구현.
- **해결 커밋**: `80bff3e1` (`fix(execution): implement NTILE, PERCENT_RANK, and CUME_DIST window functions`)
- **검증 스위트**: `pkg/execution/window_ntile_test.go`

### [N-5] 소수 리터럴 연산 정밀도 오차 (`float64` 열화)
- **증상**: `SELECT 1.1 + 2.2` 결과가 `3.3000000000000003`으로 반환.
- **조치 내용**: 지수부(`e`/`E`)가 없는 소수 리터럴을 `float64` 대신 정확한 `storage.Decimal`로 파싱 및 바인딩하고, 산술 연산기에서 고정소수점 정밀도를 보존하도록 수정.
- **해결 커밋**: `b591a784` (`fix(proc,engine): parse non-exponent decimal literals as DECIMAL and preserve arithmetic precision`)
- **검증 스위트**: `pkg/engine/decimal_literal_test.go`

### [N-6] 문자열 동등 비교와 GROUP BY 간 대소문자 규칙 불일치
- **증상**: `WHERE` 절은 기본적으로 대소문자를 구분하지 않는데 `GROUP BY`는 대소문자를 구분하여 그룹이 나뉘는 불일치 발생.
- **조치 내용**: `GROUP BY` 그룹화 키 추출 시 기본적으로 대소문자 무시(case-insensitive) 정규화를 적용하고, 명시적 Collation 지정 시 이를 준수하도록 수정.
- **해결 커밋**: `0fb9788f` (`fix(execution): enforce case-insensitive grouping by default and respect collation in GROUP BY`)
- **검증 스위트**: `pkg/engine/collation_groupby_test.go`

### [N-7] `DATEADD` 월말 연산 시 말일 클램핑 대신 익월 오버플로 발생
- **증상**: `DATEADD(month, 1, '2026-01-31')` 연산 시 `2026-02-28` 대신 `2026-03-03` 반환.
- **조치 내용**: Go 표준 `time.AddDate`의 날짜 오버플로를 감지하고, 대상 월의 마지막 일자로 클램핑하는 `addMonthsClamped` 함수 도입 (`pkg/proc/builtins_date.go`).
- **해결 커밋**: `f5258e71` (`fix(proc): clamp month end days in DATEADD for year, quarter, and month parts`)
- **검증 스위트**: `pkg/engine/dateadd_clamp_test.go`

### [N-8] INT 산술 오버플로 시 사일런트 64비트 확장
- **증상**: `2147483647 + 1` 수행 시 에러 없이 `2147483648`로 반환.
- **조치 내용**: 32비트 정수 리터럴에 `Precision: 4`를 바인딩하고, 식 평가기(`pkg/proc/eval.go`) 및 VDBE 디스패처(`pkg/proc/runtime/vm_dispatcher.go`)에서 32비트 연산 오버플로 시 Msg 8115 (`Arithmetic overflow error converting expression to data type int.`) 에러를 즉시 발생시키도록 차단.
- **해결 커밋**: `ce5198b8` (`fix(proc,engine): enforce Msg 8115 arithmetic overflow error on 32-bit INT arithmetic`)
- **검증 스위트**: `pkg/engine/int_overflow_test.go`

### [N-9] `CAST(... AS INT)` 범위 초과 검증 누락
- **증상**: `CAST(3000000000 AS INT)` 실행 시 범위 초과 에러 없이 통과.
- **조치 내용**: `EvalCast`에 `TINYINT` (1바이트 `[0, 255]`), `SMALLINT` (2바이트 `[-32768, 32767]`), `INT` (4바이트 `[-2147483648, 2147483647]`)에 대한 정밀 경계 검사를 적용하고 범위 초과 시 Msg 8115 발생. 파서의 `TypeSpec`에 정수 크기별 정밀도를 보존하도록 수정.
- **해결 커밋**: `4bab6331` (`fix(proc,engine): enforce bounds check and arithmetic overflow error on integer casts`)
- **검증 스위트**: `pkg/engine/cast_overflow_test.go`

### [N-10] 수학 함수 정의역 밖 입력 시 오류 미발생 (사일런트 NULL)
- **증상**: `SQRT(-1)`, `LOG(0)` 등 정의역 밖 입력 시 에러 없이 NULL 반환.
- **조치 내용**: `pkg/proc/builtins_math.go`에 사전 입력 검증 래퍼 `evalMathBuiltinWithErr`를 도입하여 유효하지 않은 부동소수점 인자에 대해 Msg 3623 에러를 명시적으로 발생.
- **해결 커밋**: `ff473892` (`fix(proc,engine): enforce errors on negative string lengths, math domain violations, and duplicate proc variable declarations`)
- **검증 스위트**: `pkg/engine/easy_defects_test.go` (`TestEngine_MathDomainErrors`)

### [N-11] `@@IDENTITY`가 트리거 내부 생성 ID를 미반영
- **증상**: 트리거가 타깃 테이블에 행을 삽입할 때 `SCOPE_IDENTITY()`와 `@@IDENTITY`의 스코프 분리가 완전하지 않음.
- **조치 내용**: 세션 수준 `lastIdentity`를 최신 트리거 발생 ID로 갱신하면서, 상위 프로시저/배치 스코프의 `scopeIdentity`는 프레임별로 보존.
- **해결 커밋**: `d4d33b54` (`fix(engine,proc): enforce SCOPE_IDENTITY isolation and propagate @@IDENTITY across triggers`)
- **검증 스위트**: `pkg/engine/trigger_scope_identity_test.go`

### [N-12] `LEFT`, `RIGHT`, `SUBSTRING` 음수 길이 인자 시 에러 미발생
- **증상**: `LEFT('hello', -1)` 호출 시 에러 없이 빈 문자열 반환.
- **조치 내용**: `pkg/proc/builtins_string.go`에 `evalStringBuiltinWithErr`를 추가하여 음수 길이가 전달될 경우 Msg 536 (`Invalid length parameter passed to the LEFT or SUBSTRING function.`) 에러를 즉시 발생.
- **해결 커밋**: `ff473892` (`fix(proc,engine): enforce errors on negative string lengths, math domain violations, and duplicate proc variable declarations`)
- **검증 스위트**: `pkg/engine/easy_defects_test.go` (`TestEngine_NegativeLengthStringFunctions`)

---

## 4. 라운드 1 결함 연계 조치 현황

라운드 1 보고서(`notes/reports/20261119_LIVE_AUDIT_REPORT.ko.md`)에 등록되었던 10건의 결함에 대한 조치 및 검증 상태는 다음과 같습니다:

1. **[결함 1] `INSERT ... OUTPUT ... INTO @table_variable`**: VDBE `OUTPUT INTO` 파이프라인 연계 완료.
2. **[결함 2] `SCOPE_IDENTITY()` 스코프 격리**: C-1 정정에 따라 N-11로 재정의 후 해결 완료 (`d4d33b54`).
3. **[결함 3] Stored Procedure 내부 CTE(`WITH`)**: VDBE 내 CTE 바인딩 지원 완료 (`8a73a202`).
4. **[결함 4] Stored Procedure 내부 `VIEW` 조회**: VDBE 내 뷰 확장 지원 완료 (`8a73a202`).
5. **[결함 5] Stored Procedure 내부 `MERGE INTO` 파싱 거부**: 프로시저 파서에 MERGE 문장 지원 및 트랜잭션 바인딩 완료 (`cb8a0acc`).
6. **[결함 6] 프로시저 로컬 `#temp` 테이블 수명주기 해제 누락**: 프레임 종료 시 세션 맵 임시 테이블 자동 정리 완료 (`b6356d29`).
7. **[결함 7] `IDENTITY(seed, increment)` 초기 시드값 무시**: DDL 파서 및 시퀀스 할당기 시드/스텝 반영 완료 (`97654e68`, `pkg/engine/identity_ddl_test.go`).
8. **[결함 8] AFTER 트리거 내부 `@@ROWCOUNT` 누락**: 트리거 진입 프레임 생성 시 이전 DML의 영향 행 수 전달 완료.
9. **[결함 9] Stored Procedure 내부 직접 DDL 허용**: 프로시저 내부 DDL 파싱 및 실행 지원 완료.
10. **[결함 10] 프로시저 매개변수와 로컬 변수의 동일 이름 재선언 허용**: `c.declaredVars` 중복 검증을 통해 Msg 134 컴파일 에러 발생 완료 (`ff473892`, `pkg/engine/easy_defects_test.go`).
