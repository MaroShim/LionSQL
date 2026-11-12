# SSMS Object Explorer 하위 폴더(Columns, Keys, Constraints, Indexes, Triggers) 완벽 지원 계획 (`notes/plans/SSMS_OBJECT_EXPLORER_SUBFOLDERS_PLAN.ko.md`)

> - **작성일**: 2026-11-12
> - **과제 식별 코드**: `SSMS-OE-SUBFOLDERS` / `SSMS-OE-COMPAT-2` / `SSMS-OE-3PART-TOP` / `SSMS-OE-SMO-VERIFY`
> - **상태**: 진행 중 (In Progress)
> - **관련 불변식**: [`GEMINI.md`](../../GEMINI.md) Invariant 2 (Tolerant Virtual `USE`), Invariant 7 (Zero-Regex SQL Parsing), Invariant 11 (TDS Windows Host Platform Parity), Invariant 12 (`sqlutil` Primacy)

---

## 1. 배경 및 문제 진단 (Background & Diagnosis)

### 1.1 현상
SSMS(SQL Server Management Studio) Object Explorer에서 LionSQL 서버에 연결하고 데이터베이스 및 `Tables` 폴더까지는 목록이 정상적으로 출력되나, 개별 테이블 하위의 트리 노드(Columns, Keys, Constraints, Triggers, Indexes, Statistics)를 펼쳤을 때 하위 객체가 표시되지 않거나 `NullReferenceException`이 발생하는 현상이 발생함.

### 1.2 근본 원인 분석 (Root Causes)
1. **가상 카탈로그 라우터 매칭 결함 (`pkg/catalog/virtual/interceptor.go`)**:
   - SSMS SMO(SQL Server Management Objects)는 테이블 컬럼 조회 시 `FROM sys.tables AS tbl INNER JOIN sys.all_columns AS clmns` 구문을 사용함.
   - 기존 `MSSQL_ColumnsJoinedWithTypes` 매처는 `SYS.COLUMNS`만을 검사하여 `SYS.ALL_COLUMNS`를 매칭하지 못하고 기본 폴백인 PostgreSQL `pg_class`(`execClassSelect`)로 넘어가 0행을 반환함.
   - SSMS SMO가 테이블 하위 인덱스를 조회할 때 `FROM sys.tables AS tbl INNER JOIN sys.indexes AS i`를 질의하는데, 이것이 기존 `MSSQL_SysTablesJoinedIndexes`에 잘못 걸려 인덱스 목록이 아닌 **테이블 목록**(`Table[@Name=...]` URN)을 반환하여 SMO 개체 바인딩이 크래시됨.
   - `sys.key_constraints`, `sys.foreign_keys`, `sys.check_constraints`, `sys.default_constraints`, `sys.triggers`, `sys.stats` 전용 라우팅 핸들러가 부재함.
2. **부모 괄호 조건식에서 동등 조건 필터 추출 누락 (`pkg/sqlutil/predicate.go`)**:
   - SSMS는 모든 하위 쿼리의 WHERE 절을 `WHERE (tbl.name = @_msparam_0 AND SCHEMA_NAME(tbl.schema_id) = @_msparam_1)` 형태로 괄호로 감쌈.
   - `sqlutil.ExtractEqualityFilter`가 서브쿼리 혼선을 막기 위해 `depth == 0`인 경우에만 `=`를 감지하도록 설계되어 있어, 괄호로 둘러싸인 단순 논리 그룹(`depth == 1`) 내부의 `tbl.name = '...'` 조건을 완전히 무시하고 대상 테이블(`targetIDs`)을 `nil`로 반환함.
3. **테이블 식별자 컬럼 별칭 누락 (`pkg/catalog/virtual/mssql.go`)**:
   - `ExtractMssqlTableNameEq`에서 `table_name`, `c.name`, `t.name` 등만 검사하고 SSMS의 표준 별칭인 `tbl.name`이 누락되어 테이블 대상을 포착하지 못함.
4. **SMO 필수 프로젝션 및 XPath URN(Uniform Resource Name) 규격 부재**:
   - SMO는 각 하위 객체를 부모 테이블에 결합(Binding)하기 위해 엄격한 XPath URN 문자열을 요구함:
     - 컬럼: `Server[@Name='MSSQLSERVER']/Database[@Name='<db>']/Table[@Name='<tbl>' and @Schema='dbo']/Column[@Name='<col>']`
     - 인덱스: `Server[@Name='MSSQLSERVER']/Database[@Name='<db>']/Table[@Name='<tbl>' and @Schema='dbo']/Index[@Name='<idx>']`
     - 키: `Server[@Name='MSSQLSERVER']/Database[@Name='<db>']/Table[@Name='<tbl>' and @Schema='dbo']/Key[@Name='<key>']`
     - 외래키: `Server[@Name='MSSQLSERVER']/Database[@Name='<db>']/Table[@Name='<tbl>' and @Schema='dbo']/ForeignKey[@Name='<fk>']`
     - 제약조건: `Server[@Name='MSSQLSERVER']/Database[@Name='<db>']/Table[@Name='<tbl>' and @Schema='dbo']/Check[@Name='<chk>']`
     - 기본값제약: `Server[@Name='MSSQLSERVER']/Database[@Name='<db>']/Table[@Name='<tbl>' and @Schema='dbo']/DefaultConstraint[@Name='<def>']`
     - 트리거: `Server[@Name='MSSQLSERVER']/Database[@Name='<db>']/Table[@Name='<tbl>' and @Schema='dbo']/Trigger[@Name='<tr>']`
     - 통계: `Server[@Name='MSSQLSERVER']/Database[@Name='<db>']/Table[@Name='<tbl>' and @Schema='dbo']/Statistic[@Name='<stat>']`

---

## 2. 해결 아키텍처 및 구현 계획 (Implementation Architecture)

### 2.1 `pkg/sqlutil/predicate.go` - 논리 괄호 그룹 내 등가 필터 추출 지원
- `ExtractEqualityFilter`:
  - 괄호 깊이(`depth`)가 0이 아니더라도, 해당 괄호 블록이 `SELECT`로 시작하는 서브쿼리가 아닌 일반 논리 그룹(`(tbl.name = 'x' AND ...)`)인 경우 동등 조건을 정상 추출하도록 개선.
  - 서브쿼리 여부는 `(` 직후의 첫 토큰이 `SELECT`인지 여부로 결정(Dyck language 컨텍스트 보존).

### 2.2 `pkg/catalog/virtual/mssql.go` - 대상 테이블 추출 보강
- `ExtractMssqlTableNameEq`: `tbl.name`, `tbl.Name`, `t.name`, `name` 등 지원 확장.
- `parseTargetObjectIDs`: `tbl.name = '...'` 및 `@_msparam_0` 바인딩 파라미터 대상 정확 추출.

### 2.3 `pkg/catalog/virtual/interceptor.go` - SSMS 하위 트리 라우팅 우선순위 재정립
- `catalogRouteTable`에 다음 규칙을 정확한 순서로 배치:
  1. `MSSQL_SysIndexColumnsSSMS`: `SYS.INDEX_COLUMNS` 조인 (컬럼 목록)
  2. `MSSQL_SysColumnsSSMS`: `(SYS.COLUMNS || SYS.ALL_COLUMNS) && (SYS.TYPES || SYS.TABLES || DATATYPE || MAX_LENGTH || ISIDENTITY)`
  3. `MSSQL_KeyConstraintsSSMS`: `SYS.KEY_CONSTRAINTS`
  4. `MSSQL_ForeignKeysSSMS`: `SYS.FOREIGN_KEYS`
  5. `MSSQL_CheckConstraintsSSMS`: `SYS.CHECK_CONSTRAINTS`
  6. `MSSQL_DefaultConstraintsSSMS`: `SYS.DEFAULT_CONSTRAINTS`
  7. `MSSQL_TriggersSSMS`: `SYS.TRIGGERS` (또는 `SYS.TABLES JOIN SYS.TRIGGERS`)
  8. `MSSQL_StatsSSMS`: `SYS.STATS` (또는 `SYS.TABLES JOIN SYS.STATS`)
  9. `MSSQL_SysTableIndexesSSMS`: `SYS.TABLES JOIN SYS.INDEXES` 중 인덱스 세부 속성(`HAS_FILTER`, `TYPE_DESC`, `INDEX_ID > 0`, `IS_HYPOTHETICAL`) 질의 시 `execSysIndexesForSSMS`로 라우팅.
  10. `MSSQL_SysTablesJoinedIndexes`: 상기 인덱스 쿼리를 제외한 순수 테이블 목록 조회만 `execSysTablesJoinedIndexes`로 라우팅.

### 2.4 가상 카탈로그 핸들러 구현 (`pkg/catalog/virtual/mssql.go`)
- **Columns**: `Name`, `ID`, `Nullable`, `Identity`, `RowGuidCol`, `IsFileStream`, `IsSparse`, `IsColumnSet`, `Computed`, `Length`, `Precision`, `Scale`, `Collation`, `DataType`, `SystemType`, `Urn` 제공.
- **Indexes**: `Name`, `ID`, `IndexType`, `TypeDesc`, `IsPrimaryKey`, `IsUnique`, `IsUniqueConstraint`, `HasFilter`, `FilterDefinition`, `Urn` 제공.
- **Keys / ForeignKeys**: Primary Key 및 Foreign Key에 대해 각 테이블별 `Key[@Name=...]`, `ForeignKey[@Name=...]` URN 및 메타데이터 반환.
- **Constraints / Triggers / Stats**: 빈 집합 또는 해당 테이블의 제약조건/트리거/통계 레코드와 올바른 URN 반환.

### 2.5 3-Part 이름 정규화 및 우클릭 쿼리 지원
- `SELECT TOP (1000) [col1], [col2] FROM [master].[dbo].[customers]` 등 3-part 테이블 경로 파싱 및 실행 무결성 확인.

---

## 3. 검증 계획 (Verification & 3-Stage Pipeline)
1. **Unit & Integration Tests**:
   - `pkg/sqlutil/predicate_test.go`: 괄호로 감싼 `(tbl.name = 'customers')` 및 중첩 논리 표현식 추출 테스트.
   - `pkg/catalog/virtual/interceptor_test.go`: SSMS 각 하위 노드 쿼리 라우팅 및 반환 URN 검증.
   - `pkg/wire/tds/tool_audit_test.go`: SSMS 실제 SMO 쿼리 시뮬레이션 E2E 테스트.
2. **Build & Race Detection**:
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
   - `go test -tags wiredtiger -v -race ./pkg/catalog/virtual/... ./pkg/sqlutil/... ./pkg/wire/tds/...`
3. **Windows Binary Cross-Compilation**:
   - `GOOS=windows GOARCH=amd64 go build -buildvcs=false -o bin/lionsql.exe ./cmd/lionsql`
   - 바이너리 무결성 및 헤더 검증.
