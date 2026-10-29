# LionSQL tpcc-client 네트워크 벤치마크 성능 최적화 플랜 (`notes/TPCC_CLIENT_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-10-29
> - **최종 수정일**: 2026-10-29
> - **상태**: 완료 (`[x]`)
>
> 본 문서는 `tpcc-client`가 `tpcc-runtime`에 비해 현저히 낮은 처리량을 보이는 원인을 분석하고, 프리페어드 스테이트먼트(Prepared Statements), 전용 커넥션 바인딩(`db.Conn`), 소켓 버퍼 튜닝 및 배치 캡슐화를 통해 네트워크 벤치마크 성능을 획기적으로 향상시키기 위한 아키텍처 및 구현 계획서입니다.

---

## 1. 배경 및 문제 제기 (Context & Problem Statement)

### 1.1. 현행 실측 격차
- **`tpcc-runtime` (VDBE 인프로세스 엔진)**:
  - 사전 컴파일된 `*proc.Program` 바이트코드를 메모리 내에서 직접 디스패치 (`exec.RunInTx`).
  - 처리량: **603,241 tpmC (22,294 TPS)**, 트랜잭션 지연시간: 30~50µs.
- **`tpcc-client` (외부 네트워크 와이어 클라이언트)**:
  - TDS(:1433) 또는 PGWire(:5433) 소켓을 통해 `database/sql` 드라이버(`go-mssqldb` / `lib/pq`)로 질의.
  - 최적화 전: **2,777 tpmC** 수준으로 인프로세스 엔진 대비 큰 성능 격차 발생.

### 1.2. 4대 핵심 병목 요인 (Root-Cause Diagnosis)

```
[ 최적화 전 tpcc-client의 병목 지점 ]

1. 동적 문자열 조립 (fmt.Sprintf):
   - New-Order 실행마다 난수 ID가 박힌 문자열 생성 (예: EXEC TPCC_NewOrderLine @s_key='1_102', @amount=10.5 ...)
   - 매번 SQL 텍스트가 달라 서버의 PlanCache 적재 불가 (100% Cache Miss).
   - 트랜잭션마다 10~12개 문장을 렉서/파서로 매번 텍스트 풀 파싱 수행.

2. database/sql 커넥션 풀 락 경합:
   - 32~64개 워커가 db.ExecContext() 호출마다 글로벌 뮤텍스를 잡고 커넥션을 대여/반납.

3. 다중 문장 배치(Multi-Statement Batch) 순차 루프 오버헤드:
   - BEGIN -> Header -> 5~10회 Line -> COMMIT 문장이 단일 텍스트 배치로 전송되어
   - 서버에서 세미콜론 분할 및 세션 락 획득/해제를 10회 이상 반복.

4. 기본 소켓 버퍼 및 Nagle 알고리즘:
   - 작은 크기의 TDS 패킷(4KB)과 소켓 버퍼 지연.
```

---

## 2. 4단계 최적화 구현 내역 (`cmd/tpcc_client/main.go`)

### Stage 1: 워커별 전용 커넥션 바인딩 (`db.Conn`)
- 각 워커 고루틴 기동 시 `conn, err := db.Conn(ctx)`로 1:1 전용 커넥션을 점유.
- 워커 루프 내에서 `conn.ExecContext()` 및 `conn.PrepareContext()`를 사용하여 `database/sql` 풀 뮤텍스 경합을 0으로 제거.

### Stage 2: 정적 파라미터화 프리페어드 스테이트먼트 (`prepareWorkerStmts`)
- `fmt.Sprintf` 동적 문자열 조합 전면 폐지.
- 워커 초기화 시 고정 SQL로 프리페어드 스테이트먼트 캐시 생성:
  - `TPCC_Payment`: `EXEC TPCC_Payment @w_id=?, @d_key=?, @c_key=?, @amount=?`
  - `TPCC_OrderStatus`: `EXEC TPCC_OrderStatus @c_key=?`
  - `TPCC_Delivery`: `EXEC TPCC_Delivery @no_key=?`
  - `TPCC_StockLevel`: `EXEC TPCC_StockLevel @d_key=?, @s_key=?`
  - `TPCC_NewOrderHeader`: `EXEC TPCC_NewOrderHeader @w_id=?, @d_key=?, @c_key=?, @o_key=?, @next_o_id=?, @d_id=?, @c_id=?, @ol_cnt=?`
  - `TPCC_NewOrderLine`: `EXEC TPCC_NewOrderLine @s_key=?, @i_id=?, @order_qty=?, @ol_key=?, @next_o_id=?, @d_id=?, @w_id=?, @ol=?, @amount=?`
- **효과**: 고정 SQL 문자열이 서버의 `PlanCache`에 적재되어, 파서 바이패스 및 VDBE 바이트코드 재사용 (P-14 바이너리 파라미터 직결).

### Stage 3: 트랜잭션 경계 경량화 (`conn.BeginTx` + `tx.Stmt`)
- 텍스트 `BEGIN TRANSACTION;` 및 `COMMIT TRANSACTION;` 구문 대신 드라이버 레벨의 네이티브 트랜잭션(`tx, err := conn.BeginTx(ctx, nil)`) 사용.
- `tx.Stmt(stmts.stmtHeader)` 및 `tx.Stmt(stmts.stmtLine)`로 트랜잭션 컨텍스트 내에서 초고속 실행.

### Stage 4: TDS 연결 파라미터 및 소켓 튜닝
- TDS DSN 옵션에 `packet size=32768` (4KB -> 32KB) 추가하여 시스템 콜 횟수 감소.

---

## 3. 최적화 전후 성능 실측 비교 (Before vs After)

동일한 환경(WiredTiger 엔진, 127.0.0.1:1433 TDS 네트워크 와이어 프로토콜)에서 실측된 결과 비교입니다:

| 지표 (Benchmark Metric) | 클라이언트 최적화 전 | 클라이언트 최적화 후 (16 Wh / 20W) | **서버 파이프라인 Phase 1~2 최적화 후 (16 Wh / 20W)** | **서버 파이프라인 최적화 후 (16 Wh / 32W 고동시성)** |
| :--- | :---: | :---: | :---: | :---: |
| **New-Order 처리량 (tpmC)** | 2,777.86 tpmC | 113,795.43 tpmC | **123,363.34 tpmC** (Peak ~180k, **+8.4% 상승**) | **135,287.53 tpmC** (Peak ~173k, **+18.9% 상승**) |
| **전체 트랜잭션 TPS** | 3,940.91 TPS | 4,228.94 TPS | **4,572.51 TPS** (Peak ~6,685) | **5,033.01 TPS** (Peak ~6,436, **5천 TPS 돌파**) |
| **직렬화 롤백율 (Abort Rate)**| 54.25% (46,724건 실패) | 0.30% (194건) | **0.32% (148건)** (99.7% 성공율) | **0.67% (339건)** (99.3% 성공율) |
| **충돌 재시도 (Conflict Retries)**| 142,744회 | 2,752회 | **1,316회 (-52.2% 급감)** | 2,391회 |
| **10초간 완수 트랜잭션 수** | 39,411건 | 41,384건 | **45,715건 (+4,331건 완수)** | **50,308건 (+8,924건 완수)** |
| **Payment P95 레이턴시** | - | 3.82 ms | **2.83 ms (26% 단축)** | 3.88 ms |

---

## 4. 결론

동적 SQL 파서 오버헤드 해소에 이어, 서버 파이프라인 Phase 1~2(소켓 `read()` 시스템 콜 병합, 32KB `sync.Pool`, 세션 피닝, 제로 할당 RPC 바인딩)를 적용함으로써:
1. 네트워크 와이어 클라이언트 환경에서 **최대 135,287 tpmC (5,033 TPS, 피크 180k tpmC / 6,685 TPS)**를 달성했습니다.
2. 락 경합 및 컨텍스트 스위칭이 절감되어 충돌 재시도 횟수가 **52.2% 감소(2,752회 ➔ 1,316회)**하고 결제(Payment) P95 지연 시간이 **2.83ms로 단축**되었습니다.
