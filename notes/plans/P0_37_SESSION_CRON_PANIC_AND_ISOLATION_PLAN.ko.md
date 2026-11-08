# P0-37 전사 세션 패닉 방어 및 크론 격리 실행 아키텍처 설계서

> - **최초 작성일**: 2026-11-08
> - **상태**: 승인 대기 / TDD 착수 예정
> - **대상 결함**: `P0-37` (`pkg/api/cron.go:126-152`, `pkg/engine/sqlexec.go:332`)
> - **관련 규칙**: `[RULE:PanicRecovery]`, `[RULE:ZeroOrphan]`, `[RULE:CleanTeardown]`, `[RULE:BoundedBuffers]`, `[RULE:RadicalKISS]`

---

## 1. 배경 및 문제 분석

### 1.1 초기 감사 의견에 대한 정정 (Negative Knowledge)
초기 감사 보고서(`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`)에서는 크론 작업의 고아/OOM 방지를 위해 `exec.CommandContext` 기반의 외부 자식 프로세스(Child Process) 격리 실행 모델을 제안하였으나, 다음과 같은 엔진 코어 원칙으로 인해 이는 배제되어야 마땅한 오버스펙임이 확인되었습니다:
1. **스토리지 락 충돌**: LionSQL은 프로세스당 단일 카탈로그/데이터 디렉토리 불변식(`Invariant 2`)을 엄격히 준수하며, WiredTiger 스토리지 엔진은 물리 파일 락(`WiredTiger.lock`)으로 인해 동일 데이터 디렉토리에 대해 두 번째 프로세스의 기동이 원천 불가합니다.
2. **기능의 본질**: LionSQL 크론은 OS 쉘 스크립트를 실행하는 것이 아니라, 데이터베이스 내부의 SQL(`DELETE FROM sessions ...`) 또는 저장 프로시저(`EXEC proc`)를 실행하는 **인-데이터베이스(In-DB) 스케줄러**입니다.

### 1.2 실제 식별된 4대 핵심 결함
1. **크론 고루틴 및 전사 세션 패닉 복구 부재 (`[RULE:PanicRecovery]`)**:
   - `cron.go:executeJob` 및 `sqlexec.go:ExecuteSessionSQLContext` 최상단에 `defer recover()`가 누락되어 있습니다.
   - 복합 쿼리, nil 포인터 역참조, 0 나누기 등 예기치 않은 런타임 패닉 발생 시 메인 DB 데몬 전체가 즉사(Crash)합니다.
2. **크론 고루틴 고아 방치 및 셧다운 동기화 부재 (`[RULE:ZeroOrphan]`, `[RULE:CleanTeardown]`)**:
   - `cron.go:evaluate`에서 `go cs.executeJob(ctx, job)` 실행 시 `cs.wg.Add(1)` 추적이 누락되어 있습니다.
   - `cs.Stop()` 호출 시 실행 중인 작업 고루틴들이 내팽개쳐져 DB 셧다운 중 스토리지를 오염시킵니다.
3. **작업 중복 실행(Job Overlapping) 가드 부재**:
   - 1분 주기 작업이 슬로우 쿼리로 30초 이상 지연될 때 다음 1분이 도래하면, 동일한 작업의 고루틴이 무한 증식하여 커넥션/락 고갈을 유발합니다.
4. **타임아웃 및 해상도 불일치**:
   - 크론 스케줄러의 최소 해상도는 1분(`minuteKey: YYYY-MM-DD HH:MM`)입니다.
   - 작업 실행 타임아웃이 30초로 보장되지 않으면 주기 간 간섭이 발생합니다.

---

## 2. 아키텍처 설계

### 2.1 크론 해상도 및 타임아웃 황금비율
- **스케줄 해상도**: 표준 5-field cron (`min hour dom month dow`). 최소 실행 간격은 정확히 **1분**입니다.
- **최대 실행 타임아웃**: **30초 (`DefaultCronJobTimeout = 30 * time.Second`)**.
- **안전 버퍼**: 30초 강제 타임아웃 적용 시, 1분 주기 잡이 최악의 슬로우 쿼리에 걸리더라도 다음 회차 시작까지 **최소 30초의 리소스 회수 버퍼**가 보장됩니다.

### 2.2 전사 세션 계층 글로벌 패닉 복구 (`pkg/engine/sqlexec.go`)
모든 세션(PGWire, TDS, REST, Cron, Internal Batch)이 공유하는 공통 관문인 `ExecuteSessionSQLContext`에 글로벌 패닉 복구 훅을 장착합니다.

```go
func (s *Coordinator) ExecuteSessionSQLContext(ctx context.Context, sessionID string, query string) (res *QueryResult, err error) {
    defer func() {
        if r := recover(); r != nil {
            stackBuf := make([]byte, 4096)
            n := runtime.Stack(stackBuf, false)
            err = fmt.Errorf("runtime panic caught during query execution: %v\nstack:\n%s", r, string(stackBuf[:n]))
            res = &QueryResult{
                Success: false,
                Error:   err.Error(),
                Command: "PANIC_RECOVERED",
            }
        }
    }()
    ...
}
```

### 2.3 크론 스케줄러 계층 고도화 (`pkg/api/cron.go`)
1. **`CronJob` 구조체에 `isRunning bool` 및 개별 뮤텍스 도입**:
   - 이전 회차 실행이 아직 완료되지 않았으면 다음 회차는 안전하게 스킵(`skip`)하고 경고 로그를 기록합니다.
2. **`cs.jobWg sync.WaitGroup` 생명주기 완벽 동기화**:
   - `evaluate`에서 잡 스폰 시 `cs.jobWg.Add(1)` ➔ `executeJob` 종료 시 `defer cs.jobWg.Done()`.
   - `cs.Stop()` 호출 시 백그라운드 틱커 종료 후 `cs.jobWg.Wait()`로 모든 실행 중인 잡의 안전 종료를 보장.
3. **`executeJob` 로컬 `defer recover()` 이중 방어막**:
   - 코어 엔진 호출 전후의 스케줄러 자체 패닉까지 완벽 흡수하여 `job.LastStatus = "error"`, `job.LastError = panicMsg`로 상태 기록.

---

## 3. TDD 검증 계획 (Red ➔ Green)

### 3.1 세션 엔진 패닉 복구 단위 검증 (`pkg/engine/sqlexec_test.go`)
- 의도적으로 `panic("simulated query panic")`을 유발하는 커스텀 스칼라 함수를 등록하고 `ExecuteSessionSQLContext` 실행.
- **기대 결과**: DB 프로세스가 크래시되지 않고 에러(`runtime panic caught...`)를 정상 반환하며 후속 쿼리가 100% 정상 수행됨을 입증.

### 3.2 크론 중복 실행 방지 및 생명주기 검증 (`pkg/api/cron_test.go`)
1. **Job Overlap 방어**: 50ms 동안 슬립하는 잡에 대해 10ms 주기로 틱을 발생시켰을 때, 작업이 중복 스폰되지 않고 1회만 단독 실행됨을 검증.
2. **Graceful Teardown**: 실행 중인 크론 작업이 있을 때 `cs.Stop()`을 호출하면 작업이 누수되지 않고 정상 완료 후 리턴함을 검증.
3. **Panic Resilience**: 크론 실행 콜백에서 `panic()`이 발생해도 스케줄러가 다운되지 않고 `job.LastStatus == "error"`를 기록하며 다음 주기를 정상 수행함을 검증.

---

## 4. 기대 효과 및 불변식 준수

- **`[RULE:PanicRecovery]` 완전 준수**: LionSQL 전역의 모든 쿼리 경로에서 런타임 패닉으로 인한 프로세스 즉사 원천 차단.
- **`[RULE:ZeroOrphan]` & `[RULE:CleanTeardown]` 준수**: 크론 백그라운드 고루틴 누수 제로 및 안전 셧다운 확립.
- **`[RULE:RadicalKISS]` 준수**: 불필요한 Child Process OS 프로세스 분기 및 IPC 복잡도 배제, 순수 Go 고루틴/채널/뮤텍스 기본기로 극강의 단순성 달성.
