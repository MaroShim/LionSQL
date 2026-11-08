# LionSQL P0-29: Go sync.Pool 및 슬라이스 재활용 시 잔류 포인터 메모리 누수 분석 (`notes/P0_29_GO_SYNC_POOL_SLICE_LEAK_POSTMORTEM.ko.md`)

> - **최초 작성일**: 2026-11-08
> - **도메인**: Go 런타임 메모리 모델, 가비지 컬렉터(GC), Zero-Allocation 아키텍처
> - **참조 규칙**: `[RULE:ZeroAllocation]`, `[RULE:BoundedBuffers]`, `[RULE:HandleHygiene]`
> - **관련 과제**: `P0-29` (VDBE VM, Volcano Executor, In-Memory OCC 트랜잭션 풀 일괄 완치)

---

## 1. 개요 (The Slice Re-slicing Illusion)

Go 언어에서 고성능을 추구할 때 가장 흔히 사용하는 기법은 **`sync.Pool`을 통한 버퍼 재사용**과 **`s = s[:0]`을 통한 슬라이스 재활용**입니다. 매번 힙 메모리를 할당(`malloc`)하고 해제하는 오버헤드를 없애 가비지 컬렉터(GC)의 압박을 줄이려는 의도입니다.

그러나 **"슬라이스의 길이를 0으로 줄였다고 해서(`s = s[:0]`), 기존에 들어있던 객체들이 메모리에서 사라지는 것은 아니다"**라는 사실을 간과할 때, 오히려 GC가 절대 수거하지 못하는 치명적인 **영구 메모리 누수(Silent GC Leak)**가 발생합니다.

---

## 2. 작동 메커니즘과 메모리 누수의 본질

### 2.1. Go 슬라이스의 내부 메모리 구조
Go의 슬라이스는 24바이트(64비트 아키텍처 기준)의 3요소 헤더 구조체로 동작합니다:

```go
type SliceHeader struct {
    Data uintptr // 기저 백킹 배열(Backing Array)의 메모리 시작 주소
    Len  int     // 현재 유효한 원소 개수
    Cap  int     // 백킹 배열이 수용할 수 있는 최대 원소 개수
}
```

### 2.2. Tracing GC의 "도달 가능성(Reachability)"과 함정
Go의 Tracing GC(Mark-and-Sweep)는 전역 객체, 스택 프레임, 그리고 **`sync.Pool`**을 루트(Root)로 삼아 포인터를 추적합니다:
1. 슬라이스 원소 타입이 포인터, 인터페이스(`any`), 슬라이스(`[]byte`), 또는 포인터를 포함하는 구조체(`storage.Value`)인 경우, 기저 백킹 배열의 각 슬롯에는 **힙 객체의 8바이트 메모리 주소**가 기록됩니다.
2. 개발자가 `s = s[:0]`을 호출하면 오직 헤더의 `Len` 필드만 `0`으로 바뀔 뿐, **기저 백킹 배열의 `0 ~ Cap-1` 슬롯에 적혀 있는 객체 포인터 주소는 단 1비트도 지워지지 않고 그대로 유지**됩니다.
3. 이 슬라이스를 `sync.Pool`에 반납하면, `sync.Pool` ➔ `백킹 배열` ➔ `힙 객체`로 이어지는 참조 체인이 계속 살아남게 됩니다.
4. 결과적으로, **해당 트랜잭션이나 요청이 이미 종료되었음에도 불구하고 이전 요청의 대용량 데이터, 문자열, 행 버퍼가 영원히 GC되지 않고 힙에 갇히게 됩니다.**

```
[ sync.Pool ] (전역 루트 객체)
      │
      ▼
 [ 슬라이스 헤더 ] { Data: 0x1000, Len: 0, Cap: 128 }
      │
      ▼
 [ 백킹 배열 (0x1000 ~) ]
      ├── 슬롯 0 ~ 9   : nil로 덮어쓰지 않고 방치됨
      └── 슬롯 10 ~ 127: 이전 세션의 대용량 객체 포인터가 영구 잔류!
            │
            ▼
      [ 이전 세션의 대용량 쿼리 결과셋 (수십 MB) ] ➔ 💣 GC 수거 불가 영구 누수!
```

---

## 3. LionSQL 감사에서 적발된 전형적인 4대 누수 패턴

### 패턴 1: `sync.Pool` 반납 전 슬라이스 길이만 축소 (`ReleaseValuesSlice`)
- **문제 코드** (`pkg/execution/iterator.go:385`):
  ```go
  func ReleaseValuesSlice(s []any) {
      if cap(s) > 128 {
          return
      }
      s = s[:0] // 💣 길이만 줄이고 s[0~cap-1] 안의 인터페이스 포인터는 그대로 잔류
      tupleValuesPool.Put(&s)
  }
  ```
- **파급 효과**: 쿼리가 끝났음에도 튜플 컬럼값(`[]any`)이 가리키던 모든 하위 객체들이 풀에 묶여 누수됨.

### 패턴 2: `range` 기반 순회의 착시 (`PutChunk`)
- **문제 코드** (`pkg/execution/chunk.go:37`):
  ```go
  func PutChunk(c *Chunk) {
      for i := range c.Tuples { // 💣 현재 len(c.Tuples) 까지만 순회!
          c.Tuples[i] = Tuple{}
      }
      c.Reset() // c.Tuples = c.Tuples[:0]
      chunkPool.Put(c)
  }
  ```
- **파급 효과**: 필터링으로 인해 `len`이 축소되었거나 `Reset()` 후 호출된 경우, 백킹 배열 `cap`(기본 1,024개)의 나머지 슬롯에 담긴 튜플 포인터들이 전혀 지워지지 않고 풀에 반납됨.

### 패턴 3: VDBE VM 가상머신 프레임 풀의 얕은 반납 (`releaseFrame`)
- **문제 코드** (`pkg/proc/runtime/vm.go:725`):
  ```go
  func (e *Executor) releaseFrame(frame *VMFrame) {
      frame.tx = nil
      frame.session = nil
      frame.prog = nil
      e.framePool.Put(frame) // 💣 regs, slots, vars, evalCtx, rows 포인터 미제거!
  }
  ```
- **파급 효과**: 무거운 저장 프로시저 실행 후 프레임이 풀에 들어가면, 다음 가벼운 프로시저가 실행되더라도 `cap` 뒤쪽의 레지스터 슬롯에 남은 이전 프로시저의 결과 데이터가 영원히 방치됨.

### 패턴 4: 롤백 트랜잭션의 맵 데이터 방치 (`releaseToPoolLocked`)
- **문제 코드** (`pkg/storage/memengine/tx.go:185`):
  ```go
  func (tx *Transaction) releaseToPoolLocked() {
      tx.inPool = true
      txPool.Put(tx) // 💣 tx.local, tx.changes, tx.tableCache를 지우지 않고 반납!
  }
  ```
- **파급 효과**: 롤백된 로우 데이터가 다음 `txPool.Get()`이 발생하기 전까지 풀에 보관되어 불필요한 메모리 점유.

---

## 4. 올바른 해결 원칙 (Golden Rules)

### 원칙 1: 슬라이스를 풀에 반납할 때는 반드시 `clear(slice[:cap(slice)])`를 사용한다
Go 1.21에 도입된 빌트인 `clear()` 함수를 슬라이스의 `cap` 전체 영역에 적용하면, 기저 백킹 배열의 모든 슬롯을 제로 값(`nil`, `0`)으로 덮어씁니다:

```go
// 올바른 패턴: 백킹 배열 자체는 재사용하되 포인터는 완전 소각
if cap(s) > 0 {
    clear(s[:cap(s)]) // 백킹 배열 전체를 즉시 nil/zero로 청소
    s = s[:0]         // 길이 0으로 리셋
}
pool.Put(&s)
```

### 원칙 2: 구조체 풀링 시 전용 `resetForPool()`을 의무화한다
구조체를 풀에 넣기 직전에 내부 슬라이스, 맵, 인터페이스 참조를 전면 해제하는 단일 책임 메서드를 둡니다:

```go
func (f *VMFrame) resetForPool() {
    // 1. 슬라이스 cap 끝까지 zeroing
    clear(f.regs[:cap(f.regs)])
    f.regs = f.regs[:0]

    // 2. 비대해진 맵은 축소 재할당, 작은 맵은 clear() 재사용
    if len(f.vars) > 64 {
        f.vars = make(map[string]storage.Value, 16)
    } else {
        clear(f.vars)
    }

    // 3. 외부 리소스 Close 및 포인터 nil화
    f.rows = nil
    f.columns = nil
    f.tx = nil
}
```

### 원칙 3: 모범 구현 사례 (`pkg/storage/arena.go`)를 준용한다
LionSQL 아레나 얼로케이터에 구현되어 있는 패턴을 시스템 전역 표준으로 확장합니다:

```go
// Return any slabs (clear references to prevent memory leak)
for _, slabPtr := range a.anySlabs {
    clear(*slabPtr) // 슬랩 전체를 nil로 지워 하위 객체 GC 보장
    anySlabPool.Put(slabPtr)
}
```

---

## 5. 결론

Zero-Allocation 고성능 프로그래밍의 핵심은 **"버퍼 껍데기(Memory Slab)는 아끼고 재사용하되, 내용물 알맹이(Reference Pointer)는 가차 없이 소각하는 것"**입니다. 슬라이스 길이만 0으로 줄이는 안일한 패턴을 배제하고, `clear(slice[:cap(slice)])`를 통한 명시적 포인터 절단을 관용구로 확립해야 메모리 안전성과 성능을 동시에 달성할 수 있습니다.
