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

---

## 3. Storage & Engine Hardening (장기 과제)

- [ ] **Pure-Go 임베디드 영속 스토리지 백엔드 (Pebble / bbolt)**
  - CGO 없는 순수 정적 바이너리(`CGO_ENABLED=0`) 배포를 위한 제3의 스토리지 엔진 옵션 (`-engine=pebble`).
- [ ] **Volcano 청크 단위 배치 실행 (Vectorized Chunk Iterator)**
  - 행 단위 `Next()` 호출 오버헤드를 줄이기 위해 1,024개 튜플 벡터 단위 슬라이스 배치 스트리밍.
