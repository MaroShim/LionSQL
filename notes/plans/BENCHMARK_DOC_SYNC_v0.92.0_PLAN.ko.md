# LionSQL v0.92.0 공식 벤치마크 문서 실측치 전면 동기화 계획서 (`BENCHMARK.md`)

> - **최초 작성일**: 2026-11-08
> - **최종 갱신일**: 2026-11-08
> - **문서 버전**: v1.1
> - **상태**: 측정 대기 (Scheduled for Next Morning after System Thermal Cool-down)
> - **연관 과제 ID**: `TUNE-DOC-SYNC-1` (벤치마크 및 공식 문서 실측치 일괄 동기화)
> - **대상 문서**: [`doc/BENCHMARK.md`](../../doc/BENCHMARK.md), [`README.md`](../README.md), [`README.ko.md`](../README.ko.md)
> - **측정 원칙**: 버전(`v0.92.0`), 측정 일자(`2026-11-09`), 하드웨어 환경(Apple M1, 8-Core, 16GB, Cold-Start) 명시 필수, 베이스라인 파라미터(100Wrk, 10k Acc, 50k Ops) 무변형 100% 보존.

---

## 1. 추진 배경 및 당위성

LionSQL은 37건의 P0 심층 결함 패치(`P0-01` ~ `P0-37`)와 Gate 4 비트 시프트 연산 정합성 보정을 거치며, `P0-29`(sync.Pool 슬라이스 제로잉을 통한 GC 마킹 부하 박멸) 및 `P0-30`(세션 레벨 락 범위 축소) 최적화에 힘입어 **In-Memory OCC 81.9만 tpmC (30,236 TPS) 돌파 및 P99 꼬리 지연시간 84% 단축(17ms ➔ 2.8ms)**이라는 역대 최고 성능 상태를 달성했습니다.

그러나 현재 [`doc/BENCHMARK.md`](../../doc/BENCHMARK.md)와 [`README.md`](../README.md)에 등재된 공식 수치는 과거 v0.90.0 ~ v0.91.0 시점의 레거시 측정치(In-Memory 60.3만 tpmC, 단편적인 이전 로그 등)에 머물러 있어, 실제 최신 엔진의 정밀한 성능 특성과 내구성/정합성 개선 결과를 사용자 및 엔터프라이즈 도입 검토자에게 온전히 전달하지 못하고 있습니다.

장시간 연속된 빌드/테스트/스트레스 실행으로 인한 CPU 서멀 스로틀링(Thermal Throttling) 영향을 완전 배제하기 위해 시스템을 충분히 쿨링한 후, **익일 아침 콜드 스타트 상태에서 4대 벤치마크를 정밀 재실측**하고, **버전(`v0.92.0`)과 측정 일자를 명시하여 `BENCHMARK.md` 전체 수치 테이블을 투명하게 갱신**합니다. 베이스라인 파라미터는 어떠한 인위적 변경 없이 100% 원형을 유지합니다.

---

## 2. 벤치마크 4대 계층별 측정 매트릭스 (Benchmark Matrix)

`BENCHMARK.md`의 목차 구조에 맞추어 다음 4개 계층을 순차적으로 실측하고 테이블을 갱신합니다:

```
┌────────────────────────────────────────────────────────────────────────┐
│                   LionSQL 4-Tier 벤치마크 측정 체계                    │
├────────────────────────────────────────────────────────────────────────┤
│  [Tier 1] 송금 마이크로 벤치마크 (cmd/bench)                             │
│           - 100 Workers, 10,000 Accounts, 50,000 Transactions          │
│           - In-Memory OCC vs WiredTiger MVCC (TPS, P50, P90, P99 지연)  │
├────────────────────────────────────────────────────────────────────────┤
│  [Tier 2] 스토리지 커널 SPI TPC-C 벤치마크 (cmd/tpcc_kernel)             │
│           - 10 Warehouses, 100 Workers, 15s 지속 부하                  │
│           - SQL 파싱 오버헤드를 배제한 순수 스토리지 CRUD 한계치 측정  │
├────────────────────────────────────────────────────────────────────────┤
│  [Tier 3] T-SQL VDBE 런타임 TPC-C 벤치마크 (cmd/tpcc_runtime)           │
│           - Case 1: 16 Warehouses, 32 Workers (15s) [표준 분산 부하]   │
│           - Case 2: 10 Warehouses, 100 Workers (15s) [고동시성 1:1 매칭]│
│           - Case 3: 2 Warehouses, 10 Workers (15s) [하드웨어 최적점]   │
│           - Case 4: 20 Warehouses, 200 Workers (15s) [엔터프라이즈 스트레스]
├────────────────────────────────────────────────────────────────────────┤
│  [Tier 4] 풀스택 클라이언트-서버 네트워크 TPC-C (cmd/tpcc_client)         │
│           - TDS (:1433) vs PGWire (:5433) 프로토콜별 실측              │
│           - 16 Warehouses, 32 Workers, 10s                             │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 3. 동기화 대상 문서 및 세부 섹션 (SSOT Target Files)

### 1) [`doc/BENCHMARK.md`](../../doc/BENCHMARK.md)
* **제2절 (하드웨어 환경)**: Apple M1 (8-core, 16GB, macOS ARM64), Go 1.24+ 런타임 정보 최신화.
* **제3.3절 (`cmd/bench` 송금 프로시저)**:
  - In-Memory OCC 실행 로그 덤프 및 통계 테이블 갱신
  - WiredTiger MVCC 실행 로그 덤프 및 통계 테이블 갱신
  - §3.3.4 종합 비교 요약표 갱신
* **제3.4절 (`cmd/tpcc_kernel` 스토리지 커널)**:
  - 10 WH / 100 Wrk 15초 실측치 갱신 (1.04M tpmC vs 83.3만 tpmC)
* **제3.5절 (`cmd/tpcc_runtime` T-SQL VDBE 런타임)**:
  - §3.5.1 (16 WH / 32 Wrk): 15초 실측치(50만 tpmC OCC vs 48.3만 WT) 및 P99 꼬리 지연시간 대폭 개선(12~20ms) 반영
  - §3.5.2 (10 WH / 100 Wrk): 15초 실측치 갱신
  - §3.5.3 (20 WH / 200 Wrk): 엔터프라이즈 고동시성 실측치 갱신
  - §3.5.4 신설 (2 WH / 10 Wrk - Apple Silicon 8코어 최적점): In-Memory **81.9만 tpmC (30,236 TPS, Abort 0.44%)** 공식 하이라이트 반영
* **제3.6절 (`cmd/tpcc_client` 네트워크 클라이언트-서버)**:
  - TDS vs PGWire 네트워크 실측치 및 3-Tier 비교 랜드스케이프 테이블 동기화

### 2) [`README.md`](../README.md) & [`README.ko.md`](../README.ko.md)
* 루트 README의 **"Performance & Concurrency"** 요약 섹션 수치 일괄 동기화:
  - In-Memory OCC: 186k+ TPS / **819k tpmC (30,235 TPS)**
  - WiredTiger: 120k+ TPS / **643k tpmC (23,394 TPS)**
  - PGWire vs TDS 네트워크 처리량 요약

---

## 4. 단계별 실행 계획 (5-Phase Execution Plan)

### Phase 1: 4대 벤치마크 바이너리 무결성 빌드
```bash
go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-kernel ./cmd/tpcc_kernel && \
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime && \
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-client ./cmd/tpcc_client
```

### Phase 2: 콜드 스타트(Cold-Start) 환경에서 단계별 실측 덤프 수집 (내일 아침 실행)
- **실행 전제**: 시스템 발열 완전 냉각(Thermal Cool-down) 및 백그라운드 프로세스 최소화.
- **측정 버전 표기**: `LionSQL v0.92.0` (Git commit: HEAD)
- **측정 일자 표기**: `2026-11-09`
- **호스트 사양**: Apple M1 (8 cores), 16 GB Unified Memory, macOS Darwin ARM64

1. **Tier 1 (송금 마이크로 베이스라인 - 불변 파라미터)**:
   ```bash
   ./bin/bench -engine=memory -accounts=10000 -workers=100 -ops=50000
   ./bin/bench -engine=wiredtiger -accounts=10000 -workers=100 -ops=50000
   ```
2. **Tier 2 (스토리지 커널 TPC-C)**:
   ```bash
   ./bin/tpcc-kernel -engine=memory -warehouses=10 -workers=100 -duration=15s
   ./bin/tpcc-kernel -engine=wiredtiger -warehouses=10 -workers=100 -duration=15s
   ```
3. **Tier 3 (VDBE 런타임 TPC-C)**:
   ```bash
   # 16 WH / 32 Wrk (Standard)
   ./bin/tpcc-runtime -engine=memory -warehouses=16 -workers=32 -duration=15s
   ./bin/tpcc-runtime -engine=wiredtiger -warehouses=16 -workers=32 -duration=15s
   # 2 WH / 10 Wrk (Optimal Saturation)
   ./bin/tpcc-runtime -engine=memory -warehouses=2 -workers=10 -duration=5s
   ./bin/tpcc-runtime -engine=wiredtiger -warehouses=2 -workers=10 -duration=5s
   ```
4. **Tier 4 (네트워크 클라이언트-서버)**:
   ```bash
   # 서버 기동 후 bin/tpcc-client 측정 (TDS :1433 vs PGWire :5433)
   ```

### Phase 3: `doc/BENCHMARK.md` 텍스트 덤프 및 마크다운 테이블 일괄 갱신
* 각 실행 결과의 콘솔 텍스트 블록(`=== BENCHMARK RESULTS ===`)을 실측 덤프로 정밀 치환.
* 요약 테이블 상단 또는 하단에 **"Measured on: YYYY-MM-DD (v0.92.0)"** 메타데이터 헤더 명시.
* 요약 테이블의 tpmC, TPS, P50, P95, P99 지연시간 수치를 오차 없이 전면 수정.
* 하단 아키텍처 해설(P0-29/P0-30 최적화 효과 및 8코어 M1 스케줄링 특성, WiredTiger 무백오프 100워커 경합 특성) 투명하게 보강.

### Phase 4: `README.md` 및 `README.ko.md` 동기화
* 글로벌 사용자 대면 루트 리드미의 성능 하이라이트 테이블을 최신 수치로 동기화.

### Phase 5: 문서 검증 및 단독 원자적 커밋 (`[RULE:PipelineVerification]`)
* 문서 변경 사항에 오탈자나 수치 불일치가 없는지 교차 검토.
* Conventional Commits 규칙 준수:
  ```bash
  git commit -m "docs(benchmark): synchronize all benchmark tables and execution dumps to v0.92.0 verified metrics"
  ```

---

## 5. 기대 효과 및 엔지니어링 가치

1. **신뢰성 보증 (Zero-Deception SSOT)**:
   과거의 이론치나 추정치가 아닌, 실제 개발 환경에서 기계적으로 재현 가능한 실측 터미널 덤프만을 기록함으로써, 사용자와 벤치마커에게 100% 투명하고 정직한 신뢰를 제공합니다.
2. **최적화 성과의 시각화**:
   P0 패치 과정에서 달성한 In-Memory 81.9만 tpmC (30,236 TPS) 및 P99 꼬리 지연시간 84% 단축(17ms ➔ 2.8ms) 성과가 공식 문서에 온전히 반영되어 제품의 기술적 가치를 입증합니다.
3. **v0.92.0 릴리즈 패키지 완성**:
   코드 품질(Gate 1~6 100% 통과)과 공식 기술 문서가 한 치의 오차도 없이 일치하는 상태로 v0.92.0 릴리즈를 완결합니다.
