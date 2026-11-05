# LionSQL Tolerant DDL Mode & Catalog Refactoring Architecture Plan (`TOLERANT_DDL_MODE_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-05
> - **상태**: 구현 및 3단계 파이프라인 검증 완료 (`DONE`)
> - **관련 과제**: `FEAT-TOLERANT-DDL-1` (Tolerant DDL Bootstrap Mode & Ad-hoc DDL Cleanup), `REFACTOR-CATALOG-INTERCEPTOR-1` (Table-Driven Catalog Interceptor Cleanup)
> - **핵심 불변식**: `[RULE:SafeDefaults]` (안전한 기본값), `[RULE:RadicalKISS]` (KISS 및 Deletion-First), `[RULE:ExplicitError]` (명시적 에러 가시성)

---

## 1. 개요 및 배경 (Context & Problem)

LionSQL은 10개 실전 오픈소스 애플리케이션을 외부 코드 수정 없이(`Zero Code Change`) 연동하는 과정에서 뛰어난 호환성을 입증했으나, 점진적 패치로 인해 다음과 같은 **2대 아키텍처 부채**가 발생했다:

1. **`sqlexec_ddl.go` 전반의 개별 DDL 땜질 코드 분산**:
   - 특정 ORM(Knex, GORM, XORM)의 비정규 DDL을 수용하기 위해 `if strings.Contains(err.Error(), "already exists") { continue }` 같은 개별 예외 처리 분기가 분산됨.
   - NocoDB 등 신규 앱 부트스트랩 시 `DROP INDEX` 비존재 인덱스 예외로 인해 설치가 중단되는 문제 발생.
2. **`pkg/catalog/virtual/interceptor.go`의 휴리스틱 순서 의존성(Order Sensitivity)**:
   - 수십 개의 `strings.Contains`, `containsEq`, `matchAny` IF문이 순차적으로 나열되어 있어, `pg_attribute` 검사가 `pg_index`보다 위에 위치하여 Directus 등의 복합 쿼리가 엉뚱한 핸들러로 라우팅되는 부작용 초래.

---

## 2. 해결 방안: "Tolerant DDL 서버 옵션" 기반 통합

### 2.1 서버 옵션 격리 원칙 및 기본값(Default = false) 확정 사유 (`[RULE:SafeDefaults]`)

> [!IMPORTANT]
> **왜 Tolerant DDL의 기본값은 반드시 `false` (Strict Mode)여야 하는가?**
>
> 1. **오타 및 실수 은폐 방지 (`[RULE:ExplicitError]`, 명시적 에러 가시성)**:
>    - 만약 관용 모드가 기본값(`true`)이라면, 사용자가 중요한 인덱스나 테이블을 삭제할 때 오타를 내도(예: `DROP INDEX idx_user_emial` ➔ `email`의 오타) 시스템이 "에러 없음(성공)"으로 무음 처리해 버린다.
>    - 사용자는 실제 인덱스(`idx_user_email`)가 삭제된 줄 알고 넘어가지만, 실제로는 인덱스가 여전히 디스크와 메모리를 차지하며 불필요한 인덱스 유지 오버헤드와 리소스 누수를 일으킨다.
> 2. **RDBMS의 데이터 무결성 및 엄격성 훼손 차단**:
>    - RDBMS에서 존재하지 않는 객체 조작이나 중복 생성에 에러를 반환하는 것은 데이터 무결성을 보호하는 핵심 방어선이다.
>    - 편의를 이유로 기본값을 관용(`tolerant`)으로 두는 것은 MySQL의 과거 `0000-00-00` 허용이나 비엄격 모드처럼 데이터 무결성을 파괴하는 안티패턴이다.
> 3. **서드파티 부트스트랩 특화 격리 (Transient Bootstrap Only)**:
>    - NocoDB나 Knex 등 일부 프레임워크가 부트스트랩 과정에서 `IF EXISTS` 없이 비존재 인덱스를 지우는 것은 해당 프레임워크의 비정규 마이그레이션 결함이다.
>    - 따라서 이는 엔진 전체의 기본 동작을 바꿀 사안이 아니며, **최초 설치/마이그레이션 시점에만 일시적으로 `lionsql set tolerant_ddl on`으로 켰다가 설치 완료 즉시 `off`로 복귀하는 격리된 도구**로 제공되는 것이 가장 안전하고 올바른 아키텍처다.

- **기본 모드 (`--tolerant-ddl=false`, Default)**:
  - 엄격한 ANSI/PostgreSQL 표준 에러 반환 (`[RULE:ExplicitError]`).
  - 사용자의 오타나 의도치 않은 비존재 객체 삭제(`DROP TABLE/INDEX`) 시 명확한 에러를 발생시켜 데이터 무결성 보장.
- **관용 모드 (`--tolerant-ddl=true` 또는 `LIONSQL_TOLERANT_DDL=true`)**:
  - 서드파티 앱 부트스트랩, 마이그레이션 툴 실행, 덤프 복원 시 명시적으로 활성화.
  - 선언적 멱등성(Idempotency)에 따라 **"이미 원하는 최종 상태(Desired State)에 도달해 있는 예외"**를 에러 대신 `NOTICE` 로깅 후 무음 성공 처리.

### 2.2 관용 DDL 대상 스펙 (Tolerant DDL Specification)
1. **`DROP INDEX <name>`**:
   - 인덱스가 대상 테이블에 없어도 `NOTICE: index "..." does not exist, skipping` 반환 후 성공.
2. **`DROP CONSTRAINT / FOREIGN KEY <name>`**:
   - 외래키나 제약조건이 없어도 성공 처리.
3. **`CREATE TABLE <name>` (IF NOT EXISTS 누락 시)**:
   - 이미 동일한 스키마의 테이블이 존재하면 경고 후 성공 처리.
4. **`ALTER TABLE ADD COLUMN <col>` (IF NOT EXISTS 누락 시)**:
   - 동일 이름의 컬럼이 이미 존재하면 성공 처리.
5. **`ALTER TABLE tbl SET (...)`**:
   - PostgreSQL 전용 스토리지 파라미터(autovacuum 팩터 등)를 No-Op 성공 처리.

### 2.3 무중단 런타임 동적 제어: CLI 인터페이스 (`lionsql get` & `lionsql set`)
서버를 재시작하지 않고도 운영 중인 인스턴스의 런타임 상태를 조회하고 안전하게 전환할 수 있도록, 상태 대칭성(State Symmetry)을 갖춘 **조회(`get`) 및 sudo 스타일 인터랙티브 인증 제어(`set`) 한 쌍의 CLI 명령**을 제공한다:

1. **상태 조회 명령 (`lionsql get [key]`)**:
   - 실행 중인 인스턴스의 런타임 파라미터를 읽기 전용(Read-Only)으로 무암호 조회:
     ```bash
     # 특정 키 조회
     ./bin/lionsql get tolerant_ddl [--addr=http://127.0.0.1:8090]
     # 결과: tolerant_ddl = off (default, strict)

     # 전체 동적 설정 테이블 조회
     ./bin/lionsql get
     # 결과:
     # KEY                     VALUE      TYPE     DESCRIPTION
     # tolerant_ddl            off        bool     Tolerant DDL bootstrap mode
     # log_level               info       string   Runtime logger verbosity
     # slow_query_threshold    0ms        duration Slow query detection threshold
     # read_only               off        bool     Emergency read-only safe mode
     ```

2. **상태 변경 명령 (`lionsql set <key> <value>`)**:
   - 시스템 상태를 변조하므로 Unix의 `sudo`처럼 **실행 즉시 관리자 암호를 인터랙티브하게 검증** (`term.ReadPassword` 사용):
     ```bash
     ./bin/lionsql set tolerant_ddl on
     [sudo] Password for admin@lionsql.local: ********
     OK: tolerant_ddl set to 'on' (active on PID 93535)
     ```
   - 스크립트/CI 환경 지원을 위해 `LIONSQL_ADMIN_PASS` 환경변수 또는 표준 입력 토큰을 수용 (`[RULE:ExecveSafety]`, `[RULE:HeadlessParity]`).

3. **순수 인메모리 휘발성 보장 (100% Volatile In-Memory & Safe Defaults)**:
   - **`--persist` 안티패턴 배제**: 디스크 설정 파일(`lionsql.json`) 변조로 인한 주석 유실, 포맷 파괴, GitOps/IaC 설정 드리프트 및 권한 충돌을 원천 차단하기 위해 디스크 자동 덮어쓰기 기능은 명시적으로 제외한다 (`[RULE:RadicalKISS]`).
   - 실행 중인 LionSQL HTTP 관리 엔드포인트(`POST /api/admin/config`)로 인증 요청을 전송하여, 인스턴스 내부 `atomic.Bool` / `atomic.Pointer` 플래그만 원자적으로 갱신한다 (`[RULE:AtomicFlags]`).
   - 프로세스가 재시작되면 본래의 안전한 기본값(`false`) 또는 정적 설정 파일의 상태로 자동 복귀하여 의도치 않은 관용 모드 방치 사고를 원천 방지한다 (`[RULE:SafeDefaults]`).
   - 영구적으로 기본 설정을 변경해야 하는 환경은 사용자가 직접 `lionsql.json`을 편집하거나 시작 플래그(`--tolerant-ddl`)로 지정하는 정석적인 방식을 따른다.

---

## 3. 코드베이스 대청소 (Deletion-First Refactoring)

### 3.1 `sqlexec_ddl.go` 분산 땜질 삭제
- `sqlexec_ddl.go`에 흩어져 있던 ad-hoc 분기들을 전면 제거하고, DDL 실행 종료 시점에 단일 인터셉터(`applyTolerantDDL(err)`)로 일원화:
  ```go
  if err != nil {
      if s.config.TolerantDDL && isTolerantDDLError(err) {
          s.logger.Warn("Tolerant DDL notice: %v (treated as success)", err)
          return &QueryResult{Success: true, Command: cmd, Messages: []string{err.Error()}}, nil
      }
      return nil, err
  }
  ```

### 3.2 `pkg/catalog/virtual/interceptor.go` Table-Driven 개편
- `interceptor.go`의 330여 줄에 달하는 순서 의존적 `if strings.Contains(...)` 체인을 제거.
- `COMPAT-TABLE-DRIVEN-1` 설계에 맞춰 **`CatalogInterceptorTable` 구조체 기반 O(1) 정적 디스패치 테이블**로 전면 전환.
- 쿼리 특성(대상 테이블 집합 `qc.TargetTables` 및 핵심 키워드 조합)을 해시 키로 매핑하여 호출 순서에 따른 라우팅 오류를 원천 박멸.

---

## 4. 단계별 실행 계획

```
[ Phase 1: Tolerant DDL 옵션 구현 ] ───► [ Phase 2: DDL 땜질 코드 Deletion ] ───► [ Phase 3: interceptor.go Table-Driven ]
  - CLI 플래그: --tolerant-ddl            - sqlexec_ddl.go ad-hoc 분기 삭제        - 정적 디스패치 테이블 전환
  - Config 및 Coordinator 통합            - applyTolerantDDL 단일화                  - Directus/NocoDB 쿼리 회귀 검증
```

1. **Phase 1: `--tolerant-ddl` 서버 옵션 파이프라인 구축 (`FEAT-TOLERANT-DDL-1`)**:
   - `pkg/config/config.go`에 `TolerantDDL bool` 필드 추가.
   - `cmd/lionsql/main.go`에 `-tolerant-ddl` 플래그 및 환경변수 바인딩.
2. **Phase 2: DDL 단일 관용 처리 및 기존 분산 땜질 삭제**:
   - `sqlexec_ddl.go` 내부의 개별 `already exists` 땜질 코드를 정리하고 단일 관용 검사기로 일원화.
   - NocoDB 마이그레이션(`DROP INDEX nc_fr_file_url`) 통과 검증.
3. **Phase 3: 가상 카탈로그 `interceptor.go` Table-Driven 리팩토링 (`REFACTOR-CATALOG-INTERCEPTOR-1`)**:
   - 순서 의존적 IF 블록을 제거하고 정적 매핑 테이블로 개편하여 Directus 스키마 인스펙션 쿼리 라우팅 정합성 확보.

---

## 5. 구현 및 검증 결과 보고서 (Implementation & Verification Complete)

### 5.1 `FEAT-TOLERANT-DDL-1` 구현 결과
1. **설정 파이프라인 및 안전한 기본값**:
   - `pkg/config/config.go`에 `TolerantDDL bool` 필드 추가 (기본값: `false`, Strict 모드 유지).
   - `--tolerant-ddl` 서버 플래그 및 `LIONSQL_TOLERANT_DDL` 환경변수 연동 완료.
2. **무중단 동적 제어 CLI (`lionsql get` & `lionsql set`)**:
   - `cmd/lionsql/cli_config.go`: 무암호 읽기 전용 `get`과 Unix `sudo` 스타일의 인터랙티브 터미널 암호 프롬프트(`term.ReadPassword`)를 탑재한 `set` 구현 완료.
   - 실행 중인 인스턴스의 `POST /api/config` 엔드포인트 호출을 통해 인메모리 `atomic.Bool`을 무중단 원자적 전환.
   - 디스크 설정 파일 변조 배제(`--persist` 제외)로 재시작 시 안전한 기본값 자동 복원 보장.
3. **DDL 단일 관용 처리 파이프라인**:
   - `sqlexec_ddl.go`: `DROP INDEX`, `DROP TABLE`, `DROP CONSTRAINT`, `DROP FOREIGN KEY`, `CREATE TABLE` 중복, `ALTER TABLE ADD COLUMN` 중복, `ALTER TABLE tbl SET (...)` 스토리지 옵션을 단일 `s.TolerantDDL()` 플래그로 일원화.

### 5.2 `REFACTOR-CATALOG-INTERCEPTOR-1` 구현 결과
1. **Table-Driven 정적 라우팅 테이블 (`RouteRule[]`) 전환**:
   - `pkg/catalog/virtual/interceptor.go` 내의 약 330라인에 달하던 순차적 `if strings.Contains(...)` 체인을 전면 삭제.
   - `catalogRouteTable ([]RouteRule)` 정적 테이블로 개편하여, 고유 시그니처 및 복합 조인(XORM GetIndexes, Primary Key, Foreign Key 등)이 우선 매칭되고 단일 테이블 조회가 후순위로 평가되도록 결정론적 디스패치 구조 확립.
2. **순서 의존성(Order Sensitivity) 원천 박멸**:
   - `pg_attribute` 조회가 `pg_index` 조회를 덮어쓰거나 가로채던 순서 버그 완전 해결.

### 5.3 3단계 파이프라인 검증 통과 내역
- **Stage 1 (Build)**: `bin/lionsql`, `bin/bench`, `bin/tpcc-runtime` 컴파일 완료.
- **Stage 2 (Test)**:
  - `pkg/engine/tolerant_ddl_test.go`: Strict vs Tolerant 모드 전환 및 비정규 DDL 허용 검증 통과.
  - `pkg/api/config_api_test.go`: HTTP REST 설정 API 권한 및 동적 토글 검증 통과.
  - `cmd/lionsql/cli_config_test.go`: CLI get/set 인터페이스 검증 통과.
  - `pkg/catalog/virtual`: 가상 카탈로그 스위트 100% PASS.
  - `pkg/engine`: 모든 외부 앱(Vaultwarden, Umami, Vikunja, WoodpeckerCI) 호환성 및 회귀 테스트 100% PASS (0 data race).
- **Stage 3 (Commit)**: `a4221dd` (`FEAT-TOLERANT-DDL-1`), `5d01dfa` (`REFACTOR-CATALOG-INTERCEPTOR-1`).
