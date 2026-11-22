# LionSQL 공식 한도 및 알려진 제약사항 명세서 (Limitations & Known Issues)
`doc/LIMITATIONS.ko.md`

> - **최초 작성일**: 2026-11-22
> - **대상 버전**: LionSQL v0.94.0+ / 1.0-GA
> - **문서 목적**: 상용 또는 엔터프라이즈 프로덕션 환경에서 LionSQL의 도입 타당성을 객관적으로 평가할 수 있도록, 시스템의 물리적/논리적 한도(Limits & Quotas), 미지원 SQL 문법, 동시성 트레이드오프, 스토리지 엔진별 제약 및 알려진 미해결 과제(Known Issues)를 가감 없이 투명하게 공개합니다.
> - **연계 불변식**: `GEMINI.md` [Invariant 2, 3, 4, 5, 7, 10, 13, 15] 및 `doc/SPEC_OUT_DECISIONS.ko.md`

---

## 1. 개요 및 도입 판단 가이드 (Executive Evaluation Guide)

LionSQL은 **"단일 바이너리 초고속 임베디드 OLTP 데이터베이스 엔터프라이즈 엔진"**을 지향합니다. 단일 노드에서 20,000 ~ 34,000+ TPS의 초고동시성 트랜잭션과 마이크로초 단위의 T-SQL 저장 프로시저 실행을 달성하기 위해, 복잡한 분산 코디네이터나 레거시 RDBMS의 무거운 기능들을 의도적으로 배제(Radical KISS, Spec-Out)하는 전략적 아키텍처 결정을 내렸습니다.

따라서 사용자는 장점뿐만 아니라 아래의 제약과 트레이드오프를 사전에 명확히 이해하고 도입 여부를 판단해야 합니다.

### 1.1 최적의 도입 대상 (Recommended Workloads)
- **고빈도 OLTP 마이크로서비스**: 결제, 주문, 게임 서버, 세션 관리 등 초당 수만 건의 읽기/쓰기가 단일 노드에서 집중되는 시스템.
- **T-SQL 기반 저장 프로시저 마이그레이션**: 무거운 MS SQL Server 인스턴스를 경량 단일 Go 바이너리로 축소/대체하고자 하는 환경.
- **임베디드 및 엣지 컴퓨팅**: 외부 데몬(Redis, PgBouncer, Nginx) 없이 REST API, Web Studio, 와이어 리스너가 단일 실행 파일(`.exe` / ELF)로 패키징되어야 하는 엣지/온프레미스 노드.
- **하이브리드 프로토콜 서비스**: 단일 데이터 스토어에 대해 PostgreSQL 와이어(psql, Prisma, DBeaver)와 TDS 와이어(SSMS, ADO.NET)가 동시에 접속해야 하는 환경.

### 1.2 비추천 대상 (When NOT to Use LionSQL)
- **대규모 페타바이트급 분산 DW/OLAP**: 수십 대의 노드에 데이터를 분산 셔플링(Shuffle Join)하거나 컬럼 지향 벡터 집계를 수행해야 하는 빅데이터 분석 워크로드 (➔ ClickHouse, DuckDB 권장).
- **글로벌 다중 지역 멀티 마스터 쓰기 (Multi-Master Active-Active)**: 분산 2PC(Two-Phase Commit)나 Paxos/Raft 기반 멀티 마스터 복제가 필수적인 글로벌 분산 DB 환경 (➔ CockroachDB, Spanner 권장).
- **무제한 대형 배치 분석 및 비정형 XML 조작**: XML DML/XQuery 중심의 레거시 엔터프라이즈 시스템.

---

## 2. 물리적 용량 및 스토리지 한도 (Capacity & Storage Limits)

| 항목 | 공식 한도 규격 | 관련 설정 및 에러 | 설명 및 아키텍처 제약 |
| :--- | :--- | :--- | :--- |
| **최대 데이터베이스 용량** | **16 TB** (WiredTiger 권장)<br>물리 RAM 한도 (In-Memory OCC) | OS 파일시스템 한도 | 64-bit B+Tree 기반. In-Memory 엔진은 RAM 용량 및 `-max-memory`에 의해 바운딩됨. |
| **테이블당 최대 행 수** | 사실상 무제한 ($2^{64}-1$) | - | 64-bit 정수 레코드 ID 및 B+Tree 인덱스 지원. |
| **최대 테이블 수** | 실질적 무제한 | - | 통합 카탈로그 메타데이터에 등록. 디스크 B+Tree 파일 핸들링 안정성 보장. |
| **테이블당 최대 인덱스 수** | **64개 권장** (기술적 무제한) | `ARCH-WT-FILE-LAYOUT-1` | 모든 보조 인덱스는 테이블당 단 1개의 물리 파일(`_idx_<Table>.wt`)에 멀티플렉싱되어 파일 디스크립터 고갈 없음. |
| **인덱스 키 최대 바이트** | **1,700 Bytes** | `54000` / Msg 1946<br>`ErrIndexKeyTooLarge` | B+Tree 리프 노드 팬아웃(Fan-out) 보장 및 성능 저하 방지. |
| **인덱스당 최대 컬럼 수** | **32개** | `54011` / Msg 1904<br>`ErrTooManyIndexColumns` | 복합 인덱스 컬럼 수 상한. |
| **물리 네임스페이스 분리** | **단일 스토리지 커널 원칙**<br>(물리적 다중 DB 분리 불가) | `ARCH-LOGICAL-NAMESPACE-1`<br>(Invariant 2) | 프로세스당 단일 스토리지 엔진 운용. `USE <dbname>`은 모델 B 논리 복합 키(`__db_<dbname>_<schema>_<table>`)로 가상 매핑. |

---

## 3. 컬럼, 레코드 및 식별자 한도 (Column, Row & Identifier Limits)

`[Invariant 13: Single Canonical V2 Binary Tuple Codec]` 및 SQL Server 물리 규격을 1:1 준수합니다.

| 항목 | 공식 한도 규격 | 강제 계층 | 초과 시 오류 코드 (SQLSTATE / Msg) |
| :--- | :--- | :--- | :--- |
| **테이블당 최대 컬럼 수** | **1,024개** | DDL 및 튜플 코덱 | `54011` / Msg 1702 (`ErrTooManyColumns`) |
| **인라인 고정 행 최대 크기** | **8,060 Bytes** | DDL (`CREATE`/`ALTER`) | `54000` / Msg 511 (`ErrInlineRowTooLarge`) |
| **단일 레코드 총 최대 크기** | **16 MB** | DML (`INSERT`/`UPDATE`) | `54000` / Msg 511 (`ErrRowTooLarge`) |
| **LOB 컬럼 단일 크기** | **16 MB** | 스토리지 인코더 | `VARCHAR(MAX)`, `NVARCHAR(MAX)`, `VARBINARY(MAX)`, `JSON` 포함. |
| **식별자 최대 길이** | **UTF-8 128자** | DDL 파서 및 카탈로그 | `42622` / Msg 103 (`ErrIdentifierTooLong`) |
| **컬럼 삭제(DROP COLUMN) 제약** | **꼬리(Tail) 컬럼만 즉시 허용**<br>(중간 컬럼 DROP Fail-Closed) | DDL 실행기 | `54000` / `ErrDropColumnNotTail` |

> [!IMPORTANT]
> **중간 컬럼 DROP 제한 및 운영 권장사항 (`Invariant 13`)**:
> 데이터가 존재하는 상태에서 테이블의 중간(Middle) 컬럼을 삭제하는 것은 온라인 OLTP 가용성을 위협하는 안티패턴입니다. LionSQL은 물리 테이블 재작성에 따른 락 지연이나 가상 스킵 마스킹에 의한 디코딩 성능 저하를 방지하기 위해 중간 컬럼 삭제를 Fail-Closed로 차단합니다. 중간 컬럼 제거가 필요할 경우 표준 프로덕션 패턴인 **Copy-and-Switch** (`CREATE TABLE new AS SELECT ...` 후 원자적 RENAME)를 권장합니다.

---

## 4. 네트워크 연결, 세션 및 자원 소비 제한 (Connections & Resource Quotas)

`[RULE:BoundedBuffers]` 원칙에 따라 무제한 메모리 누수와 고루틴 폭증을 원천 차단합니다.

| 항목 | 기본값 | 설정 플래그 / 범위 | 초과 시 동작 및 영향 |
| :--- | :--- | :--- | :--- |
| **최대 클라이언트 연결 수** | **200** (동시 연결) | `-max-connections` (최대 10,000) | 초과 시 연결 거부 및 SQLSTATE `53300` 반환. 프로세스 OOM 방지. |
| **동시 쿼리 워커 스레드** | **`NumCPU * 4`** | `-max-workers` | 고루틴 스케줄링 오버헤드 억제 및 CPU L1/L2 캐시 적중률 유지. |
| **스토리지 유휴 세션 풀** | **64개** | `-max-idle-sessions` | 풀 초과 유휴 세션은 즉시 닫아 스토리지 핸들 누수 방지. |
| **유휴 트랜잭션 타임아웃** | **5분 (`5m`)** | `-tx-idle-timeout` (스윕: 10초) | 유휴 방치된 트랜잭션 자동 롤백 (`25P03`). MVCC 스냅샷 점유 방지. |
| **전역 힙 메모리 버짓** | **물리 RAM의 65%** | `-max-memory` (예: `4096MB`) | 인메모리 테이블 및 실행기 힙 사용량 감시. 초과 시 안전 거부. |
| **연산자 작업 메모리** | **64 MB** | `-work-mem` | 해시 조인, 그룹핑, 서브쿼리 버킷팅 상한. |
| **인메모리 Undo 스택 상한** | **100개** | 고정 한도 | 트랜잭셔널 DDL 롤백 액션 상한 캡. |
| **서브쿼리 HashSet 구체화** | **10,000 행** | 고정 한도 (`SubqueryHashSet`) | 비상관 `IN` 서브쿼리 인메모리 해시 버퍼링 상한. 초과 시 스트리밍. |
| **세션당 Prepared Statement** | **256개** | LRU 축출 / 에러 | PGWire 및 TDS 세션 캐시 누수 방지. |

---

## 5. SQL 지원 제한 및 영구 기각(Spec-Out) 항목

LionSQL은 T-SQL과 PostgreSQL의 주요 DDL/DML을 폭넓게 지원하지만, 복잡성 억제를 위해 다음 구문들은 제한되거나 의도적으로 기각되었습니다 (`doc/SPEC_OUT_DECISIONS.ko.md`).

### 5.1 미지원 또는 제약이 있는 SQL 문법
- **`FULL OUTER JOIN`**:
  - 현재 `INNER JOIN`, `LEFT OUTER JOIN`, `RIGHT OUTER JOIN`, `CROSS JOIN`을 완벽 지원하나, `FULL OUTER JOIN`은 미지원입니다 (필요 시 `LEFT JOIN ... UNION ... RIGHT JOIN`으로 대체).
- **윈도우 함수 프레이밍 (`WINDOW FRAMING`)**:
  - `OVER (PARTITION BY ... ORDER BY ...)`는 지원되나, `ROWS BETWEEN N PRECEDING AND M FOLLOWING` 등 세부 프레이밍은 기본 누적/전체 범위로 평가됩니다. 동적 `RANGE` 오프셋은 제한됩니다.
- **`MERGE` 구문**:
  - T-SQL `MERGE` 문의 기본 형태는 지원되나, 서브쿼리가 포함된 복잡한 다중 `WHEN MATCHED AND ...` 분기는 제한적입니다 (개별 `UPDATE`/`INSERT` 분리 권장).
- **재귀 CTE (Recursive CTE)**:
  - 단일 레벨 공통 테이블 식(`WITH cte AS (...)`)은 지원되나, 자기 참조 재귀 CTE(`UNION ALL` 재귀 전개)는 스택 오버플로우 방지(`[RULE:BoundedRecursion]`)를 위해 깊이 제한이 적용됩니다.

### 5.2 영구 기각(Spec-Out)된 기능 목록
1. **PL/pgSQL 중복 인터프리터 기각**:
   - PostgreSQL 와이어로 접속하더라도 프로시저 엔진은 T-SQL VDBE 바이트코드 VM(`pkg/proc`)으로 단일화되어 실행됩니다. PL/pgSQL 고유 언어 구문은 탑재되지 않습니다.
2. **레거시 XML DML / XQuery 기각**:
   - 구형 XML 조작(`nodes()`, `modify()`)을 영구 기각하고 모던 JSON 함수(`JSON_VALUE`, `OPENJSON` 등)로 표준화하였습니다.
3. **In-Memory OCC 백그라운드 VACUUM 기각**:
   - OCC 워크스페이스 원자적 스왑/폐기 구조이므로 PostgreSQL 스타일의 백그라운드 튜플 진공 스레드가 필요하지 않으며 탑재되지 않습니다.
4. **분산 2PC 코디네이터 (XA / MS DTC) 기각**:
   - 네트워크 분산 2단계 커밋 오버헤드를 배제하고 단일 노드 수직 스케일업(Scale-Up First, Invariant 5)을 고수합니다.

---

## 6. 동시성 모델 및 트랜잭션 격리 제약 (Concurrency Constraints)

LionSQL은 고전적인 2PL(Two-Phase Locking) 기반의 무거운 읽기 공유 락을 완전히 폐기하고 **Non-Blocking Snapshot Isolation**을 채택했습니다 (`Invariant 3`).

### 6.1 읽기-쓰기 블로킹 제로 (Zero Read-Write Blocking)
- **읽기는 쓰기를 절대 블로킹하지 않으며, 쓰기는 읽기를 절대 블로킹하지 않습니다.**
- 트랜잭션 격리 수준은 항상 **Read Committed** 또는 **Snapshot Isolation**으로 격리됩니다.
- **`READ UNCOMMITTED` (Dirty Read) 영구 기각**: 오염된 미커밋 데이터를 읽는 모드는 데이터 무결성 보장을 위해 전면 금지되며, 요청 시 안전하게 Read Committed로 자동 상향 승격됩니다.

### 6.2 쓰기-쓰기 충돌 (Write-Write Conflict) 동작 차이
동일한 레코드를 서로 다른 트랜잭션이 동시에 갱신하려 할 때 엔진별 동작 방식에 차이가 있습니다:

- **In-Memory OCC (`compact`)**:
  - 낙관적 동시성 제어(OCC) 방식이므로 락을 걸지 않고 진행하다가 커밋 시점에 버전 충돌을 감지합니다.
  - 충돌 발생 시 대기하지 않고 **즉시 실패 롤백 (`ErrWriteConflict`)**합니다.
  - **애플리케이션 요구사항**: 클라이언트 라이브러리(ORM, API)에서 `ErrWriteConflict` 발생 시 즉시 재시도(Retry)하는 루프 패턴을 구현해야 합니다.
- **WiredTiger MVCC (`wiredtiger`)**:
  - 디스크 B+Tree 상에서 행 단위 래치와 트랜잭션 스냅샷을 통해 제어합니다.
  - 다른 트랜잭션이 이미 커밋되지 않은 수정을 가한 행에 접근하면 WiredTiger 레벨에서 충돌을 감지하고 `WT_ROLLBACK`을 반환합니다.
- **2PL 행 잠금 대기 (`WITH (UPDLOCK)`) 제약**:
  - 명시적 잠금 힌트는 인메모리 감지 수준으로 동작하며, 장시간 대기 큐(Lock-Wait Queue) 기반의 2단계 잠금 관리자는 v1.10.0 로드맵에 배정되어 있습니다.

---

## 7. 스토리지 엔진별 세부 제약 (Engine-Specific Constraints)

LionSQL은 환경에 따라 두 가지 스토리지 엔진을 제공하며, 각 엔진은 뚜렷한 제약 조건을 가집니다:

### 7.1 In-Memory OCC 엔진 (`-engine compact` / `memory`)
1. **메모리 의존성**:
   - 모든 인덱스와 테이블 데이터 전체가 물리 RAM에 100% 상주해야 합니다. RAM 용량이 소진되면 신규 쓰기가 실패합니다.
2. **복구 시간 (Recovery Time)**:
   - 장애 복구 시 스냅샷 RDB(`tables.json`)를 로드한 뒤 AOF 로그(`wal.aof`)를 순차 리플레이합니다. 데이터 규모가 수천만 건에 달할 경우 서버 기동 시간이 수 초에서 수십 초 소요될 수 있습니다.

### 7.2 WiredTiger MVCC 엔진 (`-engine wiredtiger`)
1. **Cgo 컴파일 의존성**:
   - macOS 및 Linux 환경에서는 Cgo 기반 컴파일이 필수적입니다.
   - Windows 환경에서는 단일 실행 파일(`.exe`) 실행을 위해 내장된 MSVC 네이티브 DLL이 `%LOCALAPPDATA%\LionSQL\bin\`으로 자동 자가추출(Self-Extracting)되는 과정을 거칩니다 (`doc/WIREDTIGER_WINDOWS_MSVC_DLL_CGO_GUIDE.ko.md`).
2. **`transaction_sync` 금지 불변식 (`Invariant 10`)**:
   - 트랜잭션마다 매번 디스크 플러시(`fsync`)를 동기식으로 호출하는 `transaction_sync`는 멀티코어 TPS를 96% 붕괴시키므로 커널에서 엄격히 금지됩니다.
   - 내구성은 Write-Ahead Log(`log=(enabled=true)`), WiredTiger 내부 그룹 커밋, 및 1초 주기의 백그라운드 동기화(`startPeriodicSync`)로 보장됩니다. (크래시 시 최대 1초 내 비동기 트랜잭션 손실 가능성 ➔ 엄격한 Zero-Loss가 필요한 경우 동기식 복제 구성 권장).

---

## 8. 알려진 미해결 과제 및 버전별 로드맵 (Known Issues & Roadmap)

LionSQL의 v0.94.0 및 1.0-GA 이후 단계적으로 해소될 알려진 호환성/기능 과제 현황입니다:

| 구분 | 과제 내용 | 현재 상태 및 회피책(Workaround) | 계획 마일스톤 |
| :---: | :--- | :--- | :---: |
| **PostgreSQL** | `expr::type` 캐스팅 전면 지원 | 일부 위치(WHERE/SELECT) 지원, `CAST(expr AS type)` 표준 구문 사용 권장 | `v1.10.0` |
| **PostgreSQL** | `SELECT DISTINCT ON (...)` 구문 | 미지원. `ROW_NUMBER() OVER (PARTITION BY ...)`로 우회 가능 | `v1.10.0` |
| **PostgreSQL** | `FILTER (WHERE ...)` 집계 절 | 미지원. `CASE WHEN ... THEN val END` 집계로 우회 가능 | `v1.10.0` |
| **PostgreSQL** | `ILIKE` 및 POSIX 정규식 연산자 (`~`, `!~`) | 미지원. `LOWER(col) LIKE LOWER(pattern)` 사용 권장 | `v1.10.0` |
| **PostgreSQL** | `CREATE SEQUENCE` 및 `SERIAL` | `INT IDENTITY(1,1)` T-SQL 표준 시퀀스로 완벽 대체 지원 | `v1.10.0` |
| **스토리지** | `ALTER COLUMN` 가변길이 온라인 확장 | 현재 새 테이블 생성 후 이관(Copy-and-Switch) 권장 | `v1.10.0` |
| **동시성** | `WITH (UPDLOCK)` 2PL 행 잠금 대기 매니저 | 현재 즉시 충돌 감지(Fast-Fail) 방식 동작 | `v1.10.0` |
| **성능** | 타입 타워 3중화 해소 (`ARCH-UNIFIED-TUPLE-PROTOCOL-1`) | `storage.Row` 포지셔널 슬라이스 직결 (100k TPS 목표) | `v1.10.0` |
| **엔터프라이즈** | Materialized View 가상 별칭 및 자동 리프레시 | Mattermost / NocoDB 대시보드 호환 계층 | `v1.20.0` |
| **AI / 검색** | Pure-Go Vector Search (`pgvector` 호환) | 임베딩 벡터 인덱싱 (IVFFlat/HNSW) 지원 예정 | `v1.60.0` |

---

## 9. 도입 결정 체크리스트 (Adoption Decision Checklist)

LionSQL 도입 검토 시 다음 질문을 체크하십시오:

- [ ] 단일 노드 수직 스케일업(Scale-Up)으로 초당 수만 건의 트랜잭션을 처리할 수 있는가? (Yes ➔ 적합)
- [ ] 데이터베이스가 분산 2PC(XA)나 크로스 리전 분산 합의를 요구하지 않는가? (Yes ➔ 적합)
- [ ] 테이블의 한 행 크기가 16MB를 초과하지 않고 인라인 고정 컬럼 합이 8,060 바이트 이내인가? (Yes ➔ 적합)
- [ ] `FULL OUTER JOIN` 대신 `LEFT/RIGHT JOIN`이나 유니온 결합을 허용할 수 있는가? (Yes ➔ 적합)
- [ ] In-Memory 엔진 도입 시 데이터 전체가 가용 RAM 범위 내에 들어오는가? (Yes ➔ 적합)
- [ ] 중간 컬럼 삭제 시 즉시 DDL 대신 Copy-and-Switch 마이그레이션 패턴을 적용할 수 있는가? (Yes ➔ 적합)

위 질문에 모두 부합한다면, LionSQL은 외부 의존성이 전혀 없는 **단일 바이너리의 압도적인 배포 편의성, 극강의 OLTP TPS, 그리고 T-SQL/PGWire 듀얼 프로토콜 상호운용성**을 통해 시스템 인프라 복잡성을 획기적으로 낮춰줄 것입니다.
