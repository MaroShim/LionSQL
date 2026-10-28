# LionSQL 서버 파이프라인(TDS to File System) 종단 최적화 마스터 플랜

> - **최초 작성일**: 2026-10-29
> - **문서 상태**: Approved (실행 대기)
> - **연관 과제 ID**: `PERF-SERVER-PIPELINE`
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
  ├─ 3.1. WiredTiger 세션 내 핫 테이블 커서 고정 (Pinned Table Cursors)
  └─ 3.2. 순수 읽기 쿼리의 트랜잭션 및 WAL 완전 우회
       │
[Phase 4: WAL & 디스크 파일 시스템 I/O]
  ├─ 4.1. WAL 세그먼트 파일 공간 사전 할당 (fallocate / F_PREALLOCATE)
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

#### 3.1. WiredTiger 세션 내 핫 테이블 커서 고정 (Pinned Table Cursors)
- **현상 및 문제점**:
  - 트랜잭션 수명주기 동안 테이블 커서를 매번 열고 닫거나 리셋(`C.go_wt_session_reset`)하는 과정에서 내부 btree/schema 검색 오버헤드 발생.
- **구현 내용**:
  - TPC-C와 같은 핫 트랜잭션에 대해 9개 핵심 테이블 커서를 `WTTx` 세션 내에 Pre-open 상태로 핀(Pin) 고정.
- **검증 기준**:
  - WiredTiger CGO 경계 호출 빈도 및 CPU 오버헤드 감소 확인.

#### 3.2. 순수 읽기 쿼리의 트랜잭션 및 WAL 완전 우회
- **현상 및 문제점**:
  - `Order-Status`, `Stock-Level` 등 단순 읽기 작업도 트랜잭션 컨텍스트를 경유하여 세션 할당 및 정리 비용 소모.
- **구현 내용**:
  - VDBE 바이트코드 컴파일 타임에 쓰기 op-code가 없음을 감지하거나 ReadOnly 힌트가 있을 경우, 트랜잭션 래퍼 없이 OCC 샤드 직접 읽기(Point-Lookup Shortcut) 또는 WiredTiger Snapshot Cursor 읽기만 수행하도록 분기.
- **검증 기준**:
  - 읽기 쿼리 평균 지연 시간 0.2ms 이하 유지 및 불필요한 트랜잭션 롤백 카운터 0 유지.

---

### Phase 4: WAL & 디스크 파일 시스템 I/O 최적화

#### 4.1. WAL 세그먼트 파일 공간 사전 할당 (`fallocate` / `fcntl F_PREALLOCATE`)
- **현상 및 문제점**:
  - WAL 파일에 로그를 추가 기록(append)할 때마다 파일 크기가 증가하여, OS 파일 시스템(APFS, ext4, XFS)이 동기식 Inode 메타데이터(크기, 익스텐트) 갱신 저널링을 수행함.
- **구현 내용**:
  - WAL 파일 세그먼트 생성 시 64MB 단위로 디스크 블록을 사전 할당(`posix_fallocate` 또는 macOS `fcntl(fd, F_PREALLOCATE)`).
  - 커밋 경로에서 파일 크기 변경에 따른 파일 시스템 저널링 I/O를 배제하고 순수 데이터 블록 쓰기만 발생하도록 보장.
- **검증 기준**:
  - 디스크 I/O 대기 시간(iowait) 감소 및 안정적인 커밋 쓰기 지연 시간 확보.

#### 4.2. 적응형 마이크로초 그룹 커밋 배칭 (Adaptive Group Commit)
- **현상 및 문제점**:
  - `-fsync always` 모드에서 첫 번째 커밋 스레드가 도착 즉시 `log_flush(sync=on)` 및 물리 플러시를 수행하여 배칭 기회 상실.
- **구현 내용**:
  - 첫 번째 리더 스레드가 10~30µs 동안 적응형 대기(Adaptive Yield)하며 뒤이어 도착하는 트랜잭션을 그룹화하여 단 1회의 하드웨어 배리어(`F_FULLFSYNC`)로 일괄 플러시.
- **검증 기준**:
  - `-fsync always` 환경에서 물리 디스크 배리어 빈도 1/5 수준 감소 및 TPS 2~3배 향상.

---

## 3. 검증 매트릭스 및 성능 목표치

| 단계 (Phase) | 측정 지표 (Target Metric) | 현재 수치 (Baseline) | 개선 목표치 (Target) |
| :--- | :--- | :--- | :--- |
| **Phase 1 (TDS & 소켓)** | TDS 패킷당 OS `read()` 시스템 콜 수 | 2회 이상 | **1회 (Coalesced)** |
| **Phase 1 (TDS & 소켓)** | 32KB 패킷 응답 힙 메모리 할당 | 수십 MB/s GC 할당 | **0 B/op (`sync.Pool`)** |
| **Phase 1 (TDS & 세션)** | 세션 RLock 획득 경합 오버헤드 | 다중 코어 캐시라인 바운싱 | **0 ns (Direct Pinning)** |
| **Phase 2 (디코딩 & VDBE)** | 초당 파라미터 해시맵 생성 수 | 10,000~20,000개/s | **0개/s (Direct Slice/Pool)** |
| **Phase 3 (스토리지 엔진)** | WiredTiger 테이블 커서 룩업 비용 | 매 트랜잭션 재탐색 | **0 ns (Pre-pinned Cursor)** |
| **Phase 4 (WAL & I/O)** | 파일 시스템 Inode 메타데이터 쓰기 | 매 커밋 시 크기 증가 저널링 | **0회 (Pre-allocated 64MB)** |
| **종합 성능 (Client-Server)** | **TPC-C tpmC (16W / 20W)** | **113,795 tpmC** | **> 150,000 tpmC** |
| **종합 성능 (Client-Server)** | **전체 TPS** | **4,228 TPS** | **> 5,500 TPS** |
| **종합 성능 (Client-Server)** | **New-Order P95 레이턴시** | **8.54 ms** | **< 4.0 ms** |

---

## 4. 리스크 관리 및 롤백 전략

1. **소켓 버퍼 수명주기 누수 방지**:
   - `bufio.Reader` 및 32KB 버퍼를 `sync.Pool`에 반환할 때 연결 종료(`defer conn.Close()`) 시점에 안전하게 반환되도록 라이프사이클을 엄격히 바인딩.
2. **사전 할당(Pre-allocation) 플랫폼 호환성**:
   - Linux(`posix_fallocate`), macOS(`fcntl(fd, F_PREALLOCATE)`), Windows(`SetFileValidData` / `SetEndOfFile`) 플랫폼별 빌드 태그를 분리하여 플랫폼 비호환 에러 시 안전하게 일반 append 방식으로 폴백.
3. **단계별 독립 배포**:
   - Phase 1부터 Phase 4까지 각 단계는 상호 의존성이 없으므로, 개별 단계 완성 즉시 `3-Stage Pipeline (Build ➔ Test -race ➔ Commit)` 검증을 거쳐 독립 커밋.
