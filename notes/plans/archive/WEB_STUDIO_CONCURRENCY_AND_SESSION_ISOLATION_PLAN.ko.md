# Web Studio 동시 접속성 강화 및 세션 격리 아키텍처 플랜 (`notes/WEB_STUDIO_CONCURRENCY_AND_SESSION_ISOLATION_PLAN.ko.md`)

## 1. 배경 및 문제 정의 (Background & Problem Statement)

LionSQL은 포트 `:8090`을 통해 단일 바이너리 임베디드 Web Studio(`GET /_/`) 및 RESTful BaaS API(`POST /api/sql`, `GET /api/realtime`, `GET /api/overview`)를 제공합니다.
현재 구조는 경량 고루틴과 Netpoller를 통해 수천 개의 연결 유지를 무리 없이 처리하지만, 다수의 관리자나 개발자가 동시에 Web Studio에 접속할 때 다음과 같은 두 가지 구조적 결함이 존재합니다:

1. **SQL 쿼리 에디터의 `default` 세션 공유 (세션 오염 및 간섭)**:
   - Web Studio 프론트엔드(`pkg/ui/dashboard.html`)에서 SQL 실행 시 `POST /api/sql` 바디에 `session_id`를 명시하지 않음.
   - 백엔드 핸들러(`pkg/api/handlers_admin.go`)는 `session_id`가 없으면 전역 `"default"` 단일 세션으로 폴백함.
   - **결과**: 다수의 사용자가 동시에 쿼리를 실행할 때 한 사용자가 연 트랜잭션(`BEGIN TRAN`)에 다른 사용자의 쿼리가 무단으로 엮이거나, 쿼리 취소(`POST /api/query/cancel`) 시 다른 사용자의 쿼리가 중단되는 동시성 오염 발생.
2. **Overview 메트릭 폴링 시 `runtime.ReadMemStats` STW 부하**:
   - Web Studio 대시보드가 열려 있으면 3초마다 `GET /api/overview`를 호출하여 실시간 메트릭(TPS, 쿼리수, 메모리)을 갱신함.
   - 각 요청마다 Go 런타임의 `runtime.ReadMemStats(&mem)`를 직접 동기 호출함.
   - **결과**: 동시 접속 브라우저가 수십~수백 대 이상으로 늘어날 경우, 초당 수십~수백 번의 Stop-The-World(STW) 미세 멈춤이 발생하여 OLTP 코어 엔진의 지연시간을 저하시킬 수 있음.

---

## 2. 해결 아키텍처 및 세부 설계 (Architecture Design)

### 2.1. Pillar 1: 브라우저 탭별 고유 세션 ID (`tabSessionId`) 격리
- **프론트엔드 (`pkg/ui/dashboard.html`)**:
  - 브라우저 탭 최초 로드 시 `sessionStorage`를 검사하여 고유 세션 식별자(`web-sess-` + UUID 또는 타임스탬프+랜덤 문자열)를 발급 및 보관.
  - SQL 쿼리 에디터 실행(`runQuery`), DDL/컬럼 추가 등 모든 `POST /api/sql` 호출 시 `{ query: sql, session_id: getWebSessionId() }`를 기본 포함하여 전송.
  - 쿼리 취소 버튼 클릭 시에도 자신의 `session_id`를 서버로 전송(`POST /api/query/cancel`, `{ session_id: getWebSessionId() }`).
- **백엔드 (`pkg/api/handlers_admin.go`, `pkg/engine/coordinator.go`)**:
  - 전달받은 `session_id`를 기반으로 `s.coordinator.GetOrCreateSession(sessID)`를 호출하여 탭별 독립 `ClientSession` 매핑.
  - 각 탭은 독립된 트랜잭션 문맥(`cs.Tx`), 세션 변수(`cs.Vars`), 쿼리 취소 핸들(`cs.CancelFunc`)을 배타적으로 보유하여 상호 간섭 100% 배제.
  - 브라우저 탭이 닫혀 유휴 상태가 된 세션은 `SessionPool`의 `IdleSessionTTL`(30분) 및 `ReapIdleSessions` 데몬에 의해 자동 정리되므로 세션 누수 없음.

### 2.2. Pillar 2: Overview 메모리 통계 1초 캐싱 (Zero STW Contention)
- **백엔드 (`pkg/api/server.go`, `pkg/api/handlers_admin.go`)**:
  - `Server` 구조체에 원자적 메모리 스냅샷 캐시 구조체 도입:
    ```go
    type memStatsCache struct {
        mu         sync.RWMutex
        allocBytes uint64
        goroutines int
        lastUpdate time.Time
    }
    ```
  - `GET /api/overview` 호출 시:
    - 마지막 업데이트로부터 1초 이내인 경우, 락 없는 원자적 읽기/RLock으로 캐시된 값을 즉시 반환.
    - 1초가 경과한 경우에만 단일 고루틴이 `runtime.ReadMemStats` 및 `runtime.NumGoroutine()`를 호출하여 캐시 갱신.
  - **효과**: 동시 접속자가 1,000명으로 증가하여 초당 333회의 `/api/overview` 요청이 쏟아져도, `ReadMemStats`는 초당 최대 1회만 호출되어 STW 오버헤드가 사실상 0으로 수렴.

---

## 3. 정량적 검증 기준 (Verification Criteria)

1. **세션 격리 무결성 검증**:
   - 두 개의 서로 다른 HTTP 클라이언트(Session A, Session B)가 동시에 접속하여:
     - Session A에서 `BEGIN TRANSACTION; UPDATE accounts SET balance = 500 WHERE id = 1;` 실행.
     - Session B에서 `SELECT balance FROM accounts WHERE id = 1;` 실행 시 커밋되지 않은 더티 리드 없이 격리 확인.
     - Session A에서 실행 중인 장기 쿼리를 취소했을 때 Session B의 쿼리는 영향받지 않음을 검증.
2. **동시성 및 레이스 검증**:
   - `go test -tags wiredtiger -race -count=1 ./pkg/api/...` 100% 통과 (0 data races).
3. **빌드 무결성**:
   - 전체 4대 바이너리(`lionsql`, `server`, `bench`, `tpcc-runtime`) 빌드 성공.

---

## 4. 실행 계획 (Implementation Roadmap)

- [ ] **Step 1**: 플랜 문서 및 UI 아티팩트 영구 기록.
- [ ] **Step 2**: 백엔드 `pkg/api/server.go` 및 `handlers_admin.go`에 Overview 메모리 캐싱(`memStatsCache`) 및 세션별 쿼리 취소 지원 구현.
- [ ] **Step 3**: 프론트엔드 `pkg/ui/dashboard.html`에 `getWebSessionId()` 및 `session_id` 연동 구현.
- [ ] **Step 4**: 동시성 세션 격리 및 메모리 캐시 유닛/통합 테스트 작성 및 `go test -race` 통과.
- [ ] **Step 5**: `TODO.md` 및 `CHANGELOG.md` 동기화, `v0.90.0-beta3` 태그 최신화 커밋.
