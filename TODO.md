# LionSQL Active Engineering Sprint & Task Dashboard (`TODO.md`)

> **SSOT (Single Source of Truth) 원칙 공지**:
> - LionSQL의 전체 180+건 정형 태스크 및 완료(`DONE`)된 버그 해결/리팩토링 이력은 **LionSQL 자체 DB(`sprint_tasks`)와 [`TASKS.md`](TASKS.md)**에 영구 보존됩니다.
> - 본 `TODO.md`는 **1.0 릴리스 활성 스프린트(Active Sprint)와 검증 게이트에만 집중하는 경량 실시간 대시보드**입니다.
> - CLI 관리: `./bin/sprint list`, `./bin/sprint set <ID> <STATUS>`, `./bin/sprint backup`
>
> **Core Architecture & Knowledge Base**:
> - Full Task & Bug Archive Mirror: [`TASKS.md`](TASKS.md)
> - Defect Resolution Archive (107 cases): [`notes/archive/BUG_ARCHIVE.ko.md`](notes/archive/BUG_ARCHIVE.ko.md)
> - Core Invariants & Engineering Context: [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md)
> - Strategic Roadmap: [`ROADMAP.md`](ROADMAP.md)
> - Post-1.0 Candidates Matrix: [`notes/POST_1.0_CANDIDATES.ko.md`](notes/POST_1.0_CANDIDATES.ko.md)

---

## 1. Active Sprint: 1.0 Release Implementation & Gates (1.0 구현 및 검증 게이트)

LionSQL 1.0 릴리스를 완성하기 위한 **3대 핵심 구현 과제**와 **신뢰성 검증 게이트**입니다.

### 1.1 1.0 핵심 구현 및 호환성 리팩토링 (Core 1.0 Features & Clean-up)
1. **`OCC-HYPER-2`: In-Memory OCC Pointer-Free Flat Record A/B 모드 및 신뢰성 검증 게이트** [진행 중]
   - **구현 현황**: 기본 모드(`OCCModeRaw`, 기존 104만 tpmC 오리지널) 100% 보존 + 신규 플랫 모드(`OCCModeFlat`) A/B 분기 구축 완료. 단일 행 기준 5.95배(1,017B ➔ 171B, 83.2% 절감), 10만 건 힙 메모리 67.5% 절감(68.9MB ➔ 22.3MB), 처리 속도 대등 실측 확인.
   - **기본값 정책**: **`OCCModeRaw` (기존 안정 버전) 디폴트 영구 유지 (`[RULE:SafeDefaults]`)**. 아래 4대 검증 게이트를 100% 완주하기 전까지 디폴트 변경 절대 금지.
   - **신규 플랫 모드(`OCCModeFlat`) 안정성 강화 및 필수 검증 게이트**:
     - [x] **`REFACTOR-OCC-FLAT-1` Flat 코덱 신뢰성 및 동시성 결함 보강**: `td.layout` 불변 사전초기화(Data Race 방지), `materialize` 중복 분기 일원화, 디코딩 실패 명시적 에러 전파 (`notes/FLAT_RECORD_RELIABILITY_PLAN.ko.md`)
     - [ ] **`TEST-OCC-FLAT-1` 크래시 복구 무결성**: Flat 모드에서 AOF/RDB 로그 기록 및 `kill -9` 비정상 종료 후 복구/리플레이 100% 정합성 검증
     - [ ] **`TEST-OCC-FLAT-2` 복합 DDL/DML 회귀**: 복합 보조 인덱스(Secondary/Unique Index), SkipList 범위 검색, 외래키(FK) 연동 시 Flat 디코딩 무결성 검증
     - [ ] **`TEST-OCC-FLAT-3` 쿼리 엔진 풀 스위트 회귀**: SQLLogicTest 및 T-SQL 프로시저(Babelfish 스위트)를 Flat 모드 환경에서 실행하여 0 결함 검증
     - [ ] **`TEST-OCC-FLAT-4` 24h 장기 소크 및 가변 길이 UPDATE 단편화 검증**: VARCHAR 갱신이 반복되는 고부하 환경에서 메모리 단편화 및 P99 지연시간 지터 0ms 수호 검증
   - **설계 문서**: [`notes/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/FLAT_RECORD_RELIABILITY_PLAN.ko.md) & [`notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)
2. **`WEB-SPA-1`: 단일 바이너리 내장 SPA 웹 서버 호스팅**
   - **목표**: `--app=<path>` 단일 스마트 플래그 (파일/디렉토리 자동 감지)로 DB와 프론트엔드 동시 서빙
   - **보안 불변식**: **Flat Directory Invariant** (하위 계층 `/`, `\` 및 도트(`.`) 시작 파일 원천 차단으로 Path Traversal 100% 봉쇄)
   - **설계 문서**: [`notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md)
3. **`PKG-OFFLINE-DOCS`: 배포 패키지 오프라인 브라우저 문서 번들링**
   - **목표**: 배포 아카이브(`tar.gz`/`zip`) 내 `docs/index.html` 번들링으로 폐쇄망/에어갭 100% 자립 지원
   - **설계 문서**: [`notes/OFFLINE_DOCS_PACKAGING_PLAN.ko.md`](notes/OFFLINE_DOCS_PACKAGING_PLAN.ko.md)
4. **`FIX-SQLUTIL-PARSER-1`: `pkg/sqlutil` SQL 파서/정규화기 결함 보강 및 렉서 공통화** [x]
   - **완료 내용**: `NOLOCK` 리터럴 파괴 방지, 세미콜론(`;`) 보존, `TOP PERCENT/WITH TIES/UNION` 오변환 차단, `ANY/IN/IS NULL` 다중바이트/배열 파싱 결함 수정 및 UTF-8 안전 상태머신 모듈화 완료. 14대 probe 테스트 전수 PASS.
   - **설계 문서**: [`notes/SQL_PARSER_HARDENING_PLAN.ko.md`](notes/SQL_PARSER_HARDENING_PLAN.ko.md)
5. **`GA-REGEX-FINAL-2`: CBO Planner 및 VDBE 잔여 정규식 100% 완전 제거** [x]
   - **완료 내용**: `planner.go` 거대 SELECT/JOIN 정규식 제거(결정론적 토크나이저 대체) 및 `vm.go` 쿼리 바인딩/LIKE 정규식 제거(`sqlutil.MatchLike` 통합)로 엔진 코어 Zero-Regex 달성 완료.
   - **설계 문서**: [`notes/ZERO_REGEX_MIGRATION_PLAN.ko.md`](notes/ZERO_REGEX_MIGRATION_PLAN.ko.md) (통합 마스터 플랜)
6. **`GA-REGEX-FINAL-3`: 쿼리 실행 런타임 LIKE 정규식 100% 완전 제거** [x]
   - **완료 내용**: `execution/filter.go`의 런타임 행 순회 `LIKE` 평가 시 정규식 컴파일을 `sqlutil.MatchLike`로 전면 교체하여 핫패스 GC 부하를 0으로 제거하고, `catalog/virtual/result.go`의 가상 카탈로그 `LIKE` 평가는 T-SQL 대괄호 이스케이프(`[_]`, `[%]`)를 보정하여 XORM 및 Collation 회귀 방어 완료.
   - **설계 문서**: [`notes/ZERO_REGEX_MIGRATION_PLAN.ko.md`](notes/ZERO_REGEX_MIGRATION_PLAN.ko.md) (통합 마스터 플랜)
7. **`FEAT-TOLERANT-DDL-1`: 동적 런타임 관용 DDL 제어 CLI (`get` & `set`) 및 PostgreSQL SQLSTATE 42704 정합성** [x]
   - **완료 내용**: 기본 엄격 모드(`strict`) 유지. 정적 옵션(Flag/Env/JSON) 전면 배제 및 오직 `./bin/lionsql set tolerant_ddl on/off` 단일 수단만 제공(재시작 시 자동 복귀). Wire 프로토콜에서 인덱스 부재 에러 시 PostgreSQL 표준 `42704` SQLSTATE 매핑 완료.
   - **설계 문서**: [`notes/TOLERANT_DDL_MODE_PLAN.ko.md`](notes/TOLERANT_DDL_MODE_PLAN.ko.md)
8. **`REFACTOR-CATALOG-INTERCEPTOR-1`: 가상 카탈로그 `interceptor.go` Table-Driven 라우터 전환** [x]
   - **완료 내용**: 330여 줄의 순서 의존적 ad-hoc IF 체인을 `RouteRule[]` 정적 디스패치 테이블로 전면 개편. 라우팅 순서 종속성 원천 차단 및 모든 외부 앱 회귀 테스트 통과.
   - **설계 문서**: [`notes/TOLERANT_DDL_MODE_PLAN.ko.md`](notes/TOLERANT_DDL_MODE_PLAN.ko.md)
9. **`SEC-PGWIRE-PARAM-1`: PGWire 달러 파라미터($N) 상태머신 토크나이저 전환 및 보안 강화** [x]
   - **완료 내용**: `pkg/wire/server.go`에서 `pgParamRe` 정규식을 전면 제거하고 `"regexp"` import를 완전 삭제. 작은따옴표(`'...'`), 큰따옴표(`"..."`), 달러 인용(`$$...$$`, `$tag$...$tag$`) 및 주석(`--`, `/* */`)을 완벽히 보존하는 `sqlutil.ReplacePgParams`, `TranslatePgParamsToAtParams`, `MaxPgParamIndex`를 개발하여 쿼리 변조 및 DoS 원천 차단 완료.
   - **설계 문서**: [`notes/PGWIRE_PARAM_TRANSLATION_PLAN.ko.md`](notes/PGWIRE_PARAM_TRANSLATION_PLAN.ko.md)

### 1.2 1.0 신뢰성 검증 게이트 (Release Gates)
1. **`DURA-KILL-1`: `kill -9` 크래시 복구 장기 라운드 검증**
   - 상태: 진행 중 (100라운드 100% 무손실 복구 통과 완료, 릴리스 직전 500 ~ 1,000 라운드 최종 확인)
   - 설계 문서: [`notes/DURABILITY_VERIFICATION_PLAN.ko.md`](notes/DURABILITY_VERIFICATION_PLAN.ko.md)
2. **`DURA-SOAK-1`: 24h ~ 72h 장기 무중단 소크 테스트**
   - 상태: 진행 중 (단기 연속 부하 통과, 24시간 ~ 72시간 연속 가동으로 자원 유계 입증)
   - 설계 문서: [`notes/DURABILITY_VERIFICATION_PLAN.ko.md`](notes/DURABILITY_VERIFICATION_PLAN.ko.md)
3. **`DURA-SLT-2`: SQLLogicTest 1.0 풀 스위트 듀얼 엔진 최종 인증**
   - 상태: 107개 스위트 55만 쿼리 100% PASS 입증 완료 (`scripts/verify_0.90.sh`)
   - 설계 문서: [`notes/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md`](notes/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md)
4. **`GA-VALID-3`: 연동 앱 섀도 모드 실운영 도그푸딩**
   - 이미 100% 연동 통과된 실전 서비스(Vaultwarden, Umami, Answer 중 1~2종) 1~2주간 실운영 백그라운드 구동
5. **`TUNE-DOC-SYNC-1`: 벤치마크 및 공식 문서 실측치 일괄 동기화**
   - 1.0 GA 릴리스 직전 모든 최적화 실측치를 `doc/BENCHMARK.md` 및 `README.md`에 일괄 반영

### 1.3 [테스트베드] 타임박스 외부 앱 호환성 프로브 (Time-Boxed Probe Protocol)
> **운영 원칙**: 완벽 연동을 위해 무한정 시간을 쓰지 않고, **엄격한 30분 타임박스**를 두고 가볍게 찔러보며 엔진의 숨은 엣지 케이스를 발굴하는 테스트베드로만 활용 (비침습적 패치 우선, 무한 루프나 구조적 대수술 감지 시 30분 내 즉시 중단).
- `INTEG-DIRECTUS-1`: Directus (30분 프로브 완료 ➔ PENDING 정리: 92개 마이그레이션 DDL 통과 확인, 이후 `@directus/schema` 인스펙터의 `information_schema.columns` LEFT JOIN `information_schema.tables` 및 `relnamespace IN ('public'::regnamespace)` 복합 쿼리 체인 식별 완료)
- `INTEG-NOCODB-1`: NocoDB (30분 프로브 완료 ➔ PENDING 정리: 100+개 시스템 테이블 DDL 및 다중 마이그레이션 통과 확인, 이후 `nc_202610121136_file_references_url_index` 마이그레이션의 `DROP INDEX "nc_fr_file_url"` 비정규 인덱스 삭제 시 존재하지 않는 인덱스 예외 식별. **공식 해결 아키텍처 확정**: 코어 무결성을 훼손하지 않고 외부 앱 부트스트랩을 무음 통과시키는 **"설치 호환 모드 플래그(`--tolerant-ddl`)"** 도입 결정)
- `INTEG-MATTERMOST-1`: Mattermost (30분 프로브 완료 ➔ PENDING 정리: `SHOW server_version_num` 및 `ALTER TABLE tbl SET (...)` 스토리지 옵션 관용 통과 확인, 이후 `CREATE MATERIALIZED VIEW IF NOT EXISTS poststats` 마테리얼라이즈드 뷰 DDL 및 후속 `REFRESH` 수명주기 요구 식별)
- `INTEG-GHOST-1`: Ghost (10분 프로브 완료 ➔ PENDING 정리: 공식 릴리스 v5.x는 내부 드라이버로 `mysql2`와 `sqlite3`만 패키징하고 `pg` 드라이버 번들링이 영구 제거됨 확인. LionSQL의 PGWire/TDS 리스너와 직접 호환 불가하여 클라우드 백로그로 보존)

---

## 2. Post-1.0 Candidates & Deep Freezer (1.0 이후 후보 및 냉동 보관)

### 2.1 클라우드 유틸리티 및 엔터프라이즈 백로그
1. **`FEAT-MATVIEW-ALIAS-1` Materialized View Virtual Alias & Tolerant Refresh (`v1.1.0`)**
   - **목표**: Mattermost, Discourse 등 엔터프라이즈 앱 부트스트랩을 위해 `CREATE MATERIALIZED VIEW`를 가상 뷰(Virtual View, `relkind='m'`)로 인라인 치환하고 `REFRESH MATERIALIZED VIEW`를 Tolerant No-Op(성공) 처리하여 수천 줄의 복잡도와 락 위험을 99% 절감.
   - **설계 문서**: [`notes/MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md`](notes/MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md)
2. **`S3-1~4` 클라이언트 사이드 S3 Pre-signed URL 및 SigV4 서명기** ([`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md))
3. **`AUDIT-LOG-1` 암호화 SHA-256 체이닝 불변 감사 로그 서브시스템** ([`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md))

### 2.2 고도화 스토리지 & AI/빅데이터 백로그 (Storage, AI & Replication)
1. **`FEAT-VECTOR-1` 순수 Go 기반 Vector 임베딩 및 유사도 검색 (`v1.1.0`)**
   - **목표**: `VECTOR(dim)` 타입, L2/Cosine/Dot 거리 연산자(`<->`, `<=>`), 멀티코어 병렬 Flat KNN 스캔 (LangChain/pgvector 호환).
   - **설계 문서**: [`notes/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`](notes/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md)
2. **`FEAT-TIMESERIES-LSM-1` LSM 기반 초고속 Time Series 파티셔닝 & 불변 청크 스토리지 (`v1.2.0`)**
   - **목표**: DDL `WITH (TYPE='TIMESERIES')`, WiredTiger 내장 `lsm:` 핸들러(경로 A) 및 순수 Go 일별 불변 청크 플러시(경로 B) 하이브리드 결합, `time_bucket()`, $O(1)$ 청크 드롭.
   - **설계 문서**: [`notes/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`](notes/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md)
3. **`REPL-STREAM-BIN-1` Replication 네트워크 Length-Prefixed Binary Frame Zero-Copy 스트리밍**
   - **목표**: 복제 스트림 프로토콜을 기존 JSON-lines에서 AOF와 동일한 4바이트 길이 접두사 바이너리 프레임(`[4B length][MsgPack/Raw]`)으로 전환하여 Base64 인코딩 및 JSON 파싱 오버헤드를 100% 제거하고 네트워크 Zero-Copy 달성.
   - **난이도/영향도**: 낮음~중간(★☆☆~★★☆) / 국소 (네트워크 송수신 레이어 한정)
4. **`WAL-DELTA-1` Partial / Delta WAL 로깅 (UPDATE 변경분 컬럼 차분 기록)**
   - **목표**: 대규모 컬럼 테이블에서 `UPDATE` 발생 시 전체 튜플을 재직렬화하지 않고, 변경된 컬럼만 Dirty 비트마스킹(`[DirtyMask][ColIdx, Val]`)하여 AOF 디스크 I/O와 복제 전송량을 최소화.
   - **난이도/영향도**: 중간~높음(★★☆~★★★) / 엔진 내부 (트랜잭션 변경 감지, Delta 코덱, 리플레이 시 Read-Modify-Write 및 보조 인덱스 정합성 유지 필요)

### 2.3 초장기 냉동 보관 후보 (Deep Freezer Backlog)
> 구현 난이도(★★★~★★★★)와 시스템 파급 위험도가 극히 높고 실무 효익 대비 오버헤드가 크므로, 특수 목적의 극단적 대규모 요구 전까지 **초장기 냉동 보관(Deep Freeze)**합니다.
1. **`BENCH-TPCH-1` Mini TPC-H Suite & CBO 서브쿼리 Decorrelation**
2. **`PERF-COMPLEX-QUERY-2` Complex Query Flat Hash Join & Direct Slot Projection**
3. **`LOC-1~4` MSSQL Collation 및 유니코드 다국어 로케일 정렬/비교**

### 2.4 영구 기각 (Spec-Out Invariants)
- **`FRIENDLY-SQL`**: DuckDB 스타일 문법 (`* EXCLUDE`, `FROM tbl;` 등) ➔ 오타 침묵 및 RDBMS 엄격성 훼손으로 기각
- **`PERF-SERVER-PIPELINE-P3-P4`**: ReadOnly 자동 바이패스 ➔ 겉형식과 실질 불일치 및 격리성 파괴 위험으로 기각

---

## 3. Completed Milestones & Historical SSOT Link

- **전체 완료 태스크 및 버그 아카이브 (180+건)**: [`TASKS.md`](TASKS.md) (LionSQL DB 자동 동기화 미러)
- **10대 실전 오픈소스 앱 연동 완료**: Answer, Vaultwarden, Metabase, Umami, Vikunja, Woodpecker CI, Gitea, Ghost, Casdoor, Affine
- **VDBE VM 5대 혁신 (`PERF-VDBE-EXTREME-OPT-1`)**: 슬롯 배열 직접 인덱싱, DML 풀링, 평탄화 루프 전환 완료 (상시 84,766 TPS 통과)
- **정규식 제로 불변식 (`Invariant 7`)**: 엔진 및 와이어 프로덕션 경로 정규식 100% 박멸 완료
