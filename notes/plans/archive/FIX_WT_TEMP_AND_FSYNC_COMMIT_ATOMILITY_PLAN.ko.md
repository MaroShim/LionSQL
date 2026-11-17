# FIX-HYBRID-TX-SPLIT-BRAIN-1 & FIX-WT-FSYNC-FAIL-CLOSED-1 설계 및 구현 계획

## 1. 개요 및 배경
WiredTiger 영구 스토리지 엔진과 인메모리 임시 테이블 엔진(`tempMemEngine`) 간의 하이브리드 커밋 원자성 결함과, WiredTiger의 `FsyncAlways` 정책 하에서 디스크 fsync 실패 시 발생하는 팬텀 커밋(Phantom Commit) 및 데이터 분할(Split-Brain) 위험을 방어한다.

---

## 2. 세부 설계 및 해결 방안

### 과제 1. 하이브리드 트랜잭션 2PC 부재로 인한 분할 커밋 방지 (`FIX-HYBRID-TX-SPLIT-BRAIN-1`)
- **문제점**:
  - `WTTx.Commit()`에서 `C.go_wt_session_commit`이 성공한 후 `tempTx.Commit()`이 실패하면, 이미 커밋된 WiredTiger 영구 테이블은 롤백할 수 없어 영구 데이터와 임시 테이블 간 트랜잭션 원자성이 파괴됨.
- **해결 방안 (2-Phase Validation)**:
  1. `storage.Transaction` 인터페이스 또는 `memengine.Transaction`에 `Validate() error` 메소드 구현:
     - OCC 버전 검증 및 충돌 여부 확인
     - Unique 인덱스 키 중복 충돌 사전 검사
     - 분할 커밋 실패 가능성 100% 사전 차단
  2. `WTTx.Commit()` 실행 순서 재편:
     - **Phase 1 (Pre-Commit Validation)**: `tx.tempTx != nil`인 경우 `tempTx.Validate()`를 먼저 실행. 검증 실패 시 WiredTiger C API 커밋에 진입하지 않고 양쪽 모두 즉시 롤백(`Rollback`).
     - **Phase 2 (WT Permanent Commit)**: `C.go_wt_session_commit` 실행. 실패 시 `tempTx.Rollback()` 실행 후 에러 반환.
     - **Phase 3 (Guaranteed Temp Commit)**: 사전 검증을 완결한 `tempTx.Commit()`을 실행하여 메모리 샤드에 안전하게 반영.

---

### 과제 2. WiredTiger Fsync 실패 시 Fail-Closed Taint 격리 (방안 B, `FIX-WT-FSYNC-FAIL-CLOSED-1`)
- **문제점**:
  - `C.go_wt_session_commit`은 더티 페이지를 WiredTiger 캐시와 메모리 로그에 반영하여 다른 세션에 변경 내용을 즉시 노출함.
  - 직후 `eng.syncUpTo`(`log_flush(sync=on)`)가 디스크 I/O 오류로 실패하면, 호출자는 커밋이 실패했다고 인지하지만 메모리상에는 이미 변경 데이터가 살아있어 팬텀 커밋 및 재시작 시 데이터 손상(Split-Brain)이 발생함.
- **해결 방안 (Fail-Closed Taint 격리)**:
  1. `pkg/storage/errors.go`에 `ErrEngineTainted` 정의:
     - `var ErrEngineTainted = errors.New("storage: engine is tainted due to unrecoverable I/O or fsync failure")`
  2. `Engine` 구조체(`wt_engine.go`)에 Taint 상태 관리 추가:
     - `tainted atomic.Bool`
     - `taintMu sync.RWMutex`
     - `taintErr error`
     - `markTainted(err error)`: 최초 발생한 디스크 동기화 실패 에러를 보존하고 `tainted`를 `true`로 설정.
     - `checkTainted() error`: Tainted 상태인 경우 모든 신규 트랜잭션 쓰기 및 커밋을 즉시 거부 (`[RULE:ExplicitError]`, `[RULE:AtomicSaves]`).
  3. `WTTx.Commit()`에서 fsync 실패 시:
     - `eng.markTainted(err)` 호출.
     - 반환 에러: `fmt.Errorf("%w: wiredtiger fsync failed: %v", storage.ErrEngineTainted, err)`.
  4. 이후 모든 `BeginTx(write)`, `Insert`, `Update`, `Delete`, `Commit`은 즉시 `ErrEngineTainted`를 반환하여 데이터베이스 오염을 원천 차단.

---

## 3. 검증 전략 (`[RULE:PipelineVerification]`)
1. **단위 테스트**:
   - `pkg/storage/wiredtiger/hybrid_commit_atomicity_test.go`:
     - 임시 테이블에 Unique 충돌을 유발하는 데이터를 넣고 커밋 시도 시, WiredTiger 영구 테이블이 커밋되지 않고 완전 롤백되는지 검증.
   - `pkg/storage/wiredtiger/fsync_taint_test.go`:
     - Fsync 실패 시 엔진이 `Tainted` 상태로 전환되고 이후의 모든 쓰기 요청이 `ErrEngineTainted`로 거절되는지 검증.
2. **파이프라인 검증**:
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
   - `go test -v -race ./pkg/storage/memengine/...`
   - `go test -v -race -tags wiredtiger ./pkg/storage/wiredtiger/...`
