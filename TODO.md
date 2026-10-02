# SQLion Engineering TODO & Backlog

This document tracks active tasks, short-term engineering priorities, and conceptual backlog items for SQLion. For long-term architectural visions, refer to [doc/ROADMAP.md](doc/ROADMAP.md).

---

## 1. High Priority (Near-Term)

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
- [ ] **`ANALYZE TABLE` 통계 및 히스토그램 병렬 수집 (`pkg/planner`)**
  - 대형 테이블 분석 시 컬럼별 NDV, Min/Max, 등빈도 히스토그램 수집을 `sync.WaitGroup` 멀티코어 병렬화.
- [ ] **`CREATE INDEX` 백그라운드 병렬 정렬 및 벌크 로딩 (`pkg/catalog`, `pkg/storage`)**
  - 대용량 데이터에서 보조 인덱스 생성 시 데이터를 N개 청크로 분할하여 병렬 정렬 후 B+Tree/SkipList 벌크 삽입.
- [x] **Go 네이티브 스칼라 함수 등록 SPI (In-Process Go UDF) (`pkg/proc`)**
  - SQLion을 라이브러리로 임베딩하는 개발자를 위해 `proc.RegisterScalarFunction(name, fn)` Go API 제공. 제로 오버헤드 나노초 단위 직결 호출.
- [ ] **WebAssembly (Wasm) 런타임 기반 동적 UDF 샌드박스 (`pkg/extension`, `wazero`)**
  - 독립 서버 환경에서 `CREATE FUNCTION <name> AS WASM '<path.wasm>'` DDL 지원.
  - CGO 없는 순수 Go `wazero` 런타임 탑재, 메모리 샌드박싱(Golden Rule 26)을 통한 크래시/패닉 격리 및 크로스 플랫폼 호환.

---

## 3. Storage & Engine Hardening (장기 과제)

- [ ] **Volcano 청크 단위 배치 실행 (Vectorized Chunk Iterator)**
  - 행 단위 `Next()` 호출 오버헤드를 줄이기 위해 1,024개 튜플 벡터 단위 슬라이스 배치 스트리밍.
- [ ] **WiredTiger 세션 및 커서 풀링 최적화 (`pkg/storage/wiredtiger`)**
  - 고빈도 트랜잭션 경로에서 Cgo 커서 오픈/클로즈 오버헤드를 줄이기 위한 스레드 로컬 커서 재사용 고도화.

---

## 4. Next Week Architecture & Engine Refactoring Backlog (차주 핵심 리팩토링)

- [ ] **VDBE 바이트코드 디스패처 모듈화 (`pkg/proc/runtime/vm.go`)**
  - `VMFrame.Execute` 내 824라인 거대 `switch op.Code` 블록을 기능군별 private 헬퍼 메서드로 분리 (`execFlowOp`, `execStorageOp`, `execRelOp`, `execYieldOp`).
  - 인라이닝과 무결성을 유지하면서 함수 순환 복잡도(Cyclomatic Complexity) 완화 및 프로파일링 스택 추적성 개선.
- [ ] **아키텍처 계층 정립: SQL 실행 엔진 코디네이터 패키지 분리 (`pkg/api` ➔ `pkg/engine`)**
  - `pkg/api`에 집중되어 있는 15,000+ 라인의 SQL 실행 오케스트레이션 로직을 `pkg/engine` 또는 `pkg/coordinator` 코어 레이어로 승격.
  - `pkg/api`(HTTP REST/Studio), `pkg/wire`(PGWire), `pkg/wire/tds`(TDS) 세 어댑터가 동등하게 코어 엔진 인터페이스를 호출하도록 단방향 클린 아키텍처 확립.

