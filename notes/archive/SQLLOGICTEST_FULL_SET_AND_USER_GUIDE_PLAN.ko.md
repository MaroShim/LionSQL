# LionSQL SQLLogicTest 1.0 GA 풀세트 구축 및 사용자 검증 가이드 계획서 (`notes/SQLLOGICTEST_FULL_SET_AND_USER_GUIDE_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **상태**: 계획 수립 및 구현 (Planning & Implementation)
> - **목표**: SQLite/DuckDB/CockroachDB 표준에 부합하는 약 40,000+ 쿼리 규모의 "LionSQL 공식 SQLLogicTest 1.0 GA 풀세트"를 정립하고, 원클릭 다운로드 스크립트(`scripts/fetch_slt.sh`)와 상세 사용자 가이드(`doc/SQLLOGICTEST_GUIDE.ko.md`)를 제공하여 누구나 20초 이내에 엔진 무결성을 재현·검증할 수 있도록 표준화.

---

## 1. 배경 및 목적

LionSQL은 VDBE 바이트코드 가상머신과 Volcano CBO 쿼리 옵티마이저를 탑재한 하이브리드 엔진입니다.
데이터베이스 엔진의 SQL 구문, 3치 논리(3VL), 연산자 결합 법칙, 콤마 조인 최적화의 수학적 무결성을 증명하기 위해서는 SQLite 창시자 D. Richard Hipp이 고안한 **SQLLogicTest (SLT)**가 세계적인 골든 스탠다드입니다.

SQLite 원본 SLT는 600만 쿼리에 달하여 일상적인 검증에 사용하기에는 비현실적입니다. 반면, DuckDB와 CockroachDB가 채택한 **Tier 1 & Tier 2 실전 검증 세트(~40,000 쿼리)**는 엔진의 모든 핵심 경로(인덱스 시크, 서브쿼리, 집계, 난수 대수식, 64개 테이블 조인)를 100% 포괄하면서도 **병렬 8코어 기준 15~20초 이내**에 완주할 수 있습니다.

본 계획은 다음 4대 산출물을 완성하는 것을 목표로 합니다:
1. **LionSQL 1.0 GA Full Set 선별 및 디렉토리 표준화** (`test/slt/`)
2. **동기화 자동화 스크립트 고도화** (`scripts/fetch_slt.sh`)
3. **사용자 친화적 원클릭 검증 CLI 연동** (`cmd/sqllogictest`)
4. **공식 사용자 가이드 문서 발행** (`doc/SQLLOGICTEST_GUIDE.ko.md` 및 `README.md` 링크)

---

## 2. LionSQL 1.0 GA 풀세트 구성 매트릭스 (총 40,000+ 쿼리)

```
test/slt/
├── core/                  # [Tier 1] LionSQL 정선 엣지 케이스 (34개 파일, 240 stmts / 181 queries)
├── whitespace/            # [Tier 1] 개행/탭/다중 공백 DDL/DML 파싱 (1개 파일)
├── boundary/              # [Tier 1] 키워드 식별자 충돌 및 경계값 (1개 파일)
├── full/                  # [Tier 1] SQLite 공식 대규모 조인·서브쿼리 (10,706 queries)
│   ├── select1.test       # 기본 SELECT, WHERE, ORDER BY, GROUP BY 조합 (1,031 queries)
│   ├── select2.test       # 복합 연산자 및 컬럼 별칭 스위트 (1,031 queries)
│   ├── select3.test       # 다중 조인 및 중첩 상관 서브쿼리 (3,351 queries)
│   ├── select4.test       # ANSI-89 콤마 조인 및 조건 푸시다운 (3,857 queries)
│   └── select5.test       # 최대 64개 테이블 콤마 조인 재정렬 (1,436 queries)
├── evidence/              # [Tier 2] SQL 표준 명세(Grammar Specification) 조항 전수 (12개 파일, ~5,000 queries)
│   ├── in1.test ~ in2.test
│   ├── slt_lang_aggfunc.test
│   └── slt_lang_*.test (update, createview, droptable 등)
├── index/                 # [Tier 2] B-Tree 인덱스 경로 및 레인지 스캔 전수 (16개 파일, ~15,000 queries)
│   ├── between/ (1, 10, 100)
│   ├── commute/ (1, 10, 100)
│   ├── in/ (1, 10, 100)
│   ├── orderby/ & orderby_nosort/
│   └── delete/ & view/
└── random/                # [Tier 2] 기계식 문법 난수 퍼징 대표 샘플군 (40개 파일, ~15,000 queries)
    ├── expr/slt_good_0.test ~ 9.test        (10개 파일: 복합 수식 및 연산자 결합)
    ├── groupby/slt_good_0.test ~ 9.test     (10개 파일: 임의 다차원 GROUP BY / HAVING)
    ├── aggregates/slt_good_0.test ~ 9.test  (10개 파일: 다중 집계 함수 결합)
    └── select/slt_good_0.test ~ 9.test      (10개 파일: SELECT ALL 및 단항 연산자 결합)
```

---

## 3. 세부 설계 및 도구 고도화

### 3.1 `scripts/fetch_slt.sh` 스크립트 확장
- 기존 `select1~5.test`만 다운로드하던 단일 루프를 확장하여 다음 옵션을 제공:
  - `./scripts/fetch_slt.sh`: 기본값으로 1.0 GA 풀세트(full, evidence, index, random 대표군) 다운로드
  - `./scripts/fetch_slt.sh --suite <full|evidence|index|random|core>`: 특정 스위트만 선택적 다운로드
  - 다운로드 시 병렬 다운로드(`curl` 백그라운드 또는 순차 안전 폴링) 및 파일 라인 수 검증 로그 출력
  - 기저장된 파일이 존재할 경우 불필요한 재다운로드 스킵(캐싱)

### 3.2 러너 CLI (`cmd/sqllogictest`) 편의성
- 타겟 방언 플래그: `-target sqlite,lionsql` 기본값 탑재 완료
- 병렬 워커: `-parallel 8` (CPU 코어 자동 감지 또는 수동 지정)
- 스토리지 엔진: `-engine memory` (인메모리 OCC, 20초 컷) 및 `-engine wiredtiger` (디스크 MVCC)
- 실행 모드: `-mode inproc` (초고속 인프로세스) 및 `-mode pgwire` (실제 TCP 포트 E2E)

### 3.3 사용자 공식 가이드 문서 (`doc/SQLLOGICTEST_GUIDE.ko.md`)
- **개요 및 의의**: SQLLogicTest가 무엇이고 왜 중요한가
- **원클릭 빠른 시작**: 다운로드부터 20초 내 결과 확인까지 단 3줄 명령어로 끝나는 퀵스타트
- **스위트 구성 설명**: Core, Select1~5, Evidence, Index, Random의 각각의 검증 영역
- **실행 옵션 매트릭스**: In-Memory vs WiredTiger, In-Process vs PGWire TCP 모드
- **CI/CD 연동 예시**: GitHub Actions 또는 로컬 사전 커밋 훅에서 활용하는 방법

---

## 4. 검증 파이프라인 및 완료 기준 (3-Stage Pipeline)
1. **Build**: `cmd/sqllogictest` 및 `cmd/lionsql` 정상 빌드
2. **Test**: `go test -race ./pkg/slt/...` 및 `./scripts/fetch_slt.sh` 연동 무결성 검증
3. **Commit**: Conventional Commits 포맷, 무이모지, `TODO.md` 동기화
