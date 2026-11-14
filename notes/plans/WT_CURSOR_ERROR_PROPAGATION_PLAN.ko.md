# WTCursor.Next() 에러 전파 부재로 인한 사일런트 데이터 누락 방지 아키텍처 계획서
(`WT_CURSOR_ERROR_PROPAGATION_PLAN.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-11-14
> - **최종 갱신일**: 2026-11-14
> - **과제 식별 코드**: `FIX-WT-CURSOR-ERROR-PROPAGATION-1` (결함 108)
> - **대상 마일스톤**: `1.0-GA` (제1그룹 최우선 쇼스토퍼 P1)
> - **관련 파일**: `pkg/storage/engine.go`, `pkg/storage/wiredtiger/wt_engine.go`, `pkg/storage/memengine/tx.go`, `pkg/storage/slice_cursor.go`, `pkg/execution/scan.go`
> - **연계 규칙**: `[RULE:ExplicitError]`, `[RULE:AtomicSaves]`, `[Invariant 9: Fail-Closed Recovery]`, `[RULE:PipelineVerification]`

---

## 1. 개요 및 배경 (Executive Summary)

LionSQL의 WiredTiger 스토리지 커서(`WTCursor`, `wtIndexBTreeCursor`)는 테이블 스캔 및 인덱스 범위 탐색의 핵심 진입점입니다.  
그러나 기존 `Next() bool` 메서드는 C API `cursor->next(c.cur)`의 반환값이 0이 아니면, 그것이 정상적인 테이블 끝(`C.WT_NOTFOUND`)인지 아니면 I/O 장애, 온디스크 CRC 손상(`C.WT_PANIC`), 동시성 트랜잭션 롤백/충돌(`C.WT_ROLLBACK`), 메모리 고갈(`ENOMEM`)인지 전혀 구분하지 않고 일괄적으로 `c.eof = true`로 처리하여 루프를 조용히 탈출하고 있었습니다.

이로 인해 스토리지 읽기 에러가 발생해도 상위 쿼리 엔진(`SELECT COUNT(*)`, 집계 연산 등)이 오류를 인지하지 못하고 **마치 테이블이 정상적으로 끝난 것처럼 오염된 부분 데이터(Partial Data)를 사용자에게 정상 결과로 반환하는 치명적인 사일런트 데이터 누락/왜곡(Silent Data Omission)**이 발생합니다.

본 계획은 Go 표준 라이브러리(`database/sql.Rows.Err()`, `bufio.Scanner.Err()`) 규격에 맞춰 `storage.Cursor` 인터페이스에 `Err() error` 메서드를 확립하고, 비정상 에러 시 Fail-Closed로 쿼리를 즉시 실패 처리하도록 전면 개편합니다.

---

## 2. 근본 원인 및 문제 메커니즘 (Root Cause)

### 2.1 WiredTiger C API 반환 코드 체계
- `ret == 0`: 다음 행 탐색 성공 (정상)
- `ret == C.WT_NOTFOUND (-31803)`: B+Tree/테이블 끝 도달 (정상 EOF)
- `ret != 0 && ret != C.WT_NOTFOUND`: **심각한 런타임 오류**
  - `C.WT_ROLLBACK`: MVCC 트랜잭션 충돌, 캐시 퇴출 압박, 데드락
  - `C.WT_PANIC`: 온디스크 블록 손상, 하드웨어 I/O 실패
  - `ENOMEM`, `EINVAL`: 리소스 고갈

### 2.2 기존 코드의 치명적 결함
```go
// 기존 wt_engine.go:3347
if C.go_wt_cursor_next(c.cur) != 0 {
    c.eof = true
    return false // WT_NOTFOUND와 WT_ROLLBACK/WT_PANIC/I/O에러를 전혀 구분하지 않고 조용히 삼킴!
}
```

---

## 3. 해결 아키텍처 및 세부 설계 (Architecture Design)

### 3.1 `storage.Cursor` 인터페이스 확장 (`pkg/storage/engine.go`)
```go
type Cursor interface {
    Next() bool
    Record() (Record, error)
    Row() (Row, error)
    Close() error
    Err() error // 순회 중 발생한 비정상 오류 반환 (정상 EOF 시 nil)
}
```

### 3.2 `WTCursor` 및 `wtIndexBTreeCursor` 에러 전파 (`pkg/storage/wiredtiger/wt_engine.go`)
- 커서 구조체에 `err error` 필드 추가.
- `Next()` 순회 도중 `C.go_wt_cursor_next` / `C.go_wt_cursor_prev` / `C.go_wt_cursor_search_near` 호출:
  - `ret == 0`: return true
  - `ret == C.WT_NOTFOUND`: `c.eof = true`, return false
  - `ret != 0 && ret != C.WT_NOTFOUND`:
    - `c.eof = true`
    - `c.err = fmt.Errorf("wiredtiger cursor scan error: %d (%s)", ret, C.GoString(C.wiredtiger_strerror(ret)))`
    - return false
- `Err() error` 메서드: `return c.err`

### 3.3 기타 모든 `storage.Cursor` 구현체 정합성 준수
- `pkg/storage/memengine/tx.go` (`memCursor`): `func (c *memCursor) Err() error { return nil }`
- `pkg/storage/slice_cursor.go` (`SliceCursor`): `func (c *SliceCursor) Err() error { return nil }`
- 단위 테스트 모의 커서(`faultyCursor`): `func (fc *faultyCursor) Err() error { return fc.err }`

### 3.4 쿼리 엔진 및 스캔 파이프라인 에러 점검 확립
- `pkg/execution/scan.go`: `!cursor.Next()` 발생 시 `if err := cursor.Err(); err != nil { return err }` 점검.
- `pkg/engine/`의 핵심 스캔 루프에서 `cur.Err()` 사일런트 방어.

---

## 4. 검증 계획 (Verification Strategy)

1. **단위 및 회귀 테스트 (`pkg/storage/wiredtiger/wt_cursor_err_test.go`)**:
   - `WTCursor`가 정상 스캔 완료 시 `cur.Err() == nil`임을 검증.
   - 모의 결함 커서 또는 유효하지 않은 커서 호출 시 `cur.Err() != nil`이 정확히 전파됨을 검증.
2. **Fail-Closed 무결성 검증**:
   - `[RULE:PipelineVerification]`: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
   - `go test -v -race ./pkg/storage/...` 100% PASS 확인.
