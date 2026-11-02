# 세션 로컬 에페머럴 CTE 버퍼 아키텍처 및 Zero-DDL 최적화 계획 (Session Ephemeral CTE Buffer Plan)

> - **최초 작성일**: 2026-11-02
> - **상태**: In Progress
> - **대상 모듈**: `pkg/engine/sqlexec_explain.go`, `pkg/engine/session.go`, `pkg/engine/sqlexec_select.go`, `pkg/storage/slice_cursor.go`

---

## 1. 배경 및 문제 진단

### 1.1 현상
Grove 벤치마크 환경에서 다중 Writable CTE 쿼리(`ReserveWithTxReturning`, `CreateWithOutbox`)를 실행할 때:
- **단일 워커**: 건당 35ms (약 28.2 TPS)로 정상 실행 및 데이터 정합성 검증 완료.
- **50 워커 동시성**: 처리량이 26 TPS로 정체되고, 지연시간이 정확히 $35\text{ms} \times 50 \approx 1,500\text{ms}$로 선형 증가.

### 1.2 근본 원인 (물리적 DDL 전역 락 직렬화)
1. **쿼리당 2~4회 카탈로그 DDL 실행**:
   - `sqlexec_explain.go:execCTE`는 CTE 결과를 임시 보관하기 위해 매 쿼리마다 `CREATE TABLE #__cte_<name>_<seq> (...)` 및 `defer DROP TABLE ...`을 호출함.
2. **스토리지 엔진 뮤텍스 및 WiredTiger 전역 스키마 락**:
   - `s.engine.CreateTable` 및 `DropTable`은 엔진 전역 뮤텍스 `e.mu.Lock()`을 획득함.
   - 특히 WiredTiger 엔진(`pkg/storage/wiredtiger/wt_engine.go`)에서는 CGo `WT_SESSION::create` 및 `drop`을 통해 물리적 `.wt` 파일 생성/삭제를 수행하며 엔진 내부의 전역 스키마 락을 독점함.
3. **행 단위 동시성 마비**:
   - LionSQL의 스토리지 계층은 이미 OCC/MVCC 로우 레벨 동시성을 갖추고 있으나, CTE 구체화 계층이 물리 카탈로그 DDL을 거치면서 모든 워커가 단일 큐로 직렬화(Serialization)됨.

---

## 2. 목표 아키텍처 (방안 A: 세션 로컬 에페머럴 CTE 버퍼)

CTE 결과는 **해당 쿼리/세션에서만 읽히는 단기 불변(Immutable) 데이터**이므로, 물리 엔진 카탈로그(`storage.Engine`)에 등록할 이유가 전혀 없습니다.

```
[ 기존 경로 (Bottleneck) ]
CTE 실행 -> SQL CREATE TABLE #temp -> Engine.mu.Lock() -> CGo WT_SESSION::create (.wt 파일 생성)
         -> SQL INSERT INTO #temp (N회)
         -> 메인 쿼리 실행
         -> SQL DROP TABLE #temp -> Engine.mu.Lock() -> CGo WT_SESSION::drop (.wt 파일 삭제)

[ 신규 경로 (Zero-DDL In-Memory Ephemeral) ]
CTE 실행 -> *QueryResult 획득
         -> cs.RegisterEphemeralTable(tempName, schema, records) (순수 Go 맵 적재, 0-Lock, 0-CGo)
         -> 메인 쿼리: getTable(cs, tempName) -> 세션 맵 반환
                      tx.Scan(tempName)      -> SliceCursor(records) 순수 메모리 스트리밍
         -> defer cs.UnregisterEphemeralTable(tempName) (맵 삭제, 0-Lock, 0-CGo)
```

---

## 3. 세부 설계 및 구현 컴포넌트

### 3.1 `storage.SliceCursor` 범용 인메모리 커서 표준화
- 위치: `pkg/storage/record.go` 또는 `pkg/storage/slice_cursor.go`
- `storage.Cursor` 인터페이스(`Next()`, `Row()`, `Record()`, `Close()`)를 만족하는 가벼운 슬라이스 반복자 제공.
- 기존 `wiredtiger/wtSliceCursor`와 통합하거나 호환되도록 표준화.

### 3.2 `ClientSession` 에페머럴 테이블 저장소 확장
- 위치: `pkg/engine/session.go`
- 필드 추가:
  ```go
  type EphemeralTable struct {
      Schema *storage.TableSchema
      Rows   []storage.Row
      Recs   []storage.Record
  }

  // ClientSession struct:
  EphemeralTables map[string]*EphemeralTable
  ```
- 메서드:
  - `RegisterEphemeralTable(name string, schema *storage.TableSchema, rows []storage.Row, recs []storage.Record)`
  - `GetEphemeralTable(name string) *EphemeralTable`
  - `AppendEphemeralRows(name string, rows []storage.Row, recs []storage.Record)` (재귀 CTE 지원)
  - `UnregisterEphemeralTable(name string)`

### 3.3 `Coordinator` 스키마 및 트랜잭션 라우팅
- 위치: `pkg/engine/coordinator_impl.go`, `pkg/engine/sqlexec_select.go`
- `s.getTable(cs *ClientSession, name string) (*storage.TableSchema, error)`:
  - `cs != nil`이고 `cs.GetEphemeralTable(name)`이 존재하면 해당 스키마 즉시 반환 (엔진 카탈로그 락 회피).
  - 없으면 `s.engine.GetTable(name)` 폴백.
- `sessionTxWrapper`:
  - `Scan(tableName string)` 시 `cs.GetEphemeralTable(tableName)`이 있으면 `NewSliceCursor` 즉시 반환.
  - 없으면 기저 `Transaction.Scan(tableName)` 호출.

### 3.4 `execCTE` 전면 Zero-DDL 리팩토링
- 위치: `pkg/engine/sqlexec_explain.go`
- `CREATE TABLE`, `INSERT INTO`, `DROP TABLE` 문자열 생성 및 `s.execSingleStatement` 호출 코드 전면 제거.
- `res`(`*QueryResult`)로부터 컬럼 정의를 유추하여 `storage.TableSchema`를 인메모리 빌드하고, `res.Rows` / `res.Values`를 `storage.Record`로 변환하여 `cs.RegisterEphemeralTable`로 직접 등록.
- 재귀 CTE(Recursive CTE) 루프에서는 `cs.AppendEphemeralRows`로 슬라이스 확장.

---

## 4. 검증 계획 (3-Stage Verification)

1. **단위 및 회귀 테스트**:
   - `pkg/engine/writable_cte_test.go`: 기존 Grove Writable CTE 체크리스트(다중 CTE, 아웃박스 패턴, 롤백 정합성, 재고 차감) 100% 통과 확인.
   - 재귀 CTE 테스트(`pkg/engine/cte_test.go` 등) 무결성 검증.
2. **동시성 벤치마크 검증**:
   - 50개 고루틴 동시 Writable CTE 실행 테스트를 신설하여 엔진 락 경합 없이 처리량이 수천 TPS로 확장되는지 확인.
3. **파이프라인 통과**:
   - `go build -tags wiredtiger -buildvcs=false ./...`
   - `go test -tags wiredtiger -count=1 -v -race ./pkg/engine/...`
   - 전체 패키지 `go test -tags wiredtiger -count=1 -race ./...`
