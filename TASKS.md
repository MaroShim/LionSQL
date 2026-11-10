# TASKS (Sprint SSOT Register & Mirror)

> - **Last Synced**: 2026-11-10
> - **Primary Source of Truth (SSOT)**: SQLite `sprint_tasks` Table
> - **Backup File**: Auto-synced on task update & Git commit

## 1. Quick Status Overview

| Status | ID | Domain | Priority | Title | Plan Doc | Metrics |
| :---: | :---: | :---: | :---: | :--- | :--- | :--- |
| `IN_PROGRESS` | **`1.8`** | Durability | P2 | 1.0 GA 검증 및 잔여 정리 | [`notes/plans/GA_REGEX_DDL_REFACTORING_PLAN.ko.md`](notes/plans/GA_REGEX_DDL_REFACTORING_PLAN.ko.md) |  |
| `PENDING` | **`3.9`** | Ecosystem | P3 | 기타 엔터프라이즈 기능 확장 |  |  |
| `PENDING` | **`AI-STARTER-1`** | Ecosystem | P2 | AI 에이전트용 컨텍스트 문서 (llms.txt, AGENTS.md 템플릿) | [`notes/AI_APP_STARTER_PLAN.ko.md`](notes/AI_APP_STARTER_PLAN.ko.md) |  |
| `PENDING` | **`AI-STARTER-2`** | Ecosystem | P2 | 랜딩 AI 앱 스타터 섹션 및 Go/Node/Python 최소 예제 | [`notes/AI_APP_STARTER_PLAN.ko.md`](notes/AI_APP_STARTER_PLAN.ko.md) |  |
| `PENDING` | **`AI-STARTER-3`** | Ecosystem | P2 | 호환성 매트릭스 문서 (되는 것/안 되는 것) | [`notes/AI_APP_STARTER_PLAN.ko.md`](notes/AI_APP_STARTER_PLAN.ko.md) |  |
| `PENDING` | **`AI-STARTER-4`** | Research | P3 | Go 임베디드 모드 타당성 조사 (SQLite 대비) | [`notes/AI_APP_STARTER_PLAN.ko.md`](notes/AI_APP_STARTER_PLAN.ko.md) |  |
| `PENDING` | **`ARCH-ANTLR-ORACLE-1`** | Architecture | P2 | ANTLR4 기반 T-SQL 문법 검증 및 미지원 기능 정밀 진단 오라클 아키텍처 | [`notes/plans/ANTLR4_SYNTAX_ORACLE_DIAGNOSTICS_PLAN.ko.md`](notes/plans/ANTLR4_SYNTAX_ORACLE_DIAGNOSTICS_PLAN.ko.md) |  |
| `PENDING` | **`ARCH-LOGICAL-NAMESPACE-1`** | ENGINE | P2 | 프리픽스 기반 논리적 네임스페이스 및 테이블 변수 통합 아키텍처 | [`notes/plans/LOGICAL_NAMESPACE_DATABASE_PREFIX_PLAN.ko.md`](notes/plans/LOGICAL_NAMESPACE_DATABASE_PREFIX_PLAN.ko.md) |  |
| `DONE` | **`ARCH-PRED`** | Ecosystem | P3 | & `ARCH-RULE-SLUG`) |  |  |
| `DONE` | **`ARCH-VDBE-ROUTING-1`** | Ecosystem | P3 | AST 및 카탈로그 기반 2-Tier 쿼리 라우팅 정규화 (Anti-Heuristic Refactoring) | [`notes/plans/ARCH_VDBE_ROUTING_1_PLAN.ko.md`](notes/plans/ARCH_VDBE_ROUTING_1_PLAN.ko.md) |  |
| `DONE` | **`AUDIT-135-DUAL-REVIEW`** | Integrity | P1 | 전 소스코드 135단계 듀얼 AI 정밀 코드 리뷰 및 엣지 케이스 감사 | [`notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md`](notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md) | 135/135 스코프 완주 (총 134건 결함 식별 및 등록 완료) |
| `DONE` | **`AUDIT-CORE-DEEP-RESCAN`** | Integrity | P1 | 12대 고복잡도 핵심 코어 구역 다중 결함 심층 전수 감사 (Deep Core Precision Rescan) | [`notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md`](notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md) |  |
| `PENDING` | **`AUDIT-LOG-1`** | Integrity | P2 | 암호화 SHA-256 체이닝 불변 감사 로그 서브시스템 | [`notes/plans/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/plans/IMMUTABLE_AUDIT_LOG_PLAN.md) |  |
| `DONE` | **`AUDIT-PATCH-P0`** | Integrity | P1 | 전수 감사 P0 (Must Fix 37선) 결함 TDD 순차 패치 및 회귀 검증 | [`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`](notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md) |  |
| `IN_PROGRESS` | **`AUDIT-PATCH-P1`** | Core | P1 | 1.0 전수 감사 P1 (Should Fix 47선) 난이도별(하/중/상) 순차 안정화 패치 | [`notes/plans/P1_AUDIT_STABILIZATION_PLAN.ko.md`](notes/plans/P1_AUDIT_STABILIZATION_PLAN.ko.md) | 1단계(하 21건, Mini-Batch 1~6) 100% 완주 완료... |
| `DONE` | **`AUDIT-TRIAGE-POST`** | Integrity | P1 | 전수 감사 완료 직후 공식 Triage(결함 분류) 회의 및 패치 범위 확정 | [`notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md`](notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md) |  |
| `DONE` | **`BENCH-ALL-IN-ONE-1`** | Durability | P2 | 원클릭 벤치마크 및 종합 검증 스위트 (`scripts/run_all_benchmarks.sh`) 구축 |  | - [x] **Phase 1~4 (성능/와이어 실측)**: Bank... |
| `PENDING` | **`BENCH-TPCH-1`** | Ecosystem | P3 | Mini TPC-H Benchmark Suite & CBO 서브쿼리 Decorrelation | [`notes/plans/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/plans/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md) |  |
| `DONE` | **`BUG-ADD-COL-NOT-NULL-1`** | Integrity | P2 | 기본값 없는 NOT NULL 컬럼 추가 시 NULL 오염 방지 (Add Column Null Bug) |  |  |
| `DONE` | **`BUG-AGG-MIXED-NUM-DEC-1`** | Integrity | P2 | AggregateIterator Decimal-정수 혼합 합계 유실 방지 (Agg Mixed Decimal Bug) |  |  |
| `DONE` | **`BUG-API-CREATE-DEFAULT-1`** | Integrity | P2 | HTTP API 레코드 생성 시 NOT NULL DEFAULT 필드 주입 (API Create Default Bug) |  |  |
| `DONE` | **`BUG-API-JSON-DATETIME-EMPTY-STR-1`** | Integrity | P2 | HTTP API 레코드 조회 시 DATETIME 빈 문자열 직렬화 방지 (API JSON DateTime Bug) |  |  |
| `DONE` | **`BUG-API-JSON-DECIMAL-PRECISION-LOSS-1`** | Integrity | P2 | HTTP API 레코드 직렬화 시 DECIMAL 실수 부동소수점 정밀도 손실 방지 (API Decimal Loss Bug) |  |  |
| `DONE` | **`BUG-API-PK-GEN-1`** | Integrity | P2 | HTTP API 레코드 밀리초 PK 충돌 및 UUID 누락 해결 (API PK Gen Bug) |  |  |
| `DONE` | **`BUG-APPLY-REGEX-1`** | Integrity | P2 | CROSS/OUTER APPLY 정규식 및 맵 순회 왜곡 방지 (Apply Regex Bug) |  |  |
| `DONE` | **`BUG-ARITH-1`** | Integrity | P2 | 수식 0 초기화 방지 (Arithmetic Bug) |  |  |
| `DONE` | **`BUG-ARITH-EXPR-ZERO-1`** | Integrity | P2 | UPDATE 산술식 유연성 및 0 리셋 방지 (Arith Expr Zero Bug) |  |  |
| `DONE` | **`BUG-AUTH-ADMIN-PW-BYPASS-1`** | Integrity | P2 | 내장 슈퍼유저 비밀번호 인증 완전 우회 백도어 해결 (Auth Admin PW Bypass Bug) |  |  |
| `DONE` | **`BUG-AUTH-RBAC-ROLE-GRANT-IGNORE-1`** | Integrity | P2 | RBAC 사용자 커스텀 롤 테이블 권한 전수 누락 방지 (RBAC Role Grant Bug) |  |  |
| `DONE` | **`BUG-BACKUP-RESTORE-VIEW-OMISSION-1`** | Integrity | P2 | 백업 아카이브 생성 및 복원 시 뷰(View) 메타데이터 전무 누락 방지 (Backup Restore View Bug) |  |  |
| `DONE` | **`BUG-BOUNDS-1`** | Integrity | P2 | 인덱스 바운드 무작위 추출 방지 (Index Bound Bug) |  |  |
| `DONE` | **`BUG-CASE-1`** | Integrity | P2 | 컬럼 대소문자 정규화 (Case-Insensitive) |  |  |
| `DONE` | **`BUG-CATALOG-FK-MULTI-COL-ACTIONS-1`** | Integrity | P2 | 가상 카탈로그 복합 외래키 쉼표 병합 및 액션 왜곡 방지 (Catalog Multi Col FK Bug) |  |  |
| `DONE` | **`BUG-CATALOG-IDENT-1`** | Integrity | P2 | 가상 카탈로그 3-part/4-part 식별자 파싱 결함 해결 (Catalog Ident Bug) |  |  |
| `DONE` | **`BUG-CATALOG-INTERCEPT-BRACKET-1`** | Integrity | P2 | 가상 카탈로그 대괄호 식별자 인터셉트 보장 (Catalog Bracket Bug) |  |  |
| `DONE` | **`BUG-CATALOG-MSSQL-REGEX-UNICODE-1`** | Integrity | P2 | 가상 카탈로그 N'...' 리터럴 및 대괄호 식별자 매칭 (Catalog Regex Unicode Bug) |  |  |
| `DONE` | **`BUG-CATALOG-OBJECT-PERSISTENCE-OMISSION-1`** | Integrity | P2 | 서버 재기동 시 프로시저/함수/뷰/트리거 영구 증발 방지 (Catalog Object Persistence Bug) |  |  |
| `DONE` | **`BUG-CATALOG-ORDERBY-1`** | Integrity | P2 | 가상 카탈로그 ORDER BY 콤마 잔존 정렬 무력화 방지 (Catalog Order By Bug) |  |  |
| `DONE` | **`BUG-COORD-AGG-MIN-MAX-NON-NUMERIC-NIL-1`** | Integrity | P2 | 코디네이터 집계 MIN/MAX 비숫자형 파싱 실패 시 NULL 반환 방지 (Coord Agg MinMax Bug) |  |  |
| `DONE` | **`BUG-COPY-DELIM-1`** | Integrity | P2 | COPY FROM DELIMITER 후속 옵션 따옴표 변조 방지 (Copy Delimiter Bug) |  |  |
| `DONE` | **`BUG-COPY-PK-1`** | Integrity | P2 | COPY FROM 정수 PK 0 보존 (Copy PK Zero Bug) |  |  |
| `DONE` | **`BUG-CTE-MATERIALIZE-1`** | Integrity | P2 | CTE 임의 PK 강제 및 VARCHAR 타입 파괴 해결 (CTE Materialize Bug) |  |  |
| `DONE` | **`BUG-DATE-TODATETIMEOFFSET-INT-MINS-1`** | Integrity | P2 | TODATETIMEOFFSET 정수 분 단위 오프셋 시간 오해석 방지 (ToDateTimeOffset Mins Bug) |  |  |
| `DONE` | **`BUG-DATEADD-NULL-1`** | Integrity | P2 | DATEADD/EOMONTH 증분 NULL 시 NULL 정규 반환 (DateAdd NULL Bug) |  |  |
| `DONE` | **`BUG-DDL-COMPOSITE-PK-PARSE-1`** | Integrity | P2 | CREATE TABLE 복합 기본키 콤마 분해 및 스키마 보존 (DDL Composite PK Bug) |  |  |
| `DONE` | **`BUG-DDL-PK-1`** | Integrity | P2 | PK 임의 추측 방지 (DDL PK Guess Bug) |  |  |
| `DONE` | **`BUG-DDL-SPLIT-QUOTE-1`** | Integrity | P2 | CREATE TABLE splitColumnDefs 따옴표 추적 (DDL Split Quote Bug) |  |  |
| `DONE` | **`BUG-DECORRELATE-TRIM-1`** | Integrity | P2 | 비상관화 and 접미 단순 Trim 결함 해결 (Decorrelate Trim Bug) |  |  |
| `DONE` | **`BUG-DERIVED-TABLE-PK-GUESS-EMPTY-1`** | Integrity | P2 | 0건 파생테이블 Columns[0] 임의 PK 지정 및 제약 파괴 방지 (Derived Table PK Bug) |  |  |
| `DONE` | **`BUG-DML-EVAL-UPDATE-ARITH-CORRUPT-1`** | Integrity | P2 | 코디네이터 UPDATE 산술식 미지원 및 0값/타입변조 방지 (Eval Update Arith Bug) |  |  |
| `DONE` | **`BUG-DML-PK-RANDOM-1`** | Integrity | P2 | 비-id PK 테이블 DML 시 무작위 컬럼 PK 간주 해결 (DML Random PK Bug) |  |  |
| `DONE` | **`BUG-DML-PK-RANDOM-GUESS-1`** | Integrity | P2 | DML/DDL PK 식별 실패 시 임의 순회 행 훼손 방지 (DML PK Guess Bug) |  |  |
| `DONE` | **`BUG-DROP-INDEX-CASE-1`** | Integrity | P2 | DropIndex 대소문자 정규화 및 좀비 인덱스 방지 (Drop Index Case Bug) |  |  |
| `DONE` | **`BUG-EVAL-IN-COMMA-SPLIT-1`** | Integrity | P2 | WHERE IN 절 문자열 리터럴 쉼표 오절단 방지 (Eval IN Comma Bug) |  |  |
| `DONE` | **`BUG-EVAL-NEQ-NULL-1`** | Integrity | P2 | evalRowBinaryOp 불일치 비교 NULL 3가 논리 보장 (Eval NEQ NULL Bug) |  |  |
| `DONE` | **`BUG-EXEC-AGG-DISTINCT-IGNORED-1`** | Integrity | P2 | AggregateIterator DISTINCT 집계 무시 및 전체 행 집계 왜곡 방지 (Agg Distinct Ignored Bug) |  |  |
| `DONE` | **`BUG-EXEC-BUILDER-JOIN-COND-SPLIT-1`** | Integrity | P2 | 복합 조인 조건 '=' 분해 파괴 및 무제한 카테시안 곱 왜곡 방지 (Join Cond Split Bug) |  |  |
| `DONE` | **`BUG-EXEC-JOIN-STR-CASE-1`** | Integrity | P2 | HashJoin/MergeJoin 문자열 조인 키 대소문자 무관 매칭 보장 (Join Str Case Bug) |  |  |
| `DONE` | **`BUG-EXEC-PROC-TX-BYPASS-1`** | Integrity | P2 | EXEC 프로시저 호출 시 외부 트랜잭션 세션 우회 및 독립 자동 커밋 방지 (Exec Proc Tx Bypass Bug) |  |  |
| `DONE` | **`BUG-EXP-1`** | Integrity | P2 | EXPLAIN 서브쿼리 무작위 치환 방지 (Explain Bug) |  |  |
| `DONE` | **`BUG-FK-CASE-1`** | Integrity | P2 | 외래키 부모 컬럼 대소문자 정규화 (FK Case Bug) |  |  |
| `DONE` | **`BUG-FK-CHILD-CASE-1`** | Integrity | P2 | 외래키 자식 컬럼 대소문자 정규화 (FK Child Case Bug) |  |  |
| `DONE` | **`BUG-FK-PARENT-TABLE-CASE-1`** | Integrity | P2 | 외래키 부모 테이블 대소문자 정규화 (FK Parent Table Case Bug) |  |  |
| `DONE` | **`BUG-FOR-JSON-TABLE-ALIAS-WRAPPER-1`** | Integrity | P2 | FOR JSON PATH 테이블 별칭 중첩 객체 오해석 방지 (For JSON Alias Bug) |  |  |
| `DONE` | **`BUG-HTTP-TABLE-PK-1`** | Integrity | P2 | HTTP API 테이블 생성 시 임의 PK 승격 제거 (HTTP Table PK Bug) |  |  |
| `DONE` | **`BUG-INDEX-KEY-CASE-1`** | Integrity | P2 | 세컨더리 인덱스 키 추출 대소문자 정규화 (Index Key Case Bug) |  |  |
| `DONE` | **`BUG-INDEX-SCAN-ORDERBY-1`** | Integrity | P2 | 인덱스 스캔 다중 정렬 무시 및 정렬 누락 방지 (Index Scan Order By Bug) |  |  |
| `DONE` | **`BUG-JOIN-IDX-1`** | Integrity | P2 | 조인 인덱스 임의 선택 방지 (Join Index Guess Bug) |  |  |
| `DONE` | **`BUG-JOIN-PARSE-1`** | Integrity | P2 | 조인 조건 단순 Split 파싱 결함 수정 (Join Parse Bug) |  |  |
| `DONE` | **`BUG-MEM-INDEX-LOCAL-ORDER-1`** | Integrity | P2 | MemEngine 로컬 삽입 행 인덱스 정렬 보장 (MemEngine Index Order Bug) |  |  |
| `DONE` | **`BUG-MEM-SHARD-INIT-RACE-1`** | Integrity | P2 | TableData 샤드 지연 초기화 동시성 경쟁 런타임 패닉 방지 (MemEngine Shard Init Race Bug) |  |  |
| `DONE` | **`BUG-MEM-SKIPLIST-UNIQUE-INCLUDE-COPY-1`** | Integrity | P2 | SkipList 유니크 인덱스 재인덱싱 시 커버링 컬럼 복사본 갱신 방지 (SkipList Unique Include Bug) |  |  |
| `DONE` | **`BUG-MEM-TX-INDEX-CASE-1`** | Integrity | P2 | MemEngine IndexScan 인덱스명 대소문자 무관 탐색 보장 (MemEngine Index Case Bug) |  |  |
| `DONE` | **`BUG-MEM-TX-TABLE-CASE-1`** | Integrity | P2 | 트랜잭션 커밋 시 테이블 대소문자 사일런트 쓰기 증발 방지 (MemEngine Tx Case Bug) |  |  |
| `DONE` | **`BUG-MEM-WAL-CREATE-TABLE-INDEX-OMISSION-1`** | Integrity | P2 | WAL 복구 시 CREATE TABLE 인라인 인덱스 등록 누락 방지 (WAL Create Table Index Bug) |  |  |
| `DONE` | **`BUG-MERGE-IDENTITY-PK-1`** | Integrity | P2 | MERGE WHEN NOT MATCHED IDENTITY 자동 채번 보장 (Merge Identity PK Bug) |  |  |
| `DONE` | **`BUG-MERGE-NOT-MATCHED-SOURCE-SELF-DESTRUCT-1`** | Integrity | P2 | MERGE 구문 내 신규 삽입 행의 SOURCE 미일치 오인 즉각 삭제 방지 (Merge Self Destruct Bug) |  |  |
| `DONE` | **`BUG-MERGE-SET-1`** | Integrity | P2 | MERGE SET 절 다중 인자 수식 보존 (Merge Set Bug) |  |  |
| `DONE` | **`BUG-OCC-COMMIT-INTRA-TX-UNIQUE-1`** | Integrity | P2 | 단일 트랜잭션 내 다중 유니크 충돌 사일런트 커밋 방지 (OCC Intra Tx Unique Bug) |  |  |
| `DONE` | **`BUG-OCC-UPDATE-UNIQUE-CHECK-OMISSION-1`** | Integrity | P2 | MemEngine updateLocked 유니크 인덱스 사전 검증 누락 방지 (OCC Update Unique Bug) |  |  |
| `DONE` | **`BUG-ORDER-BY-SPLIT-1`** | Integrity | P2 | ORDER BY 다중 컬럼 및 대소문자 정합성 (Order By Bug) |  |  |
| `DONE` | **`BUG-PARSER-SIMPLE-CASE-OMISSION-1`** | Integrity | P2 | Pratt 파서 Simple CASE 피연산자 누락 및 문법 거부 방지 (Parser Simple Case Bug) |  |  |
| `DONE` | **`BUG-PGWIRE-DESCRIBE-1`** | Integrity | P2 | PGWire Describe 단순 문자열 분해 OID 왜곡 방지 (PGWire Describe Bug) |  |  |
| `DONE` | **`BUG-PGWIRE-PARAM-1`** | Integrity | P2 | PGWire 파라미터 경계 인식 치환 (PGWire Param Bug) |  |  |
| `DONE` | **`BUG-PGWIRE-PREP-EXEC-TX-BYPASS-1`** | Integrity | P2 | PGWire 사전 컴파일 바이트코드 실행 시 트랜잭션 우회 방지 (PGWire Prep Tx Bypass Bug) |  |  |
| `DONE` | **`BUG-PITR-DDL-SILENT-SWALLOW-1`** | Integrity | P2 | PITR WAL 재생 시 DDL 실행 에러 침묵 및 복구 실패 은폐 방지 (PITR DDL Swallow Bug) |  |  |
| `DONE` | **`BUG-PIVOT-COUNT-NULL-1`** | Integrity | P2 | PIVOT COUNT 집계 시 빈 카테고리 0 정규 반환 (Pivot Count NULL Bug) |  |  |
| `DONE` | **`BUG-PIVOT-ORDER-BY-SPLIT-1`** | Integrity | P2 | PIVOT/UNPIVOT ORDER BY 절 쉼표 분해 왜곡 방지 (Pivot Order By Split Bug) |  |  |
| `DONE` | **`BUG-PK-1`** | Integrity | P2 | 무결성 결함 수정 (Integrity Bug) |  |  |
| `DONE` | **`BUG-PLANNER-CBO-SELECTIVITY-CASE-SENSITIVE-1`** | Integrity | P2 | CBO 선택도 추정 시 컬럼명 대소문자 불일치 0.33 휴리스틱 추락 방지 (CBO Selectivity Case Bug) |  |  |
| `DONE` | **`BUG-PLANNER-DECORRELATE-OR-SUBQUERY-CORRUPT-1`** | Integrity | P2 | OR 조건 하위 서브쿼리 불법 SEMI 조인 변환 및 'and' 식별자 파괴 방지 (Subquery Decorrelate Bug) |  |  |
| `DONE` | **`BUG-PLANNER-ORDERBY-1`** | Integrity | P2 | CBO/SetOp 다중 정렬 키 콤마 잔존 해결 (Planner OrderBy Bug) |  |  |
| `DONE` | **`BUG-PLANNER-SPLIT-AND-QUOTE-ESCAPE-1`** | Integrity | P2 | WHERE 절 이스케이프 따옴표 및 대괄호 식별자 AND 오절단 방지 (Planner Split AND Bug) |  |  |
| `DONE` | **`BUG-PROC-SIMPLE-CASE-1`** | Integrity | P2 | 단순 CASE 문(Simple CASE) 파싱 및 컴파일 보장 (Simple CASE Bug) |  |  |
| `DONE` | **`BUG-PROJ-CAST-AS-1`** | Integrity | P2 | 수식 내 AS 오분해 방지 (Project Cast AS Bug) |  |  |
| `DONE` | **`BUG-RECORD-1`** | Integrity | P2 | 사일런트 유실 방지 (Silent Loss Bug) |  |  |
| `DONE` | **`BUG-SELECT-AGG-CASE-1`** | Integrity | P2 | GROUP BY 및 집계함수 대소문자 정합성 보장 (Select Agg Case Bug) |  |  |
| `DONE` | **`BUG-SELECT-INTO-PK-1`** | Integrity | P2 | SELECT INTO PK 임의 추측 방지 (Select Into PK Bug) |  |  |
| `DONE` | **`BUG-SP-EXEC-SUFFIX-OUT-1`** | Integrity | P2 | sp_executesql OUT 접미 변수명 오절단 방지 (SP Suffix OUT Bug) |  |  |
| `DONE` | **`BUG-SP-PARAM-DECIMAL-1`** | Integrity | P2 | sp_executesql 복합 파라미터 타입 파싱 보존 (SP Param Decimal Bug) |  |  |
| `DONE` | **`BUG-SPLIT-VALUES-1`** | Integrity | P2 | VALUES 튜플 중첩 수식 콤마 분해 정합성 (Split Values Bug) |  |  |
| `DONE` | **`BUG-STORAGE-CMP-DATETIME-STR-1`** | Integrity | P2 | DateTime-VarChar 상호 변환 누락 비교 역전 방지 (Storage Cmp DateTime Bug) |  |  |
| `DONE` | **`BUG-STORAGE-CMP-INT-FLOAT-1`** | Integrity | P2 | Value.Compare Int-Float 상호 강제변환 보장 (Compare Int-Float Bug) |  |  |
| `DONE` | **`BUG-STORAGE-EQUAL-FLOAT-STR-1`** | Integrity | P2 | 실수 6자리 문자열 포맷팅 동등 비교 왜곡 방지 (Storage Equal Float Bug) |  |  |
| `DONE` | **`BUG-STORAGE-TABLE-CASE-SENSITIVITY-1`** | Integrity | P2 | 스토리지 엔진 테이블명 대소문자 미구분 맵 조회 실패 및 중복 생성 방지 (Storage Table Case Bug) |  |  |
| `DONE` | **`BUG-STORAGE-TEMP-TABLE-PERSIST-LEAK-1`** | Integrity | P2 | 세션 임시테이블 스냅샷/WAL 영속화 및 재기동 후 영구 누수 방지 (Temp Table Persist Bug) |  |  |
| `DONE` | **`BUG-STORAGE-UPDATE-CASE-1`** | Integrity | P2 | 스토리지 Update 레코드 컬럼명 대소문자 정규화 (Storage Update Case Bug) |  |  |
| `DONE` | **`BUG-STR-CHARINDEX-NULL-ZERO-1`** | Integrity | P2 | CHARINDEX/STRPOS의 NULL 인자 수신 시 0 반환 삼치논리 왜곡 방지 (CharIndex NULL Zero Bug) |  |  |
| `DONE` | **`BUG-STR-NULL-1`** | Integrity | P2 | 문자열 함수 NULL 전파 보장 (String NULL Bug) |  |  |
| `DONE` | **`BUG-STRING-SPLIT-SEP-1`** | Integrity | P2 | STRING_SPLIT 빈 구분자 콤마 임의 간주 제거 (String Split Sep Bug) |  |  |
| `DONE` | **`BUG-SUBQ-1`** | Integrity | P2 | 스칼라 서브쿼리 무작위 반환 방지 (Subquery Bug) |  |  |
| `DONE` | **`BUG-TABLE-CASE-1`** | Integrity | P2 | 테이블명 대소문자 무관 탐색 보장 (Table Case Bug) |  |  |
| `DONE` | **`BUG-TDS-ROW-CASE-1`** | Integrity | P2 | TDS 행 직렬화 대소문자 정규화 (TDS Row Case Bug) |  |  |
| `DONE` | **`BUG-TDS-RPC-PARAM-NAME-ORDER-1`** | Integrity | P2 | TDS sp_executesql 파라미터명 미인식 순서 왜곡 방지 (TDS RPC Param Name Bug) |  |  |
| `DONE` | **`BUG-TDS-RPC-PREP-TX-BYPASS-1`** | Integrity | P2 | TDS RPC 사전 컴파일 플랜 실행 시 세션/트랜잭션 우회 방지 (TDS RPC Prep Tx Bypass Bug) |  |  |
| `DONE` | **`BUG-TUPLE-RECORD-CASE-1`** | Integrity | P2 | RecordToRow 대소문자 일치로 인한 NULL 덮어쓰기 방지 (Tuple Case Bug) |  |  |
| `DONE` | **`BUG-UPDATE-SET-COMMA-SPLIT-1`** | Integrity | P2 | 코디네이터 UPDATE SET 절 쉼표 오절단 방지 (Update Set Comma Bug) |  |  |
| `DONE` | **`BUG-UPDATE-SET-SPLIT-1`** | Integrity | P2 | UPDATE SET 절 다중 인자 수식 사일런트 유실 방지 (Update Set Split Bug) |  |  |
| `DONE` | **`BUG-UPSERT-1`** | Integrity | P2 | Upsert 충돌 정합성 (Upsert Bug) |  |  |
| `DONE` | **`BUG-VM-COMP-IDX-1`** | Integrity | P2 | VDBE 복합 인덱스 인코딩 정합성 (VM Composite Index Bug) |  |  |
| `DONE` | **`BUG-VM-DML-COL-CASE-1`** | Integrity | P2 | VDBE DML 컬럼명 대소문자 정규화 및 값 보존 (VM DML Col Case Bug) |  |  |
| `DONE` | **`BUG-VM-DML-PK-NOT-FOUND-CRASH-1`** | Integrity | P2 | VDBE OpUpdate/OpDelete PK 미존재 시 ErrKeyNotFound 예외 크래시 방지 (VM DML PK Not Found Bug) |  |  |
| `DONE` | **`BUG-VM-INSERT-DEFAULT-1`** | Integrity | P2 | VDBE OpInsert 상수 리터럴 기본값 주입 보장 (VM Insert Default Bug) |  |  |
| `DONE` | **`BUG-VM-INTO-1`** | Integrity | P2 | VDBE 변수대입 무작위 순회 방지 (IntoVar Bug) |  |  |
| `DONE` | **`BUG-VM-SUBQ-NIL-1`** | Integrity | P2 | VDBE 스칼라 서브쿼리 nil 패닉 및 컬럼 침묵 버림 방지 (VM Subquery Nil Bug) |  |  |
| `DONE` | **`BUG-VM-UPDATE-1`** | Integrity | P2 | VDBE UPDATE 컬럼명 정규화 (VM Update Silent Loss) |  |  |
| `DONE` | **`BUG-WAL-TORN-BOOT-1`** | Integrity | P2 | MemEngine WAL 테일 절단 시 기동 실패 방지 (WAL Torn Write Boot Bug) |  |  |
| `DONE` | **`BUG-WHERE-1`** | Integrity | P2 | WHERE IN 절 정합성 (WHERE IN Bug) |  |  |
| `DONE` | **`BUG-WINDOW-RANGE-PEER-EVAL-1`** | Integrity | P2 | 윈도우 함수 RANGE 피어 프레임 집계 왜곡 방지 (Window Range Peer Bug) |  |  |
| `DONE` | **`BUG-WIRE-COPY-OUT-UNESCAPED-1`** | Integrity | P2 | COPY TO STDOUT 텍스트 포맷 탭/줄바꿈 이스케이프 보장 (Copy Out Unescaped Bug) |  |  |
| `DONE` | **`BUG-WIRE-COPY-SILENT-CORRUPT-1`** | Integrity | P2 | COPY FROM STDIN 파싱 에러 침묵 및 0값/현재시각 변조 방지 (Copy Silent Corrupt Bug) |  |  |
| `DONE` | **`BUG-WIRE-COPY-TX-BYPASS-1`** | Integrity | P2 | COPY FROM STDIN 트랜잭션 세션 우회 및 독립 자동 커밋 방지 (Copy Tx Bypass Bug) |  |  |
| `DONE` | **`BUG-WIRE-PG-PARAM-SUBSTITUTION-INJECTION-1`** | Integrity | P2 | PGWire 파라미터 치환 시 재귀 치환 왜곡 및 숫자형 문자열 따옴표 탈락 방지 (PG Param Substitute Bug) |  |  |
| `DONE` | **`BUG-WT-FK-1`** | Integrity | P2 | WiredTiger 외래키 무결성 검증 구축 (WT FK Bug) |  |  |
| `DONE` | **`BUG-WT-INDEX-COMP-1`** | Integrity | P2 | WiredTiger 복합 인덱스 정합성 (WT Composite Index Bug) |  |  |
| `DONE` | **`BUG-WT-INDEX-DESC-1`** | Integrity | P2 | WiredTiger IndexScanDesc 역정렬 스텁 결함 해결 (WT Index Desc Bug) |  |  |
| `DONE` | **`BUG-WT-SWEEP-1`** | Integrity | P2 | WiredTiger 60분 유휴 세션 경고 방지 및 세션 리셋 (WT Sweep Server Bug) |  |  |
| `DONE` | **`BUG-WT-UNIQUE-INDEX-UNENFORCED-1`** | Integrity | P2 | WiredTiger 스토리지 엔진 보조 유니크 인덱스 제약 검증 및 색인 누락 방지 (WT Unique Index Bug) |  |  |
| `PENDING` | **`CFG-MEMORY-COMPACT-DEFAULT-1`** | STORAGE | P1 | 인메모리 컴팩트(compact) 모드 4대 게이트 검증 및 기본값 공식 승격 | [`notes/plans/STORAGE_OPTIONS_AND_MEMORY_COMPACT_PLAN.ko.md`](notes/plans/STORAGE_OPTIONS_AND_MEMORY_COMPACT_PLAN.ko.md) |  |
| `DONE` | **`CFG-STORAGE-OPT-1`** | CONFIG | P1 | 스토리지 옵션 계층 분리(--disk/--memory) 및 하위 호환성 매핑 | [`notes/plans/STORAGE_OPTIONS_AND_MEMORY_COMPACT_PLAN.ko.md`](notes/plans/STORAGE_OPTIONS_AND_MEMORY_COMPACT_PLAN.ko.md) |  |
| `PENDING` | **`COMPAT-DECLARATIVE-1`** | Durability | P2 | 외부 앱 호환성 규칙 선언적 외재화 (Declarative Compatibility Engine) |  |  |
| `DONE` | **`COMPAT-TABLE-DRIVEN-1`** | Durability | P2 | 10개 실전 서비스 호환 규칙 Table-Driven 구조 및 안티패턴 키워드 매칭 전면 정식화 | [`notes/plans/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md`](notes/plans/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md) |  |
| `PENDING` | **`DOC-REVAMP-10`** | Documentation | P2 | 공식 문서 및 엔지니어링 지식 체계 10대 핵심 고도화 마스터 플랜 | [`notes/plans/DOCUMENTATION_SYSTEM_UPGRADE_PLAN.ko.md`](notes/plans/DOCUMENTATION_SYSTEM_UPGRADE_PLAN.ko.md) |  |
| `DONE` | **`DOGFOOD-SPRINT-1`** | Performance | P1 | LionSQL 자체 백엔드 기반 정형 스프린트/태스크 관리 시스템 구축 (Dogfooding) |  |  |
| `IN_PROGRESS` | **`DURA-KILL-1`** | Durability | P2 | kill -9 크래시 복구 장기 라운드 검증 |  |  |
| `DONE` | **`DURA-SLT-1`** | Durability | P2 | SQLLogicTest (SLT) 블랙박스 정합성 오라클 검증 | [`notes/plans/SQLLOGICTEST_INTEGRATION_PLAN.ko.md`](notes/plans/SQLLOGICTEST_INTEGRATION_PLAN.ko.md) |  |
| `DONE` | **`DURA-SLT-2`** | Durability | P2 | SQLLogicTest 1.0 릴리즈 풀 스위트 (60,000+ 쿼리) 전수 인증 | [`notes/plans/SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md`](notes/plans/SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md) |  |
| `IN_PROGRESS` | **`DURA-SOAK-1`** | Durability | P2 | 24h ~ 72h 장기 소크 테스트 |  |  |
| `PENDING` | **`FEAT-MATVIEW-ALIAS-1`** | FEAT | P2 | Materialized View Virtual Alias & Tolerant Refresh (Mattermost/Enterprise) | [`notes/plans/MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md`](notes/plans/MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md) |  |
| `PENDING` | **`FEAT-PUREGO-WT-ENGINE`** | Storage | P3 | WiredTiger 호환 자체 Pure-Go MVCC 스토리지 엔진 구현 및 CGo 분리 (Post-1.0) |  |  |
| `PENDING` | **`FEAT-TIMESERIES-LSM-1`** | FEAT | P2 | LSM Time-Series Chunk Storage & time_bucket | [`notes/plans/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`](notes/plans/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md) |  |
| `DONE` | **`FEAT-TOLERANT-DDL-1`** | FEAT | P2 | Dynamic Runtime Tolerant DDL CLI Switch & PostgreSQL 42704 SQLSTATE | [`notes/plans/TOLERANT_DDL_MODE_PLAN.ko.md`](notes/plans/TOLERANT_DDL_MODE_PLAN.ko.md) |  |
| `PENDING` | **`FEAT-VECTOR-1`** | FEAT | P2 | Pure-Go Vector Search Engine (pgvector compat) | [`notes/plans/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`](notes/plans/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md) |  |
| `DONE` | **`FIX-SQLUTIL-PARSER-1`** | Engine | P2 | pkg/sqlutil SQL 파서/정규화기 결함 보강 및 렉서 공통화 | [`notes/plans/SQL_PARSER_HARDENING_PLAN.ko.md`](notes/plans/SQL_PARSER_HARDENING_PLAN.ko.md) |  |
| `DONE` | **`FRIENDLY-SQL`** | Ecosystem | P3 | DuckDB-Style Friendly SQL 문법 지원 | [`notes/plans/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md`](notes/plans/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md) |  |
| `DONE` | **`GA-REGEX-FINAL-2`** | Engine | P2 | CBO Planner 및 VDBE 잔여 정규식 100% 완전 제거 | [`notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md) |  |
| `DONE` | **`GA-REGEX-FINAL-3`** | Engine | P2 | 쿼리 실행 런타임 LIKE 정규식 100% 완전 제거 | [`notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md) |  |
| `PENDING` | **`GA-VALID-3`** | Ecosystem | P2 | 연동 앱 섀도 모드 실운영 도그푸딩 (Vaultwarden, Umami, Answer 등) |  |  |
| `DONE` | **`INTEG-ANSWER-1`** | Durability | P2 | Apache Answer (Q&A 지식베이스) |  |  |
| `PENDING` | **`INTEG-DIRECTUS-1`** | Ecosystem | P2 | Directus (Headless CMS/데이터 API) Fast-Fail 호환성 탐색 |  |  |
| `PENDING` | **`INTEG-GHOST-1`** | Ecosystem | P2 | Ghost (블로그/출판 플랫폼) Fast-Fail 호환성 탐색 |  |  |
| `PENDING` | **`INTEG-MATTERMOST-1`** | Ecosystem | P3 | Mattermost (팀 협업 채팅) — **[v1.1.0 로드맵]** |  |  |
| `DONE` | **`INTEG-METABASE-1`** | Durability | P2 | Metabase (BI 대시보드) | [`notes/plans/METABASE_INTEGRATION_PLAN.ko.md`](notes/plans/METABASE_INTEGRATION_PLAN.ko.md) |  |
| `PENDING` | **`INTEG-NOCODB-1`** | Ecosystem | P3 | NocoDB (노코드 DB 스튜디오) — **[v1.1.0 로드맵]** |  |  |
| `DONE` | **`INTEG-NOCODB-MATTERMOST-DEFER-1`** | Durability | P2 | NocoDB 및 Mattermost 연동 1.0 이후(v1.1.0) 연기 확정 |  |  |
| `DONE` | **`INTEG-UMAMI-1`** | Durability | P2 | Umami (웹 애널리틱스) |  |  |
| `DONE` | **`INTEG-VAULTWARDEN-1`** | Durability | P2 | Vaultwarden (비밀번호 관리) | [`notes/plans/VAULTWARDEN_INTEGRATION_PLAN.ko.md`](notes/plans/VAULTWARDEN_INTEGRATION_PLAN.ko.md) |  |
| `DONE` | **`INTEG-VIKUNJA-1`** | Durability | P2 | Vikunja (프로젝트 칸반 & To-Do) | [`notes/plans/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md`](notes/plans/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md) |  |
| `DONE` | **`INTEG-WOODPECKER-1`** | Durability | P2 | Woodpecker CI (CI/CD 엔진) | [`notes/plans/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md`](notes/plans/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md) |  |
| `PENDING` | **`LOC-1`** | Engine | P3 | MSSQL Collation 및 유니코드 다국어 로케일 정렬/비교 | [`notes/plans/COLLATION_AND_LOCALE_PLAN.md`](notes/plans/COLLATION_AND_LOCALE_PLAN.md) |  |
| `DONE` | **`M1`** | Ecosystem | P3 | v0.90.0 Feature Freeze & Production Hardening (100% PASS) |  |  |
| `DONE` | **`M2`** | Ecosystem | P3 | v0.90.0 액티브 스프린트 완료 과제 (18개 전량 완료 아카이브) |  | - [x] **`PERF-PGWIRE-PREP-TX`**: PGWi... |
| `DONE` | **`M3`** | Ecosystem | P3 | 내구성 및 신뢰성 1차 검증 완료 이력 |  |  |
| `IN_PROGRESS` | **`OCC-HYPER-2`** | STORAGE | P2 | In-Memory OCC Pointer-Free Flat Record A/B Benchmark & Implementation | [`notes/plans/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/plans/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md) |  |
| `DONE` | **`OPT-LARGE-JOIN-1`** | Durability | P2 | 대규모 조인(10+ Tables) 최적화 | [`notes/plans/LARGE_JOIN_OPTIMIZATION_PLAN.ko.md`](notes/plans/LARGE_JOIN_OPTIMIZATION_PLAN.ko.md) |  |
| `DONE` | **`P-2`** | Performance | P1 | WiredTiger VDBE 바이너리 튜플 지연 역직렬화 |  | - [x] **Phase 1 (`BENCH-P2-BASE`)**: ... |
| `PENDING` | **`P2-BACKLOG`** | Engine | P3 | Post-1.0 P2 Deferred Audit Items (92 Cases) | [`notes/POST_1.0_CANDIDATES.ko.md`](notes/POST_1.0_CANDIDATES.ko.md) |  |
| `DONE` | **`P3-INVARIANTS`** | Architecture | P3 | By-Design & Spec-Out Architecture Invariants (25 Cases) | [`notes/POST_1.0_CANDIDATES.ko.md`](notes/POST_1.0_CANDIDATES.ko.md) |  |
| `DONE` | **`PERF-BENCH-WT-RESTORE-1`** | Performance | P1 | WiredTiger Banking SP cmd/bench 5k->120k TPS 회귀 원인 분석 및 성능 복원 |  |  |
| `PENDING` | **`PERF-COMPLEX-QUERY-2`** | Ecosystem | P3 | Complex Query Flat Hash Join & Direct Slot Projection | [`notes/plans/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/plans/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md) |  |
| `DONE` | **`PERF-COMPLEX-QUERY-COLUMNAR`** | Architecture | P3 | 순수 Columnar Vectorization 전면 재작성 영구 기각 (Spec-Out) | [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md) |  |
| `DONE` | **`PERF-CTE-EPHEMERAL-1`** | Durability | P2 | 세션 로컬 에페머럴 CTE 버퍼 및 Zero-DDL 최적화 | [`notes/plans/SESSION_EPHEMERAL_CTE_BUFFER_PLAN.ko.md`](notes/plans/SESSION_EPHEMERAL_CTE_BUFFER_PLAN.ko.md) |  |
| `DONE` | **`PERF-CTE-LOWERING-VDBE-1`** | Durability | P2 | Writable CTE Stored Procedure 바이트코드 Lowering 가속 | [`notes/plans/WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md`](notes/plans/WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md) |  |
| `DONE` | **`PERF-GROVE-CONCURRENCY-1`** | Durability | P2 | Grove 벤치마크 기반 동시성 최적화 및 핫스팟 락 완성 | [`notes/plans/GROVE_BENCHMARK_CONCURRENCY_OPTIMIZATION_PLAN.ko.md`](notes/plans/GROVE_BENCHMARK_CONCURRENCY_OPTIMIZATION_PLAN.ko.md) | - [x] **Phase 2 (`PERF-HOTSPOT-LATCH-... |
| `PENDING` | **`PERF-HYBRID-TEMPMEM-1`** | STORAGE | P2 | WiredTiger 모드 임시 테이블 및 테이블 변수 인메모리 하이브리드 바이패스 | [`notes/plans/LOGICAL_NAMESPACE_DATABASE_PREFIX_PLAN.ko.md`](notes/plans/LOGICAL_NAMESPACE_DATABASE_PREFIX_PLAN.ko.md) |  |
| `DONE` | **`PERF-REGRESSION-RECOVERY-1`** | Performance | P1 | 성능 회귀 정상화 및 회귀 방지 게이트 구축 |  | - [x] **Phase 3 (`VERIFY-BENCH-RESTOR... |
| `DONE` | **`PERF-SERVER-PIPELINE-P3-P4`** | Ecosystem | P3 | 서버 파이프라인 Phase 3/4 | [`notes/plans/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`](notes/plans/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md) |  |
| `DONE` | **`PERF-VDBE-EXTREME-OPT-1`** | Durability | P2 | T-SQL VDBE 바이트코드 VM 극강의 성능 쥐어짜기 5대 혁신 | [`notes/plans/VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md`](notes/plans/VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md) | - [x] **Phase 1 (`PERF-VDBE-SLOT-1`)*... |
| `PENDING` | **`PKG-OFFLINE-DOCS`** | Ecosystem | P3 | 배포 패키지 오프라인 문서 번들링 (`docs/index.html`) | [`notes/plans/OFFLINE_DOCS_PACKAGING_PLAN.ko.md`](notes/plans/OFFLINE_DOCS_PACKAGING_PLAN.ko.md) |  |
| `DONE` | **`PLANNER-SLIM-1`** | Durability | P2 | & `PLANNER-AST-1~3` 플래너·엔진 정규식 완전 제거 및 순수 AST 전환 | [`notes/plans/PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md`](notes/plans/PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md) |  |
| `DONE` | **`R-6`** | Ecosystem | P3 | 세션 캡슐화 및 모듈 디커플링 (Lean 3단계 리팩토링) | [`notes/plans/MODULAR_ARCHITECTURE_PLAN.ko.md`](notes/plans/MODULAR_ARCHITECTURE_PLAN.ko.md) |  |
| `DONE` | **`REFACTOR-CATALOG-INTERCEPTOR-1`** | REFACTOR | P2 | Table-Driven Virtual Catalog Interceptor Refactoring | [`notes/plans/TOLERANT_DDL_MODE_PLAN.ko.md`](notes/plans/TOLERANT_DDL_MODE_PLAN.ko.md) |  |
| `DONE` | **`REFACTOR-OCC-FLAT-1`** | Storage | P2 | Flat 코덱 신뢰성 및 동시성 결함 보강 | [`notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md) |  |
| `PENDING` | **`REPL-STREAM-BIN-1`** | Replication | P2 | Replication 네트워크 Length-Prefixed Binary Frame Zero-Copy 스트리밍 |  |  |
| `PENDING` | **`RESEARCH-INPROCESS-REBOOT-USE`** | Research | P3 | [Post-1.0 초안] In-Process Graceful Reboot 기반 물리 서브폴더 USE DB 전환 아키텍처 연구 | [`notes/plans/IN_PROCESS_REBOOT_MULTI_DATABASE_PLAN.ko.md`](notes/plans/IN_PROCESS_REBOOT_MULTI_DATABASE_PLAN.ko.md) |  |
| `DONE` | **`ROUTING-AST-CATALOG-1`** | Durability | P2 | 심볼 테이블 & AST 기반 쿼리 라우팅 리팩토링 | [`notes/plans/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](notes/plans/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md) |  |
| `PENDING` | **`S-SYNTAX-EMPTY-PARENS`** | Engine | P3 | 빈 괄호 IN () 문법 평가 정책 (미결 검토) | [`notes/plans/EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md`](notes/plans/EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md) |  |
| `PENDING` | **`S3-1`** | Cloud | P2 | 클라이언트 사이드 S3 Pre-signed URL 및 SigV4 서명기 | [`notes/plans/S3_PRESIGNED_STORAGE_PLAN.md`](notes/plans/S3_PRESIGNED_STORAGE_PLAN.md) |  |
| `DONE` | **`SEC-BOOT-DEFAULT-PASS-WARN`** | SEC | P2 | 초기 패스워드 미변경 시 부팅 출력 최후행 대문자 보안 경고 불변식 | [`notes/plans/BOOT_DEFAULT_PASSWORD_WARNING_PLAN.ko.md`](notes/plans/BOOT_DEFAULT_PASSWORD_WARNING_PLAN.ko.md) |  |
| `DONE` | **`SEC-PGWIRE-PARAM-1`** | Wire | P2 | PGWire 달러 파라미터() 상태머신 토크나이저 전환 및 보안 강화 | [`notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md) |  |
| `DONE` | **`SQL-GOOSE-IDENTITY-1`** | Durability | P2 | PostgreSQL `GENERATED { ALWAYS | BY DEFAULT } AS IDENTITY` 지원 및 goose 마이그레이션 호환 |  |  |
| `DONE` | **`SQL-WRITABLE-CTE-1`** | Durability | P2 | PostgreSQL Writable CTE (`WITH ... INSERT/UPDATE/DELETE ... RETURNING`) 및 `::type[]` 캐스트 지원 |  |  |
| `DONE` | **`SSMS-OE-COMPAT-1`** | Ecosystem | P2 | SSMS Object Explorer 호환성 (sys.resource_governor_configuration, HostPlatform 배치 및 가상 카탈로그 수식 평가) |  |  |
| `IN_PROGRESS` | **`SSMS-OE-COMPAT-2`** | Ecosystem | P2 | SSMS Object Explorer 호환성 고도화 (Phase 1~4 테이블 목록 표시 완료, Phase 5 SMO NullReferenceException 및 하위 노드 바인딩) | [`notes/plans/SSMS_OBJECT_EXPLORER_COMPATIBILITY_PLAN.ko.md`](notes/plans/SSMS_OBJECT_EXPLORER_COMPATIBILITY_PLAN.ko.md) |  |
| `DONE` | **`TDS-MOD-1`** | Architecture | P3 | TDS 대규모 파일 모듈 분할 기각 (Spec-Out) | [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md) |  |
| `PENDING` | **`TEST-OCC-FLAT-1`** | Storage | P2 | Flat 모드 크래시 복구 무결성 검증 (AOF/RDB 및 kill -9) | [`notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md) |  |
| `PENDING` | **`TEST-OCC-FLAT-2`** | Storage | P2 | Flat 모드 복합 DDL/DML 회귀 검증 (복합 보조 인덱스, SkipList, FK) | [`notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md) |  |
| `PENDING` | **`TEST-OCC-FLAT-3`** | Storage | P2 | Flat 모드 쿼리 엔진 풀 스위트 회귀 검증 (SLT 및 Babelfish) | [`notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md) |  |
| `PENDING` | **`TEST-OCC-FLAT-4`** | Storage | P2 | Flat 모드 24h 장기 소크 및 가변 길이 UPDATE 단편화 검증 | [`notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md) |  |
| `DONE` | **`TUNE-DOC-SYNC-1`** | Documentation | P2 | 1.0 GA 벤치마크 및 공식 문서 실측치 일괄 동기화 |  |  |
| `DONE` | **`TUNE-HOTSPOT-OCC-1`** | Durability | P2 | 고동시성 핫스팟 OCC 경합 완화 및 와이어 버퍼 풀링 |  | - [x] **Phase 3 (`TUNE-EPHEMERAL-VERI... |
| `DONE` | **`TUNE-SP-FASTPATH-1`** | Durability | P2 | TPC-C Stored Procedure Fast-Path 직결 및 고동시성 In-Memory OCC 튜닝 | [`notes/plans/TPCC_STORED_PROCEDURE_FASTPATH_TUNING_PLAN.ko.md`](notes/plans/TPCC_STORED_PROCEDURE_FASTPATH_TUNING_PLAN.ko.md) | - [x] **Phase 4 (`TUNE-TPCC-BENCH-1`)... |
| `PENDING` | **`WAL-DELTA-1`** | Engine | P2 | Partial / Delta WAL 로깅 (UPDATE 컬럼 차분 비트마스킹) |  |  |
| `DONE` | **`WEB-SPA-1`** | Ecosystem | P3 | 단일 바이너리 내장 SPA 웹 서버 호스팅 | [`notes/plans/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](notes/plans/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md) |  |
| `DONE` | **`WT-LOCKDOWN-1`** | Storage | P1 | WiredTiger 고동시성 4계층 잠금 및 transaction_sync 영구 배제 | [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md) |  |

---

## 2. Detailed Task Specifications & Execution Records

### [IN_PROGRESS] 1.0 GA 검증 및 잔여 정리 `1.8`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Plan Doc**: [`notes/plans/GA_REGEX_DDL_REFACTORING_PLAN.ko.md`](notes/plans/GA_REGEX_DDL_REFACTORING_PLAN.ko.md)
- **Target Files**: `cmd/soaktest, cmd/crashtest, pkg/engine, pkg/sqlutil, pkg/api, pkg/sqlutil/predicate.go`

**Phases & Checklist**:
- [x] **`SLT-FIX-1~4` 및 SLT 러너 5대 기술부채 전면 해소**: 설계 계획서 [`notes/SLT_EXTENDED_SUITE_FIXES_PLAN.ko.md`](notes/SLT_EXTENDED_SUITE_FIXES_PLAN.ko.md)
- [x] **`GA-VALID-1`**: SQLite 공식 `index` / `random` / `evidence` 스위트 탐색 및 결함 전량 수정·검증 완료 (`test/slt/core/slt_extended_fixes.slt` 100% PASS)
- [x] **`SLT-FULL-100`**: SQLLogicTest 107개 풀세트 (551,217 쿼리) 100% 무결점 완치 (`notes/SLT_FULL_SUITE_100_PERCENT_PLAN.ko.md`, `notes/SLT_FULL_SUITE_100_PERCENT_REPORT.ko.md`, `scripts/verify_0.90.sh`)
- [x] 1. 프로젝션 별칭과 원본 컬럼명 충돌 및 동명 컬럼 슬롯 보존 완치 (`random/select/*` 10개 파일)
- [x] 2. `GROUP BY` 단항 부호 프로젝션 및 `HAVING NOT AVG(...) IS NULL` 중첩 집계 평가 완치 (`random/groupby/*` 14개 파일)
- [x] 3. SQLite 관용 문법 5종 (`total()`, 빈 `IN ()`, `CREATE TEMP VIEW`, `x IN table`, `CREATE TRIGGER ... UPDATE ON`) 및 테이블리스 집계 수용 (`evidence/*`, `random/expr/*`)
- [x] 4. `OR` + 서브쿼리 인덱스 바운드 방어 및 VDBE 3VL 비교 연산자(`OpEq`~`OpGte`, `OpBetween`) 정합성 완치 (`index/*`, `random/aggregates/*`)
- [x] 5. 107개 풀세트 (19,211 문장, 551,217 쿼리) 전수 재검증 100% PASS 입증 및 `scripts/verify_0.90.sh` 게이트 스크립트 작성 완료
- [-] **`GA-VALID-2`**: `cmd/soaktest` 연속 부하(OCC `19,613` tx / WiredTiger `19,678` tx, RSS/FD/고루틴 1.00 무누수 PASS) 및 `cmd/crashtest` 100라운드 확장 실행 완료 (WiredTiger `100/100` PASS `344,129` tx, OCC `100/100` PASS `301,184` tx; 24h/72h 초장기 소크 대기)
- [ ] **`GA-VALID-3`**: 연동 앱 2~3종 1~2주 도그푸딩
- [x] **`TEST-BABEL-SLT-1`**: Babelfish 기반 T-SQL Stored Procedure 전용 SLT 로직 테스트 스위트 구축 — **[완료 (Pass)]**
- [x] Phase 1: 아키텍처 및 커버리지 계획 수립 (`notes/BABELFISH_STORED_PROCEDURE_SLT_PLAN.ko.md`)
- [x] Phase 2: VDBE VM `OpPointSelect` 변수 전용 프로젝션(Variable-Only Assign) 결과셋 억제 보완 (`vm_dispatcher.go`)
- [x] Phase 3: `test/slt_proc/` 독립 스위트 디렉토리 및 고속 인메모리 테스트 러너(`slt_proc_test.go`) 구축
- [x] Phase 4: Babelfish T-SQL 6대 핵심 영역 `.slt` 파일 작성 및 전수 통과 (Variables, Flow, TryCatch, Tx, Cursors, DynamicSQL, 100% PASS, 0 Data Races)
- [x] **`GA-REGEX-DDL-1`**: `sqlexec_ddl.go` 및 `sqlexec_eval.go`/`sqlexec_explain.go` 잔여 정규식 렉서 전환 — **[완료 (Pass)]**
- [x] 1단계: `sqlexec_eval.go` & `sqlexec_explain.go` 조건식/설명문/서브쿼리/LIKE 정규식 제거 (`sqlutil.MatchLike`, `findSubqueryFromKind`)
- [x] 2단계: `sqlexec_ddl.go` 단순 DDL 라우팅(View, Table, Index, Stats, Truncate, Reindex, Functions) 토크나이저 전환
- [x] 3단계: `sqlexec_ddl.go` `CREATE TABLE` / `ALTER TABLE` 복합 제약조건 및 컬럼 파서 정규식 완전 박멸 (정규식 35+종 전량 제거, `"regexp"` import 0건)
- [x] 회귀 검증: `regex_ddl_migration_test.go` E2E 및 서브쿼리 테스트, `pkg/engine` / `pkg/sqlutil` / `pkg/api` 100% PASS
- [x] **`GA-REGEX-FINAL-1`**: 엔진 프로덕션 경로 잔여 정규식 4대 영역 완전 제거 및 100% Zero-Regex 달성 (`Invariant 7` 완성) — **[완료 (Pass)]**
- [x] **`GA-REGEX-NESTED-1`**: SQL 중첩/괄호/조건식 잔여 정규식 제거 및 sqlutil 추상화 모듈화 (`Invariant 7` 준수) — **[완료 (Pass)]**
- [x] **아키텍처 불변식 명문화**: `GEMINI.md` (Invariant 7 영어 요약) 및 `notes/ENGINEERING_CONTEXT.ko.md` (제1절 ⑮) 촘스키 계층론(Type 3 정규 언어 vs Type 2 CFG) 기반 SQL 구문 정규식 금지 및 선형 포맷 허용 경계 정의
- [x] **`pkg/sqlutil/predicate.go` 조건식 모듈 신설**: `ParseInPredicate`, `ParseIsNullPredicate`, `ParseAnyPredicate`, `ParseBetweenPredicate`, `StripParentheses` 추상화 및 단위 테스트(`predicate_test.go`) 100% PASS
- [x] **엔진·실행기·가상 카탈로그 정규식 일괄 박멸**:
- [x] **회귀 검증**: 3-Stage Pipeline (Build, Test `-race`) 100% PASS (0 data race)

**Details & Resolution**:
- 인수인계 및 실측 기록서: [`notes/GA_VALIDATION_HANDOFF_PLAN.ko.md`](notes/GA_VALIDATION_HANDOFF_PLAN.ko.md)
1. `SLT-FIX-1`: `pkg/slt/parser.go` 다중 `onlyif`/`skipif` 구조적 지시자 평가기(`shouldSkip`, OR 시맨틱스) 도입 및 조기 `halt` 오작동 완치
2. `SLT-FIX-2`: `pkg/planner/bounds.go` 및 `planner.go` 중첩 괄호 `AND` 평탄화, 동일 컬럼 다중 바운드 교집합 병합 및 인덱스 범위 잔여 필터 보존
3. `SLT-FIX-3`: `pkg/proc/` 및 `pkg/engine` `SELECT ALL` 수량자, 단항 `+` 연산자, `CAST(... AS SIGNED)` 및 `DIV` 정수 나눗셈 연산자 VDBE/Volcano 지원
4. `SLT-FIX-4`: `pkg/execution/aggregate.go` `GROUP BY` 사칙연산 프로젝션 AST 파싱 및 `matchColName` 식별자 오탐 차단
5. `SLT-RUNNER-DEBT`: `pkg/slt/` 및 `cmd/sqllogictest` 5대 기술부채 전면 해소 (`skipNext` 단일 불리언 제거, `ParseOptions`/`-target` 플래그로 타겟 방언 가변화, `FormatCell` `%.3f` SQLite SLT 표준 부동소수점 포맷팅, `statement error [pattern]` 에러 패턴 검증, `splitRowFields` 따옴표/탭/공백 안전 토크나이저 전환)
- 설계 계획서: [`notes/BABELFISH_STORED_PROCEDURE_SLT_PLAN.ko.md`](notes/BABELFISH_STORED_PROCEDURE_SLT_PLAN.ko.md)
- 설계 계획서: [`notes/GA_REGEX_DDL_REFACTORING_PLAN.ko.md`](notes/GA_REGEX_DDL_REFACTORING_PLAN.ko.md)
- **1) T-SQL 페이징 치환기 (`sqlexec_select.go`)**: `mssqlTopRe`, `mssqlDistinctTopRe`, `mssqlOffsetFetchRe` ➔ `sqlutil.NormalizeMSSQLPagination` 모듈화 및 토크나이저 전환 (`"regexp"` 제거)
- **2) SQLite 호환 테이블 IN 구문 (`sqlexec_select.go`)**: `reBareTableIn` (`WHERE x IN tbl`) ➔ `sqlutil.DesugarBareTableIn` 토크나이저 전환 (`"regexp"` 제거)
- **3) JDBC 메타데이터 배치 치환기 (`sqlexec_jdbc_metadata.go`)**: `reJdbcTempSpExec`, `reJdbcSelectFrom` ➔ `sqlutil.SplitTopLevel` 및 `FindTopLevelKeyword` 전환 (`"regexp"` 제거)
- **4) 가상 카탈로그 GUI 필터링 (`virtual/mssql.go`, `virtual/pg.go`)**: `reMssqlObjectIDEq`, `reGuiRelnameEq` 등 9개 정규식 ➔ `sqlutil.ExtractEqualityFilter`, `ExtractObjectIdCall` 모듈화 전환 (`"regexp"` 제거)
- **검증**: `go test -tags wiredtiger -count=1 -v -race ./pkg/...` 전수 PASS 및 10대 실전 앱/메타베이스/드라이버 회귀 검증 완료 (0 data race)
- `pkg/execution/aggregate.go`: 9개 집계함수 정규식 제거 및 Pratt AST 기반 파싱 전환 (`"regexp"` 제거)
- `pkg/execution/filter.go`: `inRe` 제거 및 `sqlutil.ParseInPredicate`/`ParseIsNullPredicate` 적용
- `pkg/catalog/virtual/result.go`: `reEvalInClause`, `reEvalAnyClause`, `reEvalIsNull`, `reEvalIsNotNull` 제거 및 `sqlutil` 조건식 파서 전환
- `pkg/catalog/virtual/mssql.go`: `reMssqlObjectIdIn`, `reMssqlParentObjectIdIn` 제거
- `pkg/engine/sqlexec_merge.go`: `VALUES`, `AS alias(cols)`, `INSERT...VALUES` 정규식 제거 및 `StripParentheses` 전환 (`"regexp"` 제거)
- `pkg/engine/sqlexec_derived.go`, `sqlexec_dbcc.go`, `sqlexec_select.go`: 인라인 파렌/토크나이저 전환
- `pkg/wire/server.go`, `pkg/wire/tds/server.go`: 문자열/주석 스캐너 및 인덱스 슬라이싱 전환

---

### [PENDING] 기타 엔터프라이즈 기능 확장 `3.9`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:55

**Phases & Checklist**:
- [ ] `AUDIT-LOG-1`: 암호화 SHA-256 불변 감사 로그 서브시스템 ([`notes/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/IMMUTABLE_AUDIT_LOG_PLAN.md))
- [ ] `TDS-COMPAT-6`: Multiple Result Sets (MARS) 스트리밍 (Dapper `QueryMultiple`)
- [ ] `S3-1` ~ `S3-4`: 클라이언트 사이드 S3 Pre-signed URL 직접 업로드 및 SigV4 서명기 ([`notes/S3_PRESIGNED_STORAGE_PLAN.md`](notes/S3_PRESIGNED_STORAGE_PLAN.md))
- [ ] `LOC-1` ~ `LOC-4`: MSSQL Collation 및 로케일 정렬 지원 ([`notes/COLLATION_AND_LOCALE_PLAN.md`](notes/COLLATION_AND_LOCALE_PLAN.md))
- [ ] `SEC-1` / `AUTH-1`: TDS 8.0 Pre-Login SSL/TLS 핸드셰이크 강화 및 클라이언트 사이드 OIDC 토큰 교환

---

### [PENDING] AI 에이전트용 컨텍스트 문서 (llms.txt, AGENTS.md 템플릿) `AI-STARTER-1`
- **Domain**: Ecosystem | **Priority**: P2 | **Updated**: 2026-11-09 07:57:37
- **Plan Doc**: [`notes/AI_APP_STARTER_PLAN.ko.md`](notes/AI_APP_STARTER_PLAN.ko.md)

---

### [PENDING] 랜딩 AI 앱 스타터 섹션 및 Go/Node/Python 최소 예제 `AI-STARTER-2`
- **Domain**: Ecosystem | **Priority**: P2 | **Updated**: 2026-11-09 07:57:37
- **Plan Doc**: [`notes/AI_APP_STARTER_PLAN.ko.md`](notes/AI_APP_STARTER_PLAN.ko.md)

---

### [PENDING] 호환성 매트릭스 문서 (되는 것/안 되는 것) `AI-STARTER-3`
- **Domain**: Ecosystem | **Priority**: P2 | **Updated**: 2026-11-09 07:57:38
- **Plan Doc**: [`notes/AI_APP_STARTER_PLAN.ko.md`](notes/AI_APP_STARTER_PLAN.ko.md)

---

### [PENDING] Go 임베디드 모드 타당성 조사 (SQLite 대비) `AI-STARTER-4`
- **Domain**: Research | **Priority**: P3 | **Updated**: 2026-11-09 07:57:38
- **Plan Doc**: [`notes/AI_APP_STARTER_PLAN.ko.md`](notes/AI_APP_STARTER_PLAN.ko.md)

---

### [PENDING] ANTLR4 기반 T-SQL 문법 검증 및 미지원 기능 정밀 진단 오라클 아키텍처 `ARCH-ANTLR-ORACLE-1`
- **Domain**: Architecture | **Priority**: P2 | **Updated**: 2026-11-09 21:52:36
- **Plan Doc**: [`notes/plans/ANTLR4_SYNTAX_ORACLE_DIAGNOSTICS_PLAN.ko.md`](notes/plans/ANTLR4_SYNTAX_ORACLE_DIAGNOSTICS_PLAN.ko.md)

---

### [PENDING] 프리픽스 기반 논리적 네임스페이스 및 테이블 변수 통합 아키텍처 `ARCH-LOGICAL-NAMESPACE-1`
- **Domain**: ENGINE | **Priority**: P2 | **Updated**: 2026-11-10 00:53:23
- **Plan Doc**: [`notes/plans/LOGICAL_NAMESPACE_DATABASE_PREFIX_PLAN.ko.md`](notes/plans/LOGICAL_NAMESPACE_DATABASE_PREFIX_PLAN.ko.md)

---

### [DONE] & `ARCH-RULE-SLUG`) `ARCH-PRED`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: `pkg/sqlutil/predicate.go`

**Phases & Checklist**:
- [x] **`ARCH-PRED-1` & `ARCH-PRED-2`**: 조건식·괄호(`Dyck Language`) 표준 파서(`pkg/sqlutil/predicate.go`) 구축, PostgreSQL JSON 연산자(`->`, `->>`) 충돌 방어, `reEvalBinaryOp` 정규식 완전 제거
- [x] **`ARCH-RULE-SLUG`**: 골든 룰 불변 슬러그(`[RULE:xxx]`) 및 캐노니컬 명칭 체계(`Rule Reference Policy`) 수립, 전체 소스코드 주석 및 문서 내 휘발성 순번 완전 제거
- [x] **`REV-ENG-011`**: `ALTER TABLE` 10개 서브 커맨드 내 스토리지 엔진 에러 무시(`_ = `) 결함 전면 해소 및 에러 반환 보장 ([`RULE:ExplicitError`])
- [x] **`REV-WIRE-010`**: PGWire `handleParse` / `handleBind` 비정상/음수/과대 슬라이스 할당 가드 적용 ([`RULE:DefensiveIndexing`], [`RULE:BoundedBuffers`])
- [x] **`REV-CAT-003`**: 가상 카탈로그 행 필터링 잔여 정규식 제거 및 `sqlutil.ParseBinaryComparison` 일원화 (`Invariant 7`, [`RULE:ModularAbstraction`])
- [x] **감사 대시보드 동기화**: `CODE_REVIEW.ko.md`, `CODE_REVIEW.md`, `REFACTORING.ko.md` 최신화 완료 (누적 결함 36건 100% 조치 완료)

---

### [DONE] AST 및 카탈로그 기반 2-Tier 쿼리 라우팅 정규화 (Anti-Heuristic Refactoring) `ARCH-VDBE-ROUTING-1`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:55
- **Plan Doc**: [`notes/plans/ARCH_VDBE_ROUTING_1_PLAN.ko.md`](notes/plans/ARCH_VDBE_ROUTING_1_PLAN.ko.md)
- **Target Files**: `pkg/engine/session.go, pkg/engine/coordinator_impl.go, pkg/api/server.go, pkg/proc/characteristics.go, pkg/wire/server.go`

**Phases & Checklist**:
- [x] **`ClientSession.Dialect` 정식 enum 도입**: `DialectDefault`, `DialectTSQL`, `DialectPostgres`, `DialectMySQL` 타입 시스템 구축 및 `cs.IsPostgres()`, `cs.IsTSQL()` 제공으로 문자열 꼼수 박멸 (`pkg/engine/session.go`, `pkg/engine/coordinator_impl.go`, `pkg/api/server.go`)
- [x] **PostgreSQL 트랜잭션 실패 규칙 및 Identity Insert 정규화**: `sqlexec.go`, `sqlexec_dml.go`, `sqlexec_select.go` 내 `strings.HasPrefix(cs.ID, "pg-")`를 `cs.IsPostgres()`로 전면 교체
- [x] **파생 테이블 AST 특성 기반 판별**: `proc.QueryCharacteristics`에 `HasDerivedTable` 필드 추가 및 `canCompileToVDBE()` 내 `FROM (` 문자열 슬라이싱 휴리스틱 완전 삭제 (`pkg/proc/characteristics.go`, `pkg/wire/server.go`)
- [x] **회귀 검증**: 3-Stage Pipeline (Build, Test `-race`) 100% PASS (0 data race)

**Details & Resolution**:
- **설계 계획서**: [`notes/ARCH_VDBE_ROUTING_1_PLAN.ko.md`](notes/ARCH_VDBE_ROUTING_1_PLAN.ko.md)
- **목표**: `pkg/wire` 및 `pkg/engine`의 문자열 매칭(`strings.HasPrefix(cs.ID, "pg-")`, `FROM (` 슬라이싱) 휴리스틱을 완전 제거하고, AST 노드와 카탈로그/세션 Dialect 기반의 견고한 역량 검증으로 전환

---

### [DONE] 전 소스코드 135단계 듀얼 AI 정밀 코드 리뷰 및 엣지 케이스 감사 `AUDIT-135-DUAL-REVIEW`
- **Domain**: Integrity | **Priority**: P1 | **Updated**: 2026-11-07 14:02:55
- **Plan Doc**: [`notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md`](notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md)
- **Metrics**: 135/135 스코프 완주 (총 134건 결함 식별 및 등록 완료)

**Phases & Checklist**:
- [x] Scopes 001 ~ 012 (도메인 1: 스토리지 엔진 - In-Memory OCC Core) 완주
- [x] Scopes 013 ~ 024 (도메인 2: 스토리지 엔진 - WiredTiger MVCC Core & 공통 스토리지) 완주
- [x] Scopes 025 ~ 034 (도메인 3: 카탈로그 & 가상 시스템 메타데이터) 완주
- [x] Scopes 035 ~ 055 (도메인 4: 통합 쿼리 코디네이터 & DDL/DML 실행기) 완주
- [x] Scopes 056 ~ 065 (도메인 5: T-SQL 파서 & 바이트코드 컴파일러) 완주
- [x] Scopes 066 ~ 075 (도메인 6: T-SQL VDBE 런타임 가상머신) 완주
- [x] Scopes 076 ~ 083 (도메인 7: Volcano Cost-Based Optimizer 플래너) 완주
- [x] Scopes 084 ~ 095 (도메인 8: Volcano 실행 반복자 & 메모리 거버너) 완주
- [x] Scopes 096 ~ 105 (도메인 9: 네트워크 와이어 프로토콜 - PGWire & TDS) 완주
- [x] Scopes 106 ~ 113 (도메인 10: 서버 인프라, HA 복제, 백업/PITR & CDC) 완주
- [x] Scopes 114 ~ 119 (도메인 11: SQL 유틸리티, 공통 파서, 보안/인증 및 엔진 설정) 완주
- [x] Scopes 120 ~ 128 (도메인 12: REST API, RLS 보안, 백그라운드 서비스 & 내장 웹 SPA) 완주
- [x] Scopes 129 ~ 135 (도메인 13: CLI, 데몬, 마이그레이션 도구 & 닥터) 완주

---

### [DONE] 12대 고복잡도 핵심 코어 구역 다중 결함 심층 전수 감사 (Deep Core Precision Rescan) `AUDIT-CORE-DEEP-RESCAN`
- **Domain**: Integrity | **Priority**: P1 | **Updated**: 2026-11-07 14:02:55
- **Plan Doc**: [`notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md`](notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md)

**Phases & Checklist**:
- [x] 핵심 코어 1구역: 스토리지 엔진 코어 (`wt_engine.go`, `memengine.go`, `tx.go`, `persistence.go`) 완주 (7건 심층 결함 발굴)
- [x] 핵심 코어 2구역: 트랜잭션 코디네이터 (`coordinator.go`, `coordinator_impl.go`, `session.go`, `session_pool.go`) 완주 (5건 심층 결함 발굴)
- [x] 핵심 코어 3구역: 바이트코드 컴파일러 & VDBE VM (`compiler.go`, `runtime/vm.go`, `vm_dispatcher.go`) 완주 (6건 심층 결함 발굴)
- [x] 핵심 코어 4구역: CBO 플래너 & 실행 오퍼레이터 (`planner.go`, `join.go`, `sort.go`, `builder.go`) 완주 (5건 심층 결함 발굴)
- [x] 핵심 코어 5구역: 멀티 와이어 & 복제 엔진 (`wire/server.go`, `tds/packet.go`, `replication/broadcaster.go`, `replication/client.go`) 완주 (5건 심층 결함 발굴)
- [x] 핵심 코어 6구역: T-SQL 렉서 & 파서 코어 (`pkg/proc/lexer.go`, `pkg/proc/parser.go`, `pkg/proc/parser_expr.go`, `pkg/proc/parser_dml.go`) 완주 (5건 심층 결함 발굴)
- [x] 핵심 코어 7구역: 시스템 카탈로그 & DDL 메타데이터 코어 (`pkg/catalog/catalog.go`, `pkg/catalog/virtual/interceptor.go`) 완주 (5건 심층 결함 발굴)
- [x] 핵심 코어 8구역: SQL 실행 엔진 본체 (`pkg/engine/sqlexec_dml.go`, `sqlexec_ddl.go`, `sqlexec_merge.go`, `sqlexec_cursor.go`) 완주 (5건 심층 결함 발굴)
- [x] 핵심 코어 9구역: 백업, 복구 & PITR 시점 복구 (`pkg/backup/backup.go`, `restore.go`, `pitr.go`, `wal_archive.go`) 완주 (5건 심층 결함 발굴)
- [x] 핵심 코어 10구역: 고급 분석 실행 오퍼레이터 (`pkg/execution/aggregate.go`, `window.go`, `merge_join.go`, `setop.go`) 완주 (5건 심층 결함 발굴)
- [x] 핵심 코어 11구역: 인메모리 스토리지 코어 잔여 (`pkg/storage/memengine/skiplist.go`, `table_data.go`, `flat_record.go`, `wal.go`) 완주 (5건 심층 결함 발굴)
- [x] 핵심 코어 12구역: CDC 스트리밍 & 인증/RBAC 보안 (`pkg/cdc/streamer.go`, `outbox.go`, `pkg/auth/auth.go`, `pkg/auth/rbac.go`) 완주 (5건 심층 결함 발굴)

---

### [PENDING] 암호화 SHA-256 체이닝 불변 감사 로그 서브시스템 `AUDIT-LOG-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Plan Doc**: [`notes/plans/IMMUTABLE_AUDIT_LOG_PLAN.md`](notes/plans/IMMUTABLE_AUDIT_LOG_PLAN.md)

---

### [DONE] 전수 감사 P0 (Must Fix 37선) 결함 TDD 순차 패치 및 회귀 검증 `AUDIT-PATCH-P0`
- **Domain**: Integrity | **Priority**: P1 | **Updated**: 2026-11-08 09:59:06
- **Plan Doc**: [`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`](notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md)

**Phases & Checklist**:
- [x] Batch 1: 보안 및 접근 제어 코어 (P0-06 ~ P0-10, 5건) TDD 패치
- [ ] Batch 2: 스토리지, FlatRecord & WAL 영속성 (P0-01 ~ P0-05, 5건) TDD 패치
- [ ] Batch 3: 백업, 복원 & 카탈로그 메타데이터 (P0-15 ~ P0-17, P0-20 ~ P0-22, 6건) TDD 패치
- [ ] Batch 4: T-SQL 렉서, 파서 & CBO 플래너 (P0-23 ~ P0-25, P0-28, 4건) TDD 패치
- [ ] Batch 5: 실행 오퍼레이터, 와이어 & 동시성 VM (P0-11 ~ P0-14, P0-18 ~ P0-19, P0-26, P0-27, P0-29, P0-30, 10건) TDD 패치

---

### [IN_PROGRESS] 1.0 전수 감사 P1 (Should Fix 47선) 난이도별(하/중/상) 순차 안정화 패치 `AUDIT-PATCH-P1`
- **Domain**: Core | **Priority**: P1 | **Updated**: 2026-11-09 09:07:26
- **Plan Doc**: [`notes/plans/P1_AUDIT_STABILIZATION_PLAN.ko.md`](notes/plans/P1_AUDIT_STABILIZATION_PLAN.ko.md)
- **Metrics**: 1단계(하 21건, Mini-Batch 1~6) 100% 완주 완료 | 2단계(중 18건) 대기

**Phases & Checklist**:
- [x] Mini-Batch 1 (pkg/proc/runtime, 4건): 3-2, 3-3, 3-4, 3-5 (1fcc772)
- [x] Mini-Batch 2 (pkg/storage 코덱 & SkipList, 3건): Scope-010, Scope-018, Scope-008 (f56f7d2)
- [x] Mini-Batch 3 (pkg/storage 라이프사이클, 3건): 1-3, Scope-001, Scope-003 (03f37c9)
- [x] Mini-Batch 4 (pkg/execution & pkg/planner, 4건): 4-3, 4-4, 10-5, 4-5 (5e7bfce)
- [x] Mini-Batch 5 (pkg/engine & pkg/proc, 4건): 8-5, Scope-049, 3-6, 2-5 (990ff01)
- [x] Mini-Batch 6 (cmd/lionsql, 3건): Scope-132, Scope-133, Scope-131 (cc5850c)
- [ ] 2단계 난이도 중 (Medium, 18건): 도메인 A(5건), 도메인 B(6건), 도메인 C(7건)
- [ ] 3단계 난이도 상 (High, 8건)

---

### [DONE] 전수 감사 완료 직후 공식 Triage(결함 분류) 회의 및 패치 범위 확정 `AUDIT-TRIAGE-POST`
- **Domain**: Integrity | **Priority**: P1 | **Updated**: 2026-11-07 14:02:55
- **Plan Doc**: [`notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md`](notes/plans/PRE_RELEASE_AUDIT_PLAN.ko.md)

**Phases & Checklist**:
- [ ] P0 (Must Fix) 선별: 데이터 손상, 크래시, 데드락, 카탈로그 영속화 누락 ➔ 1.0 릴리즈 전 필수 패치 및 TDD 회귀 테스트
- [ ] P1 (Should Fix) 선별: 표준 SQL 동작 불일치, 일반적 메모리 누수 ➔ 1.0 패치 세트 편입
- [ ] P2 (Won't Fix) 기각: 실무 발생 희박한 극단 엣지 케이스 ➔ 복잡도 억제 사유 명시 후 클로즈
- [ ] P3 (Spec-Out / By-Design) 확인: KISS, Fast-Fail 원칙 등에 입각한 의도적 설계 ➔ 아키텍처 문서 명시

---

### [DONE] 원클릭 벤치마크 및 종합 검증 스위트 (`scripts/run_all_benchmarks.sh`) 구축 `BENCH-ALL-IN-ONE-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Metrics**: - [x] **Phase 1~4 (성능/와이어 실측)**: Banking SP(142k+ TPS, 100% 검증), Storage Kernel TPC-C(246만+ tpmC), VDBE Runtime TPC-C(82.7만+ tpmC), Network PGWire(21.6만+ tpmC) 및 TDS(13.9만+ tpmC) 완주

**Phases & Checklist**:
- [x] **Phase 0 (자동 빌드)**: `lionsql`, `bench`, `tpcc-kernel`, `tpcc-runtime`, `tpcc-client`, `sqllogictest` 6개 바이너리 일괄 컴파일
- [x] **Phase 1~4 (성능/와이어 실측)**: Banking SP(142k+ TPS, 100% 검증), Storage Kernel TPC-C(246만+ tpmC), VDBE Runtime TPC-C(82.7만+ tpmC), Network PGWire(21.6만+ tpmC) 및 TDS(13.9만+ tpmC) 완주
- [x] **Phase 5~6 (정합성/크래시 복구)**: SQLLogicTest 코어 32개 파일 167 쿼리 100% PASS, `kill -9` 돌발 사살 후 콜드 재기동 50/50 무손실 복구
- [x] **Phase 7 (리포트 자동화 & 격리)**: 시스템 하드웨어 캡처, POSIX/Bash 3.2+ 호환, 완전 무잔여 `trap` 프로세스 사살, 마크다운 리포트(`notes/benchmark_runs/`) 보존 (`scripts/run_all_benchmarks.sh`, `scripts/README.md`)

**Details & Resolution**:
- 목표: `BENCHMARK.md`를 관통하는 7대 핵심 성능 및 정합성/내구성 벤치마크를 원클릭으로 순차 실행하고 요약표와 마크다운 리포트를 자동 생성.

---

### [PENDING] Mini TPC-H Benchmark Suite & CBO 서브쿼리 Decorrelation `BENCH-TPCH-1`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:55
- **Plan Doc**: [`notes/plans/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/plans/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)

**Details & Resolution**:
- TPC-H Q1, Q3, Q4, Q6, Q9 스위트 및 `EXISTS`/`IN` -> Semi-Join 자동 변환 ([`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md))

---

### [DONE] 기본값 없는 NOT NULL 컬럼 추가 시 NULL 오염 방지 (Add Column Null Bug) `BUG-ADD-COL-NOT-NULL-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/ddl.go:500-502``

**Details & Resolution**:
데이터 존재 테이블에 NOT NULL 컬럼 추가 시 기존 행을 NULL로 채워 무결성을 파괴하는 결함 방지

- 기존 데이터가 존재하는 테이블에 기본값 없는 `NOT NULL` 컬럼 추가 시 기존 행을 무조건 `storage.NullValue`로 채워 NOT NULL 제약조건을 사일런트 파괴하는 결함.
- 데이터 존재 시 기본값 없는 `NOT NULL` 컬럼 추가를 거부하거나 기본값 평가 연동.

---

### [DONE] AggregateIterator Decimal-정수 혼합 합계 유실 방지 (Agg Mixed Decimal Bug) `BUG-AGG-MIXED-NUM-DEC-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/execution/aggregate.go:408-428, 573-594``

**Details & Resolution**:
그룹 내 Decimal 출현 시 비-Decimal 수치 Decimal 변환 및 합산으로 수치 누락 방지

- Volcano `AggregateIterator`에서 단일 그룹 내에 `Decimal`과 일반 숫자(`Int`, `Float`)가 혼합되어 있을 때, 비-Decimal 값들을 `acc.sums`에 누적해두고 최종 결과 방출 시 `acc.isDecimal` 플래그로 인해 `acc.decSums`만 단독 반환함으로써 비-Decimal 수치 합계를 전량 사일런트 유실시키는 결함.
- 그룹 내 Decimal 출현 시 기존 및 후속 비-Decimal 수치를 `Decimal`로 변환하여 누적 합산함으로써 수치 누락 방지.

---

### [DONE] HTTP API 레코드 생성 시 NOT NULL DEFAULT 필드 주입 (API Create Default Bug) `BUG-API-CREATE-DEFAULT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/api/handlers_records.go:437-462``

**Details & Resolution**:
생략된 필드 대상 스키마 기본값(리터럴 및 함수 기본값) 자동 주입으로 400 에러 해결

- HTTP API `POST /api/records/:table` 처리 시 `col.Default`를 검사하지 않아, `NOT NULL DEFAULT ...` 컬럼이 요청 페이로드에서 생략되면 400 에러(`field cannot be null`)로 거부되고, 기본값이 있는 nullable 컬럼은 기본값 대신 `NULL`이 삽입되던 결함.
- 누락된 컬럼에 대해 스키마 기본값(리터럴 및 함수 기본값)을 자동 주입하도록 보강.

---

### [DONE] HTTP API 레코드 조회 시 DATETIME 빈 문자열 직렬화 방지 (API JSON DateTime Bug) `BUG-API-JSON-DATETIME-EMPTY-STR-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/api/handlers_records.go:53`, `pkg/storage/engine.go:466``

**Details & Resolution**:
`recordToJSONMap`에서 `v.SVal` 대신 `v.String()` / `v.TVal` 정규 포맷팅으로 일시 데이터 유실 해결

- `DateTimeValue` 생성 시 `SVal`을 비워두는(`""`) 반면, HTTP API의 `recordToJSONMap` 스키마 분기에서 `default: row[col.Name] = v.SVal`을 직접 읽어냄으로써 REST API로 레코드 조회 시 모든 DATETIME/DATE/TIME 컬럼이 빈 문자열(`""`)로 방출되던 일시 데이터 유실 결함.
- `v.SVal` 직접 참조 대신 `v.String()` 또는 `v.TVal` 기반 ISO-8601 포맷팅으로 직렬화 수정.

---

### [DONE] HTTP API 레코드 직렬화 시 DECIMAL 실수 부동소수점 정밀도 손실 방지 (API Decimal Loss Bug) `BUG-API-JSON-DECIMAL-PRECISION-LOSS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/api/handlers_records.go:46-51``

**Details & Resolution**:
`strconv.ParseFloat` 변환을 제거하고 정밀 문자열 또는 `json.Number`로 직렬화하여 금융 데이터 왜곡 방지

- `recordToJSONMap`이 DECIMAL 컬럼을 직렬화할 때 `strconv.ParseFloat(v.String(), 64)`로 무조건 변환하여 부동소수점(`float64`)으로 변환함으로써, 금융/통화 데이터에서 IEEE-754 이진 부동소수점 오차(예: `0.1000000000000000055511...`)가 발생하던 정밀도 파괴 결함.
- DECIMAL 컬럼을 고정 소수점 문자열 또는 `json.Number`로 직렬화하여 금융 연산 정밀도 보존.

---

### [DONE] HTTP API 레코드 밀리초 PK 충돌 및 UUID 누락 해결 (API PK Gen Bug) `BUG-API-PK-GEN-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/api/handlers_records.go:449-455``

**Details & Resolution**:
나노초/카운터 결합 유니크 채번 및 UUID PK 자동 생성 지원으로 PK 충돌 및 NULL 방지

- `CreateRecord`에서 정수 PK 미제공 시 나노초가 아닌 밀리초(`UnixNano() / int64(time.Millisecond)`)로 채번하여 동일 밀리초 내 동시 요청 시 PK 충돌 에러가 발생하고, VarChar/Int 외 타입(예: UUID) 미처리로 `storage.NullValue`가 PK로 강제 삽입되는 결함.
- 나노초 타임스탬프 또는 원자적 카운터 결합 유니크 채번 및 UUID PK 자동 생성(`uuid.New().String()`) 보장.

---

### [DONE] CROSS/OUTER APPLY 정규식 및 맵 순회 왜곡 방지 (Apply Regex Bug) `BUG-APPLY-REGEX-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_apply.go:214-222``

**Details & Resolution**:
`buildParameterizedApplyQuery` 단순 정규식 치환 및 무작위 맵 순회로 키워드/리터럴 오염 방지

- TVF 실행 시 `buildParameterizedApplyQuery`에서 컬럼명을 `\b%s\b` 정규식으로 단순 문자열 치환하여, `id` 컬럼이 SQL 키워드나 리터럴을 훼손하거나 컬럼 치환 순서가 Go 맵 순회에 의존하는 결함.
- 토큰 인식 파라미터 치환 및 결정론적 컬럼 순회 적용.

---

### [DONE] 수식 0 초기화 방지 (Arithmetic Bug) `BUG-ARITH-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_eval.go:925``

**Details & Resolution**:
`evalUpdateExpr` 수식 평가 시 컬럼명 대소문자 무관 탐색으로 컬럼 0 리셋 방지

- `evalUpdateExpr`에서 `SET price = Price + 10`과 같은 수식 평가 시 `rec[colName]` 대소문자 불일치로 조회가 실패하면 `parseLiteralValue`로 떨어져 컬럼 값이 0으로 강제 리셋되던 결함.
- `rec` 조회 시 대소문자 무관 탐색을 보장하여 정상 연산 값 반환.

---

### [DONE] UPDATE 산술식 유연성 및 0 리셋 방지 (Arith Expr Zero Bug) `BUG-ARITH-EXPR-ZERO-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_eval.go:19, 920-966``

**Details & Resolution**:
`[Price] + 10`, `a + b`, `10 + a` 등 수식 매칭 실패로 인한 컬럼 0 리셋 방지

- `evalUpdateExpr`의 정규식이 `col <op> num` 형태만 엄격하게 매칭하여, 대괄호 컬럼(`[Price] + 10`), 다중 컬럼 산술(`subtotal + tax`), 상수 선행(`10 + price`) 수식이 들어왔을 때 매칭에 실패하고 `parseLiteralValue`로 떨어져 대상 컬럼 값을 조용히 `0`으로 리셋하는 결함.
- 대괄호 식별자, 다중 컬럼 참조 및 상수 선행 연산식을 포괄하는 유연한 수식 평가기 도입.

---

### [DONE] 내장 슈퍼유저 비밀번호 인증 완전 우회 백도어 해결 (Auth Admin PW Bypass Bug) `BUG-AUTH-ADMIN-PW-BYPASS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/wire/server.go:561-565`, `pkg/wire/tds/server.go:702-706``

**Details & Resolution**:
PGWire 및 TDS에서 비밀번호가 설정된 `sa`, `postgres`, `admin`의 무암호 로그인 우회 차단

- 글로벌 인증 플래그가 비활성화된 상태에서 PGWire 및 TDS 리스너가 `authSvc.GetLogin(user)`로 등록된 계정 비밀번호 해시가 존재함에도 `if !auth.IsBuiltinAdminLogin(user) { needsPassword = true }`를 두어, `sa`, `postgres`, `admin` 등 슈퍼유저 계정에 명시적 비밀번호가 설정되어 있어도 패스워드 검증 없이 무조건 무암호 접속을 허용하던 보안 백도어 결함.
- 관리자가 슈퍼유저 비밀번호를 설정(`login.PasswordHash != ""`)한 경우 내장 관리자 계정이라도 반드시 비밀번호 인증을 거치도록 보안 가드레일 수정.

---

### [DONE] RBAC 사용자 커스텀 롤 테이블 권한 전수 누락 방지 (RBAC Role Grant Bug) `BUG-AUTH-RBAC-ROLE-GRANT-IGNORE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/auth/rbac.go:371-432``

**Details & Resolution**:
`HasTablePermission`에서 내장 롤만 검사하고 사용자 소속 커스텀 롤(`u.Roles`) 권한 누락으로 인한 인가 실패 해결

- `HasTablePermission`이 하드코딩된 내장 역할(`db_owner`, `db_datareader`, `db_datawriter`), 사용자 개별 권한(`normUser`), 및 `public` 권한만 검사하고, 사용자가 부여받은 사용자 정의 데이터베이스 롤(`u.Roles`)을 전혀 검사하지 않아 커스텀 롤에 부여된 모든 테이블 권한(`GRANT ... TO role_name`)이 해당 롤 소속 사용자에게 무효화되던 치명적 인가 결함.
- `u.Roles`의 모든 커스텀 역할에 대해 `checkSubject(r)`를 순회 검사하여 역할 기반 테이블 권한 정상 부여.

---

### [DONE] 백업 아카이브 생성 및 복원 시 뷰(View) 메타데이터 전무 누락 방지 (Backup Restore View Bug) `BUG-BACKUP-RESTORE-VIEW-OMISSION-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
`CreateBackup`/`RestoreBackup`에 `catalog/views.json` 직렬화 및 복원 파이프라인을 추가하여 뷰 정의 영구 유실 방어

---

### [DONE] 인덱스 바운드 무작위 추출 방지 (Index Bound Bug) `BUG-BOUNDS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/planner/bounds.go:389``

**Details & Resolution**:
`ParseIndexCond` 무작위 맵 순회 제거 및 인덱스 대상 컬럼 바운드 정밀 매칭

- Volcano `IndexScanIterator` 생성 시 `ParseIndexCond`가 `for colName, valStr := range eqColVals { return &parsed, &parsed }` 무작위 맵 순회를 수행하여, 다중 등호 조건 시 인덱스 컬럼과 무관한 임의의 컬럼 값으로 인덱스를 검색하여 결과가 파괴되던 결함.
- 대상 인덱스 컬럼과의 정합성 매칭으로 수정.

---

### [DONE] 컬럼 대소문자 정규화 (Case-Insensitive) `BUG-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/engine.go:317``

**Details & Resolution**:
`TableSchema.FindColumn`의 `strings.EqualFold` 대소문자 무관 탐색 보장

- `TableSchema.FindColumn`이 `s.Columns[i].Name == name` 엄격 비교로 작성되어 DML, DDL, MERGE, ON CONFLICT, PK 바이패스, 복합 인덱스 범위 플래너에서 대소문자 불일치 시 컬럼 조회 실패를 초래하던 결함.
- `strings.EqualFold`를 적용하여 대소문자 무관 탐색을 보장.

---

### [DONE] 가상 카탈로그 복합 외래키 쉼표 병합 및 액션 왜곡 방지 (Catalog Multi Col FK Bug) `BUG-CATALOG-FK-MULTI-COL-ACTIONS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/catalog/virtual/mssql.go:369-375``

**Details & Resolution**:
`execDbGateMSSQLForeignKeySelect` 복합 외래키 단일 행 쉼표 결합 및 CASCADE/SET NULL 액션 누락 방지

- `execDbGateMSSQLForeignKeySelect`가 복합 외래키 컬럼들을 단일 행에 콤마(`strings.Join(..., ", ")`)로 결합하고, `updateAction`과 `deleteAction`을 무조건 `"NO ACTION"`으로 하드코딩하여 클라이언트(DbGate, SSMS, DBeaver) 스키마 분석 시 복합 외래키가 단일 이상 컬럼(`"col1, col2"`)으로 인식되고 실제 `CASCADE`/`SET NULL` 제약이 은폐되던 결함.
- 복합 외래키 컬럼 쌍마다 개별 메타데이터 행 방출 및 `fk.OnUpdate.String()`, `fk.OnDelete.String()` 실측 액션 바인딩.

---

### [DONE] 가상 카탈로그 3-part/4-part 식별자 파싱 결함 해결 (Catalog Ident Bug) `BUG-CATALOG-IDENT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/catalog/virtual/result.go:372``

**Details & Resolution**:
점 단위 분할 후 최종 식별자 정규 트림 적용으로 `[master].[sys].[tables]` 정상 인식 보장

- 가상 카탈로그 `cleanIdentifier`에서 `strings.Index(s, ".")`를 사용하여 첫 번째 점을 기준으로 슬라이스함으로써, `[master].[sys].[tables]`와 같은 3-part/4-part 쿼리 시 `sys].[tables`라는 깨진 문자열이 남아 카탈로그 조회가 깨지던 결함.
- 점 단위 분할 후 최종 식별자 정규 트림 적용.

---

### [DONE] 가상 카탈로그 대괄호 식별자 인터셉트 보장 (Catalog Bracket Bug) `BUG-CATALOG-INTERCEPT-BRACKET-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/catalog/virtual/interceptor.go:113-160``

**Details & Resolution**:
정규화 단계 대괄호 및 스키마 접두사 트림으로 SSMS/sqlcmd 가상 뷰 조인 쿼리 100% 정상 인터셉트

- `IsVirtualCatalogJoinQuery`에서 대괄호(`[`와 `]`)를 제거하지 않고 `"SYS.TABLES"`, `"SYS.COLUMNS"` 등의 문자열을 매칭하여, SSMS/sqlcmd 표준 쿼리(`FROM [sys].[tables] t JOIN [sys].[columns] c`) 인입 시 가상 카탈로그 조인 인터셉터가 동작하지 않고 물리 테이블 조회로 떨어져 `table not found` 에러가 발생하는 결함.
- 정규화 단계에서 대괄호(`[` 및 `]`) 및 스키마 접두사를 정규 트림하여 가상 카탈로그 조인 쿼리 100% 정상 인터셉트 보장.

---

### [DONE] 가상 카탈로그 N'...' 리터럴 및 대괄호 식별자 매칭 (Catalog Regex Unicode Bug) `BUG-CATALOG-MSSQL-REGEX-UNICODE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/catalog/virtual/mssql.go:17-18``

**Details & Resolution**:
`N'...'` 접두사 및 대괄호 허용 견고한 카탈로그 정규식으로 SSMS/DBeaver 전수 덤프 방지

- 가상 카탈로그의 `reMssqlTableNameEq` 및 `reMssqlSchemaNameEq` 정규식이 일반 문자열(`='...'`)만 기대하여, SSMS/sqlcmd/DBeaver 등 표준 클라이언트가 전송하는 유니코드 리터럴(`WHERE t.name = N'users'`) 및 대괄호 식별자(`WHERE [t].[name] = ...`) 매칭에 실패, 전체 테이블의 메타데이터를 불필요하게 덤프 반환하던 결함.
- `N'...'` 접두사 및 대괄호 식별자를 허용하는 견고한 카탈로그 정규식으로 고도화.

---

### [DONE] 서버 재기동 시 프로시저/함수/뷰/트리거 영구 증발 방지 (Catalog Object Persistence Bug) `BUG-CATALOG-OBJECT-PERSISTENCE-OMISSION-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
인메모리 맵 보관 객체들을 WAL 레코드 또는 `catalog_objects.json`으로 영속화하여 데이터베이스 재시작 후 메타데이터 복원 보장

---

### [DONE] 가상 카탈로그 ORDER BY 콤마 잔존 정렬 무력화 방지 (Catalog Order By Bug) `BUG-CATALOG-ORDERBY-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/catalog/virtual/result.go:34`, `pkg/engine/sqlexec_dbcc.go:310``

**Details & Resolution**:
`strings.Fields` 분해로 인해 `orderCol`에 콤마가 남아(`"col1,"`) 카탈로그 정렬이 무력화되던 결함 해결

- `sys.tables` 등 가상 뷰 및 DBCC 명령에서 `strings.Fields(orderByClause)`로 정렬 컬럼을 파싱하여 다중 컬럼 정렬 시 `orderCol`에 콤마가 포함(`"col1,"`)되어 정렬이 완전히 무력화되던 결함.
- 콤마 분해 및 개별 컬럼 트림 적용.

---

### [DONE] 코디네이터 집계 MIN/MAX 비숫자형 파싱 실패 시 NULL 반환 방지 (Coord Agg MinMax Bug) `BUG-COORD-AGG-MIN-MAX-NON-NUMERIC-NIL-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
`strconv.ParseFloat` 전용 파싱을 문자열/일시/불리언 비교 인터페이스로 확장하여 문자열 및 날짜 컬럼의 극값 유실 해결

---

### [DONE] COPY FROM DELIMITER 후속 옵션 따옴표 변조 방지 (Copy Delimiter Bug) `BUG-COPY-DELIM-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/wire/copy.go:136-145``

**Details & Resolution**:
DELIMITER 뒤 후속 옵션 존재 시 닫는 따옴표(`'`)를 구분자로 파싱하여 CSV 파싱이 깨지는 결함 해결

- PGWire `COPY FROM`의 `DELIMITER` 파싱 시 `rest[dIdx+10:]`을 단순 슬라이스하여 `WITH (FORMAT CSV, DELIMITER '|', HEADER)`와 같이 후속 옵션이 올 때 `dSub[0]`이 닫는 따옴표(`'`)를 구분자로 파싱하여 CSV 전체가 깨지던 결함.
- 토큰 단위 파싱 및 따옴표 제거 정합화.

---

### [DONE] COPY FROM 정수 PK 0 보존 (Copy PK Zero Bug) `BUG-COPY-PK-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/wire/copy.go:338-342``

**Details & Resolution**:
PGWire `COPY FROM` 시 정수 기본키 `0`을 미지정으로 오해하여 Unix 에포크 타임스탬프로 위조하는 결함 수정

- PGWire `COPY FROM` 처리 시 `curPK.IVal == 0` 조건을 "PK 미지정"으로 오해하여, 사용자가 적법하게 임포트한 정수 PK `0`을 현재 Unix 에포크 타임스탬프(`time.Now().UnixNano()...`)로 덮어써 데이터를 훼손하던 결함.
- 컬럼 누락(`!exists`) 또는 `IsNull`인 경우에만 자동 생성을 허용하고 수치 `0`은 온전히 보존.

---

### [DONE] CTE 임의 PK 강제 및 VARCHAR 타입 파괴 해결 (CTE Materialize Bug) `BUG-CTE-MATERIALIZE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_explain.go:862-868``

**Details & Resolution**:
첫 번째 컬럼 임의 PK 선언으로 인한 중복 키 에러 방지 및 원본 쿼리 데이터 타입 메타데이터 보존

- CTE 구체화 임시 테이블(`#__cte_*`) 생성 시 `if i == 0 { pk = " PRIMARY KEY" }`로 첫 번째 컬럼을 멋대로 PK로 선언하여 중복 값이 있을 때 `duplicate key violation` 에러로 실패하고, 모든 컬럼을 `VARCHAR(255)`로 선언해 데이터 타입을 파괴하던 결함.
- 임의 PK 지정 제거 및 원본 쿼리 데이터 타입 메타데이터 보존.

---

### [DONE] TODATETIMEOFFSET 정수 분 단위 오프셋 시간 오해석 방지 (ToDateTimeOffset Mins Bug) `BUG-DATE-TODATETIMEOFFSET-INT-MINS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/builtins_date.go:441-458, 733``

**Details & Resolution**:
T-SQL 규격 정수 오프셋(분 단위)을 시간 단위로 60배 왜곡 포맷팅하던 `parseOffsetLocation` 파싱 정합성 보장

- T-SQL 표준 규격에서 `TODATETIMEOFFSET(dt, tz)`의 두 번째 인자가 정수일 경우 '분(Minute)' 단위 오프셋(예: `60` = 1시간 = `+01:00`, `-120` = -2시간)을 의미함에도 불구하고, `parseOffsetLocation`이 콜론이 없는 경우 이를 시간(`hours`)으로 취급하여 `60`을 `+60:00`(60시간)으로 60배 왜곡 변환하던 결함.
- 인자가 정수형 문자열(콜론 미포함)일 경우 분 단위 오프셋으로 정규 변환하도록 보정.

---

### [DONE] DATEADD/EOMONTH 증분 NULL 시 NULL 정규 반환 (DateAdd NULL Bug) `BUG-DATEADD-NULL-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/builtins_date.go:567-584, 635-645``

**Details & Resolution**:
증분 파라미터 NULL 검사 및 NULL 즉시 반환, 미지원 datepart 에러 정규화

- `DATEADD` 및 `EOMONTH`에서 증분 파라미터(`n`, `monthsToAdd`)의 NULL 여부를 검사하지 않아, `DATEADD(day, NULL, '2024-01-01')`이나 `EOMONTH(date, NULL)` 호출 시 `n=0`으로 오판되어 NULL 대신 원본 날짜를 그대로 반환하는 결함.
- 인자 목록 중 하나라도 NULL일 경우 표준 규격에 따라 `storage.NullValue`를 즉시 정규 반환하도록 수정.

---

### [DONE] CREATE TABLE 복합 기본키 콤마 분해 및 스키마 보존 (DDL Composite PK Bug) `BUG-DDL-COMPOSITE-PK-PARSE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_ddl.go:89-98``

**Details & Resolution**:
괄호 내부 콤마 분해 루프로 선언된 모든 복합 PK 컬럼 플래그 정상 부여

- `CREATE TABLE` 시 테이블 레벨 복합 기본키(`PRIMARY KEY (col1, col2)`) 구문을 파싱할 때 괄호 내부를 콤마로 분해하지 않고 `"col1, col2"` 전체를 단일 컬럼명으로 취급하여 어떤 컬럼도 PK로 매칭되지 못하고, 결국 첫 번째 컬럼(`Columns[0]`)만 임의 PK로 지정되어 복합 PK 테이블 스키마가 파괴되던 결함.
- 괄호 내부 콤마 분해 루프를 적용하여 선언된 모든 복합 PK 컬럼에 `PrimaryKey = true` 플래그 정상 부여.

---

### [DONE] PK 임의 추측 방지 (DDL PK Guess Bug) `BUG-DDL-PK-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_ddl.go:185`, `pkg/api/handlers_tables.go:162``

**Details & Resolution**:
PK 미지정 테이블 생성 시 `Columns[0]`을 멋대로 PK로 승격하는 안티패턴 제거 및 가상 PK(`_row_id`) 보장

- `CREATE TABLE` 시 사용자가 PK를 지정하지 않은 경우 `if !hasPK && len(schema.Columns) > 0 { schema.Columns[0].PrimaryKey = true }`를 통해 첫 번째 컬럼(예: `created_at` 또는 `category`)을 임의로 Primary Key로 강제 승격시켜, 이후 동일 값의 행 INSERT 시 중복 키 위반 에러를 일으키는 치명적 결함.
- 임의 컬럼 PK 승격을 영구 제거하고, 스토리지 내부 가상 PK(`_row_id`)를 투명하게 생성하도록 정규화.

---

### [DONE] CREATE TABLE splitColumnDefs 따옴표 추적 (DDL Split Quote Bug) `BUG-DDL-SPLIT-QUOTE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_ddl.go:737-767``

**Details & Resolution**:
`DEFAULT 'hello, world'` 콤마 및 `DEFAULT '('` 괄호로 인한 컬럼 정의 절단/합쳐짐 해결

- `splitColumnDefs`가 괄호 중첩만 추적하고 문자열 따옴표를 추적하지 않아, `DEFAULT 'hello, world'`와 같이 문자열 내 콤마 분해 시 후속 구문을 새 컬럼으로 오인하거나 `DEFAULT '('`와 같은 괄호로 인해 `depth`가 영구히 >0으로 남아 이후 모든 컬럼이 1개로 뭉개지는 결함.
- 따옴표 상태 추적기를 결합하여 문자열 리터럴 내부의 콤마 및 괄호 무시 보장.

---

### [DONE] 비상관화 and 접미 단순 Trim 결함 해결 (Decorrelate Trim Bug) `BUG-DECORRELATE-TRIM-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/planner/decorrelate.go:501-508, 473``

**Details & Resolution**:
`removeClause`에서 `"and"` 단순 슬라이스로 `Poland` ➔ `Pol` 등 리터럴 손상 방지 토큰 경계 검사 적용

- `removeClause`에서 `strings.TrimSuffix(before, "and")` 단순 슬라이스로 인해 `country = 'Poland'` 등 `"and"`로 끝나는 리터럴의 뒷부분을 잘라(`'Pol'`) 문법 에러 및 조건 훼손을 유발하고, `findKeywordIndex`의 따옴표 미추적으로 리터럴 내 키워드를 오인하던 결함.
- 토큰 경계 검사 및 따옴표 상태 추적 적용.

---

### [DONE] 0건 파생테이블 Columns[0] 임의 PK 지정 및 제약 파괴 방지 (Derived Table PK Bug) `BUG-DERIVED-TABLE-PK-GUESS-EMPTY-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_derived.go:116-118``

**Details & Resolution**:
0건 반환 시 임의 컬럼 PK 승격을 중단하고 항상 가상 기본키(`_dt_row_id`)를 부여하여 스키마 정합성 보장

- `resolveDerivedTables`에서 서브쿼리 실행 결과가 0건일 때 `Columns[0]`을 임의로 PRIMARY KEY(`PrimaryKey: true, Nullable: false`)로 지정하여, 첫 번째 컬럼이 NULL 허용이거나 중복값을 갖는 경우 파생 테이블 임시 스키마 제약이 파괴되던 결함.
- 결과가 0건이거나 고유 컬럼이 확정되지 않은 경우 항상 합성 가상 기본키(`_dt_row_id`)를 부여하도록 일원화.

---

### [DONE] 코디네이터 UPDATE 산술식 미지원 및 0값/타입변조 방지 (Eval Update Arith Bug) `BUG-DML-EVAL-UPDATE-ARITH-CORRUPT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_eval.go:920-965``

**Details & Resolution**:
0으로 나누기 예외 처리 및 컬럼 간 연산 시 문자열 폴백 방지로 데이터 사일런트 변조 차단

- 코디네이터 폴백 `evalUpdateExpr`에서 단일 숫자 가감승제 정규식만 매칭하고, 0으로 나누기(`delta == 0`)나 컬럼 간 연산(`salary = salary + bonus`) 발생 시 실패를 무시한 채 `parseLiteralValue`로 떨어져 값을 0으로 만들거나 정수 컬럼에 문자열 값을 대입하여 데이터를 사일런트 변조하던 결함.
- 0으로 나누기 시 명시적 예외 반환 및 컬럼 참조 산술식 평가 기능 보강.

---

### [DONE] 비-id PK 테이블 DML 시 무작위 컬럼 PK 간주 해결 (DML Random PK Bug) `BUG-DML-PK-RANDOM-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_dml.go:1071-1078, 1228-1235``

**Details & Resolution**:
`ExecuteUpdate`/`ExecuteDelete` 폴백의 `rec["id"]` 하드코딩 및 맵 순회 임의 컬럼 PK 간주 결함 해결

- `ExecuteUpdate` 및 `ExecuteDelete` 폴백에서 `rec["id"]`를 하드코딩하고 없을 때 `for _, v := range rec { pkVal = v; break }`로 무작위 컬럼을 PK로 간주하여 `tx.Update`/`tx.Delete`를 호출함으로써 엉뚱한 행을 수정/삭제하는 치명적 데이터 파괴 결함.
- `schema.PrimaryKeyColumn()`을 통한 정규 PK 룩업 보장.

---

### [DONE] DML/DDL PK 식별 실패 시 임의 순회 행 훼손 방지 (DML PK Guess Bug) `BUG-DML-PK-RANDOM-GUESS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/runtime/vm.go:940-945, 1076-1081`, `pkg/engine/sqlexec_dml.go:1071-1078, 1228-1235`, `pkg/engine/sqlexec_ddl.go:349, 363-366``

**Details & Resolution**:
`Values[0]` 및 `for range rec` 무작위 순회 완전 삭제, 공식 PK 엄격 조회로 엉뚱한 행 훼손 원천 차단

- VDBE(`execUpdateWhere`, `execDeleteWhere`), 코디네이터(`execUpdate`, `execDelete`), DDL(`TRUNCATE TABLE`)에서 대상 행의 PK를 식별하지 못할 때 `row.Values[0]`으로 임의 추측하거나 Go 맵의 `for _, v := range rec { pkVal = v; break }` 무작위 순회로 첫 번째 컬럼 값을 PK로 임의 간주하여 무관한 엉뚱한 행을 갱신/삭제/트렁케이트하던 치명적 무결성 결함.
- 임의 컬럼 추측 및 무작위 Go 맵 순회를 완전 삭제하고, 스키마 공식 기본키(`schema.PrimaryKeyColumn()`)를 엄격 조회하며 미지정 시 에러를 반환하도록 정규화.

---

### [DONE] DropIndex 대소문자 정규화 및 좀비 인덱스 방지 (Drop Index Case Bug) `BUG-DROP-INDEX-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/ddl.go:148-154``

**Details & Resolution**:
`td.Indexes` 맵 삭제 시 공식 인덱스 명칭(`foundIdx.Name`) 삭제로 좀비 인덱스 잔존 방지

- `DropIndex`에서 인덱스 스키마 탐색은 `FindIndex`로 대소문자 무관하게 수행한 뒤, 인덱스 맵 삭제 시 인자로 전달된 원문 대소문자(`delete(td.Indexes, indexName)`)를 사용하여 대소문자가 다를 경우 실제 `td.Indexes`에서 삭제되지 않고 좀비 인덱스로 영구 잔존하는 결함.
- 정규화된 공식 인덱스 명칭(`foundIdx.Name`)을 키로 `delete`하여 완벽한 인덱스 정리 보장.

---

### [DONE] WHERE IN 절 문자열 리터럴 쉼표 오절단 방지 (Eval IN Comma Bug) `BUG-EVAL-IN-COMMA-SPLIT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_eval.go:360``

**Details & Resolution**:
따옴표 상태 인식 스마트 IN 분해기로 콤마 포함 문자열 리터럴 절단 및 매칭 영구 실패 해결

- 조건 평가기 `evaluateWhereCondition`의 `IN (...)` 절에서 `strings.Split(valList, ",")`로 리스트를 단순 분해하여, `WHERE city IN ('Seoul, KR', 'Tokyo, JP')`와 같이 콤마를 포함한 문자열 리터럴이 `'Seoul`, `KR'`로 쪼개져 IN 조건 매칭이 영구 실패하던 결함.
- 문자열 리터럴 이스케이프 및 따옴표 상태를 인식하는 스마트 IN 리스트 분해기 도입으로 정합성 보장.

---

### [DONE] evalRowBinaryOp 불일치 비교 NULL 3가 논리 보장 (Eval NEQ NULL Bug) `BUG-EVAL-NEQ-NULL-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/eval.go:341-347``

**Details & Resolution**:
`TokenNeq` 연산자 NULL 피연산자 시 `nil`(UNKNOWN) 반환으로 WHERE 절 누수 원천 차단

- `evalRowBinaryOp`에서 불일치 비교(`TokenNeq`: `<>`, `!=`) 시 피연산자 중 하나가 `nil`일 때 `true`를 반환하도록 작성되어(`if left == nil || right == nil { return true }`), SQL 3가 논리(3VL)를 위반하고 `WHERE status <> 'completed'` 조건 등에서 NULL 행들이 필터를 무단 통과하는 결함.
- `TokenNeq` 역시 다른 비교 연산자와 동일하게 NULL 피연산자 시 `nil`(UNKNOWN)을 반환하여 WHERE 절 누수 원천 차단.

---

### [DONE] AggregateIterator DISTINCT 집계 무시 및 전체 행 집계 왜곡 방지 (Agg Distinct Ignored Bug) `BUG-EXEC-AGG-DISTINCT-IGNORED-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
정규식 `(?:DISTINCT\s+)?` 일치 시 `isDistinct` 플래그 보존 및 `groupAccumulator` 고유값 셋 추적으로 중복 집계 왜곡 차단

---

### [DONE] 복합 조인 조건 '=' 분해 파괴 및 무제한 카테시안 곱 왜곡 방지 (Join Cond Split Bug) `BUG-EXEC-BUILDER-JOIN-COND-SPLIT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
`parseJoinCond`에서 다중 복합 조건(`k1=v1 AND k2=v2`) 지원으로 빈 문자열("") 해시 매칭 및 카테시안 왜곡 해결

---

### [DONE] HashJoin/MergeJoin 문자열 조인 키 대소문자 무관 매칭 보장 (Join Str Case Bug) `BUG-EXEC-JOIN-STR-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/execution/join.go:184, 291`, `pkg/execution/merge_join.go:297``

**Details & Resolution**:
조인 키 해싱 및 비교 시 대소문자 무관 정규화 적용으로 SQL 표준 조인 매칭 보장

- `HashJoinIterator`와 `MergeJoinIterator`가 문자열 조인 키 비교 시 Go 맵의 엄격 일치(`hashTable[kStr]`) 및 바이트 단위 `strings.Compare`를 사용하여, 대소문자가 다른 동일 의미의 문자열 키(예: `'admin'` vs `'ADMIN'`)가 일치하지 않는 것으로 판정되어 유효한 조인 결과 행들이 대거 누락되는 결함.
- 조인 키 해싱 및 비교 시 대소문자 무관 정규화 키를 적용하여 SQL 표준 조인 매칭 보장.

---

### [DONE] EXEC 프로시저 호출 시 외부 트랜잭션 세션 우회 및 독립 자동 커밋 방지 (Exec Proc Tx Bypass Bug) `BUG-EXEC-PROC-TX-BYPASS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_procs.go:988-992, 1026``

**Details & Resolution**:
`execProcedureCall`에서 활성 트랜잭션(`cs.Tx`) 공유로 프로시저 내 수정 사항의 트랜잭션 원자성 및 ROLLBACK 보장

- `execProcedureCall`(`EXEC procName @args`) 실행 시 활성 클라이언트 세션의 트랜잭션(`cs.Tx`)을 확인하지 않고 무조건 `s.engine.NewSession()`으로 독립 스토리지를 생성하여 프로시저를 실행함으로써, 선행 트랜잭션 변경사항을 읽지 못하고 프로시저 수정분이 즉시 자동 커밋되어 외부 `ROLLBACK`이 무력화되던 트랜잭션 원자성 파괴 결함.
- `cs.Tx` 존재 시 `ExecuteInTxContext`를 통해 상위 활성 트랜잭션을 공유 바인딩하여 트랜잭션 일관성 보장.

---

### [DONE] EXPLAIN 서브쿼리 무작위 치환 방지 (Explain Bug) `BUG-EXP-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_explain.go:703, 723, 760``

**Details & Resolution**:
`EXPLAIN` 서브쿼리 평탄화 시 무작위 맵 순회 제거 및 서브쿼리 컬럼 키 정합

- `EXPLAIN` 서브쿼리 평탄화 시 `for _, v := range r { break }` 무작위 맵 순회로 서브쿼리 IN 리스트 및 스칼라 비교 연산자에 엉뚱한 컬럼 값이 대입되던 결함.
- `innerRes.Columns[0]` 대소문자 무관 매칭 및 무작위 순회 완전 삭제.

---

### [DONE] 외래키 부모 컬럼 대소문자 정규화 (FK Case Bug) `BUG-FK-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/tx.go:940-946``

**Details & Resolution**:
`findMatchingChildPKsUnderLock` 대소문자 불일치로 자식 참조 0건 오판 및 부모 삭제 허용 결함 수정

- `memengine`의 `findMatchingChildPKsUnderLock`에서 `parentRecord[pCol]` 부모 컬럼 조회가 대소문자 차이로 실패하면 "참조 자식 행이 0건"이라고 오판하여 `nil, nil`을 사일런트 반환함으로써, `ON DELETE RESTRICT` 및 CASCADE를 완전 우회하고 부모 행 삭제를 허용하던 결함.
- `parentRecord` 룩업 시 `strings.EqualFold` 대소문자 무관 매칭 보장.

---

### [DONE] 외래키 자식 컬럼 대소문자 정규화 (FK Child Case Bug) `BUG-FK-CHILD-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/tx.go:853-860``

**Details & Resolution**:
`checkForeignKeyUnderLock` 대소문자 불일치 시 가짜 외래키 삽입이 무단 허용되는 결함 수정

- `memengine`의 `checkForeignKeyUnderLock`에서 `childRecord[cCol]` 조회가 대소문자 차이로 실패하면 "외래키가 NULL이므로 만족됨(`return true, nil`)"으로 오판하여 부모에 없는 가짜 외래키 삽입을 무단 허용하던 결함.
- `childRecord` 룩업 시 `strings.EqualFold` 대소문자 무관 매칭 보장.

---

### [DONE] 외래키 부모 테이블 대소문자 정규화 (FK Parent Table Case Bug) `BUG-FK-PARENT-TABLE-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/tx.go:932``

**Details & Resolution**:
부모 테이블 대소문자 불일치 시 `ON DELETE RESTRICT` 및 외래키 연쇄 검사 완전 우회 결함 해결

- `tx.engine.tables[fk.ParentTable]` 맵 조회가 대소문자 불일치로 실패할 경우 부모 컬럼 목록이 비어 `rowMatchesFK`가 모든 자식에 대해 false를 반환함으로써 부모 삭제를 무단 허용하던 결함.
- 대소문자 무관 테이블 탐색으로 정규 부모 스키마 조회 보장.

---

### [DONE] FOR JSON PATH 테이블 별칭 중첩 객체 오해석 방지 (For JSON Alias Bug) `BUG-FOR-JSON-TABLE-ALIAS-WRAPPER-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_pivot.go:1071-1084``

**Details & Resolution**:
테이블 별칭과 컬럼 별칭 구분으로 순수 컬럼 프로젝션 시 불필요 래핑 제거

- `execForJSONSelect`가 컬럼명에 점(`.`)이 포함되어 있으면 무조건 중첩 JSON 객체 경로로 해석하여, `SELECT u.id, u.name FROM users u FOR JSON PATH`와 같이 테이블 별칭이 붙은 일반 쿼리에서 불필요하게 `{"u": {"id": 1, ...}}` 형태로 래핑되어 출력되는 결함.
- 테이블 별칭과 명시적 컬럼 별칭(`AS [path.prop]`)을 구분하여 순수 컬럼 프로젝션 시 불필요한 래핑 객체 생성 방지.

---

### [DONE] HTTP API 테이블 생성 시 임의 PK 승격 제거 (HTTP Table PK Bug) `BUG-HTTP-TABLE-PK-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/api/handlers_tables.go:147, 160-163``

**Details & Resolution**:
`cols[0].PrimaryKey = true` 임의 지정 제거 및 미지원 타입 전달 시 400 에러 정규 반환

- HTTP API 테이블 생성(`CreateTableHandler`) 시 PK 미지정 시 첫 번째 컬럼을 멋대로 PK로 강제 지정(`cols[0].PrimaryKey = true`)하고, 알 수 없는 타입을 조용히 `TypeVarChar`로 대체하는 안티패턴.
- 임의 PK 승격 제거 및 미지원 타입 전달 시 400 Bad Request 에러 반환.

---

### [DONE] 세컨더리 인덱스 키 추출 대소문자 정규화 (Index Key Case Bug) `BUG-INDEX-KEY-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/ddl.go:172, 211, 222``

**Details & Resolution**:
`extractIndexKey`의 대소문자 미정규화로 인한 세컨더리 인덱스 NULL 오염 및 스캔 0건 결함 해결

- `extractIndexKey`에서 `rec[c]` 맵 조회가 대소문자 차이로 실패할 경우 키를 `TypeUnknown` NULL 또는 `"NULL"`로 기록하여 세컨더리 인덱스를 영구 오염시키고 차후 인덱스 조회를 0건으로 만드는 결함.
- `strings.EqualFold` 대소문자 무관 레코드 컬럼 탐색 보장.

---

### [DONE] 인덱스 스캔 다중 정렬 무시 및 정렬 누락 방지 (Index Scan Order By Bug) `BUG-INDEX-SCAN-ORDERBY-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_eval.go:661, 692, 814``

**Details & Resolution**:
첫 번째 컬럼만 일치 시 다중 정렬 전체가 만족된 것으로 오판하여 정렬을 건너뛰는 결함 해결

- `isIndexAscendingOrder`/`isIndexDescendingOrder`가 `ORDER BY`의 첫 번째 컬럼만 비교하고 다중 정렬(`ORDER BY a ASC, b DESC`) 전체가 만족된 것으로 오판하여 인덱스 스캔 후 정렬을 건너뛰고 잘못된 순서로 결과를 반환하던 결함.
- `ORDER BY`의 모든 컬럼이 인덱스 순서 및 방향과 일치하는지 전수 검증.

---

### [DONE] 조인 인덱스 임의 선택 방지 (Join Index Guess Bug) `BUG-JOIN-IDX-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/execution/builder.go:256``

**Details & Resolution**:
`IndexNestedLoopJoin` 구성 시 인덱스 미일치 시 `Indexes[0]`을 임의 선택하여 조인 오염시키는 결함 방지 (Hash Join 안전 전환)

- `IndexNestedLoopJoin` 구성 시 `innerPlan.IndexName`을 찾지 못하면 `else if len(innerSchema.Indexes) > 0 { innerIdx = &innerSchema.Indexes[0] }`로 조인 조건과 무관한 임의의 첫 번째 인덱스(예: `idx_email`)를 가져와 외부 조인 키(예: `user_id = 1001`)로 검색하여 조인 결과를 완전히 파괴하던 결함.
- 일치하는 조인 인덱스가 없는 경우 안전하게 `HashJoin` 또는 `MergeJoin`으로 Fallback 전환.

---

### [DONE] 조인 조건 단순 Split 파싱 결함 수정 (Join Parse Bug) `BUG-JOIN-PARSE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/execution/builder.go:315`, `pkg/wire/server.go:1064, 1586``

**Details & Resolution**:
`strings.Split(cond, "=")` 다중 조건 분해 실패 시 빈 컬럼명 전달 및 PGWire FROM 분해 오작동 방지

- `strings.Split(cond, "=")`로 다중 조건(`a = b AND c = d`) 분해 실패 시 `"", ""`를 반환하여 빈 컬럼명 `" ASC"` 소트 이터레이터를 생성하고, PGWire에서 `strings.Split(upper, "FROM")`로 `valid_from` 컬럼명 등에서 조기 분리되어 테이블명 조회가 파괴되던 결함.
- 조인 조건 AST 분해 및 PGWire 쿼리 키워드 경계 정규식 기반 정합 분해.

---

### [DONE] MemEngine 로컬 삽입 행 인덱스 정렬 보장 (MemEngine Index Order Bug) `BUG-MEM-INDEX-LOCAL-ORDER-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/tx.go:1264-1277``

**Details & Resolution**:
트랜잭션 로컬 삽입 행 Go 맵 무작위 순회 append 제거 및 인덱스 키 병합 정렬 적용

- MemEngine `IndexScan` 시 현재 쓰기 트랜잭션에서 삽입된 로컬 행(`tx.local[tableName]`)을 Go 맵 무작위 순회(`for pk, locRow := range localRows`)로 스캔 결과 꼬리에 무조건 `append`하여, 인덱스 키 정렬 순서를 파괴하고 비결정론적 행 순서를 반환하는 치명적 결함.
- 트랜잭션 로컬 행에 대해 인덱스 키 추출 후 메인 인덱스 스캔 결과와 병합 정렬(Merge-Sort)하여 인덱스 순서 무결성 보장.

---

### [DONE] TableData 샤드 지연 초기화 동시성 경쟁 런타임 패닉 방지 (MemEngine Shard Init Race Bug) `BUG-MEM-SHARD-INIT-RACE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/table_data.go:87-116, 120``

**Details & Resolution**:
`ensureShardsInit`의 락 없는 맵 초기화로 인한 동시 읽기 시 `fatal error: concurrent map writes` 크래시 방지 (`sync.Once`)

- `TableData.ensureShardsInit()`이 뮤텍스 락 없이 `td.initShards()`를 호출하여, 기동 후나 역직렬화 직후 복수의 동시 읽기 트랜잭션이 `getRowAndVer` 또는 `hasKey`를 호출할 때 `td.shards[sIdx].rows` 맵에 동시 쓰기를 발생시켜 `fatal error: concurrent map writes` 런타임 크래시를 유발하던 결함.
- `sync.Once`를 적용하여 샤드 지연 초기화의 원자적/멱등적 단 1회 실행 보장.

---

### [DONE] SkipList 유니크 인덱스 재인덱싱 시 커버링 컬럼 복사본 갱신 방지 (SkipList Unique Include Bug) `BUG-MEM-SKIPLIST-UNIQUE-INCLUDE-COPY-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/skiplist.go:86-90``

**Details & Resolution**:
`curr.Items` 값 복사 순회(`for _, item`)로 인한 커버링 컬럼(`IncludeValues`) 갱신 누락 및 인덱스 조회 왜곡 해결

- `SkipList.Insert`에서 유니크 인덱스의 기존 키 갱신 시 `for _, item := range curr.Items` 값 복사 순회를 수행하며 `item.IncludeValues = inc`를 대입하여, 실제 슬라이스 원소(`curr.Items[i]`)가 전혀 갱신되지 못하고 커버링 인덱스 데이터가 유실/과거값으로 방치되던 결함.
- 비유니크 분기와 동일하게 인덱스 인덱싱(`curr.Items[i].IncludeValues = inc`)으로 수정하여 커버링 인덱스 갱신 보장.

---

### [DONE] MemEngine IndexScan 인덱스명 대소문자 무관 탐색 보장 (MemEngine Index Case Bug) `BUG-MEM-TX-INDEX-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/tx.go:1229``

**Details & Resolution**:
인덱스 맵 1차 실패 시 대소문자 무관 순회 보강으로 `ErrIndexNotFound` 및 풀스캔 강제 방지

- MemEngine `IndexScan`에서 `td.Indexes[indexName]`으로 엄격 대소문자 조회를 수행하여, 쿼리 또는 플래너가 대소문자가 다른 인덱스명(예: `idx_name` vs `IDX_NAME`)으로 요청할 경우 `storage.ErrIndexNotFound`를 반환하고 쿼리를 실패시키거나 풀스캔으로 강제 후퇴하던 결함.
- 인덱스 맵 탐색 시 1차 실패 시 대소문자 무관 순회 탐색을 보강하여 인덱스 히트 보장.

---

### [DONE] 트랜잭션 커밋 시 테이블 대소문자 사일런트 쓰기 증발 방지 (MemEngine Tx Case Bug) `BUG-MEM-TX-TABLE-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/tx.go:244, 1479, 1546, 1622``

**Details & Resolution**:
변경 레코드 테이블명 스키마 공식 명칭 정규화로 커밋 시 샤드 미반영 쓰기 유실 차단

- MemEngine 트랜잭션의 `getTableLocked`, OCC 충돌 검증, 커밋 시 샤드 반영 루프가 `tx.engine.tables[ch.table]` 엄격 대소문자 조회로 작성되어, 등록 시와 다른 대소문자(`Users` vs `users`)로 테이블을 조작한 트랜잭션이 커밋할 때 `if !exists { continue }`로 통과되어 쓰기 데이터가 메모리에 반영되지 않고 사일런트 유실되던 결함.
- 변경 레코드(`rowChange`)의 테이블명을 스키마 공식 명칭으로 정규화하고 대소문자 무관 탐색 보장.

---

### [DONE] WAL 복구 시 CREATE TABLE 인라인 인덱스 등록 누락 방지 (WAL Create Table Index Bug) `BUG-MEM-WAL-CREATE-TABLE-INDEX-OMISSION-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/persistence.go:61-70``

**Details & Resolution**:
`OpCreateTable` 재생 시 `op.Schema.Indexes` 순회 초기화로 충돌 복구 후 인덱스 갱신 누락 해결

- MemEngine의 WAL 복구 루프 `applyWALRecordLocked`에서 `OpCreateTable` 재생 시 `td.Indexes` 맵만 생성하고 `op.Schema.Indexes`에 정의된 인라인 인덱스들을 전혀 등록하지 않아, 크래시 복구 후 생성된 테이블의 인덱스들이 증발하여 후속 WAL의 `OpInsert`/`OpUpdate` 시 인덱스가 갱신되지 못하던 결함.
- `op.Schema.Indexes`를 순회하며 `td.Indexes`에 각 인덱스 구조체를 정상 인스턴스화하도록 복구 로직 보강.

---

### [DONE] MERGE WHEN NOT MATCHED IDENTITY 자동 채번 보장 (Merge Identity PK Bug) `BUG-MERGE-IDENTITY-PK-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_merge.go:180-205``

**Details & Resolution**:
MERGE INSERT 시 IDENTITY 자동 채번 및 세션/테이블 식별자 동기화 연동

- `MERGE` 문의 `WHEN NOT MATCHED THEN INSERT` 절에서 대상 테이블의 `IDENTITY` 컬럼 자동 생성 처리가 완전히 누락되어, IDENTITY PK 테이블에 MERGE 수행 시 `missing primary key` 에러로 연산이 실패하고, 커밋 후 `@@identity`가 갱신되지 않던 결함.
- MERGE INSERT 시 IDENTITY 컬럼 자동 채번 및 세션/테이블 식별자 동기화 파이프라인 연동.

---

### [DONE] MERGE 구문 내 신규 삽입 행의 SOURCE 미일치 오인 즉각 삭제 방지 (Merge Self Destruct Bug) `BUG-MERGE-NOT-MATCHED-SOURCE-SELF-DESTRUCT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
`WHEN NOT MATCHED` 삽입 행의 `matchedTargetKeys` 등록으로 동일 MERGE 문 내 `NOT MATCHED BY SOURCE`의 자가 삭제 방어

---

### [DONE] MERGE SET 절 다중 인자 수식 보존 (Merge Set Bug) `BUG-MERGE-SET-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_merge.go:646-658``

**Details & Resolution**:
`parseSetPairs` 괄호 고려 토큰 분해로 `COALESCE` 등 수식 내 콤마 분해 시 후속 구문 사일런트 유실 방지

- MERGE 문의 `UPDATE SET` 파싱 시 `strings.Split(s, ",")` 단순 분해로 인해 `SET col = COALESCE(a, 'N/A'), col2 = 1` 등 함수 내 콤마 분해 시 후속 구문이 `=` 미포함으로 조용히 버려지고 문법이 깨지던 결함.
- 괄호 중첩을 고려한 토큰 분해 적용으로 다중 인자 수식 보존.

---

### [DONE] 단일 트랜잭션 내 다중 유니크 충돌 사일런트 커밋 방지 (OCC Intra Tx Unique Bug) `BUG-OCC-COMMIT-INTRA-TX-UNIQUE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/tx.go:1557-1588, 1645, 1674``

**Details & Resolution**:
`tx.changes` 내부 상호 유니크 충돌 사전 검증 및 `idxData.Insert` 에러 전파로 중복 커밋 원천 차단

- `memengine.Transaction.Commit()`의 5단계 OCC 검증이 커밋된 인덱스 데이터만을 상대로 `ScanRange`를 수행하여, 동일 트랜잭션 내부에서 여러 행이 동일한 유니크 키로 삽입/수정될 경우 이를 감지하지 못하고, 6단계 적용 루프에서 `idxData.Insert`가 반환한 유니크 위반 에러를 `_ =`로 침묵시켜 중복 행들이 스토리지에 무단 커밋되던 결함.
- `tx.changes` 내부 상호 유니크 키 충돌 사전 검사 및 적용 단계 에러 즉시 전파로 중복 커밋 원천 차단.

---

### [DONE] MemEngine updateLocked 유니크 인덱스 사전 검증 누락 방지 (OCC Update Unique Bug) `BUG-OCC-UPDATE-UNIQUE-CHECK-OMISSION-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/tx.go:481-568``

**Details & Resolution**:
레코드 수정(`Update`) 시 유니크 인덱스 즉각 검증 로직 추가로 트랜잭션 내 중복 키 갱신 방어

- `memengine.Transaction.updateLocked` 실행 시 유니크 인덱스 제약 검증이 완전히 누락되어, 행을 수정하여 다른 행의 고유 키와 충돌하더라도 DML 실행 시점에 에러가 발생하지 않고 트랜잭션 커밋 단계까지 오류 감지가 지연되던 결함.
- `updateLocked` 시점에 대상 유니크 인덱스 및 로컬 변경본(`tx.local`) 대상 충돌 사전 검증 루프 구축.

---

### [DONE] ORDER BY 다중 컬럼 및 대소문자 정합성 (Order By Bug) `BUG-ORDER-BY-SPLIT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/catalog/virtual/result.go:34`, `pkg/engine/sqlexec_dbcc.go:310`, `pkg/engine/sqlexec_pivot.go:421, 946`, `pkg/engine/sqlexec_select.go:1857, 2253``

**Details & Resolution**:
`strings.Fields` 분해로 인한 다중 정렬 누락, 콤마 포함 시 DESC 무력화 및 대소문자 미매칭 무력화 원천 해결

- `orderParts := strings.Fields(orderByClause)`로 공백 분해하여 다중 컬럼 정렬(`ORDER BY a, b DESC`) 시 `b` 이후가 누락되고, `ORDER BY a DESC, b` 시 `"DESC,"` 콤마로 인해 `EqualFold`가 거짓이 되어 DESC가 ASC로 반전되며, 대소문자 미매칭으로 정렬이 무력화되던 6개 파일 공통 결함.
- 표준 토큰 분해기 및 대소문자 무관 다중 정렬 슬라이스 비교기 적용.

---

### [DONE] Pratt 파서 Simple CASE 피연산자 누락 및 문법 거부 방지 (Parser Simple Case Bug) `BUG-PARSER-SIMPLE-CASE-OMISSION-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/parser_expr.go:445-475`, `pkg/proc/ast.go:486``

**Details & Resolution**:
`parseCase`에서 `CASE expr WHEN ...` 단순 CASE 피연산자 인식 및 `CaseExpr.Operand` AST 필드 보강

- Pratt 파서의 `parseCase`가 Searched CASE(`CASE WHEN cond THEN ...`)만 파싱하고 `CASE` 바로 뒤에 `TokenWhen`이 오지 않는 Simple CASE(`CASE expr WHEN val THEN ...`)를 만나면 즉시 문법 에러(`nil`)를 반환하며, `CaseExpr` AST에도 피연산자(`Operand`) 필드가 결여되어 있던 결함.
- `CASE` 직후 조건식 존재 시 `Operand`로 파싱하고 `CaseExpr`에 피연산자 필드 추가 및 VDBE/평가기 연동.

---

### [DONE] PGWire Describe 단순 문자열 분해 OID 왜곡 방지 (PGWire Describe Bug) `BUG-PGWIRE-DESCRIBE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/wire/server.go:1064, 1099, 1586``

**Details & Resolution**:
쿼리를 FROM/콤마로 단순 분해하여 서브쿼리 및 함수 콤마가 파괴되고 OID 25로 왜곡되는 결함 해결

- PGWire `writeSchemaDescription` 및 `inferColumnOIDs`에서 쿼리를 `"FROM"`과 `","`로 단순 분해하여 서브쿼리, 콤마 포함 함수(`COALESCE`, `SUBSTRING`), 리터럴이 테이블/컬럼 추출을 파괴하고 미매칭 수식을 OID 25(VARCHAR)로 왜곡 전달하는 결함.
- 안전한 쿼리 파싱 및 표현식 타입 추론 연동.

---

### [DONE] PGWire 파라미터 경계 인식 치환 (PGWire Param Bug) `BUG-PGWIRE-PARAM-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/wire/server.go:992``

**Details & Resolution**:
단순 문자열 치환으로 `$10` 이상의 다중 파라미터가 `$1` 치환 시 깨지는 결함 수정

- PGWire `buildPreviewSQL`에서 `strings.ReplaceAll(res, "$1", replacement)` 단순 치환으로 인해 `$10` 이상의 파라미터가 `$1` 치환 시 앞부분이 잘려 문법 에러를 유발하던 결함.
- 단어 경계 인식 및 단일 패스 파서 기반 파라미터 치환 엔진 적용.

---

### [DONE] PGWire 사전 컴파일 바이트코드 실행 시 트랜잭션 우회 방지 (PGWire Prep Tx Bypass Bug) `BUG-PGWIRE-PREP-EXEC-TX-BYPASS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
`handleExecute` 패스트패스에서 신규 독립 세션(`NewSession`) 강제 생성을 중단하고 활성 세션 컨텍스트 및 트랜잭션 바인딩

---

### [DONE] PITR WAL 재생 시 DDL 실행 에러 침묵 및 복구 실패 은폐 방지 (PITR DDL Swallow Bug) `BUG-PITR-DDL-SILENT-SWALLOW-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/backup/pitr.go:112, 121, 129, 137, 146, 155``

**Details & Resolution**:
DDL 실행 실패(`CreateTable`, `DropTable` 등) 시 에러 즉시 전파로 후속 데이터 파괴 및 복구 왜곡 방지

- PITR 및 WAL 재생 엔진 `ApplyWALRecordWithOptions`에서 DDL 작업(`CreateTable`, `DropTable`, `TruncateTable`, `DropPartition`, `CreateIndex`, `DropIndex`) 실행 시 발생하는 에러를 `_ =`로 완전히 침묵시켜, 테이블 생성 실패 등 치명적 오류에도 복구가 성공한 것처럼 가장되던 결함.
- DDL 호출 에러 즉시 반환 및 롤백 연동으로 복구 실패 시 정밀 진단 보장.

---

### [DONE] PIVOT COUNT 집계 시 빈 카테고리 0 정규 반환 (Pivot Count NULL Bug) `BUG-PIVOT-COUNT-NULL-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_pivot.go:292-357``

**Details & Resolution**:
`COUNT` 집계에 대해 빈 그룹 시 `0`을 정규 방출하도록 보강

- `parsePivotQuery`에서 `len(vals) > 0`일 때만 집계 연산을 수행하고 빈 그룹은 `aggResult = nil`로 방치하여, 매칭 건수가 0건인 카테고리에 대해 표준 SQL 규격인 `0` 대신 `NULL`이 반환되는 집계 왜곡 결함.
- `COUNT` 집계에 대해 빈 그룹 시 `0`을 정규 방출하도록 보강.

---

### [DONE] PIVOT/UNPIVOT ORDER BY 절 쉼표 분해 왜곡 방지 (Pivot Order By Split Bug) `BUG-PIVOT-ORDER-BY-SPLIT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_pivot.go:420-425, 946-950``

**Details & Resolution**:
`strings.Fields` 대신 스마트 정렬 파서 적용으로 쉼표 포함 정렬 키(`col,`, `DESC,`) 파괴 및 정렬 무력화 해결

- `PIVOT` 및 `UNPIVOT` 연산자 처리 시 `ORDER BY` 절을 단순 공백 분해(`strings.Fields`)하여, 콤마가 붙은 컬럼명(`"col1,"`)이나 방향 키워드(`"DESC,"`)가 매칭에 실패하여 정렬 방향이 뒤바뀌거나 다중 컬럼 정렬이 완전히 무력화되던 결함.
- 스마트 토큰 분해기를 통해 콤마를 분리하고 모든 다중 정렬 컬럼 및 방향을 온전히 반영하도록 수정.

---

### [DONE] 무결성 결함 수정 (Integrity Bug) `BUG-PK-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_dml.go:1071, 1228`, `sqlexec_ddl.go:363`, `pkg/proc/runtime/vm.go:940, 1076``

**Details & Resolution**:
`execUpdate`/`execDelete`/`execTruncate` 및 VDBE의 PK 동적 해석 및 맵 순회 왜곡 방지

- `execUpdate`, `execDelete`, `execTruncate`, VDBE `execUpdateWhere`, `execDeleteWhere`에서 PK 미확정 시 `rec["id"]`를 하드코딩하고 Go 맵을 무작위 순회(`for _, v := range rec { break }`)하여 엉뚱한 행을 갱신/삭제하던 치명적 결함.
- `schema.PrimaryKeyColumn()`을 통해 실제 PK 컬럼명을 정규 조회하고, 무작위 순회 코드를 완전 삭제하여 PK를 식별할 수 없는 경우 안전하게 에러를 반환.

---

### [DONE] CBO 선택도 추정 시 컬럼명 대소문자 불일치 0.33 휴리스틱 추락 방지 (CBO Selectivity Case Bug) `BUG-PLANNER-CBO-SELECTIVITY-CASE-SENSITIVE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
`EstimateSelectivity`의 `ts.Columns` 직접 맵 조회를 `findColumnStats`로 대체하여 대소문자 무관 통계 조회 보장

---

### [DONE] OR 조건 하위 서브쿼리 불법 SEMI 조인 변환 및 'and' 식별자 파괴 방지 (Subquery Decorrelate Bug) `BUG-PLANNER-DECORRELATE-OR-SUBQUERY-CORRUPT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
OR 분기 내 서브쿼리 무단 변환 차단 및 `removeClause`의 경계 인식 토큰화로 `brand` 등 컬럼명 절단 및 문법 오류 해결

---

### [DONE] CBO/SetOp 다중 정렬 키 콤마 잔존 해결 (Planner OrderBy Bug) `BUG-PLANNER-ORDERBY-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/planner/planner.go:1327`, `pkg/planner/setop.go:326``

**Details & Resolution**:
CBO 및 SetOp 플래너 다중 정렬 시 `strings.Fields` 콤마 잔존(`"a,"`)으로 인한 플랜 왜곡 해결

- CBO 플래너 및 SetOp 플래너가 `strings.Fields(OrderByClause)`로 정렬 키를 파싱하여 다중 컬럼 정렬 시 `SortKey`에 콤마가 포함(`"a,"`)되고 후속 컬럼이 버려져 실행 계획 텍스트 및 정렬 플랜 노드가 왜곡되던 결함.
- 콤마 분해 기반 다중 컬럼 정렬 키 정합화.

---

### [DONE] WHERE 절 이스케이프 따옴표 및 대괄호 식별자 AND 오절단 방지 (Planner Split AND Bug) `BUG-PLANNER-SPLIT-AND-QUOTE-ESCAPE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/planner/decorrelate.go:429-434`, `pkg/planner/pushdown.go:172``

**Details & Resolution**:
`splitAndConditions`의 중첩 따옴표(`''`) 및 대괄호 식별자 미인식으로 인한 WHERE 조건 파괴 및 플래너 에러 해결

- `splitAndConditions`가 연속 작은따옴표 이스케이프(`''`)나 대괄호 식별자(`[col AND val]`)를 인식하지 못하고, 따옴표를 단순 토글 처리하여 문자열 리터럴 내부나 식별자 내부에 `AND`가 포함된 경우 조건을 경계 밖으로 무단 절단하여 WHERE 절 문법을 파괴하던 플래너 결함.
- `''` 이스케이프 연속 토큰 건너뛰기 및 대괄호(`[...]`) 깊이 추적을 보강하여 조건 절단 방어.

---

### [DONE] 단순 CASE 문(Simple CASE) 파싱 및 컴파일 보장 (Simple CASE Bug) `BUG-PROC-SIMPLE-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/parser_expr.go:445-478`, `pkg/proc/ast.go:486`, `pkg/proc/compiler.go:1276-1315``

**Details & Resolution**:
`CaseExpr`에 `Operand` 필드 신설 및 VDBE 피연산자-조건값 비교 분기 바이트코드 방출

- LionSQL 프로시저 엔진이 Searched CASE(`CASE WHEN cond THEN ...`)만 지원하고 단순 CASE(`CASE @val WHEN 1 THEN ...`) 구문 파싱 및 컴파일을 지원하지 않아, 피연산자(Operand)가 뒤따르는 T-SQL 표준 CASE 문 사용 시 nil 파싱 실패 및 컴파일 불가 구문 에러를 유발하는 결함.
- `CaseExpr`에 `Operand Expr` 필드를 신설하고, 파서 및 VDBE 컴파일러에서 피연산자-조건값 간의 동등 비교 분기 바이트코드 방출 보장.

---

### [DONE] 수식 내 AS 오분해 방지 (Project Cast AS Bug) `BUG-PROJ-CAST-AS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/execution/project.go:82`, `pkg/wire/server.go:1106``

**Details & Resolution**:
`CAST(x AS INT)` 등 수식 내부 `" AS "` 오분해로 인한 표현식 절단 및 NULL 반환 결함 해결

- `ProjectionIterator`에서 `" AS "` 문자열을 괄호 중첩 검사 없이 단순 검색하여 `CAST(expr AS type)` 수식 내부의 `" AS "`를 별칭 구분자로 오인해 수식을 두 동강 내고(`"CAST(price"`, `"INT)"`) 결과 행에 NULL을 반환하던 결함.
- 괄호 중첩 깊이 인식 기반 최상위 `AS` 별칭 분해 적용.

---

### [DONE] 사일런트 유실 방지 (Silent Loss Bug) `BUG-RECORD-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_dml.go:415, 1080`, `pkg/storage/tuple.go:63``

**Details & Resolution**:
DML Record 컬럼명 정규화(`colSchema.Name`)로 `RecordToRow` 업데이트 유실 및 NULL 삽입 방지

- `execUpdate`, `execInsert`에서 DML Record 맵을 구성할 때 쿼리 원문 `colName`(예: `"Name"`)을 키로 저장하여, 스토리지 병합 후 `RecordToRow`가 스키마 컬럼(`"name"`)만 읽어 업데이트 값이 사일런트 유실(Silent Loss)되거나 NULL로 삽입되던 결함.
- `rec[colSchema.Name] = ...`으로 스키마 공식 컬럼명으로 정규화하여 저장하고, `RecordToRow`에서도 대소문자 방어 로직 추가.

---

### [DONE] GROUP BY 및 집계함수 대소문자 정합성 보장 (Select Agg Case Bug) `BUG-SELECT-AGG-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_select.go:2020-2224``

**Details & Resolution**:
`computeAggregates` 행 맵 대소문자 무관 탐색으로 `GROUP BY <nil>` 및 집계함수 NULL 변조 방지

- `computeAggregates`에서 `GroupByCols` 및 집계함수(`COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, 비집계 프로젝션)가 행 맵을 `r[gClean]`, `r[bare]`, `r[item.ArgCol]` 엄격 대소문자로 조회하여, 쿼리 컬럼명의 대소문자가 다를 경우 모든 행이 `<nil>` 단일 버킷으로 합쳐지거나 집계 결과가 `NULL` 또는 0으로 왜곡 반환되는 결함.
- 행 맵 조회 시 `strings.EqualFold` 대소문자 무관 탐색 보장.

---

### [DONE] SELECT INTO PK 임의 추측 방지 (Select Into PK Bug) `BUG-SELECT-INTO-PK-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_select.go:930-965`, `pkg/engine/sqlexec_derived.go:94-118``

**Details & Resolution**:
`SELECT INTO` 및 파생 테이블의 일시적 고유 컬럼 임의 PK 지정 및 0건 시 `Columns[0]` PK 지정 제거

- `SELECT ... INTO` 및 파생 테이블 구체화 시, 초기 조회 결과에서 우연히 중복이 없는 컬럼이 있으면 해당 컬럼을 새 테이블의 영구 PK로 임의 지정하거나, 조회 결과가 0건일 때 `selRes.Columns[0]`을 PK로 지정하여 차후 DML 실패를 유발하는 결함.
- 원본 테이블의 공식 PK가 보존되지 않은 경우 무조건 합성 PK(`_row_id` / `_dt_row_id`)를 생성하도록 일원화.

---

### [DONE] sp_executesql OUT 접미 변수명 오절단 방지 (SP Suffix OUT Bug) `BUG-SP-EXEC-SUFFIX-OUT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_procs.go:1204-1209``

**Details & Resolution**:
`cleanOutputSuffix` 선행 공백/단어 경계 검사로 `@timeout` 등 `OUT` 접미 변수명 3글자 절단 훼손 해결

- `cleanOutputSuffix`에서 공백/단어 경계 검사 없이 `strings.HasSuffix(upper, "OUT")`를 수행하여, `@timeout`, `@layout`, `@logout`, `@callout` 등 `OUT`으로 끝나는 정상 변수명의 뒷부분 3글자를 강제 절단(`@time`, `@lay`)하여 변수 바인딩이 실패하는 결함.
- 선행 공백 또는 단어 경계가 존재하는 독립 키워드 `OUTPUT`/`OUT`만 제거하도록 수정.

---

### [DONE] sp_executesql 복합 파라미터 타입 파싱 보존 (SP Param Decimal Bug) `BUG-SP-PARAM-DECIMAL-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_procs.go:1285-1300``

**Details & Resolution**:
괄호 중첩 추적으로 `DECIMAL(10, 2)` 등 다중 인자 타입 콤마 분해 절단 및 유령 파라미터 생성 방지

- `parseSpParamDeclarations`에서 `strings.Split(declStr, ",")` 단순 분해로 인해 `@param DECIMAL(10, 2)` 등 다중 인자 데이터 타입 내부의 콤마에서 쪼개져 타입이 `DECIMAL(10`으로 절단되고 이름이 `"2)"`인 유령 파라미터가 생성되는 결함.
- 괄호 중첩 추적 기반 콤마 분해 엔진 적용으로 복합 타입 매개변수 선언문 보존.

---

### [DONE] VALUES 튜플 중첩 수식 콤마 분해 정합성 (Split Values Bug) `BUG-SPLIT-VALUES-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_eval.go:84-106`, `sqlexec_dml.go:405``

**Details & Resolution**:
`splitValues` 괄호 깊이(`depth`) 추적으로 `COALESCE(a, b)` 등 다중 인자 함수 사용 시 컬럼 수 불일치 에러 방지

- `splitValues`가 괄호 중첩 깊이(`depth`)를 계산하지 않고 콤마(`,`)를 단순 분리하여, `INSERT INTO t VALUES (1, COALESCE(a, b))`와 같은 다중 인자 함수 사용 시 내부 콤마에서 쪼개져 "column count does not match value count" 에러를 내던 결함.
- 괄호 중첩 깊이(`depth`)를 추적하여 최상위 콤마만 분해하도록 수정.

---

### [DONE] DateTime-VarChar 상호 변환 누락 비교 역전 방지 (Storage Cmp DateTime Bug) `BUG-STORAGE-CMP-DATETIME-STR-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/engine.go:466, 742-772``

**Details & Resolution**:
날짜/시간과 문자열 간의 상호 파싱/강제변환으로 올바른 시간 축 비교 및 필터 왜곡 해결

- `DateTimeValue`가 `TVal`만 설정하고 `SVal`을 빈 문자열로 남겨둠에 따라, `TypeVarChar`와 `TypeDateTime` 상호 비교 시 `Value.Compare`가 미초기화된 0값 시간(`0001-01-01`) 또는 빈 문자열과 비교하여 비교 결과가 역전되고, `WHERE date_col >= '2024-01-01'` 조건 등의 조회가 0건으로 왜곡되던 결함.
- `Value.Compare`에 DateTime/Date와 문자열 간의 상호 파싱/강제변환(Coercion)을 보강하여 올바른 시간 축 비교 보장.

---

### [DONE] Value.Compare Int-Float 상호 강제변환 보장 (Compare Int-Float Bug) `BUG-STORAGE-CMP-INT-FLOAT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/engine.go:734-755``

**Details & Resolution**:
`IntValue(100) > FloatValue(200.5)` 등 타입 불일치 비교 시 미초기화 0값 비교 역전 해결

- `storage.Value.Compare`에서 `TypeInt`와 `TypeFloat` 간의 상호 타입 변환 로직이 누락되어, Int-Float 비교 시 미초기화된 0값(`other.IVal` 또는 `other.FVal`)과 직접 비교함으로써 `IntValue(100) > FloatValue(200.5)`가 성립되는 등 비교 연산 및 인덱스 범위 검색이 완전히 역전/왜곡되는 결함.
- `Int <-> Float` 상호 비교 시 `float64(v.IVal)` 강제 변환을 선행하여 정확한 수치 비교 보장.

---

### [DONE] 실수 6자리 문자열 포맷팅 동등 비교 왜곡 방지 (Storage Equal Float Bug) `BUG-STORAGE-EQUAL-FLOAT-STR-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/engine.go:666``

**Details & Resolution**:
실수 문자열 포맷팅 제거 및 부동소수점 비트/엡실론 비교로 Compare/Equal 정합성 보장

- `Value.Equal`에서 실수(`TypeFloat`) 동등 비교 시 `fmt.Sprintf("%f", v.FVal)`을 호출하여 소수점 6자리까지만 포맷팅 비교함으로써, 7자리 이후가 다른 서로 다른 두 실수가 참으로 판정되고, 핫패스 비교마다 힙 할당을 유발하며, `Compare`($\ne 0$)와 `Equal`($== true$) 간의 불일치가 발생하는 결함.
- 문자열 포맷팅을 제거하고 네이티브 부동소수점 비트 또는 엡실론 정밀도 비교로 단일화.

---

### [DONE] 스토리지 엔진 테이블명 대소문자 미구분 맵 조회 실패 및 중복 생성 방지 (Storage Table Case Bug) `BUG-STORAGE-TABLE-CASE-SENSITIVITY-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
MemEngine/WiredTiger의 `e.tables` 원시 맵 조회를 대소문자 무시 정규화(`EqualFold` / `ToLower`)하여 `ErrTableNotFound` 및 동명이인 테이블 방어

---

### [DONE] 세션 임시테이블 스냅샷/WAL 영속화 및 재기동 후 영구 누수 방지 (Temp Table Persist Bug) `BUG-STORAGE-TEMP-TABLE-PERSIST-LEAK-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
`saveDiskLocked` 및 WAL 재생 시 `__temp_*` / `__tv_*` 필터링으로 종료 세션 임시 객체의 영구 카탈로그 오염 차단

---

### [DONE] 스토리지 Update 레코드 컬럼명 대소문자 정규화 (Storage Update Case Bug) `BUG-STORAGE-UPDATE-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/tx.go:502`, `pkg/storage/wiredtiger/wt_engine.go:1298``

**Details & Resolution**:
`updateLocked`에서 입력 레코드 키를 스키마 컬럼명으로 정규화하여 대소문자 차이 업데이트 유실 방지

- `memengine`과 `wiredtiger`의 `updateLocked`에서 입력 `record`의 키를 대소문자 정규화 없이 `updated`에 그대로 복사하여, 스키마 컬럼명과 대소문자가 다를 경우 구값을 그대로 유지하고 신규 값을 고아 키로 방치하여 업데이트가 사일런트 유실되는 결함.
- 스키마 공식 컬럼명으로 정규화하여 대입.

---

### [DONE] CHARINDEX/STRPOS의 NULL 인자 수신 시 0 반환 삼치논리 왜곡 방지 (CharIndex NULL Zero Bug) `BUG-STR-CHARINDEX-NULL-ZERO-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55

**Details & Resolution**:
`CHARINDEX`와 `STRPOS`에서 NULL 인자 입력 시 0(검색실패) 대신 `NULL` 정상 전파로 ANSI/T-SQL 삼치논리 정합성 보장

---

### [DONE] 문자열 함수 NULL 전파 보장 (String NULL Bug) `BUG-STR-NULL-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/builtins_string.go:316-435``

**Details & Resolution**:
`UPPER`/`LOWER`/`TRIM`/`SUBSTRING`/`REPLACE` 등 NULL 입력 시 빈 문자열/변조 대신 `NULL` 정규 반환

- `UPPER`, `LOWER`, `TRIM`, `LTRIM`, `RTRIM`, `LEFT`, `RIGHT`, `SUBSTRING`, `REVERSE`에 `NULL` 전달 시 `NULL` 대신 빈 문자열(`""`)을 반환하고, `LEN(NULL)`은 `0`을 반환하며, `REPLACE('abc', NULL, 'x')`는 `"xaxbxcx"`로 문자열을 변조하는 결함.
- 모든 문자열 함수에서 인자가 `IsNull`일 경우 `storage.NullValue(storage.TypeVarChar)`를 정규 반환하도록 표준화.

---

### [DONE] STRING_SPLIT 빈 구분자 콤마 임의 간주 제거 (String Split Sep Bug) `BUG-STRING-SPLIT-SEP-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_string_split.go:116-118``

**Details & Resolution**:
빈 문자열 구분자를 콤마로 멋대로 대체하는 안티패턴 제거 및 표준 규격 에러 반환

- `STRING_SPLIT(str, sep)`에서 `if sep == "" { sep = "," }` 안티패턴으로 인해 빈 문자열 구분자 전달 시 조용히 콤마(`,`)로 대체하여 분해하는 임의 추측 결함.
- 빈 문자열 구분자 전달 시 표준 규격 에러 반환.

---

### [DONE] 스칼라 서브쿼리 무작위 반환 방지 (Subquery Bug) `BUG-SUBQ-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/eval.go:316-318``

**Details & Resolution**:
스칼라 서브쿼리 수식 평가 시 무작위 맵 순회 제거 및 `cols[0]` 대소문자 무관 탐색

- 스칼라 서브쿼리 수식 평가 시 `cols[0]` 조회가 실패할 경우 `for _, v := range firstRow { return v, nil }` 무작위 맵 순회로 임의 컬럼 값이 서브쿼리 결과로 반환되던 결함.
- `cols[0]` 대소문자 무관 탐색 및 무작위 순회 완전 삭제.

---

### [DONE] 테이블명 대소문자 무관 탐색 보장 (Table Case Bug) `BUG-TABLE-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/ddl.go:458`, `pkg/storage/wiredtiger/wt_engine.go:745``

**Details & Resolution**:
`GetTable`/`DropTable` 맵 엄격 일치로 테이블 미인식 에러 방지 (`EqualFold` 폴백 보장)

- `GetTable(tableName)` 및 `DropTable(tableName)`이 Go 맵의 엄격 일치(`e.tables[tableName]`)로만 작성되어, `CREATE TABLE Users` 후 `SELECT * FROM users` 또는 `INSERT INTO users` 실행 시 `table not found: users` 에러를 반환하는 치명적 대소문자 결함.
- 1차 룩업 실패 시 `strings.EqualFold` 순회 또는 소문자 인덱싱을 통해 대소문자 무관 테이블 탐색 보장.

---

### [DONE] TDS 행 직렬화 대소문자 정규화 (TDS Row Case Bug) `BUG-TDS-ROW-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/wire/tds/token.go:774, 912, 923``

**Details & Resolution**:
`WriteRowDirect`/`WriteNBCRowDirect` 대소문자 무관 탐색 지원으로 올바른 데이터 전송 보장

- TDS의 `WriteRowDirect`와 `WriteNBCRowDirect`가 `row[col.Name]` 엄격 일치 조회만 수행하여, 행 맵의 키와 컬럼명의 대소문자가 다를 때 `val`이 `nil`이 되어 실제 데이터가 존재함에도 MSSQL 클라이언트에 NULL을 전송하던 결함.
- `strings.EqualFold` 대소문자 무관 행 값 매칭 적용.

---

### [DONE] TDS sp_executesql 파라미터명 미인식 순서 왜곡 방지 (TDS RPC Param Name Bug) `BUG-TDS-RPC-PARAM-NAME-ORDER-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/wire/tds/server.go:1205-1216``

**Details & Resolution**:
`handleRPCExecuteSQL`에서 파라미터명(`@stmt`, `@params`) 검사로 JDBC/.NET 클라이언트 명명 파라미터 도치 오류 해결

- `handleRPCExecuteSQL`(`sp_executesql`)이 파라미터명(`p.Name`)을 검사하지 않고 `rpc.Params[0]`을 `@stmt`, `rpc.Params[1]`을 `@params`로 단정하여, JDBC나 .NET 드라이버가 파라미터 순서를 바꾸어 전달할 때 `@stmt`와 `@params`가 뒤바뀌어 SQL 구문 에러를 유발하던 결함.
- `p.Name`의 대소문자 무관 비교를 통해 `@stmt`와 `@params`를 정확히 식별 바인딩하도록 보강.

---

### [DONE] TDS RPC 사전 컴파일 플랜 실행 시 세션/트랜잭션 우회 방지 (TDS RPC Prep Tx Bypass Bug) `BUG-TDS-RPC-PREP-TX-BYPASS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/wire/tds/server.go:1031-1035, 1114-1117``

**Details & Resolution**:
`sp_execute`/`sp_prepexec` 패스트패스에서 독립 스토리지 세션 대신 세션 컨텍스트 및 `inTx` 트랜잭션 바인딩

- TDS 프로토콜 `sp_execute` 및 `sp_prepexec`의 VDBE 사전 컴파일 플랜 실행 경로에서 활성 세션 컨텍스트 및 `inTx` 트랜잭션을 무시하고 매번 독립 `s.engine.NewSession()`을 생성하여 실행함으로써, TDS 트랜잭션 내에서 실행된 준비된 문장이 트랜잭션 외부에서 즉시 영구 커밋되던 결함.
- TDS 세션 트랜잭션 상태를 연동하여 트랜잭션 내에서는 활성 트랜잭션 컨텍스트를 경유하도록 개선.

---

### [DONE] RecordToRow 대소문자 일치로 인한 NULL 덮어쓰기 방지 (Tuple Case Bug) `BUG-TUPLE-RECORD-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/tuple.go:63, 98``

**Details & Resolution**:
`rec[col.Name]` 엄격 조회로 대소문자가 다른 레코드 입력 시 컬럼 값이 NULL로 훼손되는 결함 해결

- `RecordToRow`, `RecordToRowWithArena`, `ApplyRecordToRow`에서 `rec[col.Name]` 엄격 일치 조회로 인해 대소문자가 다른 레코드 입력 시 컬럼 값을 `NullValue`로 강제 대체하거나 갱신을 누락하여 바이너리 튜플에 데이터 손실을 유발하던 결함.
- 대소문자 무관 탐색 보장.

---

### [DONE] 코디네이터 UPDATE SET 절 쉼표 오절단 방지 (Update Set Comma Bug) `BUG-UPDATE-SET-COMMA-SPLIT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_dml.go:1045-1052``

**Details & Resolution**:
따옴표 및 괄호 상태 추적 토큰 분해기로 문자열 리터럴/함수 인자 콤마 오분해 및 수식 파괴 해결

- 코디네이터 `execUpdate`에서 SET 절을 `strings.Split(setClause, ",")`로 단순 콤마 분해하여, `SET note = 'hello, world'`와 같은 문자열 리터럴 내부 콤마나 `SET full_name = CONCAT(first, ', ', last)` 등 함수 인자 내부 콤마까지 절단되어 수식이 파괴되고 컬럼 대입이 유실되던 결함.
- 따옴표 및 괄호 중첩 상태를 추적하는 정밀 SQL 절 토큰 분해기를 적용하여 콤마 오절단 방지.

---

### [DONE] UPDATE SET 절 다중 인자 수식 사일런트 유실 방지 (Update Set Split Bug) `BUG-UPDATE-SET-SPLIT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_dml.go:1045``

**Details & Resolution**:
`strings.Split(setClause, ",")` 단순 분해 제거로 다중 인자 함수 및 리터럴 콤마 분해 유실 방지

- `ExecuteUpdate`의 `SET` 절 분해 시 `strings.Split(setClause, ",")` 단순 분해로 인해 `CONCAT(a, b)` 등 함수 내 콤마 분해 시 후속 구문이 조용히 유실되던 결함.
- 괄호 및 따옴표 중첩 추적 기반 최상위 콤마 토큰 분해 적용.

---

### [DONE] Upsert 충돌 정합성 (Upsert Bug) `BUG-UPSERT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/engine/sqlexec_dml.go:492, 841``

**Details & Resolution**:
`ON CONFLICT (col)` 충돌 컬럼 대소문자 정규화로 Upsert 유니크 위반 방지

- `execInsert` 및 `execInsertSelect`의 `ON CONFLICT (col)` 충돌 행 검사 시 `r[cc]`와 `rec[cc]`가 대소문자 차이로 매칭에 실패하여 중복 키 위반 에러가 발생하던 결함.
- 충돌 컬럼을 스키마 공식 명칭으로 정규화하고 대소문자 무관 비교 적용.

---

### [DONE] VDBE 복합 인덱스 인코딩 정합성 (VM Composite Index Bug) `BUG-VM-COMP-IDX-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/runtime/vm.go:1232-1237``

**Details & Resolution**:
VDBE VM에서 복합 인덱스를 단일 인덱스로 착각하고 비인코딩 스캔하여 0건 반환되는 결함 수정

- VDBE `findMatchingIndex`에서 첫 번째 컬럼만 일치하면 복합 인덱스를 선택하여 스칼라 단일 값을 `tx.IndexScan`에 전달함으로써, `memengine` 복합 인덱스 NUL 인코딩과의 불일치로 인해 0건이 반환되던 결함.
- 단일 컬럼 인덱스 우선 매칭 및 복합 인덱스 프리픽스 인코딩 정합성 보장.

---

### [DONE] VDBE DML 컬럼명 대소문자 정규화 및 값 보존 (VM DML Col Case Bug) `BUG-VM-DML-COL-CASE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/runtime/vm_dispatcher.go:598-608, 665-671`, `pkg/storage/tuple.go:63`, `pkg/storage/memengine/tx.go:502-508``

**Details & Resolution**:
Record 컬럼 대소문자 차이로 인한 `RecordToRow`/`updateLocked` 값 유실 및 NULL 삽입 해결

- VDBE `OpInsert` 및 `OpUpdate`에서 AST 컬럼명을 그대로 `rec[col] = stVal`, `rec[a.Column] = stVal`로 맵에 대입하고, `RecordToRow` 및 `updateLocked`에서는 스키마 컬럼명(`col.Name`)으로 엄격 대소문자 조회를 수행하여, 쿼리 컬럼 대소문자가 스키마와 다를 경우(`INSERT INTO t (Name) ...` vs 스키마 `name`) 값이 유실되거나 NULL이 삽입/업데이트되던 결함.
- DML Record 생성 및 스토리지 레코드 변환 시 스키마 공식 컬럼명으로 정규화 또는 대소문자 무관 탐색 보장.

---

### [DONE] VDBE OpUpdate/OpDelete PK 미존재 시 ErrKeyNotFound 예외 크래시 방지 (VM DML PK Not Found Bug) `BUG-VM-DML-PK-NOT-FOUND-CRASH-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/runtime/vm_dispatcher.go:671, 719``

**Details & Resolution**:
VDBE 단일 PK 수정/삭제 시 대상 행 미존재 시 에러 중단 대신 표준 SQL 규격 준수 0건 처리(`@@ROWCOUNT = 0`) 보장

- VDBE 컴파일러가 최적화한 단일 PK 기반 `OpUpdate` 및 `OpDelete` 실행 시, 스토리지 계층(`f.tx.Update`, `f.tx.Delete`)이 반환한 `storage.ErrKeyNotFound`를 정상적인 0건 매칭(`@@ROWCOUNT = 0`)으로 처리하지 않고 치명적 런타임 에러로 판단하여 프로시저를 강제 중단(Catch 점프)시키던 SQL 규격 위반 결함.
- `ErrKeyNotFound` 수신 시 에러 전파 대신 `f.rowCount = 0` 설정 후 정상 완료로 분기.

---

### [DONE] VDBE OpInsert 상수 리터럴 기본값 주입 보장 (VM Insert Default Bug) `BUG-VM-INSERT-DEFAULT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/runtime/vm_dispatcher.go:611-626``

**Details & Resolution**:
6대 내장 함수 외 상수 리터럴 기본값(`DEFAULT 0`, `DEFAULT 'active'`) 누락 및 NULL 삽입 해결

- VDBE `OpInsert`에서 기본값(`DEFAULT`) 평가 시 6종의 하드코딩된 날짜/UUID 함수(`NEWID()`, `GETDATE()` 등)만 switch로 검사하고, `DEFAULT 0`, `DEFAULT 'active'`, `DEFAULT 1.0` 등 상수 리터럴 기본값을 전면 무시하여 생략된 컬럼에 `NULL`이 강제 삽입되던 결함.
- 상수 리터럴 기본값 표현식 및 수식 평가 파이프라인을 연동하여 스키마 기본값 100% 정상 주입 보장.

---

### [DONE] VDBE 변수대입 무작위 순회 방지 (IntoVar Bug) `BUG-VM-INTO-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/runtime/vm_dispatcher.go:1046, 1128``

**Details & Resolution**:
VDBE `SELECT ... INTO @var` 무작위 맵 순회 제거 및 `cols[0]` 결정론적 바인딩

- VDBE `SELECT ... INTO @var` 및 `OpIndexScan INTO @var` 처리 시 `for _, v := range rows[0] { break }` 무작위 맵 순회로 변수에 프로젝션된 첫 번째 컬럼이 아닌 엉뚱한 임의의 컬럼 값이 대입되던 결함.
- `cols[0]` 프로젝션 명칭을 통한 결정론적 조회(`toStorageValue(rows[0][cols[0]])`)로 수정.

---

### [DONE] VDBE 스칼라 서브쿼리 nil 패닉 및 컬럼 침묵 버림 방지 (VM Subquery Nil Bug) `BUG-VM-SUBQ-NIL-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/parser_expr.go:321`, `pkg/proc/compiler.go:1358``

**Details & Resolution**:
`(SELECT 1)` 등 FROM 없는 서브쿼리 nil 포인터 패닉 제거 및 다중 컬럼 서브쿼리 문법 에러 반환

- `(SELECT 1)` 등 `FROM` 절 없는 서브쿼리에서 `sel.From.Name`을 nil 검사 없이 역참조하여 런타임 패닉을 유발하고, `(SELECT a, b)`와 같은 다중 컬럼 서브쿼리를 에러 없이 첫 번째 컬럼만 남기고 침묵 버리는 결함.
- nil 가드 및 다중 컬럼 스칼라 서브쿼리 문법 에러 반환.

---

### [DONE] VDBE UPDATE 컬럼명 정규화 (VM Update Silent Loss) `BUG-VM-UPDATE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/proc/runtime/vm_dispatcher.go:668`, `pkg/proc/runtime/vm.go:959``

**Details & Resolution**:
VDBE `OpUpdate`/`execUpdateWhere` 컬럼명 대소문자 미정규화로 인한 업데이트 사일런트 유실 방지

- VDBE `OpUpdate` 및 `execUpdateWhere`에서 `rec[a.Column] = ...` 대입 시 쿼리 원문 컬럼명을 그대로 사용하여, 스키마 공식 컬럼명과의 대소문자 불일치 시 스토리지 병합에서 무시되어 업데이트가 사일런트 유실되던 결함.
- `schema.FindColumn`을 통해 스키마 공식 컬럼명으로 정규화하여 `rec`에 기록.

---

### [DONE] MemEngine WAL 테일 절단 시 기동 실패 방지 (WAL Torn Write Boot Bug) `BUG-WAL-TORN-BOOT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:55
- **Target Files**: ``pkg/storage/memengine/wal.go:349`, `pkg/storage/memengine/memengine.go:311``

**Details & Resolution**:
비정상 종료 시 손상된 미완료 레코드 이전 정상 지점까지 복구 완료 후 트렁케이트하여 정상 기동 보장

- 시스템 비정상 종료(Crash, 정전)로 인해 WAL 파일의 마지막 레코드가 중간에 잘린(Torn Write) 경우, `ReplayWAL`이 치명적 에러(`truncated WAL frame payload`)를 반환하여 `OpenWithOptions` 엔진 기동 전체를 영구 중단시키는 결함.
- 마지막 손상된 미완료 레코드 발견 시 이전 정상 커밋 지점까지 복구 완료 후 트렁케이트하고 정상 기동하도록 무결성 보장 (Golden Rule 1, 8).

---

### [DONE] WHERE IN 절 정합성 (WHERE IN Bug) `BUG-WHERE-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: ``pkg/engine/sqlexec_eval.go:339``

**Details & Resolution**:
`evaluateSingleConditionMap` 내 `WHERE IN (...)` 절 대소문자 무관 탐색 누락 보강

- `evaluateSingleConditionMap`의 다른 조건들과 달리 `WHERE col IN (...)` 블록에서만 대소문자 무관 루프가 누락되어 쿼리 결과가 0건으로 누락되던 결함.
- `strings.EqualFold` 대소문자 무관 탐색 루프 보강.

---

### [DONE] 윈도우 함수 RANGE 피어 프레임 집계 왜곡 방지 (Window Range Peer Bug) `BUG-WINDOW-RANGE-PEER-EVAL-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: ``pkg/execution/window.go:303-309, 480-510`, `pkg/planner/planner.go:63, 1713``

**Details & Resolution**:
`ORDER BY` 시 표준 기본값인 `RANGE` 피어 구간 확장을 구현하여 동순위 행 간 누적 집계 왜곡 해결

- `WindowIterator`가 `ORDER BY` 지정 시 SQL 표준 기본 프레임인 `RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`를 `ROWS` 기반 단순 행 인덱스 슬라이딩으로 축소 처리하여, 동순위 피어(Peer) 행들이 동일한 누적 집계값을 갖지 못하고 임의의 행 순서에 따라 서로 다른 누적값을 반환하는 SQL 표준 규격 위반 결함.
- `WindowFrame`에 `RANGE` 모드를 명시하고 동순위 키를 가진 피어 행들을 동일 프레임으로 묶어주는 피어 구간 확장 로직 구축.

---

### [DONE] COPY TO STDOUT 텍스트 포맷 탭/줄바꿈 이스케이프 보장 (Copy Out Unescaped Bug) `BUG-WIRE-COPY-OUT-UNESCAPED-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: ``pkg/wire/copy.go:488-497``

**Details & Resolution**:
표준 PostgreSQL 텍스트 포맷 이스케이프 규칙(`\t`, `\n`, `\\`) 적용으로 덤프 정합성 보장

- `handleCopyOut`이 텍스트 포맷 필드를 `strings.Join(fields, delimiter)`로 단순 결합하면서 필드 내 탭(`\t`), 줄바꿈(`\n`, `\r`), 백슬래시(`\\`)를 전혀 이스케이프하지 않아, 탭이나 줄바꿈이 포함된 문자열 데이터 내보내기 시 행/열 경계가 무너져 파일이 손상되고 이후 재가져오기 시 컬럼이 밀리는 결함.
- 표준 PostgreSQL 텍스트 포맷 이스케이프 규칙(`\t`, `\n`, `\r`, `\\`)을 적용하여 데이터 덤프 정합성 보장.

---

### [DONE] COPY FROM STDIN 파싱 에러 침묵 및 0값/현재시각 변조 방지 (Copy Silent Corrupt Bug) `BUG-WIRE-COPY-SILENT-CORRUPT-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: ``pkg/wire/copy.go:518-545``

**Details & Resolution**:
파싱 실패 시 명시적 SQL 구문 에러 반환 및 전체 데이터 타입 정밀 파싱 지원

- `parseFieldVal`이 파싱 에러를 조용히 삼키고 `IntValue(0)`, `FloatValue(0)`, `DateTimeValue(time.Now())` 등 임의의 기본값으로 치환하며, `Decimal`/`UUID`/`Date` 등 기타 데이터 타입을 전부 `StringValue`로 떨어뜨려, `COPY FROM STDIN` 시 잘못된 입력 데이터가 에러 없이 0이나 현재 시각으로 무단 변조되어 저장되는 치명적 데이터 오염 결함.
- 파싱 실패 시 명시적 SQL 구문 에러를 클라이언트에 반환하고 전체 데이터 타입 정밀 파싱 지원.

---

### [DONE] COPY FROM STDIN 트랜잭션 세션 우회 및 독립 자동 커밋 방지 (Copy Tx Bypass Bug) `BUG-WIRE-COPY-TX-BYPASS-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: ``pkg/wire/copy.go:183, 302-358``

**Details & Resolution**:
활성 세션 컨텍스트 바인딩으로 트랜잭션 범위 내 원자적 COPY 및 롤백 보장

- `handleCopyIn`이 호출 인자로 전달된 세션 식별자(`sessID`)를 무시하고 독자적인 새 세션을 열어 트랜잭션을 즉시 커밋(`tx.Commit()`)함으로써, 클라이언트가 명시적 트랜잭션 블록(`BEGIN` ... `ROLLBACK`) 내에서 `COPY FROM STDIN`을 실행하더라도 롤백되지 않고 강제 영속화되는 트랜잭션 원자성 파괴 결함.
- 활성 세션 컨텍스트 및 트랜잭션 핸들(`cs.Tx`)을 바인딩하여 트랜잭션 범위 내 원자적 COPY 실행 및 롤백 보장.

---

### [DONE] PGWire 파라미터 치환 시 재귀 치환 왜곡 및 숫자형 문자열 따옴표 탈락 방지 (PG Param Substitute Bug) `BUG-WIRE-PG-PARAM-SUBSTITUTION-INJECTION-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
`strings.ReplaceAll` 대신 단일 패스 토큰화 치환 적용 및 파라미터 실제 데이터 타입 보존으로 파라미터 재귀 오염 해결

---

### [DONE] WiredTiger 외래키 무결성 검증 구축 (WT FK Bug) `BUG-WT-FK-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: ``pkg/storage/wiredtiger/wt_engine.go:1195-1380``

**Details & Resolution**:
WiredTiger `Insert`/`Update`/`Delete` 시 외래키 참조 무결성 검사 및 CASCADE/RESTRICT 완전 연동

- WiredTiger 스토리지 백엔드의 `Insert`, `Update`, `Delete` 함수에 외래키(FK) 제약조건 검증 및 CASCADE/RESTRICT 처리 로직이 전무하여, 가짜 외래키 삽입 및 참조 부모 행 무단 삭제로 고아 데이터가 무제한 생성되던 치명적 결함.
- WiredTiger CGO 트랜잭션 계층에 외래키 참조 무결성 검증 및 부모 행 삭제/갱신 시 연쇄 처리 연동.

---

### [DONE] WiredTiger 복합 인덱스 정합성 (WT Composite Index Bug) `BUG-WT-INDEX-COMP-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: ``pkg/storage/wiredtiger/wt_engine.go:1588-1590, 1617-1620``

**Details & Resolution**:
WiredTiger 복합 인덱스 스캔 시 첫 번째 컬럼만 비교하고 컬럼 누락 시 TypeUnknown 0(동일) 판정하는 결함 수정

- WiredTiger `wtFilteredIndexCursor`에서 복합 인덱스 스캔 시 첫 번째 컬럼(`c.idx.Columns[0]`)만 필터링하고 나머지 복합 컬럼을 무시하며, `FindColumn` 실패 시 초기화되지 않은 `storage.Value{}`(TypeUnknown)가 `Compare`에서 0(동일함)을 반환하여 엉뚱한 행이 필터를 통과하던 결함.
- 복합 인덱스 키 디코딩 및 미초기화 값의 비교 실패(Fail) 명시화.

---

### [DONE] WiredTiger IndexScanDesc 역정렬 스텁 결함 해결 (WT Index Desc Bug) `BUG-WT-INDEX-DESC-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: ``pkg/storage/wiredtiger/wt_engine.go:1532-1534``

**Details & Resolution**:
`WTTx.IndexScanDesc` 정방향 스캔 반환 결함 수정으로 `ORDER BY DESC` 인덱스 스캔 역정렬 보장

- `WTTx.IndexScanDesc`가 커서를 역방향으로 순회하지 않고 정방향 `tx.IndexScan`을 그대로 반환하는 스텁으로 작성되어 있어, WiredTiger에서 `ORDER BY col DESC` 인덱스 쿼리 시 사후 정렬이 생략되어 오름차순(ASC)으로 반대 결과를 반환하던 결함.
- WiredTiger 커서 역방향 순회 연동 또는 슬라이스 반전 적용.

---

### [DONE] WiredTiger 60분 유휴 세션 경고 방지 및 세션 리셋 (WT Sweep Server Bug) `BUG-WT-SWEEP-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: ``pkg/storage/wiredtiger/wt_engine.go:265`, `pkg/engine/session_pool.go:184``

**Details & Resolution**:
`startPeriodicSync` 고아 세션 상시 점유 제거, `session->reset` 바인딩 및 `SessionPool` TTL 리퍼 연동

- `startPeriodicSync` 고아 세션 상시 점유 및 유휴 세션 미회수로 인해 기동 60분 후 WiredTiger `sweep-server`로부터 `Session did not run a sweep for 60 minutes` 경고가 발생하는 결함.
- `WT_SESSION::reset` CGO 바인딩 및 주기적 세션 리셋, `SessionPool` 유휴 세션 TTL 리퍼 도입으로 60분 와치독 방지.

---

### [DONE] WiredTiger 스토리지 엔진 보조 유니크 인덱스 제약 검증 및 색인 누락 방지 (WT Unique Index Bug) `BUG-WT-UNIQUE-INDEX-UNENFORCED-1`
- **Domain**: Integrity | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
`insertLocked`/`updateLocked` 시 유니크 인덱스 제약 검증 및 `IndexScan`의 풀 테이블 스캔 폴백을 B-Tree 커서 검색으로 개편

---

### [PENDING] 인메모리 컴팩트(compact) 모드 4대 게이트 검증 및 기본값 공식 승격 `CFG-MEMORY-COMPACT-DEFAULT-1`
- **Domain**: STORAGE | **Priority**: P1 | **Updated**: 2026-11-10 01:24:23
- **Plan Doc**: [`notes/plans/STORAGE_OPTIONS_AND_MEMORY_COMPACT_PLAN.ko.md`](notes/plans/STORAGE_OPTIONS_AND_MEMORY_COMPACT_PLAN.ko.md)

---

### [DONE] 스토리지 옵션 계층 분리(--disk/--memory) 및 하위 호환성 매핑 `CFG-STORAGE-OPT-1`
- **Domain**: CONFIG | **Priority**: P1 | **Updated**: 2026-11-10 01:40:41
- **Plan Doc**: [`notes/plans/STORAGE_OPTIONS_AND_MEMORY_COMPACT_PLAN.ko.md`](notes/plans/STORAGE_OPTIONS_AND_MEMORY_COMPACT_PLAN.ko.md)

---

### [PENDING] 외부 앱 호환성 규칙 선언적 외재화 (Declarative Compatibility Engine) `COMPAT-DECLARATIVE-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
- 대상: `COMPAT-TABLE-DRIVEN-1`에서 정식화된 Go 구조체 테이블
- 목표: 정식화된 구조체 테이블을 기반으로 필요 시 외부 텍스트 설정 파일(YAML/JSON 또는 embed.FS)로 분리하여 런타임 동적 규칙 주입 가능성 검토 (YAGNI 평가 후 결정)

---

### [DONE] 10개 실전 서비스 호환 규칙 Table-Driven 구조 및 안티패턴 키워드 매칭 전면 정식화 `COMPAT-TABLE-DRIVEN-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md`](notes/plans/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md)

**Details & Resolution**:
- 설계 계획서: [`notes/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md`](notes/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md)
- 회귀 테스트: `pkg/wire/param_rules_test.go`, `pkg/wire/command_tags_test.go`, `pkg/engine/*_compat_test.go`, `pkg/engine/pk_bypass_test.go`, `pkg/planner/planner_test.go`
- 대상 서비스: 10개 실전 서비스 (Gitea, Vaultwarden, Casdoor, Ghost, Answer, Affine, Metabase, Umami, Woodpecker, Vikunja)
- 엔진(`pkg/engine`), 와이어(`pkg/wire`), 가상 카탈로그(`pkg/catalog/virtual`), 플래너(`pkg/planner`)에 흩어진 case-by-case 분기문 및 ad-hoc 원시 문자열 매칭을 Go 정적 구조체 테이블 및 `sqlutil` SSOT 토크나이저 원형으로 전면 정식화 완료:
1. `pkg/wire/param_rules.go`: 파라미터 OID 추론 규칙 테이블 (`defaultParamOIDRules`, `LookupParamOID`)
2. `pkg/wire/command_tags.go`: PGWire VDBE DML 커맨드 완료 태그 테이블 (`defaultCommandTagRules`, `formatPgCommandComplete`)
3. `pkg/catalog/virtual/sys_tables.go`: 가상 카탈로그 뷰 매핑 테이블 (`sysViewAliases`, `IsSysCatalogTable`), 기존 40줄 `switch norm` 제거
4. `pkg/planner/bounds.go`: `HasDisjunction` 공통 술어를 통한 복합 `OR` 단일 인덱스 색인 방어 일원화
5. 엔진/플래너/와이어 전반의 나이브 `strings.Contains`/`strings.Index` 키워드 매칭 안티패턴 전수 감사 및 `sqlutil.ContainsKeyword`/`FindTopLevelKeyword` 대체 완료 (PK 바이패스, DDL `IF [NOT] EXISTS`, DML `OUTPUT`/`RETURNING`, 윈도우 `OVER`/`BETWEEN...AND`, 서브쿼리 `NOT EXISTS`/`NOT IN`, `IS [NOT] NULL`)
- 10개 실전 앱 회귀 테스트 100% 통과, 벤치마크 18.7만 TPS 유지 확인, 전체 25개 패키지 race detector 0 data race 검증 완료

---

### [PENDING] 공식 문서 및 엔지니어링 지식 체계 10대 핵심 고도화 마스터 플랜 `DOC-REVAMP-10`
- **Domain**: Documentation | **Priority**: P2 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/DOCUMENTATION_SYSTEM_UPGRADE_PLAN.ko.md`](notes/plans/DOCUMENTATION_SYSTEM_UPGRADE_PLAN.ko.md)

---

### [DONE] LionSQL 자체 백엔드 기반 정형 스프린트/태스크 관리 시스템 구축 (Dogfooding) `DOGFOOD-SPRINT-1`
- **Domain**: Performance | **Priority**: P1 | **Updated**: 2026-11-07 14:02:56

**Phases & Checklist**:
- [x] **Phase 1 (`SCHEMA-SPRINT`)**: `sprint_tasks` 정형 테이블 스키마 DDL 작성 및 초기 데이터(182건: TODO 62건 + BUG_ARCHIVE 120건) 마이그레이션
- [x] **Phase 2 (`CLI-SPRINT`)**: `tools/sprint` 도구 구현 (`sprint init`, `sprint list`, `sprint set`, `sprint add`, `sprint backup` 마크다운 뷰 `SPRINT_TASKS_BACKUP.md` 자동 동기화)
- [x] **Phase 3 (`UI-SPRINT`)**: LionSQL 내장 Web Studio(`/_/`) 테이블 조회 및 백엔드 REST API 영구 연동 (Git 백업과 DB SSOT 상호 보완 완성)

---

### [IN_PROGRESS] kill -9 크래시 복구 장기 라운드 검증 `DURA-KILL-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: `cmd/crashtest`

**Phases & Checklist**:
- [x] **도구 구현 (`cmd/crashtest`)**: 다중 워커 계좌 이체 + SHA-256 커밋 장부(`crash_ledger`) + 4단계 정합성 오라클(D1 커밋 내구성, D2 원자성 `SUM(balance)=200,000`, D3 체크섬 일관성, D4 복구 가능성) 구현
- [x] **변형 시나리오 구현**: `SIGKILL`, `SIGKILL+DOUBLE_KILL`(복구 중 재크래시 멱등성), `SIGKILL+TORN_WAL_TAIL`(로그 꼬리 절단 손상 복구), `SIGTERM` 대조군
- [x] **크래시 결함 수정 4건**:
- [x] **100라운드 누적 완주**: WiredTiger 100/100 PASS (`344,129` tx 복구) & In-Memory OCC 100/100 PASS (`301,184` tx 복구, 간헐적 `SIGKILL` 직전 마지막 1건 tx AOF 플러시 타이밍 윈도우 별도 기록) — 단일 러닝 100라운드 연속 무손실 복구 입증
- [ ] **초장기 라운드 누적 검증**: 500 ~ 5,000 라운드 장기 크래시 복구 무결성 검증

**Details & Resolution**:
1. WiredTiger 유저 버퍼 유실 방지(`transaction_sync`)
2. `CREATE TABLE` 제약 조건 접두사 충돌(`checksum` 누락 방지)
3. PGWire 텍스트 파라미터 숫자 오인 방지
4. WiredTiger 세션 간 커서 비동기 해제 동시성 충돌(`go_wt_cursor_close` SIGSEGV) 완치 및 `force=true` 안전 드롭

---

### [DONE] SQLLogicTest (SLT) 블랙박스 정합성 오라클 검증 `DURA-SLT-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/SQLLOGICTEST_INTEGRATION_PLAN.ko.md`](notes/plans/SQLLOGICTEST_INTEGRATION_PLAN.ko.md)
- **Target Files**: `cmd/sqllogictest, pkg/slt`

**Phases & Checklist**:
- [x] **SLT 러너 구현 (`cmd/sqllogictest`, `pkg/slt`)**: 순수 Go 기반 표준 SLT 파일 파서, In-Process 및 PGWire 듀얼 모드, rowsort/valuesort/nosort 정렬 및 MD5 해시 대조 엔진
- [x] **Phase 1 (코어 로직 & 구문 스위트)**: `select1.slt`, `where.slt`, `join.slt`, `null.slt`, `aggregate.slt` (100% PASS)
- [x] **Phase 2 (화이트스페이스 & 엣지 케이스 스위트)**: 탭/개행/다중 공백(`whitespace.slt`), 키워드 식별자 충돌 및 리터럴 포함(`boundary.slt`), 복합 키워드 변형(`IF EXISTS`, `WITHIN GROUP`) (100% PASS)
- [x] **Phase 2.5 (핵심 영역 고난도 스위트 1차~6차 확장)**: 조건 분기 및 단락 평가(`case_expr.slt`), 다중 컬럼/NULL/집계 고립(`distinct.slt`), IN/EXISTS/스칼라 서브쿼리(`subquery.slt`), 윈도우 함수(`window.slt`), 집합 연산(`setops.slt`), 내장 스칼라 함수(`functions.slt`), 복합 DML 변이(`dml_mutate.slt`), 범위 조건(`between.slt`), 리스트 조건(`in_list.slt`), 패턴 매칭(`like.slt`), 그룹 필터링(`group_having.slt`), 페이지네이션(`limit_offset.slt`), 공통 테이블 식(`cte.slt`), 사칙연산 우선순위/모듈로(`arithmetic.slt`), 타입 캐스팅(`cast_type.slt`), 3치 논리 및 드모르간 법칙(`boolean_3vl.slt`), 다중/셀프/안티/크로스 조인(`join_advanced.slt`), JSON 연산자(`json_ops.slt`), 다중 정렬 및 Upsert(`order_upsert.slt`), 중첩 대수식 스트레스(`expr_stress.slt`), 복합 집계 및 `COALESCE(SUM(...), 0)` 스트레스(`agg_stress.slt`), 파생 테이블 다중 조인(`derived_join.slt`), 트랜잭션 롤백 격리(`tx_isolation.slt`), 복합 3VL 불리언 논리(`boolean_logic_stress.slt`), 대규모 IN 및 NOT IN NULL 함정(`in_subquery_stress.slt`), 문자열 연결 및 이스케이프(`datetime_string_stress.slt`) (총 33개 파일, 240문장, 181쿼리 — `memory`/`wiredtiger` × `inproc`/`pgwire` 4대 매트릭스 전수 100% PASS)
- [x] **엔진 버그 16건 발굴 및 완치**:

**Details & Resolution**:
- 설계 계획서: [`notes/SQLLOGICTEST_INTEGRATION_PLAN.ko.md`](notes/SQLLOGICTEST_INTEGRATION_PLAN.ko.md)
1. `HasPrefixKeyword` 다중 공백/탭/개행 DDL 라우팅 버그 완치
2. SQL 3-valued logic `NULL = NULL` 및 `val = NULL` UNKNOWN(falsy) 표준 정합성 완치
3. `BinaryExpr.String()` 연산자 우선순위 부모 괄호 탈락 (`(A OR B) AND C`) 및 우측 결합성(`a - (b + c)`) deparser 완치
4. SLT 러너 멀티컬럼 공백 포함 셀 파서 탭/길이 정합성 완치
5. `FunctionCallExpr` 내 `DISTINCT` 파싱 누락으로 인한 `COUNT(DISTINCT ...)` 평가 결함 완치
6. `BetweenExpr` (`NOT BETWEEN`) 평가 시 `NULL` 피연산자가 `true`로 오판되던 SQL 3VL 결함 완치
7. `InExpr` (`NOT IN`) 평가 시 `NULL` 대상 컬럼 및 리스트 내 `NULL` 원소(`x NOT IN (100, NULL)`)의 SQL 3VL UNKNOWN 정합성 완치
8. `%` 모듈로 연산자(`TokenPercent`) 렉서 토큰화, Pratt 파서 우선순위(`precProduct`), AST deparser(`%`) 및 `EvalRowExpr` 평가 지원 누락 완치
9. `UnaryExpr.String()` 내 `BinaryExpr` 괄호 보존(`NOT (x = 10)`, `-(a + b)`), `TokenNot` 파서 우선순위(`precNot`), 단항/논리 연산자(`NOT NULL`, `-NULL`, `FALSE AND NULL`, `TRUE OR NULL`) SQL 3VL 정합성 완치
10. Self-Join(`slt_emp2 e JOIN slt_emp2 m`) 시 동일 컬럼 별칭(`e.emp_name`, `m.emp_name`)이 `Tuple.ToMap()` 변환 중 덮어써지던 스키마 별칭 충돌 결함 완치
11. `CROSS JOIN` (`ON` 절 없는 카티션 곱) 파서 허용 및 `HashJoinIterator` 빈 키 매칭 기반 카티션 곱 실행 완치
12. `GROUP BY` 프로젝션 내 복합 집계 표현식(`COALESCE(SUM(o.amount), 0)`, `SUM(a) + SUM(b)`) 실행 시 내부 중첩 집계 함수를 누적하지 않고 첫 행만 참조하던 `AggregateIterator` 2-Pass 평가 결함 완치
13. `||` 문자열 연결 연산자(`TokenConcat`) 렉서 토큰화 및 Pratt 파서, Volcano/Row 수식 평가자 지원 완치
14. `NOT IN (SELECT ...)` 서브쿼리 내 `NULL` 키가 1개라도 포함된 경우 SQL 3VL UNKNOWN 규칙에 의해 모든 행이 suppress되어야 함에도 행이 반환되던 `HashJoinIterator` ANTI 조인 결함 완치
15. 프로젝션 내 스칼라 서브쿼리의 집계 함수(`(SELECT avg(c) FROM t1)`)가 외부 쿼리를 전역 집계 쿼리로 오인시켜 1행만 반환되던 CBO 플래너 `hasTopLevelAggregateExpr` 결함 완치
16. `ORDER BY 1, 2` 등 서수(Ordinal Position) 정렬 지정 시 프로젝션 이전에 실행되어 테이블 원본 컬럼으로 정렬되거나 무시되던 CBO 플래너 및 `SortIterator` 서수 인덱스 평가 결함 완치

---

### [DONE] SQLLogicTest 1.0 릴리즈 풀 스위트 (60,000+ 쿼리) 전수 인증 `DURA-SLT-2`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md`](notes/plans/SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md)
- **Target Files**: `cmd/sqllogictest`

**Phases & Checklist**:
- [x] **병렬 워커 풀(`-parallel N`) 및 4대 매트릭스 러너 완성**: `cmd/sqllogictest`에 `-engine wiredtiger` 디스크 격리 엔진, `-mode pgwire` 실제 PostgreSQL v3 TCP 소켓 실행기(`PGWireExecutor`), `-parallel N` 고루틴 워커 풀 구현 완료
- [x] **Phase 1 (고위험 핵심 도메인 33개 스위트, 181개 쿼리 전수 인증)**: 다중 중첩 사칙연산, `COALESCE`+`NULLIF` 복합 대수식, 3VL 드모르간 항등식, `LEFT JOIN`+`GROUP BY`+`COALESCE(SUM(...), 0)`, 트랜잭션 격리, 복합 불리언 논리, 대규모 IN 및 NOT IN NULL 함정 검증 100% 완료
- [x] **Phase 2 (SQLite 공식 `select1.test` ~ `select5.test` 10,706 쿼리 전수 100% PASS)**:
- [x] **Phase 2.5 (1.0 GA 풀세트 40,000+ 쿼리 스크립트 및 공식 가이드 완비)**:
- [x] **Phase 3 (SQLite 공식 `index` / `random` / `evidence` 스위트 확장)**: 인덱스 복합 스캔, 난수 대수식, 통계적 카디널리티 스트레스 테스트 연동 (107개 파일, 551,217 쿼리 전수 100% PASS)
- [ ] **Phase 6 (1.0 릴리즈 게이트 통합)**: 1.0 릴리즈 직전 풀 스위트 듀얼 엔진 100% 무오류 인증

**Details & Resolution**:
- 설계 계획서: [`notes/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md`](notes/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md)
- 확장 로드맵: [`notes/SQLLOGICTEST_EXPANSION_ROADMAP_PLAN.ko.md`](notes/SQLLOGICTEST_EXPANSION_ROADMAP_PLAN.ko.md)
- 업계 적용 및 권장 범위: [`notes/SQLLOGICTEST_INDUSTRY_PRACTICES_AND_SCOPE.ko.md`](notes/SQLLOGICTEST_INDUSTRY_PRACTICES_AND_SCOPE.ko.md)
- `select1.test`: 1,031 쿼리 (2.29s)
- `select2.test`: 1,031 쿼리 (2.18s)
- `select3.test`: 3,351 쿼리 (5.88s)
- `select4.test`: 3,857 쿼리 (6.36s)
- `select5.test`: 1,436 쿼리 (5.67s, 최대 64개 테이블 콤마 조인)
- 총 10,706개 쿼리 전수 100% 무오류 인증 통과 (소요 시간 22.69s, 0 data races).
- BFS 기반 Spanning Tree 조인 키 추론(`pushdown.go`) 및 Greedy Join Ordering 필터 우선순위화(`planner.go`)로 최대 64개 테이블 조인 카테시안 폭발 완치.
- 설계 계획서: [`notes/SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md`](notes/SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md)
- 원클릭 다운로드 스크립트: [`scripts/fetch_slt.sh`](scripts/fetch_slt.sh) (`--suite all|full|evidence|index|random`, 온디맨드 로컬 캐싱 지원)
- 공식 사용자 검증 가이드: [`doc/SQLLOGICTEST_GUIDE.ko.md`](doc/SQLLOGICTEST_GUIDE.ko.md) / [`doc/SQLLOGICTEST_GUIDE.md`](doc/SQLLOGICTEST_GUIDE.md)
- 고성능 병렬 러너: `bin/sqllogictest` 8코어 병렬 15~20초 완주 파이프라인

---

### [IN_PROGRESS] 24h ~ 72h 장기 소크 테스트 `DURA-SOAK-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: `cmd/soaktest`

**Phases & Checklist**:
- [x] **소크 테스트 도구 구현 (`cmd/soaktest`)**: 8 워커 계좌 이체 + Prometheus 지표 수집 + RSS/FD/디스크/p99 추이 자동 판정 엔진
- [x] **WiredTiger 스모크 완주**:
- [x] **In-Memory OCC 소크 완주 및 2대 동시성 결함 완치**:
- [ ] **24시간 ~ 72시간 장기 소크 완주**: 장기 무중단 운영을 통한 극한의 메모리/고루틴/디스크 누수 0 검증

**Details & Resolution**:
- `719,970` ops 완주, 커밋 장부 일치(`393,523` 행), 잔액 불변(`SUM(balance)=1,000,000`, 위반 0건)
- `open_fds = 28` 완전 고정 (핸들 누수 0), `p99 = 6.23ms` 성능 열화 0
- WiredTiger 500MB 캐시 워밍업 분석 완료 및 `/metrics` Go 힙/고루틴 수집 반영
- `Scan()` 찢긴 읽기(torn read) 결함 발굴 및 `snapshotRowsAndKeys` 카노니컬 샤드 RLock 스냅샷으로 완치.
- 다중 워커 계좌 이체 간 `KeyLatch` 상호 교차 대기 데드락 및 `CancelSessionQuery`의 `cs.Mu` 블로킹 결함 발굴, 25ms 타임아웃 `ErrSerializationFailure` (`SQLSTATE 40001`) 반환 및 `cancelMu` 분리로 원천 완치 (`pkg/storage/key_latch.go`, `pkg/engine/coordinator_impl.go`, `session.go`).
- 5분 연속 소크 완주: `60,000` ops (`32,986` commits 일치), 잔액 불변(`SUM(balance)=1,000,000`, 위반 0건), RSS 비율 1.00, FD 비율 1.00 (`24` 고정), 고루틴 비율 1.00 (`22` 고정), p99 비율 0.92 (`2.18ms ➔ 2.02ms`, 성능 열화 0) 전 지표 PASS.

---

### [PENDING] Materialized View Virtual Alias & Tolerant Refresh (Mattermost/Enterprise) `FEAT-MATVIEW-ALIAS-1`
- **Domain**: FEAT | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md`](notes/plans/MATERIALIZED_VIEW_VIRTUAL_ALIAS_PLAN.ko.md)

---

### [PENDING] WiredTiger 호환 자체 Pure-Go MVCC 스토리지 엔진 구현 및 CGo 분리 (Post-1.0) `FEAT-PUREGO-WT-ENGINE`
- **Domain**: Storage | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
1. CGo 호출 오버헤드 제거 및 Go 고루틴 스케줄러 밀착 극강 성능 달성
2. Windows 환경에서 CGo 제약으로 인메모리에 머물던 한계를 극복하고, CGO_ENABLED=0 순수 단일 정적 바이너리로 Windows 네이티브 디스크 영속 MVCC B+Tree 완벽 지원 ([RULE:HermeticParity], [RULE:StaticBinary])

---

### [PENDING] LSM Time-Series Chunk Storage & time_bucket `FEAT-TIMESERIES-LSM-1`
- **Domain**: FEAT | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`](notes/plans/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md)

---

### [DONE] Dynamic Runtime Tolerant DDL CLI Switch & PostgreSQL 42704 SQLSTATE `FEAT-TOLERANT-DDL-1`
- **Domain**: FEAT | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/TOLERANT_DDL_MODE_PLAN.ko.md`](notes/plans/TOLERANT_DDL_MODE_PLAN.ko.md)

---

### [PENDING] Pure-Go Vector Search Engine (pgvector compat) `FEAT-VECTOR-1`
- **Domain**: FEAT | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md`](notes/plans/POST_1.0_VECTOR_TIMESERIES_PLAN.ko.md)

---

### [DONE] pkg/sqlutil SQL 파서/정규화기 결함 보강 및 렉서 공통화 `FIX-SQLUTIL-PARSER-1`
- **Domain**: Engine | **Priority**: P2 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/SQL_PARSER_HARDENING_PLAN.ko.md`](notes/plans/SQL_PARSER_HARDENING_PLAN.ko.md)

---

### [DONE] DuckDB-Style Friendly SQL 문법 지원 `FRIENDLY-SQL`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md`](notes/plans/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md)

**Details & Resolution**:
- `SELECT * EXCLUDE`, `FROM <table>;`, `GROUP BY ALL`, CSV 파일 직접 조회 ([`notes/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md`](notes/DUCKDB_STYLE_FRIENDLY_SQL_ERGONOMICS_PLAN.ko.md))

---

### [DONE] CBO Planner 및 VDBE 잔여 정규식 100% 완전 제거 `GA-REGEX-FINAL-2`
- **Domain**: Engine | **Priority**: P2 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md)

---

### [DONE] 쿼리 실행 런타임 LIKE 정규식 100% 완전 제거 `GA-REGEX-FINAL-3`
- **Domain**: Engine | **Priority**: P2 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md)

---

### [PENDING] 연동 앱 섀도 모드 실운영 도그푸딩 (Vaultwarden, Umami, Answer 등) `GA-VALID-3`
- **Domain**: Ecosystem | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

---

### [DONE] Apache Answer (Q&A 지식베이스) `INTEG-ANSWER-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
- 연동 보고서: [`notes/ANSWER_INTEGRATION_REPORT.ko.md`](notes/ANSWER_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`guide/ANSWER_INTEGRATION_GUIDE.ko.md`](guide/ANSWER_INTEGRATION_GUIDE.ko.md) / [`guide/ANSWER_INTEGRATION_GUIDE.md`](guide/ANSWER_INTEGRATION_GUIDE.md)
- 스택: Go + XORM / PGWire (`TCP :5433`)
- 35개 테이블 스키마 자동 생성, VDBE 다중 행 배치 Identity 키 자동 발급, 관리자 로그인, 질문/답변 E2E 100% 무오류 완료 (Answer 소스 수정 0줄)

---

### [PENDING] Directus (Headless CMS/데이터 API) Fast-Fail 호환성 탐색 `INTEG-DIRECTUS-1`
- **Domain**: Ecosystem | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

---

### [PENDING] Ghost (블로그/출판 플랫폼) Fast-Fail 호환성 탐색 `INTEG-GHOST-1`
- **Domain**: Ecosystem | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

---

### [PENDING] Mattermost (팀 협업 채팅) — **[v1.1.0 로드맵]** `INTEG-MATTERMOST-1`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
- 스택: Go + `sqlx`/`squirrel` / PGWire (`TCP :5433`)
- 대규모 팀 채널 및 메시지 인덱싱 검증
## 4. Architectural Invariants & Spec-Out Invariants (13대 불변 원칙)
To preserve sub-microsecond latency and single-binary simplicity, the following features are permanently **Spec-Out** (detailed rationales preserved in [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md) and [`GEMINI.md`](GEMINI.md)):
1. **Duplicate PL/pgSQL Interpreter**: Stored procedures unify on native T-SQL VDBE VM (`pkg/proc`).
2. **Legacy XML DML / XQuery**: Standardized on modern JSON functions (`ISJSON`, `JSON_VALUE`) and native Go parsers.
3. **In-Memory OCC Background Vacuum**: Workspace atomic swap/discard leaves zero dead tuples; vacuum workers rejected.
4. **POSIX Shared Memory (Arrow IPC) Snapshots**: Standardized on CDC/WAL event streams.
5. **Distributed 2PC Coordinators (XA / MS DTC)**: Standardized on 1-Writer Multi-Reader WAL replication.
6. **In-Kernel OAuth2 Web Flows**: Standardized on Client-Side OIDC Token Exchange.
7. **In-Kernel Blob File Storage**: Standardized on S3 pre-signed direct client uploads.
8. **Immature Pure-Go Storage Engines**: Focused exclusively on WiredTiger MVCC and In-Memory OCC.
9. **In-Kernel Slow-Query Webhooks**: Standardized on Prometheus `/metrics` and OpenTelemetry OTLP.
10. **In-Kernel Wasm Sandbox**: Standardized on zero-allocation Go Native UDF SPI (`RegisterScalarFunction`).
11. **Total Bytecode Translation for Complex Queries**: 2-Tier query engine (Tier 1 VDBE, Tier 2 Volcano CBO) invariant.
12. **Pure Columnar Vectorization Engine Full Rewrite**: Rejected due to Row-storage I/O scan physical limits and single-binary KISS code complexity; standardized on 1,024-row chunk streaming (`ChunkedIterator`, `PullChunk`) and zero-unboxing slot optimizations.
13. **MySQL-Specific DML & Legacy Tabular EXPLAIN Extensions (`ON DUPLICATE KEY UPDATE`, Comma `LIMIT offset, count`, 8-Column Tabular `EXPLAIN`)**: Permanently Spec-Out. Standard ANSI/MSSQL `MERGE` and PostgreSQL `ON CONFLICT DO UPDATE` already provide full functional coverage without 3-way transactional branching. Flat 8-column tabular `EXPLAIN` flattens and destroys modern hierarchical Volcano CBO plan trees (which even MySQL 8.0 deprecated in favor of `FORMAT=TREE`) and breaks PostgreSQL GUI visualizers. LionSQL standardizes exclusively on modern indented tree text and JSON plan representations, keeping MySQL syntax support strictly at Phase 1 ergonomics (`DESC`, `SHOW TABLES`, backticks).
## Completed Milestones Archive (완료된 작업 이력)

---

### [DONE] Metabase (BI 대시보드) `INTEG-METABASE-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/METABASE_INTEGRATION_PLAN.ko.md`](notes/plans/METABASE_INTEGRATION_PLAN.ko.md)

**Details & Resolution**:
- 연동 계획서: [`notes/METABASE_INTEGRATION_PLAN.ko.md`](notes/METABASE_INTEGRATION_PLAN.ko.md)
- 연동 보고서: [`notes/METABASE_INTEGRATION_REPORT.ko.md`](notes/METABASE_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`guide/METABASE_INTEGRATION_GUIDE.ko.md`](guide/METABASE_INTEGRATION_GUIDE.ko.md) / [`guide/METABASE_INTEGRATION_GUIDE.md`](guide/METABASE_INTEGRATION_GUIDE.md)
- 회귀 테스트: `pkg/engine/metabase_compat_test.go`
- 스택: Clojure/JVM + JDBC (`pgjdbc`, `mssql-jdbc`) / 듀얼 프로토콜 (PGWire + TDS)
- 데이터 소스 등록(PGWire `:5433` & TDS `:1433`) 100% 무오류 통과, 스키마 인트로스펙션(테이블/컬럼/외래키/인덱스 350ms 내 완료), 비주얼 쿼리 빌더(MBQL 다중 테이블 파생 조인, COUNT/SUM 집계, `date_trunc` 시계열) 및 Native SQL 완벽 동작 검증 (Metabase 순정 컨테이너 소스 수정 0줄)

---

### [PENDING] NocoDB (노코드 DB 스튜디오) — **[v1.1.0 로드맵]** `INTEG-NOCODB-1`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
- 스택: Node.js + Knex / 듀얼 프로토콜 (PGWire + TDS)
- 메타데이터 자동 감지 및 스프레드시트 뷰 CRUD 검증

---

### [DONE] NocoDB 및 Mattermost 연동 1.0 이후(v1.1.0) 연기 확정 `INTEG-NOCODB-MATTERMOST-DEFER-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
- 결정: 1.0 LTS 코어 안정성 및 내구성 검증에 집중하기 위해 NocoDB(`INTEG-NOCODB-1`) 및 Mattermost(`INTEG-MATTERMOST-1`) 연동을 `v1.1.0` (Priority 3.10, 3.11)으로 공식 이관
- 결과: 1.0 정식 지원 10대 실전 앱(Gitea, Casdoor, Ghost, Affine, Answer, Vaultwarden, Metabase, Umami, Woodpecker, Vikunja) 연동 100% 완료 및 1.0 범위 확정 완료

---

### [DONE] Umami (웹 애널리틱스) `INTEG-UMAMI-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
- 상세 보고서: [`notes/UMAMI_INTEGRATION_REPORT.ko.md`](notes/UMAMI_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`guide/UMAMI_INTEGRATION_GUIDE.ko.md`](guide/UMAMI_INTEGRATION_GUIDE.ko.md) / [`guide/UMAMI_INTEGRATION_GUIDE.md`](guide/UMAMI_INTEGRATION_GUIDE.md)
- 단위 및 회귀 테스트: `pkg/engine/umami_compat_test.go` (`TestUmami_PrismaCanConnectQuery`, `TestUmami_PrismaTableNamespaceQuery`, `TestUmami_CountDerivedTableQuery`)
- 스택: Next.js 16.4.2 + Prisma ORM / PGWire (`TCP :5433`)
- Prisma 초기화 26개 마이그레이션(01~26) 100% 무오류 적용, 관리자 인증(`POST /api/auth/login`), 웹사이트 등록(`POST /api/websites`), 웹사이트 목록 조회(`GET /api/websites` 파생 테이블 집계), 웹 분석 이벤트 수집(`POST /api/send`), 통계 분석 쿼리(`GET /api/websites/{id}/stats` 복합 서브쿼리, GROUP BY 1, 2, HAVING, CASE WHEN) 완벽 검증 통과 (Umami 순정 컨테이너 소스 수정 0줄)

---

### [DONE] Vaultwarden (비밀번호 관리) `INTEG-VAULTWARDEN-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/VAULTWARDEN_INTEGRATION_PLAN.ko.md`](notes/plans/VAULTWARDEN_INTEGRATION_PLAN.ko.md)

**Details & Resolution**:
- 연동 계획서: [`notes/VAULTWARDEN_INTEGRATION_PLAN.ko.md`](notes/VAULTWARDEN_INTEGRATION_PLAN.ko.md)
- 연동 보고서: [`notes/VAULTWARDEN_INTEGRATION_REPORT.ko.md`](notes/VAULTWARDEN_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`guide/VAULTWARDEN_INTEGRATION_GUIDE.ko.md`](guide/VAULTWARDEN_INTEGRATION_GUIDE.ko.md) / [`guide/VAULTWARDEN_INTEGRATION_GUIDE.md`](guide/VAULTWARDEN_INTEGRATION_GUIDE.md)
- 회귀 테스트: `pkg/engine/vaultwarden_compat_test.go`
- 스택: Rust + Diesel ORM / PGWire (`TCP :5433`)
- 47개 Diesel ORM 마이그레이션 100% 자동 완주, 29개 테이블 생성, 복합 기본 키 `ON CONFLICT DO UPDATE`, 8바이트 Timestamp OID 1114 인코딩, KDF/JWT/비밀번호 사이퍼 CRUD/동기화 E2E 무오류 통과 (Vaultwarden 소스 수정 0줄)

---

### [DONE] Vikunja (프로젝트 칸반 & To-Do) `INTEG-VIKUNJA-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md`](notes/plans/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md)

**Details & Resolution**:
- 설계 계획서: [`notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md`](notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md)
- 통합 보고서: [`notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md`](notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`guide/VIKUNJA_INTEGRATION_GUIDE.ko.md`](guide/VIKUNJA_INTEGRATION_GUIDE.ko.md) / [`guide/VIKUNJA_INTEGRATION_GUIDE.md`](guide/VIKUNJA_INTEGRATION_GUIDE.md)
- 회귀 테스트: `pkg/engine/vikunja_compat_test.go`
- 스택: Go + XORM / PGWire (`TCP :5433`)
- 80개 이상의 XORM 마이그레이션 100% 무오류 자동 적용, 사용자 회원가입(`POST /api/v1/register`), JWT 로그인(`POST /api/v1/login`, 복합 `OR` 술어 처리), 프로젝트 및 4대 뷰(List/Gantt/Table/Kanban) 생성(`PUT /api/v1/projects`), 칸반 태스크 생성 및 상태 변경(`PUT /api/v1/projects/:id/tasks`, `POST /api/v1/tasks/:id`), 재귀 CTE(`WITH RECURSIVE`) 프로젝트 계층 탐색, 웹 UI 서빙 완벽 동작 검증 (Vikunja 순정 컨테이너 소스 수정 0줄)

---

### [DONE] Woodpecker CI (CI/CD 엔진) `INTEG-WOODPECKER-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md`](notes/plans/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md)

**Details & Resolution**:
- 설계 계획서: [`notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md`](notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md)
- 통합 보고서: [`notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md`](notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_REPORT.ko.md)
- 운영 가이드: [`guide/WOODPECKER_INTEGRATION_GUIDE.ko.md`](guide/WOODPECKER_INTEGRATION_GUIDE.ko.md) / [`guide/WOODPECKER_INTEGRATION_GUIDE.md`](guide/WOODPECKER_INTEGRATION_GUIDE.md)
- 회귀 테스트: `pkg/engine/woodpecker_compat_test.go`
- 스택: Go + XORM / PGWire (`TCP :5433`)
- 19개 테이블, 32개 XORM 마이그레이션 100% 무오류 자동 적용, 서버 헬스체크(`/healthz` 204), 버전 조회(`/version` v3.20.1), 웹 UI 서빙 완벽 동작 검증 (Woodpecker CI 순정 컨테이너 소스 수정 0줄)

---

### [PENDING] MSSQL Collation 및 유니코드 다국어 로케일 정렬/비교 `LOC-1`
- **Domain**: Engine | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/COLLATION_AND_LOCALE_PLAN.md`](notes/plans/COLLATION_AND_LOCALE_PLAN.md)

---

### [DONE] v0.90.0 Feature Freeze & Production Hardening (100% PASS) `M1`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
- **Gate 1 (Multi-Wire)**: TDS (SSMS, DBeaver, Beekeeper, sqlcmd) & PGWire v3 (psql) live query parity 100% PASS
- **Gate 2 (ACID & Concurrency)**: In-Memory OCC 1,046k tpmC / WiredTiger 833k tpmC, 0 data races under `-race`
- **Gate 3 (HA & CDC)**: 1-Writer Multi-Reader WAL replication streaming, real-time CDC, live failover 100% PASS
- **Gate 4 (Security & Governance)**: SQL RBAC Default Deny, TLS dynamic certificates, heap memory governor
- **Gate 5 (Unified Binary)**: Single executable (`lionsql`), Web Studio suite, Windows standalone (`lionsql.exe`, 1ms timer)

---

### [DONE] v0.90.0 액티브 스프린트 완료 과제 (18개 전량 완료 아카이브) `M2`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56
- **Metrics**: - [x] **`PERF-PGWIRE-PREP-TX`**: PGWire PreparedStatement 트랜잭션 내 VDBE 바이트코드 직접 바인딩 (`ExecuteInTxWithSessionContext`, `substituteParams` 제거, PGWire TPC-C 24.3만 tpmC 달성) | - [x] **`BENCH-TPCC-RPC` & `PERF-TPCC-CLIENT`**: TPC-C 단일 왕복 RPC 벤치마크 및 클라이언트 최적화 (TDS 236k / PGWire 243k tpmC)

**Phases & Checklist**:
- [x] **`PERF-PGWIRE-PREP-TX`**: PGWire PreparedStatement 트랜잭션 내 VDBE 바이트코드 직접 바인딩 (`ExecuteInTxWithSessionContext`, `substituteParams` 제거, PGWire TPC-C 24.3만 tpmC 달성)
- [x] **`CASDOOR-INTEGRATION-1`**: Casdoor IAM/SSO 듀얼 프로토콜 연동 (44 XORM 테이블, 복합 PK `\x1f`, `0x` Hex 바이너리)
- [x] **`GRAFANA-INTEGRATION-1`**: Grafana 듀얼 프로토콜 연동 (내부 메타데이터 PGWire 785개 마이그레이션 완주 + PG/TDS 듀얼 쿼리 데이터소스)
- [x] **`IOTA-ERP-1`**: iota-sdk Go ERP PGWire 연동 (47개 마이그레이션, binary UUID, TIMESTAMPTZ)
- [x] **`GITEA-PGWIRE-1` & `GITEA-MOCK-SEED-1`**: Gitea 프로덕션 연동 (BIGSERIAL, RETURNING, pg_indexes, 모의 데이터 시더)
- [x] **`BENCH-TPCC-RPC` & `PERF-TPCC-CLIENT`**: TPC-C 단일 왕복 RPC 벤치마크 및 클라이언트 최적화 (TDS 236k / PGWire 243k tpmC)
- [x] **`PERF-SERVER-PIPELINE-P1-P2`**: TDS 파이프라인 최적화 (Syscall Coalescing, 32KB sync.Pool, Zero-Alloc RPC)
- [x] **`PERF-WIRE-SOCKET-POOL` & `PERF-FASTPATH-PING`**: PGWire 32KB 버퍼 풀링 및 `SELECT 1` 무파싱 핑 패스트패스
- [x] **`ORM-MIGRATE-1~3`**: 조건부 DDL 지원, XORM 컬럼 인트로스펙션, 할당 에일리어싱(`alias = expr`), `sys.fn_helpcollations()`
- [x] **`S-MYSQL-1-P1` & `FUNC-DIALECT-7`**: MySQL 편의 문법(`DESC`, `SHOW TABLES`, 백틱) 및 고ROI 빌트인 함수 Best 7
- [x] **`PROC-CURSOR-1`**: T-SQL Fast Forward-Only 읽기 전용 커서 및 `@@FETCH_STATUS`
- [x] **`AUDIT-EXPR-1` & `PERF-COMPLEX-QUERY-1`**: 복합 쿼리 중첩 표현식 감사 및 AST 제로할당 평가기 튜닝
- [x] **`BENCH-SSB-1`**: Star Schema Benchmark (SSB) 스위트 CLI 도구 완성

---

### [DONE] 내구성 및 신뢰성 1차 검증 완료 이력 `M3`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: `cmd/crashtest`

**Phases & Checklist**:
- [x] **`DURA-SOAK-F1`**: WiredTiger `WT_ROLLBACK`(-31800) 쓰기 충돌을 `storage.ErrSerializationFailure`로 래핑하여 PGWire `SQLSTATE 40001` 매핑
- [x] **`DURA-SOAK-F2`**: 1시간 소크 실측을 통한 500MB 페이지 캐시 워밍업 분석 및 `/metrics` Go 힙/고루틴 수집과 캐시 상한 기준선 확립
- [x] **`DURA-KILL-1 (Phase 1)`**: `cmd/crashtest` 하네스 완성, 3대 결함 수정 및 20라운드/엔진 (총 12.8만 건 tx) 강제 종료 복구 100% 무결성 입증

---

### [IN_PROGRESS] In-Memory OCC Pointer-Free Flat Record A/B Benchmark & Implementation `OCC-HYPER-2`
- **Domain**: STORAGE | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md`](notes/plans/IN_MEMORY_OCC_OPTIMIZATION_PLAN.ko.md)

**Phases & Checklist**:
- [ ] Phase 1: Flat Record 고정폭/SSO/NULL비트맵 인코더/디코더 구현 및 포괄적 단위 테스트
- [ ] Phase 2: `memengine` 샤드 레코드 모드 분기 (`OCCModeRaw` 디폴트 유지, `OCCModeFlat` 플래그 선택)
- [ ] Phase 3: A/B 벤치마크 (Raw vs Flat) 메모리 풋프린트, GC Pause, TPS 계측 비교 보고서 작성

---

### [DONE] 대규모 조인(10+ Tables) 최적화 `OPT-LARGE-JOIN-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/LARGE_JOIN_OPTIMIZATION_PLAN.ko.md`](notes/plans/LARGE_JOIN_OPTIMIZATION_PLAN.ko.md)
- **Target Files**: `pkg/planner/planner.go, pkg/execution`

**Phases & Checklist**:
- [x] **제1방안 (베이스 테이블 동적 루트 선정)**: 단일 행 필터 보유 또는 최소 카디널리티 테이블을 시작 스캔 노드로 승격하여 조인 체인 중간 튜플 증폭 차단 (`pkg/planner/planner.go`)
- [x] **제2방안 (공집합 조기 탈락 Fast-Fail)**: `1=0` 등 정적 모순 조건 감지 시 하위 10+개 테이블 스캔/해시 빌드를 바이패스하고 `EmptyIterator` 0ms 반환 (`pkg/planner`, `pkg/execution`)
- [x] **제3방안 (Selinger DP 탐색 임계치 최적화)**: `MaxJoinDPThreshold = 6` 적용 (12 -> 6개로 축소, 4,096개 상태 공간 폭발 차단 및 7개 이상 조인은 Greedy 즉시 전환)

**Details & Resolution**:
- 설계 계획서: [`notes/LARGE_JOIN_OPTIMIZATION_PLAN.ko.md`](notes/LARGE_JOIN_OPTIMIZATION_PLAN.ko.md)

---

### [DONE] WiredTiger VDBE 바이너리 튜플 지연 역직렬화 `P-2`
- **Domain**: Performance | **Priority**: P1 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: `pkg/proc/runtime/vm_lazy.go`
- **Metrics**: - [x] **Phase 1 (`BENCH-P2-BASE`)**: 다중 컬럼 테이블 대상 조건부 스캔 사전 기준치 벤치마크 측정 (`2.77ms/op`, `6.3MB/op`, `30,983 allocs`) | - [x] **Phase 3 (`VERIFY-P2-COMPARE`)**: 구현 전후 마이크로 벤치마크 실측 (`420µs/op`, 6.59배 가속, 메모리 8배 절감) 및 `go test -race` 100% PASS 검증 완료

**Phases & Checklist**:
- [x] **Phase 1 (`BENCH-P2-BASE`)**: 다중 컬럼 테이블 대상 조건부 스캔 사전 기준치 벤치마크 측정 (`2.77ms/op`, `6.3MB/op`, `30,983 allocs`)
- [x] **Phase 2 (`IMPL-P2-LAZY`)**: `pkg/proc/runtime/vm_lazy.go` 및 `vm.go` 커서 스캔/UPDATE/DELETE/SELECT-INTO 경로 `RawTupleCursor` 기반 지연 필터링 및 조기 스킵 연동
- [x] **Phase 3 (`VERIFY-P2-COMPARE`)**: 구현 전후 마이크로 벤치마크 실측 (`420µs/op`, 6.59배 가속, 메모리 8배 절감) 및 `go test -race` 100% PASS 검증 완료

---

### [PENDING] Post-1.0 P2 Deferred Audit Items (92 Cases) `P2-BACKLOG`
- **Domain**: Engine | **Priority**: P3 | **Updated**: 2026-11-07 16:50:45
- **Plan Doc**: [`notes/POST_1.0_CANDIDATES.ko.md`](notes/POST_1.0_CANDIDATES.ko.md)

---

### [DONE] By-Design & Spec-Out Architecture Invariants (25 Cases) `P3-INVARIANTS`
- **Domain**: Architecture | **Priority**: P3 | **Updated**: 2026-11-07 16:50:52
- **Plan Doc**: [`notes/POST_1.0_CANDIDATES.ko.md`](notes/POST_1.0_CANDIDATES.ko.md)

---

### [DONE] WiredTiger Banking SP cmd/bench 5k->120k TPS 회귀 원인 분석 및 성능 복원 `PERF-BENCH-WT-RESTORE-1`
- **Domain**: Performance | **Priority**: P1 | **Updated**: 2026-11-07 14:02:56

---

### [PENDING] Complex Query Flat Hash Join & Direct Slot Projection `PERF-COMPLEX-QUERY-2`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/plans/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md)

**Details & Resolution**:
- 오픈 어드레싱 정수 해시 테이블 기반 복합 쿼리 서브 50ms 목표 최적화 ([`notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md`](notes/COMPLEX_QUERY_BENCHMARK_PLAN.ko.md))

---

### [DONE] 순수 Columnar Vectorization 전면 재작성 영구 기각 (Spec-Out) `PERF-COMPLEX-QUERY-COLUMNAR`
- **Domain**: Architecture | **Priority**: P3 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md)

---

### [DONE] 세션 로컬 에페머럴 CTE 버퍼 및 Zero-DDL 최적화 `PERF-CTE-EPHEMERAL-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/SESSION_EPHEMERAL_CTE_BUFFER_PLAN.ko.md`](notes/plans/SESSION_EPHEMERAL_CTE_BUFFER_PLAN.ko.md)

**Phases & Checklist**:
- [x] **에페머럴 인메모리 커서 표준화 (`storage.SliceCursor`)**: 순수 Go 슬라이스 기반 non-blocking 커서 제공

**Details & Resolution**:
- 설계 계획서: [`notes/SESSION_EPHEMERAL_CTE_BUFFER_PLAN.ko.md`](notes/SESSION_EPHEMERAL_CTE_BUFFER_PLAN.ko.md)

---

### [DONE] Writable CTE Stored Procedure 바이트코드 Lowering 가속 `PERF-CTE-LOWERING-VDBE-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md`](notes/plans/WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md)
- **Target Files**: `pkg/engine/cte_lowering.go, pkg/proc/, pkg/proc/runtime/, pkg/engine/sqlexec_explain.go, pkg/engine/writable_cte_lowering_test.go`

**Phases & Checklist**:
- [x] **Phase 1 (`CTE-LOWER-DETECT-1`)**: 선형 Writable CTE 쓰기 체인 패턴 판별기(`detectLinearWritableCTE`) 구현 (`pkg/engine/cte_lowering.go`)
- [x] **Phase 2 (`CTE-LOWER-COMPILE-1`)**: Writable CTE 절을 VDBE 바이트코드 프로그램(`proc.Program`)으로 Lowering하는 컴파일러(`compileLinearWritableCTE`) 구현 및 T-SQL `OUTPUT ... INTO` / PostgreSQL `RETURNING ... INTO` 전역 바이트코드 엔진 확장 (`pkg/proc/`, `pkg/proc/runtime/`, `pkg/engine/cte_lowering.go`)
- [x] **Phase 3 (`CTE-LOWER-COORD-1`)**: 코디네이터 `execCTE`에서 VDBE Fast-Path 즉시 실행 및 비선형/분석용/Array ANY CTE Volcano Fallback 파이프라인 연동 (`pkg/engine/sqlexec_explain.go`)
- [x] **Phase 4 (`CTE-LOWER-VERIFY-1`)**: 3-Way Writable CTE(주문 + 품목 + 아웃박스) 및 50 워커 동시성 정합성/원자적 롤백/레이스 프리 검증 통과 (`pkg/engine/writable_cte_lowering_test.go`)

**Details & Resolution**:
- 설계 계획서: [`notes/WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md`](notes/WRITABLE_CTE_BYTECODE_LOWERING_PLAN.ko.md)

---

### [DONE] Grove 벤치마크 기반 동시성 최적화 및 핫스팟 락 완성 `PERF-GROVE-CONCURRENCY-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/GROVE_BENCHMARK_CONCURRENCY_OPTIMIZATION_PLAN.ko.md`](notes/plans/GROVE_BENCHMARK_CONCURRENCY_OPTIMIZATION_PLAN.ko.md)
- **Target Files**: `pkg/wire/numeric.go, pkg/storage/key_latch.go, pkg/engine/sp_insert_select_test.go, pkg/wire/metadata_cache_test.go, pkg/proc/runtime/vm.go, pkg/engine/sqlexec.go, pkg/engine/write_conflict_retry_test.go, pkg/engine/sqlexec_ddl.go, pkg/engine/truncate_test.go`
- **Metrics**: - [x] **Phase 2 (`PERF-HOTSPOT-LATCH-1`)**: 인-메모리 2,048-스트라이프 키 래치(Key-Striped Latch) 도입으로 핫스팟 UPDATE 경합 시 OCC Abort Storm 방지 및 50 동시 워커 16,000+ TPS 달성 (`pkg/storage/key_latch.go`, `hotspot_concurrency_test.go`)

**Phases & Checklist**:
- [x] **Phase 1 (`BUG-WIRE-OID-1700`)**: PGWire OID 1700 (NUMERIC/DECIMAL) 바이너리 Base-10000 인코더/디코더 구현 및 드라이버(`pgx`, `lib/pq`) 스캔 에러 완치 (`pkg/wire/numeric.go`, `numeric_test.go`)
- [x] **Phase 2 (`PERF-HOTSPOT-LATCH-1`)**: 인-메모리 2,048-스트라이프 키 래치(Key-Striped Latch) 도입으로 핫스팟 UPDATE 경합 시 OCC Abort Storm 방지 및 50 동시 워커 16,000+ TPS 달성 (`pkg/storage/key_latch.go`, `hotspot_concurrency_test.go`)
- [x] **Phase 3 (`PROC-SP-INSERT-SELECT-1`)**: Stored Procedure VDBE VM 내 `INSERT INTO ... SELECT` 바이트코드 브릿지(`execInsertSelect`) 지원 및 다중 테이블 복사/트랜잭션 정합성 검증 (`pkg/engine/sp_insert_select_test.go`)
- [x] **Phase 4 (`PERF-PGWIRE-PIPELINE-1`)**: PGWire Describe 메타데이터(`ColumnOIDs`, `ColumnNames`, `NoData`) 캐싱으로 불필요한 Writable CTE 파싱/사전 실행 제거 및 파이프라인 처리량 대폭 가속 (`pkg/wire/metadata_cache_test.go`)
- [x] **Phase 5 (`PERF-RETRY-WT-ROLLBACK-1`)**: WiredTiger `WT_ROLLBACK`(-31800) 및 OCC 충돌 시 MongoDB 표준 아키텍처 기반 인-커널 투명 재시도 안전망(WriteConflict Retry Loop, 지수 백오프 및 랜덤 마이크로 지터) 장착 완료 (`pkg/proc/runtime/vm.go`, `pkg/engine/sqlexec.go`, `pkg/engine/write_conflict_retry_test.go`)
- [x] **Phase 6 (`DDL-TRUNCATE-MULTI-CASCADE-1` & `PROC-SP-TRUNCATE-1`)**: 다중 테이블 `TRUNCATE TABLE t1, t2, ...`, `CASCADE`, `RESTRICT`, `RESTART IDENTITY` 지원 및 외래키 상호 참조 해소, Stored Procedure VM 내 네이티브 `OpTruncate` 바이트코드 지원 완료 (`pkg/storage`, `pkg/engine/sqlexec_ddl.go`, `pkg/proc`, `pkg/engine/truncate_test.go`)
- [ ] 1단계: 개인용 서비스(Vaultwarden / Umami / Answer)를 LionSQL에 연결해 섀도 모드 운영
- [ ] 2단계: 프로젝트 자체 서비스(Gitea 등)로 확대 및 일 단위 백업/복원 리허설

**Details & Resolution**:
- 설계 계획서: [`notes/GROVE_BENCHMARK_CONCURRENCY_OPTIMIZATION_PLAN.ko.md`](notes/GROVE_BENCHMARK_CONCURRENCY_OPTIMIZATION_PLAN.ko.md)

---

### [PENDING] WiredTiger 모드 임시 테이블 및 테이블 변수 인메모리 하이브리드 바이패스 `PERF-HYBRID-TEMPMEM-1`
- **Domain**: STORAGE | **Priority**: P2 | **Updated**: 2026-11-10 00:53:23
- **Plan Doc**: [`notes/plans/LOGICAL_NAMESPACE_DATABASE_PREFIX_PLAN.ko.md`](notes/plans/LOGICAL_NAMESPACE_DATABASE_PREFIX_PLAN.ko.md)

---

### [DONE] 성능 회귀 정상화 및 회귀 방지 게이트 구축 `PERF-REGRESSION-RECOVERY-1`
- **Domain**: Performance | **Priority**: P1 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: `pkg/proc/runtime/perf_regression_gate_test.go`
- **Metrics**: - [x] **Phase 3 (`VERIFY-BENCH-RESTORE-1`)**: 3.3.4 송금 (In-Memory 130k~160k / WiredTiger 92k~108k) 및 3.5 TPC-C (53.5만~55만 tpmC) 복원 실측 | - [x] **Phase 4 (`TEST-PERF-GATE-1`)**: `pkg/proc/runtime/perf_regression_gate_test.go` 신설하여 `go test -race` 파이프라인 내 1,000건 ACID 송금 성능 게이트 영구 구축 (0.12s 완주, 25k+ TPS 검증)

**Phases & Checklist**:
- [x] **Phase 1 (`CLEAN-KEY-LATCH-1`)**: `KeyLatchManager` 및 `AcquireKeyLatch` 전량 삭제 (순수 Lock-Free OCC / MVCC B+Tree 복원)
- [x] **Phase 2 (`CLEAN-RUN-RETRY-1`)**: `RunContext` 내 `time.After` 슬립 재시도 루프 제거 및 즉시 반환 복원
- [x] **Phase 3 (`VERIFY-BENCH-RESTORE-1`)**: 3.3.4 송금 (In-Memory 130k~160k / WiredTiger 92k~108k) 및 3.5 TPC-C (53.5만~55만 tpmC) 복원 실측
- [x] **Phase 4 (`TEST-PERF-GATE-1`)**: `pkg/proc/runtime/perf_regression_gate_test.go` 신설하여 `go test -race` 파이프라인 내 1,000건 ACID 송금 성능 게이트 영구 구축 (0.12s 완주, 25k+ TPS 검증)

---

### [DONE] 서버 파이프라인 Phase 3/4 `PERF-SERVER-PIPELINE-P3-P4`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`](notes/plans/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md)

**Details & Resolution**:
- Generic LRU 커서 및 ReadOnly Auto-Bypass, 적응형 그룹 커밋 ([`notes/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md`](notes/SERVER_PIPELINE_PERFORMANCE_OPTIMIZATION_PLAN.ko.md))

---

### [DONE] T-SQL VDBE 바이트코드 VM 극강의 성능 쥐어짜기 5대 혁신 `PERF-VDBE-EXTREME-OPT-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md`](notes/plans/VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md)
- **Metrics**: - [x] **Phase 1 (`PERF-VDBE-SLOT-1`)**: 로컬 변수 제로-맵 슬롯 인덱싱 완전화 (`setVar` 6회 Map/문자열 할당 ➔ 0.2ns 배열 인덱스 직접 대입 완료, evalCtx lazy on-demand 동기화, `OpStoreVar`/`OpLoadVar`/`bindParams` 0-할당화 달성, TPC-C 54,858 tpmC 달성) | - [x] **Phase 2 (`PERF-VDBE-DML-POOL-1`)**: DML 제로-맵 레코드 풀링(`rowScratch`), AST 산술 수식 직접 계산(`evalUpdateExprFast`), `canDirectInsert` 직결(`InsertRow`), 다중 키 교차 래치 무대기 데드락 방지(`timeout=0` fail-fast) 적용 (TransferFunds 165,187 TPS, TPC-C 636,434 tpmC / 23,576 TPS 달성) | - [x] **Phase 3 (`PERF-VDBE-FLATTEN-1`)**: VM `Execute` 2단계 중첩 switch 호출 ➔ 단일 평탄화 인라인 루프 전환 (`execFlowOp`/`execComputeOp`/`execTxOp`/`execStorageOp`/`execCursorAndCallOp`/`execRelOp` 6개 중간 헬퍼 호출 제거, pc 하드웨어 레지스터 고정, 163,499 TPS 유지) | - [ ] **Phase 4 (`PERF-VDBE-VERIFY-1`)**: 12,000 ~ 25,000+ TPS 달성 벤치마크 및 회귀 검증

**Phases & Checklist**:
- [x] **Phase 1 (`PERF-VDBE-SLOT-1`)**: 로컬 변수 제로-맵 슬롯 인덱싱 완전화 (`setVar` 6회 Map/문자열 할당 ➔ 0.2ns 배열 인덱스 직접 대입 완료, evalCtx lazy on-demand 동기화, `OpStoreVar`/`OpLoadVar`/`bindParams` 0-할당화 달성, TPC-C 54,858 tpmC 달성)
- [x] **Phase 2 (`PERF-VDBE-DML-POOL-1`)**: DML 제로-맵 레코드 풀링(`rowScratch`), AST 산술 수식 직접 계산(`evalUpdateExprFast`), `canDirectInsert` 직결(`InsertRow`), 다중 키 교차 래치 무대기 데드락 방지(`timeout=0` fail-fast) 적용 (TransferFunds 165,187 TPS, TPC-C 636,434 tpmC / 23,576 TPS 달성)
- [x] **Phase 3 (`PERF-VDBE-FLATTEN-1`)**: VM `Execute` 2단계 중첩 switch 호출 ➔ 단일 평탄화 인라인 루프 전환 (`execFlowOp`/`execComputeOp`/`execTxOp`/`execStorageOp`/`execCursorAndCallOp`/`execRelOp` 6개 중간 헬퍼 호출 제거, pc 하드웨어 레지스터 고정, 163,499 TPS 유지)
- [ ] **Phase 4 (`PERF-VDBE-VERIFY-1`)**: 12,000 ~ 25,000+ TPS 달성 벤치마크 및 회귀 검증

**Details & Resolution**:
- 설계 계획서: [`notes/VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md`](notes/VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md)

---

### [PENDING] 배포 패키지 오프라인 문서 번들링 (`docs/index.html`) `PKG-OFFLINE-DOCS`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/OFFLINE_DOCS_PACKAGING_PLAN.ko.md`](notes/plans/OFFLINE_DOCS_PACKAGING_PLAN.ko.md)

**Details & Resolution**:
- **개념**: *"사람들은 터미널이 아니라 브라우저로 문서를 본다."*
- **기능**: DB 엔진 바이너리 순수성을 유지하면서 공식 배포 아카이브(`tar.gz`/`zip`) 내에 소스코드를 제외한 순수 문서(`docs/`)와 오프라인 브라우저 뷰어(`docs/index.html`) 번들링.
- **특징**: 폐쇄망/에어갭 환경 100% 자립, 오프라인 문서 검색, SQL 예제 즉시 실행(`[Try in Studio]`) 연동.
- **설계 참조**: [`notes/OFFLINE_DOCS_PACKAGING_PLAN.ko.md`](notes/OFFLINE_DOCS_PACKAGING_PLAN.ko.md)

---

### [DONE] & `PLANNER-AST-1~3` 플래너·엔진 정규식 완전 제거 및 순수 AST 전환 `PLANNER-SLIM-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md`](notes/plans/PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md)
- **Target Files**: `pkg/planner/planner.go, pkg/proc, pkg/config, cmd/lionsql`

**Phases & Checklist**:
- [x] **1.1 `PLANNER-SLIM-1` (Sort-Merge Join 비용 지연 평가)**: 힌트 또는 양쪽 정렬 조건 부재 시 O(N log N) Sort-Merge 비용 수식 계산을 바이패스하여 핫패스 레이턴시 절감 (`pkg/planner/planner.go`)
- [x] **1.2 `PLANNER-AST-1` (순수 AST 기본화 및 정규식 옵션 격리)**: Pratt AST 파서에 `OVER (...)` 윈도우 절 및 `WITH RECURSIVE` CTE 컬럼 리스트 네이티브 파싱을 완비하여 순수 AST 플래너를 디폴트(`false`)로 전환하고, 레거시 정규식 폴백은 `--enable-legacy-regex-planner` 옵션 뒤로 격리 (`pkg/proc`, `pkg/config`, `cmd/lionsql`, `pkg/planner`)
- [x] **1.3 `PLANNER-AST-2` (플래너 하위 조건식·통계·서브쿼리 정규식 완전 제거)**: `bounds.go`, `stats.go`, `pruning.go`, `planner.go`, `decorrelate.go`, `explain.go` 내 조건식 정규식(22개)을 Pratt AST `proc.Expr` 및 `proc.Lexer` 기반으로 100% 교체
- [x] **1.4 `ENGINE-AST-3` (엔진 관리·부가 명령어 정규식 완전 제거)**: `sqlexec.go`, `sqlexec_auth.go`, `sqlexec_pivot.go`, `sqlexec_apply.go`, `sqlexec_string_split.go`, `sqlexec_procs.go` 내 구문 정규식(40개)을 `proc.Lexer` 및 `sqlutil` 토큰 파서 기반으로 100% 교체

**Details & Resolution**:
- 설계 계획서: [`notes/PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md`](notes/PLANNER_SLIMMING_AND_PURE_AST_TRANSITION_PLAN.ko.md)

---

### [DONE] 세션 캡슐화 및 모듈 디커플링 (Lean 3단계 리팩토링) `R-6`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/MODULAR_ARCHITECTURE_PLAN.ko.md`](notes/plans/MODULAR_ARCHITECTURE_PLAN.ko.md)

**Phases & Checklist**:
- [x] **Phase 1 (`R-6-P1`)**: `ClientSession` 내부 락 및 상태 은닉 (와이어의 `cs.Mu.Lock()` 직접 침범 제거, `ExecuteProgram` 세션 일원화)
- [x] **Phase 2 (`TDS-MOD-1`)**: TDS 모놀리스(`server.go` 2.9k 라인) 파일 분할 영구 기각(Spec-Out) — AI 페어 프로그래밍 환경에서 Git Blame/히스토리 보존 가치가 파일 분할 이점보다 크므로 단일 파일 유지 결정
- [x] **Phase 3 (`R-6-P3`)**: 서버 기동 시 `Coordinator` 최상위 인스턴스화 및 3대 리스너(PG, TDS, HTTP) 대등 주입 (역할 전도 해소)

**Details & Resolution**:
- 상세 계획: [`notes/MODULAR_ARCHITECTURE_PLAN.ko.md`](notes/MODULAR_ARCHITECTURE_PLAN.ko.md)

---

### [DONE] Table-Driven Virtual Catalog Interceptor Refactoring `REFACTOR-CATALOG-INTERCEPTOR-1`
- **Domain**: REFACTOR | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/TOLERANT_DDL_MODE_PLAN.ko.md`](notes/plans/TOLERANT_DDL_MODE_PLAN.ko.md)

---

### [DONE] Flat 코덱 신뢰성 및 동시성 결함 보강 `REFACTOR-OCC-FLAT-1`
- **Domain**: Storage | **Priority**: P2 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md)

---

### [PENDING] Replication 네트워크 Length-Prefixed Binary Frame Zero-Copy 스트리밍 `REPL-STREAM-BIN-1`
- **Domain**: Replication | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

---

### [PENDING] [Post-1.0 초안] In-Process Graceful Reboot 기반 물리 서브폴더 USE DB 전환 아키텍처 연구 `RESEARCH-INPROCESS-REBOOT-USE`
- **Domain**: Research | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/IN_PROCESS_REBOOT_MULTI_DATABASE_PLAN.ko.md`](notes/plans/IN_PROCESS_REBOOT_MULTI_DATABASE_PLAN.ko.md)

---

### [DONE] 심볼 테이블 & AST 기반 쿼리 라우팅 리팩토링 `ROUTING-AST-CATALOG-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](notes/plans/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md)

**Details & Resolution**:
- 계획서: [`notes/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md`](notes/AST_CATALOG_ROUTING_REFACTORING_PLAN.ko.md)
- 회귀 테스트: `pkg/wire/routing_ast_test.go`, `pkg/proc/characteristics_test.go`
- 45개의 `strings.Contains`/`regexp` 문자열 휴리스틱 완전 삭제 완료
- C 언어 typedef 심볼 테이블 방식의 AST 특성 추출기(`proc.ExtractQueryCharacteristics`) 및 카탈로그 심볼 조회(`virtual.IsVirtualTable`) 도입
- `canCompileToVDBE`, `isVirtualCatalogQuery`, `virtual.IsVirtualCatalogJoinQuery`, `resolveColumnOIDs` 구조적 정상화
- False Positive (유사 테이블명 오인) 및 False Negative (리터럴/주석 키워드로 인한 VDBE 차단) 100% 원천 해결

---

### [PENDING] 빈 괄호 IN () 문법 평가 정책 (미결 검토) `S-SYNTAX-EMPTY-PARENS`
- **Domain**: Engine | **Priority**: P3 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md`](notes/plans/EMPTY_IN_PREDICATE_EVALUATION_PLAN.ko.md)

---

### [PENDING] 클라이언트 사이드 S3 Pre-signed URL 및 SigV4 서명기 `S3-1`
- **Domain**: Cloud | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/S3_PRESIGNED_STORAGE_PLAN.md`](notes/plans/S3_PRESIGNED_STORAGE_PLAN.md)

---

### [DONE] 초기 패스워드 미변경 시 부팅 출력 최후행 대문자 보안 경고 불변식 `SEC-BOOT-DEFAULT-PASS-WARN`
- **Domain**: SEC | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/BOOT_DEFAULT_PASSWORD_WARNING_PLAN.ko.md`](notes/plans/BOOT_DEFAULT_PASSWORD_WARNING_PLAN.ko.md)

---

### [DONE] PGWire 달러 파라미터() 상태머신 토크나이저 전환 및 보안 강화 `SEC-PGWIRE-PARAM-1`
- **Domain**: Wire | **Priority**: P2 | **Updated**: 2026-11-09 09:07:03
- **Plan Doc**: [`notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md`](notes/plans/REGULAR_EXPRESSION_REFACTORING_PLAN.ko.md)

---

### [DONE] PostgreSQL `GENERATED { ALWAYS | BY DEFAULT } AS IDENTITY` 지원 및 goose 마이그레이션 호환 `SQL-GOOSE-IDENTITY-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
- 회귀 테스트: `pkg/engine/identity_ddl_test.go`
- goose 마이그레이션 이력 테이블(`goose_db_version`)의 `id integer PRIMARY KEY GENERATED BY DEFAULT AS IDENTITY` DDL 파싱 지원
- `reColDefault`가 `BY DEFAULT AS` 뒤의 `AS`를 컬럼 기본값으로 오인식하던 결함 완치
- `isIdentity = true` Auto-Increment 승격 및 `Default = ""` 정규화로 다중 마이그레이션 버전 삽입 시 `duplicate key "AS"` (SQLSTATE 23505) 완치

---

### [DONE] PostgreSQL Writable CTE (`WITH ... INSERT/UPDATE/DELETE ... RETURNING`) 및 `::type[]` 캐스트 지원 `SQL-WRITABLE-CTE-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

**Details & Resolution**:
- 회귀 테스트: `pkg/engine/writable_cte_test.go`, `pkg/wire/writable_cte_pgwire_test.go`
- 가이드 문서: [`doc/SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md`](doc/SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.ko.md), [`doc/SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.md`](doc/SQL_COMPATIBILITY_AND_ERGONOMICS_GUIDE.md)
- Grove 실전 서비스(`CreateWithOutbox`, `CreateJournal`, `ReserveWithTxReturning`) 5대 체크리스트 완벽 지원:
1. 다중 Writable CTE 나열 및 메인 `SELECT` 암묵적 크로스 조인(`FROM ins_order o, ins_item i, ins_outbox e`) 위치 기반 컬럼 매핑
2. 선행 `UPDATE ... RETURNING`의 갱신 후(Post-Update) 값을 후속 `INSERT ... SELECT`에서 참조
3. 조건 불충족(0행 매칭) 시 빈 CTE 스키마 유지(`pgx.ErrNoRows`) 및 미참조 쓰기 CTE(`ins_tx`) 무조건 실행(Materialize) 보장
4. 단일 문 원자적 트랜잭션 보장(Autocommit 암시적 트랜잭션 및 Savepoint 래핑, `UNIQUE` 위반 시 전체 롤백 및 SQLSTATE `23505` 반환)
5. 바인딩 파라미터(`$1 ~ $N`) 다중 CTE 재사용, `INSERT ... SELECT $4` 타깃 컬럼 기반 파라미터 OID 역추론, `::type` 및 `::type[]`(`ANY($1::text[])`) AST `CastExpr` 파싱 지원

---

### [DONE] SSMS Object Explorer 호환성 (sys.resource_governor_configuration, HostPlatform 배치 및 가상 카탈로그 수식 평가) `SSMS-OE-COMPAT-1`
- **Domain**: Ecosystem | **Priority**: P2 | **Updated**: 2026-11-09 05:34:10

---

### [IN_PROGRESS] SSMS Object Explorer 호환성 고도화 (Phase 1~4 테이블 목록 표시 완료, Phase 5 SMO NullReferenceException 및 하위 노드 바인딩) `SSMS-OE-COMPAT-2`
- **Domain**: Ecosystem | **Priority**: P2 | **Updated**: 2026-11-09 07:44:19
- **Plan Doc**: [`notes/plans/SSMS_OBJECT_EXPLORER_COMPATIBILITY_PLAN.ko.md`](notes/plans/SSMS_OBJECT_EXPLORER_COMPATIBILITY_PLAN.ko.md)

---

### [DONE] TDS 대규모 파일 모듈 분할 기각 (Spec-Out) `TDS-MOD-1`
- **Domain**: Architecture | **Priority**: P3 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md)

---

### [PENDING] Flat 모드 크래시 복구 무결성 검증 (AOF/RDB 및 kill -9) `TEST-OCC-FLAT-1`
- **Domain**: Storage | **Priority**: P2 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md)

---

### [PENDING] Flat 모드 복합 DDL/DML 회귀 검증 (복합 보조 인덱스, SkipList, FK) `TEST-OCC-FLAT-2`
- **Domain**: Storage | **Priority**: P2 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md)

---

### [PENDING] Flat 모드 쿼리 엔진 풀 스위트 회귀 검증 (SLT 및 Babelfish) `TEST-OCC-FLAT-3`
- **Domain**: Storage | **Priority**: P2 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md)

---

### [PENDING] Flat 모드 24h 장기 소크 및 가변 길이 UPDATE 단편화 검증 `TEST-OCC-FLAT-4`
- **Domain**: Storage | **Priority**: P2 | **Updated**: 2026-11-09 09:06:51
- **Plan Doc**: [`notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md`](notes/plans/FLAT_RECORD_RELIABILITY_PLAN.ko.md)

---

### [DONE] 1.0 GA 벤치마크 및 공식 문서 실측치 일괄 동기화 `TUNE-DOC-SYNC-1`
- **Domain**: Documentation | **Priority**: P2 | **Updated**: 2026-11-09 00:47:19

---

### [DONE] 고동시성 핫스팟 OCC 경합 완화 및 와이어 버퍼 풀링 `TUNE-HOTSPOT-OCC-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Target Files**: `pkg/wire/protocol.go, pkg/wire/command_tags.go, pkg/wire/tds/token.go, pkg/wire/tds/server.go`
- **Metrics**: - [x] **Phase 3 (`TUNE-EPHEMERAL-VERIFY-1`)**: `-ephemeral` 순수 인메모리 모드에서 32W PGWire 10초 지속 **204,472 tpmC (7,616 TPS)** 및 TDS 10초 지속 **148,968 tpmC (5,546 TPS, 피크 197k)** 달성 검증

**Phases & Checklist**:
- [x] **Phase 1 (`TUNE-WIRE-BUF-1`)**: PGWire `WriteCommandComplete` 정적 태그 슬라이스 및 스택 버퍼(`[64]byte`) 적용으로 힙 할당 0화 (`pkg/wire/protocol.go`, `pkg/wire/command_tags.go`)
- [x] **Phase 2 (`TUNE-TDS-UTF16-1`)**: TDS `BuildDone`/`BuildDoneProc`/`BuildReturnStatus` 리플렉션 100% 제거 및 `WriteDoneDirect`/`WriteReturnStatusDirect`/`WriteReturnValueIntDirect` 스택 인코딩 직결 (`pkg/wire/tds/token.go`, `pkg/wire/tds/server.go`)
- [x] **Phase 3 (`TUNE-EPHEMERAL-VERIFY-1`)**: `-ephemeral` 순수 인메모리 모드에서 32W PGWire 10초 지속 **204,472 tpmC (7,616 TPS)** 및 TDS 10초 지속 **148,968 tpmC (5,546 TPS, 피크 197k)** 달성 검증

**Details & Resolution**:
- 목표: 100 워커 등 고동시성 환경에서 단일 District/Warehouse 행 경합 시 발생하는 OCC 충돌 재시도(1.5만 회)를 완화하고, PGWire/TDS 응답 버퍼 할당을 풀링하여 지속 처리량 20만+ tpmC 도달.

---

### [DONE] TPC-C Stored Procedure Fast-Path 직결 및 고동시성 In-Memory OCC 튜닝 `TUNE-SP-FASTPATH-1`
- **Domain**: Durability | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/TPCC_STORED_PROCEDURE_FASTPATH_TUNING_PLAN.ko.md`](notes/plans/TPCC_STORED_PROCEDURE_FASTPATH_TUNING_PLAN.ko.md)
- **Target Files**: `pkg/engine/proc_bind.go, pkg/engine/proc_bind_test.go, pkg/wire/server.go, pkg/wire/prep_sp_test.go, pkg/wire/tds/server.go, pkg/wire/tds/server_test.go`
- **Metrics**: - [x] **Phase 4 (`TUNE-TPCC-BENCH-1`)**: 32W 및 100W 조건 TPC-C 실측 및 지속 처리량 검증 (16W/32Workers 순간 피크 **236,399 tpmC / 8,746 TPS**, 10초 지속 **178,355 tpmC / 6,612 TPS**, 10W/100Workers 지속 **119,854 tpmC / 4,443 TPS**)

**Phases & Checklist**:
- [x] **Phase 1 (`TUNE-SP-BIND-1`)**: SP 인자 매핑 헬퍼 및 메타데이터 정적 분석 구현 (`pkg/engine/proc_bind.go`, `pkg/engine/proc_bind_test.go`)
- [x] **Phase 2 (`TUNE-SP-PGWIRE-1`)**: PGWire `handleParse` & `handleExecute` SP Fast-Path 직결 (Zero-Allocation VDBE 바인딩, `pkg/wire/server.go`, `pkg/wire/prep_sp_test.go`)
- [x] **Phase 3 (`TUNE-SP-TDS-1`)**: TDS `handleRPCPrepare` & `handleRPCExecute` SP Fast-Path 직결 (`pkg/wire/tds/server.go`, `pkg/wire/tds/server_test.go`)
- [x] **Phase 4 (`TUNE-TPCC-BENCH-1`)**: 32W 및 100W 조건 TPC-C 실측 및 지속 처리량 검증 (16W/32Workers 순간 피크 **236,399 tpmC / 8,746 TPS**, 10초 지속 **178,355 tpmC / 6,612 TPS**, 10W/100Workers 지속 **119,854 tpmC / 4,443 TPS**)
- [ ] **Phase 5 (`TUNE-DOC-SYNC-1`)**: `doc/BENCHMARK.md` 일괄 갱신 보류 (사용자 방침: 1.0 GA 릴리스 직전에 전체 실측치 일괄 동기화)

**Details & Resolution**:
- 설계 계획서: [`notes/TPCC_STORED_PROCEDURE_FASTPATH_TUNING_PLAN.ko.md`](notes/TPCC_STORED_PROCEDURE_FASTPATH_TUNING_PLAN.ko.md)
- 목표: PGWire/TDS Prepared Statement에서 `EXEC <proc>` 구문의 정적 분석 및 파라미터 직결 바인딩을 구현하여 초당 29만 회의 문자열 직렬화/재파싱/메모리 할당을 제거하고 고동시성(100 워커) 처리량을 극대화합니다.

---

### [PENDING] Partial / Delta WAL 로깅 (UPDATE 컬럼 차분 비트마스킹) `WAL-DELTA-1`
- **Domain**: Engine | **Priority**: P2 | **Updated**: 2026-11-07 14:02:56

---

### [DONE] 단일 바이너리 내장 SPA 웹 서버 호스팅 `WEB-SPA-1`
- **Domain**: Ecosystem | **Priority**: P3 | **Updated**: 2026-11-07 14:02:56
- **Plan Doc**: [`notes/plans/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](notes/plans/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md)

**Details & Resolution**:
- `--app`, `--public` 플래그를 통한 SPA 정적 웹 호스팅 및 네이티브 DB 서빙 ([`notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`](notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md))

---

### [DONE] WiredTiger 고동시성 4계층 잠금 및 transaction_sync 영구 배제 `WT-LOCKDOWN-1`
- **Domain**: Storage | **Priority**: P1 | **Updated**: 2026-11-08 12:09:17
- **Plan Doc**: [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md)

---

