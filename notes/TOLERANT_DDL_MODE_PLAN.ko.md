# LionSQL Tolerant DDL Mode & Catalog Refactoring Architecture Plan (`TOLERANT_DDL_MODE_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-05
> - **상태**: 승인 완료 (1.0 호환성 및 클린 리팩토링 로드맵: `v1.0.0-rc` / `v1.1.0`)
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

### 2.1 서버 옵션 격리 원칙 (`[RULE:SafeDefaults]`)
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
