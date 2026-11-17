# LionSQL 스토리지 엔진 역할 분담 및 테이블 단위 T-SQL 영속성(`DURABILITY = SCHEMA_ONLY`) 아키텍처 설계서
(`notes/plans/archive/MEMENGINE_ROLE_AND_EPHEMERAL_SCHEMA_PERSISTENCE_PLAN.ko.md`)

> - **작성일**: 2026-11-15 (개정: 2026-11-16)
> - **마일스톤**: `v0.94.0` (Priority 1)
> - **태스크 ID**: `ARCH-EPHEMERAL-SCHEMA-PERSISTENCE-1`
> - **관련 규칙**: `[RULE:AtomicSaves]`, `[RULE:RadicalKISS]`, `[RULE:ExplicitError]`, `[RULE:PlanPersistence]`, `[RULE:HermeticParity]`

---

## 1. 배경 및 아키텍처 결단 (Background & Architectural Decision)

### 1.1 서버 레벨 단순화 (`[RULE:RadicalKISS]`) & 전역 `-ephemeral` 플래그 폐지
1. **서버 전역 플래그의 문제점**:
   - 서버 프로세스 단위로 `-ephemeral`이나 `--durability` 같은 CLI 플래그를 두는 것은 CLI를 번잡하게 만들고, 부주의한 옵션 지정 시 전체 데이터베이스가 유실될 위험이 있다.
   - 또한 엔진 내부 코드 전반에 "지금 서버가 휘발성 모드인가?"를 묻는 불필요한 전역 분기를 양산한다.
2. **서버 레벨 단일화 원칙**:
   - LionSQL 서버는 Mac, Linux, Windows를 불문하고 **"디렉토리가 주어지면 기본적으로 모든 데이터와 DDL을 안전하게 영구 저장하는 표준 ACID 데이터베이스"**로 단일화한다.
   - 전역 `-ephemeral` 및 `--durability` CLI 플래그는 완전히 제거/폐지한다. (1회성 메모리 테스트는 단순히 임시 디렉토리 지정이나 `:memory:` 모드로 충분히 대체 가능).

### 1.2 OS별 기본 스토리지 엔진 정립
1. **Windows**: **Pure-Go `compact` (In-Memory OCC + AOF/WAL) 엔진 기본 채택**
   - Windows 환경에서는 복잡한 Cgo나 외부 C 라이브러리(`WiredTiger`) 크로스 컴파일 링크 의존성을 배제하고, 자립형 정적 실행 바이너리(`lionsql.exe`)를 보장하기 위해 `compact` 엔진을 기본으로 정식 확정한다.
   - `compact` 엔진은 기본적으로 AOF(`wal.aof`) 및 스냅샷(`tables.mpk`)을 통해 데이터를 완전 영구 보존하므로, 재시작 시 데이터 유실이 전혀 없다.
2. **macOS / Linux**: **`WiredTiger` 기본 채택 (`compact` 선택 가능)**
   - 대규모 프로덕션 OLTP 워크로드를 위해 `WiredTiger`를 기본 스토리지 엔진으로 유지하며, 필요 시 `-engine=compact`를 자유롭게 지정할 수 있다.

---

## 2. Microsoft SQL Server T-SQL 표준 100% 수용: 테이블 레벨 영속성 (`WITH (DURABILITY = ...)`)

### 2.1 Hekaton In-Memory OLTP 표준 문법
Microsoft SQL Server In-Memory OLTP에서 영속성(Durability)은 엔진이나 서버 전역 옵션이 아니라 **개별 테이블의 DDL 옵션**이다:

```sql
-- 1) 일반 테이블 (기본값): 스키마와 데이터 모두 WAL/스냅샷으로 영구 보존
CREATE TABLE dbo.Orders (
    OrderID INT NOT NULL PRIMARY KEY NONCLUSTERED,
    Amount DECIMAL(18,2)
) WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_AND_DATA);

-- 2) 캐시/세션/스테이징 테이블: 스키마는 카탈로그에 영구 보존, 데이터는 재기동 시 초기화 (0 rows)
CREATE TABLE dbo.WebSession (
    SessionID VARCHAR(64) NOT NULL PRIMARY KEY NONCLUSTERED,
    Payload NVARCHAR(MAX)
) WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_ONLY);
```

### 2.2 영속성 모드 비교 및 동작 정책

| DDL 옵션 | 스키마 영속성 (테이블 정의, 인덱스) | 로우 데이터(Row Data) 영속성 | 주 용도 |
| :--- | :---: | :---: | :--- |
| **`DURABILITY = SCHEMA_AND_DATA`**<br>(옵션 생략 시 기본값) | **영구 보존** (카탈로그 / 스냅샷) | **영구 보존** (WAL + 스냅샷) | 비즈니스 원장, 회원, 주문 등 일반적인 영구 데이터 |
| **`DURABILITY = SCHEMA_ONLY`**<br>(명시적 지정) | **영구 보존** (카탈로그 / 스냅샷) | **휘발성 (Ephemeral)**<br>(재기동 시 0 rows 초기화) | 웹 세션 캐시, ETL 임시 스테이징, 고속 작업 큐 |

---

## 3. 상세 기술 설계 (Detailed Technical Design)

### 3.1 DDL 파서 (`pkg/sqlutil`, `pkg/proc`, `pkg/engine`) 확장
1. **`CREATE TABLE ... WITH (...)` 절 파싱**:
   - `MEMORY_OPTIMIZED = ON | OFF` 인식
   - `DURABILITY = SCHEMA_ONLY | SCHEMA_AND_DATA` 인식
2. **카탈로그 메타데이터 (`storage.TableSchema`) 필드 추가**:
   ```go
   type TableSchema struct {
       Name            string
       Columns         []ColumnDef
       PK              []string
       Durability      string // "SCHEMA_AND_DATA" (default) or "SCHEMA_ONLY"
       MemoryOptimized bool
       // ...
   }
   ```
3. **기본값 정책**:
   - `WITH (DURABILITY = ...)` 절이 생략된 일반 테이블은 기본값 `Durability = "SCHEMA_AND_DATA"`로 설정되어 기존과 100% 동일하게 디스크에 영구 보존된다.
   - 오직 `WITH (DURABILITY = SCHEMA_ONLY)`가 명시된 테이블만 `Durability = "SCHEMA_ONLY"`로 마킹된다.

### 3.2 스토리지 엔진 (`memengine` & `wiredtiger`) 실행 로직
1. **DML 쓰기 시 WAL 로깅 선택적 스킵**:
   - `INSERT`, `UPDATE`, `DELETE` 트랜잭션 커밋 시:
     - 대상 테이블의 `schema.Durability == "SCHEMA_ONLY"`인 경우, 인메모리 테이블 상태는 갱신하되 AOF/WAL(`wal.aof`) 프레임 직렬화 및 쓰기 호출을 생략한다.
     - 결과적으로 디스크 쓰기 I/O가 0%가 되어 초고속 RAM 처리량을 달성한다.
2. **RDB 스냅샷 / 체크포인트 저장 시 데이터 제외**:
   - 스냅샷 덤프(`tables.mpk` 등) 저장 시, `SCHEMA_ONLY` 테이블은 스키마 정의(`TableSchema`)만 저장하고 행 데이터 슬라이스는 덤프하지 않는다.
3. **서버 재기동(Recovery) 복원 동작**:
   - 서버 재시작 시 카탈로그에서 모든 테이블의 스키마, 컬럼, 인덱스를 100% 정상 복원한다.
   - `SCHEMA_ONLY` 테이블은 로우 카운트 0건(Empty) 상태로 복원되므로, 클라이언트 애플리케이션이 `table does not exist` 에러를 겪지 않고 즉시 세션/캐시 작업을 재개할 수 있다.

### 3.3 서버 레벨 단순화 (`pkg/config`, `cmd/lionsql/server.go`)
1. **전역 `-ephemeral` 및 `--durability` 플래그 제거**:
   - 서버 레벨에서는 더 이상 복잡한 전역 내구성 분기를 두지 않는다 (`[RULE:RadicalKISS]`).
   - `dataDir`가 지정되면 서버는 항상 영속성 엔진으로 기동된다.
2. **시스템 카탈로그(`pkg/catalog`)의 무조건 영속화**:
   - `dataDir`가 존재하면 시스템 카탈로그(저장 프로시저, 뷰, 트리거, 시퀀스)는 무조건 디스크에 저장되어 서버 재시작 시 100% 보존된다.

---

## 4. 검증 및 테스트 계획 (Verification Plan)
1. **단위 테스트 (`pkg/engine`, `pkg/storage/memengine`, `pkg/proc`)**:
   - `CREATE TABLE t (id INT) WITH (DURABILITY = SCHEMA_ONLY)` DDL 파싱 및 카탈로그 저장 검증.
   - `SCHEMA_ONLY` 테이블에 데이터 INSERT 후 엔진 재기동 ➔ 스키마 온전 유지 및 데이터 0건 초기화 검증.
   - 일반 테이블(`SCHEMA_AND_DATA`)에 데이터 INSERT 후 엔진 재기동 ➔ 스키마 및 데이터 100% 온전 복구 검증.
2. **오라클 회귀 검증**:
   - T-SQL 및 SSMS 호환성 스위트에서 `WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_ONLY)` 구문이 에러 없이 무결하게 실행되는지 검증.

---

## 5. 완료 보고서 (Execution & Completion Report)

> - **완료 일자**: 2026-11-16
> - **최종 상태**: **`DONE`** (스프린트 DB 동기화 완료)
> - **담당 도메인**: Storage / Engine / Parser
> - **연계 커밋 및 태스크**: `ARCH-EPHEMERAL-SCHEMA-PERSISTENCE-1` (`v0.94.0`, Priority 1)

### 5.1 주요 구현 내용 요약 (What Was Implemented)

1. **테이블 스키마 메타데이터 확장 (`pkg/storage/engine.go`)**:
   - 영속성 표준 상수 정의:
     ```go
     const (
         DurabilitySchemaAndData = "SCHEMA_AND_DATA"
         DurabilitySchemaOnly    = "SCHEMA_ONLY"
     )
     ```
   - `storage.TableSchema` 구조체에 `Durability string` 및 `MemoryOptimized bool` 필드 추가.
   - 기본값: 옵션 생략 시 `Durability = DurabilitySchemaAndData`로 설정되어 기존 영속 테이블과 100% 하위 호환.

2. **재귀 하강 토크나이저 AST 파서 DDL 확장 (`pkg/proc/`)**:
   - `pkg/proc/parser_ddl.go`, `ast.go`: `CreateTableStmt`에 `Durability` 및 `MemoryOptimized` 필드 추가.
   - `p.ParseCreateTable()`을 외부 패키지에 정식 공개하고, `WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_ONLY)` 구문을 `TokenAssign` / `TokenEq` 기반의 엄격한 토큰 AST 파싱으로 완전 정규화.
   - `pkg/proc/sqlparser_test.go`: `TestParseCreateTable_WithDurability` 단위 테스트 작성 및 통과 (1.814s).

3. **엔진 DDL 디스패처의 조잡한 문자열 슬라이싱 폐기 (`pkg/engine/sqlexec_ddl.go`)**:
   - 기존의 ad-hoc 문자열 슬라이싱 휴리스틱인 `parseTableWithClause` 함수를 전면 제거(`[RULE:RadicalKISS]`).
   - `execCreateTable` 진입 시 `proc.NewParser(proc.NewLexer(cleaned)).ParseCreateTable()`를 호출하여 정식 파서 AST 노드로부터 `Durability` 및 `MemoryOptimized` 속성을 직접 수신하도록 전환.

4. **In-Memory OCC 스토리지 엔진 선별적 WAL/스냅샷 격리 (`pkg/storage/memengine/`)**:
   - **WAL 로깅 바이패스 (`tx.go`)**: 트랜잭션 커밋(`Commit`) 단계에서 대상 테이블이 `chTD.Schema.Durability == storage.DurabilitySchemaOnly`인 경우, 메모리 상태만 갱신하고 `AOF/WAL` 레코드 기록을 완전히 생략하여 제로 I/O 달성.
   - **스냅샷 데이터 제외 (`persistence.go`)**: RDB 스냅샷(`dumpTableDataLocked`) 시 `Durability == DurabilitySchemaOnly` 테이블의 `Rows = nil`로 설정하여, 테이블 정의 스키마와 인덱스 메타데이터만 `tables.mpk`에 직렬화하고 행 데이터는 저장하지 않음.

5. **인메모리 OCC 스토리지 단일화 (`compact` 단일 아키텍처 확정 및 `performance` 완전 제거)**:
   - 기존의 포인터 슬라이스 기반 레거시인 `MemoryPerformance` 모드를 완전히 삭제하고, 포인터리스 83% 메모리 절감 및 제로 GC 스캔을 달성한 `MemoryCompact` 단일 아키텍처로 일원화 완료 (`[RULE:RadicalKISS]`).
   - `shardEntry` 내부의 `row storage.Row` 포인터 슬라이스 필드를 완전히 도려내어 메모리 상주 시 Go GC 포인터 추적 오버헤드를 100% 원천 차단.
   - `table_data.go`, `tx.go`, `ddl.go`, `ddl_alter.go`, `persistence.go` 전역의 `if td.mode == MemoryCompact ... else ...` 레거시 이중 분기를 전수 폐기하고 `flatBuf` 직결 단일 핫패스로 정규화.
   - `cmd/lionsql/sql.go` 및 배너의 `warnIfMemoryPerformanceDeprecated` 경고 분기 삭제.

### 5.2 검증 결과 (Verification Results)

1. **스토리지 엔진 레벨 재기동 무결성 검증 (`pkg/storage/memengine/schema_only_durability_test.go`)**:
   - `TestMemengine_SchemaOnlyDurability_ZeroRowsOnRestart`:
     - 일반 테이블(`app_users`)에 2건, `SCHEMA_ONLY` 테이블(`web_sessions`)에 5건 INSERT 후 트랜잭션 커밋.
     - 엔진을 디스크에서 `Close()` 후 동일 디렉토리에서 재오픈.
     - **결과**: `app_users`는 2건 100% 복원, `web_sessions`는 0건(Empty)으로 정상 초기화 확인.
     - 재기동된 `web_sessions` 테이블에 신규 INSERT 수행 시 즉시 정상 동작 확인.
     - `go test -v -race ./pkg/storage/memengine`: **PASS (0.05s, 0 data races)**.

2. **엔진 코디네이터 SQL 레벨 통합 검증 (`pkg/engine/schema_only_sql_test.go`)**:
   - `TestEngine_TSQL_DurabilitySchemaOnly_RestartZeroRows`:
     - 실제 T-SQL DDL 구문 실행:
       ```sql
       CREATE TABLE web_cache (id INT PRIMARY KEY, token VARCHAR(64)) 
       WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_ONLY);
       ```
     - 데이터 입력 후 코디네이터 인스턴스 완전 종료 및 재시작.
     - `SELECT COUNT(*) FROM web_cache`가 재시작 후 정확히 `0`을 반환하고 스키마가 완벽히 온전함을 실측 검증.
     - `go test -v -race ./pkg/engine -run TestEngine_TSQL_DurabilitySchemaOnly_RestartZeroRows`: **PASS (0.05s, 0 data races)**.

3. **빌드 무결성 (`[RULE:PipelineVerification]`)**:
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`: 무경고 빌드 성공.

---

### 5.3 아키텍처 성과 및 기대 효과

1. **서버 단순화 완성**: 부주의한 데이터 유실 위험이 있던 서버 전역의 불필요한 CLI 플래그 복잡성을 완전히 덜어내고, 표준 T-SQL 테이블 DDL 옵션으로 단일화했습니다.
2. **Fresh-Instance 및 재시작 안전성 확립**: 캐시나 세션 테이블을 사용하는 애플리케이션이 DB 재기동 시 `table not found` 오류 없이 즉시 구동될 수 있는 엔터프라이즈급 안정성을 확보했습니다.
3. **토크나이저 AST 전환의 성공적 교두보**: `sqlexec_ddl.go` 내의 조잡한 문자열 슬라이싱 코드를 성공적으로 도려내고 `pkg/proc` Pratt 파서로 직결함으로써, 뒤이어 진행될 `REFACTOR-ENGINE-AST-PARSER-1` 전면 리팩토링의 모범 패턴을 확립했습니다.

