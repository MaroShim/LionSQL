# LionSQL SQLLogicTest (SLT) 1.0 GA 풀세트 사용자 검증 가이드 (`doc/SQLLOGICTEST_GUIDE.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **최종 갱신일**: 2026-11-04


이 가이드는 SQLite, DuckDB, CockroachDB가 엔진의 수학적 무결성을 검증하기 위해 사용하는 국제 표준 **SQLLogicTest (SLT)** 스위트를 LionSQL에서 다운로드하고, 실행 및 검증하는 방법을 안내합니다.

LionSQL 1.0 GA 풀세트는 핵심 엣지 케이스부터 최대 64개 테이블 콤마 조인, B-Tree 인덱스 경로, 표준 SQL 명세 및 기계식 난수 대수식까지 **총 40,000+ 쿼리**를 포괄하며, 내장된 병렬 러너를 통해 **약 15~20초 이내에 100% 검증을 완주**할 수 있습니다.

---

## 1. 3줄 요약 빠른 시작 (Quick Start)

터미널에서 아래 3개 명령어만 실행하면 다운로드부터 40,000+ 쿼리 전수 검증까지 즉시 완료됩니다.

```bash
# 1. 1.0 GA 공식 풀세트 다운로드 (최초 1회 실행, 약 3초 소요)
./scripts/fetch_slt.sh

# 2. 초고속 병렬 SLT 러너 빌드
go build -tags wiredtiger -buildvcs=false -o bin/sqllogictest ./cmd/sqllogictest

# 3. 8코어 병렬 초고속 검증 실행 (소요 시간: 약 15~20초)
./bin/sqllogictest -dir test/slt -parallel 8
```

실행이 완료되면 아래와 같이 전수 통과(100% PASS) 요약 리포트가 출력됩니다:

```text
================================================================================
                             SLT SUITE RESULTS                                  
================================================================================
 Files:       107 total, 107 passed, 0 failed
 Statements:  1,420 executed
 Queries:     38,912 executed
 Total Time:  18.42s
--------------------------------------------------------------------------------
 Verdict:     100% PASS (Zero conformance mismatches across all SLT test cases)
================================================================================
```

---

## 2. LionSQL 1.0 GA 풀세트 구성

LionSQL의 SLT 스위트는 업계 표준(DuckDB/CockroachDB Tier 1 & 2)에 맞춰 엄선된 5개 카테고리로 구성되어 있습니다:

| 카테고리 | 디렉토리 | 파일 수 | 쿼리 규모 | 주요 검증 영역 |
| :--- | :--- | :--- | :--- | :--- |
| **Core & Edge** | `test/slt/core/` | 34개 | ~400건 | 3치 논리(3VL), CTE, Window, Self/Anti Join, NULLIF, 트랜잭션 격리 |
| **Full Select** | `test/slt/full/` | 5개 | 10,706건 | SQLite 공식 `select1~5.test`, 최대 64개 테이블 콤마 조인, 다중 서브쿼리 |
| **Evidence** | `test/slt/evidence/`| 12개 | ~5,000건 | SQL 표준 명세(Grammar Specification) 조항 전수 (`in1`, `aggfunc`, `update` 등) |
| **Index** | `test/slt/index/` | 16개 | ~15,000건 | B-Tree 인덱스 유무별 결과 일치성, `BETWEEN` 다중 하한/상한 교집합, `ORDER BY` 비용 생략 |
| **Random** | `test/slt/random/` | 40개 | ~15,000건 | BNF 기계식 난수 퍼징: `expr`(수식 중첩), `groupby`, `aggregates`, `select` |
| **Procedural (T-SQL)** | `test/slt_proc/` | 14개 | ~300건 | T-SQL Stored Procedure, 재귀 CTE 델타, 리턴 변수, UDF 라이프사이클 ([상세 보고서](../notes/reports/BABELFISH_INTEGRATION_REPORT.ko.md)) |
| **합계** | **전체 풀세트** | **121개**| **40,000+건**| **SQL 표준 엔진 및 T-SQL 절차형 무결성 100% 검증** |

---

## 3. 다운로드 스크립트 상세 옵션 (`scripts/fetch_slt.sh`)

LionSQL 저장소의 크기 비대화를 방지하기 위해 대용량 공식 테스트 세트는 온디맨드 스크립트로 다운로드됩니다. 다운로드된 파일은 로컬 디스크에 캐싱되어 두 번째 실행부터는 0.01초 만에 스킵됩니다.

```bash
# 전체 풀세트 다운로드 (기본값)
./scripts/fetch_slt.sh

# 특정 스위트만 선택적 다운로드
./scripts/fetch_slt.sh --suite full       # select1.test ~ select5.test만 다운로드
./scripts/fetch_slt.sh --suite evidence   # SQL 표준 명세 스위트만 다운로드
./scripts/fetch_slt.sh --suite index      # 인덱스 경로 스위트만 다운로드
./scripts/fetch_slt.sh --suite random     # 난수 대수식/집계 스위트만 다운로드

# 기존 파일을 강제로 덮어쓰며 새로 다운로드
./scripts/fetch_slt.sh --force
```

---

## 4. 실행 러너 상세 옵션 (`cmd/sqllogictest`)

`bin/sqllogictest`는 고성능 고루틴 워커 풀을 내장하여 다양한 스토리지 엔진과 실행 프로토콜 매트릭스를 지원합니다.

### 4.1 CLI 플래그 목록

| 플래그 | 기본값 | 설명 |
| :--- | :--- | :--- |
| `-dir` | `test/slt` | 검증할 `.slt` 및 `.test` 파일이 포함된 디렉토리 경로 |
| `-file` | `""` | 단일 특정 테스트 파일만 실행할 때 지정 |
| `-parallel` | `1` | 병렬 실행 워커(고루틴) 수 (권장: CPU 코어 수, 예: `8`) |
| `-engine` | `memory` | 스토리지 백엔드 선택 (`memory`: 인메모리 OCC, `wiredtiger`: 디스크 MVCC) |
| `-mode` | `inproc` | 실행 모드 선택 (`inproc`: 초고속 내부 실행, `pgwire`: 실제 TCP 소켓 E2E) |
| `-target` | `sqlite,lionsql` | `onlyif`/`skipif` 지시자 판별용 타겟 방언 태그 (콤마 구분) |
| `-v` | `false` | 각 레코드별 상세 SQL 실행 로그 출력 |

### 4.2 주요 실행 시나리오 예시

#### (1) 초고속 인메모리 전체 검증 (약 15~20초 소요, 개발 중 추천)
```bash
./bin/sqllogictest -dir test/slt -parallel 8 -engine memory -mode inproc
```

#### (2) 디스크 영속성 WiredTiger B+Tree 엔진 검증
```bash
./bin/sqllogictest -dir test/slt -parallel 8 -engine wiredtiger -mode inproc
```

#### (3) 실제 PostgreSQL Wire Protocol v3 TCP 소켓 E2E 검증
LionSQL 내부에 실제 PGWire TCP 서버(`127.0.0.1:0`)를 띄우고 네트워크 패킷 인코딩/디코딩 파이프라인 전체를 검증합니다.
```bash
./bin/sqllogictest -dir test/slt/core -engine wiredtiger -mode pgwire
```

#### (4) 단일 특정 스위트 집중 디버깅
```bash
# select1만 상세 로그(-v)와 함께 실행
./bin/sqllogictest -file test/slt/full/select1.test -v
```

---

## 5. 단위 테스트(`go test`) 상시 회귀 연동

`cmd/sqllogictest` CLI뿐만 아니라 Go 표준 테스트 파이프라인(`go test`)에서도 상시 실행할 수 있습니다:

```bash
# 1. 코어 스위트 및 풀세트 인프로세스 회귀 테스트
go test -tags wiredtiger -v ./test/slt/...

# 2. 데이터 레이스 0건 무결성 정밀 검증
go test -tags wiredtiger -count=1 -race ./pkg/slt/...
```

---

## 6. 트러블슈팅 및 FAQ

### Q1. `onlyif` / `skipif` 지시자가 무엇인가요?
SQLLogicTest 공식 파일 상단에는 특정 DBMS(예: `mssql`, `oracle`)에서만 실행하거나 제외하기 위한 `onlyif <db>` 및 `skipif <db>` 지시자가 선언되어 있습니다. LionSQL 러너는 `-target sqlite,lionsql`을 기본 활성화하여 SQLite 표준 오라클 결과를 그대로 검증합니다.

### Q2. 검증 결과 불일치(Mismatch)가 발생하면 어떻게 확인하나요?
러너는 쿼리 실패 시 실패한 파일 경로, 라인 번호, 실행된 SQL 문, 실제 반환값 및 기대 해시/셀 값을 즉시 표준 에러(`stderr`)로 출력합니다:
```text
FAIL  test/slt/core/example.slt (2ms) - 5 stmts, 2 queries
      Error: [test/slt/core/example.slt:25] cell 0 mismatch at SQL: SELECT x FROM tab
      Got:      "10"
      Expected: "20"
```

### Q3. CI/CD 파이프라인에 통합하려면 어떻게 하나요?
GitHub Actions 또는 로컬 사전 커밋 훅에서 다음과 같이 실행하면 불일치 발생 시 즉시 종료 코드 `1`을 반환하여 배포를 방지합니다:
```yaml
- name: Run SQLLogicTest 1.0 GA Full Suite
  run: |
    ./scripts/fetch_slt.sh
    go build -tags wiredtiger -o bin/sqllogictest ./cmd/sqllogictest
    ./bin/sqllogictest -dir test/slt -parallel 8
```
