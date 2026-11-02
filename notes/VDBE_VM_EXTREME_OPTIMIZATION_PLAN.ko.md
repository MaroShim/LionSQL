# T-SQL VDBE 바이트코드 VM 극강의 성능 쥐어짜기 5대 혁신 설계 계획서 (`notes/VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-02
> - **최종 갱신일**: 2026-11-02 (7,100 TPS 튜닝 성과 및 Writable CTE Lowering 반영)
> - **상태**: 아키텍처 설계 완료 및 백로그 등록 (Backlog)
> - **과제 식별 코드**: `PERF-VDBE-EXTREME-OPT-1`
> - **목표 성능**: T-SQL Stored Procedure 및 Lowered CTE 처리량 7,100 TPS ➔ **12,000 ~ 25,000+ TPS** (P50 50µs 초저지연 달성)
> - **영향 범위**: `pkg/proc/runtime/`, `pkg/proc/`, `pkg/proc/bytecode.go`

---

## 1. 추진 배경 및 실측 프로파일링 진단

### 1.1 1차 튜닝 성과: 스토리지 & 쿼리 레벨 최적화 (4,614 TPS ➔ 7,100 TPS)
초기 LionSQL T-SQL VDBE VM은 50 동시 워커 환경에서 **4,614.8 TPS** (P50: 5.94ms, Min: 0.20ms)를 기록하여 PostgreSQL 16(3,261 TPS) 대비 +41.5%의 우위를 점하고 있었습니다.

이후 직전 세션에서 진행된 스토리지 및 쿼리 레벨의 4대 튜닝을 통해 이미 **7,100.9 TPS (+145.7% / 2.45배)**까지 1차 성능 점프를 달성했습니다:
1. **하이브리드 키 래칭 (`PERF-HOTSPOT-LATCH-1`)**: 2,048-스트라이프 Key Latch로 100개 SKU 핫스팟 경합 시 OCC Abort Storm 방지.
2. **인-커널 투명 재시도 (`PERF-RETRY-WT-ROLLBACK-1`)**: WiredTiger `WT_ROLLBACK` 발생 시 마이크로 지터 기반 즉시 재시도로 런타임 에러 0건 보장.
3. **T-SQL `OUTPUT inserted.*` 단일 원자 반환**: `UPDATE` 후 후속 `SELECT`를 치던 2단계 쿼리를 단일 `UPDATE ... OUTPUT` 문장으로 통합하여 **물리 B-Tree 탐색 횟수를 2회에서 1회로 50% 절감** (3,556 TPS ➔ 7,100 TPS로 2배 폭증).
4. **PGWire Writable CTE Lowering (`PERF-CTE-LOWERING-VDBE-1`)**: PostgreSQL의 다중 쓰기 Writable CTE를 VDBE VM 바이트코드로 직결 Lowering하여 PG 5433 포트에서도 4,657.3 TPS 돌파.

### 1.2 7,100 TPS 도달 후 적나라하게 드러난 VDBE VM 내부의 5대 핵심 병목
스토리지 접근 횟수를 50% 절감하고 락 경합을 제거하자, CPU 프로파일러(pprof) 상에서 **순수 Go 런타임 및 VDBE VM 루프 내부의 5대 핵심 병목**이 전체 실행 시간의 60% 이상을 차지하며 가장 지배적인 병목으로 정면 노출되었습니다:

1. **`setVar` 변수 핫패스**: 변수 1회 대입 시 `cleanSymbol` 슬라이싱, `strings.ToLower` 힙 할당(3회) + 맵 쓰기(6회)가 매번 발생하여 초당 수십만 번의 Map 해싱과 GC 가비지를 양산.
2. **2단계 중첩 switch (Double Dispatch)**: `Execute` 메인 루프의 1차 switch ➔ `execComputeOp`/`execStorageOp` 함수 스택 프레임 생성 ➔ 함수 내부 2차 switch 구조로 인해 CPU 브랜치 예측 실패(Branch Misprediction) 및 파이프라인 스톨 유발.
3. **DML AST 재귀 수식 평가 (`EvalRowExpr`)**: `OpInsert`/`OpUpdate` 실행 시 레지스터에 이미 계산된 값이 있음에도 AST 트리를 다시 재귀 순회하여 값을 추출하는 비효율.
4. **레코드 맵 생성 (`make(storage.Record)`)**: 매 행 삽입마다 `map[string]Value`를 신규 힙 할당하여 초당 수천 개의 맵 객체가 Go GC 힙을 압박.
5. **명령어 `Aux any` 런타임 인터페이스 타입 단언**: `f.aux(inst).(*proc.DMLData)`와 같이 매 바이트코드 디스패치마다 RTTI 타입 단언 오버헤드 누적.

스토리지 I/O와 락 경합이 이미 정리되었기 때문에, 이제 이 **VDBE VM 5대 내부 병목**을 정밀하게 제거하면 단일 노드 처리량을 **12,000 ~ 25,000+ TPS**로 수직 상승시킬 수 있습니다.

---

## 2. 극강의 성능 쥐어짜기 5대 혁신 포인트

```
┌────────────────────────────────────────────────────────────────────────┐
│             LionSQL T-SQL VDBE VM 5대 혁신 아키텍처                     │
├────────────────────────────────────────────────────────────────────────┤
│ 1. [변수 제로-맵화] setVar 6회 Map 쓰기 ➔ 1회 슬롯 배열 직접 인덱싱     │
│ 2. [디스패치 평탄화] 2단계 중첩 switch 호출 ➔ 단일 평탄화 루프로 인라인 │
│ 3. [DML 수식 직결] EvalRowExpr AST 탐색 ➔ 사전 바인딩 레지스터 직결    │
│ 4. [레코드 제로-GC] OpInsert 시 매번 map 생성 ➔ Record / Row 풀링 재사용│
│ 5. [타입 단언 제거] Aux any 인터페이스 단언 ➔ 인라인/사전 컴파일 캐시    │
└────────────────────────────────────────────────────────────────────────┘
```

### 2.1 [혁신 1] `setVar` 변수 접근 제로-맵 슬롯화 (Zero-Map Slot Direct Access)
- **현상 (`vm.go:1015`)**:
  변수 하나 대입할 때마다 `cleanSymbol` 슬라이싱, `f.vars` 2회 쓰기, `strings.ToLower` 힙 할당, `valToAny` 박싱, `f.evalCtx` 4회 쓰기 등 **문자열 할당 3회 + 맵 쓰기 6회**가 매번 발생.
- **혁신안**:
  - 컴파일 타임에 모든 로컬 변수(`@var`)를 고정 정수 슬롯(`SlotIndex int32`)으로 100% 매핑.
  - 런타임에는 `f.slots[slot] = val` (0.2ns 단일 슬라이스 인덱스 쓰기)만 수행.
  - 디버그/로깅 목적 외 런타임 핫패스에서 `map[string]Value` 및 문자열 할당 완전 0건화.

### 2.2 [혁신 2] 2단계 중첩 switch 디스패치 평탄화 (Flattened Direct Dispatch)
- **현상 (`vm.go:723`)**:
  `Execute` 메인 루프에서 1차 switch(`inst.Op`) ➔ `execComputeOp` / `execStorageOp` 함수 호출 ➔ 함수 내부에서 2차 switch(`inst.Op`).
  명령어마다 **함수 호출 스택 프레임 생성 + 이중 분기(Double Dispatch)**가 발생하여 CPU 캐시 분기 예측(Branch Predictor)을 방해.
- **혁신안**:
  - 중간 헬퍼 함수 호출을 인라인화하여 단일 거대 `switch inst.Op` 루프로 평탄화(Flattening).
  - SQLite `sqlite3VdbeExec` 및 LuaJIT 스타일의 단일 평탄화 루프로 CPU 파이프라인 스톨 최소화.

### 2.3 [혁신 3] DML 인라인 레지스터 직결 (Direct Register-to-Storage Binding)
- **현상 (`vm_dispatcher.go:632`)**:
  `OpInsert` / `OpUpdate` 실행 시 컬럼 값들을 채우기 위해 `proc.EvalRowExpr`를 호출하여 AST 재귀 평가를 거침.
- **혁신안**:
  - 컴파일러가 리터럴과 변수의 위치를 레지스터 번호(`data.ValRegs[i]`)로 사전 컴파일.
  - 런타임에는 `rec.Set(col, f.regs[regIdx])`으로 AST 재귀 순회 없이 0ns 레지스터 직접 전송.

### 2.4 [혁신 4] `OpInsert` 레코드 제로-앨로케이션 풀링 (Record / Row Recycling)
- **현상 (`vm_dispatcher.go:582`)**:
  `rec := make(storage.Record)`로 매 행마다 `map[string]Value` 신규 힙 할당. 초당 5,000건 삽입 시 매초 5,000개의 맵이 GC에 누적.
- **혁신안**:
  - `sync.Pool` 기반 `storage.Record` 재사용 버퍼 또는 테이블 스키마 컬럼 순서에 맞춘 `storage.Row` 슬라이스 버퍼 직접 재활용.
  - DML 삽입 핫패스 **0-Allocation** 달성.

### 2.5 [혁신 5] 명령어 `Aux any`의 인터페이스 타입 단언 제거 (Type Assertion Elimination)
- **현상**:
  `data := f.aux(inst).(*proc.DMLData)`처럼 매 명령어마다 인터페이스 타입 단언 수행.
- **혁신안**:
  - 명령어 구조체 내에 자주 쓰이는 포인터를 인라인화하거나 컴파일된 포인터를 사전 캐싱하여 RTTI 조회 비용 제거.

---

## 3. 구현 단계 및 단계별 목표

1. **Phase 1: 변수 제로-맵 슬롯화 (`PERF-VDBE-SLOT-1`)**:
   - 컴파일러 심볼 테이블 기반 로컬 변수 슬롯 인덱싱 완전화.
   - `setVar` 및 `getVar`의 핫루프 맵 할당 제거.
   - 예상 효과: 루프 연산 처리량 3~5배 증가.
2. **Phase 2: DML 레코드 풀링 및 수식 바이패스 (`PERF-VDBE-DML-POOL-1`)**:
   - `OpInsert` / `OpUpdate` 레코드 풀링 및 AST 바이패스 레지스터 직결.
   - 예상 효과: 단일 행 DML 지연 시간 200µs ➔ 50µs 단축.
3. **Phase 3: 디스패치 루프 평탄화 (`PERF-VDBE-FLATTEN-1`)**:
   - `Execute` 단일 평탄화 루프 전환 및 분기 예측 최적화.

---

## 4. 검증 계획 및 목표 지표

| 지표 | 현재 VDBE VM (LionSQL 0.90 최신 실측) | 5대 혁신 적용 후 목표치 |
| :--- | :---: | :---: |
| **P50 레이턴시** | 4.8ms ~ 5.8ms (최저 200µs) | **최저 50µs 이하** |
| **P99 레이턴시** | 26.78ms (재고 선점 50 워커 기준) | **10ms 이하** |
| **단일 행 변수 대입** | ~80ns (할당 3회, 맵 6회) | **~0.2ns (할당 0, 맵 0)** |
| **DML 삽입 GC 압력** | 행당 맵 1개 힙 할당 | **0-Allocation (Pool 재활용)** |
| **최대 TPS (50 워커)** | **7,100 TPS** (재고 7,100, 주문 5,913, 원장 5,376) | **12,000 ~ 25,000+ TPS** |
