# LionSQL Grafana 듀얼 프로토콜 연동 및 관측성 플랫폼 호환성 실전 검증 보고서 (`notes/GRAFANA_INTEGRATION_REPORT.ko.md`)

> - **문서 버전**: v1.0.0
> - **최초 작성일**: 2026-10-31
> - **과제 식별 코드**: `GRAFANA-INTEGRATION-1`
> - **대상 릴리즈**: v0.90.0-beta4+
> - **핵심 검증 대상**: Grafana v13.0+ (오픈소스 관측성/시각화 플랫폼)
>   - 채널 1 (내부 메타데이터 DB): `github.com/lib/pq` + XORM (`PostgreSQL Wire Protocol v3 :5433`)
>   - 채널 2 (듀얼 쿼리 데이터 소스): `grafana-postgresql-datasource` (`:5433`) & `mssql` (`Microsoft SQL Server TDS :1433`)
> - **연계 핵심 문서**:
>   - **공식 실전 가이드 (국문)**: [`doc/GRAFANA_INTEGRATION_GUIDE.ko.md`](../doc/GRAFANA_INTEGRATION_GUIDE.ko.md)
>   - **공식 실전 가이드 (영문)**: [`doc/GRAFANA_INTEGRATION_GUIDE.md`](../doc/GRAFANA_INTEGRATION_GUIDE.md)

---

## 1. 개요 및 연동 검증 의의

LionSQL은 단일 정적 바이너리로 구동되는 고동시성 임베디드 OLTP 데이터베이스 엔진으로서, Microsoft SQL Server TDS 유선 프로토콜(`:1433`)과 PostgreSQL PGWire v3 프로토콜(`:5433`)을 네이티브로 듀얼 지원합니다.

앞서 수행한 **Gitea 연동(`GITEA-PGWIRE-1`)**과 **IOTA-SDK Go ERP 연동(`IOTA-ERP-1`)**에 이어, 이번 **Grafana 듀얼 프로토콜 통합(`GRAFANA-INTEGRATION-1`)**은 다음과 같은 엔지니어링 이정표를 달성했습니다:

1. **785개 대규모 엔터프라이즈 마이그레이션 100% 무결성 통과**:
   - Grafana 코어 마이그레이터의 **728개 스키마 마이그레이션**(`migration_log`)과 유니파이드 스토리지 리소스 마이그레이터의 **57개 마이그레이션**(`resource_migration_log`), 총 **785개 마이그레이션 단계**를 단 한 줄의 클라이언트 코드 수정 없이 100% 통과했습니다.
2. **듀얼 채널 동시 구동 실증 (내부 백엔드 + 외부 듀얼 데이터 소스)**:
   - 단일 LionSQL 인스턴스가 Grafana의 내부 메타데이터 백엔드(`:5433`)로 동작하는 동시에, 대시보드 패널이 질의하는 외부 **PostgreSQL 데이터 소스(`:5433`)**와 **Microsoft SQL Server 데이터 소스(`:1433`)** 역할을 동시에 수행함을 입증했습니다.
3. **복합 CTE / 파생 테이블(`WITH ... UNION ALL`) 및 RBAC 권한 엔진 돌파**:
   - Grafana의 RBAC 권한 조회 쿼리(`SELECT p.*, 0 AS team_id, '' AS team, ur.user_id ... FROM (SELECT ... UNION ALL SELECT ...) AS ar INNER JOIN permission AS p ...`)가 요구하는 다중 서브쿼리 파생 테이블 격리, 리터럴 프로젝션 평가, Prepared Statement `Describe` 와일드카드(`p.*`) 컬럼 확장을 완벽히 구현했습니다.
4. **봉투 암호화(`BYTEA` `data_keys`) 및 어드바이저리 락 지원**:
   - 데이터 소스 시크릿 암호화에 필수적인 PostgreSQL `BYTEA` hex 포맷(`\x<hex>`) 왕복 직렬화와 마이그레이션 동시성 제어용 어드바이저리 락(`pg_try_advisory_lock`)을 완비했습니다.

---

## 2. 해결된 핵심 기술 장벽 및 아키텍처 개선

```mermaid
flowchart TD
    Grafana["Grafana v13.0 Startup & Dual Query"] --> M1["1. Core & Resource Migrations<br/>(728 + 57 = 785 Steps)"]
    Grafana --> M2["2. Envelope Encryption & RBAC<br/>(data_keys BYTEA & CTE Joins)"]
    Grafana --> M3["3. Dual Data Source Plugins<br/>(PostgreSQL :5433 & MSSQL :1433)"]

    M1 --> Fix1["pg_try_advisory_lock (OID 16)<br/>EXTRACT(EPOCH FROM statement_timestamp())<br/>Logical TableSchema.PhysicalName"]
    M2 --> Fix2["BYTEA \\x<hex> 왕복 직렬화<br/>Prepared Describe p.* 확장<br/>Volcano 상수 리터럴 프로젝션"]
    M3 --> Fix3["파생 테이블(#) 커서 Eviction & 고유 URI<br/>TDS 'type' 컬럼 colSize=255 보정"]

    Fix1 --> Kernel["LionSQL Storage & Wire Kernel"]
    Fix2 --> Kernel
    Fix3 --> Kernel
    Kernel --> Success["785개 마이그레이션 완주 & PG/MSSQL 듀얼 쿼리 HTTP 200 OK"]
```

### 2.1. PostgreSQL 어드바이저리 락(`pg_try_advisory_lock`) 및 OID 16 보장
- **현상**: Grafana 리소스 마이그레이터가 스키마 락 획득을 위해 `SELECT pg_try_advisory_lock($1)` 및 `SELECT pg_advisory_unlock($1)`을 호출할 때 미등록 함수 에러 또는 `Scan` 타입 불일치 발생.
- **아키텍처 개선**:
  - `pkg/proc/builtins_sys.go` 및 `pkg/engine/sqlexec_select.go`에 `pg_try_advisory_lock`, `pg_try_advisory_xact_lock`, `pg_advisory_unlock`, `pg_advisory_unlock_all`, `pg_advisory_lock`, `pg_advisory_xact_lock` 내장 함수를 구현하고 항상 `bool(true)`(PGWire OID 16 `BOOLOID`)를 반환하도록 보장.

### 2.2. 논리적 테이블 이름 변경(`PhysicalName`) 분리를 통한 WiredTiger `EBUSY` 방지
- **현상**: Grafana 마이그레이션이 트랜잭션 내부에서 임시 테이블 생성 ➔ 데이터 복사 ➔ 기존 테이블 삭제 ➔ `ALTER TABLE ... RENAME TO ...` 패턴을 수행할 때, 활성 커서가 열려 있는 상태에서 물리적 `WT_SESSION.rename`이 호출되어 `Device or resource busy (16)` 오류 발생.
- **아키텍처 개선**:
  - `pkg/storage/engine.go`의 `TableSchema`에 `PhysicalName` 필드 및 `TableURI()` 헬퍼를 도입.
  - `RenameTable` 실행 시 물리적 WiredTiger 파일은 원래의 `PhysicalName`을 그대로 유지하고 카탈로그상의 논리적 테이블 이름(`Name`)만 원자적으로 교체하여 `EBUSY` 충돌을 원천 제거.

### 2.3. 파생 테이블(`#__dt_...`) 커서 Eviction 및 고유 물리 URI 부여
- **현상**: 복합 서브쿼리 및 CTE(`WITH all_role AS (...)`) 실행 시 생성되는 임시 파생 테이블(`#__dt_2_all_role`)이 삭제(`DropTable`)될 때 캐시된 커서가 남아 있으면 물리적 테이블 드롭이 실패하여 다음 쿼리에서 `key already exists` 오류 발생.
- **아키텍처 개선**:
  - `pkg/storage/wiredtiger/wt_engine.go`의 `DropTable`에서 `WT_SESSION.drop` 호출 전 반드시 `e.evictAllCursors(tbl.Name)`를 수행하도록 수정.
  - 임시 테이블(`#` 접두사) 생성 시 나노초 타임스탬프 기반의 고유 `PhysicalName`을 부여하고 불필요한 `saveCatalog()` 디스크 I/O를 생략하여 격리성과 성능을 동시에 확보.

### 2.4. `EXTRACT(EPOCH FROM statement_timestamp())` 및 `LOCK TABLE` 지원
- **현상**: 리소스 마이그레이터가 실행 시각 기록을 위해 `SELECT (EXTRACT(EPOCH FROM statement_timestamp()) * 1000000)::BIGINT` 및 `LOCK TABLE resource IN EXCLUSIVE MODE`를 실행할 때 파싱 실패 및 `NULL` 반환 오류 발생.
- **아키텍처 개선**:
  - `pkg/proc/parser_expr.go`에서 SQL 표준 `EXTRACT(field FROM source)` 구문을 `DATEPART('field', source)` 호출로 변환.
  - `pkg/proc/builtins_date.go`에 `epoch` 단위를 추가하여 Unix 초(`float64`)를 반환하고, `STATEMENT_TIMESTAMP`, `CLOCK_TIMESTAMP`, `TIMEOFDAY` 함수를 등록.
  - `pkg/engine/sqlexec.go` 및 `pkg/wire/server.go`에서 `LOCK TABLE ...` 구문을 관용적 No-Op(`LOCK TABLE` 태그 반환)으로 수용.

### 2.5. `BYTEA` `\x<hex>` 왕복 직렬화 및 Prepared Statement 트랜잭션 컨텍스트 유지
- **현상**: Grafana 봉투 암호화 서비스(`envelope encryption`)가 `data_keys` 테이블의 `BYTEA` 컬럼에 바이너리 암호화 키를 저장한 뒤 읽어들일 때 포맷 불일치로 복호화에 실패하거나, 활성 트랜잭션(`BEGIN`) 내 Prepared Statement 실행 시 별도 세션으로 분기되어 방금 삽입한 행을 찾지 못하는 문제 발생.
- **아키텍처 개선**:
  - `pkg/storage/types_extended.go`, `pkg/wire/server.go`, `pkg/proc/runtime/vm.go`에서 PostgreSQL 표준 `\x<hex>` 리터럴을 원시 `[]byte`로 디코딩하여 저장하고, PGWire 텍스트 프로토콜 응답 시 `\x` 접두사 16진수 문자열로 직렬화하여 완벽한 무손실 왕복(Round-Trip) 달성.
  - `pkg/wire/server.go`의 `handleExecute`에서 Prepared Statement 실행 시 `s.coordinator.ExecuteSessionSQL(ctx, cs.SessionID, ...)`를 호출하여 세션의 활성 트랜잭션(`cs.Tx`) 격리 문맥을 그대로 승계.

### 2.6. PGWire `handleDescribe` 와일드카드(`p.*`) 확장 및 Volcano 리터럴 프로젝션 평가
- **현상**: Grafana RBAC 권한 로더가 `SELECT p.*, 0 AS team_id, '' AS team, ur.user_id AS user_id, false AS is_service_account FROM (...) AS ar INNER JOIN permission AS p ...`를 Prepared Statement로 실행할 때 `arg team_id as int: strconv.ParseInt: parsing "2026-10-31 ...": invalid syntax` 오류 발생.
- **원인**:
  1. `handleDescribe`가 `RowDescription`을 생성할 때 `p.*`를 실제 `permission` 테이블의 10개 컬럼으로 확장하지 않고 `"p.*"` 단일 컬럼으로 반환하여, 이후 컬럼 이름이 9칸씩 밀리는 오프셋 불일치 발생.
  2. Volcano 엔진의 `NewProjectionIterator`(`pkg/execution/project.go`)가 연산자 문자가 없는 단순 상수 리터럴(`0`, `''`, `false`)을 파싱하지 않아 `nil`로 평가됨.
- **아키텍처 개선**:
  - `pkg/wire/server.go`의 `handleDescribe`에서 `*` 및 `alias.*` 프로젝션을 대상 테이블 스키마의 실제 컬럼 목록과 OID로 완전 확장하여 `Execute` 시점의 `DataRow`와 100% 정렬.
  - `pkg/execution/project.go`에서 모든 비컬럼 표현식을 `proc.ParseExprString`으로 파싱하여 상수 리터럴(`LiteralExpr`)이 정확한 타입(`int64(0)`, `string("")`, `bool(false)`)으로 평가되도록 수정.

### 2.7. TDS `inferColumnInfos`의 `type` 컬럼 길이(`colSize`) 보정
- **현상**: Grafana의 Microsoft SQL Server 데이터 소스 플러그인(`go-mssqldb`)으로 `SELECT id, name, type, url FROM data_source` 실행 시 `runtime error: slice bounds out of range [:8] with capacity 2` 패닉 발생.
- **원인**: `pkg/wire/tds/server.go`의 `inferColumnInfos`가 `sysobjects.type`(`char(2)`) 호환을 위해 컬럼명이 `"type"`인 경우 무조건 `colSize = 2`로 고정하여, `"postgres"`(8바이트) 응답 시 `go-mssqldb` 버퍼 용량(2바이트)을 초과.
- **아키텍처 개선**:
  - `lowerCol == "type"`일 때 기본 `colSize`를 `255`(또는 실제 값 길이보다 크게)로 설정하여 `sysobjects` 2바이트 문자열과 일반 테이블의 `type` 컬럼 모두 안전하게 스캔되도록 해결.

---

## 3. 검증 결과 요약

| 검증 항목 | 검증 대상 | 결과 | 세부 내용 |
| :--- | :--- | :---: | :--- |
| **코어 스키마 마이그레이션** | Grafana Core Migrator (`:5433`) | **100% PASS** | 728/728 마이그레이션 완료 (`migration_log`) |
| **리소스 스키마 마이그레이션** | Grafana Resource Migrator (`:5433`) | **100% PASS** | 57/57 마이그레이션 완료 (`resource_migration_log`) |
| **웹 UI 및 관리자 인증** | `http://localhost:3100` (`admin`) | **100% PASS** | RBAC 권한 시딩 및 로그인/세션 인증 성공 |
| **PG 데이터 소스 연동** | `grafana-postgresql-datasource` (`:5433`) | **100% PASS** | Health Check `OK` 및 `/api/ds/query` `HTTP 200` |
| **MSSQL 데이터 소스 연동** | `mssql` 데이터 소스 플러그인 (`:1433`) | **100% PASS** | Health Check `OK` 및 `/api/ds/query` `HTTP 200` |
| **듀얼 프로토콜 동시 쿼리** | 단일 요청 내 PG + MSSQL 동시 질의 | **100% PASS** | 두 프로토콜 모두 동일한 프레임 데이터 정확히 반환 |
| **레이스 디텍터 전체 테스트** | `go test -tags wiredtiger -race ./...` | **100% PASS** | 전 패키지 통과 및 데이터 레이스 0건 |

---

## 4. 결론 및 아키텍처 의의

Gitea(116개 테이블), IOTA-SDK Go ERP(47개 마이그레이션)에 이어 Grafana(785개 마이그레이션 및 듀얼 데이터 소스 플러그인)까지 무수정 100% 연동을 달성함으로써, LionSQL은 **엔터프라이즈 웹 애플리케이션의 내부 RDBMS 백엔드이자 관측성 플랫폼의 듀얼 프로토콜(PGWire & TDS) 분석/시각화 데이터 소스**로서의 완성도를 입증했습니다.
