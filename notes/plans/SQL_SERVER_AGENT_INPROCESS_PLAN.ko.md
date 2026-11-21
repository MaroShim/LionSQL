# SQL Server Agent In-Process Scheduler 아키텍처 및 구현 계획서 (`SQL_SERVER_AGENT_INPROCESS_PLAN.ko.md`)

> **문서 메타데이터**:
> - **태스크 ID**: `FEAT-SQL-SERVER-AGENT-INPROCESS-1`
> - **마일스톤**: `v0.95.0`
> - **도메인**: `Engine`
> - **우선순위**: `P2`
> - **적용 표준**: `[RULE:PlanPersistence]`, `[RULE:RadicalKISS]`, `[RULE:ConcurrencySafety]`, `[RULE:ModularAbstraction]`

---

## 1. 아키텍처 개요 및 설계 원칙

LionSQL은 **단일 정적 바이너리(Single Standalone Executable, Zero Sidecars)** 원칙(`GEMINI.md Invariant 4`)과 **극도의 단순성(`[RULE:RadicalKISS]`)**을 최우선 가치로 삼습니다.

외부 프로세스(SQL Agent 별도 데몬)를 분리하는 것은 네트워크 폴링 부하, 연결 관리 오버헤드, 프로세스 감시 복잡성만 가중시킵니다. 이미 LionSQL 코어에는 고성능 분 단위 평가 및 고루틴 풀링을 지원하는 **`CronScheduler`**(`pkg/api/cron.go`)와 프로시저 디스패치 파이프라인이 완비되어 있으므로, 이를 직접 활용하여 **"내장 고루틴 기반 SQL Server Agent(In-Process SQL Server Agent)"**를 완성합니다.

```
[ SSMS / T-SQL Client ]
       │
       ▼
[ EXEC msdb.dbo.sp_add_job / sp_add_jobstep / sp_add_jobschedule / sp_start_job ]
       │
       ▼
[ pkg/engine/agent_procs.go (T-SQL Agent Procedures Engine) ]
       │
       ├───▶ [ Agent Storage (msdb sysjobs, sysjobsteps, sysschedules, sysjobhistory) ]
       │        │
       │        ▼
       │     [ pkg/catalog/virtual (msdb.dbo.sysjobs* 가상 뷰 실데이터 프로젝션) ]
       │
       └───▶ [ pkg/api/cron.go (CronScheduler) ]
                │ (스케줄 시점에 비동기 고루틴 트리거)
                ▼
             [ Coordinator.ExecuteSessionSQL("__sys_agent", command) ]
                │
                ▼
             [ sysjobhistory 실행 결과/상태/소요시간 원자적 기록 ]
```

---

## 2. 세부 구현 컴포넌트

### 2.1 Agent 스토리지 및 메타데이터 (`sysjobs`, `sysjobsteps`, `sysschedules`, `sysjobhistory`)
- `Coordinator.EnsureAgentTables()`:
  - `sysjobs`: `job_id (UUID)`, `name`, `enabled`, `description`, `start_step_id`, `category_id`, `date_created`, `date_modified`, `version_number`
  - `sysjobsteps`: `job_id`, `step_id`, `step_name`, `subsystem ('TSQL')`, `command`, `flags`
  - `sysschedules`: `schedule_id`, `job_id`, `name`, `enabled`, `freq_type`, `freq_interval`, `cron_expr`
  - `sysjobhistory`: `instance_id (PK)`, `job_id`, `step_id`, `step_name`, `sql_message_id`, `sql_severity`, `message`, `run_status (1=성공, 0=실패)`, `run_date (YYYYMMDD)`, `run_time (HHMMSS)`, `run_duration (초)`
- `Coordinator` 초기화 시 또는 첫 Agent 프로시저 호출 시 idempotent하게 초기화(`sync.Once`).

### 2.2 T-SQL Agent 프로시저 (`pkg/engine/agent_procs.go`)
1. `sp_add_job`:
   - `@job_name`, `@enabled` (기본 1), `@description`, `@job_id OUTPUT`
   - 동일한 job_name 존재 시 에러 반환 (`Msg 14261`).
2. `sp_add_jobstep`:
   - `@job_name` 또는 `@job_id`, `@step_name`, `@command`, `@subsystem = 'TSQL'`
   - 스텝 순서 및 커맨드 등록.
3. `sp_add_jobschedule`:
   - `@job_name` 또는 `@job_id`, `@name`, `@enabled`, `@freq_type`, `@freq_interval` (또는 `@cron_expr`)
   - `CronScheduler.AddJob`으로 스케줄 등록/동기화.
4. `sp_start_job`:
   - `@job_name` 또는 `@job_id`
   - 즉시 비동기로 잡 스텝들을 실행(`CronScheduler.RunJobNow` 또는 직접 비동기 실행).
5. `sp_delete_job`:
   - `@job_name` 또는 `@job_id`
   - 잡, 스텝, 스케줄을 삭제하고 `CronScheduler.DeleteJob` 동기화.

### 2.3 가상 카탈로그 연동 (`pkg/catalog/virtual/mssql_single.go`)
- `sysjobs`, `sysjobsteps`, `sysschedules`, `sysjobhistory` 쿼리 인터셉트 시, `Coordinator`의 실제 테이블에서 레코드를 `SELECT`하여 SSMS에 실데이터 반환.
- SSMS Object Explorer의 `SQL Server 에이전트 -> 작업` 트리에 등록된 잡과 히스토리가 실시간으로 100% 매핑.

### 2.4 실행기 및 히스토리 로거
- 잡이 실행될 때 격리된 내부 세션(`__sys_agent`)을 열고 각 스텝의 SQL을 실행.
- 실행 성공/실패, 에러 메시지, 실행 시각(`run_date`, `run_time`), 소요 시간(`run_duration`)을 `sysjobhistory`에 원자적 INSERT.

---

## 3. 검증 전략 (TDD)
1. `pkg/engine/agent_procs_test.go`:
   - `sp_add_job` -> `sp_add_jobstep` -> `sp_add_jobschedule` 전체 라이프사이클 테스트
   - `sp_start_job` 즉시 실행 후 `msdb.dbo.sysjobhistory`에 성공 이력 생성 확인
   - `SELECT * FROM msdb.dbo.sysjobs` 가상 카탈로그 실데이터 프로젝션 검증
   - `sp_delete_job` 삭제 후 스케줄 제거 및 잡 조회 불가 확인
   - 에러 시나리오: 중복 잡 생성 실패, 존재하지 않는 잡에 스텝 추가 실패, 쿼리 런타임 에러 시 `run_status = 0` 및 에러 메시지 기록 검증
2. 전체 회귀 스위트: `go test -tags wiredtiger -v -race ./pkg/engine/...`
