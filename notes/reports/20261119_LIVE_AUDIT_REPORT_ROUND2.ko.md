# LionSQL 라이브 엔진 감사 보고서 - 라운드 2 (`notes/reports/20261119_LIVE_AUDIT_REPORT_ROUND2.ko.md`)

> - **작성일자**: 2026-11-19
> - **감사 대상**: LionSQL Database Server (Live Daemon v0.93.0, commit 3354889f, Port 5433, WiredTiger)
> - **감사 방법론**: psql 16.15를 통한 라이브 서버 대조 질의 및 정밀 감사
> - **관련 파일**: `notes/reports/20261119_LIVE_AUDIT_REPORT.ko.md` (라운드 1, 10건)

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

## 1. 신규 발굴 결함 목록 (Total 12건)

| 번호 | 결함 요약 | 심각도 | 근거 수준 | 핵심 증상 및 위치 |
| :--- | :--- | :--- | :--- | :--- |
| **N-1** | 배치/프로시저 실행 시 첫 번째 결과 집합만 반환 | **높음 (High)** | 동작 검증 | `SELECT 1; SELECT 2;` 실행 시 첫 번째 결과 집합만 클라이언트로 전달 |
| **N-2** | 자기 상관 `EXISTS` / `NOT EXISTS` 0건 반환 | **높음 (High)** | 동작 검증 | `WHERE EXISTS (SELECT 1 FROM r4 x WHERE x.id = r4.id)` 0건 반환 (외부 별칭 부여 시 정상) |
| **N-3** | 윈도우 집계 인자 내부 `CAST` 실패 (컬럼 미존재 오류) | **높음 (High)** | 코드 확인 | `SUM(CAST(id AS INT)) OVER ()` -> `column "INT" does not exist` (`pkg/execution/window.go:241-267`) |
| **N-4** | `NTILE`, `PERCENT_RANK`, `CUME_DIST` 윈도우 함수 NULL 반환 | **높음 (High)** | 동작 검증 | 모든 행에서 에러 없이 NULL 반환 (`RANK`, `DENSE_RANK`, `LAG`, `LEAD` 등은 정상) |
| **N-5** | 소수 리터럴 연산이 `float64` 부동소수점으로 계산되어 정밀도 오차 발생 | **높음 (High)** | 동작 검증 | `SELECT 1.1 + 2.2` -> `3.3000000000000003` (소수 리터럴 참여 시 float 열화) |
| **N-6** | 문자열 동등 비교와 GROUP BY 간 대소문자 규칙 불일치 (Collation 불일치) | **높음 (High)** | 동작 검증 | `WHERE nm = 'abc'`는 3건 일치(대소문자 무시), `GROUP BY nm`은 3개 개별 그룹으로 분리(대소문자 구분) |
| **N-7** | `DATEADD` 월말 연산 시 말일 클램핑 대신 익월 오버플로 발생 | **보통 (Medium)** | 코드 확인 | `DATEADD(month, 1, '2026-01-31')` -> `2026-03-03` (기대값 `2026-02-28`) (`pkg/proc/builtins_date.go:164-168`) |
| **N-8** | INT 산술 오버플로 시 에러 없이 64비트로 사일런트 확장 | **보통 (Medium)** | 동작 검증 | INT 최대값 `2147483647 + 1` -> `2147483648` (오버플로 오류 미발생) |
| **N-9** | `CAST(... AS INT)` 범위 초과 검증 누락 | **보통 (Medium)** | 동작 검증 | `CAST(3000000000 AS INT)` -> `3000000000` (INT 범위 초과 에러 미발생) |
| **N-10** | 수학 함수 정의역 밖 입력 시 NULL 반환 | **보통 (Medium)** | 코드 확인 | `SQRT(-1)` -> `NULL`, `LOG(0)` -> `NULL` (`pkg/proc/builtins_math.go:154-156`) |
| **N-11** | `@@IDENTITY`가 트리거 내부 생성 ID를 미반영 | **보통 (Medium)** | 동작 검증 | 트리거가 audit 테이블에 aid=6을 삽입해도 `@@IDENTITY`가 1로 유지 (기대값: `@@IDENTITY=6`, `SCOPE_IDENTITY()=1`) |
| **N-12** | `LEFT` 함수 음수 길이 인자 시 빈 문자열 반환 | **낮음 (Low)** | 동작 검증 | `LEFT('hello', -1)` -> `''` (기대값: 유효하지 않은 길이 파라미터 에러) |

---

## 2. 권장 해결 순서 (Suggested Fix Order)

1. **N-3 (우선순위 1)**: `pkg/execution/window.go` 인자 검증 로직 결함 (이전 커밋 `3354889f`의 회귀 결함이며 즉시 수정 가능).
2. **N-1 (우선순위 2)**: 다중 결과 집합 사일런트 유실 (모든 다중 배치 및 프로시저 질의에 영향).
3. **N-2 (우선순위 3)**: 자기 상관 `EXISTS`/`NOT EXISTS` 쿼리 오작동.
4. **N-5, N-6 (우선순위 4)**: 소수 리터럴 정밀도 및 대소문자 Collation 일관성 설계 및 조치.
5. **N-4, N-7, N-8, N-9, N-10, N-11, N-12 (우선순위 5)**: 개별 내장 함수 및 연산 격리 수정.
