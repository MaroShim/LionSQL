# LionSQL 21대 기각 결정(Negative Knowledge) 전용 카탈로그 (`notes/SPEC_OUT_DECISIONS.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **최종 갱신일**: 2026-11-12
> - **문서 목적**: LionSQL 아키텍처에서 의도적으로 배제(Spec-Out)된 21대 기술 항목의 이론적 배경과 영구 기각 사유(Negative Knowledge)를 집대성하여, 협업 엔지니어 및 AI 에이전트의 불필요한 헛바퀴 개발(Wheel Reinvention)과 아키텍처 퇴행을 원천 방지합니다.

---

## 1. 개요 및 설계 철학

데이터베이스 엔진 개발에서 "무엇을 만들지 않을 것인가(What NOT to build)"는 "무엇을 만들 것인가"보다 훨씬 중요합니다. 무분별한 기능 확장은 다음과 같은 치명적인 부작용을 낳습니다:
1. **서브 마이크로초 지연시간 파괴**: 추가 계층과 토글 플래그가 늘어날 때마다 CPU L1/L2 캐시라인 바운싱과 분기 예측 실패가 발생합니다.
2. **동시성 모델 파편화**: 단일 엔진 내에 이질적인 트랜잭션 모델이 혼재되면 락 계층 구조(`[RULE:LockHierarchy]`)가 무너져 교착 상태(Deadlock)가 발생합니다.
3. **단일 바이너리 원칙 훼손**: 외부 데몬이나 무거운 C/C++ 런타임 의존성이 추가되면 정적 단일 바이너리 배포 원칙(`[RULE:StaticBinary]`)이 위배됩니다.

LionSQL은 `[RULE:RadicalKISS]` 원칙에 입각하여 다음 21개 항목을 명시적으로 **영구 기각(Permanently Spec-Out)**하였습니다.

---

## 2. 21대 영구 기각(Spec-Out) 결정 일람

### 1. PL/pgSQL 중복 인터프리터 기각
- **제안 내용**: PostgreSQL Wire Protocol을 통해 유입되는 쿼리를 위해 T-SQL VDBE와 별개로 PL/pgSQL 전용 인터프리터를 커널 내에 추가 구축.
- **기각 사유 (Why Not)**: 두 개의 서로 다른 절차적 언어 가상 머신을 유지보수하는 것은 코드베이스 복잡도를 두 배로 증가시키며, 세션 컨텍스트 및 트랜잭션 바인딩의 불일치를 초래합니다.
- **표준 대체안 (What We Do)**: 모든 저장 프로시저 및 제어문은 네이티브 T-SQL VDBE 바이트코드 VM(`pkg/proc`)으로 단일화합니다. PGWire는 클라이언트 통신 관문(Gateway) 역할만 수행하며, PGWire를 통해 유입된 T-SQL 문법도 VDBE에서 서브 마이크로초 단위로 동일하게 실행됩니다.

---

### 2. 레거시 XML DML / XQuery 기각
- **제안 내용**: Microsoft SQL Server의 구형 XML 데이터 타입 조작 문법(`nodes()`, `modify()`, `value()` 등) 및 XQuery 엔진 내장.
- **기각 사유 (Why Not)**: 복잡한 XML 파서는 바이너리 크기를 대폭 증가시키고 심각한 메모리 할당(GC 압력)을 유발합니다. 최신 클라우드 및 마이크로서비스 생태계는 반정형 데이터 표현으로 JSON에 수렴했습니다.
- **표준 대체안 (What We Do)**: 모던 JSON 표준 함수(`ISJSON`, `JSON_VALUE`, `JSON_MODIFY`, `JSON_QUERY`) 및 Pure-Go 제로 알로케이션 JSON 파서를 표준으로 사용합니다.

---

### 3. In-Memory OCC 백그라운드 진공(Vacuum) 기각
- **제안 내용**: PostgreSQL 스타일의 백그라운드 가비지 컬렉션 및 Dead-Tuple 정리 진공 스레드(Vacuum) 상시 구동.
- **기각 사유 (Why Not)**: LionSQL의 In-Memory OCC 엔진은 트랜잭션 전용 프라이빗 워크스페이스(Workspace) 내에서 행 갱신을 준비한 후, 커밋 시점에 원자적 스왑(Atomic Swap)을 수행합니다. 롤백 시에는 워크스페이스를 즉시 버리기만 하면 되므로 영구 메모리 공간에 Dead-Tuple 체인이 남지 않습니다.
- **표준 대체안 (What We Do)**: 불필요한 백그라운드 스레드를 제거(YAGNI)하여 CPU 리소스를 100% 사용자 OLTP 트랜잭션에 집중시킵니다.

---

### 4. POSIX 공유 메모리 (Arrow IPC) 스냅샷 기각
- **제안 내용**: `/dev/shm` POSIX 공유 메모리를 통해 Apache Arrow IPC 형식으로 테이블 스냅샷을 실시간 공유.
- **기각 사유 (Why Not)**: OS 플랫폼 간 이식성 한계(일부 클라우드/컨테이너 환경에서 `/dev/shm` 64MB 고정 크기 제한), 높은 OLTP 직렬화 CPU 스파이크, 프로세스 간 동기화 락 경합이 발생합니다.
- **표준 대체안 (What We Do)**: 1-Writer Multi-Reader WAL 스트리밍 및 CDC(Change Data Capture) 스트림으로 일원화합니다.

---

### 5. 분산 2PC 코디네이터 (XA / MS DTC) 기각
- **제안 내용**: 분산 2단계 커밋(2-Phase Commit) 코디네이터를 커널에 내장하여 분산 트랜잭션 제어.
- **기각 사유 (Why Not)**: 2PC는 네트워크 지연시간에 종속되어 코디네이터 장애 시 모든 참가 노드의 트랜잭션이 중단(Blocking)되는 치명적인 취약점이 있습니다. LionSQL의 단일 노드 수직 확장(Scale-Up First, 100만+ tpmC) 목표와 정면 배치됩니다.
- **표준 대체안 (What We Do)**: 1-Writer Multi-Reader 비동기/반동기 WAL 복제 토폴로지 및 애플리케이션 레벨 사가(Saga) 패턴을 표준으로 채택합니다.

---

### 6. 커널 내부 OAuth2 웹 브라우징 흐름 기각
- **제안 내용**: 데이터베이스 엔진 내부에서 외부 IdP(Google, GitHub 등)와 직접 아웃바운드 HTTP 통신을 수행하는 OAuth2 리다이렉트 흐름 구현.
- **기각 사유 (Why Not)**: 데이터베이스 커널이 외부 인터넷으로 아웃바운드 HTTP 요청을 전송하는 것은 보안 격리 원칙을 위배하며 방화벽/에어갭 환경에서 장애를 유발합니다.
- **표준 대체안 (What We Do)**: 클라이언트 사이드 OIDC 토큰 교환(`POST /api/auth/token-exchange`) 방식을 표준화하여 커널 내부 아웃바운드 네트워크 통신을 완전히 배제합니다.

---

### 7. 커널 내부 대용량 BLOB 스토리지 및 이미지 리사이징 기각
- **제안 내용**: 데이터베이스 바이너리 내에 이미지 리사이징 라이브러리와 수 기가바이트급 BLOB 저장/가공 파이프라인 내장.
- **기각 사유 (Why Not)**: CPU 및 메모리 고갈을 유발하여 고속 트랜잭션 처리 스레드를 기아(Starvation) 상태에 빠뜨립니다.
- **표준 대체안 (What We Do)**: 파일 바이너리는 S3 호환 오브젝트 스토리지에 Pre-signed URL을 통해 직접 업로드/다운로드하고, 데이터베이스는 메타데이터와 파일 URL만 보관합니다.

---

### 8. 미성숙 Pure-Go 스토리지 엔진 (Pebble/bbolt) 단기 도입 기각
- **제안 내용**: WiredTiger Cgo 의존성을 즉시 제거하기 위해 개발 중인 서드파티 Pure-Go 키-값 저장소 엔진을 기본 스토리지로 채택.
- **기각 사유 (Why Not)**: 엔터프라이즈 ACID 내구성, 장기 소크 테스트, B+Tree MVCC 완성도 면에서 10년 이상 검증된 WiredTiger를 즉시 대체할 수 없습니다.
- **표준 대체안 (What We Do)**: 1.0 프로덕션은 입증된 WiredTiger MVCC 디스크 엔진과 자체 In-Memory OCC 256-Shard 엔진을 표준으로 유지합니다.

---

### 9. 커널 내부 실시간 슬로우 쿼리 웹훅 (`OBS-3`) 기각
- **제안 내용**: 슬로우 쿼리 발생 시 커널이 지정된 슬랙/디스코드 웹훅 URL로 즉시 HTTP POST 요청 발송.
- **기각 사유 (Why Not)**: 핫패스에서 외부 HTTP 호출을 동기 또는 비동기 고루틴으로 남발하면 슬로우 쿼리 폭주 시 고루틴 누수와 소켓 고갈이 발생합니다.
- **표준 대체안 (What We Do)**: Prometheus 메트릭 엔드포인트(`/metrics`) 및 표준 OTLP 텔레메트리를 통해 전문 모니터링 시스템(Prometheus, Grafana Alertmanager)이 능동 수집(Pull)하도록 위임합니다.

---

### 10. 커널 내부 Wasm UDF 런타임 샌드박스 (`wazero`) 기각
- **제안 내용**: WebAssembly 런타임을 임베딩하여 다국어 사용자 정의 함수(UDF)를 실행.
- **기각 사유 (Why Not)**: 바이너리 용량 급증, JIT 컴파일 오버헤드, Wasm 메모리 경계 데이터 복사 오버헤드로 인해 서브 마이크로초 쿼리 성능이 심각하게 훼손됩니다.
- **표준 대체안 (What We Do)**: Go 언어로 구현된 제로 알로케이션 Native UDF SPI(`RegisterScalarFunction`)를 제공하여 제로 오버헤드 네이티브 성능을 보장합니다.

---

### 11. 복합 분석 쿼리의 무리한 바이트코드 단일 변환 (`ARCH-2TIER-1`) 기각
- **제안 내용**: 복잡한 다중 테이블 조인, 윈도우 함수, CTE 등을 모두 단일 선형 VDBE 바이트코드 루프로 강제 변환.
- **기각 사유 (Why Not)**: 복합 관계대수 트리를 선형 바이트코드로 평탄화하면 CBO 옵티마이저의 동적 조인 순서 재배치와 파이프라인 스트리밍 최적화가 극도로 어려워집니다.
- **표준 대체안 (What We Do)**: 2계층 하이브리드 엔진(`ARCH-2TIER`) 채택. 단순 OLTP CRUD 및 제어문은 1계층 VDBE 바이트코드로 직결하고, 복합 관계대수 쿼리는 2계층 Volcano CBO 반복자 파이프라인(`OpRelQuery`)으로 디스패치합니다.

---

### 12. 순수 Columnar Vectorization 전면 재작성 기각
- **제안 내용**: 기존 행 기반(Row-Store) 스토리지 구조를 버리고 ClickHouse 스타일의 100% 컬럼형 벡터화 엔진으로 전면 재작성.
- **기각 사유 (Why Not)**: 극단적인 코드 복잡도 증가, 단일 행 INSERT/UPDATE 트랜잭션 지연시간 급증 등 OLTP 본연의 가치를 훼손합니다.
- **표준 대체안 (What We Do)**: 1,024개 행 단위 청크 스트리밍 반복자(`ChunkedIterator`)와 제로 언박싱 슬롯 최적화를 통해 OLTP 처리량을 유지하면서 분석 쿼리를 가속합니다.

---

### 13. MySQL 고유 DML 및 8열 평면 EXPLAIN 확장 기각
- **제안 내용**: `ON DUPLICATE KEY UPDATE`, 쉼표 구분 `LIMIT offset, count`, 레거시 8열 테이블형 `EXPLAIN` 지원.
- **기각 사유 (Why Not)**: PostgreSQL `ON CONFLICT DO UPDATE` 및 표준 ANSI/MSSQL `MERGE`가 이미 완비되어 있으므로, MySQL 고유 DML을 추가하면 충돌 처리 경로가 3개로 분기되어 버그 발생 가능성이 급증합니다. 또한 8열 평면 `EXPLAIN`은 계층적 CBO 플랜 트리를 훼손합니다.
- **표준 대체안 (What We Do)**: 현대적인 들여쓰기 트리 텍스트 및 JSON 플랜 표현을 표준화하고, MySQL 호환성은 1단계 편의 문법(`DESC`, `SHOW TABLES`, 백틱 식별자)에 한정합니다.

---

### 14. 인메모리 Varint/비트 패킹 레코드 압축 (Option B) 기각
- **제안 내용**: 메모리 절감을 위해 인메모리 레코드를 비트 단위 가변길이 정수(Varint)로 압축 보관.
- **기각 사유 (Why Not)**: 비트 패킹 압축은 매 컬럼 접근마다 25~50ns의 순차적 디코딩 루프를 강제하여, 인메모리 엔진의 최대 장점인 0.5ns 서브 나노초 $O(1)$ 고정 오프셋 읽기 성능을 파괴합니다.
- **표준 대체안 (What We Do)**: 포인터 없는 플랫 레코드(Pointer-Free Flat Record, Option A)를 표준 채택하여 GC 스캔 오버헤드를 0으로 만들고 즉시 고정 오프셋 접근을 보장합니다.

---

### 15. 인메모리 부분 컬럼 델타 워크스페이스 (`PERF-OCC-DELTA-1`) 기각
- **제안 내용**: UPDATE 시 수정된 컬럼만 부분 델타 체인으로 유지하여 워크스페이스 메모리 절감.
- **기각 사유 (Why Not)**: 트랜잭션 진행 중의 읽기, 보조 인덱스 유효성 검사, 가변길이 재정렬 시점에 델타 체인을 동적으로 오버레이 계산해야 하므로 심각한 코드 복잡도와 트랜잭션 이상 현상(Anomaly) 위험을 초래합니다.
- **표준 대체안 (What We Do)**: 원자적 행 단위 플랫 레코드 워크스페이스(`MemoryCompact`)를 표준화하여 83% 메모리 절감과 제로 GC 스캔을 달성합니다.

---

### 16. 단일 쿼리 멀티코어 병렬 실행 (`ARCH-INTRA-QUERY-PARALLEL-1`) 기각
- **제안 내용**: 단일 분석 SELECT 쿼리를 여러 고루틴/코어로 분할 실행하는 Intra-Query Parallelism 구현.
- **기각 사유 (Why Not)**: Exchange/Merge 동기화 복잡도가 폭증하며, 대규모 OLTP 워커 스레드의 CPU 코어를 기아 상태에 빠뜨려 동시성 트랜잭션 처리량을 급감시킵니다.
- **표준 대체안 (What We Do)**: 쿼리 간 동시성(Inter-Query Concurrency) 및 서브밀리초 OLTP에 집중하며, 무거운 OLAP 대규모 분석은 DuckDB CDC 연동으로 위임합니다.

---

### 17. 복합 상관 집계 서브쿼리 AST 디코릴레이션 기각 (`BENCH-TPCH-1`)
- **제안 내용**: Kim's Group-By Pushdown 알고리즘을 적용하여 상관 집계 서브쿼리를 복합 조인 트리로 강제 재작성.
- **기각 사유 (Why Not)**: 악명 높은 Count Bug(`COUNT(*) = 0` vs `NULL`)와 SQL 3VL(Three-Valued Logic) 왜곡 위험이 매우 큽니다.
- **표준 대체안 (What We Do)**: 쿼리 단위 유한 메모이제이션 캐시(`PERF-SUBQUERY-MEMO-1`)를 채택하여 100% SQL 의미론적 안전성을 보장하면서 최대 1,000배의 실전 속도 향상을 제공합니다.

---

### 18. 인메모리 OCC Epoch/Generation 기반 Zero-Lock 스냅샷 기각 (`PERF-OCC-ZERO-LOCK-1`)
- **제안 내용**: 글로벌 Epoch 카운터를 활용하여 읽기 시 락을 전혀 잡지 않는 스냅샷 격리 구현.
- **기각 사유 (Why Not)**: 긴 읽기 트랜잭션이 구버전 행 회수(Reclamation)를 지연시켜 심각한 메모리 팽창을 유발하고, 글로벌 원자적 Epoch 카운터에서 멀티코어 CPU 캐시라인 바운싱이 발생합니다.
- **표준 대체안 (What We Do)**: 256개 캐시라인 패딩 샤드와 포인터 없는 FlatRecord(`MemoryCompact`)를 표준화하여 104만 tpmC와 안정적인 메모리 풋프린트를 유지합니다.

---

### 19. Partial / Delta WAL 로깅 (`WAL-DELTA-1`) 기각
- **제안 내용**: UPDATE 시 전체 행 대신 변경된 컬럼만 차분(Delta) 비트마스크로 WAL에 로깅.
- **기각 사유 (Why Not)**:
  1. 크래시 복구 및 복제 리플레이 시 매 레코드마다 베이스 행을 먼저 읽어 패치해야 하는 치명적인 Read-Modify-Write 페널티가 발생하여 복구 시간 목표(RTO)가 대폭 늘어납니다.
  2. 수정되지 않은 컬럼을 포함하는 복합 보조 인덱스의 이전 키 값을 찾기 위해 추가 룩업이 필요하여 인덱스 정합성 훼손 위험이 있습니다.
  3. OS의 4KB 페이지 캐싱과 LionSQL의 그룹 커밋(`wal.GroupCommit`) 환경에서는 서브 블록 단위 차분이 실제 디스크 I/O 절감에 거의 기여하지 못합니다.
- **표준 대체안 (What We Do)**: 즉각적인 $O(1)$ Full-Row WAL 로깅을 단일 표준으로 유지하여 크래시 복구 무결성과 단순성을 극대화합니다.

---

### 20. 프로세스 내 Graceful Reboot 기반 멀티 DB 전환 기각 (`RESEARCH-INPROCESS-REBOOT-USE`)
- **제안 내용**: `USE <dbname>` 실행 시 프로세스 내에서 전체 커널을 Graceful Reboot하여 다른 서브폴더 스토리지로 재기동.
- **기각 사유 (Why Not)**:
  1. 동시 실행 중인 수천 개 클라이언트 세션의 연결 다중화와 트랜잭션 격리가 완전히 파괴됩니다.
  2. 고빈도 쿼리 경로에서 수 초 단위의 심각한 지연시간 스파이크가 발생합니다.
  3. 데이터베이스 간 교차 조인 및 3-Part 네이밍(`SalesDB.dbo.Orders`)이 물리적으로 불가능합니다.
- **표준 대체안 (What We Do)**: `v1.1.0`에서 **접두사 기반 논리적 데이터베이스 네임스페이스(`__db_<dbname>_*`)**(`ARCH-LOGICAL-NAMESPACE-1`)를 구현하여 재기동 없이 서브 마이크로초 교차 조인을 지원합니다.

---

### 21. 복합 중간 컬럼 가상 드롭 마스킹 (`PERF-OCC-INSTANT-DROP-COL-1`) 기각
- **제안 내용**: 테이블 중간 컬럼 DROP 시 물리적 데이터 재작성 없이 가상 톰스톤 마스킹을 통해 즉시 DDL 완료.
- **기각 사유 (Why Not)**:
  1. 운영 중인 프로덕션 데이터베이스에서 중간 컬럼을 삭제하는 것은 가용성을 위협하는 안티패턴입니다.
  2. 런타임에 물리 행을 재작성하면 전역 테이블 락과 지연시간 폭증이 발생합니다.
  3. 가상 톰스톤 마스킹이나 인디렉션 맵은 엔진 복잡도를 극도로 증가시키고 0.5ns 고정 오프셋 디코딩 핫패스를 영구적으로 퇴행시킵니다.
- **표준 대체안 (What We Do)**: Instant Tail-Only DDL을 표준으로 채택하고, 비말단 중간 컬럼 DROP은 Fail-Closed 원칙에 따라 명시적 에러(`ErrDropColumnNotTail`)로 거부하며, 표준적인 **Copy-and-Switch** 절차(`CREATE TABLE new AS SELECT ...` 후 원자적 이름 변경)를 안내합니다.
