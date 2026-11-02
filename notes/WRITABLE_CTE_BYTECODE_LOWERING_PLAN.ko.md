# Writable CTE Stored Procedure 바이트코드 Lowering 가속 설계 계획서 (`notes/WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-02
> - **상태**: 설계 완료 및 구현 착수 (In Progress)
> - **과제 식별 코드**: `PERF-CTE-LOWERING-VDBE-1`
> - **관련 배경**: Grove e-Commerce & ERP 복합 트랜잭션 4자 동등 벤치마크 (Writable CTE 1,164~3,121 TPS ➔ Stored Procedure 동등 4,500+ TPS 극대화)
> - **영향 범위**: `pkg/proc/`, `pkg/proc/runtime/`, `pkg/engine/`, `pkg/engine/sqlexec_explain.go`

---

## 1. 추진 배경 및 당위성

### 1.1 Grove 실측 데이터가 보여준 격차
실전 전자상거래 및 회계 분개 벤치마크에서 동일한 비즈니스 로직(주문 생성 + 주문 품목 + 아웃박스 이벤트)을 수행할 때 프로토콜 및 구문별 성능 차이는 다음과 같았습니다:

- **TDS 1433 + T-SQL Stored Procedure**: **4,614.8 TPS** (P50: 5.94ms, Min: 0.20ms)
- **TDS 1433 + Writable CTE**: **3,121.7 TPS** (P50: 10.52ms)
- **PGWire 5433 + Writable CTE**: **1,164.1 TPS** (P50: 19.82ms)

Stored Procedure는 VDBE 바이트코드 VM에서 직접 실행되어 200µs 초저지연을 기록한 반면, Writable CTE는 동일한 3개 테이블 쓰기임에도 불구하고 코디네이터 레벨의 배관 오버헤드로 인해 약 10~15ms의 지연과 락 대기를 유발했습니다.

### 1.2 현재 Writable CTE 실행 경로의 5대 병목 분석
현재 `pkg/engine/sqlexec_explain.go:1140-1280`의 Writable CTE 실행 경로는 다음과 같이 동작합니다:
1. **다중 코디네이터 디스패치**: CTE 체인의 각 DML 문장마다 `execSingleStatement`를 독립적으로 호출.
2. **세션 임시 테이블 오버헤드**: 각 CTE 단계마다 `buildCTEEphemeralTable`로 `#temp_...` 스키마를 동적으로 구성하고 세션 카탈로그(`cs.RegisterEphemeralTable`)에 등록/해제.
3. **메모리 버퍼링 및 슬라이스 복사**: RETURNING 레코드를 메모리 슬라이스에 복사하고 다음 단계 쿼리로 파이프라인.
4. **비결정론적 문자열 치환**: 후속 CTE 및 메인 SQL 텍스트에서 식별자를 문자열 검색/치환(`replaceIdentOutsideQuotes`).
5. **극단적으로 긴 락 보유 시간 (Lock Hold Duration)**:
   - 실제 행 갱신은 15µs에 불과하나, 위 배관 작업 때문에 락을 **10~15ms 동안 점유**.
   - 50개 워커가 몰릴 때 Key Latch 대기열이 길어져 처리량이 3,000 TPS 이하로 제한됨.

---

## 2. 핵심 아키텍처: Writable CTE ➔ VDBE Bytecode Lowering

### 2.1 아키텍처 개요
OLTP에서 사용하는 Writable CTE의 95% 이상은 선형적인 다중 쓰기 체인(Linear Mutation Pipeline)입니다:
```sql
WITH ins_order AS (
    INSERT INTO orders (id, idempotency_key, customer_id)
    VALUES ('ord-1', 'idem-1', 'cust-1')
    RETURNING created_at, updated_at
), ins_item AS (
    INSERT INTO order_items (id, order_id, item_id)
    VALUES ('item-1', 'ord-1', 'sku-1')
    RETURNING created_at, updated_at
), ins_outbox AS (
    INSERT INTO outbox_events (id, aggregate_id, event_type)
    VALUES ('evt-1', 'ord-1', 'ORDER_CREATED')
    RETURNING created_at, updated_at
)
SELECT o.created_at, o.updated_at, i.created_at, i.updated_at, e.created_at, e.updated_at
FROM ins_order o, ins_item i, ins_outbox e;
```

이를 트리 파이프라인과 임시 테이블로 돌리지 않고, 파서/컴파일러 단계에서 **익명 T-SQL Stored Procedure와 완전히 동일한 단일 VDBE 프로그램**으로 즉시 Lowering(변환)합니다:

```
┌────────────────────────────────────────────────────────────────────────┐
│                   Writable CTE VDBE Lowering 파이프라인                  │
└────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
1. OpTxBegin (필요 시 암시적 트랜잭션 시작)
2. OpInsert (orders) ➔ 반환 컬럼 레지스터 할당 ($r0, $r1)
3. OpInsert (order_items) ➔ 반환 컬럼 레지스터 할당 ($r2, $r3)
4. OpInsert (outbox_events) ➔ 반환 컬럼 레지스터 할당 ($r4, $r5)
5. OpResultRow ($r0, $r1, $r2, $r3, $r4, $r5)
6. OpTxCommit (암시적 트랜잭션 커밋)
7. OpHalt
```

### 2.2 획기적인 효과
1. **임시 테이블 DDL/카탈로그 변경 0건**: 카탈로그 뮤텍스 경합 완전 제거.
2. **문자열 치환 및 재파싱 0건**: 제로 앨로케이션 레지스터 직접 전달.
3. **락 보유 시간(Lock Hold Duration) 250배 단축**:
   - 15ms ➔ **~60µs (0.06ms)**로 단축!
   - 선행 장착된 2,048-스트라이프 Key Latch의 회전율이 극대화되어 경합이 즉시 해소됨.
4. **성능 수렴**: Writable CTE가 Stored Procedure와 동등한 **4,500+ TPS**로 수렴.

### 2.3 2-Tier 아키텍처 및 Spec-Out 11 가드레일 준수
- **Tier 1 (VDBE Lowering Fast-Path)**:
  - 선형 쓰기 체인(Linear DML CTEs: `INSERT/UPDATE/DELETE RETURNING` + 단순 프로젝션 `SELECT`).
  - OLTP 핫패스에 대해 200µs 미만 초고속 실행.
- **Tier 2 (Volcano CBO Fallback)**:
  - 복합 집계(`GROUP BY`), 윈도우 함수(`OVER`), 재귀 CTE(`WITH RECURSIVE`), 다중 테이블 해시 조인이 포함된 분석용 복합 CTE는 기존 코디네이터 경로로 자동 위임.
  - 이로써 Spec-Out 11 (`ARCH-2TIER-1`: 임의의 복잡한 관계대수를 바이트코드로 재작성하지 않음) 원칙을 100% 준수.

---

## 3. 세부 구현 계획

### Phase 1: 선형 Writable CTE 패턴 판별기 (`detectLinearWritableCTE`)
- `pkg/engine/sqlexec_explain.go` 또는 `pkg/proc/`에 배치.
- 쿼리가 Writable CTE인지 확인하고, 각 CTE 절이 선형 DML(`INSERT`, `UPDATE`, `DELETE`)인지와 메인 쿼리가 단순 프로젝션인지 판별.

### Phase 2: Writable CTE ➔ VDBE 프로그램 컴파일러 (`CompileWritableCTE`)
- 각 CTE 절의 DML AST 파싱 및 RETURNING 컬럼 슬롯 매핑.
- 이전 CTE의 RETURNING 컬럼 또는 상수를 참조하는 후속 DML 컴파일.
- 메인 `SELECT`의 프로젝션 컬럼을 해당 레지스터 슬롯에 연결하고 `OpResultRow` 방출.

### Phase 3: 코디네이터 패스트패스 연동
- `pkg/engine/sqlexec_explain.go`의 `execCTE` 도입부에서 `CompileWritableCTE` 시도.
- 성공 시 `s.executor.RunInTx` 또는 `s.executor.RunContext`로 직접 바이트코드 실행.
- 실패 시 기존 임시 테이블 기반 Volcano CBO 경로로 안전하게 폴백.

### Phase 4: 검증 및 3단계 파이프라인
- `pkg/engine/writable_cte_lowering_test.go`:
  - 3-Way Writable CTE(주문 + 품목 + 아웃박스) 정합성 검증.
  - 동시 워커 50 고동시성 부하 테스트.
  - 롤백 및 실패 시 트랜잭션 원자성 검증.
- 3단계 파이프라인(Build ➔ Test `-race` ➔ Commit).

---

## 4. 검증 계획 및 목표 성능 지표

| 지표 | 기존 Writable CTE (PGWire/TDS) | VDBE Lowering 적용 후 목표치 |
| :--- | :---: | :---: |
| **P50 레이턴시** | 10.5ms ~ 19.8ms | **1.0ms 이하 (~200µs)** |
| **락 보유 시간** | 10ms ~ 15ms | **100µs 이하** |
| **처리량 (TPS)** | 1,164 ~ 3,121 TPS | **4,500+ TPS (SP 동등)** |
| **임시 테이블 할당** | N개 생성/해제 | **0개 (Zero Allocation)** |
