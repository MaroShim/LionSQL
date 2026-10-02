# SQLion Engineering Context & Architectural Invariants (`notes/ENGINEERING_CONTEXT.md`)

이 문서는 SQLion의 설계 원칙, 확립된 핵심 불변식(Invariants), 의도적으로 배제(Spec-Out)한 대안들의 이유, 그리고 사용자가 요구하는 엄격한 엔지니어링 및 협업 규약을 설명합니다.

---

## 1. 핵심 아키텍처 불변식 (Core Architectural Invariants)

새로운 환경의 AI나 개발자가 **가장 자주 범하는 실수는 다른 대형 DBMS(MySQL, Oracle, CockroachDB 등)의 복잡한 메커니즘을 섣불리 SQLion에 도입하려 하는 것**입니다. 아래 6가지 불변식은 심도 깊은 검토 끝에 확립된 핵심 기둥입니다.

### ① 데이터베이스 격리 전략: 인스턴스 단위 물리 격리 (Process-per-Database)
* **결정 사항**: 데이터베이스 단위 물리 프로세스/컨테이너 격리.
* **배제(Spec-Out)된 것**: 단일 프로세스 내 멀티 데이터베이스 카탈로그 멀티플렉싱 및 `USE <dbname>` 문법.
* **이유 & 맥락**:
  * 단일 프로세스 내 카탈로그 멀티플렉싱은 세션별 DB 컨텍스트 스위칭, 교차 DB 잠금 경합, 한 테넌트의 메모리 누수가 전체를 죽이는 장애 반경(Blast Radius) 문제를 낳습니다.
  * SQLion은 단일 정적 바이너리로 매우 가볍게 구동되므로, 멀티 테넌시는 포트(`--port`, `--http`)와 데이터 디렉토리(`--dir`)를 분리한 독립 프로세스/컨테이너로 배포하는 것이 Radically Simple(KISS)하며 보안 및 자원 격리에 절대적으로 유리합니다.

### ② 트랜잭션 동시성 모델: Non-Blocking 스냅샷 격리 / 커밋 읽기
* **결정 사항**: 독자(Reader)는 필자(Writer)를 막지 않고, 필자도 독자를 막지 않는 비차단 스냅샷 격리.
  * In-Memory OCC: 레코드 버전(`Version uint64`) 기반 낙관적 검증. 충돌 시 `ErrWriteConflict` 즉시 롤백.
  * WiredTiger MVCC: 타임스탬프 및 Hazard Pointer 기반 락프리 B+Tree. 충돌 시 `WT_ROLLBACK`.
* **배제(Spec-Out)된 것**: 2PL(Two-Phase Locking) 공유 읽기 잠금, `READ UNCOMMITTED`, 복잡한 SSI(Serializable Snapshot Isolation) 직렬화 의존성 그래프 추적.
* **이유 & 맥락**: 고빈도 OLTP 환경에서 가짜 어보트(False Abort) 패널티와 분산 락 오버헤드를 배제하고 초당 수만 건의 비차단 읽기/쓰기 처리량을 보장하기 위함.

### ③ 단일 정적 바이너리 자립성 (Zero External Dependencies)
* **결정 사항**: 코어 커널, PGWire(:5433), TDS(:1433), HTTP API(:8090), 임베디드 Web Studio, 고루틴 커넥션 풀러, 크론 스케줄러, SMTP 메일러가 단일 정적 실행 파일(`bin/server`) 내에서 동작.
* **배제(Spec-Out)된 것**: 외부 사이드카(PgBouncer, Redis 캐시 계층, Nginx, 외부 cron 데몬) 및 외부 코디네이터(ZooKeeper, etcd, Consul, Kafka).
* **이유 & 맥락**: SQLion의 최대 강점은 바이너리 하나 복사해서 `--dir`만 주면 수 초 만에 엔터프라이즈급 DB가 기동되는 간결함입니다. 외부 의존성 추가는 절대 금지됩니다.

### ④ 수직적 확장 우선(Scale-Up Primacy) & 1-Writer Multi-Reader 복제
* **결정 사항**: 단일 노드 하드웨어 포화(Multi-core, NVMe, VDBE 바이트코드 최적화) + 1-Writer Primary와 N-Reader Replica 간 비동기/반동기 WAL 스트리밍 복제.
* **배제(Spec-Out)된 것**: 분산 2PC(Two-Phase Commit) 트랜잭션 샤딩 및 멀티 마스터 클러스터링.
* **이유 & 맥락**: 분산 2PC는 네트워크 RTT로 인해 쓰기 지연시간을 급증시킵니다. SQLion은 단일 노드에서 16,000+ TPS 이상의 raw 성능을 뽑아내고, 읽기 확장은 LSN 기반 복제본으로 처리하며, 고가용성은 페일오버 승격(`Promote`)으로 해결합니다.

### ⑤ 듀얼 파서 전략 (Dual-Parser Architecture)
* **결정 사항**:
  * **Primary (Native Pratt Parser)**: `pkg/proc/parser.go`, 고빈도 쿼리 및 표준 절차 흐름용 초고속 제로할당 파서.
  * **Secondary Fallback (ANTLR4 TSqlParser)**: `pkg/proc/parser/antlr/`, MSSQL 전용 특화 문법(`[dbo].[Name]`, `WITH RECOMPILE` 등) 자동 폴백.
* **설계 원칙**: 두 파서 간 순환 참조(Circular Dependency)를 방지하기 위해 `proc.RegisterProcedureFallback` SPI로 분리되어 있으며, 두 파서 모두 영구적으로 유지/검증되어야 합니다.

### ⑥ 듀얼 트랙 UDF 전략 (ADR-018)
* **결정 사항**:
  * **Track 1 (Go Native In-Process SPI)**: `proc.RegisterScalarFunction`. Go로 SQLion을 임베딩하는 개발자를 위한 제로 FFI 오버헤드 직결 호출 (<5ns).
  * **Track 2 (WebAssembly 샌드박스)**: 독립 서버 환경에서 안전하게 외부 함수를 실행하기 위한 `wazero` 메모리 샌드박스.

---

## 2. 사용자의 엔지니어링 철학 및 협업 규약

SQLion 개발 시 아래의 원칙은 **절대적인 우선순위**를 가집니다:

1. **3단계 검증 파이프라인 (Build ➔ Test `-race` ➔ Commit)**:
   - 모든 수정 후에는 빌드 컴파일 검증 ➔ 레이스 디텍터를 포함한 전체 테스트 통과 ➔ Conventional Commit 순서를 엄격히 지켜야 합니다.
   - 명령어:
     ```bash
     go build -tags wiredtiger -buildvcs=false -o bin/server ./cmd/server && \
     go build -tags wiredtiger -buildvcs=false -o bin/bench ./cmd/bench && \
     go build -tags wiredtiger -buildvcs=false -o bin/tpcc_vdbe ./cmd/tpcc_vdbe
     go test -tags wiredtiger -count=1 -race <modified_packages>
     ```
2. **이모지 절대 배제 (Golden Rule 19)**:
   - 커밋 메시지, 소스코드 주석, 로그 출력, 문서 등에 장식용 이모지(🎉, 🚀, 💡 등)를 일체 사용하지 않으며, 단정한 엔지니어링 톤을 유지합니다.
3. **Radical KISS & Deletion-First (Golden Rule 15 & 21)**:
   - 복잡한 추상화나 불필요한 번역 계층을 새로 짜기 전에, **안 쓰는 레거시 코드를 삭제(Pruning)**하고 OS/Go 표준 라이브러리 프리미티브를 활용하는 해결책을 최우선합니다.
   - 복잡한 에뮬레이션이나 새로운 플래그를 도입하기 전에는 반드시 트레이드오프를 사용자에게 먼저 제안하고 합의를 거칩니다.
4. **비파괴적 원자적 파일 I/O (Golden Rule 1 & 5)**:
   - 데이터나 설정 파일을 쓸 때는 직접 덮어쓰지 않고, 반드시 `filepath.Clean` 후 임시 파일(`*.tmp`)에 쓰고 `os.Rename`으로 원자적 커밋을 수행합니다 (권한: `0644`, 디렉토리: `0755`).
5. **모놀리식 파일 리팩토링 기준**:
   - 단순히 기계적으로 라인 수만 보고 자르는 것이 아니라, **도메인 책임(Responsibility)별로 묶어서 분할**합니다.
   - 예: `sqlexec_dml.go`에서 SELECT/PIVOT/EXPLAIN/EVAL을 분리하고 순수 DML만 남김.
