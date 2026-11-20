# E2E 리그레션 스위트 보강 계획서 (9개 엔터프라이즈 서비스 + DBeaver/Beekeeper 스튜디오)

> **문서 코드**: `notes/E2E_SERVICE_REGRESSION_SUITE_PLAN.ko.md`  
> **관련 규칙**: `[RULE:PlanPersistence]`, `[RULE:PipelineVerification]`, `[RULE:DeterministicTesting]`, `[RULE:HermeticParity]`  
> **태스크 ID**: `TEST-E2E-SERVICES-1`  
> **작성 일자**: 2026-11-20  

---

## 1. 배경 및 문제 의식

LionSQL은 외부 엔터프라이즈 서비스 9개(Gitea, Metabase, Wiki.js, Vaultwarden, Casdoor, GoCD, Umami, Woodpecker, Vikunja)와의 연동을 위해 DDL/DML 및 가상 카탈로그 호환성을 지속적으로 확장해 왔습니다.
그러나 현재의 테스트 체계는 다음과 같은 한계가 존재합니다:

1. **In-Memory `memengine` 중심 편향**:
   - 현재 `pkg/wire/service_compat_wire_test.go`의 9개 서비스 테스트는 거의 전적으로 `memengine`(단일 메모리) 위에서만 구동됩니다.
   - 실제 상용 환경에서 핵심인 **WiredTiger MVCC 디스크 B+Tree 엔진** 위에서의 연속 트랜잭션, 복합 인덱스, 롱런 DDL/DML 동작 검증이 분리되어 있지 않습니다.
2. **단편적 쿼리 스니펫 vs. 실제 마이그레이션 라이프사이클 갭**:
   - 테이블 1~2개 생성 및 샘플 INSERT 수준의 단위 모킹에 머물러 있어, 실제 프레임워크가 부팅될 때 쏟아내는 **다단계 스키마 마이그레이션(Liquibase, GORM, Knex, XORM, SeaORM, Prisma)** 전체 파이프라인의 회귀를 원천 차단하기 어렵습니다.
3. **GUI 클라이언트(DBeaver, Beekeeper Studio)의 WiredTiger 동시성 검증 부족**:
   - GUI 도구가 날리는 복잡한 시스템 카탈로그 조인 및 커서 기반 데이터 페이징이 WiredTiger 스토리지 위에서 멀티 세션 동시 요청으로 들어왔을 때의 안정성(Hazard Pointer, Cursor Leak, Deadlock)을 hermetic하게 검증하는 전용 E2E 스위트가 부족합니다.

---

## 2. 목표 및 핵심 요구사항 (Deliverables)

1. **`test/e2e/services/` 전용 E2E 패키지 구축**:
   - 9개 엔터프라이즈 서비스의 실제 스키마 마이그레이션 및 실무 CRUD 워크플로우를 담은 독립 E2E 테스트 스위트 작성.
2. **Dual-Storage Engine 매트릭스 실행**:
   - 동일한 서비스 마이그레이션 코퍼스를 **In-Memory OCC**와 **WiredTiger MVCC** 양쪽 모두에서 100% Hermetic하게 교차 검증.
3. **Dual-Wire Protocol 검증**:
   - 서비스 성격에 맞게 **PGWire (`:5433`)** 및 **TDS (`:1433`)** 프로토콜 드라이버(`lib/pq`, `microsoft/go-mssqldb`)를 통한 실제 TCP 네트워크 통신 검증.
4. **DBeaver & Beekeeper 동시성 인트로스펙션 스트레스 스위트 (`test/e2e/tools/`)**:
   - 수십 개 세션이 동시에 `pg_class`, `information_schema`, `sys.tables` 등을 쿼리하면서 테이블을 읽고 쓰는 실전 GUI 사용 환경 시뮬레이션.

---

## 3. 대상 서비스 9종 및 스키마/엔진 특성 매트릭스

| 번호 | 서비스명 | ORM / 마이그레이션 도구 | 주요 검증 영역 | 대상 프로토콜 |
| :---: | :--- | :--- | :--- | :--- |
| 1 | **Gitea** | XORM | 다중 복합 인덱스, Git 커밋/이슈/PR 트랜잭션, `ALTER TABLE ADD COLUMN` | PGWire, TDS |
| 2 | **Metabase** | Toucan / Clojure JDBC | 복잡한 시스템 뷰 탐색, 서브쿼리 조인, 임의 애드혹 분석 쿼리 | PGWire, TDS |
| 3 | **Wiki.js** | Knex.js | 트리형 문서 페이지 계층, JSON 저장 컬럼, Full-Text 연동 | PGWire, TDS |
| 4 | **Vaultwarden** | Diesel / SeaORM | 암호화 BLOB/LOB 저장, 고빈도 동시 세션 동기화, 트랜잭션 롤백 | PGWire |
| 5 | **Casdoor** | XORM | 사용자 인증/인가, OAuth 토큰, 계층형 조직/역할 퍼미션 모델 | PGWire, TDS |
| 6 | **GoCD** | Liquibase | `DATABASECHANGELOGLOCK` 잠금 획득, 파이프라인 스테이지 히스토리 | PGWire, TDS |
| 7 | **Umami** | Prisma | 대용량 페이지뷰/이벤트 수집, 시계열 집계, 세션 식별자 매핑 | PGWire |
| 8 | **Woodpecker** | XORM | CI/CD 파이프라인 빌드 로그, 스텝별 상태 머신, 워커 풀 동시성 | PGWire |
| 9 | **Vikunja** | XORM | 칸반 보드, 중첩 프로젝트/태스크 의존성, 캘린더 반복 일정 | PGWire |
| + | **DBeaver** | JDBC Metatada | `pg_catalog`, `information_schema`, `sys.*`, 대량 페이징 그리드 | PGWire, TDS |
| + | **Beekeeper** | Native Node Client | 스키마 트리 확장, 인덱스/외래키 인트로스펙션, 인라인 튜플 편집 | PGWire |

---

## 4. 단계별 구현 로드맵 (4-Phase Execution)

### Phase 1: E2E 하네스 인프라 구축 (`test/e2e/harness/`)
- 임의의 포트(`127.0.0.1:0`)에 엔진(MemEngine 또는 WiredTiger 임시 디렉토리)을 올리고, PGWire/TDS 리스너를 바인딩하여 표준 `*sql.DB` 풀을 반환하는 재사용 하네스 구축.
- 테스트 종료 시 소켓 닫기, 임시 디렉토리 정리(`os.RemoveAll`), 고루틴 누수 방지 원자적 클린업 보장 (`[RULE:CleanTeardown]`).

### Phase 2: 9대 엔터프라이즈 서비스 마이그레이션 & CRUD 시나리오 구현 (`test/e2e/services/`)
- 각 서비스의 핵심 DDL 스키마 전체(10~30개 테이블)와 복합 인덱스 생성.
- 엔티티 등록 ➔ 관계 매핑 ➔ 트랜잭션 갱신 ➔ 일괄 조회의 표준 비즈니스 라이프사이클 실행.
- MemEngine 및 WiredTiger 양쪽에서 서브테스트(`t.Run("Engine=MemEngine", ...)`, `t.Run("Engine=WiredTiger", ...)`)로 구동.

### Phase 3: DBeaver & Beekeeper 동시성 인트로스펙션 스위트 구현 (`test/e2e/tools/`)
- 10개 워커가 동시에 연결하여 DBeaver 메타데이터 탐색 쿼리(카탈로그 조인)와 Beekeeper 스키마 검색을 수행하는 동안, 백그라운드에서 테이블 INSERT/UPDATE가 일어나는 복합 스트레스 테스트.
- WiredTiger 스토리지 위에서 트랜잭션 격리와 메타데이터 락 경합 안전성 검증.

### Phase 4: CI 파이프라인 연동 및 3-Stage 검증
- `go test -v -race ./test/e2e/...` 실행 및 0 데이터 레이스 달성 (`[RULE:PipelineVerification]`).
- 태스크 DB(`sprint`) 상태 업데이트 및 백로그 동기화.

---

## 5. 위험 요소 및 방어 대책
1. **WiredTiger CGo 링크 및 디렉토리 I/O 비용**:
   - 여러 테스트가 동시에 디스크 디렉토리를 열 때 충돌 방지를 위해 `t.TempDir()`을 사용하여 완전히 격리된 물리 경로 제공 (`[RULE:PathSecurity]`).
2. **테스트 시간 장기화 (Flakiness 방지)**:
   - 외부 실제 도커 컨테이너를 띄우는 무거운 방식 대신, 프레임워크가 발행하는 정밀 SQL 코퍼스를 순수 Go 드라이버(`lib/pq`, `go-mssqldb`)로 직결 송수신하여 5~10초 내 100% 완주하도록 경량화 (`[RULE:DeterministicTesting]`).
