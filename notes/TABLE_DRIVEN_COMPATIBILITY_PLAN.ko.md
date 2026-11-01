# 10개 실전 서비스 호환 규칙 Table-Driven 구조 정식화 실행 계획서 (`notes/TABLE_DRIVEN_COMPATIBILITY_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **과제 식별 코드**: `COMPAT-TABLE-DRIVEN-1`
> - **상태**: 구현 및 검증 완료 (Completed)
> - **목표**: 10개 실전 서비스(Gitea, Vaultwarden, Casdoor, Ghost, Answer, Affine, Metabase, Umami, Woodpecker, Vikunja) 연동을 통해 축적된 ad-hoc 분기문 및 문자열 매칭 로직을 외부 파일 없이 Go 내부의 정규 테이블 구조(Table-Driven Dispatch Table)로 정식화하여, 제로 할당(Zero-Allocation), 컴파일 타임 타입 안전성, O(1) 룩업, 코드베이스 단순화(Deletion-First)를 달성합니다.
> - **선행 조건**: 기존 10개 서비스 전용 영구 회귀 테스트 100% 통과 유지 및 0 데이터 레이스.

---

## 1. 추진 배경 및 문제 정의

### 1.1 현재 상태 및 부채 분석
LionSQL은 총 10종의 실전 오픈소스 애플리케이션을 외부 수정 없이(Zero Code Change) 지원하기 위해 엔진 계층 전반에 걸쳐 호환성 로직을 점진 추가해 왔습니다.
- **`pkg/wire/server.go`**:
  - `inferParamOID`, `inferParamTypes`: 개별 컬럼명("NSPNAME", "RELNAME", "ATTNAME", "ID", "OID") 및 `= ANY($1)` 여부를 판별하는 `switch/case` 분기.
  - `formatPgCommandComplete`, `handleExecute`: `"DML"`, `"BLOCK"`, `"QUERY"` 등의 내부 타입을 `"UPDATE"`, `"INSERT"`, `"DELETE"` 등의 프로토콜 규격 태그로 변환하는 문자열 슬라이싱.
  - `resolveColumnOIDs`: 데이터 타입 및 컬럼 메타데이터 역추론 로직.
- **`pkg/planner/` & `pkg/engine/`**:
  - `ParseCTEs`: `"WITH RECURSIVE"` 한정자 수용, CTE prefix 분리.
  - `BuildMultiJoinPlanWithStats` / `bounds.go`: 인덱스 Seek 후보 탐색 시 `" OR "` 포함 조건 건너뛰기.
  - `vm.go`: Volcano 엔진용 달러 파라미터(`$1`, `$2`) 바인딩 분기.
- **`pkg/catalog/virtual/`**:
  - `tables.go`: `switch norm` 기반의 수십 개 가상 뷰 이름 나열.
  - `interceptor.go`: `containsFold` 기반의 카탈로그 뷰 탐색.

### 1.2 왜 외부 파일(YAML/DSL) 직행을 지양하고 Go Table-Driven을 선행하는가?
1. **과도한 엔지니어링 방지 (Radical KISS)**:
   - 외부 파일(YAML/JSON)을 로드하려면 파서 라이브러리 도입, 파일 I/O, 디코딩 힙 할당(GC 압박), 런타임 문법 검증기 등 수백 줄의 새로운 복잡성 레이어가 발생합니다.
2. **타입 안전성 및 제로 할당 (Compile-Time Safety & Zero-Allocation)**:
   - Go 내부 정적 구조체 슬라이스(`[]Rule`) 및 해시 맵(`map[string]Rule`)은 컴파일러가 타입과 문법을 보증하며, 런타임 파싱 비용이 0이고 핫패스(Hot-Path) 지연 시간이 16ms 이내(수 나노초 수준)로 보장됩니다.
3. **스키마 안정화 후 외재화 가능**:
   - Go 구조체로 데이터 모델이 완벽히 고정되면, 차후 필요 시 이를 외부 파일로 빼는 것은 단순히 해당 구조체에 역직렬화 태그만 추가하는 10분짜리 작업으로 축소됩니다.

---

## 2. 4대 정식화 대상 도메인 및 구조체 스키마 설계

### 도메인 1: 파라미터 OID 추론 및 바인딩 테이블 (`ParamOIDInferenceTable`)
- **위치**: `pkg/wire/param_rules.go`
- **구조체 정의**:
  ```go
  package wire

  type ParamPatternType int
  const (
      ParamPatternExactColumn ParamPatternType = iota // "nspname = $1"
      ParamPatternAnyArray                           // "nspname = ANY($1)"
      ParamPatternInList                             // "id IN ($1, $2)"
  )

  type ParamRule struct {
      ColumnToken  string           // e.g. "NSPNAME", "RELNAME", "OID", "ID"
      ScalarOID    int32            // e.g. 25 (text), 26 (oid), 23 (int4)
      ArrayOID     int32            // e.g. 1009 (_text), 1028 (_oid), 1007 (_int4)
  }

  // 컴파일 타임 정적 룩업 테이블 (Zero-Allocation O(1) 맵)
  var defaultParamRules = map[string]ParamRule{
      "NSPNAME": {ColumnToken: "NSPNAME", ScalarOID: 25, ArrayOID: 1009},
      "RELNAME": {ColumnToken: "RELNAME", ScalarOID: 25, ArrayOID: 1009},
      "ATTNAME": {ColumnToken: "ATTNAME", ScalarOID: 25, ArrayOID: 1009},
      "TYPNAME": {ColumnToken: "TYPNAME", ScalarOID: 25, ArrayOID: 1009},
      "NAME":    {ColumnToken: "NAME",    ScalarOID: 25, ArrayOID: 1009},
      "OID":     {ColumnToken: "OID",     ScalarOID: 26, ArrayOID: 1028},
      "ID":      {ColumnToken: "ID",      ScalarOID: 23, ArrayOID: 1007},
  }
  ```
- **효과**: `inferParamOID` 내부의 중첩 `switch/case`를 단일 맵 룩업으로 대체하고 신규 토큰 확장을 1줄 추가로 단순화.

---

### 도메인 2: 프로토콜 커맨드 태그 정규화 테이블 (`CommandTagMappingTable`)
- **위치**: `pkg/wire/command_tags.go`
- **구조체 정의**:
  ```go
  package wire

  type CommandTagFormatter func(rowsAffected int64, rowsCount int) string

  type CommandTagRule struct {
      VerbPrefix string
      Formatter  CommandTagFormatter
  }

  var defaultCommandTagRules = []CommandTagRule{
      {
          VerbPrefix: "INSERT",
          Formatter: func(aff int64, cnt int) string {
              return fmt.Sprintf("INSERT 0 %d", aff)
          },
      },
      {
          VerbPrefix: "DELETE",
          Formatter: func(aff int64, cnt int) string {
              return fmt.Sprintf("DELETE %d", aff)
          },
      },
      {
          VerbPrefix: "UPDATE",
          Formatter: func(aff int64, cnt int) string {
              return fmt.Sprintf("UPDATE %d", aff)
          },
      },
      {
          VerbPrefix: "SELECT",
          Formatter: func(aff int64, cnt int) string {
              if cnt == 0 && aff > 0 {
                  cnt = int(aff)
              }
              return fmt.Sprintf("SELECT %d", cnt)
          },
      },
      {
          VerbPrefix: "MOVE",
          Formatter: func(aff int64, cnt int) string { return fmt.Sprintf("MOVE %d", aff) },
      },
      {
          VerbPrefix: "FETCH",
          Formatter: func(aff int64, cnt int) string { return fmt.Sprintf("FETCH %d", aff) },
      },
      {
          VerbPrefix: "COPY",
          Formatter: func(aff int64, cnt int) string { return fmt.Sprintf("COPY %d", aff) },
      },
  }
  ```
- **효과**: VDBE `cmd == "DML"` 및 raw SQL 문장의 첫 번째 토큰 매칭을 테이블 디스패치 함수 하나로 일원화.

---

### 도메인 3: 가상 카탈로그 메타데이터 및 뷰 디스패치 테이블 (`VirtualViewDispatchTable`)
- **위치**: `pkg/catalog/virtual/view_rules.go`
- **구조체 정의**:
  ```go
  package virtual

  type CatalogFamily int
  const (
      FamilyPostgres CatalogFamily = iota // pg_catalog
      FamilyMSSQL                         // sys.*
      FamilyInfoSchema                    // information_schema
  )

  type VirtualViewSpec struct {
      CanonicalName string        // "pg_tables"
      Aliases       []string      // ["pg_catalog.pg_tables", "\"pg_tables\""]
      Family        CatalogFamily
      Columns       []string
  }

  // 컴파일 타임 인덱싱된 불변 룩업 맵 (초기화 시 1회 구축)
  var viewLookupMap map[string]*VirtualViewSpec
  ```
- **효과**: `tables.go`의 60줄이 넘는 하드코딩 `switch norm`을 O(1) 맵 룩업으로 치환하고, 별칭(따옴표, 대소문자, 스키마 접두사) 정규화를 단일 파이프라인으로 통합.

---

### 도메인 4: SQL 방언 정규화 규칙 테이블 (`DialectRewriteTable`)
- **위치**: `pkg/sqlutil/dialect_rules.go` (또는 `pkg/planner/`)
- **구조체 정의**:
  ```go
  package sqlutil

  type RewriteRuleType int
  const (
      RewritePrefixStrip RewriteRuleType = iota // e.g. "WITH RECURSIVE" -> "WITH"
      RewriteKeywordReplace                     // e.g. "LIMIT offset, count"
  )

  type DialectRewriteRule struct {
      ID          string
      TargetApp   string            // "Vikunja", "Prisma", "XORM" (문서화 목적)
      RuleType    RewriteRuleType
      Pattern     string            // 정규화 트리거 키워드
      Replacement string
  }

  var defaultDialectRules = []DialectRewriteRule{
      {
          ID:          "PG-RECURSIVE-CTE",
          TargetApp:   "Vikunja / PostgreSQL Standard",
          RuleType:    RewritePrefixStrip,
          Pattern:     "RECURSIVE ",
          Replacement: "",
      },
  }
  ```
- **효과**: 파서/플래너 곳곳에 임의로 추가된 문자열 조작 로직을 명시적 규칙 목록으로 시각화하고 단일 전처리 파이프라인으로 일원화.

---

## 3. 단계별 실행 로드맵 (Phased Execution)

```
[Phase 1] PGWire 와이어 계층 Table-Driven 정식화
  ├── 1.1 `pkg/wire/param_rules.go` 생성: OID 추론 규칙 테이블 구축
  ├── 1.2 `pkg/wire/command_tags.go` 생성: 커맨드 완료 태그 테이블 구축
  ├── 1.3 `pkg/wire/server.go` 내부 switch/case 및 중복 로직 Deletion-First 정리
  └── 1.4 단위 테스트 및 빌드 검증

[Phase 2] 가상 카탈로그 뷰 매핑 Table-Driven 정식화
  ├── 2.1 `pkg/catalog/virtual/view_rules.go` 생성: Canonical View Spec 맵 구축
  ├── 2.2 `pkg/catalog/virtual/tables.go` switch 분기문 전면 교체
  └── 2.3 가상 카탈로그 단위 테스트 검증 (`pkg/catalog/virtual/...`)

[Phase 3] SQL 방언 전처리 정식화
  ├── 3.1 `pkg/planner/` 및 `pkg/sqlutil/` 내의 ad-hoc 방언 보정 로직 테이블화
  └── 3.2 플래너 및 VM 실행 회귀 테스트 검증

[Phase 4] 10개 실전 서비스 전체 회귀 테스트 및 3단계 파이프라인
  ├── 4.1 10개 서비스 통합 회귀 테스트 검증 (`pkg/engine/..._compat_test.go`)
  ├── 4.2 `go test -tags wiredtiger -count=1 -race ./...` (전체 25개 패키지 0 data race)
  └── 4.3 `TODO.md` 및 `CHANGELOG.md` 갱신 후 최종 커밋
```

---

## 4. 검증 계획 및 완료 기준 (Exit Criteria)

1. **기존 기능 무결성 (Zero Regression)**:
   - 기연동 10개 서비스 회귀 테스트 100% 통과:
     - Gitea (`pkg/engine/gitea_compat_test.go`)
     - Casdoor (`pkg/engine/casdoor_compat_test.go`)
     - Vaultwarden (`pkg/engine/vaultwarden_compat_test.go`)
     - Apache Answer (`pkg/engine/answer_compat_test.go`)
     - AFFiNE (`pkg/engine/affine_compat_test.go`)
     - Metabase (`pkg/engine/metabase_compat_test.go`)
     - Umami (`pkg/engine/umami_compat_test.go`)
     - Woodpecker CI (`pkg/engine/woodpecker_compat_test.go`)
     - Vikunja (`pkg/engine/vikunja_compat_test.go`)
2. **코드 감량 및 가독성 (Deletion-First)**:
   - `pkg/wire/server.go`, `pkg/catalog/virtual/tables.go`에서 ad-hoc switch/case 코드 라인 최소 100줄 이상 순삭제.
3. **3단계 검증 파이프라인**:
   - `go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql`
   - `go test -tags wiredtiger -count=1 -race ./...` (0 fail, 0 race)
   - Conventional Commits (`refactor(wire): formalize table-driven compatibility rules (COMPAT-TABLE-DRIVEN-1)`)
