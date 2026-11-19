# LionSQL 저비용 고효율 성능 최적화 계획 (Performance Optimization Plan)

- **작성일**: 2026-11-19
- **대상**: LionSQL 실행 엔진 (`pkg/execution`, `pkg/engine`, `pkg/wire`, `pkg/proc`)
- **목표**: 아키텍처 대규모 개편 없이, 핫 패스(Hot Path)에 남아 있는 불필요한 힙 할당, 강제 디코딩, 반복 조회를 국소 수정으로 제거하여 OLTP/OLAP 처리량과 GC 지연을 즉시 개선한다.

---

## 1. 배경 및 원칙

현재 LionSQL 엔진에는 `Arena`, `LazyTuple`, `Chunk` 배치 실행, `bufio.NewWriterSize(32KB)`, `PlanCache`, `PK Bypass` 등 핵심 최적화 기반이 이미 구축되어 있다.
그러나 실제 실행 경로를 감사한 결과, 몇몇 국소 지점이 기존 최적화를 무력화하거나 대량의 힙 객체/GC 압력을 유발하고 있다.

**최적화 원칙**
1. 대규모 구조 변경 금지: 파일 1~2개 단위의 국소 수정으로 범위를 제한한다.
2. 의미론 보존: 기존 회귀 테스트(`go test -tags wiredtiger ./pkg/...`) 100% 통과를 전제로 한다.
3. 수치 기반 검증: 각 단계 적용 전후 `go test -bench -benchmem`으로 ns/op, B/op, allocs/op 변화를 기록한다.

---

## 2. 최적화 항목 요약 (우선순위 순)

| 순위 | 항목 | 대상 파일 | 수정 규모 | 기대 효과 |
|---|---|---|---|---|
| P0 | Filter/Project 무조건 `ToEvalMap()` + `Materialize()` 제거 | `pkg/execution/filter.go`, `project.go` | 소 (~20줄) | 일반 WHERE/SELECT CPU 및 GC 부하 수 배 감소, `LazyTuple` 최적화 복원 |
| P1 | `AggregateIterator` 그룹당 9개 map 할당을 슬라이스로 전환 | `pkg/execution/aggregate.go` | 중 (~120줄) | 고카디널리티 `GROUP BY` 메모리 70%+ 절감 및 속도 향상 |
| P2 | `sqlexec_select.go` `Values`/`Rows` 이중 할당 제거 | `pkg/engine/sqlexec_select.go`, `pkg/wire/server.go`, `pkg/wire/tds/server.go` | 중 (~80줄) | 대량 행 `SELECT` 힙 할당량 절반 이하로 축소 |
| P3 | `HashJoinIterator` 정수 키 Fast-Path 및 키 인덱스 캐싱 | `pkg/execution/join.go` | 중 (~100줄) | `INT`/`BIGINT` PK-FK 조인 문자열 할당 제거 |
| P4 | `ProjectionIterator` 일반 컬럼 `srcIdx` 바인딩 및 지연 디코딩 | `pkg/execution/project.go` | 소 (~30줄) | 와이드 테이블 부분 컬럼 SELECT 시 디코딩/조회 비용 대폭 감소 |
| P5 | `PATINDEX` 등 정규식 컴파일 결과 캐싱 | `pkg/proc/builtins_string.go` | 소 (~20줄) | 패턴 매칭 반복 호출 시 정규식 재컴파일 비용 제거 |

---

## 3. 단계별 상세 구현 계획

### Phase 1 (P0): Filter/Project의 무조건적인 `ToEvalMap()` 제거

#### 현재 병목
- `pkg/engine/sqlexec_select.go:155-188`에서 모든 SELECT 쿼리에 `opts.SubqueryExecutor`를 non-nil로 설정한다.
- `pkg/execution/builder.go:87, 107, 124`가 모든 `FilterIterator`와 `ProjectionIterator`에 이를 주입한다.
- 결과적으로 서브쿼리가 전혀 없는 단순 `WHERE` / `SELECT`에서도:
  - `pkg/execution/filter.go:170-177`: 스캔되는 **모든 행마다** `t.ToEvalMap()`과 클로저를 생성한다.
  - `pkg/execution/project.go:225-232`: 통과한 **모든 행마다** `tuple.ToEvalMap()`과 클로저를 생성한다.
- `ToEvalMap()`이 `LazyTuple`을 강제 디코딩하고 맵을 할당하므로, 필터에서 탈락할 행까지 전부 디코딩되어 `scan.go`의 `LazyTuple` 및 `Arena` 최적화가 무력화된다.

#### 수정 방안
1. `FilterIterator`와 `ProjectionIterator`에 `hasSubquery bool` 필드를 추가하고, 생성자/초기화 시점에 AST(`*proc.SubqueryExpr`, `*proc.ExistsExpr`, `*proc.InSubqueryExpr`) 또는 조건 문자열의 서브쿼리 존재 여부를 1회만 판별한다.
2. `hasSubquery == false`이면 `subExec = nil`로 두고 `ToEvalMap()`을 호출하지 않는다.
3. `hasSubquery == true`인 경우에도 `evalMap` 생성을 `subExec` 클로저 내부(실제 호출 시 1회 지연 생성)로 이동시켜 단락 평가(short-circuit)로 건너뛴 행에서는 할당이 일어나지 않게 한다.

#### 주의사항
- 상관 서브쿼리(`EXISTS`, `IN (SELECT ...)`, 스칼라 서브쿼리)가 있는 경우 `ToEvalMap()`이 기존과 동일하게 동작하는지 기존 서브쿼리 테스트로 반드시 검증한다.

---

### Phase 2 (P1): `AggregateIterator` 그룹당 9개 Go map 할당 제거 및 키 조립 최적화

#### 현재 병목
- `pkg/execution/aggregate.go:764-795`:
  - `GROUP BY` 루프에서 매 행마다 `lookupVal(tuple, k)`로 문자열 컬럼명 조회를 수행하고 `fmt.Sprintf("%v", val)`로 그룹 키를 조립한다.
  - 새 그룹이 생성될 때마다 `groupAccumulator` 내부에 Go map 9개(`counts`, `sums`, `decSums`, `isDecimal`, `mins`, `maxs`, `values`, `strLists`, `tDigests`)를 무조건 `make`로 할당한다.
  - 그룹 수가 10만 개이면 90만 개의 map 객체가 힙에 생성되어 GC 부담이 급증한다.

#### 수정 방안
1. `AggregateIterator.Open()` 시점에 `a.fields`를 스캔하여 쿼리에서 실제로 사용하는 집계 종류 비트마스크(`needCounts`, `needSums`, `needDec`, `needMinMax`, `needValues`, `needStrLists`, `needTDigest`)를 계산한다.
2. `groupAccumulator`의 `map[int]T` 9개를 고정 길이(`len(a.fields)`) 슬라이스 `[]T`로 교체하고, 비트마스크상 필요한 슬라이스만 할당한다.
3. `Open()` 시점에 `a.groupKeys`의 자식 컬럼 인덱스를 `[]int`로 사전 해석(`groupKeyIdx`)하고, 매 행 키 생성 시 `fmt.Sprintf` 대신 재사용 가능한 `[]byte` 버퍼 + `strconv.AppendInt`/`AppendFloat`를 사용한다.

#### 주의사항
- `ROLLUP` / `CUBE`의 `mask` 처리와 `NULL` 그룹 키 구분(빈 문자열 vs `NULL`)이 기존 동작과 달라지지 않도록 구분 바이트 규칙을 유지한다.

---

### Phase 3 (P2): `sqlexec_select.go`의 `Values`/`Rows` 이중 할당 제거

#### 현재 병목
- `pkg/engine/sqlexec_select.go:219-230`:
  - 모든 SELECT 결과 행에 대해 `values = append(values, vals)`와 `rows = append(rows, tuple.ToMap())`를 동시에 수행한다.
  - 10만 행 반환 시 `[][]any` 외에 10만 개의 `map[string]any`가 추가 할당된다.
- 이미 `pkg/wire/server.go:1941, 2101`(`writePgDataRows`)와 `pkg/wire/tds/server.go:1442`(`streamQueryRows`)는 `res.Values` 기반 인코딩을 지원하고 있다.

#### 수정 방안
1. `QueryResult`에 `RowCount() int` 헬퍼와 `EnsureRows() []map[string]any` 지연 변환 메서드를 추가한다.
2. Wire 프로토콜(PGWire / TDS) 실행 경로에서는 `tuple.ToMap()` 호출을 생략하고 `Values`만 채워 반환한다.
3. `pkg/wire/server.go` 및 `pkg/wire/tds/server.go`에서 `len(res.Rows)`로 행 수를 세거나 `res.Rows[0]`으로 컬럼 타입을 샘플링하는 코드를 `res.Values` 기준으로 통일한다.
4. REST API(JSON 직렬화)나 내부 Go 테스트 호출자처럼 `Rows` 맵이 필요한 경우 `EnsureRows()`를 통해 1회만 생성한다.

#### 주의사항
- `DISTINCT` 중복 제거(`deduplicateRowsAndValues`)가 `Rows` 없이 `Values`만으로 동작하도록 분기 처리해야 한다.
- 외부 패키지나 기존 테스트가 `res.Rows`를 직접 참조하는 경우 하위 호환성을 깨지 않도록 옵션 플래그(`SkipRowMaps bool`)를 Wire 세션 경로에만 먼저 활성화한다.

---

### Phase 4 (P3): `HashJoinIterator` 정수 조인 키 Fast-Path 및 인덱스 캐싱

#### 현재 병목
- `pkg/execution/join.go:137, 167, 187, 835-865`:
  - 단일 `map[string][]hashMatch` 구조여서 `INT`/`BIGINT` PK-FK 조인에서도 매 행마다 `strconv.FormatInt` 문자열 할당이 발생한다.
  - 매 행마다 `extractTupleKey`가 `strings.TrimSpace`와 컬럼명 해시 조회를 반복한다.
  - 매 행마다 `EstimateTupleSize(tuple)`를 호출해 `work_mem`을 검사한다.

#### 수정 방안
1. `Open()` 시점에 단일 컬럼 등가 조인 키의 `leftKeyIdx`, `rightKeyIdx`를 1회 해석해 캐싱한다.
2. 빌드 단계 첫 비-NULL 키가 `int64`인 경우 `map[int64][]hashMatch` 전용 테이블(`intHashTable`)을 사용하고, 비정수 키가 섞이면 기존 `string` 테이블로 폴백한다.
3. `work_mem` 추정은 매 행 대신 64행 단위(또는 청크 단위)로 묶어 수행한다.

---

### Phase 5 (P4): `ProjectionIterator` 일반 컬럼 `srcIdx` 바인딩 및 `LazyTuple` 지연 디코딩

#### 현재 병목
- `pkg/execution/project.go:96, 195-242`:
  - `SELECT id, name FROM tbl`처럼 명시적 컬럼을 선택할 때 `projField.srcIdx`가 `-1`로 남아 매 행마다 `tuple.GetOK(f.expr)` 해시 조회를 수행한다.
  - `projectTuple` 222행에서 무조건 `tuple.Materialize()`를 호출하여, 50개 컬럼 테이블에서 2개 컬럼만 조회해도 나머지 48개 컬럼까지 모두 디코딩한다.

#### 수정 방안
1. `ProjectionIterator.Open()` 시점에 `p.child.ColIndexMap()`을 조회해 단순 컬럼 참조 필드의 `srcIdx`를 사전 바인딩한다.
2. 모든 프로젝션 필드가 `srcIdx >= 0`인 단순 컬럼 프로젝션인 경우, `tuple.Materialize()` 대신 `tuple.Get` / `tuple.Lazy.Get(f.srcIdx)`로 필요한 컬럼만 디코딩하고 `Arena`가 있으면 `vals` 슬라이스도 `Arena`에서 할당한다.

---

### Phase 6 (P5): `PATINDEX` 정규식 컴파일 캐싱

#### 현재 병목
- `pkg/proc/builtins_string.go:132`: 패턴 매칭 호출마다 `regexp.Compile("(?i)" + rx.String())`을 매 행 재컴파일한다.

#### 수정 방안
- 최대 크기 제한(예: 1,024 엔트리)이 있는 경량 캐시(`sync.RWMutex` + `map[string]*regexp.Regexp`, 한도 초과 시 초기화 또는 LRU)를 추가해 컴파일된 `*regexp.Regexp`를 재사용한다.

---

## 4. 검증 및 벤치마크 계획

### 4.1 정확성 회귀 검증 (각 Phase 완료 시 필수 실행)
```bash
go test -tags wiredtiger ./pkg/execution/... ./pkg/engine/... ./pkg/proc/... ./pkg/wire/...
```

### 4.2 성능 벤치마크 측정 (Before / After 비교)
각 Phase 적용 전후로 아래 시나리오에 대해 `go test -bench -benchmem -count=5`를 실행하고 `benchstat`으로 비교한다.
1. **풀스캔 + 단순 WHERE 필터** (10만 행, 선택도 1%): Phase 1, 5 효과 측정
2. **고카디널리티 GROUP BY** (10만 행, 그룹 1만~5만 개, `SUM`/`COUNT`): Phase 2 효과 측정
3. **대량 행 SELECT 응답** (5만 행 반환, PGWire 경유): Phase 3 효과 측정
4. **INT PK-FK Hash Join** (10만 행 x 1만 행): Phase 4 효과 측정
5. **PATINDEX 포함 WHERE 스캔** (1만 행): Phase 6 효과 측정

---

## 5. 실행 권장 순서

1. **Phase 1 (`Filter`/`Project` `ToEvalMap` 지연화)** 부터 착수: 수정량이 가장 작고 모든 SELECT/WHERE 쿼리에 즉각적인 효과가 발생한다.
2. **Phase 2 (`AggregateIterator` 슬라이스화)** 와 **Phase 3 (`Values`/`Rows` 이중 할당 제거)** 를 순차 진행하여 분석/대량 조회 병목을 해소한다.
3. **Phase 4, 5, 6** 을 마무리하고 전체 벤치마크 리포트를 정리한다.

---

## 6. 실행 현황 및 결정 사항 (Status & Decisions)

- **적용 완료 (1.0 이전 핫 패스 최적화 완료, 부작용 최소화 검증)**:
  - `PERF-OPT-FILTER-PROJECT-EVALMAP-1` (Phase 1 / P0): Filter/Project 무조건 `ToEvalMap()` 및 클로저 힙 할당 지연화 완료 (커밋 `51a68543`)
  - `PERF-OPT-AGGREGATE-GROUP-SLICES-1` (Phase 2 / P1): `AggregateIterator` 그룹당 9개 map 할당을 고정 슬라이스로 전환 완료 (커밋 `376887cb`)
  - `PERF-OPT-PATINDEX-REGEX-CACHE-1` (Phase 6 / P5): `PATINDEX` 등 내장 함수 정규식 컴파일 Bounded RWMutex 캐싱 완료 (커밋 `ed3d3e81`)
- **Post-1.0 이관 (사용자 지침에 따른 잔여 Pending 항목 1.0 이후 검토)**:
  - `PERF-OPT-SELECT-VALUES-ROWS-DEDUPE-1` (Phase 3 / P2): Wire 세션 한정 옵트인 및 EnsureRows() 적용 검토 -> `Post-1.0` 이관
  - `PERF-OPT-HASH-JOIN-INT-FASTPATH-1` (Phase 4 / P3): 정수-문자열 이종 타입 조인 안전 검증 후 적용 검토 -> `Post-1.0` 이관
  - `PERF-OPT-PROJECTION-SRCIDX-DEFER-DECODE-1` (Phase 5 / P4): 와이드 테이블 선택 디코딩 -> `Post-1.0` 이관
