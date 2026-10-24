# LionSQL Over-Specification Remediation Plan (`notes/OVER_SPECIFICATION_REMEDIATION_PLAN.md`)

## 1. 개요 및 추진 배경 (Overview & Problem Statement)

LionSQL은 단일 정적 바이너리로 다중 프로토콜(PGWire, TDS, HTTP)과 다양한 서드파티 GUI 도구(DBeaver, DbGate, Beekeeper Studio 등)를 지원하는 고동시성 OLTP 엔진입니다.

그러나 기능 확장 및 서드파티 도구 연동 과정에서 일부 영역에 **"표준적이고 단순한 프로토콜 불변식(위치 기반 폴백 등)을 신뢰하는 대신, 특정 도구의 겉모습이나 임의의 식별자/문자열을 예단하여 코드를 과도하게 제약(Over-Specification)하거나 도구 전용 땜질 코드를 삽입한 안티패턴"**이 누적되었습니다.

최근 발생한 `BUG-TDS-BEEKEEPER-RPC-STATEMENT-1` 장애(Beekeeper Studio의 `undefined.split` 크래시)는 이러한 오버스펙의 전형적인 결과였습니다. 이에 따라 코드베이스 전반의 오버스펙 및 도구 종속적 하드코딩을 체계적으로 전수 감사하고, **Golden Rule #15 (Radical KISS, DRY & Deletion-First)** 원칙에 따라 정돈하기 위한 마스터 플랜을 수립하고 즉시 실행합니다.

---

## 2. 3대 핵심 정돈 대상 및 작업 범위 (Target Scope)

### Phase 1: `pkg/engine/sqlexec_procs.go` T-SQL 파라미터 매핑 단순화
* **현상**:
  - `execSpExecuteSQL`에서 `case "@stmt", "@statement":`, `case "@params", "@parameters":`와 같이 특정 드라이버 전용 별칭을 스위치문에 하드코딩함.
  - `@sql = 'SELECT 1'`이나 `@query = '...'` 처럼 다른 명칭이 사용될 경우 `default`로 빠져 바인딩 변수로 오인되고 쿼리가 빈 문자열(`""`)로 유실됨.
* **정돈 방향**:
  - `@statement`, `@parameters` 등의 임의 별칭 하드코딩을 완전히 제거.
  - 공식 T-SQL 명칭인 `@stmt`, `@params`만 공식 이름으로 매핑.
  - 이름이 불일치하거나 생략된 경우, 오늘 TDS 레이어에서 검증된 것과 동일하게 **순수 위치 기반(0번째 인자 = statement, 1번째 인자 = paramsDeclaration)**으로 자연스럽게 매핑하도록 정돈.

---

### Phase 2: `pkg/wire/tds/server.go` (`matchVersionQuery`) 하이재킹 제거 및 정규 평가 일원화
* **현상**:
  - `matchVersionQuery`가 `strings.Count(upper, "SERVERPROPERTY") > 1` 조건을 만나면, 사용자가 무엇을 SELECT했든 상관없이 **무조건 4개 컬럼(`version`, `productVersion`, `engineEdition`, `versionText`)을 하드코딩하여 반환**함.
  - 실측 검증: `SELECT SERVERPROPERTY('ProductVersion') AS pv, SERVERPROPERTY('EngineEdition') AS ee` 쿼리를 보내 2개 컬럼을 요구했음에도, 하이재킹이 발동하여 요구하지도 않은 4개 컬럼이 강제 반환되는 결함 확인.
  - 정작 LionSQL의 내장 함수 엔진(`pkg/proc/builtins_sys.go`)에는 `SERVERPROPERTY`가 표준 스칼라 함수로 이미 완벽하게 구현되어 있음.
* **정돈 방향**:
  - `matchVersionQuery`에서 다중 컬럼 `SERVERPROPERTY` 하이재킹 로직을 전면 제거.
  - 정규 SQL 엔진(`s.executor.ExecuteSQL` / `evalSelect`)이 `pkg/proc/builtins_sys.go`를 통해 사용자가 프로젝션한 컬럼만 정확히 평가하여 반환하도록 일원화.
  - `matchVersionQuery`는 순수 단일 값인 `@@VERSION`과 단건 스칼라 프로브만 최소한으로 인터셉트하도록 단순화.

---

### Phase 3: `pkg/catalog/virtual/` 도구 종속적 명명 및 분기 플래그 정돈
* **현상**:
  - 함수명이 `execBeekeeperMSSQLColumnSelect`, `execDbGateMSSQLColumnSelect`, `execDbGateMSSQLPrimaryKeySelect`, `execDBeaverMSSQLAllObjectsSelect`처럼 특정 GUI 도구 이름으로 파편화되어 있음.
  - 쿼리 내부에 `isDBeaver := strings.Contains(upper, "TOTALSIZE") || strings.Contains(upper, "USEDSIZE")` 같은 특정 도구 탐지 분기 존재.
* **정돈 방향**:
  - 도구 이름을 함수명에서 완전히 제거하고, 쿼리의 구조적/스키마적 목적에 따른 객관적 함수명으로 리팩토링:
    - `execBeekeeperMSSQLColumnSelect` ➔ `execInformationSchemaColumnsWithComputed`
    - `execDbGateMSSQLColumnSelect` ➔ `execSysColumnsJoinedWithTypes`
    - `execDbGateMSSQLPrimaryKeySelect` ➔ `execSysIndexesPrimaryKey`
    - `execDBeaverMSSQLAllObjectsSelect` ➔ `execSysAllObjectsJoined`
  - `isDBeaver` 등 도구 전용 분기 플래그를 컬럼 존재 여부(`hasTotalSizeColumn`) 등 표준 SQL 프로젝션 요구 기반으로 전환.

---

## 3. 실행 단계 및 검증 파이프라인 (Execution Plan)

```
[ Step 1: 계획서 수립 및 문서화 ]
  - notes/OVER_SPECIFICATION_REMEDIATION_PLAN.md 작성 및 notes/README 색인 연동
                │
                ▼
[ Step 2: Phase 1 실행 (sqlexec_procs.go) ]
  - execSpExecuteSQL 파라미터 매핑을 순수 위치 기반 폴백으로 정돈
  - dynamic_sql_test.go 및 기존 T-SQL 회귀 검증
                │
                ▼
[ Step 3: Phase 2 실행 (tds/server.go matchVersionQuery) ]
  - SERVERPROPERTY 다중 컬럼 하이재킹 제거 및 표준 스칼라 엔진 위임
  - 2개 컬럼 쿼리(pv, ee)가 정확히 2개 컬럼만 반환하는지 실측 검증
  - DbGate/DBeaver 버전 쿼리 회귀 테스트 통과 확인
                │
                ▼
[ Step 4: Phase 3 실행 (catalog/virtual 리팩토링) ]
  - 도구 종속적 함수명 및 플래그를 구조적 스키마 함수로 리팩토링
  - pgcatalog_test.go 및 coordinator_test.go 통과 확인
                │
                ▼
[ Step 5: 3단계 검증 파이프라인 (Build ➔ Test -race ➔ Commit) ]
  - 4개 바이너리 전체 컴파일 (server, lionsql, bench, tpcc-runtime)
  - go test -tags wiredtiger -count=1 -race ./... 전수 100% PASS 검증
  - CHANGELOG.md 및 TODO.ko.md 동기화, Conventional Commit
```
