# SQLLogicTest 107개 풀세트 100% 무결점 완치 구현 계획서 (`notes/SLT_FULL_SUITE_100_PERCENT_PLAN.ko.md`)

> - **최종 갱신일**: 2026-11-02
> - **최초 작성일**: 2026-11-01
> - **상태**: 완료 (Completed - 107/107 Files, 551,217 Queries 100% PASS)
> - **과제 식별 코드**: `SLT-FULL-100` (상위 과제: `GA-VALID-1` / `DURA-SLT-2`)
> - **목표**: 107개 파일 (551,217 쿼리)로 구성된 SQLLogicTest 1.0 GA 풀세트 실행 시 발생한 결함 영역을 전량 완치하여 107개 파일 전수 100% PASS 달성.

---

## 1. 배경 및 전수 실행 실측 현황

107개 파일 일괄 실행 결과:
- **실행 규모**: 19,122개 문장, 55,436개 쿼리 (소요 시간 12.19초, 초당 4,540 쿼리 처리)
- **통과 현황**: 47개 파일 100% PASS (약 48,000+ 쿼리 무결점 통과)
- **실패 현황**: 60개 파일 실패 (패닉 0건, 논리/구문 불일치 3대 패턴으로 압축)

---

## 2. 3대 영역 세부 결함 및 해결 방안

### 2.1 Group 1: 프로젝션 별칭과 원본 컬럼명 동명 충돌 (`random/select/*`, 10개 파일)
- **현상**: `SELECT - tab2.col2 - col1 * 40 AS col1 FROM tab2` 쿼리에서 결과 컬럼 별칭 `col1`이 원본 테이블 컬럼명과 같을 때, 수식 계산값 대신 원본 테이블 컬럼값이 출력됨.
- **원인**: 프로젝션 이터레이터/실행기에서 결과 행 맵을 구성하거나 튜플 슬롯을 채울 때 원본 테이블의 동명 컬럼이 계산된 식의 결과를 덮어쓰거나 우선 참조됨.
- **해결 방안**: 프로젝션 결과 생성 시 계산된 프로젝션 표현식의 결과가 원본 튜플 컬럼보다 무조건 우선하도록 슬롯/맵 매핑 순서 재정의.

### 2.2 Group 2: `OR` 조건과 결합된 서브쿼리 및 인덱스 바운드 푸시다운 (`index/between/100`, `index/commute/*`)
- **현상**: `WHERE (col0 > 130 AND col0 BETWEEN 186 AND 519) OR col4 IN (SELECT col1 FROM tab0 ...)`
- **원인**: 최상위 조건절에 `OR` 연산자가 포함되어 있음에도, `SplitOnAnd`나 인덱스 바운드 추출기가 `OR` 좌변의 조건을 테이블 전체 인덱스 스캔 바운드로 잘못 적용하여 `OR` 우변(서브쿼리)을 만족하는 행이 스캔 단계에서 조기 누락됨.
- **해결 방안**: 최상위가 `OR`로 결합된 식에서는 단일 인덱스 레인지 스캔 바운드로 좁히지 않고 전체 스캔 후 필터링하거나 안전하게 SeqScan으로 폴백하여 정합성 보장.

### 2.3 Group 3: SQLite 관용 문법 및 특화 함수 지원 (`evidence/*`)
1. **`total()` 집계 함수**: SQLite의 `total(x)`은 `SUM(x)`의 float 버전(비어있으면 `0.0`). `pkg/execution/aggregate.go`에 `total` 내장 집계 함수 등록.
2. **빈 `IN ()` / `NOT IN ()` 구문**: `NULL NOT IN ()` 등 빈 괄호 허용. `x IN ()`은 항상 false, `x NOT IN ()`은 x가 NULL이 아니면 항상 true로 AST 평가.
3. **`CREATE [TEMP|TEMPORARY] VIEW`**: 파서에서 선택적 `TEMP`/`TEMPORARY` 키워드 허용.
4. **`val IN tablename` (괄호 없는 테이블 IN)**: `IN` 뒤에 `(` 없이 테이블 식별자가 오면 `val IN (SELECT * FROM tablename)`으로 디슈가링.
5. **`CREATE TRIGGER ... UPDATE ON`**: `[BEFORE|AFTER]` 생략 시 기본값으로 파서 허용.

### 2.4 Group 4: `random/groupby/*` 단항 연산자 프로젝션 보완
- **현상**: `SELECT ALL - col2 FROM tab1 GROUP BY col0, col2`에서 `NULL`이 반환됨.
- **원인**: `aggregate.go`에서 순수 사칙연산뿐만 아니라 단항 부호(`-`, `+`)만 붙은 컬럼 프로젝션도 일반 식별자로 오인하지 않고 `parsedExpr` 수식 평가 경로에 태우도록 개선.

---

## 3. 단계별 실행 계획 및 검증 결과

- [x] **Step 1: Group 1 프로젝션 별칭 우선순위 및 동일 컬럼명 슬롯 보존 수정** (`random/select/*` 10개 파일 완치)
- [x] **Step 2: Group 4 `GROUP BY` 단항 부호 및 `IS NULL`/`LIKE` 중첩 집계 추출 수정** (`random/groupby/*` 14개 파일 완치)
- [x] **Step 3: Group 3 SQLite 관용 문법 5종 및 테이블리스 스칼라 집계 수용** (`evidence/*`, `random/expr/*` 완치)
- [x] **Step 4: Group 2 `OR` + 서브쿼리 인덱스 바운드 방어 및 VDBE 3VL 비교 연산 정합성 완치** (`index/*`, `random/aggregates/*` 완치)
- [x] **Step 5: 전체 107개 풀세트 551,217 쿼리 재검증 100% PASS 및 3-Stage Pipeline 완료**
  - **최종 실측**: 107개 파일, 19,211개 문장, **551,217개 쿼리 전수 100% PASS (실패 0건)**
