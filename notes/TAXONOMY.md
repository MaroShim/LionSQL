# LionSQL Task Identification Code Taxonomy & Catalog (`notes/TAXONOMY.md`)

이 문서는 LionSQL 프로젝트 전반에서 과제 관리, 로드맵 추적, 커밋 메시지 식별용으로 사용되는 **전체 식별 코드 체계(Taxonomy)와 도메인별 과제 일람표**입니다.

과거 완료된 마일스톤부터 현재 진행 중인 과제, 향후 착수할 백로그까지 고유 식별자(ID)를 통해 일관되게 추적할 수 있도록 정의합니다.

---

## 1. 식별 코드 체계 범례 (Taxonomy Architecture)

| 접두사 / 코드 | 트랙 명칭 (도메인) | 번호 체계 | 관리 목적 및 설명 |
| :---: | :--- | :---: | :--- |
| **`P1` ~ `P3`** | **우선순위 (Priority)** | `P1` ~ `P3` | 개발 착수 긴급도 및 릴리즈 로드맵 단계 구분 |
| **`P-1` ~ `P-5`** | **엔진 성능 (Performance)** | `P-1` ~ `P-5` | 스토리지 I/O, 와이어 프로토콜, 실행 엔진 5대 성능 극대화 트랙 |
| **`A` ~ `D`** | **CBO 최적화 (Optimizer)** | `A-1` ~ `D-9` | 비용 기반 옵티마이저 4대 기능 트랙 (통계 수집, 조인, 선택도, 운영 제어) |
| **`S-1` ~ `S-7`** | **SQL 문법 (Syntax)** | `S-1` ~ `S-7` | T-SQL 및 ANSI SQL 7대 문법 호환성 완비 트랙 (전 항목 완료) |
| **`BUG-PK-1`** | **무결성 결함 수정 (Integrity Bug)** | `BUG-PK-1` | `execUpdate`/`execDelete`/`execTruncate` 및 VDBE의 PK 동적 해석 및 맵 순회 왜곡 방지 |
| **`BUG-CASE-1`** | **컬럼 대소문자 정규화 (Case-Insensitive)** | `BUG-CASE-1` | `TableSchema.FindColumn`의 `strings.EqualFold` 대소문자 무관 탐색 보장 |
| **`BUG-RECORD-1`** | **사일런트 유실 방지 (Silent Loss Bug)** | `BUG-RECORD-1` | DML Record 컬럼명 정규화(`colSchema.Name`)로 `RecordToRow` 업데이트 유실 및 NULL 삽입 방지 |
| **`BUG-ARITH-1`** | **수식 0 초기화 방지 (Arithmetic Bug)** | `BUG-ARITH-1` | `evalUpdateExpr` 수식 평가 시 컬럼명 대소문자 무관 탐색으로 컬럼 0 리셋 방지 |
| **`BUG-UPSERT-1`** | **Upsert 충돌 정합성 (Upsert Bug)** | `BUG-UPSERT-1` | `ON CONFLICT (col)` 충돌 컬럼 대소문자 정규화로 Upsert 유니크 위반 방지 |
| **`BUG-WHERE-1`** | **WHERE IN 절 정합성 (WHERE IN Bug)** | `BUG-WHERE-1` | `evaluateSingleConditionMap` 내 `WHERE IN (...)` 절 대소문자 무관 탐색 누락 보강 |
| **`BUG-VM-INTO-1`** | **VDBE 변수대입 무작위 순회 방지 (IntoVar Bug)** | `BUG-VM-INTO-1` | VDBE `SELECT ... INTO @var` 무작위 맵 순회 제거 및 `cols[0]` 결정론적 바인딩 |
| **`BUG-SUBQ-1`** | **스칼라 서브쿼리 무작위 반환 방지 (Subquery Bug)** | `BUG-SUBQ-1` | 스칼라 서브쿼리 수식 평가 시 무작위 맵 순회 제거 및 `cols[0]` 대소문자 무관 탐색 |
| **`BUG-BOUNDS-1`** | **인덱스 바운드 무작위 추출 방지 (Index Bound Bug)** | `BUG-BOUNDS-1` | `ParseIndexCond` 무작위 맵 순회 제거 및 인덱스 대상 컬럼 바운드 정밀 매칭 |
| **`BUG-EXP-1`** | **EXPLAIN 서브쿼리 무작위 치환 방지 (Explain Bug)** | `BUG-EXP-1` | `EXPLAIN` 서브쿼리 평탄화 시 무작위 맵 순회 제거 및 서브쿼리 컬럼 키 정합 |
| **`VPERF-1` ~ `VPERF-14`** | **수직 핫패스 최적화 (Vertical Perf)** | `VPERF-1` ~ `VPERF-14` | TDS 와이어 프로토콜 ➔ 코디네이터 ➔ VDBE VM ➔ 스토리지 엔진(OCC/WiredTiger) 전 계층 수직 성능 최적화 트랙 |

---

## 2. 우선순위 레벨 체계 (Priority Levels: `P1` ~ `P3`)

| 우선순위 | 영역 | 대상 과제 식별자 | 대상 소스코드 위치 | 상태 및 주요 목표 |
| :---: | :---: | :--- | :--- | :---: |
| **P1** | **최적화** | **`D-9`**, **`B-3`** | `pkg/planner`, `pkg/execution` | **완료**: CBO 옵티마이저 힌트 오버라이드 및 소트-머지 조인 완비 |
| **P2** | **성능** | **`P-5`** | `pkg/engine`, `pkg/storage` | **완료**: 단순 PK 조회 고속 바이패스 캐시 완비 (TPS 50%+ 향상) |
| **P2** | **프로토콜** | **TDS NBCROW** | `pkg/wire/tds` | **완료**: TDS 행 압축(`0xD2`) 기반 네트워크 대역폭 절감 및 NULL 비트맵 지원 |
| **P3** | **생태계** | **드라이버 E2E** | `pkg/wire`, `pkg/wire/tds` | **진행 예정**: Python(`psycopg3`, `pymssql`), Node.js, .NET 드라이버 E2E 하네스 |

---

## 3. 스토리지 & 엔진 성능 5대 트랙 (`P-1` ~ `P-5`)

스토리지 엔진 트랜잭션, 직렬화/역직렬화, 네트워크 와이어 I/O, 메모리 캐시 전반의 병목을 제거하기 위한 핵심 고속화 과제입니다.

| 코드 | 전략 분야 | 핵심 구현 과제 | 상태 | 구현 상세 및 검증 지표 | 대상 패키지 |
| :---: | :--- | :--- | :---: | :--- | :--- |
| **P-1** | **스토리지/트랜잭션** | **배치형 그룹 커밋 & 무경합 Fast-Path** | **완료** | `memengine` WAL 및 `wiredtiger` 리더/팔로워 그룹 커밋 구현. `FsyncAlways` 마이크로벤치마크 100배 가속(0.038ms/op), TPC-C 2.8K TPS 검증. | `pkg/storage/memengine`, `pkg/storage/wiredtiger` |
| **P-2** | **스토리지/디코딩** | **바이너리 튜플 지연 역직렬화 (Lazy Decoding)** | **대기** | 풀 스캔 시 WHERE 절 컬럼만 선별 디코딩하고 조건 통과 행만 전체 언패킹하여 CPU 및 힙 할당 70% 삭감. `CREATE INDEX ... INCLUDE` 커버링 인덱스 결합. | `pkg/storage`, `pkg/execution`, `pkg/catalog` |
| **P-3** | **와이어 프로토콜** | **제로카피 와이어 인코딩 & I/O 버퍼 결합** | **대기** | 중간 `[]any` 레코드 슬라이스 박싱을 제거하고 `sync.Pool` 버퍼에 바이너리 튜플 데이터를 직접 포맷팅하여 소켓 syscall 횟수 최소화. | `pkg/wire`, `pkg/storage` |
| **P-4** | **실행 엔진** | **벡터화 청크 반복자 (Chunked Batch Next)** | **대기** | Volcano 반복자에서 1행 단위 함수 호출 대신 64~1024건 청크 단위 배치 실행을 적용하여 대량 집계 및 스캔 지연 60% 단축. | `pkg/execution` |
| **P-5** | **캐시 및 메모리** | **고속 SQL 지문 해싱 & 단순 PK 조회 바이패스** | **완료** | `SELECT * FROM tbl WHERE id = ?` 단순 PK 점조회에 대해 FNV-1a 지문 해싱 및 바운디드 캐시 기반으로 파서/플래너/VDBE 우회 스토리지(`tx.Get`) 직결 반환. DDL 시 자동 무효화. | `pkg/engine`, `pkg/storage` |

---

## 4. CBO 비용 기반 옵티마이저 4대 발전 트랙 (`A` ~ `D`, 총 9개 과제)

비용 기반 옵티마이저(Cost-Based Optimizer)의 통계 수집, 조인 플래닝, 선택도 평가, 운영 제어 고도화 과제입니다.

### Track A: 통계 수집 & 영속화 (Statistics Collection & Persistence)
| 코드 | 과제명 | 상태 | 구현 상세 및 기대 효과 | 대상 패키지 |
| :---: | :--- | :---: | :--- | :--- |
| **A-1** | **`ANALYZE TABLE` 멀티코어 병렬 통계 수집** | **완료** | 테이블 내 컬럼별 정렬 및 64-버킷 히스토그램 수집을 `sync.WaitGroup` 고루틴 풀로 병렬화(`BuildTableStatsParallel`). 대용량 테이블 분석 시간 단축. | `pkg/catalog`, `pkg/engine` |
| **A-2** | **CBO 통계 메타데이터 디스크 영속화** | **완료** | 원자적 임시 파일 스왑을 통해 `cbo_stats.json` 파일에 카디널리티, NDV, 히스토그램을 디스크 저장하고 엔진 기동 시 무손실 자동 복원. | `pkg/catalog` |

### Track B: 조인 알고리즘 & 순서 탐색 (Join Algorithms & Enumeration)
| 코드 | 과제명 | 상태 | 구현 상세 및 기대 효과 | 대상 패키지 |
| :---: | :--- | :---: | :--- | :--- |
| **B-3** | **소트-머지 조인 (Sort-Merge Join) 연산자 및 비용 모델** | **완료** | 양측 입력이 사전 정렬(인덱스 순서 또는 `ORDER BY`)된 경우 해시 테이블 빌드 오버헤드를 제거하는 $O(M+N)$ `MergeJoinIterator` 스트리밍 조인 구현. 중복 키 Mark & Restore 슬라이딩 윈도우 지원. | `pkg/execution`, `pkg/planner` |
| **B-4** | **$N > 4$ 체인 조인 동적 계획법 (DP / Selinger)** | **완료** | 5개 이상 다중 테이블 조인 시 팩토리얼($N!$) 전수 탐색 대신 Left-Deep DP 및 Bushy Tree 동적 계획법(DPccp) 탐색을 적용하여 최적 조인 트리 도출. | `pkg/planner` |

### Track C: 선택도 & 통계 정밀화 (Selectivity & Distribution Modeling)
| 코드 | 과제명 | 상태 | 구현 상세 및 기대 효과 | 대상 패키지 |
| :---: | :--- | :--- :--- | :--- |
| **C-5** | **다중 컬럼 확장 통계 (Extended Statistics)** | **완료** | 컬럼 간 상관관계(예: 국가-도시)로 인한 복합 조건 카디널리티 과소평가(Underestimation)를 방지하는 다중 컬럼 NDV 수집 및 결합 선택도 산출. | `pkg/catalog`, `pkg/planner` |
| **C-6** | **OR 조건 및 불리언 트리 재귀 선택도 계산** | **완료** | `TableStats.EstimateExprSelectivity()` 신설. 동일 컬럼 배타 합집합 $P(A)+P(B)$ 및 독립 합집합 $P(A)+P(B)-P(A)P(B)$, NOT, IN, BETWEEN 재귀 트리 평가 완비. | `pkg/planner` |
| **C-7** | **MCV (Most Common Values) 최빈값 목록 결합** | **완료** | 왜곡(Skew)이 심한 상위 N개 최빈값은 MCV 목록으로 별도 보관하고 균등 분포는 히스토그램으로 이원화 처리하여 선택도 오차 최소화. | `pkg/catalog`, `pkg/planner` |

### Track D: 운영 & 실행 제어 (Operations & Optimizer Control)
| 코드 | 과제명 | 상태 | 구현 상세 및 기대 효과 | 대상 패키지 |
| :---: | :--- | :--- :--- | :--- |
| **D-8** | **백그라운드 자동 통계 갱신 데몬 (Auto-Analyze)** | **완료** | 테이블 DML 변경 비율(예: 10% 이상 Row 변동 발생)을 추적하여 백그라운드 워커에서 비동기로 `ANALYZE` 자동 트리거. | `pkg/catalog`, `pkg/engine` |
| **D-9** | **CBO 옵티마이저 힌트 오버라이드 제어** | **완료** | 테이블 힌트 `WITH (INDEX(...))` 및 쿼리 힌트 `OPTION (HASH/MERGE/LOOP JOIN, FORCE ORDER)` 파싱 및 CBO 플래너 비용 계산 강제 오버라이드. | `pkg/planner`, `pkg/proc` |

---

## 5. T-SQL & ANSI SQL 문법 완비 트랙 (`S-1` ~ `S-7`, 전 항목 완료)

LionSQL 네이티브 파서 및 바이트코드 엔진에서 공식 지원하는 7대 핵심 SQL 문법 체계입니다.

| 코드 | 과제명 | 상태 | 구현 상세 및 지원 문법 | 대상 패키지 |
| :---: | :--- | :---: | :--- | :--- |
| **S-1** | **T-SQL & ANSI 표준 페이징 정규화** | **완료** | `OFFSET n ROWS FETCH NEXT m ROWS ONLY`, `OFFSET n ROWS`, `LIMIT` 및 `TOP (n)` AST 정규 통합 및 VDBE 바이트코드 컴파일 완비. | `pkg/proc`, `pkg/engine` |
| **S-2** | **복합 한정자 & 인라인 별칭 파싱** | **완료** | `[db].[schema].[tbl].[col]`, `"schema"."tbl"` 4-part 식별자 및 `AS` 없는 인라인 컬럼/테이블 별칭(`FROM accounts basetbl`) 파싱 정밀화. | `pkg/proc`, `pkg/engine` |
| **S-3** | **FROM 절 파생 테이블 & 인라인 서브쿼리** | **완료** | `FROM (SELECT ...) AS t`, `JOIN (SELECT ...) AS o` 서브쿼리 AST 노드화, 재귀 스키마 추론 및 임시 격리 구체화(`__dt_*`) 완비. | `pkg/proc`, `pkg/engine` |
| **S-4** | **스칼라 함수 & 조건부 수식 AST화** | **완료** | `COALESCE`, `ISNULL` 단락 평가(Short-circuit), `CAST`, `TRY_CAST`, `CONVERT`, `TRY_CONVERT`, `::` 타입 캐스팅 및 `CASE WHEN` 완비. | `pkg/proc`, `pkg/engine` |
| **S-5** | **고급 조인 문법 확장** | **완료** | `FULL [OUTER] JOIN`, `RIGHT [OUTER] JOIN`, `CROSS JOIN`, T-SQL 특화 `CROSS/OUTER APPLY` 볼케이노 해시조인 파이프라인 완비. | `pkg/proc`, `pkg/execution` |
| **S-6** | **세션 제어문(SET Statements) 정식 바인딩** | **완료** | `SetOptionStmt` AST, VDBE 컴파일, 다중 옵션 쉼표 구분 및 세션 플래그(`SET NOCOUNT ON`, `SET ANSI_NULLS ON` 등) 반영. | `pkg/proc`, `pkg/engine` |
| **S-7** | **EXISTS / NOT EXISTS 상관 서브쿼리** | **완료** | `WHERE [NOT] EXISTS (...)` 조건의 Semi-Join / Anti-Join CBO 변환 및 비상관 서브쿼리 조기 단락 평가 완비. | `pkg/planner`, `pkg/execution` |

---

## 6. 연계 문서 및 진실 공급원 (SSOT)

- **액티브 백로그 및 단기 우선순위**: [`TODO.ko.md`](../TODO.ko.md)
- **중장기 거시 릴리즈 로드맵 (Phase 1~6)**: [`ROADMAP.md`](../ROADMAP.md)
- **엔지니어링 불변식 및 개발 규약**: [`notes/ENGINEERING_CONTEXT.md`](ENGINEERING_CONTEXT.md)
- **실전 개발 함정 및 디커플링 패턴**: [`notes/PITFALLS_AND_TRICKS.md`](PITFALLS_AND_TRICKS.md)
- **아키텍처 설계 문서**: [`doc/ARCHITECTURE.md`](../doc/ARCHITECTURE.md)
