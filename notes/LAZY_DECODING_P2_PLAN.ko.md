# P-2 WiredTiger VDBE 바이너리 튜플 지연 역직렬화 (Lazy Decoding) 구현 계획서 (`notes/LAZY_DECODING_P2_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-04  
> - **최종 갱신일**: 2026-11-04  
> - **문서 상태**: [x] Completed (구현 및 검증 완료)  
> - **과제 식별 코드**: `P-2` (`PERF-LAZY-DECODE-1`)  
> - **실측 결과**:  
>   - **스캔 쿼리 소요 시간**: `2,774,208 ns/op` ➔ **`420,793 ns/op` (6.59배 가속, 지연시간 -84.8% 절감)**  
>   - **힙 메모리 할당량**: `6,305,980 B/op` ➔ **`790,094 B/op` (8.0배 메모리 절감, -87.5% 삭감)**  
>   - **메모리 할당 횟수**: `30,983 allocs/op` ➔ **`4,801 allocs/op` (84.5% 할당 횟수 감소)**  
>   - **정합성 검증**: `go test -tags wiredtiger -v -race` 100% PASS (0 data race)  
> - **연계 문서**:  
>   - 액티브 대시보드: [`TODO.md`](../TODO.md)  
>   - 태스크 체계: [`notes/TAXONOMY.ko.md`](TAXONOMY.ko.md) (`P-2`)  

---

## 1. 개요 및 병목 현상 분석

### 1.1 현행 WiredTiger Eager Decoding의 문제점
- 현재 `WTCursor.Row()` 및 `scanCursorRows`는 행을 읽을 때마다 `storage.DecodeTupleToRow(schema, data)`를 즉각 호출합니다.
- 테이블에 컬럼이 수십 개(예: 20~50개) 있고, `WHERE status = 1` 조건을 만족하는 행이 1% 미만인 경우:
  - 99%의 탈락하는 행조차 모든 컬럼의 데이터(문자열, JSON, 날짜 등)를 파싱하고 `[]Value` 슬라이스를 생성하느라 CPU 사이클과 힙 할당을 과도하게 소모합니다.

### 1.2 Lazy Decoding / Direct Column Extraction 전략
- `pkg/storage/tuple.go`에 이미 구현된 `ExtractColumnFromTuple`은 0 allocs, 25.8ns로 단일 컬럼을 즉각 추출합니다 (전체 디코딩 223ns 대비 8.6배 가속).
- `scanCursorRows` 및 VDBE `RawTupleCursor` 경로에서:
  - WHERE 절 평가 시 필요한 컬럼만 먼저 추출하여 조건 검사.
  - 조건 불일치 시 전체 행 디코딩을 완전히 건너뜀(Skip).
  - 조건을 통과한 행만 최종 레코드/행으로 디코딩.

---

## 2. 세부 구현 단계

1. **Step 1: Baseline 벤치마크 작성 및 측정**
   - 다중 컬럼 테이블(예: 15컬럼)에서 선택도 10% 조건 필터 스캔 벤치마크 생성 및 사전 기준치 기록.
2. **Step 2: VDBE 커서 스캔 경로 지연 디코딩 연동**
   - `pkg/proc/runtime/vm.go`의 `scanCursorRows`에서 `cursor`가 `storage.RawTupleCursor`를 지원할 경우 조건 평가 최적화.
   - 단순 비교 수식(`BinaryExpr`)에서 컬럼 값 추출 시 `ExtractColumnFromTuple` 적용.
3. **Step 3: 성능 비교 및 회귀 검증**
   - 사후 벤치마크 실행으로 ns/op, allocs/op 개선폭 실측.
   - `go test -tags wiredtiger -v -race ./...` 100% 통과 확인.
4. **Step 4: 결과 평가 및 유지/롤백 결정**

---

## 3. 검증 지표 및 롤백 기준
- **성능 목표**: 조건부 스캔 쿼리에서 CPU 처리 시간 30% 이상 단축 및 할당량 대폭 감소.
- **롤백 기준**: 기존 SQLLogicTest, Babelfish SLT, 실전 앱 테스트 중 단 1건이라도 실패하거나 데이터 레이스 발생 시 즉각 롤백.
