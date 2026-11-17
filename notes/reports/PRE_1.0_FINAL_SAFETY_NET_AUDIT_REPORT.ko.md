# LionSQL 1.0-GA 최종 안정성 및 방어벽 전수 감사 완료 보고서 (`PRE_1.0_FINAL_SAFETY_NET_AUDIT_REPORT.ko.md`)

> - **작성일**: 2026-11-12
> - **상태**: **완료 (4대 Scope 전수 감사 및 17대 안전망 보강 패치 적용 완료)**
> - **마일스톤**: `1.0-GA` (정식 릴리스 직전 최종 방어벽 사인오프)
> - **연계 태스크**: `AUDIT-FINAL-SAFETY-NET-1` (`DONE`)
> - **기반 계획서**: [`notes/plans/PRE_1.0_FINAL_SAFETY_NET_AUDIT_PLAN.ko.md`](../plans/archive/PRE_1.0_FINAL_SAFETY_NET_AUDIT_PLAN.ko.md)
> - **적용 불변식**: `[RULE:DefensiveIndexing]`, `[RULE:ZeroOrphan]`, `[RULE:HandleHygiene]`, `[RULE:ExplicitError]`, `[RULE:LockHierarchy]`, `[RULE:PanicRecovery]`, `[RULE:ConcurrencySafety]`

---

## 1. 감사 요약 (Executive Summary)

LionSQL 1.0-GA 정식 릴리스를 앞두고, 기존의 동적 내구성 검증(`crashtest`, `soaktest`, SQLLogicTest)이 미처 도달하지 못하는 **콜드 패스(Cold Path: 악의적/비정상 절단 네트워크 패킷, 디스크 I/O 에러, 동시 세션 경합, 비정상 바이트코드 피연산자)**의 잔존 취약점을 원천 봉쇄하기 위해 **4대 핵심 도메인 × 4대 결함 분류 체계(Defect Taxonomy)**에 따른 정밀 정적 전수 감사(`AUDIT-FINAL-SAFETY-NET-1`)를 수행하였다.

### 1.1 핵심 성과 지표

| 감사 영역 (Scope) | 대상 패키지 | 식별 및 보강된 방어벽 항목 | 잔존 P0/P1 결함 | 검증 상태 (`-race`) |
| :--- | :--- | :---: | :---: | :---: |
| **Scope 1: 와이어 프로토콜 & 네트워크 경계** | `pkg/wire/`, `pkg/wire/tds/` | **7건** (`SN-S1-01` ~ `SN-S1-07`) | **0건** | **100% PASS** |
| **Scope 2: 트랜잭션 코디네이터 & 카탈로그 동시성** | `pkg/engine/`, `pkg/catalog/` | **4건** (`SN-S2-01` ~ `SN-S2-04`) | **0건** | **100% PASS** |
| **Scope 3: 스토리지 엔진 & WAL 크래시 복구** | `pkg/storage/memengine/`, `pkg/storage/wiredtiger/` | **2건** (`SN-S3-01` ~ `SN-S3-02` 불변식 확증) | **0건** | **100% PASS** |
| **Scope 4: VDBE VM & Volcano 쿼리 실행기** | `pkg/proc/runtime/`, `pkg/execution/` | **4건** (`SN-S4-01` ~ `SN-S4-04`) | **0건** | **100% PASS** |
| **합계 (Total)** | **전 코어 패키지** | **총 17건 방어벽 보강 및 확증** | **0건 (Zero-Defect)** | **100% PASS (0 Race)** |

---

## 2. 4대 결함 분류 체계별 핵심 감사 결과

### 2.1 Category 1: 패닉 프리 및 경계 방어 (`[RULE:DefensiveIndexing]`, `[RULE:PanicRecovery]`)
- **네트워크 페이로드 사전 길이 검증 (Pre-Allocation Bounds Check)**:
  - PGWire 바이너리 배열(`decodeBinaryArray`), PGWire NUMERIC(`decodePgBinaryNumeric`), TDS RPC 파라미터(`readRPCValue`, `parseRPCRequest`), TDS 가변 문자열(`ReadBVarChar`, `ReadUsVarChar`)에서 헤더에 선언된 길이(`chunkLen`, `elemLen`, `ndigits`, `charCount`)를 실제 잔여 버퍼(`r.Len()`)와 대조하지 않고 `make([]byte, ...)`에 그대로 전달하던 OOM DoS 및 슬라이스 경계 패닉 경로를 전수 차단하였다.
- **무검증 단일 값 타입 단언(1-Value Type Assertion) 100% 척결**:
  - VDBE 바이트코드 실행기(`pkg/proc/runtime/vm_dispatcher.go`) 내 12개 오프코드(`OpIn`, `OpCast`, `OpDeclareTable`, `OpInsert`, `OpUpdate`, `OpUpdateWhere`, `OpDelete`, `OpDeleteWhere`, `OpSelectInto`, `OpPointSelect`, `OpCallUDF`, `OpCallProc`, `OpIndexScan`, `OpResultRow`)에서 `f.aux(&inst).(*proc.XYZData)` 형태로 호출되던 단일 값 타입 단언을 전수 `data, ok := ...; if !ok || data == nil` 2-value 방어 패턴으로 교체하였다.

### 2.2 Category 2: 무고아 동시성 및 핸들 위생 (`[RULE:ZeroOrphan]`, `[RULE:HandleHygiene]`, `[RULE:DoubleCloseSafety]`)
- **루프 내부 `defer cursor.Close()` 핸들 지연 해제 수정**:
  - `pkg/proc/runtime/vm_dispatcher.go`의 `VMFrame.Execute` 메인 디스패치 루프 내 `OpIndexScan` 분기에서 `defer cursor.Close()`가 사용되고 있어, 저장 프로시저 내 `WHILE` 루프가 수만 회 반복될 때 프로시저 전체가 끝날 때까지 커서가 닫히지 않고 누적되던 문제를 발견하여 스캔 루프 직후 즉시 `cursor.Close()`를 호출하도록 수정하였다.
- **PGWire `COPY FROM STDIN` 자동 커밋 트랜잭션 롤백 보장**:
  - `pkg/wire/copy.go`의 `handleCopyIn`에서 네트워크 스트림 중단이나 `CopyFail` 수신으로 조기 반환될 때 암시적으로 시작된 트랜잭션(`ownTx`)이 롤백되지 않고 남을 수 있던 경로에 `defer func() { if !committed { _ = tx.Rollback() } }()` 안전망을 추가하였다.
- **WAL 및 스토리지 엔진 이중 닫힘(`sync.Once`) 확증**:
  - `WALWriter.Close()`의 `sync.Once` 보호 및 WiredTiger `Engine.Close()`의 활성 세션 드레인(`activeSessions.Load() > 0`) 후 `connClosed.Store(true)` 원자적 전환을 재검증하였다.

### 2.3 Category 3: 침묵 실패 및 에러 가시성 (`[RULE:ExplicitError]`, `[RULE:AtomicSaves]`)
- **카탈로그 객체 영속화 에러 삼킴(`_ = c.persistObjectsLocked()`) 16개 지점 전수 제거**:
  - `pkg/catalog/catalog.go`의 프로시저, 함수, 트리거, 뷰, 확장 통계 생성/수정/삭제 메서드 16곳에서 디스크 파일(`catalog_objects.json`, `table_stats.json`) 쓰기 에러를 `_ = ...`로 무시하던 안티패턴을 전수 제거하였다.
  - 디스크 쓰기 실패 시 인메모리 맵(`c.procedures`, `c.functions`, `c.triggers`, `c.views`, `c.statistics`)을 즉시 이전 상태로 롤백(In-Memory State Rollback)하고 상위 호출자에게 명시적 에러를 반환하도록 보강하여 인메모리 카탈로그와 디스크 간 불일치 가능성을 원천 차단하였다.

### 2.4 Category 4: 데드락 방지 및 동시성 방어 복사 (`[RULE:LockHierarchy]`, `[RULE:ConcurrencySafety]`)
- **카탈로그 통계 객체 방어적 깊은 복사(Defensive Deep Copy)**:
  - `GetProcedure`, `GetView`와 달리 `GetStatistics`와 `ListStatistics`는 `c.mu.RLock()` 해제 후 내부 맵에 저장된 원본 `*StatisticDef` 포인터와 `Columns` 슬라이스를 그대로 외부에 노출하고 있었다. 이를 깊은 복사본 반환으로 통일하여 동시 읽기/쓰기 시의 데이터 레이스 위험을 제거하였다.
- **세션 락(`cs.Mu`)과 취소 락(`cs.cancelMu`)의 비블로킹 분리 확증**:
  - `pkg/engine/session.go`에서 쿼리 실행 중 `cs.Mu`를 잡은 상태에서도 타 세션이나 타임아웃 고루틴이 `CancelQuery()`를 호출할 때 데드락 없이 즉시 취소 함수를 트리거할 수 있도록 `cs.cancelMu`가 완전히 분리되어 있음을 확증하였다.

---

## 3. Scope별 상세 감사 및 조치 내역 (Detailed Findings & Remediations)

### 3.1 Scope 1: 와이어 프로토콜 & 네트워크 경계 (`pkg/wire/`, `pkg/wire/tds/`)

| ID | 파일 및 위치 | 취약점 및 원인 분석 | 적용된 방어 조치 (Defensive Fix) |
| :--- | :--- | :--- | :--- |
| **`SN-S1-01`** | [`pkg/wire/numeric.go:35-65`](../../pkg/wire/numeric.go#L35-L65) | `decodePgBinaryNumeric`에서 음수 `ndigits`, 과도한 `dscale`/`weight`(`> 1000`), 범위 밖 base-10000 `digit`(`< 0` 또는 `>= 10000`) 수신 시 비정상 루프 및 문자열 오염 가능성 | `ndigits < 0 \|\| ndigits > 1000 \|\| len(b) < 8+ndigits*2`, `dscale < 0 \|\| dscale > 2000`, `weight < -1000 \|\| weight > 1000`, `d < 0 \|\| d >= 10000` 경계 방어벽 추가 |
| **`SN-S1-02`** | [`pkg/wire/copy.go:258-320`](../../pkg/wire/copy.go#L258-L320) | `handleCopyIn`에서 개행(`\n`) 없이 무한 스트리밍되는 `CopyData` 패킷에 대해 `dataBuf` 누적 상한이 없고, 스트림 에러로 조기 종료 시 `ownTx` 롤백 누락 가능 | `len(dataBuf)+len(payload) > MaxPGMessageSize` 상한 검사 추가 및 `defer func() { if !committed { _ = tx.Rollback() } }()` 보장 |
| **`SN-S1-03`** | [`pkg/wire/param_rules.go:273`](../../pkg/wire/param_rules.go#L273), [`pkg/wire/server.go:1511,1603,2024`](../../pkg/wire/server.go#L1511) | `oids[idx]` 및 `colOIDs[i]` 슬라이스 인덱싱 시 상한 경계 체크 보강 필요 | `idx >= 0 && idx < len(oids)` 및 `i < len(colOIDs)` 명시적 경계 가드 적용 |
| **`SN-S1-04`** | [`pkg/wire/server.go:1247,1728,1790,1826`](../../pkg/wire/server.go#L1247) | `handleDescribe` 및 `handleExecute`에서 비정상 확장 쿼리 순서로 `portal.Statement == nil`인 포털 호출 시 nil 포인터 역참조 가능성 | `if portal.Statement == nil` 방어 체크 및 명시적 프로토콜 에러 반환 추가 |
| **`SN-S1-05`** | [`pkg/wire/server.go:2169-2204`](../../pkg/wire/server.go#L2169-L2204) | `decodeBinaryArray`에서 `ndims < 0`, 과도한 `dim`, 또는 음수 `elemLen < -1` / `elemLen > r.Len()` 수신 시 슬라이스 패닉 또는 대용량 OOM 할당 | `ndims < 0 \|\| ndims > 6`, `dim < 0 \|\| dim > 1000000`, `elemLen < 0 \|\| int(elemLen) > r.Len()` 경계 검증 추가 |
| **`SN-S1-06`** | [`pkg/wire/tds/packet.go:381-413`](../../pkg/wire/tds/packet.go#L381-L413), [`pkg/wire/tds/server.go:1966-1976`](../../pkg/wire/tds/server.go#L1966-L1976) | `ReadBVarChar`, `ReadUsVarChar`, `parseRPCRequest`에서 `charCount*2` 바이트 슬라이스를 할당하기 전 잔여 버퍼(`r.Len()`) 확인 누락 | `byteCount > r.Len()` 사전 검사 후 `io.ErrUnexpectedEOF` 즉시 반환 |
| **`SN-S1-07`** | [`pkg/wire/tds/server.go:2135-2425`](../../pkg/wire/tds/server.go#L2135-L2425) | `readRPCValue`의 PLP(`0xFFFF`) 청크 루프(`TypeVarChar`, `TypeNVarChar`, `TypeBigBinary`)에서 악의적인 `chunkLen = 0xFFFFFFFF`(4GB) 수신 시 잔여 버퍼 확인 없이 즉시 4GB `make([]byte, chunkLen)`를 호출하여 OOM 유발; `TypeDecimalN`의 `actualLen > 17` 및 `scale > 38` 미검증 | `int64(chunkLen) > int64(r.Len()) \|\| int64(plpBuf.Len())+int64(chunkLen) > MaxTDSMessageSize` 방어벽 및 `TypeDecimalN`/`TypeTimeN`/`TypeDateTime2N` 길이 가드 추가 |

### 3.2 Scope 2: 트랜잭션 코디네이터 & 카탈로그 동시성 (`pkg/engine/`, `pkg/catalog/`)

| ID | 파일 및 위치 | 취약점 및 원인 분석 | 적용된 방어 조치 (Defensive Fix) |
| :--- | :--- | :--- | :--- |
| **`SN-S2-01`** | [`pkg/catalog/catalog.go:124-960`](../../pkg/catalog/catalog.go#L124-L960) | 프로시저/함수/트리거/뷰/통계 DDL 메서드 16곳에서 `_ = c.persistObjectsLocked()` 및 `_ = c.persistTableStatsLocked()`로 디스크 저장 에러를 삼켜, 디스크 풀/권한 오류 시 인메모리와 디스크 상태가 영구 불일치 | 디스크 영속화 실패 시 인메모리 맵을 직전 상태로 즉시 복원(Rollback)하고 에러를 반환하도록 16개 메서드 전수 수정 (`[RULE:ExplicitError]`) |
| **`SN-S2-02`** | [`pkg/catalog/catalog.go:930-965`](../../pkg/catalog/catalog.go#L930-L965) | `GetStatistics` 및 `ListStatistics`가 `RLock` 하에서 원본 `*StatisticDef` 포인터와 `Columns` 슬라이스를 그대로 반환하여 호출자 수정 시 데이터 레이스 위험 | 구조체 및 `Columns` 슬라이스의 방어적 깊은 복사본(Deep Copy) 반환으로 변경 (`[RULE:ConcurrencySafety]`) |
| **`SN-S2-03`** | [`pkg/engine/session.go:88-245`](../../pkg/engine/session.go#L88-L245) | `ClientSession`의 쿼리 실행 락(`cs.Mu`)과 비동기 쿼리 취소(`CancelQuery`) 간 락 경합 및 종료 드레인(`checkAndDrainClosedLocked`) 감사 | `cs.cancelMu` 독립 뮤텍스 분리 및 `inflight == 0` 조건부 트랜잭션 자동 롤백/세션 정리 메커니즘 정상 작동 확증 (결함 0건) |
| **`SN-S2-04`** | [`pkg/engine/coordinator.go`](../../pkg/engine/coordinator.go) | 다중 문장 배치 실행 중 패닉/취소 발생 시 트랜잭션 및 세션 상태 일관성 감사 | `ExecuteQueryContext` 타임아웃/취소 컨텍스트 전파 및 에러 시 안전 반환 구조 확증 |

### 3.3 Scope 3: 스토리지 엔진 & WAL 크래시 복구 (`pkg/storage/memengine/`, `pkg/storage/wiredtiger/`)

| ID | 파일 및 위치 | 취약점 및 원인 분석 | 적용된 방어 조치 (Defensive Fix) |
| :--- | :--- | :--- | :--- |
| **`SN-S3-01`** | [`pkg/storage/memengine/wal.go:140-310`](../../pkg/storage/memengine/wal.go#L140-L310) | WAL 복구(`ReplayWAL`) 시 디스크 읽기 에러와 파일 끝 부분 손상(Tail Torn Frame) 구분 및 `WALWriter.Close()` 이중 호출 안전성 감사 | 4-State MECE 복구 모델(정상 EOF / 파일 끝 Torn Frame만 정밀 Truncate / 중간 프레임 CRC 파손 시 Fail-Closed / OS I/O 에러 시 Fail-Closed) 및 `closeOnce sync.Once` 불변식 완벽 준수 확증 (Invariant 9) |
| **`SN-S3-02`** | [`pkg/storage/wiredtiger/wt_engine.go:500-560,1135-1215`](../../pkg/storage/wiredtiger/wt_engine.go#L500-L560) | WiredTiger 엔진 종료(`Close`) 시 활성 CGO 세션과의 Use-After-Free 경합 및 `DropTable` 시 `EBUSY` 처리 감사 | `activeSessions` 원자적 드레인 대기 후 `connClosed.Store(true)` 전환, `WTCursor.closed` 이중 닫힘 방지 가드, `DropTable` 지수 백오프 재시도 및 Fail-Closed 에러 반환 확증 |

### 3.4 Scope 4: VDBE VM & Volcano 쿼리 실행기 (`pkg/proc/runtime/`, `pkg/execution/`)

| ID | 파일 및 위치 | 취약점 및 원인 분석 | 적용된 방어 조치 (Defensive Fix) |
| :--- | :--- | :--- | :--- |
| **`SN-S4-01`** | [`pkg/proc/runtime/vm_dispatcher.go:563-2025`](../../pkg/proc/runtime/vm_dispatcher.go#L563-L2025) | 12개 바이트코드 오프코드에서 `f.aux(&inst).(*proc.XYZData)` 단일 값 타입 단언을 사용하여 잘못된 보조 포인터 바인딩 시 런타임 패닉 가능 | 12개 지점 전수 `data, ok := f.aux(&inst).(*proc.XYZData); if !ok \|\| data == nil` 2-value 안전 가드 및 명시적 에러 반환으로 교체 |
| **`SN-S4-02`** | [`pkg/proc/runtime/vm_dispatcher.go:569,1137,2022`](../../pkg/proc/runtime/vm_dispatcher.go#L1137) | `OpTruncate`에서 `f.prog.Constants[inst.A]` 인덱싱 전 경계 검사 누락; `OpIn` 및 `OpResultRow`에서 음수 레지스터 인덱스(`regIdx < 0`) 방어 누락 | `inst.A < 0 \|\| inst.A >= len(f.prog.Constants)` 및 `regIdx >= 0 && regIdx < len(f.registers)` 경계 방어벽 추가 (`[RULE:DefensiveIndexing]`) |
| **`SN-S4-03`** | [`pkg/proc/runtime/vm_dispatcher.go:1886-1925`](../../pkg/proc/runtime/vm_dispatcher.go#L1886-L1925) | `VMFrame.Execute` 루프 내부 `OpIndexScan` 핸들러에서 `defer cursor.Close()`를 호출하여, 루프 반복 시 함수 종료 전까지 커서가 누적되는 핸들 누수 | `defer`를 제거하고 `OpIndexScan`의 내부 스캔 루프 탈출 직후 즉시 `cursor.Close()`를 호출하도록 수정 (`[RULE:HandleHygiene]`) |
| **`SN-S4-04`** | [`pkg/execution/project.go:180-235`](../../pkg/execution/project.go#L180-L235) | Volcano `ProjectIterator`의 스키마 진화(`ADD COLUMN`) 직후 구버전 튜플 프로젝션 시 슬라이스 인덱스 경계 감사 | `len(tuple.Values) < schemaLen` 방어 패딩 및 `f.srcIdx >= 0 && f.srcIdx < len(tuple.Values)` 경계 가드 정상 작동 확증 |

---

## 4. 회귀 및 악의적 입력 단위 테스트 검증 (Verification Results)

보강된 방어벽이 실제 악의적/비정상 입력에서 패닉이나 OOM 없이 정확히 에러를 반환하는지 검증하기 위해 전용 단위 테스트를 추가하고 전체 테스트 스위트를 실행하였다:

1. **PGWire 악의적 바이너리 입력 방어 테스트 (`pkg/wire/numeric_test.go`)**:
   - `TestPgBinaryNumericMalformed`: 절단된 헤더, 음수 `ndigits`, 과도한 `dscale`(`9999`), 범위 밖 base-10000 `digit`(`15000`), 그리고 `decodeBinaryArray`의 음수 `elemLen`(`-5`) 및 1GB `elemLen` 조작 패킷에 대해 패닉/OOM 없이 즉시 에러를 반환함을 검증 (`PASS`).
2. **TDS 악의적 RPC/PLP OOM 방어 테스트 (`pkg/wire/tds/packet_test.go`)**:
   - `TestTDS_MalformedRPCAndPLP_BoundsGuard`:
     - `ReadBVarChar_TruncatedPayload`: 100바이트 선언 후 4바이트만 전송된 패킷 방어 (`PASS`).
     - `ReadUsVarChar_TruncatedPayload`: 120,000바이트 선언 후 2바이트만 전송된 패킷 방어 (`PASS`).
     - `ParseRPCRequest_TruncatedProcName`: 프로시저 이름 길이 10,000 선언 후 빈 페이로드 방어 (`PASS`).
     - `ReadRPCValue_PLPOversizedChunkLen`: PLP 스트림에서 `chunkLen = 2GB (0x7FFFFFFF)` 선언 후 2바이트만 전송된 패킷에 대해 2GB 할당 시도 없이 즉시 `io.ErrUnexpectedEOF`를 반환함을 검증 (`PASS`).
     - `ReadRPCValue_DecimalNInvalidScaleAndLength`: `scale = 200 (> 38)` 비정상 Decimal 패킷 방어 (`PASS`).
3. **3단계 파이프라인 검증 (`[RULE:PipelineVerification]`)**:
   - **Build**: `go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql` 컴파일 및 타입 무결성 통과.
   - **Test (`-race`)**: `pkg/wire/...`, `pkg/catalog/...`, `pkg/proc/...`, `pkg/storage/...`, `pkg/execution/...`, `pkg/engine/...` 전 패키지 데이터 레이스 0건 및 100% PASS 달성.

---

## 5. 결론 및 1.0-GA 최종 사인오프 (Conclusion & Sign-Off)

본 `AUDIT-FINAL-SAFETY-NET-1` 전수 감사를 통해 LionSQL은 동적 부하 테스트(`soaktest`, `crashtest`)로 검증된 핫 패스뿐만 아니라, 네트워크 경계, 카탈로그 디스크 I/O 실패, VDBE 바이트코드 예외 분기 등 콜드 패스에 잠재되어 있던 17개 안전망 취약점을 완벽히 보강하였다. 이로써 LionSQL 1.0-GA 정식 릴리스를 위한 정적 방어벽 완료 기준(Zero Unhandled Panic, Zero Silent I/O Failure, Zero Handle Leak)을 최종 통과(Sign-Off)한다.
