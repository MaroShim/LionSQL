# LionSQL Pitfalls & Engineering Tricks (`notes/PITFALLS_AND_TRICKS.md`)

이 문서는 LionSQL 코드베이스에서 작업할 때 다른 기계의 개발자나 AI가 섣불리 건드렸다가 컴파일 에러, 런타임 패닉, 데이터 레이스를 겪기 쉬운 **미묘한 지뢰밭(Landmines)과 특수한 아키텍처 트릭**을 기록합니다.

---

## 1. 듀얼 파서 순환 참조 방지 트릭 (Circular Import Immunity)

* **상황**: `pkg/proc`(원시 Pratt 파서, AST 정의)과 `pkg/proc/parser/antlr`(공식 ANTLR4 T-SQL 파서)은 서로를 필요로 합니다.
* **해결 구조**:
  ```
  pkg/proc (AST 정의, RegisterProcedureFallback SPI 제공)
     ▲
     │ import (단방향)
     │
  pkg/proc/parser/antlr (ASTBuilder 구현, init()에서 SPI 등록)
  ```
* **주의점**:
  * `pkg/proc` 내부 코드에서 절대 `sqlion/pkg/proc/parser/antlr`을 import하지 마십시오. Go 컴파일러가 즉시 순환 참조 에러(`import cycle not allowed`)를 냅니다.
  * ANTLR 폴백 기능이 동작해야 하는 바이너리(`cmd/server`, `cmd/bench` 등)나 테스트에서는 `import _ "sqlion/pkg/proc/parser/antlr"`를 명시하여 ANTLR의 `init()`이 실행되도록 해야 합니다.

---

## 2. 빌드 태그 불변식: 반드시 `-tags wiredtiger` 포함

* **상황**: WiredTiger 스토리지 엔진은 Cgo 기반으로 동작하며, 빌드 태그로 분기되어 있습니다.
  * `wt_engine.go`: `//go:build wiredtiger` (네이티브 B+Tree 구현)
  * `wt_stub.go`: `//go:build !wiredtiger` (미지원 스텁)
* **주의점**:
  * 빌드 및 테스트 시 `-tags wiredtiger`를 누락하면 스텁 코드가 링크되어 B+Tree 엔진 테스트 및 벤치마크가 실패합니다.
  * 빌드 표준 명령:
    ```bash
    go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server
    ```
  * 테스트 표준 명령:
    ```bash
    go test -tags wiredtiger -count=1 -race ./...
    ```

---

## 3. 테스트 환경에서의 샌드박스 네트워크 격리 (Localhost Loopback)

* **상황**: AI 샌드박스 환경(BypassSandbox: false)에서는 로컬 루프백(`127.0.0.1`) 네트워크 I/O가 OS 레벨에서 차단될 수 있습니다.
* **주의점**:
  * `pkg/replication`(WAL 스트리밍 복제 클라이언트)이나 `pkg/api/replication_integration_test.go`처럼 실제 HTTP 포트를 열고 클라이언트가 통신하는 통합 테스트는 샌드박스 내부에서 `deadline exceeded` 타임아웃이 발생할 수 있습니다.
  * 이러한 복제 네트워크 테스트는 샌드박스 우회(BypassSandbox: true) 상태에서 실행해야 정상 통과합니다.

---

## 4. `storage.Decimal` 및 `Value`의 JSON 직렬화 무결성

* **상황**: `storage.Decimal`은 내부에 비공개 필드(`value *big.Int`, `scale int32`)를 가집니다.
* **주의점**:
  * Go 기본 `json.Marshal`은 비공개 필드를 직렬화하지 못해 빈 객체(`{}`)로 출력되고, 역직렬화 시 `value`가 `nil`이 되어 산술 연산 시 패닉이 발생합니다.
  * 이를 방지하기 위해 `Decimal`에는 문자열 기반 `MarshalJSON`과 `UnmarshalJSON`이 구현되어 있습니다.
  * `storage.Value`에 새로운 필드나 타입을 추가할 때는 CBO 통계 영속화(`cbo_stats.json`) 및 카탈로그 덤프에서 JSON 왕복(Round-trip)이 완벽히 보존되는지 반드시 검증하십시오.

---

## 5. 비파괴적 원자적 파일 I/O 규약 (Golden Rule 1 & 5)

* **상황**: 서버 비정상 종료(Power-halt, SIGKILL) 시 설정 파일이나 WAL/통계 파일이 0바이트로 깨지는 문제 방지.
* **규약**:
  * 절대 파일에 직접 `os.OpenFile(..., O_TRUNC)` 또는 `os.WriteFile`을 수행하지 마십시오.
  * 1) `filepath.Clean`으로 경로 정규화.
  * 2) 동일 디렉토리에 임시 파일(`*.tmp.<nano>`) 생성 후 데이터 쓰기 (`0644`).
  * 3) `os.Rename`으로 원자적 교체(Atomic Commit).
  * 4) 에러 발생 시 임시 파일 정리(`defer os.Remove`).

---

## 6. API 테스트 픽스처: `setupTestServer()`는 빈 카탈로그를 반환함

* **상황**: `pkg/api` 패키지의 테스트 함수들에서 사용하는 `setupTestServer()` 헬퍼 함수는 미리 시드(Seed)된 계좌나 고객 데이터가 없는 깨끗한 빈 인메모리 엔진을 반환합니다.
* **주의점**:
  * 테스트 쿼리를 작성할 때 `SELECT * FROM accounts` 등을 바로 호출하면 `table "accounts" not found` 에러가 발생합니다.
  * 테스트 내에서 `CREATE TABLE ...` 및 `INSERT INTO ...`를 명시적으로 실행한 후 본 쿼리를 테스트해야 합니다.

---

## 7. VDBE 트랜잭션 결합 (Transaction Coupling Invariant)

* **상황**: TPC-C New-Order나 다단계 저장 프로시저를 실행할 때 각 구문을 개별 autocommit으로 실행하면 ACID 원자성이 깨집니다.
* **규약**:
  * 다단계 비즈니스 로직은 반드시 단일 트랜잭션 핸들 `tx`를 공유하여 `exec.RunInTx(prog, params, tx, nil)` 형태로 실행해야 합니다.
  * 도중 에러나 충돌(`ErrWriteConflict`, `WT_ROLLBACK`) 발생 시 즉시 `tx.Rollback()`을 호출하여 단 하나의 잔여 데이터도 남기지 않아야 합니다.

---

## 8. TDS 트랜잭션 디스크립터 & `PacketTransactionManager` (0x0E)

* **상황**: 공식 Microsoft Go 드라이버(`github.com/microsoft/go-mssqldb`)나 .NET SqlClient는 `db.BeginTx()` 호출 시 일반 텍스트 `BEGIN TRANSACTION` 배치 대신 TDS 전용 패킷인 `PacketTransactionManager` (0x0E, `tmBeginXact = 5`)를 전송합니다.
* **주의점**:
  * TDS 서버가 0x0E 패킷을 단순 DONE으로 처리하면, 클라이언트 드라이버 내부의 `sess.tranid`가 0으로 남아 이어지는 트랜잭션 쿼리 실행(`tx.Exec`) 시 `server does not have an active transaction: the transaction was aborted by the server, likely due to an error while SET XACT_ABORT is ON` 에러를 발생시킵니다.
  * 트랜잭션 시작 시 서버는 반드시 8바이트 고유 트랜잭션 디스크립터를 발급하고 `ENVCHANGE` 토큰 (Type 8: `EnvChangeBeginTransaction`)을 응답 스트림에 포함해야 하며, 트랜잭션 활성 중의 모든 DONE 토큰 상태 플래그에 `DoneInXact` (0x0004) 비트를 설정해야 합니다.
  * 트랜잭션 종료 시(COMMIT: Type 9, ROLLBACK: Type 10) 역시 구 디스크립터를 담은 `ENVCHANGE` 토큰을 반환해야 드라이버의 트랜잭션 상태 머신이 정상 복귀합니다.

---

## 9. PostgreSQL Wire Extended Query & lib/pq 바이너리 결과 디코딩

* **상황**: 공식 Go PostgreSQL 드라이버(`github.com/lib/pq`)는 매개변수 쿼리(`QueryContext(..., $1)`) 및 프리페어드 구문(`PrepareContext`) 실행 시 Extended Query Protocol (`Parse` ➔ `Describe` ➔ `Bind` ➔ `Execute` ➔ `Sync`)을 사용합니다.
* **주의점**:
  * **매개변수 개수 추론 (Parameter Inference)**: `lib/pq`는 Parse 요청 시 매개변수 타입 목록을 길이 0(`numParamTypes = 0`)으로 전달하며 서버가 쿼리 스트링의 `$1`, `$2`를 보고 매개변수 개수를 스스로 추론할 것을 기대합니다. 서버가 0개로 `ParameterDescription`을 응답하면 드라이버는 `got 1 parameters but the statement requires 0` 에러를 냅니다. 따라서 쿼리 스트링의 `\$(\d+)`를 스캔하여 최대 인덱스만큼 OID 0(Unspecified) 배열을 동적 할당해야 합니다.
  * **바이너리 결과 포맷 (Binary Result Format)**: `Describe` 응답의 `RowDescription`에서 컬럼 OID가 `oid.T_int4`(23), `int8`(20), `int2`(21), `bytea`(17), `uuid`(2950)로 보고되면, `lib/pq`는 `Bind` 패킷의 Result Format 코드로 바이너리(`format = 1`)를 요청합니다. 서버가 이를 무시하고 기존처럼 텍스트 문자열(예: `"20"`)을 `DataRow`로 전송하면, 드라이버의 `binaryDecode`가 4바이트 정수를 기대하다가 2바이트 ASCII를 읽고 슬라이스 범위 초과(`panic: runtime error: index out of range [3] with length 2`)로 런타임 패닉을 일으킵니다. 반드시 `Bind`의 Result Format을 파싱하여 빅엔디안 바이너리(`binary.BigEndian.PutUint32`)로 인코딩해야 합니다.
  * **트랜잭션 구문 및 CommandComplete 태그**: `db.BeginTx()`는 `BEGIN READ WRITE` 또는 `BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE`를 보냅니다. `sqlexec.go`에서 `BEGIN` 프리픽스를 유연하게 수용해야 하며, 응답 시 PostgreSQL CommandComplete 태그는 부가 옵션 없이 반드시 정규 태그(`BEGIN`, `COMMIT`, `ROLLBACK`, `SET`)여야 드라이버가 인식합니다.

---

## 10. Go Map 순회 비결정론(Non-Determinism)과 가상 카탈로그 Object ID 불일치 지뢰

* **상황**:
  * Go 언어의 `map` 순회(`for ... range map`)는 런타임 보안 및 의도치 않은 순서 의존성을 방지하기 위해 매 실행마다 무작위 시작 오프셋을 갖는 비결정론적(Non-deterministic) 특성을 가집니다.
  * WiredTiger 스토리지 엔진(`pkg/storage/wiredtiger/wt_engine.go`)의 초기 `ListTables()` 구현은 내부 `e.tables map[string]*storage.TableSchema`를 정렬 없이 그대로 순회하여 슬라이스를 반환했습니다.
  * 가상 카탈로그(`pkg/api/sqlexec_pgcatalog.go`)는 MSSQL 시스템 뷰(`sys.objects`, `sys.tables`, `sys.columns`, `sys.indexes` 등)의 `objectId`를 테이블 슬라이스 인덱스에 기반하여 `1000 + tIdx`로 부여했습니다.
* **치명적인 연쇄 장애 메커니즘 (Cascading Bug Mechanism)**:
  * GUI 클라이언트(DbGate, Beekeeper Studio 등)는 메타데이터를 단일 쿼리가 아닌 여러 독립적인 쿼리로 분할 로딩합니다:
    1. `modifications`: `sys.objects` 조회로 객체 목록 및 수정일자 확인
    2. `tables`: `sys.tables` 조회로 테이블 기본 정보 수집
    3. `columns`: `sys.columns` 조회로 컬럼 메타데이터 수집
    4. `primaryKeys`: `sys.indexes` 조회로 기본 키 정보 수집
  * 각 쿼리가 실행될 때마다 `ListTables()`의 반환 순서가 무작위로 섞였습니다:
    * 쿼리 1(`tables`): `[accounts (1000), customers (1001), students (1002), users (1003)]`
    * 쿼리 2(`columns`): `[users (1000), accounts (1001), students (1002), customers (1003)]`
  * 그 결과:
    * `tables` 쿼리에서 `accounts` 테이블은 `objectId: 1000`을 부여받았습니다.
    * 그러나 `columns` 쿼리에서 `objectId: 1000`은 `users`의 컬럼들(`name`, `balance`)로 인코딩되었습니다.
    * DbGate 클라이언트는 `columns.filter(c => c.objectId == table.objectId)`로 테이블과 컬럼을 조합하므로, `accounts` 테이블 아래에 `users`의 컬럼들이 매핑되고, `students` 아래에 `accounts`의 컬럼들이 매핑되는 대규모 메타데이터 뒤틀림이 발생했습니다.
    * 사용자가 GUI에서 `students` 테이블을 열면, GUI는 존재하지도 않는 컬럼(`SELECT [id], [holder], [balance] FROM [dbo].[students]`)을 쿼리하여 데이터 조회가 완전히 실패했습니다.
* **해결 및 영구 불변식 (Engineering Invariants)**:
  1. **스토리지 엔진 레벨 결정론적 정렬 보장**:
     - `memengine`과 `wiredtiger`의 `ListTables()`는 반환 슬라이스를 반드시 테이블 이름 오름차순으로 정렬(`sort.Slice(res, func(i, j int) bool { return res[i].Name < res[j].Name })`)하여 반환해야 합니다.
  2. **가상 카탈로그 레이어의 이중 방어**:
     - `pkg/api/sqlexec_pgcatalog.go`에 `getSortedTables()`, `getSortedViews()`, `getSortedProcedures()`를 배치하여 카탈로그 핸들러가 스토리지 엔진의 반환 순서와 무관하게 언제나 결정론적 순서와 `objectId`를 생성하도록 방어합니다.
  3. **스칼라 함수와 양방향 일관성**:
     - `OBJECT_ID('name')`과 `OBJECT_NAME(id)` 함수 역시 동일한 `getSortedTables()`를 공유하여, 개별 객체 조회와 전체 배치 카탈로그 조회 간에 단 1의 오차도 없는 일관성을 보장해야 합니다.

---

## 11. WiredTiger 커서의 EOF 래핑(Wrap-Around)과 벡터화 청크(ChunkedIterator) 무한 루프 지뢰

* **상황**:
  * WiredTiger 스토리지 엔진의 C API 커서(`WT_CURSOR`)는 테이블 끝(EOF)에 도달하여 `C.go_wt_cursor_next`가 0이 아닌 값(`WT_NOTFOUND`)을 반환한 후, 다시 `next()`를 호출하면 에러 상태를 유지하지 않고 **테이블의 첫 번째 레코드로 돌아가 다시 `0`(성공)을 반환하며 재순환(wrap-around)**하는 C API 고유의 순환 특성을 가집니다.
* **치명적인 무한 루프 메커니즘**:
  * P-4 벡터화 청크 배치 반복자(`ChunkedIterator`, `DefaultChunkSize = 1024`)는 청크 버퍼를 채울 때까지 `cursor.Next()`를 반복 호출합니다.
  * 테이블의 행 수가 1024개 미만(예: 2건, 50건 등 일반적인 테이블 데이터)인 경우:
    1. 첫 번째 청크 호출(`NextChunk`)에서 테이블 전체 레코드를 읽고 `cursor.Next()`가 false가 됩니다.
    2. 데이터가 채워졌으므로 청크는 정상 반환(`return nil`)됩니다.
    3. 다음 청크가 있는지 확인하기 위해 상위 연산자(`PullChunk`, `SortIterator`, `LimitIterator`)가 `NextChunk`를 다시 호출합니다.
    4. 이때 내부에서 다시 `cursor.Next()`를 부르면 WiredTiger 커서가 1번 레코드로 래핑되어 다시 `true`를 반환합니다!
    5. 이로 인해 청크가 끊임없이 무한히 채워져 반환되며, `SortIterator.Open`이나 `AggregateIterator.Open`이 100% CPU를 점유하며 영원히 끝나지 않는 무한 루프(Infinite Loading)에 빠집니다.
* **해결 및 영구 불변식 (Engineering Invariants)**:
  1. **스토리지 커서 레이어의 불변식 (`WTCursor`)**:
     - `WTCursor` 구조체에 `eof bool` 플래그를 추가하여, `C.go_wt_cursor_next`가 0이 아닌 값을 한 번이라도 반환하면 `eof = true`로 래치(Latch)하고 이후의 모든 `Next()`, `Record()`, `Row()`, `RawTuple()` 호출에서 즉시 `false` 및 에러를 반환해야 합니다.
  2. **실행 엔진 스캔 반복자 레이어의 이중 방어 (`SeqScanIterator`, `IndexScanIterator`)**:
     - `SeqScanIterator`와 `IndexScanIterator` 역시 자체 `eof bool` 플래그를 유지하여, 하위 커서가 `Next() == false`를 반환한 시점에 `eof = true`로 설정하고 이후 재진입 시 커서를 다시 건드리지 않고 즉시 `io.EOF`를 반환해야 합니다.

---

## 12. Graceful Shutdown 시 유휴 클라이언트 TCP 소켓과 `sync.WaitGroup` 무한 대기 지뢰

* **상황**:
  * GUI 클라이언트(DbGate, Beekeeper Studio 등)나 연결 풀(Connection Pool)은 TCP 연결을 맺은 뒤 커넥션을 닫지 않고 유휴(Keep-Alive) 상태로 열어둔 채 소켓 읽기(`net.Conn.Read`) 루프에서 블로킹 대기합니다.
* **문제 메커니즘**:
  * 서버 종료 시 리스너 소켓(`net.Listener.Close()`)만 닫고 `s.wg.Wait()`을 호출하면, 이미 수락(Accept)되어 읽기 루프를 돌고 있는 클라이언트 고루틴들은 원격 클라이언트가 연결을 끊기 전까지 종료되지 않습니다.
  * 결과적으로 프로세스가 `SIGINT`/`SIGTERM`을 받아도 `s.wg.Wait()`에서 수십 초 이상 멈춰 있거나 사실상 무한히 대기하는 현상이 발생합니다.
* **해결 및 영구 불변식 (Engineering Invariants)**:
  1. **활성 연결 추적 및 강제 소켓 종료**:
     - PGWire(`wire.Server`) 및 TDS(`tds.Server`)는 연결 수락 시 `activeConns map[net.Conn]struct{}`에 소켓을 등록하고, `Close()` 진입 시 리스너 종료 직후 등록된 모든 활성 클라이언트 소켓의 `conn.Close()`를 명시적으로 호출해야 합니다.
     - 소켓이 닫히면 블로킹 중이던 모든 고루틴의 `Read()`가 즉시 `net.ErrClosed` 에러를 만나 반환되며 `wg.Done()`이 호출됩니다.
  2. **1.5초 바운디드 셧다운 데드라인 (Golden Rule 4)**:
     - `cmd/server/main.go`의 시그널 수신 루프에서 최대 1.5초의 타임아웃 타이머(`time.After(1500 * time.Millisecond)`)를 동반한 채널 셀렉트를 적용하여, 만에 하나 비정상 블로킹이 발생하더라도 1.5초 이내에 결정론적으로 프로세스가 종료되도록 보장합니다.



