# Lioness: Hash Partitioning Sharding Router Architecture Plan (`LIONESS_SHARDING_ROUTER_PLAN.ko.md`)

- **작성일**: 2026-11-19
- **상태**: DRAFT / PROPOSED (Post-1.0 Distributed Roadmap)
- **우선순위**: Prio 3 (Post-1.0 Architecture)
- **목표 태스크 ID**: `DIST-LIONESS-SHARDING-ROUTER-1`
- **관련 규칙**: `[RULE:PlanPersistence]`, `[RULE:RadicalKISS]`, `[RULE:StyleIntegrity]`, `[RULE:ContinuousBacklog]`

---

## 1. 아키텍처 배경 및 설계 철학

### 1.1 배경: 단일 노드 스케일업(LionSQL)에서 분산 스케일아웃(Lioness)으로
LionSQL은 단일 노드 임베디드 OLTP 엔진으로서 20,000+ TPS와 서브마이크로초 지연시간을 달성했습니다 (`GEMINI.md Invariant 5: Scale-Up First`).
그러나 단일 머신의 물리 메모리/디스크 한계를 초과하는 **초거대 DB 환경(Multi-Terabyte / PB급 및 수십만 동시 연결)**을 수용하기 위해서는 수평적 스케일아웃 레이어가 필요합니다.

### 1.2 핵심 철학: Vitess / Citus 모델의 순수 Hash Partitioning Router
- **복잡한 분산 MPP/2PC Spec-Out**: 무거운 분산 트랜잭션 코디네이터(2PC), 크로스 노드 분산 락, 분산 조인 셔플링(Shuffle/Exchange)을 배제합니다 (`[RULE:RadicalKISS]`).
- **투명한 해시 샤딩(Transparent Hash Sharding)**:
  - Vitess(MySQL)의 `VTGate`나 Citus(PostgreSQL)의 코디네이터 노드와 같이, 클라이언트로부터 들어온 쿼리를 **파티셔닝 키(Partition Key)의 해시값에 따라 대상 LionSQL 샤드 노드로 직접 라우팅(Targeted Route)**하거나 분산 수집(Scatter-Gather)합니다.
- **로컬 조인 우선 (Colocated Join)**:
  - 동일한 파티션 키로 해시 샤딩된 테이블 간의 조인(예: `Users ⋈ Orders ON Users.tenant_id = Orders.tenant_id`)은 샤드 노드 내부에서 100% 로컬 조인으로 완료됩니다.

---

## 2. Lioness의 구조적 차별점 (Zero-Double-Parsing & IR Pushdown)

기존 상용 분산 솔루션(Vitess, Citus)과 Lioness의 결정적 차이는 **백엔드 스토리지 엔진(LionSQL)과의 코드 및 AST 공유**에 있습니다.

```
[ 기존 분산 라우터 (Vitess / Citus) ]
 클라이언트 SQL ──> 라우터 (1차 파싱/샤딩 판별) ──> SQL 텍스트 재생성(Stringify) 
                  ──> TCP Wire (네트워크) ──> 백엔드 노드 (2차 재파싱 & 컴파일)
                  * 심각한 이중 파싱(Double Parsing) 및 문자열 직렬화 병목 발생!

[ Lioness + LionSQL 통합 아키텍처 ]
 클라이언트 SQL ──> Lioness Router (LionSQL pkg/proc Pratt 파서로 파싱 & 샤드 타겟팅)
                  ──> 컴파일된 바이너리 AST 패킷 또는 VDBE Bytecode 직접 전송
                  ──> LionSQL Shard Node (재파싱 없이 VDBE VM에서 즉각 서브마이크로초 실행!)
```

### 2.1 주요 기술적 우위
1. **동일 코드베이스 라이브러리 활용 (`pkg/proc`, `pkg/sqlutil`)**:
   - Lioness는 LionSQL의 Pratt 파서, AST 정의, 토크나이저를 별도의 의존성 없이 내부 패키지로 직접 임포트하여 사용합니다.
2. **이중 파싱 제거 (Zero Double Parsing)**:
   - 라우터에서 파싱한 AST를 SQL 텍스트 문자열로 다시 풀어서 보내지 않고, 내부 바이너리 프로토콜(IR / Bytecode Payload)로 샤드 노드에 직접 전달할 수 있어 CPU 사이클과 메모리 할당을 극소화합니다 (`[RULE:ZeroAllocation]`).
3. **서브마이크로초 라우팅 레이턴시**:
   - 해시 계산(`Hash(key) % N`)과 AST 노드 검사만으로 100ns 미만의 순수 라우팅 판별이 가능합니다.

---

## 3. 공통 고속 인터페이스: FlatBuffers 바이너리 프로토콜 (LionSQL & Lioness 공용)

게임 서버(C++, C#, Go, Rust) 연동 및 초고성능 백엔드 IPC를 위해, **LionSQL(단일 노드)과 Lioness(샤딩 라우터) 모두가 공유하는 네이티브 FlatBuffers 바이너리 RPC 인터페이스**를 구축합니다.

### 3.1 LionSQL V2 튜플 코덱과 FlatBuffers의 1:1 아키텍처적 일치성
LionSQL의 레코드 저장 코덱인 **V2 Binary Tuple Codec** (`GEMINI.md Invariant 13`)은 이미 FlatBuffers의 설계 원리와 완전히 일치합니다:
- **LionSQL V2 튜플**: `[Header 4B | NullBitmap | Fixed-width Payloads | Var-length Payloads]`
- **FlatBuffers Table**: `[VTable Offset | Object SOFF | Inline Fixed Fields | Var-length Vectors]`

두 포맷 모두 **"구조체 역직렬화(Unmarshalling) 비용 0, 메모리 추가 할당(Allocation) 0, 오프셋 기반 O(1) 직접 포인터 접근"**이라는 동일한 불변식을 공유합니다.

### 3.2 2-Way 활용: 단일 노드 LionSQL과 분산 라우터 Lioness의 공통 활용
1. **LionSQL 단일 노드 활용 (고성능 게임 서버 직접 연동)**:
   - 전용 고속 바이너리 포트(TCP `:9000`)를 열어 C++/C# 게임 서버와 직접 통신.
   - SQL 문자열 변환 및 AST 렉싱/파싱 과정을 완전히 생략하고, 게임 서버가 전송한 FlatBuffers 바이트 버퍼를 **VDBE 레지스터 및 V2 튜플로 즉시 매핑(Zero-Copy)**하여 WiredTiger B+Tree / In-Memory OCC에 나노초 단위로 저장.
2. **Lioness 샤딩 라우터 활용 (Zero-Copy 샤드 라우팅)**:
   - 게임 서버로부터 들어온 FlatBuffers 버퍼를 역직렬화하지 않고, VTable 오프셋을 통해 `shard_key`(4~8바이트)만 즉시 읽어 `Hash(shard_key) % N` 계산.
   - 패킷 전체의 메모리 복사나 파싱 없이 바이트 스트림 그대로 대상 LionSQL 샤드 노드로 고속 포워딩(TCP Slice Pass-through).
3. **Lioness ➔ LionSQL 내부 통신 (Zero-Double-Parsing)**:
   - 라우터와 백엔드 노드 간 IPC 프로토콜 자체를 FlatBuffers 기반 바이너리 IR/Opcode로 통일하여 통신 오버헤드를 0으로 수렴.

```
[ 게임 서버 (C++, C#, Go) ]
       │ FlatBuffers Binary Packet (Zero-Copy)
       ├────────────────────────────────────────┐
       │ (분산 샤딩 환경)                       │ (단일 노드 직접 연결)
       ▼                                        ▼
┌───────────────────────────────┐        ┌───────────────────────────────┐
│        Lioness Router         │        │        LionSQL Node           │
│ - VTable에서 shard_key 직접 읽기│        │ - TDS :1433 / PGWire :5433    │
│ - 패킷 언패킹 없이 바이트 포워딩│        │ - FlatBuffers TCP :9000       │
└──────────────┬────────────────┘        │ - V2 Tuple 즉시 매핑 / 커밋    │
               │ FlatBuffers Wire        └───────────────────────────────┘
               ▼
┌───────────────────────────────┐
│       LionSQL Shard Node      │
│ - V2 Tuple 레이아웃 1:1 직통 저장│
└───────────────────────────────┘
```

### 3.3 오퍼레이션 식별 및 디스패치 아키텍처 (SQL 텍스트 없는 무비용 디스패치)
게임 서버 연동 시 SQL 문자열 텍스트가 존재하지 않으므로, 엔진이 어떤 작업을 수행해야 하는지 0나노초 만에 판별할 수 있는 **2가지 고속 디스패치 규격**을 표준화합니다:

#### 1. FlatBuffers Union 다형성 기반 디스패치 (`LionPacket.fbs`)
FlatBuffers 스키마의 `union` 기능을 활용하면, 최상위 테이블에 **1바이트 히든 필드(`op_type`)**가 생성되어 메모리 역직렬화 없이 즉시 `switch` 분기합니다:

```fbs
enum Opcode : byte {
    Get = 1,       // 단건 PK 조회 (KV Fast-Path: Volcano 플래너 완전 생략)
    Put = 2,       // 단건/배치 업서트 (V2 튜플 B+Tree 직통 저장)
    Delete = 3,    // 단건 삭제
    ExecProc = 4,  // 저장 프로시저 호출 (사전 컴파일된 VDBE 바이트코드 즉시 실행)
    RawQuery = 5   // 복잡한 비정형 쿼리 (필요시)
}

table GetRequest      { table_id: uint16; key: [byte]; }
table PutRequest      { table_id: uint16; tuple_data: [byte]; }
table ExecProcRequest { proc_id: uint16; params_data: [byte]; }

union OpPayload { GetRequest, PutRequest, ExecProcRequest }

table PacketHeader {
    request_id: uint64;   // 비동기 응답 매칭용 시퀀스 번호
    shard_key:  uint64;   // 샤딩 키 (Lioness 라우팅용)
    op:         OpPayload;// 1바이트 오퍼레이션 유니온
}
```

#### 2. 프레임 헤더 기반 극초저지연 디스패치 (Gaming Socket Framing)
소켓 스트림 레벨에서 FlatBuffers 페이로드 앞에 4바이트 고정 헤더를 배치하는 방식입니다:
```
┌──────────────┬──────────────┬──────────────┬────────────────────────────────┐
│ Length (2B)  │  Opcode (1B) │  Flags (1B)  │ FlatBuffers Payload (나머지 버퍼) │
└──────────────┴──────────────┴──────────────┴────────────────────────────────┘
```
- **`Opcode 0x01 (PK_GET)`**: 플래너/파서/옵티마이저를 100% 바이패스하고 스토리지 커널의 `engine.Get(table_id, pk)`로 0.1µs 만에 직행 (Redis급 초저지연 달성).
- **`Opcode 0x02 (PUT / INSERT)`**: 들어온 바이트 버퍼를 스토리지 엔진의 `tx.Put(table_id, key, tuple)`로 직통 커밋.
- **`Opcode 0x03 (CALL_PROC)`**: 복잡한 게임 비즈니스 로직(아이템 강화, 거래, 인벤토리 갱신 등)은 T-SQL 저장 프로시저로 작성해 두고, `proc_id`만 읽어 사전 컴파일된 VDBE VM에 파라미터만 바인딩하여 1회의 네트워크 RTT로 원자적 ACID 실행.
- **테이블/프로시저의 숫자 ID화 (`table_id`, `proc_id`)**: 문자열 식별자를 완전히 배제하여 네트워크 대역폭 90% 이상 절감 및 카탈로그 맵 조회 $O(1)$ 직행.

### 3.4 디스크 스토리지/WAL 포맷 전환 전략: 파일명 버전 분리 (Zero-Corruption File Isolation)
LionSQL 내부의 In-Memory OCC 엔진은 현재 WAL 및 스냅샷 직렬화에 `MessagePack (v5)`을 사용하고 있으며, 기존 프레임 헤더(`[4B Length | 4B CRC32]`)에는 별도의 코덱 식별자(Codec ID)가 존재하지 않습니다.
프레임 헤더 변경에 따른 복잡도와 위험을 방지하고, 기존 운영 데이터의 100% 안전한 하위 호환 복구(`[RULE:AtomicSaves]`, `[RULE:SafeDefaults]`)를 위해 **"파일명 버전 분리 (File Version Isolation)"** 방식을 표준 채택합니다:

1. **파일 명명 규칙 분리**:
   - **레거시 포맷 (Msgpack)**: `wal.aof` / `snapshot.dat` (기존 포맷 그대로 유지)
   - **차세대 포맷 (FlatBuffers)**: `wal.fb` / `snapshot.fb` (신규 Zero-Copy 엔진)
2. **무중단 기동 및 자동 승격 (Self-Compaction Migration)**:
   - **엔진 기동 시 복구 (Recovery)**:
     - `wal.aof` (레거시)가 존재하면 기존 `UnmarshalMsgPack` 파이프라인으로 안전하게 리플레이하여 메모리 OCC 테이블을 100% 복구.
     - `wal.fb`가 존재하면 FlatBuffers Zero-Copy 리더로 고속 리플레이.
   - **체크포인트 시점 자동 전환 (Clean Cut-over)**:
     - 엔진이 기동 후 첫 번째 체크포인트(Snapshot)를 수행할 때, 최종 메모리 상태를 신규 **`snapshot.fb`**에 덤프.
     - 기존의 구버전 `wal.aof` 및 `snapshot.dat`를 디스크에서 안전하게 제거(Prune)하고, 이후부터 발생하는 모든 신규 트랜잭션은 **`wal.fb`**에만 기록.
3. **효과**:
   - 유저의 수동 마이그레이션 작업이나 스크립트 실행이 전혀 필요 없음 (Zero Operator Burden).
   - 기존 프레임 비트를 비틀거나 위험한 헤더 수정을 하지 않아 코드 복잡도가 극도로 단순해짐 (`[RULE:RadicalKISS]`).

---

## 4. 핵심 아키텍처 및 쿼리 라우팅 모델

```
                   [ Application / Client (SSMS, psql, DBeaver) ]
                                      │
                                      ▼ TDS :1433 / PGWire :5433
                           ┌─────────────────────┐
                           │   Lioness Router    │
                           │  - AST Parser & CBO │
                           │  - Hash Partitioner │
                           │  - Scatter-Gatherer │
                           └──────────┬──────────┘
                                      │
         ┌────────────────────────────┼────────────────────────────┐
         │ (Shard 0)                  │ (Shard 1)                  │ (Shard 2)
         ▼                            ▼                            ▼
┌───────────────────┐        ┌───────────────────┐        ┌───────────────────┐
│   LionSQL Node 0  │        │   LionSQL Node 1  │        │   LionSQL Node 2  │
│ (WiredTiger/OCC)  │        │ (WiredTiger/OCC)  │        │ (WiredTiger/OCC)  │
└───────────────────┘        └───────────────────┘        └───────────────────┘
```

### 3.1 라우팅 분류 체계

| 분류 | 쿼리 패턴 예시 | Lioness 동작 방식 |
|---|---|---|
| **Targeted Route** (단일 샤드 타겟팅) | `SELECT * FROM orders WHERE tenant_id = 42;` | `Hash(tenant_id) % N` 계산 후 해당 샤드 노드로 쿼리(또는 Bytecode) 직행 |
| **Colocated Join** (동일 키 로컬 조인) | `SELECT * FROM users u JOIN orders o ON u.t_id = o.t_id WHERE u.t_id = 42;` | 단일 샤드 내부에서 LionSQL의 로컬 Volcano 엔진 / Hash Join으로 100% 실행 |
| **Scatter-Gather** (샤딩 키 미지정) | `SELECT * FROM orders WHERE status = 'PAID';` | 모든 샤드에 쿼리를 병렬 전파(`goroutine fan-out`) 후 결과를 `UNION ALL`로 수집 |
| **Global Aggregation** (단순 집계 수집) | `SELECT COUNT(*), SUM(amount) FROM orders;` | 각 샤드에서 부분 집계(Partial Agg)를 구한 후 Lioness에서 최종 합산(Final Agg) |
| **Cross-Shard Join** (서로 다른 키 조인) | `SELECT * FROM orders o JOIN products p ON o.p_id = p.id;` | 원칙적 **Spec-Out** 또는 2-Phase Map-Gather (Post-1.0 후반부) |

---

---

## 4. 트랜잭션 관리 전략 (Transaction Management Strategy)

해시 파티셔닝 샤딩 아키텍처에서 트랜잭션의 성능과 신뢰성을 극대화하기 위해 **"99%의 단일 샤드 로컬 ACID"**와 **"1%의 분산 트랜잭션 격리/단계적 접근"** 원칙을 수립합니다.

### 4.1 골든 룰: 단일 샤드 로컬 ACID (Single-Shard Local Transaction, 99%)
- **원칙**: 모든 쓰기 트랜잭션(`BEGIN ... COMMIT`)은 반드시 동일한 샤딩 키(예: `tenant_id`, `user_id`) 범위 내에서 실행됩니다.
- **동작**:
  1. 클라이언트가 `BEGIN TRANSACTION`을 발행합니다.
  2. 첫 번째 DML의 샤딩 키(`WHERE tenant_id = 42`)를 통해 대상 샤드(예: Shard 2)를 식별합니다.
  3. Lioness는 해당 세션의 트랜잭션 핸들을 Shard 2 노드의 로컬 트랜잭션 매니저(`pkg/tx/manager.go`)에 1:1로 직접 바인딩합니다.
  4. 이후 모든 후속 DML, Savepoint, 커밋/롤백은 Shard 2 내부의 OCC / WiredTiger 엔진에서 100% 로컬 속도로 수행됩니다.
- **효과**: 분산 락 및 2PC 코디네이터 오버헤드가 전무하며, 단일 노드 LionSQL과 완전히 동일한 초당 수만 TPS의 초저지연 ACID 보장.

### 4.2 크로스 샤드 트랜잭션(Cross-Shard Write) 처리 3단계 로드맵
여러 샤드에 걸치는 변경 작업(예: Shard A 계좌 출금, Shard B 계좌 입금)은 시스템 복잡도와 위험을 철저히 낮추기 위해 단계적으로 접근합니다:

1. **Phase 1 (원천 차단 - Spec-Out & Fail-Closed, 기본 모드)**:
   - **"다중 샤드 변경 트랜잭션(Multi-Shard Write Tx)은 기본적으로 Spec-Out으로 차단한다."**
   - 활성 트랜잭션 블록 내에서 2개 이상의 서로 다른 샤드 키를 수정하려고 시도하면, Lioness가 즉시 에러(`ERROR: Cross-shard multi-statement transaction is not supported`)를 반환하고 차단합니다 (`[RULE:ExplicitError]`, `[RULE:RadicalKISS]`).
   - 99%의 SaaS/엔터프라이즈 멀티테넌트 환경은 테넌트 키 기준으로 완벽히 단일 샤드 트랜잭션으로 수렴되므로, 복잡한 2PC 코디네이터 없이도 엔터프라이즈 안정성을 유지합니다.
2. **Phase 2 (분산 읽기 스냅샷 - Consistent Read-Only Snapshot)**:
   - 쓰기는 단일 샤드로 한정하되, 여러 샤드에 걸친 대규모 읽기/리포팅 쿼리는 일관된 글로벌 논리 타임스탬프(Global Snapshot LSN/TS)를 각 샤드에 전달하여 비차단 스냅샷 읽기를 보장합니다.
3. **Phase 3 (선택적 2PC 지원 - Optional Two-Phase Commit)**:
   - 금융/결제 등 엄격한 분산 원자성(All-or-Nothing)이 필수적인 유즈케이스에 한해 선택적 2PC 플래그(`--enable-2pc`)를 제공합니다:
     - LionSQL 트랜잭션 매니저에 `PREPARE TRANSACTION` / `COMMIT PREPARED` 프리미티브 구현.
     - Lioness 코디네이터가 2-Phase Commit 상태 머신(In-Doubt 트랜잭션 자동 롤백/복구)을 관리.
   - 단, 성능 유지를 위해 비즈니스 레이어의 **사가 패턴(Saga Pattern / Outbox Pattern)** 도입을 우선 권장합니다.

---

---

## 6. 게임 서버 특화 Redis-Style 내장 자료구조 & TTL 확장 (LionSQL & Lioness 공용)

게임 서버 아키텍처에서 외산 캐시(Redis)를 완전히 제거하고, 캐시-DB 동기화 버그 없이 **순수 ACID 내구성과 수 마이크로초(µs) 미만 응답성을 동시에 달성**하기 위한 내장 프리미티브를 제공합니다.

### 6.1 네이티브 Sorted Set (B+Tree 기반 Zero-Cost 실시간 리더보드)
- **개념**: Redis의 `ZSET`을 스토리지 엔진 내부의 B+Tree 복합 인덱스(`[Score IEEE754 Hex][Member_ID]`, `Invariant 15`)로 직접 매핑.
- **주요 프리미티브**:
  - `ZADD(key, score, member)`: B+Tree 인덱스에 $O(\log N)$으로 즉시 삽입/갱신.
  - `ZRANK(key, member)` / `ZREVRANK`: 특정 유저의 실시간 순위 조회.
  - `ZRANGE(key, start, stop)` / `ZREVRANGE`: 상위 N명 랭킹 리스트 조회.
- **혁신: RDBMS 테이블과의 Zero-Cost 하이브리드 로컬 조인**:
  - Redis 사용 시 불가피했던 'Redis 랭킹 조회 ➔ RDB 프로필 N+1 조회' 병목을 제거하고, 단일 SQL 쿼리로 0.1ms 만에 조인 완료:
  ```sql
  SELECT r.rank, u.nickname, g.guild_name, r.score
  FROM ZSET_RANGE('pvp_leaderboard', 1, 100) r
  JOIN Users u ON u.id = r.member
  JOIN Guilds g ON g.id = u.guild_id;
  ```

### 6.2 TTL (Time-To-Live) 자동 만료 테이블 (Auto-Expiring Tuples)
- **개념**: 세션 토큰, 버프/디버프 지속시간, 일일 퀘스트, 이벤트 우편함 등 기간 한정 데이터의 자동 소멸.
- **동작**:
  - DDL 수준에서 `CREATE TABLE user_buffs (...) WITH (TTL_COLUMN = 'expire_at')` 지정.
  - 읽기 시 만료된 행은 즉시 논리적 무효화(Read-time Expire Filter).
  - WiredTiger / In-Memory OCC의 백그라운드 체크포인트/콤팩션 스위퍼(Sweeper)가 CPU 오버헤드 없이 만료 행을 물리적으로 일괄 증발 처리 (`[RULE:AtomicSaves]`).
  - 게임 서버의 주기적 `DELETE WHERE expire_at < NOW()` 배치 락/부하 원천 제거.

### 6.3 고신뢰 원자적 비즈니스 프리미티브 셋 (Atomic Primitives Set)
게임 개발에서 아이템 재고(선착순 판매), 재화(골드/다이아 차감), 상태 머신 전이, 1회성 보상 지급 등은 동시성 레이스 컨디션과 복사 버그의 주원인입니다. LionSQL/Lioness는 Redis식 명령어 대신 **표준 엔지니어링 네이밍을 갖춘 6대 네이티브 원자적 프리미티브**를 제공하여 비즈니스 안정성을 보장합니다:

1. **`AtomicConsume(target, amount, min_bound=0)` (Underflow-Safe Decrement)**:
   - 재화/수량을 원자적으로 소비하며, 차감 결과가 `min_bound`(기본값 0) 미만으로 떨어지면 차감을 즉시 중단하고 명시적 에러(`ErrInsufficientBalance` / `ErrUnderflow`)를 반환 (`[RULE:ExplicitError]`, `[RULE:DefensiveIndexing]`).
   - 무거운 락이나 별도 Lua 스크립트 없이 선착순 판매 및 잔액 차감을 0나노초 안전성으로 보장.
2. **`AtomicProduce(target, amount, max_bound)` (Overflow-Safe Increment)**:
   - 재화/수량을 원자적으로 증가시키며, 최대 소지 한도(`max_bound`, 예: 골드 20억)를 초과하지 않도록 오버플로우 방어.
3. **`AtomicCompareSwap(target, expected_old_val, new_val)` (State Machine Transition)**:
   - 유저 및 세션의 상태 머신 전이(예: `OFFLINE` ➔ `MATCHING` ➔ `IN_GAME`)를 비관적 락(`SELECT FOR UPDATE`) 없이 단일 하드웨어 CAS 연산으로 원자적 수행.
4. **`AtomicSwap(target, new_val) -> old_val` (Atomic Fetch & Reset)**:
   - 이전 값을 안전하게 반환받음과 동시에 `new_val`(예: 0)로 리셋 (일일 누적 대미지 정산 후 리셋, 1회용 토큰 소비).
5. **`AtomicBitSet(target, bit_index) -> bool` (One-Time Quest/Achievement Flag)**:
   - 퀘스트/업적 1회성 달성 플래그를 비트 필드(`uint64`)로 원자적 검증. 해당 비트가 0이었으면 1로 세팅 후 `true` 반환, 이미 1이었으면 `false` 반환 (중복 보상 수령 원천 차단).
6. **`AtomicTouch(target, ttl)` (Heartbeat Extension)**:
   - 세션 키가 아직 유효하게 살아있을 때만 TTL을 원자적으로 연장 (Ping/Pong 하트비트). 이미 만료된 세션은 갱신하지 않고 세션 만료 에러 반환.
7. **`AtomicTransfer(src_queue, dest_queue)` (Zero-Loss Queue Move)**:
   - 우편함에서 인벤토리로의 아이템 수령, 대기열 큐에서 룸(Room)으로의 참가자 이동 등 큐 간 원자적 이동 (수령 중 서버 크래시 시 아이템 유실/복사 방지).

---

## 7. 게임 서버 인프로세스 직접 임베딩 아키텍처 (Zero-Socket In-Memory Direct FFI)

네트워크 루프백(TCP Loopback)조차 생략하고 게임 서버의 틱(Tick) 주기 내에서 나노초(ns) 단위로 데이터베이스 연산을 실행하기 위한 **인프로세스 임베딩(In-Process Embedding)** 아키텍처를 정의합니다.

### 7.1 C-Shared / C-Archive 라이브러리 인터페이스 (`liblionsql.so` / `lionsql.dll` / `liblionsql.a`)
Go의 `-buildmode=c-shared` / `-buildmode=c-archive` 기능을 통해 단일 헤더(`lionsql.h`)와 라이브러리를 빌드하여 C++, C#, Rust 게임 서버에 직접 정적/동적 링크합니다:

```c
// lionsql.h : 게임 서버용 표준 C-API

// 1. 엔진 인스턴스 라이프사이클
typedef void* lionsql_engine_t;
lionsql_engine_t lionsql_open(const char* data_dir, const char* options_json);
void             lionsql_close(lionsql_engine_t engine);

// 2. FlatBuffers 고속 Direct FFI (Zero-Copy 버퍼 포인터 직접 전달)
int lionsql_dispatch_flatbuf(lionsql_engine_t engine, 
                            const uint8_t* in_buf, size_t in_len,
                            uint8_t** out_buf, size_t* out_len);

// 3. 네이티브 원자적 프리미티브 직접 호출 (< 50ns)
int64_t lionsql_atomic_consume(lionsql_engine_t engine, uint16_t table_id, 
                              uint64_t key, int64_t amount, int64_t min_bound);
int     lionsql_atomic_cas(lionsql_engine_t engine, uint16_t table_id, 
                          uint64_t key, uint64_t expected_old, uint64_t new_val);
```

### 7.2 게임 엔진/서버 생태계별 연동 매핑
1. **C# / .NET Core 게임 서버 (Unity, Photon, 자체 데디케이티드 서버)**:
   - `[LibraryImport("lionsql")]` 또는 `[DllImport]` (P/Invoke) 단 몇 줄로 바인딩.
   - GC 부담 없이 언매니지드 버퍼 포인터를 직접 전달하여 **100ns 미만의 In-Memory 연산** 달성.
2. **C++ 게임 서버 (Unreal Engine Dedicated Server)**:
   - `liblionsql.a`를 정적 링크하여 외부 DLL 종속성 없이 **단 1개의 단일 실행 바이너리(`GameServer.exe`)**로 패키징.
3. **프로세스 격리가 필요한 경우 (Sidecar IPC)**:
   - 게임 서버 크래시와 DB 프로세스를 분리해야 하는 경우, TCP 대신 **Unix Domain Socket (`/tmp/lionsql.sock`)** 또는 **Windows Named Pipe (`\\.\pipe\lionsql`)**에 FlatBuffers를 직통 연결하여 < 5µs 초저지연 유지.

---

## 8. 게임 특화 차등 디스크 동기화 정책 (Per-Table Tiered Durability & Fsync Interval)

전통적인 RDBMS는 데이터베이스 인스턴스 전체에 일괄적인 `fsync` 정책을 적용합니다. 그러나 게임 데이터는 성격에 따라 요구되는 영속성 수준이 극단적으로 다릅니다:
- **유료 결제/계좌/핵심 아이템**: 단 1건의 유실도 용납되지 않음 (`Zero Data Loss`, 즉시 동기화)
- **월드 내 플레이어 좌표/체력/이동 경로**: 10초~30초 전으로 롤백되어도 게임 플레이에 지장이 없으며, 초당 수만 번의 빈번한 갱신 발생 (`Throughput First`)
- **채팅 로그/실시간 전투 통계**: 비정상 종료 시 일부 유실되어도 무방함 (`In-Memory Only / Lazy Flush`)

LionSQL/Lioness는 게임 서버의 처리량(Throughput)을 수십만 TPS로 극대화하기 위해 **"테이블 단위 차등 영속성 (Per-Table Tiered Durability)"**을 제공합니다.

### 8.1 DDL 테이블 단위 동기화 정책 스펙
`CREATE TABLE` 시 테이블별로 독립적인 디스크 동기화 주기(`SYNC_POLICY`)를 지정합니다:

```sql
-- 1. 유료 결제 및 계좌: 커밋 즉시 fsync (Zero Data Loss)
CREATE TABLE user_wallets (
    user_id BIGINT PRIMARY KEY,
    gem_balance BIGINT
) WITH (DURABILITY = 'SYNC_ALWAYS');

-- 2. 일반 인벤토리/장비: 1초 주기 백그라운드 그룹 커밋 (표준 기본값)
CREATE TABLE user_inventory (
    item_id BIGINT PRIMARY KEY,
    owner_id BIGINT
) WITH (DURABILITY = 'SYNC_INTERVAL', SYNC_INTERVAL = '1s');

-- 3. 유저 이동 좌표, 몬스터 스폰, 쿨타임: 10초~30초 주기 지연 플러시 (초고성능 Throughput)
CREATE TABLE player_locations (
    player_id BIGINT PRIMARY KEY,
    coord_x FLOAT, coord_y FLOAT, coord_z FLOAT
) WITH (DURABILITY = 'SYNC_INTERVAL', SYNC_INTERVAL = '10s');

-- 4. 휘발성 세션/매칭 대기열/채팅 룸: 디스크 동기화 비활성화 (순수 인메모리 OCC 속도)
CREATE TABLE world_chat_buffer (
    msg_id BIGINT PRIMARY KEY,
    msg_text VARCHAR(256)
) WITH (DURABILITY = 'MEMORY_ONLY');
```

### 8.2 다중 티어 WAL 플러셔(Multi-Tier WAL Flusher) 아키텍처
1. **분리된 WAL 링 버퍼 (Partitioned WAL Ring Buffers)**:
   - `SYNC_ALWAYS` 전용 로그 스트림과 `SYNC_INTERVAL` 지연 로그 스트림을 내부적으로 분리하여, 지연 테이블의 대량 쓰기가 결제 테이블의 즉시 커밋을 블로킹하지 않도록 격리 (`[RULE:LockHierarchy]`, `[RULE:NonBlockingSink]`).
2. **동적 티커 플러시 (Dynamic Ticker Flusher)**:
   - 각 인터벌(1s, 5s, 10s, 30s)별 백그라운드 고루틴이 타이머에 맞춰 비동기 배치 `fdatasync`를 실행하여 디스크 I/O IOPS를 90% 이상 절감.
3. **크래시 시 안전한 경계 복구**:
   - 서버가 비정상 다운되더라도 `SYNC_ALWAYS` 테이블은 100% 온전하게 복구되며, `10s` 인터벌 테이블은 마지막 플러시된 시점의 정합성 있는 스냅샷으로 안전하게 Fail-Closed 복구 (`Invariant 9: Zero-Truncate Fail-Closed`).

---

## 9. 게임 서버 특화 Zero-Reflection ORM (LionORM) & 멀티 DB 확장 전략

전통적인 웹 ORM(Hibernate, Entity Framework, Prisma)은 런타임 리플렉션, 비대한 객체 할당, 무거운 변경 감지(Dirty Checking)로 인해 게임 서버 틱 레이트(Tick Rate)를 파괴합니다.
**LionORM**은 컴파일 타임 코드 생성기(Roslyn Source Generator / C++ CodeGen) 기반의 **"Zero-Reflection, Zero-Allocation, Dirty Bitmask"** 게임 전용 데이터 바인딩 프레임워크로 설계됩니다.

### 9.1 타 DB(PostgreSQL, MySQL, Redis) 지원 vs. LionSQL에서의 압도적 성능 우위
LionORM은 드라이버 SPI 추상화를 통해 **다른 상용 DB(PostgreSQL, MySQL, Redis)도 지원 가능**하도록 설계하되, **LionSQL과 결합했을 때 독보적인 초격차 성능**을 발휘합니다:

| 비교 항목 | 타 DB 연동 시 (PostgreSQL / MySQL) | **LionSQL / Lioness 연동 시 (LionORM Native)** |
|---|---|---|
| **통신 경로** | TCP 루프백 네트워크 소켓 + Wire 프로토콜 | **인프로세스 C-FFI / P-Invoke 직접 호출 (< 100ns)** |
| **직렬화 비용** | SQL 문자열 포맷팅 / 파라미터 직렬화 | **FlatBuffers 바이트 포인터 Zero-Copy 즉시 전달** |
| **원자적 수량 제어** | 복잡한 트랜잭션 락 or 무거운 Lua 스크립트 | **`AtomicConsume` 네이티브 Opcode (0나노초 언더플로우 방어)** |
| **부분 갱신 (Partial)** | `UPDATE table SET col1=val1...` SQL 재생성 | **64-bit Dirty Bitmask 기반 변경 바이트만 초고속 스트리밍** |
| **내구성 제어** | DB 인스턴스 전체 일괄 `fsync`에 종속 | **테이블별 `DURABILITY = 'SYNC_INTERVAL 10s'` 초고속 플러시** |

### 9.2 핵심 아키텍처 및 게임 개발자 API
1. **컴파일 타임 무할당 코드 생성 (Roslyn Source Generator)**:
   - 런타임 리플렉션 0%. 빌드 시점에 C#/C++ 구조체의 필드 오프셋과 FlatBuffers 바인딩 코드가 100% 자동 생성.
2. **더티 비트마스크(Dirty Bitmask) 기반 델타 플러시**:
   - 객체 내 수십 개 필드 중 바뀐 필드만 64비트 정수 비트(`dirtyMask |= (1 << colIdx)`)로 추적하여, 네트워크/IPC 대역폭을 95% 절감.
3. **네이티브 원자적 비즈니스 메서드 바인딩**:
   ```csharp
   // 재화 차감: 잔액 부족(0 미만) 시 자동 실패 및 언더플로우 원천 차단
   bool success = await player.Gold.AtomicConsumeAsync(amount: 500);
   if (!success) {
       // 잔액 부족 에러 처리 (복사 버그 100% 방지)
   }

   // 퀘스트 1회성 보상 달성: 비트 연산으로 중복 수령 방지
   bool isFirst = await player.Quests.AtomicBitSetAsync(questId: 101);

   // 상태 머신 전이: 무거운 트랜잭션 락 없이 원자적 상태 변경
   bool ok = await player.State.AtomicCompareSwapAsync(PlayerState.Matching, PlayerState.InGame);
   ```
4. **Auto-Write-Behind 캐시 동기화**:
   - 메모리 상의 플레이어 객체를 게임 루프 방해 없이 10초 인터벌 테이블(`SYNC_INTERVAL = '10s'`)로 백그라운드 자동 플러시.

### 9.3 단일 물리 공간 공유 (Single Physical WT B+Tree) & C# 실무 코드 예제
LionORM과 표준 SQL(T-SQL/PGWire)은 별도의 복제본이나 메모리 복제 없이 **완전히 동일한 단일 물리 B+Tree 테이블(`table:<TableName>`)을 공유**합니다.

```
                  ┌───────────────────────────────┐
                  │      LionSQL Database         │
                  └──────────────┬────────────────┘
                                 │
         ┌───────────────────────┴───────────────────────┐
         │ (1번 문: 게임 서버 직통 고속 통로)             │ (2번 문: 표준 SQL 통로)
         ▼                                               ▼
 [ LionORM (Direct B+Tree) ]                      [ SQL (SSMS, DBeaver, Web Studio) ]
 - C-FFI / FlatBuffers                            - TDS :1433 / PGWire :5433
 - SQL 파서/플래너 완전 바이패스                  - SELECT / UPDATE / JOIN
 - 100나노초 초극저지연                             - 통계 분석, 운영툴, CS 복구 툴
         │                                               │
         └───────────────────────┬───────────────────────┘
                                 │
                                 ▼
         ┌───────────────────────────────────────────────┐
         │     WiredTiger Table: "users.wt" (B+Tree)     │
         │  - 동일한 LionSQL V2 튜플 바이너리 레코드        │
         │  - 동일한 기본 키 (Primary Key: user_id)       │
         │  - 동일한 트랜잭션 MVCC & 스냅샷 격리성        │
         └───────────────────────────────────────────────┘
```

#### 1. C# Entity 선언 (Roslyn Source Generator 적용)
```csharp
[LionTable("users", Durability = Durability.Sync10s)]
public partial struct UserEntity
{
    [LionKey] public long UserId { get; set; }
    public string Nickname { get; set; }
    public long Gold { get; set; }
    public long Gem { get; set; }
    public int State { get; set; }
    public long QuestFlags { get; set; }
    public float PosX { get; set; }
    public float PosY { get; set; }
}
```

#### 2. 게임 런타임 비즈니스 로직 실행 (LionORM C#)
```csharp
var lion = LionDatabase.GetEngine(); // 인프로세스 C-FFI 직접 바인딩

// [조회]: SQL 파싱 없이 B+Tree 커널 직접 Seek (< 100ns)
UserEntity user = await lion.Users.FindAsync(userId: 12345);

// [재화 소비]: 0 미만 언더플로우를 나노초 단위로 100% 원자적 방어 (선착순/상점 구매)
bool success = await lion.Users.AtomicConsumeAsync(user.UserId, u => u.Gold, amount: 5000, minBound: 0);
if (!success) return ErrorCode.InsufficientGold;

// [퀘스트 최초 달성]: 비트마스크 연산으로 중복 보상 수령 차단
bool isFirst = await lion.Users.AtomicBitSetAsync(user.UserId, u => u.QuestFlags, bitIndex: 42);
if (isFirst) GiveReward(user.UserId);

// [이동 좌표 갱신]: 64-bit Dirty Bitmask로 바뀐 8바이트(PosX, PosY)만 초고속 스트리밍
user.PosX = 120.5f; user.PosY = 340.2f;
lion.Users.SaveDirty(ref user); // 10s 인터벌 플러시로 틱 레이트 렉(Stall) 0
```

#### 3. 운영/CS 툴 실시간 조회 (표준 SQL 동시 접근)
게임 서버가 위 C# 코드로 수량을 깎고 좌표를 이동시키는 바로 그 순간, 운영팀이나 기획자가 SSMS/DBeaver에서 표준 SQL을 실행하면 **0초의 지연도 없이 최신 변경 상태가 실시간으로 일관되게 조회**됩니다:
```sql
-- 게임 서버 런타임과 100% 동일한 WiredTiger B+Tree를 실시간 조회
SELECT user_id, nickname, gold, pos_x, pos_y 
FROM users 
WHERE user_id = 12345;
```

---

## 10. 단계별 구현 로드맵 (Post-1.0)

### Phase 1: Lioness 샤딩 코어 및 해시 파티셔닝 엔진 (DRAFT)
- **태스크 ID**: `DIST-LIONESS-SHARDING-ROUTER-1`
- **구현 범위**:
  1. `cmd/lioness`: 라우터 전용 독립 바이너리 진입점 구성.
  2. `pkg/sharding`: Consistent Hashing / MurmurHash3 기반 파티셔너 구현.
  3. 샤드 라우팅 테이블 및 메타데이터 카탈로그 정의 (`shards.json`).
  4. TDS / PGWire 리스너 바인딩 및 Targeted Single-Shard 쿼리 패스스루.
  5. 단일 샤드 트랜잭션 바인딩 및 크로스 샤드 쓰기 Fail-Closed 차단.

### Phase 2: FlatBuffers 고속 바이너리 RPC 프로토콜 (LionSQL & Lioness 공용)
- **태스크 ID**: `WIRE-FLATBUFFERS-BINARY-RPC-1`
- **구현 범위**:
  1. `pkg/wire/flatbuf`: FlatBuffers 바이너리 프로토콜 리스너(TCP `:9000`) 구현.
  2. LionSQL 단일 노드 연동: 게임 서버에서 들어온 FlatBuffers 버퍼를 VDBE 레지스터/V2 튜플로 직접 매핑(Zero-Copy DDL/DML).
  3. Lioness 라우터 연동: VTable 기반의 역직렬화 없는 초고속 `shard_key` 추출 및 바이트 슬라이스 포워딩.
  4. C++ / C# 게임 서버용 클라이언트 스키마(`.fbs`) 배포.

### Phase 3: 게임 서버용 Redis-Style 내장 자료구조 & Auto-TTL 지원
- **태스크 ID**: `FEAT-REDIS-PRIMITIVES-AND-TTL-1`
- **구현 범위**:
  1. B+Tree 기반 `ZSET` 네이티브 테이블 및 TVF(`ZSET_RANGE`, `ZSET_RANK`) 구현.
  2. 테이블 수준 `TTL_COLUMN` 스펙 및 In-Memory / WT 백그라운드 스위퍼 구현.
  3. 초경량 원자적 카운터(`INCRBY`) 및 키 점유(`SETNX`) 바이너리 Opcode 추가.

### Phase 4: Scatter-Gather 및 부분 집계(Partial Aggregation) 파이프라인
- **태스크 ID**: `DIST-LIONESS-SCATTER-GATHER-1`
- **구현 범위**:
  1. 모든 샤드에 대한 동시 비동기 쿼리 디스패치(`sync.WaitGroup`, Bounded Worker Pool).
  2. 스트리밍 결과 병합(`MergeSort` for `ORDER BY`, Streaming `UNION ALL`).
  3. `COUNT`, `SUM`, `MIN`, `MAX` 2단계 분산 집계 지원.
  4. 다중 샤드 분산 읽기 일관성 스냅샷(Consistent Snapshot Read) 지원.

### Phase 5: Zero-Double-Parsing 바이너리 IR / Bytecode 전송 프로토콜
- **태스크 ID**: `DIST-LIONESS-ZERO-DOUBLE-PARSING-1`
- **구현 범위**:
  1. Lioness ➔ LionSQL 간 전용 고속 IPC / 바이너리 RPC 채널 구축.
  2. LionSQL 파서가 생성한 AST/VDBE Opcode의 직렬화/역직렬화(Zero-Copy Tuple/Opcode codec).
  3. LionSQL 노드에서 텍스트 파싱을 건너뛰고 직접 VM 실행 파이프라인 연계.

---

## 11. 결론 및 기대 효과
- LionSQL은 초고속 단일 노드 스토리지 커널로서의 본질에 집중할 수 있습니다.
- Lioness는 무거운 분산 트랜잭션 엔진이 아닌 **KISS 원칙에 입각한 고성능 해시 파티셔닝 라우터**로서, Vitess/Citus의 장점을 흡수하면서도 코드 공유를 통한 이중 파싱 제거라는 독보적인 성능적 우위를 확보합니다.
- FlatBuffers 및 Redis-Style 내장 자료구조/TTL 확장을 통해, **"외부 캐시(Redis) 없이 단일 DB만으로 실시간 리더보드와 초고속 세션을 감당하는 궁극의 게임 백엔드 데이터 엔진"**을 완성합니다.
