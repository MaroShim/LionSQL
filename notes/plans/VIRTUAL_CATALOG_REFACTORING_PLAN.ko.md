# 가상 카탈로그(`pkg/catalog/virtual` & `pkg/engine/sqlexec_dbcc.go`) 구조 통합 및 보일러플레이트 리팩토링 계획 (`REFACTOR-VIRTUAL-CATALOG-1`)

> **작성일**: 2026-11-12
> **상태**: 진행 중 (`IN_PROGRESS`)
> **목표 마일스톤**: `1.0-GA`
> **적용 골든 룰**: `[RULE:ModularAbstraction]`, `[RULE:RadicalKISS]`, `[RULE:PipelineVerification]`, `[RULE:PlanPersistence]`, `[RULE:ContinuousBacklog]`

---

## 1. 배경 및 목적

SSMS Object Explorer, DBeaver, Beekeeper Studio, DbGate, XORM 등 다수의 DB 클라이언트 도구 호환성 작업을 거치며 가상 카탈로그(`sys.*`, `pg_catalog.*`, `information_schema.*`) 처리 로직이 빠르게 확장되었다. 현재 모든 클라이언트 호환성 테스트가 100% 통과하고 있으나, 내부 구조 측면에서 다음 4가지 기술 부채 및 중복이 존재한다.

1. **`pkg/catalog/virtual/tables.go` 및 `result.go` 내 파서/헬퍼 중복 및 미사용 코드**:
   - `tables.go`의 `parseVirtualSelectClauses`는 호출처가 없는 데드 코드(Dead Code)이다.
   - `result.go`의 `findTopLevelKeyword`는 `pkg/sqlutil.FindTopLevelKeyword`와 동일한 기능을 중복 구현하고 있다(`[RULE:ModularAbstraction]`).
2. **`pkg/catalog/virtual/mssql.go` 내 보일러플레이트 및 URN/ObjectID 계산식 반복**:
   - `currDb` 추출 블록과 `serverName := "MSSQLSERVER"` 선언이 10개 핸들러 함수에 동일하게 복사되어 있다.
   - 테이블(`100000 + tIdx*100`), 뷰(`200000 + vIdx*100`), 프로시저(`300000 + pIdx*100`) Object ID 수식과 SMO URN 문자열(`Server[@Name=...]/Database[@Name=...]/...`) 포맷팅이 수십 곳에 인라인 하드코딩되어 있다.
3. **`pkg/engine/sqlexec_dbcc.go`와 `pkg/catalog/virtual/` 간의 책임 이원화**:
   - `pg_catalog.*` 및 `information_schema.*` 단일 뷰 조회는 이미 `virtual.ExecPgCatalogSelect`로 위임되어 있는 반면, `sys.*` 단일 뷰 조회(`sys.tables`, `sys.columns`, `sys.objects`, `sys.indexes` 등 30여 개)는 여전히 `pkg/engine/sqlexec_dbcc.go`의 `execVirtualCatalogSelect` 내부(약 830줄)에 거대한 `switch norm` 블록으로 남아 있다.
4. **`pkg/catalog/virtual/interceptor.go` 라우팅 정합성 검증**:
   - 단일 테이블 조회와 다중 조인 조회 간 라우팅 테이블(`catalogRouteTable`) 및 헬퍼의 일관성을 유지하고 전체 회귀 테스트로 검증한다.

---

## 2. 절대 불변 원칙 (100% 동작 보존)

- **외부 동작 무변경(Zero Behavioral Change)**:
  - 반환되는 컬럼명, 컬럼 순서, 데이터 타입, 기본값, 정렬 순서, SMO URN 문자열 포맷, Object ID 산출 공식은 단 1바이트도 변경하지 않는다.
  - `sqlexec_dbcc.go`의 `buildVirtualCatalogResult`가 수행하는 `db_name`/`current_database` 가상 컬럼 주입 및 AST 표현식 투영/정렬(`proc.NewParser`) 동작을 100% 그대로 유지한다.

---

## 3. 단계별 실행 계획

### Step 1: 가상 카탈로그 파싱 One-Pass 최적화 및 파서 중복 제거 (`pkg/sqlutil` & `virtual`)
- `pkg/sqlutil/select_clauses.go`:
  - `SplitSelectClauses(sql string) (proj, from, where, orderBy string)` 구현.
  - 최상위 레벨(`depth == 0`, `!inQuote`)에서 `SELECT`, `FROM`, `WHERE`, `ORDER BY`의 경계 인덱스를 단 1번의 선형 순회($O(N)$)로 슬라이싱하는 **Zero-Allocation One-Pass 스캐너** 도입.
  - 단위 테스트(`select_clauses_test.go`)로 서브쿼리 괄호 및 따옴표 경계 안전성 검증.
- `pkg/catalog/virtual/result.go` & `tables.go`:
  - `ExtractSQLParts` 및 `parseVirtualSelectClauses`를 `sqlutil.SplitSelectClauses` 호출로 단일화.
  - `result.go`의 중복 `findTopLevelKeyword` 및 `tables.go`의 중복 코드 제거.

### Step 2: `mssql.go` 공통 헬퍼 추출 및 보일러플레이트 제거
- `pkg/catalog/virtual/mssql.go`:
  - `const mssqlServerName = "MSSQLSERVER"` 상수 정의
  - `resolveCurrentDB(src Source) string` 추출 (10곳 중복 제거)
  - `tableObjectID(tIdx int) int`, `viewObjectID(vIdx int) int`, `procObjectID(pIdx int) int` 추출
  - `buildDatabaseObjectURN(currDb, nodeType, objName, schema string) string` 및 `buildTableChildURN(currDb, tableName, schema, childType, childName string) string` 추출

### Step 3: `sqlexec_dbcc.go`의 `sys.*` 단일 카탈로그 생성 로직을 `pkg/catalog/virtual/mssql_single.go`로 이관
- `pkg/catalog/virtual/source.go` 및 `pkg/engine/sqlexec_virtual.go`:
  - `sys.partitions`, `sys.table_stats`, `sys.column_stats` 생성에 필요한 `GetPartitions(tableName string) ([]storage.PartitionInfo, error)` 및 `ListTableStats() []*planner.TableStats`를 선택적 인터페이스(또는 `Source` 확장)로 연결.
- `pkg/catalog/virtual/mssql_single.go` (신규 생성):
  - `GenerateSysCatalogRows(src Source, norm string, currDb string) ([]string, []map[string]any, bool, error)` 구현.
  - `sqlexec_dbcc.go`의 830줄 `switch norm` 블록을 100% 동일한 스키마·데이터로 이관하고 Step 2의 ObjectID/헬퍼를 재사용.
- `pkg/engine/sqlexec_dbcc.go`:
  - `execVirtualCatalogSelect`에서 `virtual.GenerateSysCatalogRows(s, norm, currDb)`를 호출하도록 단순화하고, 이후 `currDb` 주입 및 `buildVirtualCatalogResult` 호출 흐름은 그대로 유지.

### Step 4: 빌드 및 전수 회귀 테스트 검증 (`[RULE:PipelineVerification]`)
1. `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. `go test -v -race ./pkg/catalog/... ./pkg/engine/... ./pkg/wire/...` 및 전체 패키지 테스트 통과 확인
3. `CHANGELOG.md`, `notes/README.ko.md`, `.sprint.db`(`TASKS.md`) 동기화 및 커밋
