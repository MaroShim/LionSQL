# Grove 벤치마크 기반 동시성 최적화 및 고난도 엔진 완성 설계 계획서 (`notes/GROVE_BENCHMARK_CONCURRENCY_OPTIMIZATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-02
> - **상태**: Phase 1~4 완료, Phase 5 설계 반영 및 구현 착수 (In Progress)
> - **과제 식별 코드**: `PERF-GROVE-CONCURRENCY-1` (세부 과제: `BUG-WIRE-OID-1700`, `PERF-HOTSPOT-LATCH-1`, `PROC-SP-INSERT-SELECT-1`, `PERF-PGWIRE-PIPELINE-1`, `PERF-RETRY-WT-ROLLBACK-1`)
> - **관련 배경 리포트**: Grove e-Commerce & ERP 복합 트랜잭션 4자 동등 비교 벤치마크 실측치 및 WiredTiger WriteConflictException 아키텍처
> - **영향 범위**: `pkg/wire/`, `pkg/engine/`, `pkg/proc/`, `pkg/storage/`

---

## 1. 추진 배경 및 Grove 벤치마크 실측 데이터 분석

실전 엔터프라이즈 전자상거래 및 회계 분개 서비스인 **Grove 프로젝트**에서 LionSQL을 대상으로 데이터베이스 초기화 후 **PostgreSQL 16(5432) vs LionSQL PGWire(5433) vs LionSQL TDS(1433)** 간의 50 동시 워커(Concurrency: 50), 100개 SKU 대상 4자 동등 벤치마크를 수행하였습니다.

### 1.1 Grove 4자 벤치마크 실측치 요약

| 벤치마크 시나리오 | 1. PostgreSQL 16 (5432)<br>`Writable CTE` | 2. LionSQL PG (5433)<br>`Writable CTE` | 3. LionSQL TDS (1433)<br>`Writable CTE` | 4. LionSQL TDS (1433)<br>**`T-SQL Stored Procedure`** |
| :--- | :---: | :---: | :---: | :---: |
| **① CreateWithOutbox**<br>(주문 + 품목 + 아웃박스) | 3,261.0 TPS<br>(P50: 11.32ms) | 1,164.1 TPS<br>(P50: 19.82ms) | 3,121.7 TPS<br>(P50: 10.52ms) | **4,614.8 TPS**<br>**(P50: 5.94ms, Min: 0.20ms)**<br>*(PG 대비 +41.5% 압도)* |
| **② ReserveWithTxReturning**<br>(조건부 재고 차감 + 수불원장) | 4,335.5 TPS<br>(P50: 8.14ms) | 481.1 TPS<br>(P50: 21.71ms) | 1,427.2 TPS<br>(P50: 17.87ms) | **1,403.3 TPS**<br>**(P50: 16.86ms, Min: 0.50ms)**<br>*(핫스팟 락 경합 병목)* |
| **③ CreateJournal**<br>(전표 헤더 + 차변/대변 2행) | 2,530.4 TPS<br>(P50: 11.40ms) | **타입 OID 스캔 에러**<br>*(바이너리 OID 1700 결함)* | 2,189.2 TPS<br>(P50: 16.29ms) | **3,196.4 TPS**<br>**(P50: 9.90ms, Min: 0.18ms)**<br>*(PG 대비 +26.3% 압도)* |

### 1.2 핵심 기술 진단 및 병목 도출

1. **T-SQL Stored Procedure의 폭발적 우위 확인 (4,614.8 TPS)**:
   - 복잡한 다중 CTE 구문을 클라이언트에서 문자열로 매번 전송/파싱하지 않고, VDBE VM 바이트코드로 직접 실행하여 **PostgreSQL 대비 +41.5% 더 높은 처리량과 200µs(0.20ms) 초저지연**을 달성함.
2. **재고 선점(ReserveStock) 1,400 TPS 수렴 병목**:
   - 100개 SKU를 50개 워커가 집중 `UPDATE`할 때, PostgreSQL은 비관적 튜플 락(Pessimistic Row Lock 큐)으로 대기 후 순차 갱신하여 4,335 TPS를 소화함.
   - LionSQL은 낙관적 MVCC/OCC 충돌 감지 시 즉시 에러(`WT_ROLLBACK` / `ErrWriteConflict`)를 내고 롤백하므로, 클라이언트가 지수 백오프 재시도를 반복하며 1,400 TPS에서 처리량이 갇힘.
3. **`CreateJournal` PG Wire 바이너리 OID 1700 결함**:
   - 금액(NUMERIC/DECIMAL) 컬럼 처리 시 PGWire 바이너리 인코더/디코더가 누락되어 raw ASCII 문자열이 전송되면서 `pgx` 드라이버 스캔 에러 발생.
4. **PG Wire(5433) vs TDS(1433) 프로토콜 직렬화 격차 (1,164 vs 3,121 TPS)**:
   - TDS는 1개의 단일 RPC 패킷으로 즉시 실행되는 반면, PGWire는 Extended Protocol의 `Describe ('D')` 단계에서 불필요한 사전 파싱/실행 및 `DataRow` 직렬화 오버헤드가 발생함.
5. **Stored Procedure VM 내 `INSERT INTO ... SELECT` 컴파일 제한**:
   - VDBE 컴파일러(`pkg/proc/compiler.go:808`)가 서브쿼리 바이트코드 브릿지 부재로 인해 명시적 에러를 반환하고 있음.

---

## 2. 4단계 완치 아키텍처 및 세부 설계

```
┌────────────────────────────────────────────────────────────────────────┐
│                   PERF-GROVE-CONCURRENCY-1 로드맵                        │
├────────────────────────────────────────────────────────────────────────┤
│  Phase 1. [긴급 버그 완치] PGWire OID 1700 (NUMERIC) 바이너리 완벽 지원   │
│           (BUG-WIRE-OID-1700: decode/formatBinary Numeric 구현)        │
│                                  │                                     │
│  Phase 2. [핫스팟 충돌 극복] 인-메모리 키 스트라이핑 뮤텍스 (Key Latch)    │
│           (PERF-HOTSPOT-LATCH-1: 1,400 TPS ➔ 4,000+ TPS 파이프라인 큐)  │
│                                  │                                     │
│  Phase 3. [SP 표현력 확장] Stored Procedure 내 INSERT ... SELECT 지원   │
│           (PROC-SP-INSERT-SELECT-1: OpExecSubquery 바이트코드 브릿지)    │
│                                  │                                     │
│  Phase 4. [프로토콜 파이프라인 최적화] PGWire Describe 캐싱 및 제로 카피 │
│           (PERF-PGWIRE-PIPELINE-1: 1,164 TPS ➔ 3,000+ TPS 단축)        │
└────────────────────────────────────────────────────────────────────────┘
```

---

### Phase 1: `BUG-WIRE-OID-1700` PGWire OID 1700 (NUMERIC/DECIMAL) 바이너리 인/디코더 완치

#### 1.1 현상 및 근본 원인
- `pkg/wire/server.go:1047` (`decodeBinaryParamValue`): OID 1700 케이스가 누락되어 클라이언트가 보낸 바이너리 금액을 `storage.BinaryValue` 바이트열로 수신함.
- `pkg/wire/server.go:2457` (`formatColumnValue`): 바이너리 모드(`isBinary == true`)에서 OID 1700 케이스가 없어 `default`로 빠져 ASCII 문자열(`"150.00"`)을 반환함.
- `pgx` 드라이버는 OID 1700 바이너리 형식을 기대하므로 `"150.00"` 바이트를 파싱하다가 `타입 OID 스캔 에러`를 일으킴.

#### 1.2 PostgreSQL Binary Numeric 스펙 구현
PostgreSQL 공식 바이너리 NUMERIC 형식:
- `ndigits int16`: 10,000진수(Base-10,000) 자릿수 개수
- `weight int16`: 첫 번째 자릿수의 가중치 ($10000^{weight}$)
- `sign uint16`: `0x0000` (양수), `0x4000` (음수), `0xC000` (NaN)
- `dscale uint16`: 소수점 이하 표시 자릿수
- `digits []int16`: 각 10,000진수 자릿수 값 ($0 \le digit < 10000$)

#### 1.3 구현 상세
- `decodeBinaryNumeric(b []byte) (storage.Decimal, error)` 구현: 바이너리 바이트열을 `storage.Decimal`로 변환.
- `encodeBinaryNumeric(dec storage.Decimal) []byte` 구현: `storage.Decimal`을 PostgreSQL 표준 8+2*N 바이트 패킷으로 직렬화.
- `server.go`의 `decodeBinaryParamValue` 및 `formatColumnValue`에 `case 1700` 등록.

---

### Phase 2: `PERF-HOTSPOT-LATCH-1` 인-메모리 키 스트라이핑 뮤텍스 (Key-Striped Latch)

#### 2.1 아키텍처 개념 (하이브리드 마이크로 래칭)
- 읽기(SELECT)는 **100% Lock-Free** 스냅샷 격리를 완벽히 유지.
- 99%의 서로 다른 행 쓰기는 1,024개 스트라이프에 해싱 분산되어 **100% 병렬 실행**.
- 오직 **동일한 테이블의 동일한 Primary Key를 동시에 수정하는 요청들만** 엔진 진입 직전에 인-메모리에서 0.1~0.2ms 동안 짧게 대기(Queueing)하도록 조율.
- **결과**: `WT_ROLLBACK` / `ErrWriteConflict`를 원천 차단하고 네트워크 재시도 대기를 완전히 소거하여 1,400 TPS ➔ 4,000+ TPS로 급상승.

#### 2.2 구현 청사진 (`pkg/engine/key_latch.go`)
```go
package engine

import (
	"hash/fnv"
	"sync"
)

const KeyLatchStripes = 2048

type KeyLatchManager struct {
	stripes [KeyLatchStripes]sync.Mutex
}

func (m *KeyLatchManager) hashKey(table string, key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(table))
	h.Write([]byte{0})
	h.Write([]byte(key))
	return h.Sum32() % KeyLatchStripes
}

func (m *KeyLatchManager) Acquire(table string, key string) func() {
	idx := m.hashKey(table, key)
	m.stripes[idx].Lock()
	return m.stripes[idx].Unlock
}
```

#### 2.3 실행 경로 인터셉트
- DML 실행기(`sqlexec_dml.go`), Stored Procedure VDBE VM `OpUpdate` / `OpDelete` 실행 시:
  - 수정 대상 Primary Key가 특정되면 트랜잭션 쓰기 단계부터 커밋/롤백 완료 시점까지 해당 키 스트라이프 래치를 획득/유지.
  - 다중 행 수정 시 키 오름차순으로 정렬 후 획득하여 교착상태(Deadlock) 방지 (골든 룰 #10).

---

### Phase 3: `PROC-SP-INSERT-SELECT-1` Stored Procedure 내 `INSERT INTO ... SELECT` 지원

#### 3.1 문제 진단
- `pkg/proc/compiler.go:808`:
  ```go
  case *GeneralInsertStmt:
      if s.Select != nil {
          return fmt.Errorf("INSERT INTO ... SELECT in bytecode VM is evaluated via execution coordinator")
      }
  ```
- Stored Procedure 컴파일 시 이 에러로 인해 전체 프로시저 컴파일이 거부됨.

#### 3.2 구현 방안
- VDBE 바이트코드 VM에 `OpExecSubquery` (또는 `OpExecVolcano`) 오프코드 도입.
- Stored Procedure 컴파일러가 `GeneralInsertStmt`에 `s.Select != nil`이 있는 경우, 대상 SELECT 쿼리를 파싱하여 하위 실행기로 위임하고 결과 행을 순회하며 `OpInsert`로 스트리밍 파이프라이닝.
- 이를 통해 Stored Procedure 내에서 복합 배치 전표 생성 및 아웃박스 이벤트 생성을 단일 SP 호출로 완결.

---

### Phase 4: `PERF-PGWIRE-PIPELINE-1` PGWire Extended Query Protocol 파이프라인 최적화

#### 4.1 문제 진단
- TDS(1433)는 단일 RPC 패킷으로 즉시 실행되어 3,121 TPS를 기록하지만, PGWire는 `Describe`와 `Execute`가 분리되어 동일 Writable CTE를 이중 분석/파싱하면서 1,164 TPS로 저하.

#### 4.2 최적화 구현
- **Prepared Statement 메타데이터 캐싱**:
  - `Statement` 객체에 한 번 분석된 `ColumnMeta` 및 OID 목록을 캐싱하여, 동일 세션 내 반복 실행 시 `proc.ExtractQueryCharacteristics` 및 심볼 테이블 조회를 제로(Zero-overhead)화.
- **불필요한 사전 실행(`buildPreviewSQL`) 제거**:
  - CTE 구조에서 프로젝션 컬럼명을 정적으로 추론하여 `ExecuteSessionSQL` 사전 호출 없이 즉시 `RowDescription`을 반환.

---

### Phase 5: `PERF-RETRY-WT-ROLLBACK-1` WiredTiger WT_ROLLBACK(-31800) 인-커널 투명 재시도 안전망 (In-Kernel WriteConflict Retry Loop)

#### 5.1 문제 진단 및 MongoDB 표준 아키텍처 배경
- **현상**:
  - 다중 워커가 동일 행/페이지를 집중 갱신할 때 WiredTiger는 즉시 `-31800 (WT_ROLLBACK)` 동시성 쓰기 충돌을 반환함.
  - 기존 LionSQL은 이 에러를 엔진 내부에서 수습하지 않고 클라이언트로 즉시 던져버림(`ErrSerializationFailure`).
  - 클라이언트는 충돌 에러를 받고 지수 백오프로 재시도해야 하므로, 네트워크 왕복 오버헤드와 연쇄 충돌(Abort Storm)로 인해 TPS가 급락함.
- **MongoDB 표준 아키텍처 (`WriteConflictException Retry Loop`)**:
  - WiredTiger를 개발한 MongoDB는 스토리지 엔진 레이어에서 `WT_ROLLBACK`이 발생해도 이를 **클라이언트로 절대 전파하지 않음**.
  - `writeConflictRetry(opCtx, ...)` 루프를 통해 엔진 메모리 내부에서 즉시 트랜잭션을 롤백하고, 마이크로 지터(Micro-jitter, 50~250µs) 후 새 스냅샷을 열어 무비용으로 투명 재시도(Transparent Retry)를 완결함.
  - 클라이언트는 충돌 발생 여부조차 인지하지 못하며, 100% 성공 응답을 수신함.

#### 5.2 LionSQL 심층 방어(Defense-in-Depth) 2단계 구조
Key Latch(선제적 예방)와 In-Kernel Retry Loop(사후 안전망)를 결합하여 완벽한 동시성 처리 파이프라인을 구축함:

```
[클라이언트 UPDATE / SP 요청]
            │
            ▼
┌────────────────────────────────────────────────────────┐
│ [1차 방어선: Key-Striped Latch (선제적 예방)]          │
│ • PK 식별 가능 갱신: Go 메모리 20µs 큐잉               │
│ • 충돌 자체를 사전에 원천 차단 (CPU 낭비 0, 16,000+ TPS) │
└────────────────────────────────────────────────────────┘
            │ (락을 타지 못하는 범위 조건 WHERE, 복잡한 조인 갱신 등)
            ▼
┌────────────────────────────────────────────────────────┐
│ [스토리지 엔진 트랜잭션 실행 (WiredTiger / In-Memory)] │
└────────────────────────────────────────────────────────┘
            │
            ├── 성공 ➔ 커밋 완료 후 응답
            │
            └── WT_ROLLBACK (-31800) 발생 시!
                    │
                    ▼
┌────────────────────────────────────────────────────────┐
│ [2차 방어선: In-Kernel WriteConflict Retry Loop (안전망)]│
│ • 클라이언트로 에러를 던지지 않음!                    │
│ • 엔진 메모리에서 마이크로 지터 후 즉시 재시도 (최대 5회)│
│ • 최신 스냅샷으로 성공 시 클라이언트엔 100% 정상 응답! │
└────────────────────────────────────────────────────────┘
```

#### 5.3 시나리오별 재시도 정책
1. **단일 쿼리 자동 커밋 (Single Statement Autocommit & Writable CTE)**:
   - 클라이언트에게 아직 어떠한 네트워크 패킷도 전달되지 않은 상태이므로, `Coordinator` 및 `sqlexec_dml` 레벨에서 `executeWithWriteConflictRetry`로 감싸 최대 5회 투명 재시도.
2. **T-SQL Stored Procedure VM (`EXEC sp_...`)**:
   - 모든 파라미터와 로컬 변수가 VDBE VM 내에 온전히 보존되어 있으므로, 프로시저 도중 또는 커밋 시점 `WT_ROLLBACK` 발생 시 트랜잭션 롤백 ➔ 레지스터 초기화 ➔ `PC = 0`부터 엔진 내부 무비용 재시도.
3. **대화형 명시적 트랜잭션 (`BEGIN; ... COMMIT;`)**:
   - 클라이언트가 다중 라운드트립으로 이전 `SELECT` 기반 비즈니스 결정을 내렸을 수 있으므로, ANSI SQL 표준에 따라 `40001 Serialization Failure` 반환 (단, 선행 Key Latch가 핫스팟 충돌을 1차로 예방).

---

## 3. 검증 계획 및 3단계 파이프라인

1. **Stage 1: Build**:
   - `go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **Stage 2: Test (-race)**:
   - `pkg/wire` 바이너리 OID 1700 인/디코딩 단위 및 드라이버 테스트 통과.
   - `pkg/engine` 동시성 50 워커 핫스팟 락 스트라이프 레이스 프리(0 data races) 검증.
   - `pkg/proc` Stored Procedure `INSERT INTO ... SELECT` E2E 테스트 통과.
3. **Stage 3: Grove 동등 벤치마크 재측정**:
   - `CreateJournal` PGWire(5433) 에러 0건 정상 통과 검증.
   - `ReserveStock` 핫스팟 처리량 1,400 TPS ➔ 4,000+ TPS 목표 달성 확인.
