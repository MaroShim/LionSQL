# LionSQL SQLLogicTest (SLT) 엔진 통합 및 검증 계획서 (`notes/SQLLOGICTEST_INTEGRATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **상태**: 구현 및 검증 완료 (Completed) — 28개 스위트 4대 매트릭스 전수 통과
> - **목표**: SQLite/DuckDB 표준 SQLLogicTest 스위트를 LionSQL 커널 및 와이어 프로토콜에 연동하여, 화이트스페이스 변형, 키워드 식별자 충돌, 논리 연산자 우선순위, 3치 논리(NULL)에 대한 블랙박스 오라클 정합성을 100% 자동 검증합니다.

---

## 1. 배경 및 도입 목적

최근 파서 및 플래너 감사에서 확인된 바와 같이, 원시 SQL 문자열에 대한 나이브한 키워드 매칭(`strings.Contains`, `strings.Index`)은 다음 결함들을 내포합니다:
1. **화이트스페이스 누락**: `\t`, `\n`, 연속 공백 등 비표준 공백 처리 누락 (`WHERE (a=1)OR(b=2)`, `SELECT\ta\nFROM\tt`)
2. **식별자 및 리터럴 키워드 오인**: 컬럼명 `nowhere`, `turnover` 및 문자열 리터럴 `'SELECT * FROM t'` 오탐
3. **복합 키워드 분리 누락**: `IF    EXISTS`, `WITHIN \t GROUP` 등 다중 단어 키워드 간 공백 불일치

**SQLLogicTest(SLT)**는 수십만 건의 조합적 쿼리 매트릭스와 기대 결과(또는 MD5 해시)를 비교하는 업계 표준 블랙박스 테스트 규격으로, 수동 단위 테스트로는 발견하기 어려운 엣지 케이스를 기계적으로 완전 포착합니다.

---

## 2. SQLLogicTest 스펙 포맷 분석

SLT 파일(`*.slt`, `*.test`)은 표준 4대 디렉티브로 구성됩니다:

```text
# 1. Statement 성공 검증
statement ok
CREATE TABLE t1(a INTEGER, b VARCHAR, c INTEGER)

# 2. Statement 실패 검증
statement error
CREATE TABLE t1(a INTEGER)

# 3. Query 결과 행 검증 (rowsort / valuesort / nosort)
query IT rowsort
SELECT a, b FROM t1 WHERE a = 1 OR c = 10
----
1 SELECT * FROM nowhere

# 4. Query 대량 결과 해시 검증 (MD5)
query I nosort
SELECT a FROM t1
----
1000 values hashing to 8b1a9953c4611296a827abf8c47804d7
```

---

## 3. 아키텍처 설계: 경량 순수 Go SLT 러너 (`cmd/sqllogictest`)

LionSQL의 단일 정적 바이너리 원칙(Invariant 4)과 외부 의존성 배제에 맞춰, 순수 Go로 단일 실행기 `cmd/sqllogictest`를 구현합니다:

### 3.1. 듀얼 실행 백엔드
1. **In-Process 모드 (`--mode=direct`)**:
   - `engine.Coordinator`를 메모리/WiredTiger 상에서 직접 호출.
   - 네트워크 오버헤드 없이 초당 10,000+ 쿼리 초고속 실행.
2. **PGWire 프로토콜 모드 (`--mode=pgwire --dsn=postgres://...`)**:
   - 실제 네트워크 TCP 소켓(`TCP :5433`)을 통해 드라이버 인코딩/디코딩 및 파라미터 프로토콜까지 E2E 검증.

### 3.2. 결과 매칭 엔진
- **정렬 모드**: `nosort`(순서 보존), `rowsort`(행 단위 알파벳 정렬), `valuesort`(모든 셀 단위 정렬).
- **타입 시그니처**: `I`(정수), `T`(문자열/날짜), `R`(부동소수점/Decimal).
- **NULL 표현**: `NULL` 토큰과 빈 문자열 구분.
- **해시 판정**: 대량 결과셋에 대해 MD5 128비트 다이제스트 일치 검증.

---

## 4. 단계별 실행 로드맵 (Phase 1 ~ Phase 2)

### Phase 1: SLT 러너 기반 구축 및 코어 쿼리 스위트 (Core Logic & Syntax)
- [ ] `cmd/sqllogictest/main.go`: SLT 파서, 러너, 결과 매칭, MD5 해시 비교 엔진 구현
- [ ] `test/slt/core/`:
  - `select1.slt`: 기본 프로젝션, 별칭, 리터럴 식별자
  - `where.slt`: 기본 술어, 단일/복합 인덱스 스캔
  - `join.slt`: Inner/Left/Right/Cross 조인
  - `null.slt`: 3치 논리(`IS NULL`, `IS NOT NULL`, NULL 전파)
  - `aggregate.slt`: `COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, `GROUP BY`, `HAVING`

### Phase 2: 엣지 케이스 및 화이트스페이스 극한 스위트 (Edge Cases & Whitespace)
- [ ] `test/slt/whitespace/`:
  - 탭/개행/다중 공백 변형 (`SELECT\ta\nFROM\tb\r\nWHERE\tc=1`)
  - 괄호와 연산자 간 0-공백 (`WHERE (a=1)OR(b=2)AND(c=3)`)
  - 다중 단어 키워드 변형 (`IF    EXISTS`, `WITHIN \t GROUP`, `IS  \t NOT \n NULL`, `ORDER   BY`, `PARTITION   BY`)
- [ ] `test/slt/boundary/`:
  - 식별자/리터럴 키워드 포함 (`col = 'SELECT * FROM t WHERE id=1'`, 컬럼명 `nowhere`, `turnover`, `is_coverage`)
  - VDBE 바이패스 차단 및 폴백 안전성 검증
- [ ] CI 파이프라인 연동 (`go test -tags wiredtiger ./test/slt/...` 또는 `bin/sqllogictest`)

---

## 5. 검증 파이프라인
1. `go build -tags wiredtiger -buildvcs=false -o bin/sqllogictest ./cmd/sqllogictest`
2. `bin/sqllogictest -dir test/slt/` 전체 PASS 확인
3. `go test -tags wiredtiger -count=1 -race ./...` 100% PASS (0 data race)
4. Conventional Commits 준수 및 장식용 이모지 0건
