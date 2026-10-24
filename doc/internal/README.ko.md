# LionSQL 코어 엔진 내부 명세 디렉토리 (`doc/internal/`)

> [English Version](README.md)

이 디렉토리는 **LionSQL**의 핵심 스토리지 엔진 메커니즘, 바이너리 직렬화 포맷, 바이트코드 가상머신(VDBE) 명령어 셋(ISA), 쿼리 옵티마이저 파이프라인 및 와이어 프로토콜 내부 구현 상세를 다루는 코어 엔진 개발자용 기술 명세 디렉토리입니다.

---

## 핵심 엔진 내부 명세 색인 (Specifications Index)

### 1. 스토리지 & 영속성 (Storage & Persistence)
* **[`STORAGE.md`](STORAGE.md)**
  * `storage.Engine` 인터페이스 규약 및 백엔드 추상화 계약.
  * In-Memory OCC (단조 증가 정수 버전 기반) vs WiredTiger 락프리 B+Tree 페이지 구조.
  * 체크포인트, AOF 로그 압축, 물리 스토리지 디렉터리 레이아웃 및 메타데이터 영속화 (`catalog.json`, `cbo_stats.json`).
* **[`RECORD_BINARY_FORMAT.md`](RECORD_BINARY_FORMAT.md)**
  * 스키마 인식형 콤팩트 바이너리 튜플 직렬화 명세 (`pkg/storage/tuple.go`).
  * 비트 단위 레이아웃: `0x01` 버전 헤더, $\lceil N/8 \rceil$ NULL 비트맵, 고밀도 ZigZag Varint 페이로드.
  * 계보: PostgreSQL 힙 튜플 `t_bits`, CockroachDB/TiDB KV 인코딩, Protobuf Varint.
* **[`TRANSACTION_CONCURRENCY.md`](TRANSACTION_CONCURRENCY.md)**
  * 양대 스토리지 백엔드 전반의 트랜잭션 격리 및 동시성 제어 모델 명세.
  * 논블로킹 스냅샷 읽기 및 세션 프라이빗 워크스페이스 격리.
  * In-Memory OCC 단조 버전 검증 vs WiredTiger MVCC 해저드 포인터 및 트랜잭션 타임스탬프.
  * VDBE 트랜잭션 결합(`exec.RunInTx`) 및 벤치마크 원자적 트랜잭션 무결성.

### 2. 고성능 실행 & 메모리 최적화 (Execution & Memory)
* **[`ZERO_ALLOCATION_ARCHITECTURE.md`](ZERO_ALLOCATION_ARCHITECTURE.md)**
  * 20,000+ TPS를 견인하는 제로 할당 메모리 아키텍처 및 버퍼 재사용/GC 완화 전략.
  * 계층별 분석: TDS 단일 프레임 풀링, PGWire 바이너리 바인드 직접 디코딩, 무할당 카탈로그 사전 필터링(`containsFold`), VDBE 레지스터 재활용, Volcano 청크 스트리밍(`TupleChunk` sync.Pool), WiredTiger `WT_CURSOR` 풀링/리셋.
* **[`VDBE.md`](VDBE.md)**
  * SQLite 스타일의 레지스터 기반 가상 데이터베이스 엔진(VDBE) 아키텍처.
  * 바이트코드 명령어 사양 (`OpInit`, `OpOpenRead`, `OpIndexScan`, `OpRelQuery`, `OpYield`).
  * `sync.Pool` 레지스터 재사용 및 컴파일된 실행 계획 캐싱 (`*runtime.PlanCache`).
* **[`CBO_VOLCANO.md`](CBO_VOLCANO.md)**
  * Volcano 반복자 파이프라인 (`SeqScan`, `IndexScan`, `HashJoin`, `IndexNestedLoopJoin`, `GroupBy`).
  * 비용 기반 옵티마이저(CBO), 선택도 모델 및 64-버킷 균등 깊이 히스토그램.
  * 상수 폴딩, 조건 푸시다운, 상관 서브쿼리 비상관화(`SEMI`/`ANTI` 조인), 디스크 스필링 외부 머지 소트.

### 3. 와이어 프로토콜 내부 구현 (Wire Protocol Internals)
* **[`WIRE_PROTOCOLS.md`](WIRE_PROTOCOLS.md)**
  * **PostgreSQL Wire Protocol v3** (포트 `5433`): 단순 및 확장 쿼리 프로토콜 (`Parse`, `Bind`, `Execute`, `Sync`), 파라미터 바인딩, SSL/TLS 협상.
  * **Microsoft SQL Server TDS 7.x/8.0** (포트 `1433`): Prelogin, Login7, RPC 패킷 처리, `sp_prepexec` 바이트코드 플랜 캐시 직결.
  * 클라이언트 호환성 매트릭스 및 고루틴 커넥션 풀러.

---

## 연계 문서 및 진실 공급원 (SSOT)

- **공식 기술 문서 루트**: [`doc/README.ko.md`](../README.ko.md)
- **개발자 인수인계 및 아키텍처 맥락**: [`notes/README.ko.md`](../../notes/README.ko.md)
- **공식 레퍼런스 매뉴얼**: [`doc/REFERENCE_MANUAL.ko.md`](../REFERENCE_MANUAL.ko.md)
