# 성능 회귀 정상화 및 회귀 방지 게이트 구축 계획서 (`notes/PERF_REGRESSION_RECOVERY_AND_GATE_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-02
> - **상태**: 진행 중 (In Progress)
> - **과제 식별 코드**: `PERF-REGRESSION-RECOVERY-1` (세부 과제: `CLEAN-KEY-LATCH-1`, `CLEAN-RUN-RETRY-1`, `VERIFY-BENCH-RESTORE-1`, `TEST-PERF-GATE-1`)
> - **영향 범위**: `pkg/storage/`, `pkg/proc/runtime/`, `pkg/engine/`

---

## 1. 추진 배경 및 당위성

### 1.1 현상 진단
10월 24일 커밋 `f14eed7` 기준 `doc/BENCHMARK.md`에 공식 기록된 고동시성 OLTP 벤치마크 결과 대비, 현재 코드베이스에서 심각한 성능 역주행(Regression)이 관측되었습니다:
- **3.3.4 송금 (WiredTiger)**: 120,629 TPS ➔ **4,492 TPS (-96.3% 대폭락)**
- **3.3.4 송금 (In-Memory OCC)**: 186,766 TPS ➔ **120,857 TPS (-35.3% 하락)**
- **3.5 TPC-C 32Wrk (WiredTiger)**: 691k tpmC ➔ **389k tpmC (-43.7% 폭락)**
- **3.5 TPC-C 32Wrk (In-Memory OCC)**: 603k tpmC ➔ **546k tpmC (-9.4% 하락)**

### 1.2 근본 원인 (Root Cause)
Grove 벤치마크(단일 행 재고 핫스팟)를 영속 디스크 엔진(WiredTiger)이 아닌 순수 In-Memory OCC 엔진 기준으로 튜닝하려다 다음과 같은 치명적 안티패턴이 전역에 삽입되었습니다:
1. **`KeyLatchManager` 전역 도입**:
   - 모든 `OpUpdate`마다 2048-스트라이프 뮤텍스를 강제하여, `TransferFunds`와 TPC-C 같은 다중 행 트랜잭션에서 전형적인 **AB-BA 데드락/락 역전** 발생.
   - 데드락 회피용 타임아웃에 `time.Sleep` 폴링이 들어가면서 macOS 커널 스케줄러 퀀텀(1~2ms) 단위로 고루틴이 대량 수면(Thundering Herd)에 빠짐.
2. **`Executor.RunContext` 내 `time.After` 슬립 재시도**:
   - 벤치마크 하네스가 이미 재시도를 관리함에도 엔진 내부에서 충돌 시 `time.After`로 강제 수면을 취하여 전체 워커가 멈추는 Convoy Stall 유발.
3. **WiredTiger 본래의 동시성 훼손**:
   - WiredTiger의 Lock-Free Hazard Pointer MVCC B+Tree 상위에 불필요한 Go 래치를 강제하여 CGo/B+Tree 네이티브 속도 파괴.

---

## 2. 4단계 완치 및 회귀 방지 아키텍처

```
┌────────────────────────────────────────────────────────────────────────┐
│                   PERF-REGRESSION-RECOVERY-1 로드맵                     │
├────────────────────────────────────────────────────────────────────────┤
│  Phase 1. [래치 완전 제거] KeyLatchManager 및 AcquireKeyLatch 전량 삭제 │
│           (CLEAN-KEY-LATCH-1: 원래의 순수 Lock-Free OCC/MVCC 복원)     │
│                                  │                                     │
│  Phase 2. [슬립 루프 제거] RunContext time.After 강제 수면 루프 제거    │
│           (CLEAN-RUN-RETRY-1: 마이크로초 즉시 반환 / 하네스 위임)       │
│                                  │                                     │
│  Phase 3. [성능 베이스라인 복원 실측] 186k / 120k / 690k tpmC 복원 확인  │
│           (VERIFY-BENCH-RESTORE-1: TransferFunds & TPC-C 전수 검증)    │
│                                  │                                     │
│  Phase 4. [영구 회귀 방지 게이트] 3-Stage Pipeline 내 경량 성능 테스트   │
│           (TEST-PERF-GATE-1: go test 시 최소 임계 TPS 미달 시 즉시 차단) │
└────────────────────────────────────────────────────────────────────────┘
```

### Phase 1: `CLEAN-KEY-LATCH-1` 키 래치 전량 삭제 (Radical KISS)
- `pkg/storage/key_latch.go` 및 `key_latch_test.go` 제거 (또는 더미화).
- `storage.Transaction` 인터페이스에서 `AcquireKeyLatch` 제거 (또는 no-op 유지 후 정리).
- `pkg/storage/memengine/tx.go` 및 `pkg/storage/wiredtiger/wt_engine.go`에서 래치 획득/해제 로직 전량 삭제.
- `pkg/proc/runtime/vm.go`, `vm_dispatcher.go`, `pkg/engine/sqlexec_dml.go`에서 `AcquireKeyLatch` 호출 완전 제거.

### Phase 2: `CLEAN-RUN-RETRY-1` `RunContext` 슬립 재시도 루프 제거
- `pkg/proc/runtime/vm.go`의 `RunContext`를 10월 24일 본래의 단일 실행 구조로 복원.
- 트랜잭션 충돌 시 `time.After`로 수면하지 않고 즉시 롤백 및 에러 반환.
- 클라이언트/벤치마크 하네스가 자체 전략으로 재시도하도록 권한 분리.

### Phase 3: `VERIFY-BENCH-RESTORE-1` 성능 복원 실측 검증
- `cmd/bench`:
  - In-Memory OCC 100 워커: **180k+ TPS** (P50 < 30µs)
  - WiredTiger 100 워커: **120k+ TPS** (P50 < 30µs)
- `cmd/tpcc_runtime`:
  - 16 Wh, 32 Wrk: In-Memory OCC **600k+ tpmC**, WiredTiger **650k+ tpmC**

### Phase 4: `TEST-PERF-GATE-1` 3단계 파이프라인 내 초경량 성능 회귀 게이트 구축
- `pkg/engine/perf_regression_gate_test.go` 신설:
  - `go test ./...` 실행 시 단 0.1~0.2초 동안 10,000건의 인-메모리 송금을 고속 실행.
  - 처리량이 기준치(예: 최소 80,000 TPS) 미만으로 떨어지면 `t.Fatalf`로 즉시 빌드/테스트 실패.
  - 향후 어떠한 변경도 성능을 갉아먹는 코드는 커밋 단계(Rule 16)를 통과하지 못하도록 물리적 차단.

---

## 3. 검증 파이프라인 준수 계획
1. **Stage 1 (Build)**: `go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench`
2. **Stage 2 (Test)**: `go test -tags wiredtiger -count=1 -v -race ./...` (0 data races, 100% PASS, 성능 게이트 통과)
3. **Stage 3 (Commit)**: `refactor(storage,proc): remove key latch and retry sleep to restore 186k/120k tps and establish perf gate (PERF-REGRESSION-RECOVERY-1)`
