# LionSQL SQLLogicTest (SLT) 1.0 릴리즈 풀 스위트 검증 계획서 (`notes/SQLLOGICTEST_FULL_SUITE_CERTIFICATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **상태**: Planned (1.0 릴리즈 필수 게이트)
> - **목표**: SQLite 공식 SQLLogicTest 풀 스위트(60,000+ 쿼리) 및 무작위 복합 대수식·집계 매트릭스를 LionSQL 1.0 릴리즈 직전에 완주하여, SQL 3치 논리, 연산자 결합 우선순위, 화이트스페이스 파싱, VDBE/Volcano 2티어 실행 엔진의 수학적 무결성을 최종 인증(Certification)합니다.

---

## 1. 배경 및 전제

초기 Phase 1 및 Phase 2의 기본 7개 파일(52개 쿼리) 실행만으로도 다음의 깊은 커널 결함 4건이 즉각 포착되어 완치되었습니다:
1. `CREATE TABLE` 다중 공백 시 DDL 라우터 미인식
2. `val = NULL`이 NULL 행을 일치시키는 SQL 3치 논리(Three-Valued Logic) 위반
3. `(A OR B) AND C` AST deparsing 시 연산자 우선순위 부모 괄호 탈락
4. SLT 공백 포함 문자열 셀 파싱 불일치

이러한 결과는 인간 개발자가 작성하는 단위 테스트로는 결코 도달할 수 없는 조합적 엣지 케이스를 SQLLogicTest가 수학적으로 탐색함을 증명합니다. 1.0 프로덕션 릴리즈 이전, 엔진의 신뢰성을 최종 입증하기 위해 SQLite 공식 SQLLogicTest 수만 개 쿼리 전량을 통과시키는 **풀 스위트 인증(Full Suite Certification)**을 1.0 마일스톤에 공식 편입합니다.

---

## 2. 풀 스위트 구성 및 단계별 확장 매트릭스

SQLite 공식 SQLLogicTest 저장소(`sqlite/sqllogictest`) 및 DuckDB/CockroachDB 표준 스위트를 기반으로 4단계 계층으로 구성합니다:

```
test/slt/
├── core/                  # [Phase 1] 기본 구문 (5 files, 완료)
├── whitespace/            # [Phase 2] 화이트스페이스 변형 (1 file, 완료)
├── boundary/              # [Phase 2] 식별자/리터럴 키워드 충돌 (1 file, 완료)
├── full/                  # [Phase 3] SQLite 공식 select1 ~ select5 (~60,000 queries)
│   ├── select1.test
│   ├── select2.test
│   ├── select3.test
│   ├── select4.test
│   └── select5.test
├── random/                # [Phase 4] 무작위 대수식 및 집계 스트레스 (~50,000 queries)
│   ├── expr/              # 중첩 괄호, 단항/이항 연산자 조합
│   ├── groupby/           # 복합 GROUP BY, HAVING, DISTINCT
│   └── aggregates/        # 다중 집계 함수 조합
└── wire/                  # [Phase 5] PGWire TCP 소켓 원격 프로토콜 전수 검증
```

---

## 3. 핵심 아키텍처 및 도구 요구사항

### 3.1. 대용량 테스트 세트 자동 동기화 (`scripts/fetch_slt_full.sh`)
- SQLite 공식 화석(Fossil)/GitHub 미러에서 `select1.test` ~ `select5.test` 및 `random/` 스위트를 로컬 캐시(`test/slt/full/`)로 다운로드.
- Git 저장소 비대를 방지하기 위해 대용량 테스트 원본 파일(`*.test`, 수십 MB)은 `.gitignore`에 등록하고 스크립트 기반 온디맨드 fetch 지원.

### 3.2. 대규모 병렬 분산 실행기 (Parallel Worker Pool)
- 60,000개 이상의 쿼리를 순차 단일 스레드로 실행할 경우 수 분 이상 소요될 수 있으므로, `cmd/sqllogictest`에 CPU 코어 수 기반 고루틴 병렬 워커 풀(`-parallel=<N>`, 기본값 `runtime.NumCPU()`) 구현.
- 테스트 파일 또는 레코드 청크 단위로 독립 세션(`NewSession`)을 할당하여 In-Memory OCC 및 WiredTiger MVCC에서 초당 5,000+ QPS로 10~15초 이내 완주.

### 3.3. 방언 차이점 관리 및 필터링 정책 (Dialect Neutrality)
- **SQLite 전용 특화 구문 스킵**:
  - SQLite 고유 pragma (`PRAGMA ...`), SQLite 전용 함수(`typeof()`) 등은 LionSQL 명세 밖이므로 스킵 필터 규칙(`--skip-dialect=sqlite`) 정의.
- **표준 ANSI/MSSQL/PostgreSQL 의미론 준수**:
  - 표준 대수식, 정수/실수/문자열 캐스팅, NULL 연산, 정렬 규칙은 100% 오라클 일치 검증.

---

## 4. 단계별 실행 계획 (Phase 3 ~ Phase 6)

### Phase 3: SQLite 공식 `select1.test` ~ `select5.test` 완주 — **[완료 (Pass)]**
- [x] `scripts/fetch_slt.sh` 동기화 도구 구현 (`select1.test` ~ `select5.test` 로컬 미러링 지원)
- [x] `cmd/sqllogictest` 병렬 워커 풀(`-parallel`) 및 메모리 사용량 상한(GC 압력 완화) 구현
- [x] `select1.test` ~ `select5.test` (총 10,706 쿼리) In-Memory OCC 엔진 전수 100% 통과 (소요 시간 22.69s)
  - `select1.test`: 1,031 쿼리 (2.29s, 100% PASS)
  - `select2.test`: 1,031 쿼리 (2.18s, 100% PASS)
  - `select3.test`: 3,351 쿼리 (5.88s, 100% PASS)
  - `select4.test`: 3,857 쿼리 (6.36s, 100% PASS)
  - `select5.test`: 1,436 쿼리 (5.67s, 100% PASS, 최대 64개 테이블 콤마 조인)
- [x] BFS Spanning Tree 조인 키 추론(`pushdown.go`) 및 Greedy Join Ordering 필터 우선순위화(`planner.go`)로 최대 64개 테이블 조인 카테시안 폭발 완치
- [x] `test/slt/full_test.go` 상시 회귀 테스트 스위트 구축 완료

### Phase 4: 복합 무작위 대수식 및 집계 매트릭스 (`random/`)
- [ ] `random/expr/` 무작위 중첩 식(수십 단계 괄호 및 단항/이항 연산자 조합) 통과 검증
- [ ] `random/groupby/` 다중 컬럼 집계 및 `HAVING` 복합 필터 오라클 검증
- [ ] VDBE 가상머신 스택 오버플로 방지 및 Volcano CBO 연계 최적화

### Phase 5: PGWire TCP 소켓 원격 프로토콜 전수 검증
- [ ] In-Process 메모리 직접 호출을 넘어, 실제 PostgreSQL Wire 프로토콜(`TCP :5433`) 리스너를 통한 SLT 스위트 E2E 실행 (`-mode=pgwire -dsn=postgres://...`)
- [ ] Extended Query 프로토콜 (Parse ➔ Bind ➔ Execute) 및 Binary/Text 데이터 포맷 정합성 입증

### Phase 6: 1.0 릴리즈 게이트 통합
- [ ] 단위 테스트(`go test`): 7개 핵심 스위트 기본 포함 (< 1초)
- [ ] 릴리즈 게이트/소크 테스트: 60,000+ 풀 스위트 완주 확인 후 1.0 프로덕션 태깅

---

## 5. 완료 기준 (Exit Criteria)

1. **테스트 통과율**: SQLite 공식 `select1~5` 및 `random` 스위트 중 유효 쿼리 **100% PASS (Zero Discrepancy)**.
2. **이중 엔진 일치성**: In-Memory OCC 및 WiredTiger MVCC 양쪽 엔진에서 동일한 오라클 결과 산출.
3. **성능 예산**: 병렬 8 워커 기준 60,000개 쿼리 전량 실행 완료 시간 **30초 이내**.
4. **품질 검증**: 3단계 파이프라인(Build ➔ Test `-race` ➔ Commit) 100% PASS 및 데이터 레이스 0건.
