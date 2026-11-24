# [PERF-GROUPBY-SLOT-HASH-1] GROUP BY & JOIN 인메모리 정수 슬롯 해시 & Zero-Alloc 가속 플랜

- **작성일**: 2026-11-24
- **상태**: IN_PROGRESS
- **태스크 ID**: `PERF-GROUPBY-SLOT-HASH-1` (Priority: P1, Target: v0.95.0)
- **대상 패키지**: `pkg/execution` (`aggregate.go`, `join.go`, `chunk.go`)

> **규칙 준수**:
> - `[RULE:PlanPersistence]`: 설계 및 구현 명세를 물리 파일로 영구 보존.
> - `[RULE:RadicalKISS]`: 불필요한 추상화 레이어를 배제하고 직접적이고 단순한 슬롯 매핑 적용.
> - `[RULE:ZeroAllocation]`: 핫패스 루프 내 힙 메모리 할당 0 바이트 달성 (`sync.Pool` 재활용).
> - `[RULE:PipelineVerification]`: 3단계 파이프라인 (Build ➔ Test `-race` ➔ Commit).
> - `[RULE:StyleIntegrity]`: 장식용 이모지 일체 배제.

---

## 1. 배경 및 병목 분석

현재 LionSQL의 `AggregateIterator`(`pkg/execution/aggregate.go`)는 대용량 데이터 집계 시 심각한 CPU 및 GC 병목에 직면합니다.
1. **문자열 키 조립 오버헤드**:
   - `accumulateSingleTupleString`에서 매 튜플마다 `var gk strings.Builder`를 할당하고 `strconv.Itoa`, `strconv.FormatInt`를 호출하여 콜론 구분자(`len:value`) 문자열을 조립.
   - 100만 건 집계 시 최소 수백만 개의 단기 객체가 힙(Heap)에 할당되어 가비지 컬렉터(GC)에 극심한 압박 유발.
2. **단일 정수 키 Fast-Path의 불완전성**:
   - 단일 정수 키에 대해 `intGroups map[int64]*groupAccumulator`를 수집한 뒤, 집계가 끝나면 모든 정수 키를 `strconv.FormatInt`로 다시 문자열 맵(`groups map[string]*groupAccumulator`)으로 복사/재변환하는 낭비 발생.
3. **Join 연산자의 다중 키 문자열 포맷팅**:
   - `HashJoinIterator`(`pkg/execution/join.go`) 역시 복합 조인 조건에서 매 튜플마다 문자열 조립을 수행하여 Build 및 Probe 핫패스를 지연시킴.

---

## 2. 핵심 아키텍처 및 해결 방안

### 2.1 단일 정수/키 직결 파이프라인 (Direct Materialize)
- 단일 정수 키(`int64`, `int`, `int32`, `uint64` 등)는 문자열 변환 없이 `intGroups map[int64]*groupAccumulator`와 `intGroupOrder []int64`를 유지.
- 집계 완료 후 문자열 맵(`groups`)으로 복사하지 않고, `intGroupOrder`로부터 직접 결과 튜플(`Tuple`)을 생성하여 반환.

### 2.2 복합 키 & 문자열 키를 위한 `sync.Pool` 기반 Zero-Alloc 해싱
- 매 행마다 `strings.Builder`를 생성하는 대신, `sync.Pool`에서 바이트 버퍼(`*bytes.Buffer` 또는 바이트 슬라이스 풀)를 대여.
- 컬럼 값들을 고속 인라인 패킹 후 xxHash64 또는 FNV-1a 기반 64비트 정수 해시(`uint64`) 도출.
- 동일 64비트 해시 슬롯 내에서 원본 튜플 키 값 동등성 검증(Collision Chain)을 지원하여 데이터 무결성 보장.

### 2.3 Hash Join 다중 키 바이트 인코딩 최적화
- `extractTupleKey`에서 문자열 결합 대신 재활용 가능한 버퍼를 통해 인코딩 및 해싱 수행.
- Build 테이블과 Probe 테이블의 매칭 핫패스 메모리 할당 최소화.

---

## 3. 세부 작업 단계 (Action Items)

1. **Phase 1: 단일 정수 키 직결 Materialization 구현**
   - `AggregateIterator.materialize()`에서 `intGroups` 후처리 문자열 복사 루프 제거.
   - `intGroupOrder` 기반 직접 튜플 생성 메서드 구현.

2. **Phase 2: 복합 키 Zero-Alloc 버퍼 풀 및 해시 슬롯 연산자 구현**
   - `sync.Pool` 기반 `groupKeyBufferPool` 도입.
   - 64비트 정수 해시 매핑 및 슬롯 체이닝 구현.

3. **Phase 3: Hash Join 핫패스 연계 최적화**
   - `pkg/execution/join.go` 다중 키 해시 경로 개선.

4. **Phase 4: 벤치마크 및 회귀 테스트**
   - `pkg/execution/aggregate_bench_test.go` 작성 (단일 정수, 복합 키 10만/100만 건).
   - `go test -v -race ./pkg/execution/...` 100% 통과 확인.
   - `bash scripts/verify_no_slt_mutation.sh` 및 SQLLogicTest 집계 테스트 통과 확인.
