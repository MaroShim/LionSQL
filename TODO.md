# SQLion Engineering TODO & Backlog

This document tracks active tasks, short-term engineering priorities, and conceptual backlog items for SQLion. For long-term architectural visions, refer to [ROADMAP.md](ROADMAP.md).

---

## 1. High Priority (Near-Term)

- [x] **공식 Microsoft MSSQL 드라이버 E2E 통합 검증 & TDS 호환성 강화 (`pkg/wire/tds`)**
  - `github.com/microsoft/go-mssqldb` 공식 Go 드라이버 기반 실제 E2E 통합 테스트 스위트 구축 (`driver_integration_test.go`).
  - TDS `PacketTransactionManager` (0x0E), Collation/Transaction `ENVCHANGE` 토큰, 트랜잭션 디스크립터 및 `DoneInXact` 상태 머신 완비.
  - 드라이버 Ping, CRUD, Prepared Statements (`sp_prepare`), 매개변수 바인딩, 세션 SET 옵션, 트랜잭션 커밋/롤백 100% 통과.
- [x] **공식 PostgreSQL lib/pq 드라이버 E2E 통합 검증 & Wire 프로토콜 호환성 강화 (`pkg/wire`)**
  - `github.com/lib/pq` 공식 Go 드라이버 기반 실제 E2E 통합 테스트 스위트 구축 (`driver_integration_test.go`).
  - Extended Query 프로토콜 매개변수 개수 추론(`handleParse`), 투영 컬럼 스키마 매칭(`writeSchemaDescription`), `Bind` 결과 바이너리 포맷 코드 지원(`formatColumnValue`, `WriteDataRowBytes`).
  - `BEGIN READ WRITE` 등 유연한 트랜잭션 구문 확장 및 PostgreSQL 표준 CommandComplete 태그 매핑.
  - 드라이버 Ping, 기본 쿼리, CRUD, 매개변수 쿼리, Prepared Statements, 트랜잭션(커밋/롤백), 세션 옵션, 메타데이터 100% 통과.
- [ ] **현대 표준 Go 드라이버 `pgx/v5` 통합 검증 및 파이프라이닝 지원 (`pkg/wire`)**
  - `github.com/jackc/pgx/v5` 기반 E2E 통합 테스트 스위트 구축.
  - Extended Query `Flush`(`'H'`) 메시지 처리 및 파이프라이닝 상태 머신 동기화.
  - `timestamp`, `timestamptz`, `bool`, `float8` 등 `pgx` 바이너리 인코딩/디코딩 타입 확장.
- [ ] **외부 언어 생태계(Python/Node.js) 클라이언트 호환성 검증 및 하네스 구축 (`pkg/wire`)**
  - Python `psycopg2`/`psycopg3` 드라이버 연결 및 초기 세션 쿼리(`extra_float_digits`, `server_version_num`) 처리.
  - Node.js `pg` 라이브러리를 활용한 커넥션 풀링 및 쿼리 실행 검증.
- [ ] **임베디드 웹 스튜디오 HA 복제 모니터링 대시보드 (`pkg/ui`)**
  - 브라우저 웹 스튜디오(`:8090/_/`)에 고가용성(HA) 복제 상태(Primary vs Replica) 시각화 카드 추가.
  - 연결된 Replica 목록, LSN 랙(Lag) 지연 시간, 스트리밍 상태 표시 및 수동 승격(`Promote`) 버튼 제공.
- [ ] **클라이언트 읽기 분산 로드밸런싱 가이드 & 라우팅 인터페이스 (`pkg/wire`, `pkg/api`)**
  - 클라이언트 드라이버/HTTP 요청에서 쓰기(`Primary`)와 조회(`SELECT`)를 자동 분기할 수 있는 라우팅 정책 및 헬퍼 정리.

---

## 2. Analytics & Parallelism Backlog (중기 과제)

- [ ] **공유 메모리(POSIX shm / mmap) 기반 Apache Arrow IPC 컬럼 스냅샷 (Zero-Copy DuckDB 연동)**
  - 특정 시점의 테이블 데이터를 Apache Arrow IPC RecordBatch 포맷으로 직렬화하여 `/dev/shm` 또는 `mmap` 임시 파일에 기록 (`SNAPSHOT TABLE <t> TO ARROW_SHM <name>`).
  - 외부 분석 프로세스(DuckDB, Python Polars, Pandas)에 파일 디스크립터(FD) 및 경로를 전달하여 Zero-Copy 커널 페이지 맵핑 기반 고속 벡터화 분석 실현.
  - 무결함 격리(Zero Blast Radius): 무거운 분석 쿼리가 OOM을 유발해도 OLTP 쓰기 트랜잭션은 100% 무중단 유지.
  - TTL 기반 자동 해제(`unlink` / `munmap`)로 메모리 누수 방지.
- [ ] **`CREATE INDEX` 백그라운드 병렬 정렬 및 벌크 로딩 (`pkg/catalog`, `pkg/storage`)**
  - 대용량 데이터에서 보조 인덱스 생성 시 데이터를 N개 청크로 분할하여 병렬 정렬 후 B+Tree/SkipList 벌크 삽입.
- [x] **Go 네이티브 스칼라 함수 등록 SPI (In-Process Go UDF) (`pkg/proc`)**
  - SQLion을 라이브러리로 임베딩하는 개발자를 위해 `proc.RegisterScalarFunction(name, fn)` Go API 제공. 제로 오버헤드 나노초 단위 직결 호출.
- [ ] **WebAssembly (Wasm) 런타임 기반 동적 UDF 샌드박스 (`pkg/extension`, `wazero`)**
  - 독립 서버 환경에서 `CREATE FUNCTION <name> AS WASM '<path.wasm>'` DDL 지원.
  - CGO 없는 순수 Go `wazero` 런타임 탑재, 메모리 샌드박싱(Golden Rule 26)을 통한 크래시/패닉 격리 및 크로스 플랫폼 호환.

---

## 3. CBO (Cost-Based Optimizer) & Query Optimization Backlog

- [x] **CBO 통계 메타데이터 디스크 영속화 (`pkg/catalog`, `cbo_stats.json`)**
  - `SetPersistenceDir` 기반 `cbo_stats.json` 원자적 파일 쓰기(`*.tmp` -> rename) 및 기동 시 자동 복원. 서버 재시작 후에도 `ANALYZE TABLE` 통계 및 최적화 실행 계획 영속 유지.
- [ ] **`ANALYZE TABLE` 통계 및 히스토그램 멀티코어 병렬 수집 (`pkg/planner`, `pkg/api`)**
  - 대형 테이블 분석 시 컬럼별 NDV, Min/Max, 20-버킷 등빈도 히스토그램 수집을 `sync.WaitGroup` 멀티코어 병렬화하여 통계 수집 시간 4~8배 단축 (로드맵 v0.12.0 P1).
- [ ] **소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델 (`pkg/execution`, `pkg/planner`)**
  - 양측 릴레이션이 인덱스 순서 또는 ORDER BY 정렬 상태일 때 메모리 해시 빌드 없이 $O(M+N)$ 단일 패스로 처리하는 `MergeJoinIterator` 및 CBO 비용 산출 공식 추가.
- [ ] **동적 계획법(DP / Selinger) 기반 대규모 조인 순서 최적화 ($N > 4$) (`pkg/planner`)**
  - 4개 이하의 체인 조인 전수 순열 탐색을 넘어, $N > 4$ 테이블 조인에 대한 Left-Deep DP / Bushy Tree 동적 계획법(DPccp) 탐색 지원.
- [ ] **다중 컬럼 확장 통계 (Extended / Multi-Column Statistics) (`pkg/planner`)**
  - 컬럼 간 강한 상관관계(예: 국가-도시)로 인한 복합 조건 카디널리티 과소평가(Underestimation)를 방지하는 다중 컬럼 NDV 및 상관관계 통계 수집.
- [ ] **OR 조건 및 불리언 표현식 트리 선택도 모델링 (`pkg/planner`)**
  - `OR` 조건 및 중첩 괄호 조건에 대해 합집합 공식 ($P(A \lor B) = P(A) + P(B) - P(A \land B)$) 기반 정밀 선택도 산출.
- [ ] **MCV (Most Common Values) 최빈값 목록 및 히스토그램 결합 (`pkg/planner`)**
  - 왜곡(Skew)이 심한 상위 N개 최빈값은 MCV 목록으로 보관하고 나머지 균등 분포는 히스토그램으로 이원화 처리.
- [ ] **백그라운드 자동 통계 갱신 데몬 (Auto-Analyze Daemon) (`pkg/catalog`, `pkg/api`)**
  - 테이블 DML 변경 비율(예: 10% 이상 Row 변동 발생)을 추적하여 백그라운드에서 비동기로 `ANALYZE` 자동 트리거.
- [ ] **CBO 옵티마이저 힌트 오버라이드 제어 (Optimizer Hints) (`pkg/planner`)**
  - `WITH (INDEX(...))`, `OPTION (HASH JOIN)`, `OPTION (FORCE ORDER)` 힌트로 CBO 비용 결정을 명시적으로 강제 제어.

---

## 4. Storage & Engine Hardening (장기 과제)

- [ ] **Volcano 청크 단위 배치 실행 (Vectorized Chunk Iterator)**
  - 행 단위 `Next()` 호출 오버헤드를 줄이기 위해 1,024개 튜플 벡터 단위 슬라이스 배치 스트리밍.
- [ ] **WiredTiger 세션 및 커서 풀링 최적화 (`pkg/storage/wiredtiger`)**
  - 고빈도 트랜잭션 경로에서 Cgo 커서 오픈/클로즈 오버헤드를 줄이기 위한 스레드 로컬 커서 재사용 고도화.

---

## 5. Next Week Architecture & Engine Refactoring Backlog (차주 핵심 리팩토링)

- [ ] **VDBE 바이트코드 디스패처 모듈화 (`pkg/proc/runtime/vm.go`)**
  - `VMFrame.Execute` 내 824라인 거대 `switch op.Code` 블록을 기능군별 private 헬퍼 메서드로 분리 (`execFlowOp`, `execStorageOp`, `execRelOp`, `execYieldOp`).
  - 인라이닝과 무결성을 유지하면서 함수 순환 복잡도(Cyclomatic Complexity) 완화 및 프로파일링 스택 추적성 개선.
- [ ] **아키텍처 계층 정립: SQL 실행 엔진 코디네이터 패키지 분리 (`pkg/api` ➔ `pkg/engine`)**
  - `pkg/api`에 집중되어 있는 15,000+ 라인의 SQL 실행 오케스트레이션 로직을 `pkg/engine` 또는 `pkg/coordinator` 코어 레이어로 승격.
  - `pkg/api`(HTTP REST/Studio), `pkg/wire`(PGWire), `pkg/wire/tds`(TDS) 세 어댑터가 동등하게 코어 엔진 인터페이스를 호출하도록 단방향 클린 아키텍처 확립.

