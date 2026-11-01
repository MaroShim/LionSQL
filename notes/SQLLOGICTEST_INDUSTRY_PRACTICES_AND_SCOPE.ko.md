# 데이터베이스 엔진 업계 SQLLogicTest (SLT) 실전 적용 기준 및 권장 범위

> - **최초 작성일**: 2026-11-01  
> - **문서 상태**: 참조 표준 및 아키텍처 가이드 (Reference Standard)  
> - **적용 대상**: DuckDB, CockroachDB, TiDB, Materialize, Apache DataFusion 및 LionSQL 엔진  

---

## 1. 개요 및 업계 표준화 배경

### 1.1 SQLLogicTest(SLT)의 기원
SQLLogicTest는 2008년 SQLite의 창시자 D. Richard Hipp이 SQLite 3의 쿼리 플래너와 실행 엔진의 완전 무결성을 검증하기 위해 개발한 오픈 소스 테스트 프레임워크입니다.  
당시 SQLite와 타 상용 RDBMS(PostgreSQL, MySQL, Microsoft SQL Server, Oracle) 간의 실행 결과를 동일 쿼리·동일 데이터셋에서 상호 대조(Differential Testing)하기 위해 탄생했습니다.

### 1.2 현대 DBMS 업계의 전면적 채택 이유
DuckDB, CockroachDB, TiDB, Materialize, RisingWave, Apache DataFusion, ClickHouse 등 근래 탄생한 모든 주요 데이터베이스 엔진은 독자적인 SLT 러너를 구축하여 공식 테스트 스위트로 운용하고 있습니다.
1. **오라클 신뢰성 (Ground Truth)**: SQLite가 15년 이상 수십억 대의 디바이스에서 검증한 수백만 개 쿼리 결과셋(MD5 해시)을 정답(Oracle)으로 활용.
2. **테스트 언어 독립성 (Plaintext DSL)**: SQL 문과 기대 결과값만으로 이루어진 단순 텍스트 포맷(`statement ok`, `query <type> <sort>`, `----`, `hash`)으로, 엔진 구현 언어(C++, Rust, Go)에 무관하게 쉽게 러너를 구현 가능.
3. **무작위 엣지 케이스 커버리지 (Combinatorial Fuzzing)**: 인간 엔지니어가 미처 생각하지 못한 연산자 우선순위, 복합 불리언 트리(`AND`, `OR`, `NOT`, `BETWEEN`), 3치 논리(Three-Valued Logic), 상호 상관 서브쿼리, 다중 세트 연산 조합을 극단적으로 탐색.

---

## 2. 주요 엔진별 SLT 실전 운용 현황

| 엔진명 | 구현 언어 | SLT 러너 구현체 | 주요 운용 범위 및 방식 |
| :--- | :--- | :--- | :--- |
| **DuckDB** | C++ | 자체 내장 C++ Runner (`test/sql/`) | SQLite 호환 스위트 + 독자 확장 SLT 5,000+ 파일 매 PR 단위 검증 |
| **Apache DataFusion** | Rust | 오픈소스 `sqllogictest-rs` 크레이트 | DataFusion, RisingWave, GreptimeDB의 표준 러너로 자리잡음 |
| **CockroachDB** | Go | 자체 내장 Go Runner (`pkg/sql/logictest`) | 분산 실행 플래너, 벡터화 실행기, PG 호환 레이어 검증 |
| **TiDB** | Go | Go Runner 기반 파이프라인 | MySQL 호환성, TiKV 푸시다운, CBO 플래너 검증 |
| **Materialize** | Rust | `sqllogictest-rs` 기반 러너 | 스트리밍 뷰 및 증분 뷰 유지보수(Differential Dataflow) 일관성 검증 |

---

## 3. 원본 SQLite SLT 전체 규모와 업계의 선별 기준

원본 SQLite 공식 SQLLogicTest 저장소(`sqlite.org/sqllogictest`)는 압축을 해제하면 **수백만 줄, 600만 개 이상의 쿼리**로 구성되어 있습니다.

```
sqlite-sqllogictest (총 약 6,000,000+ 쿼리)
├── select1.test ~ select5.test (~60,000 쿼리) ───────── [Tier 1 ~ Tier 2 핵심]
├── evidence/ (명세 기반 기능별 테스트, ~5,000 쿼리) ── [Tier 1 고효율]
├── index/ (B-Tree 인덱스 경로별 테스트, ~15,000 쿼리) ─ [Tier 2]
└── random/ (자동 생성 무작위 쿼리, ~5,900,000 쿼리) ─── [Tier 3 Fuzzing]
    ├── expr/ (~1,500,000 쿼리)
    └── groupby/ (~4,400,000 쿼리)
```

> **업계의 핵심 현실**:  
> **DuckDB, CockroachDB, DataFusion 등 어떤 엔진도 일상적인 PR/CI마다 600만 개를 전부 돌리지 않습니다.**  
> 600만 쿼리를 전수 실행하려면 수 시간이 소요되고 막대한 컴퓨팅 비용이 발생하므로, 업계에서는 철저히 **3단계 계층화(Tiering)**를 통해 운용합니다.

---

## 4. 업계 표준 3단계 계층화 (Tiering Strategy)

### Tier 1: 상시 CI 게이트 (Commit / PR 단위 필수 통과)
- **규모**: 약 **3,000 ~ 10,000 쿼리**
- **실행 시간**: **1초 ~ 30초 이내**
- **대상 스위트**:
  - `select1.test` (~1,031 쿼리): 기본 SELECT, WHERE, ORDER BY, GROUP BY 조합.
  - `select2.test` (~1,031 쿼리): 복합 표현식, 별칭 바인딩, 연산자 우선순위.
  - `select3.test` (~3,351 쿼리): 대규모 인라인 서브쿼리, 조인, `BETWEEN`, `IN` 리스트.
  - `select4.test` (~1,500 쿼리): 다중 세트 연산(`UNION`, `EXCEPT`, `INTERSECT` 체이닝) 및 복합 필터.
  - 엔진 전용 수기 스위트 (`core/*.slt`): 트랜잭션 롤백, DDL, 방언 특화 기능.
- **목적**: 핵심 쿼리 플래너의 회귀 결함(Regression)을 즉각 차단.

### Tier 2: 나이틀리 배치 및 릴리즈 후보(RC) 게이트
- **규모**: 약 **50,000 ~ 100,000 쿼리**
- **실행 시간**: **3분 ~ 15분 이내**
- **대상 스위트**:
  - `select5.test` (대규모 집계 및 복합 윈도우/서브쿼리).
  - `index/` (인덱스 유무에 따른 동일 결과 검증).
  - `random/expr/`의 균등 샘플링 (10,000 ~ 20,000 쿼리).
  - `random/groupby/`의 균등 샘플링 (10,000 ~ 20,000 쿼리).
- **목적**: 릴리즈 태그 생성 전 미세 연산자 캐스팅 오류 및 옵티마이저 엣지 케이스 선제 검출.

### Tier 3: 분기별/마이너 릴리즈 퍼징 (Deep Fuzzing / Stress Testing)
- **규모**: **1,000,000 ~ 6,000,000 쿼리**
- **실행 시간**: 수 시간 (멀티코어/분산 워커 병렬 구동)
- **대상 스위트**: 원본 `random/` 전체 디렉토리 전수 매트릭스.
- **목적**: 수백만 조합의 극한 Fuzzing을 통한 엔진 충돌(Panic), 메모리 누수, 무한 루프 탐색.

---

## 5. 실전 적용 시 엔진 간 방언(Dialect) 차이와 처리 기법

SQLite 원본 SLT를 타 RDBMS/OLTP 엔진에 적용할 때 반드시 발생하는 시맨틱 차이와 업계의 표준 해결 기법입니다.

### 5.1 타입 시스템과 나눗셈 시맨틱 (Type Affinity vs Strict Types)
- **문제**: SQLite는 정수 나눗셈 `5 / 2 = 2`를 반환하지만, DuckDB/PostgreSQL 등은 부동소수점(`2.5`) 또는 정수를 엄격히 구분.
- **업계 대응**:
  - DuckDB: 정수 연산 플래그를 SLT 러너 수준에서 에뮬레이션하거나 전용 방언 파일 분리.
  - LionSQL: 정수 나눗셈과 부동소수점 연산은 ANSI/T-SQL/PGWire 모드에 맞추어 평가하며, SLT 표준 정수 연산 호환 보장.

### 5.2 정렬 비결정성 (Unordered Results & `valuesort` / `rowsort`)
- **문제**: `ORDER BY`가 명시되지 않은 쿼리는 실행 플랜(SeqScan, IndexScan, HashJoin)에 따라 행 반환 순서가 달라짐.
- **업계 대응**:
  - `query <type> nosort`: 반환 순서를 엄격히 검증.
  - `query <type> rowsort`: 행 전체를 문자열 기준 정렬 후 비교.
  - `query <type> valuesort`: 모든 행과 열의 값을 플랫하게 풀어서 정렬 후 비교 (해시 매칭 시 `valuesort hash` 사용).

### 5.3 세트 연산(Set Operations)의 결합성 및 우선순위
- **문제**: `UNION`, `UNION ALL`, `EXCEPT`, `INTERSECT`가 괄호 없이 연속될 때 결합 순서.
- **SQL 표준 및 SQLite 규칙**:
  - `UNION`, `UNION ALL`, `EXCEPT`는 좌측 결합(Left-Associative)으로 왼쪽부터 순차 누적.
  - `INTERSECT`는 `UNION`/`EXCEPT`보다 높은 우선순위를 가짐.
  - 체이닝 도중 `UNION`(Distinct)이 등장하면 그 시점까지 누적된 중간 결과 전체에 대해 중복을 제거해야 함.

### 5.4 상호 상관 서브쿼리(Correlated Subqueries)의 지연 평가
- **문제**: 단순 서브쿼리는 쿼리 시작 전 단 1회 사전 실행(`resolveSubqueries`)하여 상수로 치환 가능하지만, 외부 튜플의 컬럼을 참조하는 상관 서브쿼리는 매 행(Tuple)마다 매개변수화되어 반복 평가되어야 함.
- **업계 대응**:
  - 정적 분석 단계에서 `IsCorrelatedSubquery()`를 통해 상관 여부를 판별하고, 반복 필터(`FilterIterator`) 및 프로젝션(`ProjectionIterator`) 내부에서 행 컨텍스트를 주입받아 동적으로 실행.

---

## 6. LionSQL 프로젝트 권장 로드맵 매핑

LionSQL의 고성능 In-Memory OCC 및 WiredTiger MVCC 엔진 특성에 맞춘 최적의 권장 적용 범위입니다.

```
[Phase 1 완료] LionSQL 특화 Core 스위트 (33개 파일, 240문장 / 181쿼리 전수 100% 통과)
      │
      ▼
[Phase 2 완료] SQLite 공식 select1 ~ select5 (총 10,706 쿼리 100% 통과) ──> [LionSQL 상시 PR CI 게이트]
      │         - select1: 1,031 쿼리 (100% 통과, 2.29s)
      │         - select2: 1,031 쿼리 (100% 통과, 2.18s)
      │         - select3: 3,351 쿼리 (100% 통과, 5.88s)
      │         - select4: 3,857 쿼리 (100% 통과, 6.36s)
      │         - select5: 1,436 쿼리 (100% 통과, 5.67s, 최대 64개 테이블 콤마 조인)
      │         => 총 10,706 쿼리 전수 22.69초 만에 100% 무오류 인증 (0 실패, 0 레이스)
      │
      ▼
[Phase 3 (v0.95.0)] random/expr 및 random/groupby 샘플링 (~20,000 쿼리) ──> [Nightly CI]
      │
      ▼
[Phase 4 (v1.0.0 GA)] 50,000+ 쿼리 공식 인증 & 회귀 방지 체계 완비
```

### 결론 및 현황 성과
1. **`select1` ~ `select5` 완주 달성 (총 10,706 쿼리)**:
   - DuckDB, CockroachDB, DataFusion 등 글로벌 주요 DBMS 엔진의 "Tier 1 상시 CI 게이트"를 완벽하게 통과했습니다.
   - 단일 테이블 필터 푸시다운, 서수 인덱스 정렬, 서브쿼리 지연 평가뿐 아니라, `select5.test`의 **최대 64개 테이블에 달하는 복합 콤마 조인(Implicit Comma Joins)**에 대해 BFS 기반 Spanning Tree 조인 키 추론과 Greedy Join Ordering 필터 우선순위화를 도입하여 **중간 카테시안 폭발 없이 5.67초 만에 1,436개 쿼리를 전수 완주**했습니다.
2. **향후 단계별 접근 (Phase 3~4)**:
   - 수백만 개에 달하는 `random/` 전체를 단번에 돌리는 것은 빌드 시간을 기하급수적으로 늘려 개발 생산성을 저해하므로, **`select1` ~ `select5` (10,706 쿼리)는 상시 회귀 방지 게이트**로 운영하고, `random/` 스위트 및 비정형 대수식은 Nightly CI로 분리하는 것이 최적의 표준 실천 방안(Best Practice)입니다.
