# Gitea XORM 실전 연동 결함 및 테스트 사각지대 사후 분석 보고서 (Postmortem)

> - **최초 작성일**: 2026-10-29
> - **문서 상태**: 영구 보존 아키텍처 사후 분석 보고서 (Architecture Postmortem & Negative Knowledge)
> - **대상 영역**: SQL 파서(`pkg/proc`), 쿼리 플래너(`pkg/planner`), 실행 엔진(`pkg/execution`), 가상 카탈로그(`pkg/catalog/virtual`)

---

## 1. 개요 및 문제 제기

LionSQL은 14만 tpmC에 달하는 TPC-C 벤치마크, SSB 복합 분석 쿼리 스위트, 그리고 수천 개의 패키지 단위 테스트를 100% 통과하며 `v0.90.0` 기능 동결(Feature Freeze)을 달성했다.

그러나 프로덕션 Git 호스팅 엔진인 **Gitea(v1.27.3)**를 Microsoft SQL Server TDS `:1433` 프로토콜을 통해 LionSQL에 실시간 연동하고 전체 모의 데이터를 시딩(`scripts/seed_gitea_mock_data.go`)하는 과정에서 다음과 같은 치명적인 커널 레벨 호환성 결함들이 연속적으로 노출되었다:

1. `UPDATE ... FROM <table> WHERE ...` 구문 처리 불가로 인한 영향받은 행 수(RowsAffected) 0 반환 및 트랜잭션 롤백 (`the issue is already changed`)
2. `SELECT repository.*` 테이블 한정 프로젝션 시 조인 대상 테이블의 동명 컬럼 덮어쓰기로 인한 Git 저장소 경로 불일치
3. `SELECT count(*) as count` 등 내장 함수 키워드가 `AS` 별칭으로 올 때 뒤따르는 `FROM` 절 파싱 유실 (`table "" not found`)
4. XORM의 다중 괄호 조건문(`(repo_id=9) AND (name='main')`)에 대한 인덱스 바운드 파싱 오류로 인한 브랜치 조회 0건
5. XORM의 컬럼 메타데이터 복합 서브쿼리 캐싱 시 `sys.columns` 쿼리가 가상 인덱스 뷰 인터셉터에 오인 포획되어 컬레이션 NULL 스캔 에러 유발

본 문서는 **"기존의 광범위한 테스트망과 벤치마크 스위트가 왜 이 결함들을 사전에 걸러내지 못했는가?"**에 대한 기술적 근본 원인을 투명하게 규명하고, **"인간이 작성한 정규 SQL"과 "ORM이 기계적으로 쏟아붓는 비정규 SQL" 사이의 구조적 간극**을 분석하여 영구적인 재발 방지 대책을 수립한다.

---

## 2. 핵심 결함별 근본 원인 및 테스트 사각지대 (Blind Spot) 분석

### 2.1. `UPDATE ... FROM <table> WHERE` 구문 파싱 실패

#### 1) 근본 원인 (Root Cause)
- **ANSI SQL 표준**: `UPDATE <table> SET col = val WHERE <condition>` 구조만 정의하며, `FROM` 절을 허용하지 않는다.
- **T-SQL(SQL Server) / Sybase 문법**: `UPDATE <table> SET col = val FROM <source_table_or_join> WHERE <condition>` 구문을 기본 지원한다.
- LionSQL의 VDBE 컴파일러(`pkg/proc/parser_dml.go`)와 폴백 DML 파서(`pkg/engine/dml_parser.go`)는 ANSI 표준에 맞춰 `UPDATE <table> SET <assignments> [WHERE <cond>]` 형태로 구현되어 있었다.
- XORM의 MSSQL 방언 빌더는 단일 테이블 업데이트임에도 불구하고 기계적으로 무조건 `FROM [table]`을 덧붙인 쿼리를 생성했다:
  ```sql
  UPDATE [issue] SET [is_closed] = ?, [closed_unix] = ?, [updated_unix] = ? 
  FROM [issue] 
  WHERE (is_closed = ?) AND [id]=?
  ```
- 이로 인해 `FROM [issue]` 토큰이 `WHERE` 이전의 `SET` 절 표현식 끝에 강제로 딸려 들어가 컬럼 매핑이 깨지거나 구문 오류가 발생했고, 결과적으로 `RowsAffected = 0`이 되어 Gitea의 낙관적 락 검증(`cnt != 1`)에 걸려 `the issue is already changed` 500 에러를 유발했다.

#### 2) 왜 이전 테스트에서 걸러지지 못했는가? (Blind Spot)
- 기존 TPC-C, SSB 및 내부 DML 회귀 테스트(`sqlexec_dml_test.go`, `runtime_test.go`)의 모든 `UPDATE` 쿼리는 엔지니어가 수동으로 작성한 표준 ANSI SQL 형태였다:
  ```sql
  UPDATE accounts SET balance = balance - 100 WHERE id = 1;
  UPDATE bmsql_warehouse SET w_ytd = w_ytd + 1 WHERE w_id = 1;
  ```
- 인간 엔지니어는 조인이 필요 없는 단순 업데이트문에 굳이 불필요한 `FROM` 절을 중복 작성하지 않는다. 따라서 "단일 테이블 갱신에 `FROM` 절을 기계적으로 부착하는 ORM 고유의 코드 생성 패턴"이 테스트망의 완전한 사각지대로 남아있었다.

---

### 2.2. 테이블 한정 와일드카드 (`SELECT repository.*`) 컬럼 덮어쓰기

#### 1) 근본 원인 (Root Cause)
- LionSQL 실행 엔진의 프로젝션 레이어(`pkg/execution/project.go`)는 와일드카드(`*`)가 포함되면 단순 불리언 플래그 `isSelectAll = true`로 처리했다.
- 이로 인해 `SELECT repository.* FROM [repository] INNER JOIN [user]` 구문에서 `repository` 테이블의 컬럼뿐만 아니라 조인된 `user` 테이블의 모든 컬럼까지 함께 튜플에 방출했다.
- `repository` 테이블(`name = 'lion-docs'`)과 `user` 테이블(`name = 'lionsql-core'`) 모두 `name`이라는 동일한 컬럼명을 가지고 있었고, 튜플 맵 구성 과정에서 뒤이어 처리된 `user.name`이 `repository.name`을 덮어썼다.
- 그 결과 Gitea는 디스크의 실제 경로인 `/data/git/repositories/lionsql-core/lion-docs.git` 대신 잘못 덮어씌워진 `/data/git/repositories/lionsql-core/lionsql-core.git`을 열려고 시도하여 `no such file or directory` 에러를 내며 Pre-receive Hook 단계에서 Git Push를 거부했다.

#### 2) 왜 이전 테스트에서 걸러지지 못했는가? (Blind Spot)
- 기존 조인 테스트는 `SELECT u.id, o.amount FROM users u JOIN orders o`처럼 명시적 컬럼을 프로젝션하거나, `SELECT * FROM users u JOIN orders o`처럼 모든 컬럼을 조회하는 경우에 집중되었다.
- 또한 기존 테스트 스키마들은 `users.name`, `orders.amount`처럼 테이블 간 주요 컬럼명이 서로 중복되지 않도록 설계되어 있었다.
- "테이블 한정 와일드카드(`table.*`)를 사용하면서, 조인 대상 테이블 간에 동명 컬럼이 충돌하는 시나리오"를 검증하는 테스트 케이스가 누락되어 있었다.

---

### 2.3. 내장 함수명과 동일한 식별자 별칭 (`SELECT count(*) as count`)

#### 1) 근본 원인 (Root Cause)
- 토크나이저/렉서(`pkg/proc/lexer.go`) 수준에서 `count`, `sum`, `max` 등은 일반 식별자(`TokenIdent`)가 아니라 전용 내장 함수 키워드(`TokenCount`, `TokenSum`)로 어휘 분석된다.
- 파서(`pkg/proc/parser_dml.go`)의 프로젝션 파싱 루프는 `AS` 키워드를 만난 뒤 별칭 식별자로 오직 `TokenIdent`와 `TokenString`만을 엄격하게 기대했다:
  ```go
  // 결함 코드: TokenCount 등 키워드를 별칭으로 인정하지 않음
  if p.curTokenIs(TokenAs) {
      p.nextToken()
      if p.curTokenIs(TokenIdent) || p.curTokenIs(TokenString) {
          alias = p.curToken.Literal
          p.nextToken()
      }
  }
  ```
- Gitea가 실행한 `SELECT issue_id, count(*) as count FROM [comment] ...` 쿼리에서 `as count`의 `count`는 `TokenCount`였기 때문에 `if` 조건을 통과하지 못했고, 토큰이 소비되지 않은 채 멈춰 섰다.
- 그 결과 파서 루프는 `count` 토큰에 머무른 채 종료되었고, 뒤이어 나오는 `if p.curTokenIs(TokenFrom)` 검사에서 현재 토큰이 `FROM`이 아닌 `count`였기 때문에 **`FROM` 절 파싱 전체가 완전히 누락(Skip)**되었다.
- `FROM` 절이 증발하여 AST의 테이블명이 빈 문자열(`""`)로 설정되었고, 플래너 실행 시 `table "" not found: table not found:` 에러로 직결되었다.

#### 2) 왜 이전 테스트에서 걸러지지 못했는가? (Blind Spot)
- 인간 개발자가 테스트 쿼리를 작성할 때는 관습적으로 `SELECT count(*) AS cnt`, `SELECT count(*) AS total_count`, `SELECT count(*) AS c`처럼 축약어 식별자를 별칭으로 부여한다.
- 함수명과 완전히 일치하는 키워드(`AS count`)를 별칭으로 부여하는 것은 인간 관점에서는 불필요하거나 어색한 패턴이지만, ORM 코드 생성기는 모델의 속성 이름에 맞춰 기계적으로 동일한 이름을 별칭으로 지정한다.
- 키워드를 식별자 위치에서 완화(Soft Keyword / Contextual Keyword)하는 처리가 미흡했던 점이 사각지대였다.

---

### 2.4. 복합 인덱스 바운드 파싱 다중 괄호 함정 (`strings.Trim(s, "()")`)

#### 1) 근본 원인 (Root Cause)
- Go 표준 라이브러리의 `strings.Trim(s, cutset)`은 문자열 양쪽 끝에서 `cutset`에 포함된 **모든 개별 문자**를 반복 제거한다.
- XORM의 SQL 빌더는 모든 논리 조건을 개별 괄호로 엄격하게 감싸서 `(repo_id = 9) AND (name = 'main')` 형태로 쿼리를 생성했다.
- 인덱스 바운드 정규화기(`pkg/planner/bounds.go`)가 최외곽 괄호를 벗겨내기 위해 `strings.Trim(cond, "()")`를 호출하자 다음과 같은 파괴적인 변형이 발생했다:
  ```go
  // 입력:
  "(repo_id=9) AND (name='main')"

  // strings.Trim(cond, "()") 실행 결과:
  "repo_id=9) AND (name='main'" // 최좌측 '('와 최우측 ')'만 깎여 괄호 짝이 어긋남!
  ```
- 짝이 어긋난 문자열로 인해 정규표현식 매칭이 완전히 실패했고, 바운드 조건이 누락되면서 인덱스 스캔 결과가 항상 0건으로 반환되어 브랜치가 존재함에도 `branch does not exist` 에러가 발생했다.

#### 2) 왜 이전 테스트에서 걸러지지 못했는가? (Blind Spot)
- 기존 복합 인덱스 단위 테스트는 `WHERE a = 1 AND b = 2` (괄호 전혀 없음) 또는 `WHERE (a = 1)` (단일 조건 단일 괄호) 케이스만 검증했다.
- 단일 괄호 문자열 `(a = 1)`에 대해서는 `strings.Trim`이 `a = 1`을 올바르게 반환하기 때문에, 함수 자체에 잠재된 결함이 기존 테스트에서는 전혀 드러나지 않았다.

---

### 2.5. 가상 시스템 뷰(`sys.columns`) 인터셉터 오인 라우팅

#### 1) 근본 원인 (Root Cause)
- LionSQL의 가상 뷰 인터셉터(`pkg/catalog/virtual/interceptor.go`)는 XORM의 특화된 인덱스 인트로스펙션 쿼리를 가로채기 위해 다음 조건을 검사했다:
  ```go
  strings.Contains(upper, "SYS.INDEXES") && 
  strings.Contains(upper, "SYS.INDEX_COLUMNS") && 
  strings.Contains(upper, "SYS.COLUMNS")
  ```
- 그러나 Gitea XORM은 테이블 컬럼 메타데이터를 인덱스와 별도로 캐싱하기 위해 `FROM sys.columns` 단독 쿼리 내부에 인덱스 관련 서브쿼리를 복합 중첩시켰다.
- 인터셉터 조건에 `!fromColumns` 가드가 누락되어 있어, 단순 컬럼 조회 쿼리가 인덱스 조회 쿼리로 오인되어 가상 인덱스 결과셋을 반환했고, 누락된 `collation_name` 필드로 인해 드라이버 레벨에서 `converting NULL to string is unsupported` 에러가 터졌다.

#### 2) 왜 이전 테스트에서 걸러지지 못했는가? (Blind Spot)
- 기존 SSMS, DBeaver, Beekeeper Studio 연동 테스트는 각 도구가 사용하는 고유의 시스템 쿼리 템플릿(DBeaver 스타일 메타데이터 쿼리 등)을 기준으로 검증되었다.
- Go 기반 오픈소스 생태계에서 가장 널리 쓰이는 XORM 특유의 시스템 카탈로그 질의 패턴을 직접 주입해 보지 않았기 때문에 사전에 발견되지 못했다.

---

## 3. "인간 작성 SQL"과 "ORM 기계 생성 SQL"의 구조적 간극

이번 연동 실패와 사후 분석을 통해 도출된 핵심 결론은 **"인간이 작성하는 SQL과 ORM이 기계적으로 생성하는 SQL 사이에는 극단적인 패턴 격차가 존재한다"**는 사실이다.

| 구분 | 인간 엔지니어 작성 SQL (기존 테스트) | ORM 기계 생성 SQL (Gitea / XORM 실전) |
| :--- | :--- | :--- |
| **식별자 표기** | `users`, `orders`, `balance` (일반 텍스트) | `[user]`, `[order]`, `[balance]` (모든 토큰에 무조건 브래킷) |
| **DML 구문 구조** | `UPDATE t SET a = 1 WHERE id = 2;` (표준 ANSI) | `UPDATE [t] SET [a] = ? FROM [t] WHERE ([id] = ?)` (무조건 FROM 포함) |
| **조건절 괄호** | `WHERE a = 1 AND b = 2` (최소한의 괄호) | `WHERE ((([a] = ?) AND ([b] = ?)))` (극단적인 다중 중첩 괄호) |
| **프로젝션 와일드카드** | `SELECT *` 또는 `SELECT a.id, b.name` | `SELECT repository.*` (테이블 한정 와일드카드 다용) |
| **식별자 별칭** | `AS cnt`, `AS total`, `AS score` (비예약어 고유명사) | `AS count`, `AS user`, `AS order` (키워드/함수명과 동일한 별칭 다용) |
| **시스템 카탈로그 질의** | 정형화된 ANSI `INFORMATION_SCHEMA` 뷰 조회 | 벤더 독자 시스템 테이블(`sys.columns`, `sys.indexes`) 복합 서브쿼리 조인 |

기존의 성능 벤치마크(TPC-C, SSB)와 단위 테스트는 첫 번째 열(인간 작성 SQL)에 100% 최적화되어 있었기 때문에, 두 번째 열(ORM 기계 생성 SQL)이 LionSQL 커널에 진입했을 때 파서, 플래너, 실행기의 경계 조건들이 연쇄적으로 붕괴했던 것이다.

---

## 4. 영구 재발 방지 대책 및 엔지니어링 표준

### 4.1. ORM 자동 생성 쿼리 전용 회귀 테스트 하네스 구축
- 수동 작성 쿼리 기반 단위 테스트에 의존하지 않고, 실제 프로덕션 ORM(XORM, GORM, Hibernate, Prisma)이 생성한 실제 SQL 트레이스 로그를 그대로 리플레이하는 회귀 테스트 패키지(`pkg/engine/orm_compat_test.go`)를 상시 운영한다.
- Gitea 전체 시딩 파이프라인(`scripts/seed_gitea_mock_data.go`)을 일회성 스크립트가 아닌, CI 릴리스 파이프라인의 필수 통합 테스트 게이트로 영구 편입한다.

### 4.2. 표준 라이브러리 단순 문자열 함수 사용 금지 (Strict Syntax Invariant)
- SQL 파싱, 바운드 추출, 조건 정규화 영역에서 `strings.Trim(s, "()")`, `strings.Split(s, ",")`, `strings.Index(s, "WHERE")`와 같이 괄호 깊이나 따옴표 이스케이프를 무시하는 단순 표준 라이브러리 함수의 사용을 엄격히 금지한다.
- 괄호 중첩 레벨(`depth == 0`)과 따옴표 영역을 완벽하게 추적하는 검증된 유틸리티 함수(`sqlutil.FindTopLevelKeyword`, `stripEnclosingParens`)만 사용하도록 코드 리뷰 및 아키텍처 규칙을 강제한다.

### 4.3. Soft / Contextual Keyword 파서 표준화
- `AS <alias>` 뒤나 테이블 별칭 위치에서는 `TokenFrom`, `TokenWhere`, `TokenSemicolon` 등 절(Clause)의 시작을 알리는 구분용 예약어를 제외한 모든 토큰(`TokenCount`, `TokenSum`, `TokenUser` 등)을 유연하게 식별자로 허용(`!isReservedAfterProjection`)하는 Soft Keyword 표준을 파서 전반에 확장 적용한다.

### 4.4. 조인 프로젝션 테이블 한정 격리 보장
- `SELECT *`와 `SELECT table.*`를 엄격히 분리하여, 테이블 한정자가 붙은 와일드카드는 해당 테이블의 스키마 컬럼 슬라이스만 선별 방출(`ColumnIndexMap.tableCols`)함으로써 조인 테이블 간 동명 컬럼 충돌로 인한 덮어쓰기 오염을 원천 차단한다.

---

## 5. 부록: PostgreSQL Wire Protocol (PGWire :5433) 실전 연동 사후 분석

TDS(`:1433`) 프로토콜을 통한 Gitea XORM 연동 안정화 이후, 동일한 Gitea 애플리케이션의 데이터베이스 백엔드를 **PostgreSQL Wire Protocol v3 (`DB_TYPE = postgres`, TCP `:5433`)**로 전환하여 검증하는 과정에서 노출된 2차 사각지대와 프로토콜 레벨 결함 분석이다.

### 5.1. 비밀번호 해시 속 `$5` 오치환 및 인증 런타임 패닉 (Parameter Substitution Corruption)

#### 1) 현상 및 근본 원인 (Root Cause)
- **현상**: 관리자 계정 `bob` 생성 후 Web UI 및 API 로그인 시 Gitea 프로세스가 `runtime error: invalid memory address or nil pointer dereference`를 내며 강제 종료(SIGSEGV).
- **근본 원인**:
  - Gitea는 기본 비밀번호 해싱 알고리즘으로 PBKDF2를 사용하며, 비밀번호 해시 알고리즘 식별 문자열을 `pbkdf2$50000$50` 형태로 DB에 보관한다.
  - LionSQL PGWire 서버(`pkg/wire/server.go`)의 파라미터 치환기(`substituteParams`)는 전통적인 순차 `strings.ReplaceAll(res, fmt.Sprintf("$%d", i+1), formatted)`을 사용했다:
    ```go
    // 결함 코드: 문자열 리터럴 영역을 무시하고 전체 쿼리에서 단순 치환
    for i := len(params) - 1; i >= 0; i-- {
        res = strings.ReplaceAll(res, fmt.Sprintf("$%d", i+1), formattedParams[i])
    }
    ```
  - Gitea가 `INSERT INTO "user" (..., passwd_hash_algo, is_admin, ...) VALUES (..., $8, $26, ...)` 쿼리를 실행했을 때, 파라미터 $8로 전달된 `'pbkdf2$50000$50'` 문자열이 먼저 쿼리 본문에 주입되었다.
  - 뒤이어 5번째 파라미터(`$5`, `keep_email_private = false`)를 치환하는 루프가 돌면서, **이미 주입된 비밀번호 해시 문자열 내부의 `$5`를 탐지하여 `false`로 치환**해 버렸다:
    ```sql
    -- 오염된 결과: $5가 치환되어 따옴표와 불리언이 뒤섞임
    'pbkdf2'false'0000'false'0'
    ```
  - 인증 시 Gitea가 이 오염된 문자열을 읽어 `hash.Parse(u.PasswdHashAlgo)`를 호출했고, 파서가 알고리즘을 인식하지 못해 `nil`을 반환했으나 Gitea 내부에서 `nil` 검사 없이 역참조하여 패닉이 발생했다.
- **테스트 사각지대 (Blind Spot)**:
  - 기존 단위 테스트의 파라미터 값들은 `100`, `'Alice'`, `'2026-01-01'`처럼 내부에 달러 기호(`$`)를 포함하지 않는 일반 텍스트/숫자 위주였다.
  - 달러 기호(`$`)가 파라미터 치환 문법인 PostgreSQL 프로토콜에서, 데이터 값 자체에 `$1`, `$2`와 유사한 패턴(예: PBKDF2/Argon2 해시, 정규표현식, 통화 기호, 쉘 스크립트)이 포함될 수 있다는 점을 간과했다.
- **해결 방안 (Defensive Architecture)**:
  - 문자열 리터럴(`'...'`), 라인 주석(`-- ...`), 블록 주석(`/* ... */`)의 시작과 끝을 완벽하게 추적하는 **단일 패스(Single-Pass) 인용구 인식 스캐너**를 구현하여 문자열 내부의 `$` 기호는 파라미터 치환 대상에서 완전히 격리했다.

---

### 5.2. Untyped 파라미터(OID 0)와 스토리지 레코드 강제 변환 결함 (모든 숫자 컬럼 0 초기화)

#### 1) 현상 및 근본 원인 (Root Cause)
- **현상**: Gitea 조직(`lionsql-core`) 생성 후 저장소 생성 API(`POST /org/lionsql-core/repos`)를 호출하면 조직을 찾을 수 없다며 `404 Not Found` 에러 반환.
- **근본 원인**:
  - Gitea는 조직 조회를 위해 `SELECT ... FROM "user" WHERE "type" = 1 AND lower_name = 'lionsql-core'`를 실행한다. (Gitea 데이터 모델에서 일반 사용자는 `type = 0`, 조직은 `type = 1`).
  - 그러나 실제 LionSQL DB에 저장된 `lionsql-core` 레코드를 확인한 결과, `type: 0`, `created_unix: 0`, `updated_unix: 0`, `max_repo_creation: 0` 등 **모든 정수형 컬럼이 모조리 0으로 저장**되어 있었다.
  - 연쇄 원인은 두 레이어에 걸쳐 존재했다:
    1. **드라이버 레벨 OID 0 (Untyped)**: Go 표준 드라이버(`lib/pq`)는 파라미터화된 쿼리 실행 시 각 파라미터의 OID를 명시하지 않고 기본값 `0`으로 전송한다. LionSQL의 `decodeTextParamValue`는 `oid == 0`일 때 값을 무조건 문자열(`storage.StringValue`)로 처리했고, `formatParamForSQL`은 이를 인용된 문자열(`'1'`, `'1793321845'`)로 쿼리에 주입했다.
    2. **리터럴 파서의 정수 강제 변환 누락**: 쿼리 실행 엔진의 `ParseLiteralDefault(s, targetType)`는 `s`가 `'1'`처럼 따옴표로 감싸진 문자열이면, 대상 컬럼의 타입(`targetType == TypeInt`)을 무시하고 무조건 `StringValue("1")`를 반환했다:
       ```go
       // 결함 코드: targetType이 TypeInt임에도 따옴표가 있으면 StringValue 반환
       if isNStr || isRegularStr {
           ...
           if targetType == TypeJSON { return JSONValue(strVal) }
           if targetType == TypeUUID { return UUIDValue(strVal) }
           return StringValue(strVal) // TypeInt, TypeFloat 누락!
       }
       ```
    3. 스토리지 엔진(WiredTiger / MemEngine) 레코드 인코더는 컬럼 정의가 `TypeInt`이면 레코드 값의 `IVal` 필드를 직접 읽어 직렬화하는데, `Type`이 `TypeVarChar`였기 때문에 `IVal`의 기본 제로값인 `0`이 저장되었다.
- **테스트 사각지대 (Blind Spot)**:
  - 정수형 컬럼에 숫자를 넣을 때 인간은 `INSERT INTO t (val) VALUES (1)`처럼 따옴표 없이 숫자를 작성한다.
  - 그러나 ORM과 PGWire 드라이버의 Extended Query 바인딩 경로를 거치면 모든 파라미터가 텍스트 포맷으로 전달되고, 파라미터 치환 과정에서 인용 문자열(`'1'`)로 변환될 수 있다. SQL 표준상 `INT` 컬럼에 `'123'` 문자열 리터럴을 대입하는 것은 유효한 묵시적 형변환이어야 함에도 이를 누락했다.
- **해결 방안 (Defensive Architecture)**:
  - `decodeTextParamValue`에서 `oid == 0`인 경우 문자열이 순수 정수(`strconv.ParseInt`) 또는 부동소수점이면 즉시 `IntValue`/`FloatValue`로 디코딩하도록 보강.
  - `ParseLiteralDefault`에서 인용된 문자열(`'...'`)이라 하더라도 `targetType`이 `TypeInt`, `TypeFloat`, `TypeDecimal`인 경우 문자열 내부 값을 정수/실수로 파싱하여 대상 타입의 `storage.Value`로 반환하도록 완전화.

---

### 5.3. DML `RETURNING`과 PGWire Describe 메시지 불일치

#### 1) 현상 및 근본 원인 (Root Cause)
- **현상**: Gitea XORM의 테이블 초기화 중 `sql: expected 0 destination arguments in Scan, not 1` 에러와 함께 기동 실패.
- **근본 원인**:
  - Gitea는 기본 키 자동 생성 후 ID를 가져오기 위해 PostgreSQL 고유의 `INSERT INTO <table> (...) VALUES (...) RETURNING "id"` 구문을 실행한다.
  - Go 드라이버(`lib/pq`)는 실행 전 Describe 단계를 통해 결과셋의 스키마를 요구한다.
  - 당시 LionSQL PGWire 서버는 단순 `INSERT`로 인식하여 결과 컬럼이 없다는 의미의 `WriteNoData` 메시지를 응답했다. 드라이버는 반환 컬럼이 0개라고 기대했으나, 실제 실행 후 1개의 ID 행이 반환되어 `Scan(&id)` 단계에서 컬럼 수 불일치 에러를 발생시켰다.
- **해결 방안 (Defensive Architecture)**:
  - DML 파서에 `RETURNING` 구문 파싱을 공식 추가하고, PGWire `writeSchemaDescription`에서 `RETURNING` 절이 감지되면 대상 테이블의 스키마를 조회하여 정확한 컬럼 메타데이터(`WriteRowDescription`)를 사전 방출하도록 구현.

---

### 5.4. 요약 및 멀티 와이어 통합 교훈

| 문제 영역 | 현상 | 근본 원인 | 재발 방지 엔지니어링 표준 |
| :--- | :--- | :--- | :--- |
| **파라미터 치환** | 비밀번호 해시 파손 및 로그인 패닉 | `strings.ReplaceAll`이 문자열 리터럴 내부의 `$5`를 탐지 | 단일 패스 인용구/주석 인식 파라미터 스캐너 필수화 |
| **타입 변환** | 조직 `type=1` 및 생성 시각이 `0`으로 저장 | Untyped 파라미터의 문자열 래핑 및 리터럴 파서의 정수 변환 누락 | `ParseLiteralDefault`의 문자열 ➔ 숫자 묵시적 강제 변환 보장 |
| **프로토콜 Describe** | `RETURNING` 실행 시 `Scan` 목적지 인자 불일치 | `Describe` 단계에서 `RETURNING` 컬럼 메타데이터 대신 `NoData` 방출 | DML `RETURNING` 감지 시 즉시 `RowDescription` 방출 파이프라인 수립 |
| **카탈로그 인터셉터** | 외래키 조회와 컬럼 메타데이터 조회의 충돌 | 복합 조인 쿼리의 시스템 뷰 이름 단순 부분 문자열 매칭 | `SRC_TBL`, `CONTYPE = 'F'` 등 엄격한 WHERE 조건 가드 적용 |

어제의 TDS와 오늘의 PGWire 연동을 거치며 입증되었듯이, **드라이버와 ORM은 인간 프로그래머가 절대로 작성하지 않을 비정형적이고 극단적인 경로(달러 기호 포함 리터럴, Untyped 문자열 파라미터 바인딩, 무조건적 괄호 감싸기)를 끊임없이 경유**한다. LionSQL은 이러한 실제 프로덕션 워크로드를 방어벽으로 흡수함으로써 진정한 멀티 와이어 엔터프라이즈 데이터베이스 엔진으로 완성되었다.
