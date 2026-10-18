# LionSQL 스토리지 엔진별 스냅샷 격리 및 DuckDB 연동 아키텍처

본 문서는 LionSQL의 듀얼 스토리지 엔진(**WiredTiger MVCC** 및 **In-Memory OCC**)에서 동작하는 스냅샷 격리(Snapshot Isolation)의 내부 메커니즘, 두 엔진 간 스냅샷 시점(Time-point)의 본질적 차이, Vacuum이 불필요한 이유, 그리고 외부 임베디드 분석 엔진인 **DuckDB와의 초기 데이터 동기화 및 실시간 CDC 연동 정합성**을 기술적으로 상세히 기술합니다.

---

## 1. 아키텍처 요약 및 핵심 명제

```
[ LionSQL 고동시성 OLTP 코어 ]
  ├── 1. WiredTiger MVCC : 디스크 기반 Lock-free B+Tree (트랜잭션 레벨 동결 스냅샷)
  └── 2. In-Memory OCC   : 순수 RAM 기반 Atomic Workspace (스테이트먼트 레벨 스냅샷)
            │
            │ (Non-blocking Reads: 읽기는 쓰기를 절대 막지 않음)
            ▼
[ 초기 데이터 스냅샷 덤프 ] ──(Watermark LSN 기준)──┐
                                                    │
                                                    ▼
[ 실시간 비동기 CDC 스트림 ] ──(HTTP NDJSON/SSE)────> [ DuckDB OLAP 분석 영역 ]
                                                    - 단일 라이터 락 격리
                                                    - INSERT OR REPLACE (최종 수렴)
```

1. **Readers Never Block Writers, Writers Never Block Readers**:
   - DuckDB가 수천만 건의 데이터를 초기 덤프하기 위해 장시간 테이블을 풀스캔하더라도, LionSQL의 실시간 결제/주문 트랜잭션에는 **0%의 락 간섭(Zero Lock Contention)**과 **0%의 지연**이 보장됩니다.
2. **비공개 워크스페이스 격리 (Zero Dirty Read)**:
   - 커밋되지 않은 트랜잭션의 임시 데이터는 세션 전용 비공개 공간에만 존재하므로, DuckDB나 다른 세션에 더티 데이터가 노출될 수 없습니다.
3. **완벽한 최종 수렴 (Eventual Convergence)**:
   - WiredTiger와 In-Memory OCC의 스냅샷 동결 시점 메커니즘이 서로 다르더라도, LSN 워터마크 기반의 CDC 연동과 DuckDB의 멱등적(Upsert) 처리를 통해 **최종 데이터 상태는 두 엔진 모두 100% 동일하게 일치**합니다.

---

## 2. 스냅샷 격리(Snapshot Isolation)의 내부 동작 원리

전통적인 RDBMS(2PL 비관적 락)는 읽기 작업 시 공유 락(S-Lock)을 걸어 쓰기 트랜잭션(X-Lock)을 멈추게 만들지만, LionSQL은 데이터를 덮어쓰지 않고 버전과 타임스탬프를 관리하는 **다중 버전 동시성 제어(MVCC/OCC)**를 통해 락을 완전히 제거합니다.

### 2.1 WiredTiger MVCC: 트랜잭션 레벨 동결 스냅샷 (Strict Snapshot Isolation)

WiredTiger는 B+Tree 페이지 내부의 각 키(Key)에 대해 단일 값이 아닌 **업데이트 연결 리스트(`WT_UPDATE` 체인)**를 메모리에 유지합니다.

```text
[ 키: 계좌번호 101번 ]
       │
       ▼
 ┌──────────────────────────┐
 │ WT_UPDATE 노드 (최신)     │ ──> 커밋 타임스탬프: 10:10 (Tx 300)
 │ 값: 2,000원               │
 └─────────────┬────────────┘
               │ (과거 링크)
               ▼
 ┌──────────────────────────┐
 │ WT_UPDATE 노드           │ ──> 커밋 타임스탬프: 10:05 (Tx 200)
 │ 값: 1,500원               │  ◀── [DuckDB 덤프 트랜잭션 시작: 10:06]
 └─────────────┬────────────┘      (DuckDB는 이 노드를 가리키며 읽음)
               │ (과거 링크)
               ▼
 ┌──────────────────────────┐
 │ WT_UPDATE 노드 (최초)     │ ──> 커밋 타임스탬프: 10:00 (Tx 100)
 │ 값: 1,000원               │
 └──────────────────────────┘
```

- **커밋 시 동작**:
  - 기존 메모리 블록을 덮어쓰지 않고, 새로운 `WT_UPDATE` 노드를 할당하여 체인의 헤드에 원자적(Atomic CAS)으로 연결합니다.
- **읽기 시 동작**:
  - DuckDB가 10:06에 트랜잭션을 시작했다면, 이후 다른 트랜잭션이 아무리 많은 새 버전을 커밋해도 자신의 시작 시점(10:06)보다 이전인 **버전 2(1,500원)**를 체인에서 찾아 읽습니다.
  - 따라서 트랜잭션이 1시간 동안 지속되더라도 **정확히 1시간 전 과거 시점의 세상만 동결되어 보이는 "타임머신형 스냅샷"**을 제공합니다.

---

### 2.2 WiredTiger에서 VACUUM이 필요 없는 이유

PostgreSQL은 디스크 테이블 파일(Heap)에 변경된 행을 물리적으로 계속 INSERT하여 죽은 튜플(Dead Tuple)이 쌓이기 때문에 주기적으로 디스크 전체를 스캔하는 무거운 `VACUUM` 프로세스가 필수적입니다. 반면 WiredTiger는 구조적으로 Vacuum이 배제되어 있습니다:

| 비교 항목 | PostgreSQL MVCC | WiredTiger MVCC (LionSQL) |
| :--- | :--- | :--- |
| **과거 버전 저장 위치** | **디스크 힙 파일** (Dead Tuple 물리적 누적) | **메모리(RAM) 연결 리스트** (`WT_UPDATE`) |
| **과거 데이터 정제 시점** | 백그라운드 **`VACUUM`** 프로세스 가동 시 | 디스크에 쓸 때 **즉시 정제 (Reconciliation)** |
| **디스크 재활용** | Free Space Map (FSM) 스캔 기반 재사용 | **블록 매니저 Free Extent List 즉시 재사용** |
| **운영 상 문제점** | Autovacuum I/O 폭발, 테이블 Bloat, TXID 랩어라운드 | **Vacuum 부재로 I/O 스톰 0%, 락 경합 0%** |

1. **디스크 쓰기 시점 실시간 정제 (Reconciliation)**:
   - 메모리의 B+Tree 더티 페이지를 디스크(`.wt`)로 내릴 때, 현재 실행 중인 가장 오래된 활성 트랜잭션(Oldest Reader)보다 오래된 모든 과거 버전은 **디스크에 쓰여지는 바로 그 순간에 실시간으로 버려집니다.**
   - 즉, 디스크에는 오직 최신 유효 데이터만 압축되어 기록되므로 디스크에 청소할 쓰레기가 남지 않습니다.
2. **하저드 포인터 (Hazard Pointer) 메모리 보호**:
   - DuckDB가 읽고 있는 과거 메모리 노드는 하저드 포인터로 보호되며, DuckDB가 읽기를 마치는 즉시 표준 C 메모리 해제(`free`)를 통해 운영체제로 반환됩니다.

---

### 2.3 In-Memory OCC: 스테이트먼트 레벨 스냅샷 (Read Committed Snapshot)

In-Memory OCC 엔진(`pkg/storage/memengine`)은 디스크 I/O 없이 오직 물리 RAM만을 사용하므로, 메모리 고갈(OOM)을 방지하고 초당 20,000+ TPS 이상의 속도를 내기 위해 다른 트레이드오프를 선택했습니다.

```text
[ 실시간 쓰기 세션 A ]                    [ 메인 인메모리 테이블 (Concurrent SkipList) ]
  비공개 작업대 (tx.local)                         │
  ┌──────────────────────┐                       │
  │ 키 101: 잔액 2,000원 │                       │ (미커밋 데이터는 메인에 없음)
  └──────────────────────┘                       │
             │                                   ▼
             │ (Commit 시점에만 검증 후 교체)    키 101: 잔액 1,500원 (Version 5)
             └───────────────────────────────>   키 102: 잔액 3,000원 (Version 2)
                                                 키 103: 잔액 5,000원 (Version 8)
                                                         ▲
                                                         │ (락 없이 순수 RAM 속도로 순회)
                                           [ DuckDB 초기 덤프 커서 ]
```

1. **비공개 작업대 (Private Workspace)**:
   - 모든 `INSERT`, `UPDATE`, `DELETE`는 세션 전용 메모리 맵(`tx.local`)에만 임시 기록됩니다.
   - 트랜잭션이 커밋되기 전까지는 메인 메모리 테이블에 변경 흔적이 전혀 없으므로, 더티 리드가 100% 방지됩니다.
2. **스테이트먼트 레벨 스냅샷 (Zero Memory Bloat)**:
   - 순수 인메모리 엔진에서 수 분 동안 과거 버전을 체인으로 길게 들고 있으면 RAM이 순식간에 소진됩니다.
   - 따라서 레코드당 단일 최신 버전(`Atomic Value`)과 단조 증가 버전 번호(`Version uint64`)만을 유지합니다.
   - DuckDB가 테이블을 스캔하는 동안 다른 트랜잭션이 커밋을 완료하면, 커서가 아직 지나가지 않은 뒷부분의 행에 대해서는 최신 커밋본이 자연스럽게 읽힐 수 있습니다 (Read Committed 수준의 스냅샷).

---

## 3. 두 엔진의 스냅샷 시점(Time-point) 본질적 차이 비교

| 구분 | WiredTiger MVCC | In-Memory OCC (`memengine`) |
| :--- | :--- | :--- |
| **스냅샷 고정 기준점** | **트랜잭션 시작 시점 (Transaction Start Time)** | **쿼리/스캔 실행 순간 (Statement Execution Time)** |
| **스냅샷 유효 기간** | 트랜잭션이 끝날 때까지 영구 동결 유지 | 해당 쿼리/커서 순회가 끝나는 순간 소멸 |
| **표준 격리 수준** | **Strict Snapshot Isolation (Repeatable Read)** | **Read Committed Snapshot** |
| **동작 시나리오**<br>(10:00 트랜잭션 시작 후<br>10:05 타 세션 커밋 발생 시) | 10:10에 조회해도 **10:00 당시의 과거 데이터 반환**<br>(타임머신 동결) | 10:10에 조회하면 **10:05에 커밋된 최신 데이터 반환**<br>(최신 커밋 반영) |
| **아키텍처 설계 목적** | 디스크/캐시를 활용한 완벽한 시점 일관성 제공 | **메모리 누적 0%, 초당 20,000+ TPS 극대화** |

---

## 4. DuckDB 초기 동기화 및 실시간 CDC 연동 정합성

LionSQL과 DuckDB의 연동은 두 엔진의 트랜잭션 모델 특성을 완벽히 흡수하도록 **표준 3단계 핸드오버(Cutover Recipe)**로 동작합니다.

```
[ LionSQL Server ] ───────────────────────────────────────── (실시간 OLTP 초당 수천 건 커밋 중)
     │
     ├─ [Step 1] Watermark LSN 확인 (예: LSN = 5000)
     │
     ├─ [Step 2] 초기 스냅샷 덤프 (SELECT * FROM orders ➔ DuckDB에 벌크 INSERT)
     │           ※ 덤프 도중 LSN이 5000 ➔ 5500으로 증가해도 논블로킹 스냅샷으로 안전
     │
     └─ [Step 3] 실시간 CDC 스트림 연결 (GET /api/cdc/stream?from_lsn=5000)
                 ※ DuckDB에서 INSERT OR REPLACE INTO orders ... (Upsert)
                 ※ 덤프 도중 발생한 변경분과 CDC 변경분이 완벽하게 최종 상태로 수렴!
```

### 단계별 정합성 수렴 메커니즘

1. **Step 1: Watermark LSN 확보**:
   - DuckDB 워커는 덤프를 시작하기 직전 LionSQL의 현재 커밋 LSN(예: `LSN = 5000`)을 확인합니다.
2. **Step 2: 논블로킹 초기 덤프**:
   - **WiredTiger 사용 시**: LSN 5000 시점 그대로 얼어붙은 완벽한 스냅샷을 덤프합니다.
   - **In-Memory OCC 사용 시**: 덤프가 진행되는 동안 커밋된 변경사항(LSN 5001 ~ 5500) 일부가 덤프 뒷부분에 자연스럽게 섞여 들어갈 수 있습니다.
3. **Step 3: CDC 스트림 구독 및 멱등적 융합 (Convergence)**:
   - 덤프가 완료되면 DuckDB 워커는 `from_lsn=5000` 파라미터로 CDC 스트림을 구독합니다.
   - 덤프가 도는 동안 발생했던 모든 트랜잭션(LSN 5001 ~ 5500)이 CDC 스트림을 통해 순차적으로 전달됩니다.
   - DuckDB 쪽 인제스천 파이프라인은 Primary Key 기반의 **`INSERT OR REPLACE INTO ...` (Upsert)**를 수행합니다.
   - **결과**:
     - 덤프에서 과거 값을 가져왔든(WiredTiger), 덤프 중에 이미 최신 값을 가져왔든(In-Memory OCC), 뒤이어 들어오는 CDC 이벤트가 최종 커밋 상태로 동일 키를 덮어쓰므로 **최종적으로 DuckDB 테이블의 모든 행은 100% 오차 없이 최신 상태로 일치(수렴)**합니다.

---

## 5. 결론 및 운영 권장 사항

1. **OLTP 트랜잭션 간섭 없음**:
   - DuckDB가 초기 전체 데이터를 덤프할 때나 실시간 CDC를 구독할 때 모두, LionSQL의 고성능 OLTP 쓰기 경로에는 어떠한 락(Lock)이나 병목도 발생하지 않습니다.
2. **엔진 선택 가이드**:
   - **WiredTiger MVCC**: 엄격한 트랜잭션 시점 동결(Strict Snapshot Isolation)과 대규모 데이터의 무중단 운영이 필요한 일반적인 엔터프라이즈 환경에 권장됩니다. (Vacuum 부재로 운영 오버헤드 0%)
   - **In-Memory OCC**: 밀리초 미만의 초극단 레이턴시와 초고속 인메모리 처리가 최우선이며, 덤프 시 스테이트먼트 레벨 스냅샷으로 충분한 환경에 권장됩니다.
3. **DuckDB 연동 베스트 프랙티스**:
   - DuckDB 인제스천 스키마에는 반드시 **Primary Key**를 정의하고, CDC 이벤트 적용 시 **`INSERT OR REPLACE`** 구문을 사용하여 재연결 및 초기 덤프 시의 멱등성을 보장하십시오.
