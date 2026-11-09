# SSMS (SQL Server Management Studio) Object Explorer 완벽 호환성 아키텍처 및 진행 현황 (`notes/plans/SSMS_OBJECT_EXPLORER_COMPATIBILITY_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-09
> - **최종 갱신일**: 2026-11-09
> - **과제 식별 코드**: `SSMS-OE-COMPAT-1` (완료) / `SSMS-OE-COMPAT-2` (진행 중 — SMO 테이블/하위 객체 바인딩)
> - **상태**: **Phase 1~4 완료 (서버 접속, Databases 트리, Tables 목록 표시 성공) / Phase 5 진행 중 (SMO `NullReferenceException` 해결 및 하위 노드 확장 검증)**
> - **관련 불변식**: [`GEMINI.md`](../../GEMINI.md) Invariant 2 (Tolerant Virtual `USE`), Invariant 7 (Zero-Regex SQL Parsing), Invariant 11 (TDS Windows Host Platform Parity)

---

## 1. 배경 및 목표 (Background & Objectives)

Microsoft SQL Server Management Studio (SSMS 19/20)는 단순한 SQL 쿼리 클라이언트가 아니라, 내부적으로 **SMO (SQL Server Management Objects)** 프레임워크를 사용하여 수십 단계의 환경 탐색 배치, XPath 기반 URN(Uniform Resource Name) 계층 트리 검증, 그리고 고도로 복잡한 시스템 카탈로그 조인 쿼리(`sys.tables`, `sys.indexes`, `sys.partitions`, `sys.allocation_units`, `msdb.dbo.syspolicy_*` 등)를 수행합니다.

본 계획 문서는 오늘 집중 수행한 SSMS Object Explorer 연동 디버깅(`ssms1.txt` ~ `ssms12.txt`)을 통해 해결된 핵심 기술 자산과 아키텍처 결정 사항을 영구 기록하고, 남은 SMO 객체 모델 바인딩 완성을 위한 로드맵을 정의합니다.

---

## 2. 단계별 해결 완료 내역 (Completed Phases: Phase 1 ~ Phase 4)

### Phase 1: TDS 핸드셰이크, Windows 호스트 플랫폼 불변식 및 초기 세션 탐색 (완료)
1. **Windows Host Platform Parity (`Invariant 11`)**:
   - SSMS SMO는 macOS용 네이티브 SQL Server가 존재하지 않음을 전제로 설계되어 있어, `host_platform`으로 `"macOS"`나 `"Linux"`가 반환되면 `ObjectExplorerControl.BuildDataModel`에서 `System.InvalidOperationException`을 던지며 연결 즉시 크래시됨.
   - `sys.dm_os_host_info` 및 TDS 환경 감지 배치(`handleSsmsEnvironmentBatch`)에서 구동 OS와 무관하게 무조건 `host_platform = "Windows"`, `host_distribution = "Windows"`, `host_release = "10.0"`을 반환하도록 영구 고정 및 3계층 방어 테스트 구축 완료.
2. **T-SQL 비트 연산자 및 시스템 내장 함수 확충**:
   - SSMS 카탈로그 쿼리가 광범위하게 사용하는 비트 연산자(`&`, `|`, `^`, `~`)를 Lexer, Pratt Parser, VDBE VM, SQL Evaluator 전 계층에 구현 (`63cfc40`, `bdc406b`).
   - 보안 및 메타데이터 내장 함수 추가: `HAS_DBACCESS()`, `PERMISSIONS()`, `IS_SRVROLEMEMBER()`, `IS_MEMBER()`, `SUSER_SNAME()`, `SCHEMA_NAME()`, `OBJECT_ID()`, `COLLATIONPROPERTY()` (`a93c998`, `30c4857`, `2108840`).

### Phase 2: DMF (Policy-Based Management) 및 Databases 폴더 트리 확장 (완료)
1. **`msdb` 정책 스토어 및 에이전트 가상 테이블 (`5e6d9a6`)**:
   - SSMS 연결 직후 PolicyStore 초기화가 조회하는 `msdb.dbo.syspolicy_configuration`, `syspolicy_system_health_state`, `sys.fn_syspolicy_is_automation_enabled()`, `msdb.dbo.sysjobs` 등을 가상 카탈로그에 등록하여 초기 예외 차단.
2. **Resource Governor 및 `sys.databases` 스키마 정밀화 (`954bc64`, `63cfc40`)**:
   - `sys.resource_governor_configuration`, `sys.master_files`, `sys.database_mirroring`, `sys.database_recovery_status`, `sys.dm_hadr_database_replica_states` 가상 뷰 지원.
   - `sys.databases`에 SSMS 필수 50+개 컬럼(`compatibility_level = 160`, `collation_name`, `state`, `is_read_only`, `recovery_model` 등)을 완비하여 Object Explorer에서 **`master`, `model`, `msdb`, `tempdb` 데이터베이스 목록 정상 출력 달성**.

### Phase 3: 가상 카탈로그 복합 수식 평가기 및 스키마 확장 (완료)
1. **가상 카탈로그 Projection 수식 평가 (`954bc64`)**:
   - 기존 가상 카탈로그 프로젝션(`pkg/catalog/virtual/result.go`)이 단순 컬럼명이나 리터럴만 반환하던 한계를 극복하고, `CAST(...)`, `CASE WHEN ... END`, `ISNULL(...)`, `SERVERPROPERTY(...)`, 산술/비교/문자열 결합(`+`) 수식을 동적으로 평가하는 `evalCatalogExpr` 엔진 탑재.
2. **Programmability 및 Synonym 가상 뷰 (`9497030`)**:
   - 테이블 확장 전후로 SSMS가 탐색하는 `sys.synonyms`, `sys.sql_modules`, `sys.all_sql_modules`, `sys.system_sql_modules`, `syscomments` 가상 테이블 등록.

### Phase 4: `sys.tables JOIN sys.indexes` 라우팅 교정 및 테이블 목록 출력 (완료)
1. **근본 원인 규명 (`ssms11.txt` ~ `ssms12.txt`)**:
   - SSMS의 테이블 목록 조회 쿼리는 `FROM sys.tables AS tbl LEFT OUTER JOIN sys.indexes AS idx ON ...` 형태의 다중 조인 쿼리임.
   - 기존 `virtual.IsVirtualCatalogJoinQuery`가 이를 가로챈 뒤 `catalogRouteTable`에 전용 매칭 규칙이 없어 기본 폴백인 PostgreSQL `execClassSelect`(`pg_class`)로 라우팅되었고, 그 결과 `SELECT` 컬럼 별칭(`Name`, `ID`, `Urn`, `CreateDate` 등)이 전부 누락되어 0행(빈 목록)이 반환되던 버그를 적발.
2. **전용 핸들러 구현 (`8a1063a`)**:
   - `MSSQL_SysTablesJoinedIndexes` 라우팅 규칙 및 `execSysTablesJoinedIndexes` 전용 핸들러를 구현하여 **SSMS Object Explorer의 `Tables` 폴더 아래에 실제 사용자 테이블 목록이 표시되는 것까지 확인 완료**.

---

## 3. 현재 진행 중인 작업: Phase 5 (SMO `NullReferenceException` 해결 및 후속 검증)

### 3.1 현상 (User Reported)
- SSMS Object Explorer에서 `Tables` 폴더를 펼치면 테이블 목록이 화면에 나타나지만, 이와 동시에 또는 항목 참조 시 **`개체 참조가 개체의 인스턴스로 설정되지 않았습니다. (Microsoft.SqlServer.Smo)`** (`System.NullReferenceException`) 대화상자가 출력됨.

### 3.2 식별된 원인 및 `v0.92.0` (`d221482`)에 반영된 선제 조치
1. **SMO 루트 `Server[@Name=...]` URN 불일치 해결**:
   - 초기 접속 배치(`handleSsmsServerInitBatch`)는 서버 URN을 `Server[@Name='MSSQLSERVER']`로 반환한 반면, `SERVERPROPERTY('Servername')`은 `'LionSQL'`, `@@SERVERNAME`과 테이블 URN은 `'LIONSQL'`을 반환하고 있었음.
   - SMO가 테이블의 부모 객체를 `ServerCollection["LIONSQL"]`에서 찾으려다 `null`을 반환받아 `NullReferenceException`이 발생하는 문제를 막기 위해 모든 경로의 서버 이름을 **`MSSQLSERVER`**로 일원화 (`d221482`).
2. **`QUOTENAME(str, quote_char)` 2번째 인자 지원**:
   - SSMS는 URN 조립 시 `quotename(CAST(serverproperty(N'Servername') AS sysname), '''')`를 호출하여 작은따옴표(`'MSSQLSERVER'`)를 기대했으나, 기존 `QUOTENAME`이 2번째 인자를 무시하고 무조건 대괄호(`[MSSQLSERVER]`)를 반환하여 XPath 파싱이 깨지던 버그 수정 (`d221482`).
3. **세션 활성 DB명(`currDb`) 동적 바인딩 및 SMO 테이블 필수 프로퍼티 보강**:
   - 테이블 URN의 `Database[@Name='...']`에 하드코딩된 `'master'` 대신 세션의 현재 DB(`cs.Database`)를 주입하고, SMO `Table` 객체 초기화에 필요한 `HistoryTableID`, `HistoryTableName`, `HistoryTableSchema`, `IsFileTable`, `IsExternal`, `Owner`, `ModifyDate` 컬럼을 추가 (`d221482`).

---

## 4. 향후 재개 시 점검 체크리스트 (Next Steps for `SSMS-OE-COMPAT-2`)

다음 세션에서 SSMS 작업을 이어갈 때 확인할 항목들입니다:

1. **`v0.92.0` (`d221482`) 바이너리 기준 `NullReferenceException` 해소 여부 확인**:
   - 통일된 `Server[@Name='MSSQLSERVER']/Database[@Name='...']/Table[@Name='...' and @Schema='dbo']` URN과 수정된 `QUOTENAME` 적용 후에도 에러가 남아 있는지 확인 및 신규 패킷 로그 수집.
2. **SMO 컬럼 타입 정밀 매핑 (TDS Type Token 검증)**:
   - SMO는 `GetBoolean()`, `GetInt32()`, `GetDateTime()`, `GetString()` 등 강타입(Strongly-Typed) `SqlDataReader` 메서드로 결과를 읽음.
   - 가상 카탈로그 결과셋(`execSysTablesJoinedIndexes`, `execDatabasesSelect` 등)에서 정수(`0`/`1`)로 내려간 필드를 SMO가 `BIT`(`bool`)로 읽으려 하거나, `NULL`을 비-Nullable 스칼라로 읽으려다 `NullReferenceException` / `InvalidCastException`이 발생하는지 프로젝션의 `CAST(... AS bit)`, `CAST(... AS int)` 타입 강제 변환 점검.
3. **테이블 하위 폴더 확장 (Columns, Keys, Indexes, Triggers)**:
   - 테이블 노드를 확장할 때 실행되는 `sys.columns` + `sys.types` + `sys.computed_columns` + `sys.identity_columns` 조인 쿼리에 대한 전용 가상 카탈로그 핸들러(`MSSQL_SysColumnsJoinedTypes`) 구축.
4. **테이블 우클릭 ➔ `Select Top 1000 Rows` / `Edit Top 200 Rows` 호환성**:
   - SSMS가 생성하는 `SELECT TOP (1000) [col1], [col2] FROM [dbname].[dbo].[tablename]` 3-part 이름 정규화 및 실행 검증.
