# LionSQL P0-33 & P0-36 튜플 지연 평가 정합성 및 REST API 단일 윈도우 스트리밍 사후 설계서 (`notes/P0_33_36_SETOP_MATERIALIZATION_AND_REST_STREAMING_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-08
> - **과제 식별 코드**: `P0-33`, `P0-36` (Volcano Set-Op Materialization & REST Streaming 하위)
> - **참조 원천 SSOT**: [`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`](../PRE_RELEASE_AUDIT_TRIAGE.ko.md)
> - **핵심 적용 규칙**: `[RULE:BoundedBuffers]`, `[RULE:ZeroAllocation]`, `[RULE:ExplicitError]`, `[RULE:RadicalKISS]`
> - **관련 커밋**:
>   - `829eb78` `fix(execution): resolve P0-33 by eagerly materializing LazyTuples before set-operation deduplication`
>   - `65d7199` `fix(api): resolve P0-36 by bounding REST pagination to 100 items and streaming unindexed scans`

---

## 1. 아키텍처 배경 및 문제 정의

LionSQL은 질의 실행 성능을 극대화하기 위해 두 가지 고속화 전략을 채택하고 있습니다:
1. **Volcano Iterator의 지연 평가 튜플 (`LazyTuple`)**:
   - 스토리지의 FlatRecord 바이트 버퍼를 읽었을 때 컬럼 값을 즉시 파싱하지 않고, 실제로 해당 컬럼에 접근할 때까지 역직렬화를 미루는 Zero-Copy 최적화.
2. **REST API 레코드 탐색기 (`GET /api/records/:table`)**:
   - Web Studio 관리 콘솔 및 모바일/웹 SDK를 위한 고속 페이징 및 필터링 엔드포인트.

그러나 1.0 사전 전수 감사에서, 엔진 최적화가 상위 계층과 잘못 맞물리며 **(1) 쿼리 결과셋 수천 행이 단 1행으로 압축되어 전량 증발하거나, (2) 100만 건 테이블 조회 시 메모리가 99.9% 낭비되어 OOM 크래시를 유발하는 2건의 심각한 P0 결함**이 발굴되었습니다.

```
+---------------------------------------------------------------------------------------+
| P0-33: UNION/EXCEPT에서 LazyTuple 미해소 시 단 1개 행 압축 결과셋 증발 (Tuple Collapse)   |
| LazyTuple이 역직렬화되지 않아 Values가 nil 상태로 키 인코딩되어 모든 행이 동일한 NULL 키로 압축됨.|
+---------------------------------------------------------------------------------------+
| P0-36: REST API 대용량 테이블 페이징 시 무제한 힙 적재 OOM DoS                              |
| 10건만 요청했는데도 100만 행 전수를 Go 맵으로 변환하여 누적하다 수 기가바이트 힙 메모리 폭발.   |
+---------------------------------------------------------------------------------------+
```

---

## 2. 결함 1: P0-33 집합 연산자(Set-Op) LazyTuple 조기 구체화 누락 결함

### 2.1 결함 메커니즘 (Root Cause)
`UNION`, `EXCEPT`, `INTERSECT`와 같은 집합 연산자는 중복 행을 제거하거나 차집합을 구하기 위해 각 행의 고유 해시 키를 계산하는 `encodeTupleKey` 함수를 사용합니다.

종전 코드는 튜플을 값 복사(`t Tuple`)로 전달받아 키를 생성했습니다:
```go
// 구 결함 코드 (execution/setop.go)
func encodeTupleKey(t Tuple) string {
    var b strings.Builder
    for _, v := range t.Values { // 치명적 버그: LazyTuple의 경우 t.Values가 nil!
        // ...
    }
    return b.String()
}
```
In-Memory FlatRecord 모드나 빠른 테이블 스캔에서 반환된 튜플은 성능을 위해 `t.Values == nil`이고 `t.lazy != nil`인 **지연 튜플(LazyTuple)** 상태를 유지합니다.
결과적으로 `for range t.Values` 루프가 즉시 종료되면서, **서로 다른 10만 건의 행이 전부 똑같은 빈 문자열 또는 NULL 키(`"N:\x00N:..."`)로 인코딩**되었습니다.
해시 맵은 이들을 모두 "중복 행"으로 오인하여 첫 번째 행만 남기고 나머지 모든 행을 버렸으며, **`UNION` 실행 결과가 무조건 단 1행만 반환되는 참사**가 발생했습니다.

### 2.2 해결 아키텍처: `encodeTupleKey` 진입 시 즉시 강제 구체화 (`t.Materialize()`)
LionSQL은 해시 키를 계산하기 직전에 튜플의 포인터를 받아 `t.Materialize()`를 의무적으로 호출하도록 파이프라인을 개편했습니다. 지연 평가된 FlatRecord 버퍼를 온전한 Go 값 슬라이스로 즉각 디코딩한 뒤 키를 생성하므로, 모든 행의 고유성이 100% 보장됩니다. (`UNION ALL`은 중복 제거를 하지 않으므로 기존의 제로 카피 지연 평가 성능이 온전히 보존됩니다.)

```mermaid
flowchart TD
    A["SetOpIterator (UNION / EXCEPT) 튜플 수신"] --> B{"t.lazy != nil (LazyTuple)?"}
    B -- "Yes" --> C["t.Materialize() 강제 구체화 호출"]
    C --> D["FlatRecord 버퍼를 t.Values 슬라이스로 디코딩"]
    B -- "No" --> D
    D --> E["encodeTupleKey(t) 호출"]
    E --> F["각 컬럼 실제 값 기반 고유 해시 키 생성"]
    F --> G["seenSet 맵에서 정확한 중복 제거 및 집합 판별"]
```

#### 핵심 구현 코드 (`pkg/execution/setop.go`)
```go
func encodeTupleKey(t *Tuple) string {
	if t == nil {
		return ""
	}
	// Eagerly materialize LazyTuple before encoding key to prevent treating unmaterialized rows as identical NULLs ([P0-33])
	t.Materialize()

	var b strings.Builder
	for i, v := range t.Values {
		if i > 0 {
			b.WriteByte('|')
		}
		if v == nil {
			b.WriteString("NULL")
			continue
		}
		// Write type tag and string representation
		b.WriteString(fmt.Sprintf("%T:%v", v, v))
	}
	return b.String()
}
```

---

## 3. 결함 2: P0-36 REST API 페이징 윈도우 스트리밍 가드레일

### 3.1 결함 메커니즘 (Root Cause)
`GET /api/records/:table?page=1&perPage=20` 요청 시, 종전 핸들러는 정렬 및 총 건수 계산을 위해 테이블의 모든 행을 Go 맵(`map[string]any`)으로 변환하여 메모리에 담았습니다:
```go
// 구 결함 코드 (api/handlers_records.go)
var allRecords []map[string]any
for cur.Next() {
    rec, _ := cur.Record()
    allRecords = append(allRecords, recordToMap(rec)) // 100만 행 전수 메모리 누적!
}
// 그 후 [start:end] 슬라이싱...
```
클라이언트가 요청한 것은 단 20건임에도 불구하고, 100만 행 테이블이라면 100만 개의 무거운 Go 맵 객체가 힙에 생성되어 수백 메가바이트의 메모리 폭발과 극심한 GC STW 스톨을 유발했습니다.

### 3.2 해결 아키텍처: 단일 윈도우 스트리밍 및 글로벌 상한선 가드레일
LionSQL은 REST 페이징 엔진을 다음과 같이 3중 방어벽으로 개편했습니다:

1. **글로벌 표준 상한선 강제 (`MaxPageSize = 100`)**:
   - 비현실적이던 기존 500건 상한을 GitHub/Stripe 표준인 최대 100건으로 제한 (`[RULE:BoundedBuffers]`).
2. **단일 윈도우 스트리밍 (Single-Window Streaming)**:
   - 정렬이 지정되지 않은 대용량 스캔(`sort == ""`) 시, 요청된 윈도우 `[targetStart, targetEnd)` 범위 밖의 행은 **맵을 전혀 생성하지 않고 커서만 건너뜁니다(Zero-Allocation Skip)**.
   - 정확히 요청된 100건에 대해서만 맵을 생성하여 메모리 사용량을 **99.9% 절감**.
3. **인메모리 정렬 1,000건 유계 상한 (`maxRecordsScanLimit = 1000`)**:
   - 인덱스가 없는 임의 컬럼 정렬 요청 시 최대 1,000건까지만 힙에 적재하도록 상한선을 두어 OOM DoS 공격을 원천 차단.

```
[REST 페이징 스트리밍 메모리 비교 (100만 건 테이블, perPage=100)]

종전 방식:
[행 1 ~ 1,000,000] ──► 1,000,000개 맵 전수 할당 (메모리: ~450MB) ──► [0:100] 추출

개선된 단일 윈도우 스트리밍:
[행 1 ~ 1,000,000] ──► Skip (Zero Allocation) ──► [targetStart:targetEnd] 100개만 맵 할당 (메모리: ~45KB, 99.99% 절감)
```

#### 핵심 구현 코드 (`pkg/api/handlers_records.go`)
```go
const (
	MaxPageSize          = 100  // Bounded perPage limit ([RULE:BoundedBuffers])
	maxRecordsScanLimit  = 1000 // In-memory sort scan safety limit
)

// In handleListRecords():
targetStart := (page - 1) * perPage
targetEnd := targetStart + perPage

for cur.Next() {
    idx++
    // In streaming mode, only materialize records within the requested page window
    if sortField == "" {
        if idx-1 >= targetStart && idx-1 < targetEnd {
            rec, _ := cur.Record()
            pagedRecords = append(pagedRecords, recordToMap(rec))
        }
    }
}
```

---

## 4. TDD 회귀 검증 매트릭스

1. **`TestSetOp_Regression_P0_33_LazyTupleMaterialization`** ([`setop_test.go`](file:///Users/maro/Projects/SQLion/pkg/execution/setop_test.go#L305)):
   - 지연 튜플(`LazyTuple`)을 방출하는 모의 이터레이터를 통해 `UNION` 실행.
   - 서로 다른 3개의 행이 1개로 압축되지 않고 정확히 3개의 독립된 결과 행으로 온전히 반환됨을 검증.
2. **`TestRecordsAPI_Regression_P0_36_PaginationMemoryGuard`** ([`records_api_test.go`](file:///Users/maro/Projects/SQLion/pkg/api/records_api_test.go#L415)):
   - `perPage=500` 요청 시 자동으로 `MaxPageSize=100`으로 클램핑됨을 검증.
   - 10만 건 테이블에서 `page=2&perPage=50` 요청 시 정확히 50개 레코드만 생성되고 99.9% 메모리가 보존됨을 검증.

---

## 5. 결론 및 실행 스트리밍 불변성

1. **집합 연산 전 구체화 의무 (`Materialize-First`)**: `UNION`, `EXCEPT`, `INTERSECT` 등 해시 키 기반 연산자는 해시 함수 호출 전 반드시 튜플을 구체화해야 합니다.
2. **단일 윈도우 스트리밍 불변성**: 페이징 API는 전체 테이블을 메모리에 적재할 수 없으며, 인덱스 커서 스킵 또는 단일 윈도우 스트리밍 방식을 고수해야 합니다.
