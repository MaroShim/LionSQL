# LionSQL 1.0-GA 최종 안정성 및 방어벽 전수 감사 계획서 (`PRE_1.0_FINAL_SAFETY_NET_AUDIT_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-12
> - **상태**: **완료 (4대 Scope 전수 감사 및 17건 방어벽 보강 완료 — 최종 보고서: [`notes/reports/PRE_1.0_FINAL_SAFETY_NET_AUDIT_REPORT.ko.md`](../../reports/PRE_1.0_FINAL_SAFETY_NET_AUDIT_REPORT.ko.md))**
> - **마일스톤**: `1.0-GA` (정식 릴리스 직전 최종 방어벽 검증)
> - **연계 태스크**: `AUDIT-FINAL-SAFETY-NET-1` (`DONE`)
> - **핵심 불변식**: `[RULE:DefensiveIndexing]`, `[RULE:ZeroOrphan]`, `[RULE:HandleHygiene]`, `[RULE:ExplicitError]`, `[RULE:LockHierarchy]`, `[RULE:PanicRecovery]`

---

## 1. 개요 및 배경 (Context & Problem)

LionSQL은 기능 동결(Feature Freeze)을 달성하고, 10대 실전 오픈소스 애플리케이션 연동, In-Memory OCC 104만 tpmC, 정규식(`regexp`) 100% 완전 척결, `strings.*` 컨텍스트 블라인드 매칭 제거를 완료하였다.

그러나 1.0 정식 릴리스를 앞두고 여전히 다음과 같은 신뢰성 불안 요소가 존재한다:
1. **동적 런타임 테스트(Soak/Crash)의 사각지대**:
   - `crashtest`, `soaktest`, SQLLogicTest 등은 핫 패스(Hot Path)의 동시성 및 내구성을 철저히 검증하지만, 극히 드물게 실행되는 **콜드 패스(Cold Path: 네트워크 이상 단절, 오기형 패킷, 디스크 I/O 순간 실패 등)**의 에러 처리 블록은 실행되지 않은 채 사각지대로 남는다.
2. **단 1건의 치명적 결함이 초래하는 파급력**:
   - 데이터베이스 엔진은 단 한 번의 언핸들드 패닉(Unhandled Panic), 단 몇 바이트의 점진적 메모리/고루틴 누수, 에러를 삼키는(`_ = err`) 침묵 실패만으로도 전체 프로덕션 시스템의 가동을 중단시키거나 데이터를 손상시킨다.

따라서 1.0 릴리스를 위한 최종 관문으로서, **기존의 동적 내구성 검증(Soak + Crash)과 상호 보완하는 "정밀 정적 전수 감사(Static Precision Safety-Net Audit)"를 가동하여 엔진 전체의 방어벽을 완벽히 봉쇄**한다.

---

## 2. 4대 결함 분류 체계 및 감사 기준 (Defect Taxonomy & Audit Rules)

이번 최종 감사는 아키텍처 개편이나 신규 기능 개발을 완전히 배제하고, **오직 안정성, 크래시 방지, 자원 무누수를 보장하기 위한 4대 방어벽 기준**에만 엄격히 집중한다.

### 2.1 Category 1: 패닉 프리 및 경계 방어 (Panic-Free & Bounds Safety)
- **적용 규칙**: `[RULE:DefensiveIndexing]`, `[RULE:PanicRecovery]`
- **점검 항목**:
  1. 슬라이스 슬라이싱(`s[start:end]`) 및 인덱스 접근(`arr[i]`) 전에 `0 <= start <= end <= len(s)` 방어벽이 100% 전제되어 있는가?
  2. 인터페이스 타입 단언(`v.(Type)`)이 패닉을 유발하는 단일 값 형태가 아닌, 안전한 2-value 형태(`val, ok := v.(Type); if !ok { ... }`)로 예외 없이 방어되어 있는가?
  3. 구조체 포인터 역참조(`ptr.Field`) 전에 `nil` 체크가 누락된 곳이 없는가?
  4. 외부 네트워크 패킷(PGWire, TDS) 파싱 계층에서 잘린(Truncated) 비정상 바이트 스트림 수신 시 패닉하지 않고 즉시 안전한 프로토콜 에러를 반환하는가?

### 2.2 Category 2: 무고아 동시성 및 핸들 위생 (Zero-Orphan & Handle Hygiene)
- **적용 규칙**: `[RULE:ZeroOrphan]`, `[RULE:HandleHygiene]`, `[RULE:DoubleCloseSafety]`
- **점검 항목**:
  1. 엔진 전역에서 생성되는 모든 백그라운드 고루틴(`go func()`)이 부모 컨텍스트(`ctx.Done()`) 또는 명시적 종료 채널(`quitCh`)에 결합되어 있는가? (부모 세션 종료 후 영구 잔류하는 고아 고루틴 0건 보장)
  2. 파일(`os.File`), 네트워크 커넥션(`net.Conn`), 리스너(`net.Listener`), 스토리지 세션/커서가 열린 즉시 `defer Close()`로 바인딩되어 있는가?
  3. 리소스 정리 함수가 다중 호출될 수 있는 경로에서 `sync.Once`를 적용하여 이중 닫힘(Double Close) 패닉을 원천 차단하고 있는가?

### 2.3 Category 3: 침묵 실패 및 에러 가시성 (No Silent Failure & Explicit Errors)
- **적용 규칙**: `[RULE:ExplicitError]`
- **점검 항목**:
  1. 스토리지 I/O, 디스크 동기화(`fsync`), 트랜잭션 커밋/롤백, 와이어 직렬화 과정에서 반환된 에러를 밑줄(`_ = err`)로 무시하거나 삼킨 지점이 단 한 줄이라도 존재하는가?
  2. 에러가 발생했을 때 로그만 남기고 정상 흐름처럼 계속 진행하여 2차 오염을 유발하는 안티패턴이 없는가? (에러 발생 시 즉시 상위로 명확한 `SQLSTATE`와 함께 버블링 전파)

### 2.4 Category 4: 데드락 및 락 계층성 (Deadlock Prevention & Lock Hierarchy)
- **적용 규칙**: `[RULE:LockHierarchy]`
- **점검 항목**:
  1. 뮤텍스(`sync.Mutex`, `sync.RWMutex`)를 획득한 상태에서 미지의 외부 콜백, 블로킹 I/O, 무제한 채널 송수신을 수행하는 지점이 있는가?
  2. 다중 락(예: 세션 락 + 카탈로그 락, 글로벌 엔진 락 + 샤드 락)을 획득하는 경로에서 전 코드베이스에 걸쳐 획득 순서가 일관되게 단방향으로 유지되는가?

---

## 3. 4대 핵심 도메인 감사 범위 (Target Audit Scopes)

코드베이스 전체(약 200개 파일)를 위험도와 계층에 따라 4대 핵심 구역으로 분할하여 단계별로 정밀 감사한다:

```
[ LionSQL 1.0-GA 최종 방어벽 4대 감사 구역 ]
                         │
      ┌──────────────────┼──────────────────┬──────────────────┐
      ▼                  ▼                  ▼                  ▼
[ Scope 1: 와이어 ]  [ Scope 2: 동시성 ]  [ Scope 3: 스토리지 ] [ Scope 4: 실행기 ]
  PGWire & TDS         세션 & 트랜잭션      In-Memory & WT       VDBE VM & CBO
  네트워크 경계 수비     고루틴 & 락 계층      디스크 I/O & 복구    타입 단언 & 인덱스
```

| 감사 구역 (Scope) | 핵심 패키지 및 대상 파일 | 중점 감사 항목 | 위험도 |
| :--- | :--- | :--- | :---: |
| **Scope 1: 와이어 프로토콜 & 네트워크 경계** | `pkg/wire/`, `pkg/wire/tds/`, `server.go`, `connection.go` | 악의적/비정상 패킷 수신 시 패닉 가드, 바이트 버퍼 슬라이스 경계 검사, 네트워크 단절 시 커넥션/소켓 누수, 고아 리더 고루틴 차단 | **최상 (P0)** |
| **Scope 2: 트랜잭션 코디네이터 & 세션 동시성** | `pkg/engine/`, `pkg/catalog/`, `session.go`, `coordinator_impl.go` | 세션 락/카탈로그 락 순서 일관성, 트랜잭션 롤백/중단 시 리소스 클린업, 취소 컨텍스트(`CancelSessionQuery`) 블로킹 방지 | **상 (P1)** |
| **Scope 3: 스토리지 엔진 & WAL 크래시 복구** | `pkg/storage/memengine/`, `pkg/storage/wiredtiger/`, `wal.go` | `_ = err` 에러 무시 전수 색출, AOF/RDB 파일 I/O 실패 시 Fail-Closed 보장, 슬랩/Arena 메모리 해제 후 참조(UAF) 방지 | **상 (P1)** |
| **Scope 4: VDBE VM & Volcano 쿼리 실행기** | `pkg/proc/runtime/`, `pkg/execution/`, `vm.go`, `project.go`, `join.go` | 레지스터 슬라이스 인덱싱 경계 방어, 런타임 산술 연산 오버플로우, 인터페이스 타입 단언 2-value 안전성, 널 안전성 | **중 (P2)** |

---

## 4. 실행 프로토콜 및 조치 원칙 (Execution Protocol)

### 4.1 감사 진행 프로세스 (Scope별 반복)
1. **정밀 정적 스캔**:
   - 해당 Scope 내 소스코드를 파일별로 정독하며 4대 결함 분류 체계(Category 1~4)에 해당하는 취약점을 전수 식별.
2. **결함 등록 및 분류**:
   - 발견된 취약점을 `Scope / 파일명:라인 / 결함 유형 / 발생 가능한 증상`으로 정형화하여 기록.
3. **방어 코드 즉각 보강**:
   - 기존 비즈니스 로직을 변경하지 않는 **순수 안전 가드(Defensive Guard)** 형태로 보강 (경계 검사, `sync.Once`, 2-value 단언, 컨텍스트 바인딩).
4. **회귀 및 가혹 테스트 검증**:
   - `go test -v -race ./...` 전수 통과 확인 및 악의적 입력/경계 조건에 대한 단위 테스트 추가.

### 4.2 완료 판정 기준 (Exit Criteria)
1. 4대 Scope 전 영역에서 Category 1(패닉 가능성), Category 2(고아 고루틴/자원 누수), Category 3(에러 무시) 결함이 **0건(Zero-Defect)**임을 확인.
2. 보강된 코드에 대해 `go test -v -race ./...` 100% PASS 및 데이터 레이스 0건 유지.
3. 최종 감사 결과를 `notes/reports/PRE_1.0_FINAL_SAFETY_NET_AUDIT_REPORT.ko.md`로 영구 문서화.

---

## 5. 로드맵 및 태스크 DB 반영

- **신규 태스크 등록**: `TASKS.md`에 `AUDIT-FINAL-SAFETY-NET-1` (LionSQL 1.0-GA 최종 방어벽 전수 감사)를 `IN_PROGRESS`로 등록.
- **후속 연계**: 본 감사가 완료된 후 `DURA-KILL-1`(크래시 500회) 및 `DURA-SOAK-1`(24시간 소크)를 가동하여 정적 감사와 동적 검증의 완벽한 듀얼 사인오프를 달성.
