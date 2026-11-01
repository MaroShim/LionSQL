# LionSQL SQLLogicTest (SLT) 단계별 확장 로드맵 (`notes/SQLLOGICTEST_EXPANSION_ROADMAP_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01  
> - **상태**: 계획 수립 (Planning)  
> - **목표**: SQLLogicTest를 체계적으로 확장하여 엔진 잠재 결함을 선제적으로 발굴하고 0.90.0 GA 및 1.0 릴리즈의 완전 무결성을 검증  

---

## 1. 개요 및 확장 기본 철학

현재까지 LionSQL은 28개 파일(202문장, 163쿼리) 규모의 SQLLogicTest 스위트를 구축하고, `memory`/`wiredtiger` × `inproc`/`pgwire` 4대 매트릭스 전수 검증을 완료하여 총 12건의 잠재 엔진 결함을 발굴·완치했습니다.

테스트 규모를 무작정 한 번에 수만 건으로 늘리기보다는, **엔진의 복잡도와 위험도가 높은 핵심 도메인을 먼저 집중 타격(Curated Edge Cases)**하고, 점진적으로 **공식 대용량 오라클 데이터셋(SQLite SLT)**으로 확장하는 4단계 점진적 확장이 가장 효율적입니다.

---

## 2. 4단계 확장 로드맵 (Phase 1 ~ Phase 4)

```
[Phase 1: 0.90.0 GA 집중] 고위험 SQL 핵심 도메인 심화 (5개 핵심 스위트 추가, ~60개 쿼리)
       │
       ▼
[Phase 2: 0.90.0 직후] SQLite 공식 select1 ~ select2 실전 스위트 연동 (~5,000개 쿼리)
       │
       ▼
[Phase 3: v0.95.0 마일스톤] SQLite random/expr 및 random/groupby 무작위 매트릭스 (~20,000개 쿼리)
       │
       ▼
[Phase 4: v1.0.0 GA 게이트] SQLite 풀 스위트 (select1~select5, 60,000+ 쿼리) CI 상시화
```

---

### Phase 1: 고위험 SQL 핵심 도메인 심화 (0.90.0 GA 릴리즈 전 즉시 추진)
실제 ORM과 BI 도구에서 자주 발생하지만 현재 커버리지가 부족한 복합 엣지 케이스를 타겟팅합니다.

1. **트랜잭션 격리 및 롤백 정합성 (`test/slt/core/tx_isolation.slt`)**
   - 다중 문장 트랜잭션 내 DML 후 `ROLLBACK` 시 데이터 원복 여부
   - 동일 세션 내 격리된 뷰와 커밋 후 가시성 검증
2. **복합 날짜/시간 함수 및 산술 연산 (`test/slt/core/datetime_stress.slt`)**
   - `DATEADD`, `DATEDIFF`, `DATEPART`, `EXTRACT(field FROM ...)`
   - `GETDATE()`, `CURRENT_TIMESTAMP`, 문자열 ➔ 날짜 캐스팅 및 비교
   - 윤년, 월말 경계값, 시간대 연산
3. **다중 파생 테이블 및 중첩 CTE 조인 (`test/slt/core/derived_join.slt`)**
   - 3개 이상의 인라인 서브쿼리(`FROM (SELECT ...) AS t1 JOIN (SELECT ...) AS t2`)
   - `LEFT JOIN` + 파생 테이블 내 집계 + 외부 필터 결합
   - 다중 조인 내 프로젝션 별칭 격리
4. **대규모 IN 리스트 및 상관 서브쿼리 (`test/slt/core/in_subquery_stress.slt`)**
   - 다수의 정수/문자열 IN 리스트 파싱 및 인덱스 시크
   - `NOT IN`과 서브쿼리 내 `NULL` 반환 시 3VL 정합성
   - `EXISTS (SELECT 1 ... WHERE ...)` 다중 중첩
5. **텍스트 패턴 및 특수 이스케이프 (`test/slt/core/string_escape.slt`)**
   - 작은따옴표 연속 이스케이프(`''`), 역슬래시(`\`), 줄바꿈, 탭 포함 문자열
   - `LIKE` 패턴 내 와일드카드(`%`, `_`) 및 결합 연산
   - 유니코드(한글, 다국어) 정렬 및 비교

> **기대 효과**: 현행 28개 ➔ 33개 파일(~230개 쿼리), 실서비스 ORM/JDBC에서 발생하기 쉬운 미세 문법 및 연산 결함 조기 박멸.

---

### Phase 2: SQLite 공식 `select1` ~ `select2` 실전 스위트 연동 (0.90.0 직후)
SQLite 공식 SQLLogicTest 저장소의 실제 벤치마크 파일 연동을 시작합니다.

1. **외부 SLT 패처 도구 구축 (`scripts/fetch_slt.go`)**
   - SQLite 공식 GitHub에서 `select1.test`, `select2.test` 원본 동기화
   - SQLite 전용 비호환 구문 자동 필터링
2. **배치 단위 실행 및 점진적 통과**
   - `select1`: 기본적인 SELECT, WHERE, ORDER BY, GROUP BY 대규모 조합 (~1,000 쿼리)
   - `select2`: 복합 연산자 및 컬럼 별칭 스위트 (~2,000 쿼리)
3. **병렬 실행 활용**
   - 앞서 구현한 `-parallel 8` 고루틴 워커 풀을 활용하여 수천 개 쿼리를 2초 이내 완주

---

### Phase 3: 무작위 대수식 및 집계 스트레스 매트릭스 (v0.95.0 마일스톤)
사람이 직접 작성하기 어려운 극한의 랜덤 중첩 수식을 기계적으로 검증합니다.

1. **`random/expr/` (랜덤 식 제너레이터)**
   - 최대 10단계 이상 중첩된 `CASE`, `COALESCE`, 단항/이항 연산자 무작위 결합
   - 부동소수점 오차 한계 및 타입 프로모션 규칙 완벽 정합성
2. **`random/groupby/` (랜덤 다중 집계)**
   - 임의의 5~10개 컬럼에 대한 복합 다차원 `GROUP BY`
   - 다중 `HAVING` 조건식과 집계 함수의 복합 계산
   - 윈도우 함수(`OVER (PARTITION BY ... ORDER BY ...)`) 결합 매트릭스

---

### Phase 4: SQLite 60,000+ 쿼리 풀 스위트 완성 및 CI 게이트화 (v1.0.0 GA)
공식 SQLite SQLLogicTest 전체를 LionSQL 릴리즈 게이트로 정식 통합합니다.

1. **`select1` ~ `select5` 전체 완주 (총 60,000+ 쿼리)**
2. **듀얼 엔진 및 듀얼 프로토콜 완전 검증**
   - In-Memory OCC 100% PASS
   - WiredTiger MVCC 100% PASS
   - PGWire TCP 원격 모드 100% PASS
3. **GitHub Actions CI 나이트리(Nightly) 연동**
   - 매일 자정 또는 릴리즈 태그 전 6만 개 쿼리 자동 실행 및 회귀 탐지
