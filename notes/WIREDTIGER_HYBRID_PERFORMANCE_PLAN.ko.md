# WiredTiger 엔진 하이브리드 성능 최적화 아키텍처 계획서

> **작성일**: 2026-11-23  
> **상태**: 승인 및 1단계 착수  
> **마일스톤**: v0.95.0 하이브리드 스토리지 가속  
> **준수 규칙**: `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`, `[RULE:ZeroAllocation]`, `[RULE:RadicalKISS]`, `[RULE:PipelineVerification]`

---

## 1. 배경 및 문제 정의

LionSQL v0.94 벤치마크 평가 결과:
- **In-Memory OCC(Compact 모드)**: 제로-카피 Read Cache(`readCache`), ArenaRowCursor, 2,304 락 샤드 분할에 힘입어 `tpcc-kernel`에서 215만 tpmC(76.8k TPS), `tpcc-runtime`에서 90.6만 tpmC를 달성하며 v0.92 대비 +102.2% 폭증.
- **WiredTiger MVCC 디스크 엔진**: 1024 락 샤드 및 배치 언락 적용 후 60.8만 tpmC(200 워커)로 회복되었으나, 매 트랜잭션마다 CGO 경계 호출(`C.go_wt_cursor_get`) 및 Go 런타임 힙 할당(`C.GoBytes`), 중복 역직렬화(`decodeRow`) 오버헤드로 인해 인메모리 엔진 대비 격차가 존재함.

따라서 Compact 엔진에서 검증된 메모리 가속 및 하이브리드 기법을 WiredTiger 트랜잭션 라이프사이클에 이식하여, CGO 오버헤드를 극소화하고 트랜잭션 처리량을 비약적으로 향상시키고자 함.

---

## 2. 3단계 하이브리드 로드맵

```
+-------------------------------------------------------------------------------+
|                    LionSQL WiredTiger Hybrid Storage Pipeline                 |
+-------------------------------------------------------------------------------+
| [1단계: 즉시 적용]                                                             |
| 트랜잭션 로컬 제로-카피 Read Cache (WTTx.readCache)                            |
|  - 동일 트랜잭션 내 중복 CGO cursor_get 호출 제거                                |
|  - C.GoBytes 동적 힙 할당 제거 및 0ns 메모리 캐시 반환                            |
|                                                                               |
| [2단계: 중기 적용]                                                             |
| sync.Pool / Arena 기반 고정 버퍼 디코딩                                        |
|  - C.GoBytes 신규 메모리 할당 대신 고정 스크래치 버퍼 재사용                      |
|  - 스캔 및 다중 행 읽기 시 Go GC Mark-Sweep 압박 완화                           |
|                                                                               |
| [3단계: 고급 적용]                                                             |
| In-Memory Private Workspace 버퍼링 (OCC-on-WiredTiger)                         |
|  - 트랜잭션 진행 중에는 Go 인메모리 맵에 변경사항 버퍼링                         |
|  - Commit 시점에만 WiredTiger C B-Tree에 순차 일괄(Bulk) 반영                    |
|  - 트랜잭션 롤백 비용 0ns 달성 및 C 커널 락 경합 제거                            |
+-------------------------------------------------------------------------------+
```

---

## 3. 세부 설계 및 사양

### 3.1. 1단계: 트랜잭션 로컬 제로-카피 Read Cache (`WTTx.readCache`)
- **대상 파일**: `pkg/storage/wiredtiger/wt_engine.go`
- **구조체 확장**:
  ```go
  type wtCachedRow struct {
      row storage.Row
      rec storage.Record
  }

  type WTTx struct {
      // ... 기존 필드 ...
      readCache map[string]wtCachedRow // "tableName\x00pkStr" -> cached row/record
  }
  ```
- **조회 흐름 (`Get` / `GetRow`)**:
  1. `tableName + "\x00" + pkStr`로 캐시 키 생성.
  2. `tx.readCache[cacheKey]` 조회:
     - 캐시 적중 시: CGO 호출 없이 캐시된 `storage.Row` 또는 `storage.Record` 즉시 반환 (0ns, zero allocation).
  3. 캐시 미스 시:
     - 기존 CGO `getRawBytesLocked()` 및 `decodeRow()` / `decodeRecord()` 수행.
     - 결과를 `tx.readCache[cacheKey]`에 저장.
- **수정/삭제 무효화 (`Insert` / `Update` / `Delete`)**:
  - `UpdateRow` / `Update`: 캐시 갱신 또는 무효화.
  - `Delete`: 캐시 키 `delete(tx.readCache, cacheKey)`.
- **풀링 및 메모리 해제 (`resetForPoolLocked` / `releaseToPoolLocked`)**:
  - 트랜잭션 종료 시 `clear(tx.readCache)` 수행.
  - 캐시 크기가 128개를 초과했을 경우 `tx.readCache = make(...)`로 슬라이스/맵 축소 (`[RULE:BoundedBuffers]`).

### 3.2. 2단계: sync.Pool / Arena 기반 고정 버퍼 디코딩
- `getRawBytesLocked`에서 매번 `C.GoBytes()`를 호출하여 새로운 Go 힙 슬라이스를 생성하는 대신, `sync.Pool`에서 대여한 스크래치 슬라이스에 메모리를 복사하여 디코딩 후 즉시 반환.
- 대용량 스캔 쿼리 시 Go GC 부하 대폭 절감.

### 3.3. 3단계: In-Memory Private Workspace 버퍼링 (OCC-on-WiredTiger)
- 트랜잭션 라이프사이클 동안 발생하는 모든 Write(Insert/Update/Delete)를 Go 레벨 메모리 맵(`tx.local[table][pk]`)에만 유지.
- `Commit()` 호출 시점에만 테이블 순서대로 일괄 B-Tree 커서 작업 수행.
- 중간 실패 및 충돌 롤백 시 WiredTiger C 커널과 상호작용하지 않고 Go 맵만 비움으로써 초고속 롤백 및 격리성 달성.

---

## 4. 검증 파이프라인 (3-Stage Verification)

1. **Stage 1 (Build)**:
   ```bash
   go build -buildvcs=false -o bin/lionsql ./cmd/lionsql
   go build -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
   ```
2. **Stage 2 (Test & Benchmark)**:
   - 레이스 검증: `go test -v -race ./pkg/storage/wiredtiger/...`
   - 정합성 검증: `go test -v ./pkg/proc/runtime/...`
   - 벤치마크: `./bin/tpcc-runtime -engine=wiredtiger -warehouses=10 -workers=100 -duration=15s`
3. **Stage 3 (Commit)**:
   - Conventional Commits 형식 (`perf(storage/wiredtiger): implement transaction-local zero-copy readCache`) 및 무이모지 원칙 준수 (`[RULE:StyleIntegrity]`).
