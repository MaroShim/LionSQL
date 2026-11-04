# SPRINT TASKS BACKUP (LionSQL SSOT Mirror)

> - **Last Synced**: 2026-11-04 18:23:58
> - **Primary Source of Truth (SSOT)**: LionSQL `sprint_tasks` Table
> - **Backup File**: Auto-synced on task update & Git commit

| Status | ID | Domain | Priority | Title | Last Updated |
| :---: | :---: | :---: | :---: | :--- | :---: |
| `PENDING` | **`BENCH-TPCH-1`** | Ecosystem | P1 | Mini TPC-H Benchmark Suite & CBO 서브쿼리 Decorrelation | 2026-11-04T09:19:53.623195Z |
| `PENDING` | **`COMPAT-DECLARATIVE-1`** | Durability | P1 | 외부 앱 호환성 규칙 선언적 외재화 (Declarative Compatibility Engine) | 2026-11-04T09:19:53.550783Z |
| `PENDING` | **`FRIENDLY-SQL`** | Ecosystem | P1 | DuckDB-Style Friendly SQL 문법 지원 | 2026-11-04T09:19:53.654353Z |
| `PENDING` | **`INTEG-MATTERMOST-1`** | Ecosystem | P1 | Mattermost (팀 협업 채팅) | 2026-11-04T09:19:53.736138Z |
| `PENDING` | **`INTEG-NOCODB-1`** | Ecosystem | P1 | NocoDB (노코드 DB 스튜디오) | 2026-11-04T09:19:53.726863Z |
| `PENDING` | **`OCC-HYPER-2`** | Ecosystem | P1 | In-Memory OCC Pointer-Free Flat Record & Memory Compaction | 2026-11-04T09:19:53.63356Z |
| `PENDING` | **`PERF-COMPLEX-QUERY-2`** | Ecosystem | P1 | Complex Query Flat Hash Join & Direct Slot Projection | 2026-11-04T09:19:53.613195Z |
| `PENDING` | **`PERF-SERVER-PIPELINE-P3-P4`** | Ecosystem | P1 | 서버 파이프라인 Phase 3/4 | 2026-11-04T09:19:53.644515Z |
| `PENDING` | **`PKG-OFFLINE-DOCS`** | Ecosystem | P1 | 배포 패키지 오프라인 문서 번들링 (docs/index.html) | 2026-11-04T09:19:53.663047Z |
| `PENDING` | **`WEB-SPA-1`** | Ecosystem | P1 | 단일 바이너리 내장 SPA 웹 서버 호스팅 | 2026-11-04T09:19:53.673346Z |
| `PENDING` | **`BUG-TEST`** | Test | P2 | test title | 2026-11-04T09:23:47.980864Z |
| `PENDING` | **`GA-VALID-3`** | Durability | P2 | 연동 앱 2~3종 1~2주 도그푸딩 | 2026-11-04T09:19:53.310473Z |
| `IN_PROGRESS` | **`DOGFOOD-SPRINT-1`** | Performance | P1 | : LionSQL 자체 백엔드 기반 정형 스프린트/태스크 관리 시스템 구축 (Dogfooding) | 2026-11-04T09:19:52.979139Z |
| `IN_PROGRESS` | **`DURA-KILL-1`** | Durability | P1 | kill -9 크래시 복구 장기 라운드 검증 (진행 중) | 2026-11-04T09:19:53.043997Z |
| `IN_PROGRESS` | **`DURA-SLT-2`** | Durability | P1 | SQLLogicTest 1.0 릴리즈 풀 스위트 (60,000+ 쿼리) 전수 인증 (진행 중) | 2026-11-04T09:19:53.217104Z |
| `IN_PROGRESS` | **`DURA-SOAK-1`** | Durability | P1 | 24h ~ 72h 장기 소크 테스트 (진행 중) | 2026-11-04T09:19:53.06141Z |
| `IN_PROGRESS` | **`PERF-VDBE-EXTREME-OPT-1`** | Durability | P1 | T-SQL VDBE 바이트코드 VM 극강의 성능 쥐어짜기 5대 혁신 (진행 중) | 2026-11-04T09:19:53.182826Z |
| `IN_PROGRESS` | **`GA-VALID-2`** | Durability | P2 | cmd/soaktest 연속 부하(OCC 19,613 tx / WiredTiger 19,678 tx, RSS/FD/고루틴 1.00 무누수 PASS) 및 cmd/crashtest 100라운드 확장 실행 완료 (WiredTiger 100/100 PASS 344,129 tx, OCC 100/100 PASS 301,184 tx; 24h/72h 초장기 소크 대기) | 2026-11-04T09:19:53.301877Z |
| `DONE` | **`ARCH-VDBE-ROUTING-1`** | Ecosystem | P1 | AST 및 카탈로그 기반 2-Tier 쿼리 라우팅 정규화 (Anti-Heuristic Refactoring) | 2026-11-04T09:19:53.716835Z |
| `DONE` | **`BENCH-ALL-IN-ONE-1`** | Durability | P1 | 원클릭 벤치마크 및 종합 검증 스위트 (scripts/run_all_benchmarks.sh) 구축 | 2026-11-04T09:19:53.130354Z |
| `DONE` | **`COMPAT-TABLE-DRIVEN-1`** | Durability | P1 | 10개 실전 서비스 호환 규칙 Table-Driven 구조 및 안티패턴 키워드 매칭 전면 정식화 | 2026-11-04T09:19:53.542464Z |
| `DONE` | **`DURA-SLT-1`** | Durability | P1 | SQLLogicTest (SLT) 블랙박스 정합성 오라클 검증 | 2026-11-04T09:19:53.200012Z |
| `DONE` | **`INTEG-ANSWER-1`** | Durability | P1 | Apache Answer (Q&A 지식베이스) | 2026-11-04T09:19:53.406838Z |
| `DONE` | **`INTEG-METABASE-1`** | Durability | P1 | Metabase (BI 대시보드) | 2026-11-04T09:19:53.471105Z |
| `DONE` | **`INTEG-NOCODB-MATTERMOST-DEFER-1`** | Durability | P1 | NocoDB 및 Mattermost 연동 1.0 이후(v1.1.0) 연기 확정 | 2026-11-04T09:19:53.569546Z |
| `DONE` | **`INTEG-UMAMI-1`** | Durability | P1 | Umami (웹 애널리틱스) | 2026-11-04T09:19:53.48866Z |
| `DONE` | **`INTEG-VAULTWARDEN-1`** | Durability | P1 | Vaultwarden (비밀번호 관리) | 2026-11-04T09:19:53.434612Z |
| `DONE` | **`INTEG-VIKUNJA-1`** | Durability | P1 | Vikunja (프로젝트 칸반 & To-Do) | 2026-11-04T09:19:53.524577Z |
| `DONE` | **`INTEG-WOODPECKER-1`** | Durability | P1 | Woodpecker CI (CI/CD 엔진) | 2026-11-04T09:19:53.506566Z |
| `DONE` | **`OPT-LARGE-JOIN-1`** | Durability | P1 | 대규모 조인(10+ Tables) 최적화 | 2026-11-04T09:19:53.233003Z |
| `DONE` | **`P-2`** | Performance | P1 | WiredTiger VDBE 바이너리 튜플 지연 역직렬화 (완료) | 2026-11-04T09:19:53.004675Z |
| `DONE` | **`PERF-CTE-EPHEMERAL-1`** | Durability | P1 | 세션 로컬 에페머럴 CTE 버퍼 및 Zero-DDL 최적화 | 2026-11-04T09:19:53.080063Z |
| `DONE` | **`PERF-CTE-LOWERING-VDBE-1`** | Durability | P1 | Writable CTE Stored Procedure 바이트코드 Lowering 가속 | 2026-11-04T09:19:53.164198Z |
| `DONE` | **`PERF-GROVE-CONCURRENCY-1`** | Durability | P1 | Grove 벤치마크 기반 동시성 최적화 및 핫스팟 락 완성 | 2026-11-04T09:19:53.147666Z |
| `DONE` | **`PERF-REGRESSION-RECOVERY-1`** | Performance | P1 | 성능 회귀 정상화 및 회귀 방지 게이트 구축 (완료) | 2026-11-04T09:19:53.025114Z |
| `DONE` | **`PLANNER-SLIM-1`** | Durability | P1 | & PLANNER-AST-1~3 플래너·엔진 정규식 완전 제거 및 순수 AST 전환 | 2026-11-04T09:19:53.249745Z |
| `DONE` | **`R-6`** | Ecosystem | P1 | 세션 캡슐화 및 모듈 디커플링 (Lean 3단계 리팩토링) | 2026-11-04T09:19:53.695826Z |
| `DONE` | **`ROUTING-AST-CATALOG-1`** | Durability | P1 | 심볼 테이블 & AST 기반 쿼리 라우팅 리팩토링 | 2026-11-04T09:19:53.453643Z |
| `DONE` | **`SQL-GOOSE-IDENTITY-1`** | Durability | P1 | PostgreSQL GENERATED { ALWAYS | BY DEFAULT } AS IDENTITY 지원 및 goose 마이그레이션 호환 | 2026-11-04T09:19:53.604254Z |
| `DONE` | **`SQL-WRITABLE-CTE-1`** | Durability | P1 | PostgreSQL Writable CTE (WITH ... INSERT/UPDATE/DELETE ... RETURNING) 및 ::type[] 캐스트 지원 | 2026-11-04T09:19:53.587268Z |
| `DONE` | **`TUNE-HOTSPOT-OCC-1`** | Durability | P1 | 고동시성 핫스팟 OCC 경합 완화 및 와이어 버퍼 풀링 | 2026-11-04T09:19:53.113236Z |
| `DONE` | **`TUNE-SP-FASTPATH-1`** | Durability | P1 | TPC-C Stored Procedure Fast-Path 직결 및 고동시성 In-Memory OCC 튜닝 | 2026-11-04T09:19:53.096899Z |
| `DONE` | **`ARCH-RULE-SLUG`** | Ecosystem | P2 | 골든 룰 불변 슬러그([RULE:xxx]) 및 캐노니컬 명칭 체계(Rule Reference Policy) 수립, 전체 소스코드 주석 및 문서 내 휘발성 순번 완전 제거 | 2026-11-04T09:19:54.006602Z |
| `DONE` | **`BENCH-SSB-1`** | Ecosystem | P2 | Star Schema Benchmark (SSB) 스위트 CLI 도구 완성 | 2026-11-04T09:19:53.911041Z |
| `DONE` | **`BUG-ADD-COL-NOT-NULL-1`** | 기본값 없는 NOT NULL 컬럼 추가 시 NULL 오염 방지 (Add Column Null Bug) | P2 | 데이터 존재 테이블에 NOT NULL 컬럼 추가 시 기존 행을 NULL로 채워 무결성을 파괴하는 결함 방지 | 2026-11-04T09:23:33.981674Z |
| `DONE` | **`BUG-AGG-MIXED-NUM-DEC-1`** | AggregateIterator Decimal-정수 혼합 합계 유실 방지 (Agg Mixed Decimal Bug) | P2 | 그룹 내 Decimal 출현 시 비-Decimal 수치 Decimal 변환 및 합산으로 수치 누락 방지 | 2026-11-04T09:23:34.77767Z |
| `DONE` | **`BUG-API-CREATE-DEFAULT-1`** | HTTP API 레코드 생성 시 NOT NULL DEFAULT 필드 주입 (API Create Default Bug) | P2 | 생략된 필드 대상 스키마 기본값(리터럴 및 함수 기본값) 자동 주입으로 400 에러 해결 | 2026-11-04T09:23:35.21205Z |
| `DONE` | **`BUG-API-JSON-DATETIME-EMPTY-STR-1`** | HTTP API 레코드 조회 시 DATETIME 빈 문자열 직렬화 방지 (API JSON DateTime Bug) | P2 | recordToJSONMap에서 v.SVal 대신 v.String() / v.TVal 정규 포맷팅으로 일시 데이터 유실 해결 | 2026-11-04T09:23:35.948263Z |
| `DONE` | **`BUG-API-JSON-DECIMAL-PRECISION-LOSS-1`** | HTTP API 레코드 직렬화 시 DECIMAL 실수 부동소수점 정밀도 손실 방지 (API Decimal Loss Bug) | P2 | strconv.ParseFloat 변환을 제거하고 정밀 문자열 또는 json.Number로 직렬화하여 금융 데이터 왜곡 방지 | 2026-11-04T09:23:35.97656Z |
| `DONE` | **`BUG-API-PK-GEN-1`** | HTTP API 레코드 밀리초 PK 충돌 및 UUID 누락 해결 (API PK Gen Bug) | P2 | 나노초/카운터 결합 유니크 채번 및 UUID PK 자동 생성 지원으로 PK 충돌 및 NULL 방지 | 2026-11-04T09:23:34.496428Z |
| `DONE` | **`BUG-APPLY-REGEX-1`** | CROSS/OUTER APPLY 정규식 및 맵 순회 왜곡 방지 (Apply Regex Bug) | P2 | buildParameterizedApplyQuery 단순 정규식 치환 및 무작위 맵 순회로 키워드/리터럴 오염 방지 | 2026-11-04T09:23:33.936959Z |
| `DONE` | **`BUG-ARITH-1`** | 수식 0 초기화 방지 (Arithmetic Bug) | P2 | evalUpdateExpr 수식 평가 시 컬럼명 대소문자 무관 탐색으로 컬럼 0 리셋 방지 | 2026-11-04T09:23:33.381142Z |
| `DONE` | **`BUG-ARITH-EXPR-ZERO-1`** | UPDATE 산술식 유연성 및 0 리셋 방지 (Arith Expr Zero Bug) | P2 | [Price] + 10, a + b, 10 + a 등 수식 매칭 실패로 인한 컬럼 0 리셋 방지 | 2026-11-04T09:23:34.721197Z |
| `DONE` | **`BUG-AUTH-ADMIN-PW-BYPASS-1`** | 내장 슈퍼유저 비밀번호 인증 완전 우회 백도어 해결 (Auth Admin PW Bypass Bug) | P2 | PGWire 및 TDS에서 비밀번호가 설정된 sa, postgres, admin의 무암호 로그인 우회 차단 | 2026-11-04T09:23:34.986118Z |
| `DONE` | **`BUG-AUTH-RBAC-ROLE-GRANT-IGNORE-1`** | RBAC 사용자 커스텀 롤 테이블 권한 전수 누락 방지 (RBAC Role Grant Bug) | P2 | HasTablePermission에서 내장 롤만 검사하고 사용자 소속 커스텀 롤(u.Roles) 권한 누락으로 인한 인가 실패 해결 | 2026-11-04T09:23:35.460701Z |
| `DONE` | **`BUG-BACKUP-RESTORE-VIEW-OMISSION-1`** | 백업 아카이브 생성 및 복원 시 뷰(View) 메타데이터 전무 누락 방지 (Backup Restore View Bug) | P2 | CreateBackup/RestoreBackup에 catalog/views.json 직렬화 및 복원 파이프라인을 추가하여 뷰 정의 영구 유실 방어 | 2026-11-04T09:23:36.441122Z |
| `DONE` | **`BUG-BOUNDS-1`** | 인덱스 바운드 무작위 추출 방지 (Index Bound Bug) | P2 | ParseIndexCond 무작위 맵 순회 제거 및 인덱스 대상 컬럼 바운드 정밀 매칭 | 2026-11-04T09:23:33.489052Z |
| `DONE` | **`BUG-CASE-1`** | 컬럼 대소문자 정규화 (Case-Insensitive) | P2 | TableSchema.FindColumn의 strings.EqualFold 대소문자 무관 탐색 보장 | 2026-11-04T09:23:33.336131Z |
| `DONE` | **`BUG-CATALOG-FK-MULTI-COL-ACTIONS-1`** | 가상 카탈로그 복합 외래키 쉼표 병합 및 액션 왜곡 방지 (Catalog Multi Col FK Bug) | P2 | execDbGateMSSQLForeignKeySelect 복합 외래키 단일 행 쉼표 결합 및 CASCADE/SET NULL 액션 누락 방지 | 2026-11-04T09:23:35.601594Z |
| `DONE` | **`BUG-CATALOG-IDENT-1`** | 가상 카탈로그 3-part/4-part 식별자 파싱 결함 해결 (Catalog Ident Bug) | P2 | 점 단위 분할 후 최종 식별자 정규 트림 적용으로 [master].[sys].[tables] 정상 인식 보장 | 2026-11-04T09:23:34.309678Z |
| `DONE` | **`BUG-CATALOG-INTERCEPT-BRACKET-1`** | 가상 카탈로그 대괄호 식별자 인터셉트 보장 (Catalog Bracket Bug) | P2 | 정규화 단계 대괄호 및 스키마 접두사 트림으로 SSMS/sqlcmd 가상 뷰 조인 쿼리 100% 정상 인터셉트 | 2026-11-04T09:23:34.547305Z |
| `DONE` | **`BUG-CATALOG-MSSQL-REGEX-UNICODE-1`** | 가상 카탈로그 N... 리터럴 및 대괄호 식별자 매칭 (Catalog Regex Unicode Bug) | P2 | N... 접두사 및 대괄호 허용 견고한 카탈로그 정규식으로 SSMS/DBeaver 전수 덤프 방지 | 2026-11-04T09:23:35.241487Z |
| `DONE` | **`BUG-CATALOG-OBJECT-PERSISTENCE-OMISSION-1`** | 서버 재기동 시 프로시저/함수/뷰/트리거 영구 증발 방지 (Catalog Object Persistence Bug) | P2 | 인메모리 맵 보관 객체들을 WAL 레코드 또는 catalog_objects.json으로 영속화하여 데이터베이스 재시작 후 메타데이터 복원 보장 | 2026-11-04T09:23:36.209533Z |
| `DONE` | **`BUG-CATALOG-ORDERBY-1`** | 가상 카탈로그 ORDER BY 콤마 잔존 정렬 무력화 방지 (Catalog Order By Bug) | P2 | strings.Fields 분해로 인해 orderCol에 콤마가 남아("col1,") 카탈로그 정렬이 무력화되던 결함 해결 | 2026-11-04T09:23:34.212464Z |
| `DONE` | **`BUG-COORD-AGG-MIN-MAX-NON-NUMERIC-NIL-1`** | 코디네이터 집계 MIN/MAX 비숫자형 파싱 실패 시 NULL 반환 방지 (Coord Agg MinMax Bug) | P2 | strconv.ParseFloat 전용 파싱을 문자열/일시/불리언 비교 인터페이스로 확장하여 문자열 및 날짜 컬럼의 극값 유실 해결 | 2026-11-04T09:23:36.118536Z |
| `DONE` | **`BUG-COPY-DELIM-1`** | COPY FROM DELIMITER 후속 옵션 따옴표 변조 방지 (Copy Delimiter Bug) | P2 | DELIMITER 뒤 후속 옵션 존재 시 닫는 따옴표()를 구분자로 파싱하여 CSV 파싱이 깨지는 결함 해결 | 2026-11-04T09:23:34.139072Z |
| `DONE` | **`BUG-COPY-PK-1`** | COPY FROM 정수 PK 0 보존 (Copy PK Zero Bug) | P2 | PGWire COPY FROM 시 정수 기본키 0을 미지정으로 오해하여 Unix 에포크 타임스탬프로 위조하는 결함 수정 | 2026-11-04T09:23:33.813108Z |
| `DONE` | **`BUG-CTE-MATERIALIZE-1`** | CTE 임의 PK 강제 및 VARCHAR 타입 파괴 해결 (CTE Materialize Bug) | P2 | 첫 번째 컬럼 임의 PK 선언으로 인한 중복 키 에러 방지 및 원본 쿼리 데이터 타입 메타데이터 보존 | 2026-11-04T09:23:34.261278Z |
| `DONE` | **`BUG-DATE-TODATETIMEOFFSET-INT-MINS-1`** | TODATETIMEOFFSET 정수 분 단위 오프셋 시간 오해석 방지 (ToDateTimeOffset Mins Bug) | P2 | T-SQL 규격 정수 오프셋(분 단위)을 시간 단위로 60배 왜곡 포맷팅하던 parseOffsetLocation 파싱 정합성 보장 | 2026-11-04T09:23:35.574278Z |
| `DONE` | **`BUG-DATEADD-NULL-1`** | DATEADD/EOMONTH 증분 NULL 시 NULL 정규 반환 (DateAdd NULL Bug) | P2 | 증분 파라미터 NULL 검사 및 NULL 즉시 반환, 미지원 datepart 에러 정규화 | 2026-11-04T09:23:34.865488Z |
| `DONE` | **`BUG-DDL-COMPOSITE-PK-PARSE-1`** | CREATE TABLE 복합 기본키 콤마 분해 및 스키마 보존 (DDL Composite PK Bug) | P2 | 괄호 내부 콤마 분해 루프로 선언된 모든 복합 PK 컬럼 플래그 정상 부여 | 2026-11-04T09:23:35.436209Z |
| `DONE` | **`BUG-DDL-PK-1`** | PK 임의 추측 방지 (DDL PK Guess Bug) | P2 | PK 미지정 테이블 생성 시 Columns[0]을 멋대로 PK로 승격하는 안티패턴 제거 및 가상 PK(_row_id) 보장 | 2026-11-04T09:23:33.53706Z |
| `DONE` | **`BUG-DDL-SPLIT-QUOTE-1`** | CREATE TABLE splitColumnDefs 따옴표 추적 (DDL Split Quote Bug) | P2 | DEFAULT hello, world 콤마 및 DEFAULT ( 괄호로 인한 컬럼 정의 절단/합쳐짐 해결 | 2026-11-04T09:23:34.69564Z |
| `DONE` | **`BUG-DECORRELATE-TRIM-1`** | 비상관화 and 접미 단순 Trim 결함 해결 (Decorrelate Trim Bug) | P2 | removeClause에서 "and" 단순 슬라이스로 Poland ➔ Pol 등 리터럴 손상 방지 토큰 경계 검사 적용 | 2026-11-04T09:23:34.365143Z |
| `DONE` | **`BUG-DERIVED-TABLE-PK-GUESS-EMPTY-1`** | 0건 파생테이블 Columns[0] 임의 PK 지정 및 제약 파괴 방지 (Derived Table PK Bug) | P2 | 0건 반환 시 임의 컬럼 PK 승격을 중단하고 항상 가상 기본키(_dt_row_id)를 부여하여 스키마 정합성 보장 | 2026-11-04T09:23:36.006954Z |
| `DONE` | **`BUG-DML-EVAL-UPDATE-ARITH-CORRUPT-1`** | 코디네이터 UPDATE 산술식 미지원 및 0값/타입변조 방지 (Eval Update Arith Bug) | P2 | 0으로 나누기 예외 처리 및 컬럼 간 연산 시 문자열 폴백 방지로 데이터 사일런트 변조 차단 | 2026-11-04T09:23:35.802669Z |
| `DONE` | **`BUG-DML-PK-RANDOM-1`** | 비-id PK 테이블 DML 시 무작위 컬럼 PK 간주 해결 (DML Random PK Bug) | P2 | ExecuteUpdate/ExecuteDelete 폴백의 rec["id"] 하드코딩 및 맵 순회 임의 컬럼 PK 간주 결함 해결 | 2026-11-04T09:23:34.004043Z |
| `DONE` | **`BUG-DML-PK-RANDOM-GUESS-1`** | DML/DDL PK 식별 실패 시 임의 순회 행 훼손 방지 (DML PK Guess Bug) | P2 | Values[0] 및 for range rec 무작위 순회 완전 삭제, 공식 PK 엄격 조회로 엉뚱한 행 훼손 원천 차단 | 2026-11-04T09:23:34.959695Z |
| `DONE` | **`BUG-DROP-INDEX-CASE-1`** | DropIndex 대소문자 정규화 및 좀비 인덱스 방지 (Drop Index Case Bug) | P2 | td.Indexes 맵 삭제 시 공식 인덱스 명칭(foundIdx.Name) 삭제로 좀비 인덱스 잔존 방지 | 2026-11-04T09:23:34.747071Z |
| `DONE` | **`BUG-EVAL-IN-COMMA-SPLIT-1`** | WHERE IN 절 문자열 리터럴 쉼표 오절단 방지 (Eval IN Comma Bug) | P2 | 따옴표 상태 인식 스마트 IN 분해기로 콤마 포함 문자열 리터럴 절단 및 매칭 영구 실패 해결 | 2026-11-04T09:23:35.042616Z |
| `DONE` | **`BUG-EVAL-NEQ-NULL-1`** | evalRowBinaryOp 불일치 비교 NULL 3가 논리 보장 (Eval NEQ NULL Bug) | P2 | TokenNeq 연산자 NULL 피연산자 시 nil(UNKNOWN) 반환으로 WHERE 절 누수 원천 차단 | 2026-11-04T09:23:34.83887Z |
| `DONE` | **`BUG-EXEC-AGG-DISTINCT-IGNORED-1`** | AggregateIterator DISTINCT 집계 무시 및 전체 행 집계 왜곡 방지 (Agg Distinct Ignored Bug) | P2 | 정규식 (?:DISTINCT\s+)? 일치 시 isDistinct 플래그 보존 및 groupAccumulator 고유값 셋 추적으로 중복 집계 왜곡 차단 | 2026-11-04T09:23:36.090302Z |
| `DONE` | **`BUG-EXEC-BUILDER-JOIN-COND-SPLIT-1`** | 복합 조인 조건 = 분해 파괴 및 무제한 카테시안 곱 왜곡 방지 (Join Cond Split Bug) | P2 | parseJoinCond에서 다중 복합 조건(k1=v1 AND k2=v2) 지원으로 빈 문자열("") 해시 매칭 및 카테시안 왜곡 해결 | 2026-11-04T09:23:36.271195Z |
| `DONE` | **`BUG-EXEC-JOIN-STR-CASE-1`** | HashJoin/MergeJoin 문자열 조인 키 대소문자 무관 매칭 보장 (Join Str Case Bug) | P2 | 조인 키 해싱 및 비교 시 대소문자 무관 정규화 적용으로 SQL 표준 조인 매칭 보장 | 2026-11-04T09:23:35.359907Z |
| `DONE` | **`BUG-EXEC-PROC-TX-BYPASS-1`** | EXEC 프로시저 호출 시 외부 트랜잭션 세션 우회 및 독립 자동 커밋 방지 (Exec Proc Tx Bypass Bug) | P2 | execProcedureCall에서 활성 트랜잭션(cs.Tx) 공유로 프로시저 내 수정 사항의 트랜잭션 원자성 및 ROLLBACK 보장 | 2026-11-04T09:23:35.658474Z |
| `DONE` | **`BUG-EXP-1`** | EXPLAIN 서브쿼리 무작위 치환 방지 (Explain Bug) | P2 | EXPLAIN 서브쿼리 평탄화 시 무작위 맵 순회 제거 및 서브쿼리 컬럼 키 정합 | 2026-11-04T09:23:33.511327Z |
| `DONE` | **`BUG-FK-CASE-1`** | 외래키 부모 컬럼 대소문자 정규화 (FK Case Bug) | P2 | findMatchingChildPKsUnderLock 대소문자 불일치로 자식 참조 0건 오판 및 부모 삭제 허용 결함 수정 | 2026-11-04T09:23:33.735465Z |
| `DONE` | **`BUG-FK-CHILD-CASE-1`** | 외래키 자식 컬럼 대소문자 정규화 (FK Child Case Bug) | P2 | checkForeignKeyUnderLock 대소문자 불일치 시 가짜 외래키 삽입이 무단 허용되는 결함 수정 | 2026-11-04T09:23:33.890508Z |
| `DONE` | **`BUG-FK-PARENT-TABLE-CASE-1`** | 외래키 부모 테이블 대소문자 정규화 (FK Parent Table Case Bug) | P2 | 부모 테이블 대소문자 불일치 시 ON DELETE RESTRICT 및 외래키 연쇄 검사 완전 우회 결함 해결 | 2026-11-04T09:23:34.418357Z |
| `DONE` | **`BUG-FOR-JSON-TABLE-ALIAS-WRAPPER-1`** | FOR JSON PATH 테이블 별칭 중첩 객체 오해석 방지 (For JSON Alias Bug) | P2 | 테이블 별칭과 컬럼 별칭 구분으로 순수 컬럼 프로젝션 시 불필요 래핑 제거 | 2026-11-04T09:23:35.411677Z |
| `DONE` | **`BUG-HTTP-TABLE-PK-1`** | HTTP API 테이블 생성 시 임의 PK 승격 제거 (HTTP Table PK Bug) | P2 | cols[0].PrimaryKey = true 임의 지정 제거 및 미지원 타입 전달 시 400 에러 정규 반환 | 2026-11-04T09:23:34.048739Z |
| `DONE` | **`BUG-INDEX-KEY-CASE-1`** | 세컨더리 인덱스 키 추출 대소문자 정규화 (Index Key Case Bug) | P2 | extractIndexKey의 대소문자 미정규화로 인한 세컨더리 인덱스 NULL 오염 및 스캔 0건 결함 해결 | 2026-11-04T09:23:34.391731Z |
| `DONE` | **`BUG-INDEX-SCAN-ORDERBY-1`** | 인덱스 스캔 다중 정렬 무시 및 정렬 누락 방지 (Index Scan Order By Bug) | P2 | 첫 번째 컬럼만 일치 시 다중 정렬 전체가 만족된 것으로 오판하여 정렬을 건너뛰는 결함 해결 | 2026-11-04T09:23:34.115376Z |
| `DONE` | **`BUG-JOIN-IDX-1`** | 조인 인덱스 임의 선택 방지 (Join Index Guess Bug) | P2 | IndexNestedLoopJoin 구성 시 인덱스 미일치 시 Indexes[0]을 임의 선택하여 조인 오염시키는 결함 방지 (Hash Join 안전 전환) | 2026-11-04T09:23:33.580394Z |
| `DONE` | **`BUG-JOIN-PARSE-1`** | 조인 조건 단순 Split 파싱 결함 수정 (Join Parse Bug) | P2 | strings.Split(cond, "=") 다중 조건 분해 실패 시 빈 컬럼명 전달 및 PGWire FROM 분해 오작동 방지 | 2026-11-04T09:23:33.667931Z |
| `DONE` | **`BUG-MEM-INDEX-LOCAL-ORDER-1`** | MemEngine 로컬 삽입 행 인덱스 정렬 보장 (MemEngine Index Order Bug) | P2 | 트랜잭션 로컬 삽입 행 Go 맵 무작위 순회 append 제거 및 인덱스 키 병합 정렬 적용 | 2026-11-04T09:23:34.64754Z |
| `DONE` | **`BUG-MEM-SHARD-INIT-RACE-1`** | TableData 샤드 지연 초기화 동시성 경쟁 런타임 패닉 방지 (MemEngine Shard Init Race Bug) | P2 | ensureShardsInit의 락 없는 맵 초기화로 인한 동시 읽기 시 fatal error: concurrent map writes 크래시 방지 (sync.Once) | 2026-11-04T09:23:35.545732Z |
| `DONE` | **`BUG-MEM-SKIPLIST-UNIQUE-INCLUDE-COPY-1`** | SkipList 유니크 인덱스 재인덱싱 시 커버링 컬럼 복사본 갱신 방지 (SkipList Unique Include Bug) | P2 | curr.Items 값 복사 순회(for _, item)로 인한 커버링 컬럼(IncludeValues) 갱신 누락 및 인덱스 조회 왜곡 해결 | 2026-11-04T09:23:35.515397Z |
| `DONE` | **`BUG-MEM-TX-INDEX-CASE-1`** | MemEngine IndexScan 인덱스명 대소문자 무관 탐색 보장 (MemEngine Index Case Bug) | P2 | 인덱스 맵 1차 실패 시 대소문자 무관 순회 보강으로 ErrIndexNotFound 및 풀스캔 강제 방지 | 2026-11-04T09:23:35.154378Z |
| `DONE` | **`BUG-MEM-TX-TABLE-CASE-1`** | 트랜잭션 커밋 시 테이블 대소문자 사일런트 쓰기 증발 방지 (MemEngine Tx Case Bug) | P2 | 변경 레코드 테이블명 스키마 공식 명칭 정규화로 커밋 시 샤드 미반영 쓰기 유실 차단 | 2026-11-04T09:23:35.129237Z |
| `DONE` | **`BUG-MEM-WAL-CREATE-TABLE-INDEX-OMISSION-1`** | WAL 복구 시 CREATE TABLE 인라인 인덱스 등록 누락 방지 (WAL Create Table Index Bug) | P2 | OpCreateTable 재생 시 op.Schema.Indexes 순회 초기화로 충돌 복구 후 인덱스 갱신 누락 해결 | 2026-11-04T09:23:35.831701Z |
| `DONE` | **`BUG-MERGE-IDENTITY-PK-1`** | MERGE WHEN NOT MATCHED IDENTITY 자동 채번 보장 (Merge Identity PK Bug) | P2 | MERGE INSERT 시 IDENTITY 자동 채번 및 세션/테이블 식별자 동기화 연동 | 2026-11-04T09:23:35.181288Z |
| `DONE` | **`BUG-MERGE-NOT-MATCHED-SOURCE-SELF-DESTRUCT-1`** | MERGE 구문 내 신규 삽입 행의 SOURCE 미일치 오인 즉각 삭제 방지 (Merge Self Destruct Bug) | P2 | WHEN NOT MATCHED 삽입 행의 matchedTargetKeys 등록으로 동일 MERGE 문 내 NOT MATCHED BY SOURCE의 자가 삭제 방어 | 2026-11-04T09:23:36.329941Z |
| `DONE` | **`BUG-MERGE-SET-1`** | MERGE SET 절 다중 인자 수식 보존 (Merge Set Bug) | P2 | parseSetPairs 괄호 고려 토큰 분해로 COALESCE 등 수식 내 콤마 분해 시 후속 구문 사일런트 유실 방지 | 2026-11-04T09:23:33.784323Z |
| `DONE` | **`BUG-OCC-COMMIT-INTRA-TX-UNIQUE-1`** | 단일 트랜잭션 내 다중 유니크 충돌 사일런트 커밋 방지 (OCC Intra Tx Unique Bug) | P2 | tx.changes 내부 상호 유니크 충돌 사전 검증 및 idxData.Insert 에러 전파로 중복 커밋 원천 차단 | 2026-11-04T09:23:35.889959Z |
| `DONE` | **`BUG-OCC-UPDATE-UNIQUE-CHECK-OMISSION-1`** | MemEngine updateLocked 유니크 인덱스 사전 검증 누락 방지 (OCC Update Unique Bug) | P2 | 레코드 수정(Update) 시 유니크 인덱스 즉각 검증 로직 추가로 트랜잭션 내 중복 키 갱신 방어 | 2026-11-04T09:23:35.918959Z |
| `DONE` | **`BUG-ORDER-BY-SPLIT-1`** | ORDER BY 다중 컬럼 및 대소문자 정합성 (Order By Bug) | P2 | strings.Fields 분해로 인한 다중 정렬 누락, 콤마 포함 시 DESC 무력화 및 대소문자 미매칭 무력화 원천 해결 | 2026-11-04T09:23:33.646246Z |
| `DONE` | **`BUG-PARSER-SIMPLE-CASE-OMISSION-1`** | Pratt 파서 Simple CASE 피연산자 누락 및 문법 거부 방지 (Parser Simple Case Bug) | P2 | parseCase에서 CASE expr WHEN ... 단순 CASE 피연산자 인식 및 CaseExpr.Operand AST 필드 보강 | 2026-11-04T09:23:35.74332Z |
| `DONE` | **`BUG-PGWIRE-DESCRIBE-1`** | PGWire Describe 단순 문자열 분해 OID 왜곡 방지 (PGWire Describe Bug) | P2 | 쿼리를 FROM/콤마로 단순 분해하여 서브쿼리 및 함수 콤마가 파괴되고 OID 25로 왜곡되는 결함 해결 | 2026-11-04T09:23:34.161978Z |
| `DONE` | **`BUG-PGWIRE-PARAM-1`** | PGWire 파라미터 경계 인식 치환 (PGWire Param Bug) | P2 | 단순 문자열 치환으로 $10 이상의 다중 파라미터가 $1 치환 시 깨지는 결함 수정 | 2026-11-04T09:23:33.913462Z |
| `DONE` | **`BUG-PGWIRE-PREP-EXEC-TX-BYPASS-1`** | PGWire 사전 컴파일 바이트코드 실행 시 트랜잭션 우회 방지 (PGWire Prep Tx Bypass Bug) | P2 | handleExecute 패스트패스에서 신규 독립 세션(NewSession) 강제 생성을 중단하고 활성 세션 컨텍스트 및 트랜잭션 바인딩 | 2026-11-04T09:23:36.148828Z |
| `DONE` | **`BUG-PITR-DDL-SILENT-SWALLOW-1`** | PITR WAL 재생 시 DDL 실행 에러 침묵 및 복구 실패 은폐 방지 (PITR DDL Swallow Bug) | P2 | DDL 실행 실패(CreateTable, DropTable 등) 시 에러 즉시 전파로 후속 데이터 파괴 및 복구 왜곡 방지 | 2026-11-04T09:23:36.031862Z |
| `DONE` | **`BUG-PIVOT-COUNT-NULL-1`** | PIVOT COUNT 집계 시 빈 카테고리 0 정규 반환 (Pivot Count NULL Bug) | P2 | COUNT 집계에 대해 빈 그룹 시 0을 정규 방출하도록 보강 | 2026-11-04T09:23:35.386532Z |
| `DONE` | **`BUG-PIVOT-ORDER-BY-SPLIT-1`** | PIVOT/UNPIVOT ORDER BY 절 쉼표 분해 왜곡 방지 (Pivot Order By Split Bug) | P2 | strings.Fields 대신 스마트 정렬 파서 적용으로 쉼표 포함 정렬 키(col,, DESC,) 파괴 및 정렬 무력화 해결 | 2026-11-04T09:23:35.773743Z |
| `DONE` | **`BUG-PK-1`** | 무결성 결함 수정 (Integrity Bug) | P2 | execUpdate/execDelete/execTruncate 및 VDBE의 PK 동적 해석 및 맵 순회 왜곡 방지 | 2026-11-04T09:23:33.314636Z |
| `DONE` | **`BUG-PLANNER-CBO-SELECTIVITY-CASE-SENSITIVE-1`** | CBO 선택도 추정 시 컬럼명 대소문자 불일치 0.33 휴리스틱 추락 방지 (CBO Selectivity Case Bug) | P2 | EstimateSelectivity의 ts.Columns 직접 맵 조회를 findColumnStats로 대체하여 대소문자 무관 통계 조회 보장 | 2026-11-04T09:23:36.390307Z |
| `DONE` | **`BUG-PLANNER-DECORRELATE-OR-SUBQUERY-CORRUPT-1`** | OR 조건 하위 서브쿼리 불법 SEMI 조인 변환 및 and 식별자 파괴 방지 (Subquery Decorrelate Bug) | P2 | OR 분기 내 서브쿼리 무단 변환 차단 및 removeClause의 경계 인식 토큰화로 brand 등 컬럼명 절단 및 문법 오류 해결 | 2026-11-04T09:23:36.417342Z |
| `DONE` | **`BUG-PLANNER-ORDERBY-1`** | CBO/SetOp 다중 정렬 키 콤마 잔존 해결 (Planner OrderBy Bug) | P2 | CBO 및 SetOp 플래너 다중 정렬 시 strings.Fields 콤마 잔존("a,")으로 인한 플랜 왜곡 해결 | 2026-11-04T09:23:34.444001Z |
| `DONE` | **`BUG-PLANNER-SPLIT-AND-QUOTE-ESCAPE-1`** | WHERE 절 이스케이프 따옴표 및 대괄호 식별자 AND 오절단 방지 (Planner Split AND Bug) | P2 | splitAndConditions의 중첩 따옴표() 및 대괄호 식별자 미인식으로 인한 WHERE 조건 파괴 및 플래너 에러 해결 | 2026-11-04T09:23:35.488314Z |
| `DONE` | **`BUG-PROC-SIMPLE-CASE-1`** | 단순 CASE 문(Simple CASE) 파싱 및 컴파일 보장 (Simple CASE Bug) | P2 | CaseExpr에 Operand 필드 신설 및 VDBE 피연산자-조건값 비교 분기 바이트코드 방출 | 2026-11-04T09:23:34.808617Z |
| `DONE` | **`BUG-PROJ-CAST-AS-1`** | 수식 내 AS 오분해 방지 (Project Cast AS Bug) | P2 | CAST(x AS INT) 등 수식 내부 " AS " 오분해로 인한 표현식 절단 및 NULL 반환 결함 해결 | 2026-11-04T09:23:34.339854Z |
| `DONE` | **`BUG-RECORD-1`** | 사일런트 유실 방지 (Silent Loss Bug) | P2 | DML Record 컬럼명 정규화(colSchema.Name)로 RecordToRow 업데이트 유실 및 NULL 삽입 방지 | 2026-11-04T09:23:33.358603Z |
| `DONE` | **`BUG-SELECT-AGG-CASE-1`** | GROUP BY 및 집계함수 대소문자 정합성 보장 (Select Agg Case Bug) | P2 | computeAggregates 행 맵 대소문자 무관 탐색으로 GROUP BY <nil> 및 집계함수 NULL 변조 방지 | 2026-11-04T09:23:34.571489Z |
| `DONE` | **`BUG-SELECT-INTO-PK-1`** | SELECT INTO PK 임의 추측 방지 (Select Into PK Bug) | P2 | SELECT INTO 및 파생 테이블의 일시적 고유 컬럼 임의 PK 지정 및 0건 시 Columns[0] PK 지정 제거 | 2026-11-04T09:23:33.558023Z |
| `DONE` | **`BUG-SP-EXEC-SUFFIX-OUT-1`** | sp_executesql OUT 접미 변수명 오절단 방지 (SP Suffix OUT Bug) | P2 | cleanOutputSuffix 선행 공백/단어 경계 검사로 @timeout 등 OUT 접미 변수명 3글자 절단 훼손 해결 | 2026-11-04T09:23:34.623888Z |
| `DONE` | **`BUG-SP-PARAM-DECIMAL-1`** | sp_executesql 복합 파라미터 타입 파싱 보존 (SP Param Decimal Bug) | P2 | 괄호 중첩 추적으로 DECIMAL(10, 2) 등 다중 인자 타입 콤마 분해 절단 및 유령 파라미터 생성 방지 | 2026-11-04T09:23:34.598721Z |
| `DONE` | **`BUG-SPLIT-VALUES-1`** | VALUES 튜플 중첩 수식 콤마 분해 정합성 (Split Values Bug) | P2 | splitValues 괄호 깊이(depth) 추적으로 COALESCE(a, b) 등 다중 인자 함수 사용 시 컬럼 수 불일치 에러 방지 | 2026-11-04T09:23:33.758898Z |
| `DONE` | **`BUG-STORAGE-CMP-DATETIME-STR-1`** | DateTime-VarChar 상호 변환 누락 비교 역전 방지 (Storage Cmp DateTime Bug) | P2 | 날짜/시간과 문자열 간의 상호 파싱/강제변환으로 올바른 시간 축 비교 및 필터 왜곡 해결 | 2026-11-04T09:23:35.068757Z |
| `DONE` | **`BUG-STORAGE-CMP-INT-FLOAT-1`** | Value.Compare Int-Float 상호 강제변환 보장 (Compare Int-Float Bug) | P2 | IntValue(100) > FloatValue(200.5) 등 타입 불일치 비교 시 미초기화 0값 비교 역전 해결 | 2026-11-04T09:23:34.671651Z |
| `DONE` | **`BUG-STORAGE-EQUAL-FLOAT-STR-1`** | 실수 6자리 문자열 포맷팅 동등 비교 왜곡 방지 (Storage Equal Float Bug) | P2 | 실수 문자열 포맷팅 제거 및 부동소수점 비트/엡실론 비교로 Compare/Equal 정합성 보장 | 2026-11-04T09:23:35.09779Z |
| `DONE` | **`BUG-STORAGE-TABLE-CASE-SENSITIVITY-1`** | 스토리지 엔진 테이블명 대소문자 미구분 맵 조회 실패 및 중복 생성 방지 (Storage Table Case Bug) | P2 | MemEngine/WiredTiger의 e.tables 원시 맵 조회를 대소문자 무시 정규화(EqualFold / ToLower)하여 ErrTableNotFound 및 동명이인 테이블 방어 | 2026-11-04T09:23:36.060702Z |
| `DONE` | **`BUG-STORAGE-TEMP-TABLE-PERSIST-LEAK-1`** | 세션 임시테이블 스냅샷/WAL 영속화 및 재기동 후 영구 누수 방지 (Temp Table Persist Bug) | P2 | saveDiskLocked 및 WAL 재생 시 __temp_* / __tv_* 필터링으로 종료 세션 임시 객체의 영구 카탈로그 오염 차단 | 2026-11-04T09:23:36.300129Z |
| `DONE` | **`BUG-STORAGE-UPDATE-CASE-1`** | 스토리지 Update 레코드 컬럼명 대소문자 정규화 (Storage Update Case Bug) | P2 | updateLocked에서 입력 레코드 키를 스키마 컬럼명으로 정규화하여 대소문자 차이 업데이트 유실 방지 | 2026-11-04T09:23:34.070814Z |
| `DONE` | **`BUG-STR-CHARINDEX-NULL-ZERO-1`** | CHARINDEX/STRPOS의 NULL 인자 수신 시 0 반환 삼치논리 왜곡 방지 (CharIndex NULL Zero Bug) | P2 | CHARINDEX와 STRPOS에서 NULL 인자 입력 시 0(검색실패) 대신 NULL 정상 전파로 ANSI/T-SQL 삼치논리 정합성 보장 | 2026-11-04T09:23:36.360815Z |
| `DONE` | **`BUG-STR-NULL-1`** | 문자열 함수 NULL 전파 보장 (String NULL Bug) | P2 | UPPER/LOWER/TRIM/SUBSTRING/REPLACE 등 NULL 입력 시 빈 문자열/변조 대신 NULL 정규 반환 | 2026-11-04T09:23:33.841863Z |
| `DONE` | **`BUG-STRING-SPLIT-SEP-1`** | STRING_SPLIT 빈 구분자 콤마 임의 간주 제거 (String Split Sep Bug) | P2 | 빈 문자열 구분자를 콤마로 멋대로 대체하는 안티패턴 제거 및 표준 규격 에러 반환 | 2026-11-04T09:23:33.959803Z |
| `DONE` | **`BUG-SUBQ-1`** | 스칼라 서브쿼리 무작위 반환 방지 (Subquery Bug) | P2 | 스칼라 서브쿼리 수식 평가 시 무작위 맵 순회 제거 및 cols[0] 대소문자 무관 탐색 | 2026-11-04T09:23:33.46782Z |
| `DONE` | **`BUG-TABLE-CASE-1`** | 테이블명 대소문자 무관 탐색 보장 (Table Case Bug) | P2 | GetTable/DropTable 맵 엄격 일치로 테이블 미인식 에러 방지 (EqualFold 폴백 보장) | 2026-11-04T09:23:33.690149Z |
| `DONE` | **`BUG-TDS-ROW-CASE-1`** | TDS 행 직렬화 대소문자 정규화 (TDS Row Case Bug) | P2 | WriteRowDirect/WriteNBCRowDirect 대소문자 무관 탐색 지원으로 올바른 데이터 전송 보장 | 2026-11-04T09:23:34.284102Z |
| `DONE` | **`BUG-TDS-RPC-PARAM-NAME-ORDER-1`** | TDS sp_executesql 파라미터명 미인식 순서 왜곡 방지 (TDS RPC Param Name Bug) | P2 | handleRPCExecuteSQL에서 파라미터명(@stmt, @params) 검사로 JDBC/.NET 클라이언트 명명 파라미터 도치 오류 해결 | 2026-11-04T09:23:35.714416Z |
| `DONE` | **`BUG-TDS-RPC-PREP-TX-BYPASS-1`** | TDS RPC 사전 컴파일 플랜 실행 시 세션/트랜잭션 우회 방지 (TDS RPC Prep Tx Bypass Bug) | P2 | sp_execute/sp_prepexec 패스트패스에서 독립 스토리지 세션 대신 세션 컨텍스트 및 inTx 트랜잭션 바인딩 | 2026-11-04T09:23:35.686761Z |
| `DONE` | **`BUG-TUPLE-RECORD-CASE-1`** | RecordToRow 대소문자 일치로 인한 NULL 덮어쓰기 방지 (Tuple Case Bug) | P2 | rec[col.Name] 엄격 조회로 대소문자가 다른 레코드 입력 시 컬럼 값이 NULL로 훼손되는 결함 해결 | 2026-11-04T09:23:34.092724Z |
| `DONE` | **`BUG-UPDATE-SET-COMMA-SPLIT-1`** | 코디네이터 UPDATE SET 절 쉼표 오절단 방지 (Update Set Comma Bug) | P2 | 따옴표 및 괄호 상태 추적 토큰 분해기로 문자열 리터럴/함수 인자 콤마 오분해 및 수식 파괴 해결 | 2026-11-04T09:23:35.014518Z |
| `DONE` | **`BUG-UPDATE-SET-SPLIT-1`** | UPDATE SET 절 다중 인자 수식 사일런트 유실 방지 (Update Set Split Bug) | P2 | strings.Split(setClause, ",") 단순 분해 제거로 다중 인자 함수 및 리터럴 콤마 분해 유실 방지 | 2026-11-04T09:23:34.026391Z |
| `DONE` | **`BUG-UPSERT-1`** | Upsert 충돌 정합성 (Upsert Bug) | P2 | ON CONFLICT (col) 충돌 컬럼 대소문자 정규화로 Upsert 유니크 위반 방지 | 2026-11-04T09:23:33.404252Z |
| `DONE` | **`BUG-VM-COMP-IDX-1`** | VDBE 복합 인덱스 인코딩 정합성 (VM Composite Index Bug) | P2 | VDBE VM에서 복합 인덱스를 단일 인덱스로 착각하고 비인코딩 스캔하여 0건 반환되는 결함 수정 | 2026-11-04T09:23:33.623688Z |
| `DONE` | **`BUG-VM-DML-COL-CASE-1`** | VDBE DML 컬럼명 대소문자 정규화 및 값 보존 (VM DML Col Case Bug) | P2 | Record 컬럼 대소문자 차이로 인한 RecordToRow/updateLocked 값 유실 및 NULL 삽입 해결 | 2026-11-04T09:23:34.928752Z |
| `DONE` | **`BUG-VM-DML-PK-NOT-FOUND-CRASH-1`** | VDBE OpUpdate/OpDelete PK 미존재 시 ErrKeyNotFound 예외 크래시 방지 (VM DML PK Not Found Bug) | P2 | VDBE 단일 PK 수정/삭제 시 대상 행 미존재 시 에러 중단 대신 표준 SQL 규격 준수 0건 처리(@@ROWCOUNT = 0) 보장 | 2026-11-04T09:23:35.630022Z |
| `DONE` | **`BUG-VM-INSERT-DEFAULT-1`** | VDBE OpInsert 상수 리터럴 기본값 주입 보장 (VM Insert Default Bug) | P2 | 6대 내장 함수 외 상수 리터럴 기본값(DEFAULT 0, DEFAULT active) 누락 및 NULL 삽입 해결 | 2026-11-04T09:23:34.894744Z |
| `DONE` | **`BUG-VM-INTO-1`** | VDBE 변수대입 무작위 순회 방지 (IntoVar Bug) | P2 | VDBE SELECT ... INTO @var 무작위 맵 순회 제거 및 cols[0] 결정론적 바인딩 | 2026-11-04T09:23:33.447296Z |
| `DONE` | **`BUG-VM-SUBQ-NIL-1`** | VDBE 스칼라 서브쿼리 nil 패닉 및 컬럼 침묵 버림 방지 (VM Subquery Nil Bug) | P2 | (SELECT 1) 등 FROM 없는 서브쿼리 nil 포인터 패닉 제거 및 다중 컬럼 서브쿼리 문법 에러 반환 | 2026-11-04T09:23:34.187375Z |
| `DONE` | **`BUG-VM-UPDATE-1`** | VDBE UPDATE 컬럼명 정규화 (VM Update Silent Loss) | P2 | VDBE OpUpdate/execUpdateWhere 컬럼명 대소문자 미정규화로 인한 업데이트 사일런트 유실 방지 | 2026-11-04T09:23:33.86869Z |
| `DONE` | **`BUG-WAL-TORN-BOOT-1`** | MemEngine WAL 테일 절단 시 기동 실패 방지 (WAL Torn Write Boot Bug) | P2 | 비정상 종료 시 손상된 미완료 레코드 이전 정상 지점까지 복구 완료 후 트렁케이트하여 정상 기동 보장 | 2026-11-04T09:23:34.5234Z |
| `DONE` | **`BUG-WHERE-1`** | WHERE IN 절 정합성 (WHERE IN Bug) | P2 | evaluateSingleConditionMap 내 WHERE IN (...) 절 대소문자 무관 탐색 누락 보강 | 2026-11-04T09:23:33.425851Z |
| `DONE` | **`BUG-WINDOW-RANGE-PEER-EVAL-1`** | 윈도우 함수 RANGE 피어 프레임 집계 왜곡 방지 (Window Range Peer Bug) | P2 | ORDER BY 시 표준 기본값인 RANGE 피어 구간 확장을 구현하여 동순위 행 간 누적 집계 왜곡 해결 | 2026-11-04T09:23:35.860032Z |
| `DONE` | **`BUG-WIRE-COPY-OUT-UNESCAPED-1`** | COPY TO STDOUT 텍스트 포맷 탭/줄바꿈 이스케이프 보장 (Copy Out Unescaped Bug) | P2 | 표준 PostgreSQL 텍스트 포맷 이스케이프 규칙(\t, \n, \\) 적용으로 덤프 정합성 보장 | 2026-11-04T09:23:35.331564Z |
| `DONE` | **`BUG-WIRE-COPY-SILENT-CORRUPT-1`** | COPY FROM STDIN 파싱 에러 침묵 및 0값/현재시각 변조 방지 (Copy Silent Corrupt Bug) | P2 | 파싱 실패 시 명시적 SQL 구문 에러 반환 및 전체 데이터 타입 정밀 파싱 지원 | 2026-11-04T09:23:35.303961Z |
| `DONE` | **`BUG-WIRE-COPY-TX-BYPASS-1`** | COPY FROM STDIN 트랜잭션 세션 우회 및 독립 자동 커밋 방지 (Copy Tx Bypass Bug) | P2 | 활성 세션 컨텍스트 바인딩으로 트랜잭션 범위 내 원자적 COPY 및 롤백 보장 | 2026-11-04T09:23:35.274342Z |
| `DONE` | **`BUG-WIRE-PG-PARAM-SUBSTITUTION-INJECTION-1`** | PGWire 파라미터 치환 시 재귀 치환 왜곡 및 숫자형 문자열 따옴표 탈락 방지 (PG Param Substitute Bug) | P2 | strings.ReplaceAll 대신 단일 패스 토큰화 치환 적용 및 파라미터 실제 데이터 타입 보존으로 파라미터 재귀 오염 해결 | 2026-11-04T09:23:36.178114Z |
| `DONE` | **`BUG-WT-FK-1`** | WiredTiger 외래키 무결성 검증 구축 (WT FK Bug) | P2 | WiredTiger Insert/Update/Delete 시 외래키 참조 무결성 검사 및 CASCADE/RESTRICT 완전 연동 | 2026-11-04T09:23:33.711281Z |
| `DONE` | **`BUG-WT-INDEX-COMP-1`** | WiredTiger 복합 인덱스 정합성 (WT Composite Index Bug) | P2 | WiredTiger 복합 인덱스 스캔 시 첫 번째 컬럼만 비교하고 컬럼 누락 시 TypeUnknown 0(동일) 판정하는 결함 수정 | 2026-11-04T09:23:33.602778Z |
| `DONE` | **`BUG-WT-INDEX-DESC-1`** | WiredTiger IndexScanDesc 역정렬 스텁 결함 해결 (WT Index Desc Bug) | P2 | WTTx.IndexScanDesc 정방향 스캔 반환 결함 수정으로 ORDER BY DESC 인덱스 스캔 역정렬 보장 | 2026-11-04T09:23:34.23524Z |
| `DONE` | **`BUG-WT-SWEEP-1`** | WiredTiger 60분 유휴 세션 경고 방지 및 세션 리셋 (WT Sweep Server Bug) | P2 | startPeriodicSync 고아 세션 상시 점유 제거, session->reset 바인딩 및 SessionPool TTL 리퍼 연동 | 2026-11-04T09:23:34.470746Z |
| `DONE` | **`BUG-WT-UNIQUE-INDEX-UNENFORCED-1`** | WiredTiger 스토리지 엔진 보조 유니크 인덱스 제약 검증 및 색인 누락 방지 (WT Unique Index Bug) | P2 | insertLocked/updateLocked 시 유니크 인덱스 제약 검증 및 IndexScan의 풀 테이블 스캔 폴백을 B-Tree 커서 검색으로 개편 | 2026-11-04T09:23:36.238739Z |
| `DONE` | **`CASDOOR-INTEGRATION-1`** | Ecosystem | P2 | Casdoor IAM/SSO 듀얼 프로토콜 연동 (44 XORM 테이블, 복합 PK \x1f, 0x Hex 바이너리) | 2026-11-04T09:19:53.774803Z |
| `DONE` | **`DURA-KILL-1 (Phase 1)`** | Ecosystem | P2 | cmd/crashtest 하네스 완성, 3대 결함 수정 및 20라운드/엔진 (총 12.8만 건 tx) 강제 종료 복구 100% 무결성 입증 | 2026-11-04T09:19:53.98181Z |
| `DONE` | **`DURA-SOAK-F1`** | Ecosystem | P2 | WiredTiger WT_ROLLBACK(-31800) 쓰기 충돌을 storage.ErrSerializationFailure로 래핑하여 PGWire SQLSTATE 40001 매핑 | 2026-11-04T09:19:53.930779Z |
| `DONE` | **`DURA-SOAK-F2`** | Ecosystem | P2 | 1시간 소크 실측을 통한 500MB 페이지 캐시 워밍업 분석 및 /metrics Go 힙/고루틴 수집과 캐시 상한 기준선 확립 | 2026-11-04T09:19:53.95936Z |
| `DONE` | **`GA-REGEX-DDL-1`** | Durability | P2 | sqlexec_ddl.go 및 sqlexec_eval.go/sqlexec_explain.go 잔여 정규식 렉서 전환 | 2026-11-04T09:19:53.348669Z |
| `DONE` | **`GA-REGEX-FINAL-1`** | Durability | P2 | 엔진 프로덕션 경로 잔여 정규식 4대 영역 완전 제거 및 100% Zero-Regex 달성 (Invariant 7 완성) | 2026-11-04T09:19:53.368805Z |
| `DONE` | **`GA-REGEX-NESTED-1`** | Durability | P2 | SQL 중첩/괄호/조건식 잔여 정규식 제거 및 sqlutil 추상화 모듈화 (Invariant 7 준수) | 2026-11-04T09:19:53.388801Z |
| `DONE` | **`GA-VALID-1`** | Durability | P2 | SQLite 공식 index / random / evidence 스위트 탐색 및 결함 전량 수정·검증 완료 (test/slt/core/slt_extended_fixes.slt 100% PASS) | 2026-11-04T09:19:53.267962Z |
| `DONE` | **`GRAFANA-INTEGRATION-1`** | Ecosystem | P2 | Grafana 듀얼 프로토콜 연동 (내부 메타데이터 PGWire 785개 마이그레이션 완주 + PG/TDS 듀얼 쿼리 데이터소스) | 2026-11-04T09:19:53.796271Z |
| `DONE` | **`IOTA-ERP-1`** | Ecosystem | P2 | iota-sdk Go ERP PGWire 연동 (47개 마이그레이션, binary UUID, TIMESTAMPTZ) | 2026-11-04T09:19:53.819705Z |
| `DONE` | **`ORM-MIGRATE-1~3`** | Ecosystem | P2 | 조건부 DDL 지원, XORM 컬럼 인트로스펙션, 할당 에일리어싱(alias = expr), sys.fn_helpcollations() | 2026-11-04T09:19:53.864369Z |
| `DONE` | **`PERF-PGWIRE-PREP-TX`** | Ecosystem | P2 | PGWire PreparedStatement 트랜잭션 내 VDBE 바이트코드 직접 바인딩 (ExecuteInTxWithSessionContext, substituteParams 제거, PGWire TPC-C 24.3만 tpmC 달성) | 2026-11-04T09:19:53.755382Z |
| `DONE` | **`PERF-SERVER-PIPELINE-P1-P2`** | Ecosystem | P2 | TDS 파이프라인 최적화 (Syscall Coalescing, 32KB sync.Pool, Zero-Alloc RPC) | 2026-11-04T09:19:53.842002Z |
| `DONE` | **`PROC-CURSOR-1`** | Ecosystem | P2 | T-SQL Fast Forward-Only 읽기 전용 커서 및 @@FETCH_STATUS | 2026-11-04T09:19:53.888066Z |
| `DONE` | **`REV-CAT-003`** | Ecosystem | P2 | 가상 카탈로그 행 필터링 잔여 정규식 제거 및 sqlutil.ParseBinaryComparison 일원화 (Invariant 7, [RULE:ModularAbstraction]) | 2026-11-04T09:19:54.080466Z |
| `DONE` | **`REV-ENG-011`** | Ecosystem | P2 | ALTER TABLE 10개 서브 커맨드 내 스토리지 엔진 에러 무시(_ = ) 결함 전면 해소 및 에러 반환 보장 ([RULE:ExplicitError]) | 2026-11-04T09:19:54.032338Z |
| `DONE` | **`REV-WIRE-010`** | Ecosystem | P2 | PGWire handleParse / handleBind 비정상/음수/과대 슬라이스 할당 가드 적용 ([RULE:DefensiveIndexing], [RULE:BoundedBuffers]) | 2026-11-04T09:19:54.056326Z |
| `DONE` | **`SLT-FULL-100`** | Durability | P2 | SQLLogicTest 107개 풀세트 (551,217 쿼리) 100% 무결점 완치 (notes/SLT_FULL_SUITE_100_PERCENT_PLAN.ko.md, notes/SLT_FULL_SUITE_100_PERCENT_REPORT.ko.md, scripts/verify_0.90.sh) | 2026-11-04T09:19:53.284547Z |
| `DONE` | **`TEST-BABEL-SLT-1`** | Durability | P2 | Babelfish 기반 T-SQL Stored Procedure 전용 SLT 로직 테스트 스위트 구축 | 2026-11-04T09:19:53.328634Z |

