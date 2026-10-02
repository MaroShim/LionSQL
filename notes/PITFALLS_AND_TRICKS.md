# SQLion Pitfalls & Engineering Tricks (`notes/PITFALLS_AND_TRICKS.md`)

이 문서는 SQLion 코드베이스에서 작업할 때 다른 기계의 개발자나 AI가 섣불리 건드렸다가 컴파일 에러, 런타임 패닉, 데이터 레이스를 겪기 쉬운 **미묘한 지뢰밭(Landmines)과 특수한 아키텍처 트릭**을 기록합니다.

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

