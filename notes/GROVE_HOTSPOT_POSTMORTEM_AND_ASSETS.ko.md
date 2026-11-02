# Grove 벤치마크 핫스팟 분석 및 성능 정상화 포스트모템 (`notes/GROVE_HOTSPOT_POSTMORTEM_AND_ASSETS.ko.md`)

> - **최초 작성일**: 2026-11-02
> - **상태**: 분석 및 검증 완료 (Completed Architecture Postmortem)
> - **과제 식별 코드**: `PERF-GROVE-POSTMORTEM-1` (연관 과제: `PERF-GROVE-CONCURRENCY-1`, `PERF-REGRESSION-RECOVERY-1`)
> - **영향 범위**: `pkg/storage/`, `pkg/proc/runtime/`, `pkg/engine/`, `pkg/wire/`

---

## 1. 개요 및 배경

Grove e-Commerce & ERP 복합 벤치마크 시나리오 중 '조건부 재고 선점(`ReserveStock`)'의 100개 SKU 대상 50 워커 동시성 경합을 해결하기 위해 인-메모리 키 래치(`KeyLatchManager`)와 슬립 기반 재시도 루프를 도입했으나, 다중 행 트랜잭션(`TransferFunds`)에서 심각한 AB-BA 교착 상태와 락 컨보이가 발생하여 WiredTiger 엔진 처리량이 96% 급락(108,000 TPS ➔ 4,492 TPS)하는 현상이 발생했습니다.

이를 해결하기 위해 키 래치와 슬립 루프를 전량 제거하는 **방안 1(Clean Removal)**을 실행하여 기존의 108,000+ / 160,000+ TPS 초고성능을 완전히 복원하였으며, 가벼운 성능 회귀 방지 게이트(`TestRegression_TransferFundsPerfGate`)를 구축했습니다.

본 문서는 오늘 일련의 과정을 통해 획득한 **핵심적인 가치와 4대 영구 자산**, 그리고 고성능 트랜잭션 커널 관점의 **음의 지식(Negative Knowledge)**을 보존하기 위해 작성되었습니다.

---

## 2. 오늘 작업을 통해 남겨진 핵심 가치와 4대 자산

### 2.1 엔진에 남겨진 영구적인 기능 및 VM 최적화 자산
Grove 벤치마크 시나리오를 충족시키기 위해 구현된 T-SQL 저장 프로시저 컴파일러 및 가상 머신(VDBE)의 코어 기능들은 롤백 대상이 아니며, LionSQL 엔진의 표현력과 실행 속도를 한 단계 도약시키는 영구 자산으로 남았습니다:
1. **VDBE 정적 변수 슬롯 인덱싱 (`PERF-VDBE-SLOT-1`)**:
   - `OpStoreVar` / `OpLoadVar` 실행 시 기존 문자열 해시맵 탐색과 소문자 정규화 오버헤드를 완전히 제거하고, 컴파일 타임에 할당된 `[SlotID]` 정수 인덱스로 $O(1)$ 직접 접근하도록 구현.
   - 단일 프로시저 내 수십 회 변수 조작 지연 시간을 수 마이크로초에서 서브 마이크로초(단위: ns)로 단축.
2. **`OUTPUT ... INTO` 및 `RETURNING ... INTO` DML 파서 지원**:
   - T-SQL 및 PostgreSQL 표준 DML 결과를 프로시저 로컬 변수에 직접 바인딩(`DMLData.IntoVars`)하여 임시 테이블(`#temp`) 없이 단일 바이트코드 프레임 내에서 원자적으로 값을 수취.
3. **`INSERT INTO ... SELECT`의 VDBE 통합 (`PROC-SP-INSERT-SELECT-1`)**:
   - 저장 프로시저 내부에서 복합 릴레이션 쿼리 결과를 테이블에 대량 삽입하는 경로를 쿼리 코디네이터 브릿지로 완전 지원.
4. **PGWire OID 1700 (NUMERIC/DECIMAL) 바이너리 완벽 지원 (`BUG-WIRE-OID-1700`)**:
   - `pgx` 및 공식 PostgreSQL 클라이언트 라이브러리가 사용하는 Base-10,000 PostgreSQL 바이너리 포맷 인코더/디코더를 완비하여 회계 전표(`CreateJournal`) 처리 중 스캔 에러를 원천 차단.

---

### 2.2 스토리지 엔진별 명확한 워크로드 타깃 정립 (OCC vs WiredTiger)
"어떤 스토리지 엔진이 어떤 워크로드를 전담해야 하는가"에 대한 아키텍처적 단일 진실 공급원(SSOT)이 명확해졌습니다:

1. **In-Memory OCC (Optimistic Concurrency Control)**:
   - **적합 워크로드**: 계좌 이체, TPC-C, 일반 사용자 OLTP 세션 등 경합이 데이터베이스 전반에 고르게 분산되는 고주파 연산.
   - **특성**: 무잠금(Lock-Free) 낙관적 검증을 통해 160,000+ TPS와 25µs의 초저지연을 달성.
   - **구조적 한계**: 단 1개의 행(Single-Row Hotspot)에 수십 개의 스레드가 동시에 쓰기를 시도하는 환경에서는 낙관적 검증이 수학적으로 연쇄 실패(`ErrWriteConflict`)할 수밖에 없음.
   - **설계 불변식**: OCC의 극단 핫스팟 충돌을 해결하겠다고 상위에 인위적인 비관적 락(Key Latch)을 덧대는 것은 전체 Lock-Free 파이프라인을 파괴하는 안티패턴이며, 이를 절대 시도하지 않는다.
2. **WiredTiger (B+Tree MVCC with Hazard Pointers)**:
   - **적합 워크로드**: 실제 엔터프라이즈 ERP, 재고 차감, 회계 수불부, 금융 원장 등 엄격한 디스크 내구성과 단일 자원 집중 갱신이 빈번한 시스템.
   - **특성**: 하위 C 레벨 B+Tree 페이지 락과 MVCC 해저드 포인터를 갖추어 행 단위 순차화 처리에 본질적으로 유리함.
   - **결론**: **Grove 벤치마크는 In-Memory OCC가 아닌 WiredTiger 엔진을 표준 타깃으로 삼아야 한다.**

---

### 2.3 락 컨보이와 OS 타이머 양자화 메커니즘의 실체 규명 (음의 지식 확보)
왜 108,000 TPS를 기록하던 엔진이 키 래치 도입 후 4,492 TPS로 96% 급락했는지를 분석하는 과정에서, 고주파 데이터베이스 커널 설계의 치명적인 함정을 규명했습니다:

1. **다중 키 트랜잭션에서의 AB-BA 데드락**:
   - 단일 행 재고 차감(`sp_ReserveStock`)을 위해 도입된 키 래치가 송금(`TransferFunds`)과 같은 2개 이상 계좌를 동시에 갱신하는 트랜잭션과 만났을 때, 임의의 계좌 순서(`from_id`, `to_id`)로 인해 상호 래치 교착이 발생함.
2. **OS 타이머 양자화(Timer Quantization)에 의한 락 컨보이(Lock Convoy)**:
   - 교착 상태 타임아웃을 처리하기 위해 추가한 `time.Sleep(50µs)` 폴링 루프와 `RunContext` 내 `time.After(delay)` 지수 백오프 루프가 재앙의 원인이었음.
   - macOS(Darwin) 및 표준 Linux 커널의 스케줄러 틱 및 타이머 해상도로 인해 수십 마이크로초 단위의 슬립 요청이 실제로는 **1~2ms** 단위로 양자화(Rounding Up)되어 지연됨.
   - 25µs 안에 끝나야 할 고속 트랜잭션 수천 개가 1~2ms 슬립으로 일제히 대기열에 묶이면서 CPU 사용률이 4%대로 폭락하고 대규모 락 컨보이가 발생함.
3. **핵심 엔지니어링 원칙 (Fast-Fail Prudence)**:
   - 고주파 OLTP 데이터베이스 커널 내부의 핫패스에서는 **어떠한 형태의 임의 슬립 루프(`time.Sleep`, `time.After`)도 추가해서는 안 된다.**
   - 충돌 발생 시 즉각 실패(`ErrSerializationFailure` / `WT_ROLLBACK`)를 반환하여 호출자(클라이언트/드라이버) 레이어에서 스마트 지수 백오프를 수행하거나, 순수 락-프리 큐를 통해 대기열을 관리해야 한다.

---

### 2.4 영구적인 경량 성능 회귀 방지 게이트 구축
향후 어떤 기능 추가나 리팩토링이 발생하더라도 동일한 성능 급락이 배포되지 않도록 차단하는 안전망을 파이프라인에 영구 구축했습니다:
- **구현 파일**: [`pkg/proc/runtime/perf_regression_gate_test.go`](file:///Users/maro/Projects/SQLion/pkg/proc/runtime/perf_regression_gate_test.go) (`TestRegression_TransferFundsPerfGate`)
- **검증 메커니즘**:
  - 메모리 엔진 상에 1,000개 계좌를 초기화하고 8개 고루틴이 동시 송금 프로시저 1,000건을 실행.
  - 실행 후 계좌 전체 잔액 합(`SUM(balance) = 1,000,000,000`)의 원자적 불변량을 100% 검증.
  - `-race` 검출기가 활성화된 무거운 상태에서도 최소 10,000 TPS를 넘는지 단언(Assert).
  - 정상 상태 실측: **25,600+ TPS (소요 시간: 0.12초)**. 테스트 스위트 실행 시간에 거의 영향을 주지 않으면서, 래치/슬립 컨보이 재발 시 즉각 `t.Fatalf`로 빌드를 차단함.

---

## 3. 최종 성능 복원 실측치 요약

| 지표 / 벤치마크 | 래치/슬립 유입 시 (이전) | **방안 1 롤백 후 (현재 복원)** | 비고 |
| :--- | :--- | :--- | :--- |
| **WiredTiger 송금 (3.3.4)** | **4,492.98 TPS** (11.16초, 2,573 롤백) | **108,204.22 TPS** (462ms, 225 롤백) | **~24배 복원 (P50: 29.04µs)** |
| **In-Memory OCC 송금 (3.3.4)** | 120,857.00 TPS (413ms) | **160,759.71 TPS** (311ms, 0 롤백) | **초고속 Lock-Free 복원 (P50: 25.54µs)** |
| **In-Memory OCC TPC-C (3.5)** | 497,250 tpmC | **535,554.39 tpmC** (19,873 TPS) | 89,324 New-Order 완주 |
| **WiredTiger TPC-C (3.5)** | 512,000 tpmC | **549,514.51 tpmC** (20,188 TPS) | 91,635 New-Order 완주 |
| **단위 테스트 성능 게이트** | 미존재 | **25,639.33 TPS (0.12초 완주)** | `go test -race` 영구 검증 |

---

## 4. 향후 Grove 벤치마크 실행 가이드라인

1. **WiredTiger 전용 실행**:
   - Grove 벤치마크는 디스크 내구성 및 MVCC 행 잠금을 사용하는 `wiredtiger` 스토리지 엔진에 바인딩하여 실행한다.
2. **트랜잭션 충돌 제어의 클라이언트 경계 배치**:
   - 극단 핫스팟에 의한 `WT_ROLLBACK`(-31800) 발생 시, 커널 내부에서 스레드를 슬립시키지 않고 `ErrSerializationFailure`를 반환하여 Grove Go 워커의 재시도 핸들러에서 백오프하도록 유지한다.
3. **불필요한 래치 계층 도입 방지**:
   - 코어 엔진의 단순성(KISS)과 서브 마이크로초 처리량을 훼손하는 인위적 키 래치는 향후에도 배제한다.
