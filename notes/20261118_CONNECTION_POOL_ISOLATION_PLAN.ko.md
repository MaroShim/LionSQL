# 커넥션 풀 격리 안정화 및 세션 리셋 아키텍처 설계서 (`notes/20261118_CONNECTION_POOL_ISOLATION_PLAN.ko.md`)

## 1. 개요 및 배경
- **태스크 ID**: `FEAT-SESSION-RESET-CONNECTION-POOL-ISOLATION-1`
- **목적**: 커넥션 풀러(HikariCP, PgBouncer, ADO.NET SqlClient, Tomcat DBCP 등)가 물리적 TCP 연결을 유지한 채 커넥션을 반환하고 재사용할 때, 이전 작업자의 미완료 트랜잭션, 임시 테이블(`#temp`), 세션 변수, Prepared Statement, 락이 다음 풀 사용자에게 누수(State Leakage)되는 현상을 원천 차단.
- **적용 범위**:
  1. **TDS (MSSQL)**: 패킷 헤더 플래그 `StatusResetConnection (0x08)`, `StatusResetConnectionSkipTran (0x10)` 및 RPC `sp_reset_connection` 완벽 지원.
  2. **PGWire (PostgreSQL)**: `DISCARD ALL` / `RESET ALL` 수신 시 코디네이터 세션 리셋 + 와이어 레벨 LRU 문장 캐시(`statements`) 및 포털(`portals`) 완전 클리어.
  3. **HTTP / Web Studio**: 명시적 세션 리셋 엔드포인트(`POST /api/session/reset`) 및 유휴 세션 스위퍼 안전 격리.

---

## 2. 현행 결함 분석 (Root Causes)

| 영역 | 현행 코드 상태 | 발생 가능한 결함 |
| :--- | :--- | :--- |
| **TDS 헤더 플래그** | `packet.go`에 `StatusResetConnection = 0x08` 정의만 있고 `server.go` 루프에서 무시됨 | 커넥션 풀 재사용 시 이전 쿼리의 미완료 트랜잭션, 행 잠금(Row Lock), `#temp` 테이블, `@var` 변수가 다음 사용자에게 누수 |
| **TDS RPC** | `sp_reset_connection` RPC 수신 미지원 | JDBC / ADO.NET 풀러가 명시적 RPC로 리셋 요청 시 인식 실패 |
| **PGWire DISCARD ALL** | `sqlexec.go`에서 임시테이블만 일부 삭제하고 트랜잭션 롤백 및 와이어 캐시 미정리 | 이전 사용자의 미완료 트랜잭션 잔류, `statements` 캐시 충돌(`prepared statement already exists`) |
| **세션 상태 격리** | `Coordinator`에 통합 `ResetSession()` 부재 (각 핸들러에서 부분 분산 처리) | 세션 변수(`cs.Vars`), 트랜잭션 카운트(`TranCount`), 커서(`Cursors`)가 불완전하게 초기화됨 |

---

## 3. 계층별 상세 아키텍처 및 구현 설계

### 3.1 코어 엔진 계층: `Coordinator.ResetSession()` 신설
- 위치: `pkg/engine/coordinator_impl.go` / `session.go`
- 시그니처: `func (c *Coordinator) ResetSession(sessionID string, resetTx bool) error`
- 원자적 리셋 작업 순서 (`cs.Mu` 보호):
  1. **트랜잭션 롤백 (`resetTx == true`)**:
     - `cs.Tx != nil`인 경우 즉시 `_ = cs.Tx.Rollback()`.
     - 트랜잭셔널 DDL 복구: `c.rollbackDDLLocked(cs, "")`.
     - 트랜잭션 핸들 및 플래그 초기화: `cs.Tx = nil`, `cs.TxFailed = false`, `cs.TranCount = 0`.
  2. **임시 테이블 및 테이블 변수 전수 드롭**:
     - `cs.TempTables` 및 `cs.TableVars`에 등록된 물리 테이블을 스토리지 엔진(`c.engine.DropTable`)에서 안전하게 제거.
     - 슬라이스 및 맵 초기화: `cs.TempTables = nil`, `cs.TempTableMap = nil`, `cs.TableVars = nil`, `cs.EphemeralTables = nil`.
  3. **커서 및 Prepared Statement 초기화**:
     - 열려있는 커서(`cs.Cursors`) 닫기 및 맵 초기화 (`cs.Cursors = nil`).
     - 엔진 레벨 문장 캐시 초기화 (`cs.PreparedStmts = nil`).
  4. **세션 변수 및 보안 컨텍스트 리셋**:
     - `cs.Vars` 맵 재생성 및 기본 시스템 설정(`transaction_isolation = "read committed"`, `search_path = "public"`, `timezone = "UTC"`) 복원.
     - `cs.VarLengths = nil`.
     - `cs.OriginalUser`가 존재하면 `cs.User = cs.OriginalUser`, `cs.OriginalUser = ""` 복원 (`EXECUTE AS` / `SETUSER` 초기화).
     - 가상 데이터베이스 초기화: `cs.Database = "master"`.

### 3.2 TDS 와이어 계층: `StatusResetConnection` 및 `sp_reset_connection` 구현
- 위치: `pkg/wire/tds/server.go`, `packet.go`
- 상세 구현:
  1. **패킷 헤더 비트 검사 (`runQueryWithAttention` 및 패킷 디스패처)**:
     - `p.Status&StatusResetConnection != 0` (0x08) 감지 시:
       `s.coordinator.ResetSession(sess.SessionID, true)` 즉시 호출하여 트랜잭션 롤백 및 세션 완전 초기화 후 요청 쿼리/RPC 실행.
     - `p.Status&StatusResetConnectionSkipTran != 0` (0x10) 감지 시:
       `s.coordinator.ResetSession(sess.SessionID, false)` 호출 (트랜잭션은 유지한 채 임시 객체만 리셋).
  2. **`sp_reset_connection` RPC 처리 (`handleRPC`)**:
     - `strings.EqualFold(rpc.ProcName, "sp_reset_connection")` 또는 고유 식별자 수신 시:
       `s.coordinator.ResetSession(sess.SessionID, true)` 실행 후 정상 완료 토큰(`BuildDone(TokenDone, DoneFinal, 0, 0)`) 반환.

### 3.3 PGWire 와이어 계층: `DISCARD ALL` 완전성 강화
- 위치: `pkg/wire/server.go`
- 상세 구현:
  1. `PreparedStatementCache.Clear()` 메서드 구현 (LRU 리스트 및 해시맵 원자적 클리어).
  2. `handleSimpleQuery`에서 `DISCARD ALL` 또는 `RESET ALL` 수신 시:
     - 와이어 레벨 Extended Query 캐시 초기화: `statements.Clear()`, `clear(portals)`.
     - 코디네이터 세션 리셋 호출: `s.coordinator.ResetSession(sessID, true)`.
     - PG 표준 응답 반환: `WriteCommandComplete(conn, "DISCARD ALL")` 및 `WriteReadyForQuery(conn)`.

### 3.4 HTTP API 계층: 명시적 리셋 엔드포인트 제공
- 위치: `pkg/api/handlers_admin.go`, `server.go`
- 엔드포인트: `POST /api/session/reset`
- 페이로드: `{"session_id": "...", "reset_tx": true}`
- 풀러 또는 웹 관리 스튜디오에서 명시적으로 세션을 안전하게 반환할 수 있도록 제공.

---

## 4. 검증 계획 (3-Stage Verification Pipeline)
1. **단위 및 통합 회귀 테스트 구축**:
   - `pkg/engine/session_pool_reset_test.go`: `Coordinator.ResetSession` 동작 시 트랜잭션 롤백, 임시 테이블 드롭, 세션 변수 초기화 검증.
   - `pkg/wire/tds/connection_reset_test.go`: TDS 패킷에 `StatusResetConnection` 비트를 설정하여 전송했을 때 이전 쿼리의 미완료 트랜잭션과 `#temp` 테이블이 즉시 격리/초기화되는지 검증.
   - `pkg/wire/connection_reset_test.go`: PGWire에서 `DISCARD ALL` 실행 후 미완료 트랜잭션 롤백 및 동일한 Prepared Statement 이름으로 재바인딩 가능한지 검증.
2. **동시성 및 0 Data Race 검증**:
   - `go test -v -race ./pkg/engine ./pkg/wire ./pkg/wire/tds ./pkg/api`
3. **빌드 검증**:
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
4. **커밋 및 원격 동기화**:
   - Conventional Commits (`feat(engine/wire): implement comprehensive connection pool session reset for TDS, PGWire, and HTTP`)
