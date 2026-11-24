# Hash Join Small-Table Build-Side 강제 및 Chunk Buffer Pool 재사용 아키텍처 플랜 (`notes/plans/HASH_JOIN_SMALL_BUILD_PLAN.ko.md`)

## 1. 개요 및 배경

LionSQL의 조인 성능을 PostgreSQL/MySQL C 엔진 수준(15~20ms)으로 최적화하기 위해, 현행 Hash Join 연산자의 핵심 병목을 진단하고 해결책을 제시한다.

### 현행 문제점
1. **Build Side 비최적화 (사용자 쿼리 순서 종속성)**:
   - 현재 `HashJoinIterator`는 무조건 오른쪽 자식(`right`)을 Build Table(해시 맵 버퍼링 대상)로 지정하고 왼쪽 자식(`left`)을 Probe Table로 지정한다.
   - 사용자가 `FROM small_table s JOIN large_table l ON s.id = l.id`를 실행할 경우, 1만 건 이상의 대형 테이블(`l`)을 통째로 해시 맵에 빌드하고 단 5건의 소형 테이블(`s`)로 프로브하는 심각한 역전 현상이 발생한다.
   - 해시 테이블 크기가 수 MB 이상으로 팽창하여 L1/L2 CPU 캐시를 벗어나고 메모리 할당 및 해시 충돌 비용이 급증한다.
2. **조인 매칭 시 매 행 힙 메모리 할당 (`make([]any)`)**:
   - `mergeTuplesWithArena`는 `arena == nil`일 경우 매 매칭 튜플마다 `make([]any, leftLen+rightLen)`를 호출하여 힙 메모리를 신규 할당한다.
   - 1만 건 조인 시 1만 번 이상의 슬라이스 할당과 GC 가비지가 발생하여 쿼리 레이턴시를 지연시킨다.
3. **소형 테이블에서의 Map 버킷 오버헤드**:
   - 빌드 테이블의 행 수가 수 건~수십 건(<=256건)에 불과한 경우에도 Go 런타임 `map[int64][]hashMatch` / `map[string][]hashMatch`의 버킷 할당 및 해시 연산 오버헤드가 발생한다.

---

## 2. 핵심 최적화 설계

### 2.1 CBO 기반 Small-Table Build-Side 강제 스왑
- **위치**: `pkg/planner/planner.go` (`buildSingleJoinNode`)
- **로직**:
  - `cleanType == "INNER"` 또는 `"CROSS"`인 경우:
  - `currentEstimatedRows < rRows` (즉, 왼쪽 테이블의 카디널리티가 오른쪽 테이블보다 작음)이면:
    - `nextNode.BuildSide = "LEFT"`로 지정.
    - CBO 비용 계산 시 작은 테이블을 Build Table로, 큰 테이블을 Probe Table로 계산하여 정확한 비용 반영.
- **실행기 바인딩**: `pkg/execution/builder.go`에서 `plan.BuildSide`를 읽어 `hIter.SetBuildSide(plan.BuildSide)` 호출.

### 2.2 HashJoinIterator의 Inverted Build/Probe 실행
- **위치**: `pkg/execution/join.go` (`HashJoinIterator`)
- **실행 원리**:
  - `buildSide == "LEFT"`인 경우:
    - `Open()` 단계에서 `left`를 해시 테이블로 빌드.
    - `Next()` / `nextTuple()` 단계에서 `right`를 프로브.
    - 매칭 시 `mergeTuplesWithArena(leftMatch, rightProbe, h.colIndexMap, h.arena)`를 호출하여 출력 튜플의 컬럼 순서(`left` 컬럼 우선, `right` 컬럼 후속)를 100% 보존.
  - 이를 통해 상위 프로젝션, `Schema()`, `ColIndexMap()`에 아무런 부작용 없이 투명하게 동작 보장.

### 2.3 자체 Arena 보유를 통한 Zero-Alloc Tuple Merge (Buffer Pool 재사용)
- **위치**: `pkg/execution/join.go`
- **로직**:
  - 상위에서 `opts.Arena`가 전달되지 않더라도(`h.arena == nil`), `HashJoinIterator.Open()` 시 `storage.GetArena()`를 호출하여 `h.ownedArena`를 확보.
  - `h.arena = h.ownedArena`로 바인딩하여 모든 튜플 병합 시 `arena.AllocAnySlice()`를 통해 8바이트 정렬 슬랩에서 버퍼 할당.
  - `Close()` 시 `storage.PutArena(h.ownedArena)`로 $O(1)$ 즉시 반환하여 힙 할당 및 GC 압력을 0으로 만듦.

### 2.4 L1/L2 캐시 친화적 Flat Array Scan (소형 테이블 최적화)
- **위치**: `pkg/execution/join.go`
- **로직**:
  - 빌드 테이블의 총 튜플 수가 $\le 256$건인 경우, `flatIntMatches []flatIntMatch` 평탄화 슬라이스에 직접 저장.
  - 정수 단일 키 프로브 시 Go 맵 해싱/버킷 포인터 추적 대신 단일 CPU 캐시 라인(L1 캐시) 내 선형 스캔 수행.
  - 10,000건 × 5건 조인에서 맵 오버헤드를 완전히 제거하여 나노초 단위 프로브 달성.

---

## 3. 검증 계획 (3-Stage Pipeline per [RULE:PipelineVerification])

1. **Stage 1 (Build)**: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. **Stage 2 (Benchmark & Unit Tests)**:
   - `pkg/execution/join_bench_test.go`: 10,000건 × 5건 조인 벤치마크 (Left Small vs Right Small 대칭 성능 검증)
   - `pkg/planner/planner_test.go`: BuildSide 선택 로직 단위 테스트
   - `go test -v -race ./pkg/planner/... ./pkg/execution/...`
   - SQLLogicTest 코어 스위트 회귀 검증 (`./bin/sqllogictest -dir test/slt/core -parallel 16`)
   - 오라클 불변성 검증 (`bash scripts/verify_no_slt_mutation.sh`)
3. **Stage 3 (Commit & Push)**:
   - Conventional Commits 준수, 데코레이션 이모지 배제 per `[RULE:StyleIntegrity]`.
   - sprint DB 완료 처리 및 `TASKS.md` 동기화 per `[RULE:ContinuousBacklog]`.
