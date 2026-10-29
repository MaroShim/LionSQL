# LionSQL 서버 파이프라인(TDS to File System) 종단 최적화 마스터 플랜

> - **최초 작성일**: 2026-10-29
> - **문서 상태**: Phase 1~2 구현 및 실측 검증 완료 (`[x]`), Phase 3~4는 v0.90.0 이후 착수 예정
> - **연관 과제 ID**: `PERF-SERVER-PIPELINE-P1-P2` (Completed `[x]`), `PERF-SERVER-PIPELINE-P3-P4` (Post-1.0)
> - **적용 대상**: `pkg/wire/tds/`, `pkg/engine/`, `pkg/proc/`, `pkg/storage/wiredtiger/`, `pkg/storage/memengine/`

---

## 1. 개요 및 배경

LionSQL은 TPC-C 벤치마크를 통해 스토리지 커널(1.0M~1.4M tpmC) 및 VDBE 런타임(500k~690k tpmC)에서 높은 처리량을 검증했습니다. 최근 클라이언트-서버(TDS/TCP) 네트워크 구조에서도 113,795 tpmC (4,228 TPS, 0.3% Abort)를 달성했으나, 네트워크 소켓 수신부터 디스크 파일 시스템 동기화(`fsync`)에 이르는 전체 수직 파이프라인에서 불필요한 OS 시스템 콜, 힙 메모리 할당, 전역 락 경합 및 파일 시스템 저널링 오버헤드가 여전히 잔존합니다.

본 문서는 **네트워크 소켓(TDS) ➔ 프로토콜 디코딩 ➔ VDBE VM 실행 ➔ 스토리지 동시성 제어 ➔ WAL 디스크 I/O**로 이어지는 전 구간을 단계적으로 고도화하여, 클라이언트-서버 환경에서 150,000+ tpmC (> 5,500 TPS) 및 극한의 P99 지연 시간 단축을 달성하기 위한 구체적인 단계별 실행 계획을 정의합니다.

---

## 2. 단계별 구현 계획 (Phased Execution Roadmap)

```
[Phase 1: TDS 프로토콜 & 소켓 I/O] 
  ├─ 1.1. bufio.Reader 기반 소켓 read() 시스템 콜 병합 (Read Coalescing)
  ├─ 1.2. 32KB 패킷 크기 sync.Pool 다단계 풀링
  └─ 1.3. ConnectionSession 내 ClientSession 포인터 고정 (Lock 경합 제거)
       │
[Phase 2: 쿼리 디코딩 & VDBE 파라미터 바인딩]
  ├─ 2.1. RPC 파라미터 맵 힙 할당 제거 (슬라이스/풀링 구조체 직결)
  └─ 2.2. 이름 기반 프로시저 호출의 Direct PlanCache 바이트코드 디스패치
       │
[Phase 3: 스토리지 엔진 & 커서 캐싱]
  ├─ 3.1. WiredTiger 세션 레벨 범용 LRU 커서 캐시 (LRU Bounded Cursor Cache)
  └─ 3.2. 순수 읽기 쿼리의 트랜잭션 및 WAL 완전 우회 (컴파일러 자동 감지 & ReadOnly 힌트)
       │
[Phase 4: WAL & 디스크 파일 시스템 I/O]
  ├─ 4.1. [Spec-Out] WAL 세그먼트 파일 공간 사전 할당 (WiredTiger 100MB 기본 동작과 중복)
  └─ 4.2. 적응형 마이크로초 그룹 커밋 배칭 (Adaptive Group Commit)
```

---

### Phase 1: TDS 네트워크 프로토콜 & 소켓 I/O 최적화

#### 1.1. `bufio.Reader` 기반 소켓 `read()` 시스템 콜 병합 (Read Coalescing)
- **현상 및 문제점**:
  - [`pkg/wire/tds/packet.go:ReadPacket`](file:///Users/maro/Projects/SQLion/pkg/wire/tds/packet.go#L53)에서 8바이트 헤더를 읽기 위해 `io.ReadFull(conn, hdr[:])` 호출 후, 페이로드를 읽기 위해 `io.ReadFull(conn, chunk)`을 별도로 호출함.
  - 하나의 TDS 요청 패킷마다 **최소 2회의 `read()` OS 시스템 콜** 및 컨텍스트 스위칭 발생.
- **구현 내용**:
  - `ConnectionSession`마다 `sync.Pool`로 관리되는 4KB/8KB `bufio.Reader` 바인딩.
  - OS TCP 수신 버퍼에서 헤더와 페이로드를 단 1회의 시스템 콜로 일괄 수신하도록 리팩토링.
- **검증 기준**:
  - `go test -v ./pkg/wire/tds/...` 통과.
  - 단일 RPC 요청당 OS `read()` 시스템 콜 50% 절감 확인.

#### 1.2. 32KB 패킷 크기 `sync.Pool` 다단계 확장
- **현상 및 문제점**:
  - [`pkg/wire/tds/packet.go:WritePacketWithSize`](file:///Users/maro/Projects/SQLion/pkg/wire/tds/packet.go#L118)에서 `DefaultPacketSize (4096)`에 대해서만 `sync.Pool`(`tdsFramePool`)을 사용.
  - 고성능 드라이버가 `packet size=32768`을 협상한 경우 매 응답 패킷 전송마다 `make([]byte, 32768)` 힙 할당이 발생하여 초당 수십 MB의 GC 가비지 생성.
- **구현 내용**:
  - 다단계 버퍼 풀(`pool4k`, `pool8k`, `pool32k`) 구조 도입.
  - 송신 프레임 할당을 100% `sync.Pool`로 처리하여 제로 할당 응답 달성.
- **검증 기준**:
  - `packet size=32768` 벤치마크 실행 중 힙 메모리 할당 프로파일(`go test -benchmem`)에서 슬라이스 할당 제로 검증.

#### 1.3. `ConnectionSession` 내 `ClientSession` 포인터 고정 (Lock 경합 제거)
- **현상 및 문제점**:
  - [`pkg/wire/tds/server.go:handleRPCExecuteSQL`](file:///Users/maro/Projects/SQLion/pkg/wire/tds/server.go#L1048)에서 매 쿼리마다 `s.coordinator.GetSession(sess.SessionID)` 호출.
  - 코디네이터의 `sessMu.RLock()`(`atomic.AddInt32`)으로 인해 100+ 동시 워커 환경에서 CPU 코어 간 L3 캐시 라인 바운싱 유발.
- **구현 내용**:
  - `ConnectionSession`은 TCP 소켓과 1:1 라이프사이클을 공유하므로, `handleLogin7` 시점에 생성된 `cs *engine.ClientSession` 포인터를 `ConnectionSession.clientSession`에 직접 캐싱/고정.
  - 핫 실행 경로에서 `s.coordinator.GetSession()` 락 조회 전면 제거.
- **검증 기준**:
  - 동시 연결 100 워커 환경에서 세션 뮤텍스 경합 프로파일(`go test -bench -blockprofile`) 0ns 확인.

---

### Phase 2: 쿼리 디코딩 & VDBE 파라미터 바인딩 최적화

#### 2.1. RPC 파라미터 맵 힙 할당 제거 (슬라이스/풀링 구조체 직결)
- **현상 및 문제점**:
  - RPC 파라미터를 VDBE에 전달하기 위해 `paramMap := make(map[string]any, len(rpc.Params)*3)`를 매 쿼리마다 새로 할당.
  - 4,200 TPS 환경에서 초당 10,000~20,000개의 맵이 생성되고 GC에 의해 스윕됨.
- **구현 내용**:
  - VDBE 레지스터가 순수 정수 인덱스(`r[0]`, `r[1]` 등)로 매핑되므로, 파라미터 맵 대신 사전 할당된 파라미터 슬라이스(`[]proc.Param`) 또는 풀링된 맵 컨텍스트(`sync.Pool`)를 직결 전달.
- **검증 기준**:
  - T-SQL Stored Procedure 실행 핫 패스에서 GC Sweep 오버헤드 50% 이상 감소.

#### 2.2. 이름 기반 프로시저 호출의 Direct PlanCache 바이트코드 디스패치
- **현상 및 문제점**:
  - `sp_executesql`이 아닌 RPC 호출(`rpc.ProcName != ""`) 시 `fmt.Sprintf("EXEC %s ...", ...)`로 동적 SQL 문자열을 생성하여 쿼리 라우터로 전달.
- **구현 내용**:
  - `rpc.ProcName`으로 `PlanCache`를 직접 색인하고, 디코딩된 RPC 인자를 VM 레지스터에 즉시 바인딩하여 바이트코드를 직접 실행하는 Fast-Path 구축.
- **검증 기준**:
  - 문자열 결합 및 재파싱 경로 완전 우회 확인.

---

### Phase 3: 스토리지 엔진 & 커서 캐싱 최적화

#### 3.1. WiredTiger 세션 레벨 범용 LRU 커서 캐시 (LRU Bounded Cursor Cache)
- **현상 및 문제점**:
  - 트랜잭션 수명주기 동안 반복 쿼리에서 B+Tree 테이블 커서를 매번 열고 닫거나 리셋(`C.go_wt_session_reset`)하는 과정에서 내부 btree/schema 검색 CGO 오버헤드가 발생함.
- **구현 내용**:
  - 특정 벤치마크 테이블 이름을 하드코딩하는 치팅(Benchmark Targeting) 방식을 전면 배제하고, SQLite/WiredTiger 자체 `cursor_cache` 표준 패턴에 따라 **세션별 최근 접근한 상위 N개(예: 16개) B+Tree 커서를 LRU 기반으로 재사용하는 범용 유한 커서 풀(Bounded Pool)** 구축.
  - 트랜잭션 커밋/롤백 시 커서를 파괴하지 않고 `reset` 상태로 풀에 반환하여 핸들 재할당 CGO 오버헤드를 제로화.
- **검증 기준**:
  - WiredTiger CGO 커서 오픈 호출 빈도 80% 이상 감소 및 임의 테이블 워크로드 대상 일반성 검증.

#### 3.2. 순수 읽기 쿼리의 트랜잭션 및 WAL 완전 우회 (컴파일러 자동 감지 & ReadOnly 힌트)
- **현상 및 문제점**:
  - 단순 포인트 조회나 집계 읽기 작업(`SELECT`)도 불필요하게 쓰기 가능한 트랜잭션 컨텍스트를 경유하여 세션 할당 및 정리 비용을 소모함.
- **구현 내용**:
  - **1) VDBE 컴파일러의 정적 분석 자동 감지 (Zero-Hint Primary)**:
    - 쿼리 내에 쓰기 opcode(`OpInsert`, `OpUpdate`, `OpDelete`, `OpTxBegin`) 및 부수 효과를 가진 함수가 없음을 파서/컴파일러 단계에서 감지하여 `prog.IsReadOnly = true`를 자동으로 태깅. 사용자가 힌트를 쓰지 않아도 트랜잭션 래퍼 없이 OCC 샤드 직접 읽기(Point-Lookup Shortcut) 또는 WiredTiger Snapshot Cursor 읽기만 수행하도록 분기.
  - **2) 사용자 명시적 ReadOnly 힌트 수용 (Explicit Hints)**:
    - **T-SQL 표준 힌트**: `SELECT ... OPTION (READ ONLY)`, `SELECT ... WITH (READONLY)`
    - **옵티마이저 주석 힌트**: `SELECT /*+ READ_ONLY */ ...`
    - **트랜잭션/세션 레벨 선언**: `BEGIN TRANSACTION READ ONLY;`, `SET TRANSACTION READ ONLY;`
- **검증 기준**:
  - 읽기 쿼리 평균 지연 시간 0.2ms 이하 유지 및 불필요한 트랜잭션 롤백 카운터 0 유지.

---

### Phase 4: WAL & 디스크 파일 시스템 I/O 최적화

#### 4.1. [Spec-Out] WAL 세그먼트 파일 공간 사전 할당 (`fallocate` / `fcntl F_PREALLOCATE`)
- **결정**: **영구 Spec-Out (제외)**
- **제외 사유**:
  - WiredTiger 스토리지 엔진은 C 레벨 기본값으로 `log=(file_max=100MB,prealloc=true)`을 통해 **100MB 세그먼트 로그 파일을 이미 백그라운드에서 자동 사전 할당 및 순환 재사용**하고 있음.
  - 외부 Go 코드에서 별도로 OS 종속적인 `fallocate` / `F_PREALLOCATE`를 호출하는 것은 100% 중복(Redundant)이며, 플랫폼별 파일 유효 길이(EOF) 추적 복잡성만 초래하므로 **Radical KISS 및 Deletion-First 원칙(Golden Rule 15)**에 따라 영구 제외.

#### 4.2. 적응형 마이크로초 그룹 커밋 배칭 (Adaptive Group Commit)
- **현상 및 문제점**:
  - `-fsync always` 모드에서 첫 번째 커밋 스레드가 도착 즉시 `log_flush(sync=on)` 및 물리 플러시를 수행하여 동시 도착 트랜잭션의 배칭 기회를 상실함.
- **구현 내용**:
  - 첫 번째 리더(Leader) 스레드가 10~30µs 동안 적응형 대기(Adaptive Yield)하며 뒤이어 도착하는 팔로워(Follower) 트랜잭션들의 WAL 레코드를 단일 버퍼에 그룹화한 뒤, **단 1회의 물리 하드웨어 배리어(`F_FULLFSYNC` / `fsync`)로 일괄 플러시**.
  - **`fsync always` 이론적 무결성 검증**:
    - 리더와 모든 팔로워 트랜잭션은 물리 `fsync`가 디스크 컨트롤러에서 성공적으로 완료될 때까지 **클라이언트에게 커밋 성공(`SUCCESS`) 응답을 결코 반환하지 않고 대기(Block)**함.
    - 클라이언트가 성공 응답을 수신하는 시점에는 모든 변경 데이터가 이미 비휘발성 디스크에 영구 기록되어 있으므로, **ACID의 지속성(Durability) 및 `fsync always`의 무손실 보장 원칙에 1%도 위배되지 않음** (MySQL InnoDB, PostgreSQL, SQLite WAL 표준 그룹 커밋과 동일 메커니즘).
- **검증 기준**:
  - `-fsync always` 환경에서 물리 디스크 배리어 빈도 1/5 수준 감소 및 TPS 2~3배 향상 (단 1건의 커밋 유실도 발생하지 않음).

---

## 3. 검증 매트릭스 및 성능 실측 결과 (Verified Results)

| 단계 (Phase) | 측정 지표 (Target Metric) | 최적화 전 (Baseline) | Phase 1~2 실측 달성치 | 최종 목표치 (Target) |
| :--- | :--- | :--- | :--- | :--- |
| **Phase 1 (TDS & 소켓)** | TDS 패킷당 OS `read()` 시스템 콜 수 | 2회 이상 | **1회 (Coalesced, 검증 완료 `[x]`)** | **1회** |
| **Phase 1 (TDS & 소켓)** | 32KB 패킷 응답 힙 메모리 할당 | 수십 MB/s GC 할당 | **0 B/op (`sync.Pool`, 검증 완료 `[x]`)** | **0 B/op** |
| **Phase 1 (TDS & 세션)** | 세션 RLock 획득 경합 오버헤드 | 다중 코어 캐시라인 바운싱 | **0 ns (Direct Pinning, 검증 완료 `[x]`)** | **0 ns** |
| **Phase 2 (디코딩 & VDBE)** | 초당 파라미터 해시맵 생성 수 | 10,000~20,000개/s | **0개/s (Direct Slice/Pool, 검증 완료 `[x]`)** | **0개/s** |
| **Phase 3 (스토리지 엔진)** | WiredTiger 테이블 커서 룩업 비용 | 매 트랜잭션 재탐색 | Post-0.90.0 착수 예정 | **0 ns (Pre-pinned Cursor)** |
| **Phase 4 (WAL & I/O)** | 파일 시스템 Inode 메타데이터 쓰기 | 매 커밋 시 크기 증가 저널링 | Post-0.90.0 착수 예정 (WAL Prealloc은 Spec-Out) | **0회 (Adaptive Batching)** |
| **종합 성능 (Client-Server)** | **TPC-C tpmC (16W / 20W)** | **113,795 tpmC** | **123,363 tpmC** (Peak ~180k, **+8.4% 상승**) | **> 150,000 tpmC** |
| **종합 성능 (Client-Server)** | **TPC-C tpmC (16W / 32W)** | - | **135,287 tpmC** (Peak ~173k) | **> 150,000 tpmC** |
| **종합 성능 (Client-Server)** | **전체 TPS (16W / 20W)** | **4,228 TPS** | **4,572 TPS** (Peak ~6,685, **+8.1% 상승**) | **> 5,500 TPS** |
| **종합 성능 (Client-Server)** | **전체 TPS (16W / 32W)** | - | **5,033 TPS** (Peak ~6,436, **5천 TPS 돌파**) | **> 5,500 TPS** |
| **충돌 재시도 횟수** | **Conflict Retries (20W)** | **2,752회** | **1,316회** (**-52.2% 급감**) | 최소화 |
| **지연 시간 (Payment P95)** | **Payment Tail Latency** | **3.82 ms** | **2.83 ms** (**26% 단축**) | **< 3.0 ms** |

---

## 4. 리스크 관리 및 롤백 전략

1. **소켓 버퍼 수명주기 누수 방지**:
   - `bufio.Reader` 및 32KB 버퍼를 `sync.Pool`에 반환할 때 연결 종료(`defer conn.Close()`) 시점에 안전하게 반환되도록 라이프사이클을 엄격히 바인딩.
2. **사전 할당(Pre-allocation) 플랫폼 호환성**:
   - Linux(`posix_fallocate`), macOS(`fcntl(fd, F_PREALLOCATE)`), Windows(`SetFileValidData` / `SetEndOfFile`) 플랫폼별 빌드 태그를 분리하여 플랫폼 비호환 에러 시 안전하게 일반 append 방식으로 폴백.
3. **단계별 독립 배포**:
   - Phase 1부터 Phase 4까지 각 단계는 상호 의존성이 없으므로, 개별 단계 완성 즉시 `3-Stage Pipeline (Build ➔ Test -race ➔ Commit)` 검증을 거쳐 독립 커밋.
