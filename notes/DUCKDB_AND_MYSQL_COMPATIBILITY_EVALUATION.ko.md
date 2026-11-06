# LionSQL 아키텍처 결정 기록: DuckDB 및 MySQL 호환성 한계와 기각 사유

> - **최초 작성일**: 2026-11-04
> - **최종 갱신일**: 2026-11-04

## — LionSQL is NOT DuckDB: 엔터프라이즈 OLTP의 엄격함과 기각된 편의 기능 (`notes/DUCKDB_AND_MYSQL_COMPATIBILITY_EVALUATION.ko.md`)

> - **최초 작성일**: 2026-11-04  
> - **문서 성격**: **Architecture Decision Record (ADR) & Negative Knowledge Note**  
> - **주제**: DuckDB 스타일 친화적 SQL 및 MySQL 호환 기능 확장에 대한 기술 평가, 거부(Spec-Out) 사유, 불변 경계 확립  
> - **연계 문서**:  
>   - 핵심 아키텍처 불변식: [`notes/ENGINEERING_CONTEXT.ko.md`](ENGINEERING_CONTEXT.ko.md) (불변 원칙 13)  
>   - 포스트 1.0 후보 과제(Candidates): [`notes/POST_1.0_CANDIDATES.ko.md`](POST_1.0_CANDIDATES.ko.md)  
>   - 액티브 대시보드: [`TODO.md`](../TODO.md)  

---

## 1. 배경 및 문제 제기 (Context & Problem Statement)

데이터베이스 엔진을 개발하다 보면 사용자 편의성이나 외연 확장을 이유로 다음과 같은 매력적인 유혹에 끊임없이 직면하게 됩니다:

1. **DuckDB 스타일 편의 SQL (`FRIENDLY-SQL`)**:
   - `SELECT * EXCLUDE (col1, col2)` (특정 컬럼 제외 프로젝션)
   - `FROM <table>;` (`SELECT *` 생략형 전체 스캔)
   - `GROUP BY ALL` (비집계 컬럼 자동 그룹핑)
   - `SELECT * FROM 'data.csv'` (로컬 파일 직접 조회)
2. **MySQL 기능의 지속적 확장 요구**:
   - `ON DUPLICATE KEY UPDATE` (MySQL 특유의 Upsert DML)
   - 8컬럼 납작한 테이블형 `EXPLAIN` (MySQL 5.x 시절 레거시 실행 계획)
   - MySQL Wire Protocol (TCP `:3306`) 지원 요구

표면적으로는 "다른 DB에 있는 좋은 편의 기능이니 다 가져오면 사용자 경험이 좋아지지 않을까?"라는 생각이 들 수 있습니다. 
그러나 **소프트웨어 아키텍처의 성패는 "무엇을 만들었는가"보다 "무엇을 만들지 않고 거부했는가"에 의해 결정**됩니다.

본 문서는 왜 LionSQL이 DuckDB 스타일 문법을 기각하고, MySQL 호환성을 손끝 편의(Phase 1) 수준에서 엄격히 동결해야 하는지에 대한 기술적·철학적 근거를 영구 기록(Negative Knowledge)으로 보존합니다.

---

## 2. LionSQL is NOT DuckDB: 편의 SQL (`FRIENDLY-SQL`) 전면 기각 사유

### 2.1 정체성의 충돌 (OLAP 임베디드 장난감 vs 엔터프라이즈 OLTP 커널)
- **DuckDB의 환경**: 데이터 분석가가 노트북에서 로컬 CSV를 열어 단일 스레드/단일 세션으로 일회성 분석을 수행하는 임베디드 OLAP 도구입니다. 쿼리가 깨지거나 잘못 돌아도 분석가 개인의 시행착오로 끝납니다.
- **LionSQL의 환경**: 초당 수만~수십만 건의 ACID 금융 트랜잭션, 계좌 이체, 동시성 격리, 멀티 세션을 다루는 **엔터프라이즈급 OLTP 관계형 데이터베이스**입니다. 
- **결론**: 두 엔진은 지향하는 신뢰성(Reliability)과 도메인의 엄격함 자체가 근본적으로 다릅니다.

### 2.2 오타의 조용한 침묵 (Silent Failure & Disaster)
가장 치명적인 기술적 문제는 **`FROM <table>;` 구문이 오타(Typo)를 삼켜버린다**는 점입니다.
- 개발자가 복잡한 쿼리를 작성하다가 실수로 앞부분 `SELECT id, name `을 빼먹고 붙여넣거나 네트워크 패킷이 잘렸을 때:
  - 표준 ANSI/T-SQL/PostgreSQL: **즉시 `Syntax Error`를 발생시키며 트랜잭션을 중단**합니다.
  - Friendly SQL 허용 시: 엔진이 "친절하게" `SELECT *`로 간주하여 **수천만 건의 민감 데이터 행 전체를 풀 스캔하여 반환**합니다.
- 이는 단순한 불편함이 아니라 심각한 서비스 장애, CPU/메모리 폭발, 데이터 보안 유출 사고로 직결됩니다. **"오타를 친절하게 넘겨주는 RDBMS는 엔터프라이즈 환경에서 재앙"**입니다.

### 2.3 제어 모델(Control Mode)의 딜레마와 복잡도 파탄
이를 방어하기 위해 어떤 우회책을 고려하더라도 모두 아키텍처 결함으로 귀결됩니다:
1. **기본 허용 (Default On)**: 위에서 지적한 오타 침묵 및 프로덕션 이식성 파괴.
2. **엄격 모드 플래그 (`sql_mode = 'strict'`)**: MySQL 특유의 악명 높은 '모드 지옥(Configuration Hell)'을 답습하게 되며, 불필요한 설정 분기를 배제하는 [`[RULE:RadicalKISS]`] 원칙을 정면 위반.
3. **접속 채널 분기 (Web Studio 허용 vs 와이어 프로토콜 차단)**: 
   동일한 LionSQL 인스턴스인데 웹 스튜디오에서는 실행되는 쿼리가 DBeaver(PGWire)나 SSMS(TDS)에서는 문법 에러가 나는 기괴한 UX 파편화 유발.

### 2.4 결론
DuckDB 스타일 편의 문법은 **오버엔지니어링이자 RDBMS의 엄격함을 훼손하는 백해무익한 유혹**입니다. LionSQL은 이를 구현하지 않으며, 영구 기각(Spec-Out)합니다.

---

## 3. MySQL 호환성 확장: 엄격한 경계선과 영구 동결 사유

LionSQL은 이미 MySQL 호환성 Phase 1(`S-MYSQL-1-P1`)을 완료하였습니다. 그러나 **이 경계선은 최종 한계선이며, 이후의 추가 확장은 영구 기각**되었습니다.

### 3.1 허용된 유일한 영역: "손끝 인체공학 (Phase 1 Ergonomics)"
다음 4가지 요소는 개발자가 CLI나 DBeaver에서 무의식적으로 입력하는 타이핑 습관을 흡수하므로 유지됩니다:
1. `DESC <table>`, `DESCRIBE <table>` (테이블 메타데이터 조회)
2. `SHOW TABLES`, `SHOW DATABASES` (목록 조회)
3. 백틱(`` `identifier` ``) 식별자 이스케이프
4. `IFNULL()`, `NOW()`, `CONCAT()` 등 부수효과 없는 순수 스칼라 함수

### 3.2 영구 기각된 영역: DML, 플래너, 와이어 프로토콜 (Phase 2 & 3 Spec-Out)

#### ① `ON DUPLICATE KEY UPDATE` (DML 침범) — **기각**
- **기각 사유**:
  - LionSQL은 이미 **PostgreSQL 표준(`ON CONFLICT DO UPDATE`)**과 **ANSI/MSSQL 표준(`MERGE`)**을 완벽하게 지원합니다.
  - 여기에 MySQL 고유의 DML을 추가하면 스토리지 레이어의 충돌 검증 및 트랜잭션 롤백 경로가 3갈래로 분기되어 OCC/MVCC의 락프리 불변식을 심각하게 오염시킵니다.
  - 이미 동일한 기능을 수행하는 두 표준이 완비되어 있으므로 완벽한 YAGNI 위반입니다.

#### ② 8컬럼 납작한 테이블형 `EXPLAIN` (플래너 침범) — **기각**
- **기각 사유**:
  - LionSQL의 CBO(Cost-Based Optimizer)는 하향식 트리 탐색(Volcano Iterator Model)을 기반으로 계층 구조를 가집니다.
  - MySQL 5.7 시절의 납작한 8컬럼(`id`, `select_type`, `table`, `type`, `possible_keys` 등) 출력은 현대적인 계층형 실행 트리를 강제로 파괴(Flatten)합니다.
  - 심지어 원조인 MySQL 8.0조차 8컬럼 방식을 레거시로 분류하고 `FORMAT=TREE`를 표준으로 권장하고 있습니다. 폐기되는 레거시를 굳이 주워 담을 이유가 없습니다. LionSQL은 현대적인 Indented Tree 텍스트와 JSON 표현만을 지원합니다.

#### ③ MySQL Wire Protocol (포트 :3306 지원) — **기각**
- **기각 사유**:
  - LionSQL은 이미 **PostgreSQL v3 (`TCP :5433`)**와 **Microsoft TDS 7.x/8.0 (`TCP :1433`)**이라는 전 세계 표준 듀얼 와이어 프로토콜을 탑재하고 있습니다.
  - 현재 산업계의 모든 프로그래밍 언어, BI 도구, ORM, GUI 클라이언트는 PostgreSQL 또는 SQL Server 드라이버 중 하나를 반드시 기본 제공합니다.
  - 단일 바이너리에 제3의 유선 프로토콜 상태 머신을 욱여넣는 것은 코드 비대화와 보안 표면적(Attack Surface)만 넓힐 뿐, 실질적인 비즈니스 효익은 0입니다.

---

## 4. 종합 아키텍처 원칙 요약 (Summary Principles)

```
[LionSQL의 확고한 2대 기둥]
  ├── Core A: Microsoft SQL Server T-SQL 바이트코드 VM & TDS 프로토콜 (고성능 엔터프라이즈 절차형 언어)
  └── Core B: PostgreSQL v3 와이어 프로토콜 & 현대적 ANSI 표준 (풍부한 오픈소스 앱 생태계)

[호환성 수용의 불변 경계선]
  ├── DuckDB 문법: 전면 수용 불가 (오타 침묵 위험, 엄격성 위배로 영구 배제)
  └── MySQL 호환: Phase 1 손끝 편의(DESC, SHOW, 백틱)에서 동결 (DML/플래너/프로토콜 침범 절대 불허)
```

1. **줏대 있는 엔지니어링**: 남들이 넣었다고 해서 무비판적으로 따라 넣지 않는다. 우리 시스템의 정체성(OLTP 트랜잭션 무결성)을 해치는 기능은 가차 없이 거부한다.
2. **KISS 원칙 준수 ([RULE:RadicalKISS])**: 편의 기능을 넣겠다고 복잡한 설정 플래그나 모드를 증식시키지 않는다.
3. **표준의 존중**: 비표준 트릭보다는 검증된 ANSI SQL, T-SQL, PostgreSQL 표준 경로 하나를 단단하게 유지하는 것이 100배 가치 있다.
