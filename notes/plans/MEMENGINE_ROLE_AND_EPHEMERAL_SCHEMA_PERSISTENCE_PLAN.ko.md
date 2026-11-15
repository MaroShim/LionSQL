# LionSQL 스토리지 엔진 역할 분담 및 Ephemeral 모드 스키마 영속성(`SCHEMA_ONLY`) 아키텍처 설계서
(`notes/plans/MEMENGINE_ROLE_AND_EPHEMERAL_SCHEMA_PERSISTENCE_PLAN.ko.md`)

> - **작성일**: 2026-11-15
> - **마일스톤**: `1.0-GA` (Priority 1)
> - **태스크 ID**: `ARCH-EPHEMERAL-SCHEMA-PERSISTENCE-1`
> - **관련 규칙**: `[RULE:AtomicSaves]`, `[RULE:RadicalKISS]`, `[RULE:ExplicitError]`, `[RULE:PlanPersistence]`

---

## 1. 배경 및 문제 정의 (Background & Problem Statement)

### 1.1 인메모리 엔진(`memengine`)의 영속성 딜레마
1. **디스크 영속성 계층의 복잡도 집중**:
   - `memengine`의 본질적인 강점은 샤드 해시맵, Lock-Free/RWLock SkipList, 그리고 낙관적 동시성 제어(OCC)를 통한 **초저지연 순수 메모리 연산**에 있다.
   - 그러나 `memengine`에 디스크 영속성(AOF WAL `wal.aof` + 전체 메모리 스냅샷 `tables.mpk`)을 부여하면서, WAL 프레임 CRC32 검증, Torn Write 절단, Dual-WAL 로테이션(`wal.aof.compacting`), 전체 테이블 직렬화 덤프, 디렉토리 `fsync`, 복구 중 2차 장애(Double-Crash) 방어 등 디스크 스토리지 엔진의 모든 복잡도가 인메모리 엔진에 중복 구현되는 구조적 부담이 발생했다.
2. **대용량 영속성 워크로드에서의 WiredTiger 우위**:
   - 페이지 단위 B+Tree 체크포인트와 MVCC를 수행하는 `WiredTiger`는 데이터가 수십 GB로 증가해도 더티 페이지(Dirty Page)만 증분 플러시하며, `FsyncAlways` 기준 TPC-C 벤치마크에서도 `WiredTiger`(28,436 TPS)가 `memengine`(26,027 TPS)보다 높은 처리량과 안정적인 P99 레이턴시를 제공한다.

### 1.2 기존 `-ephemeral` 모드의 치명적 한계 (스키마 및 카탈로그 증발)
1. **현상**:
   - 기존 `cmd/lionsql/server.go`에서 `-ephemeral` 플래그로 서버를 기동하면 `opts.DataDir = ""`로 설정하고 `cat.SetPersistenceDir(...)` 호출을 건너뛰어, 테이블의 **로우 데이터(Row Data)**뿐만 아니라 **테이블 스키마(`TableSchema`), 보조 인덱스 정의(`IndexSchema`), 저장 프로시저(`CREATE PROCEDURE`), 뷰(`VIEW`), 시퀀스(`SEQUENCE`), 트리거(`TRIGGER`), 사용자 권한(`RBAC`)**까지 모두 메모리에만 존재하다가 서버 재기동 시 완전히 증발했다.
2. **운영 환경에서의 문제점**:
   - 캐시, 세션 스토어, 실시간 집계 버퍼, 고속 스테이징 용도로 `-ephemeral` 모드를 사용하는 운영 환경에서 서버가 재기동되면, 로우 데이터가 비워지는 것은 의도된 동작이지만 **테이블과 저장 프로시저 정의 자체까지 사라져** 연결된 모든 애플리케이션과 워커가 `table or procedure does not exist` 에러로 즉시 장애에 빠진다.
   - 이를 피하기 위해 재기동마다 외부에서 DDL 마이그레이션 스크립트를 다시 실행해야 하는 비현실적인 운영 부담이 발생한다.

---

## 2. 산업계 표준 레퍼런스 및 핵심 설계 원칙

### 2.1 산업계 표준 아키텍처 (`SCHEMA_ONLY` / `ENGINE=MEMORY`)
1. **Microsoft SQL Server In-Memory OLTP (Hekaton)**:
   - 메모리 최적화 테이블 생성 시 `WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_ONLY)` 옵션을 제공한다.
   - `SCHEMA_ONLY` 테이블은 DML(`INSERT`, `UPDATE`, `DELETE`)에 대해 트랜잭션 로그 기록이나 체크포인트 I/O를 전혀 수행하지 않아 극한의 인메모리 성능을 제공한다.
   - 반면 **테이블 스키마, 인덱스 구조, 네이티브 컴파일 저장 프로시저 메타데이터는 시스템 카탈로그에 영구 저장**되어, SQL Server 재시작 시 모든 테이블과 인덱스가 **빈 테이블(0 rows) 상태로 즉시 복원**된다.
2. **MySQL `MEMORY` (`HEAP`) 스토리지 엔진**:
   - 데이터 행(Row)은 100% RAM에 보관하여 디스크 I/O가 없으며 서버 재시작 시 비워진다.
   - 반면 테이블 및 인덱스 스키마 정의는 디스크 카탈로그(`.frm` / Data Dictionary)에 영구 보존되어 서버 재기동 직후에도 동일한 테이블 구조가 그대로 유지된다.

### 2.2 LionSQL 스토리지 엔진 역할 분담 정립 (Role Separation before 1.0-GA)

| 구분 | 주력 역할 및 권장 워크로드 | 스키마/카탈로그 영속성 | 로우 데이터(Row) 영속성 |
| :--- | :--- | :---: | :---: |
| **1. `WiredTiger`** (`-disk=wiredtiger`) | **Production Persistent OLTP 주력 엔진**<br>- 대규모 트랜잭션 데이터, 금융/원장/비즈니스 영구 데이터<br>- 페이지 단위 B+Tree 체크포인트 및 MVCC | **영구 보존**<br>(B+Tree `_ lion_meta`) | **영구 보존**<br>(MVCC + WAL) |
| **3. `memengine` (`SCHEMA_ONLY` - `memengine` 기본값)** | **인메모리 OLTP 주력 엔진 (`-engine=memory` 기본)**<br>- 세션 스토어, 실시간 리더보드, 캐시, 고속 스테이징<br>- DML 디스크 I/O **0%** (순수 RAM 속도) | **영구 보존**<br>(`schemas.mpk` + 카탈로그) | **휘발성 (Ephemeral)**<br>(재기동 시 0 rows 초기화) |
| **4. `memengine` (`SCHEMA_AND_DATA` - 명시적 옵션)** | **단일 바이너리(`CGO_ENABLED=0` / Windows 폴백) 영속 엔진**<br>- CGO 미지원 환경 또는 명시적 인메모리 영속 옵션 지정 시<br>- AOF(`wal.aof`) + RDB 스냅샷(`tables.mpk`) | **영구 보존**<br>(`tables.mpk` + `wal.aof`) | **영구 보존**<br>(`tables.mpk` + `wal.aof`) |
| **5. `memengine` Anonymous** (`DataDir == ""`) | **순수 익명 메모리 엔진 (단위 테스트 / 내부 임시 버퍼)**<br>- `sqllogictest`, 단위 테스트, `#temp` / `@table_var` | **휘발성**<br>(디스크 디렉토리 없음) | **휘발성**<br>(디스크 디렉토리 없음) |

### 2.3 1.0-GA 아키텍처 결단 및 조기 전환(Early Transition) 원칙
1. **LionSQL 전체 기본 엔진과 `memengine` 기본 모드의 명확한 분리**:
   - **LionSQL의 기본 스토리지 엔진(`DefaultEngine` / `DefaultDisk`)은 이미 `WiredTiger`**이다. 따라서 디스크에 스키마와 데이터를 모두 영구 보존해야 하는 표준 OLTP 워크로드는 기본적으로 `WiredTiger`가 전담한다.
   - 사용자가 기본값(`WiredTiger`) 대신 **`-engine=memory` (`memengine`)를 명시적으로 선택했다는 것은 디스크 WAL/스냅샷 오버헤드 없는 순수 인메모리 처리 성능을 의도한 것**이다.
   - 따라서 `memengine`은 SQL Server In-Memory OLTP와 같이 `SCHEMA_ONLY`와 `SCHEMA_AND_DATA` 두 가지 영속성 수준을 모두 제공하되, **`memengine`의 기본값(Default)을 `SCHEMA_ONLY` (스키마 및 카탈로그만 디스크 영속화, 로우 데이터는 100% RAM 휘발성)**로 설정하고 `SCHEMA_AND_DATA`(WAL + RDB)는 명시적 옵션(또는 WiredTiger가 없는 Windows 폴백)으로 제공한다.
2. **1.0 이전 조기 전환(Early Switch) 전략**:
   - 이 기본값 전환은 `1.0-GA` 직전에 급박하게 바꾸는 것이 아니라, **`v0.93.0` 릴리스 직후 차기 마일스톤(`v0.94.0` / `1.0-GA` 초입)에서 가장 먼저 전환**하여 `1.0-GA`에 도달하기 전까지 충분한 통합 테스트 및 운영 검증 기간을 확보한다.

---

## 3. 상세 기술 설계 (Detailed Architecture Design)

### 3.1 `memengine`의 3단계 영속성 모드 구분
`pkg/storage/memengine`의 `Options` 및 동작 모드를 다음과 같이 명확히 정의한다:

1. **Persistent Mode (`DataDir != "" && (EnableAOF || EnableRDB)`)**:
   - 기존과 동일하게 스키마와 로우 데이터 모두 `tables.mpk` 및 `wal.aof`를 통해 영구 보존한다.
2. **Ephemeral Schema-Only Mode (`DataDir != "" && !EnableAOF && !EnableRDB`)**:
   - **로우 데이터(DML) 무(無)-I/O**:
     - `tx.Commit()` 시 `e.wal == nil`이므로 WAL 프레임 직렬화, `write(2)`, `fsync(2)`, 백그라운드 RDB 스냅샷 루프(`bgSaveLoop`)를 전혀 수행하지 않는다 (`wal.aof`, `tables.mpk` 생성 안 함).
   - **스키마 정의(DDL) 원자적 영속화 (`<dataDir>/schemas.mpk`)**:
     - 사용자 테이블(`!storage.IsTemporaryTable(name)`)에 대해 DDL이 실행될 때만 테이블 스키마(`storage.TableSchema`)와 보조 인덱스 정의(`storage.IndexSchema` 맵)를 `<dataDir>/schemas.mpk` 파일에 원자적으로 저장(`writeFileAtomicDurable`: `schemas.mpk.tmp` $\rightarrow$ `f.Sync()` $\rightarrow$ `os.Rename` $\rightarrow$ `syncDir`)한다.
     - 대상 DDL 메서드:
       - `CreateTable`
       - `DropTable`
       - `RenameTable`
       - `CreateIndex`
       - `DropIndex`
       - `AlterTable` / `AddColumn` / `DropColumn`
     - `TruncateTable`, `DropPartition` 등 데이터만 비우는 작업은 스키마 변경이 없으므로 디스크 쓰기를 수행하지 않는다.
   - **엔진 기동(`OpenWithOptions`) 시 스키마 자동 복원**:
     - `DataDir != ""`이고 `!EnableAOF && !EnableRDB`인 경우, `<dataDir>/schemas.mpk` 파일이 존재하면 이를 읽어 각 테이블의 `TableData`(빈 64개 샤드 배열)와 빈 보조 인덱스 트리(`NewIndexData`) 및 외래키 캐시(`rebuildFKCacheLocked`)를 메모리에 즉시 재구성한다.
     - 만약 `schemas.mpk` 파일이 존재하지만 손상되었거나 0바이트 절단된 경우에는 `tables.mpk`와 동일하게 즉시 에러를 반환하여 Fail-Closed 원칙(`[RULE:ExplicitError]`)을 준수한다.
3. **Pure Anonymous In-Memory Mode (`DataDir == ""`)**:
   - 물리적 디렉토리 자체가 지정되지 않은 순수 익명 인메모리 모드로서, 어떠한 파일도 생성하거나 읽지 않는다.

### 3.2 `schemas.mpk` 직렬화 포맷 (`SchemaSnapshotContainer`)
`pkg/storage/memengine/persistence.go`에 경량 스키마 전용 컨테이너 구조체를 정의한다:

```go
// TableSchemaEntry stores only the DDL metadata of a table (schema and index definitions)
// with zero row payloads, used for SCHEMA_ONLY persistence in ephemeral mode.
type TableSchemaEntry struct {
    Schema  storage.TableSchema            `msgpack:"schema"`
    Indexes map[string]storage.IndexSchema `msgpack:"indexes,omitempty"`
}

// SchemaSnapshotContainer encapsulates all persistent table & index definitions on disk.
type SchemaSnapshotContainer struct {
    Version int                         `msgpack:"version"`
    Tables  map[string]TableSchemaEntry `msgpack:"tables"`
}
```
- 임시 테이블(`#temp`, `##global_temp`, `@table_var` / `__tv_*`)은 `saveSchemaSnapshotLocked()` 대상에서 철저히 제외한다.
- DDL은 실행 빈도가 매우 낮고 `schemas.mpk` 파일 크기는 수백 개의 테이블이 있어도 수십 KB 이내이므로, `writeFileAtomicDurable`을 통한 동기식 원자적 저장이 수 밀리초 이내에 완료된다.

### 3.3 서버(`cmd/lionsql/server.go`) 및 시스템 카탈로그(`pkg/catalog`) 연동
`cmd/lionsql/server.go`에서 `-ephemeral` 플래그가 지정되었을 때의 초기화 경로를 다음과 같이 교정한다:

1. **데이터 디렉토리 및 디렉토리 락(`dirlock`) 유지**:
   - `cfg.Server.DataDir`가 지정되어 있으면 `-ephemeral` 모드에서도 `dirlock.Acquire(cfg.Server.DataDir)`를 획득하여, 두 프로세스가 동시에 동일한 디렉토리의 `schemas.mpk` 및 카탈로그 파일(`procedures.json` 등)을 덮어쓰는 경쟁 조건을 방지한다.
2. **`memengine` 옵션 전달**:
   - 기존에는 `cfg.Server.Ephemeral == true`일 때 `opts.DataDir = ""`로 지워버렸으나, 이를 **`opts.DataDir = cfg.Server.DataDir`, `opts.EnableAOF = false`, `opts.EnableRDB = false`**로 전달한다.
3. **시스템 카탈로그(`pkg/catalog`) 영속화 활성화**:
   - 기존 `if !cfg.Server.Ephemeral && cfg.Server.DataDir != ""` 조건에서 `!cfg.Server.Ephemeral` 제약을 제거하고 **`if cfg.Server.DataDir != ""`**일 때 항상 `cat.SetPersistenceDir(cfg.Server.DataDir)`를 호출한다.
   - 이를 통해 `-ephemeral` 모드에서도 **저장 프로시저(`procedures.json`), 뷰(`views.json`), 트리거(`triggers.json`), 시퀀스(`sequences.json`), 사용자定義 타입/함수**가 재기동 후에도 100% 보존된다.

---

## 4. 구현 및 검증 단계 (Implementation & Verification Plan)

### Step 1: `pkg/storage/memengine` 스키마 영속화 구현
1. `pkg/storage/memengine/persistence.go`에 `saveSchemaSnapshotLocked()` 및 `loadSchemaSnapshotLocked()` 구현 (`writeFileAtomicDurable` 재사용).
2. `OpenWithOptions`에서 `opts.DataDir != ""`일 때:
   - `dirlock.Acquire(e.dataDir)` 획득 (Ephemeral Schema-Only 모드 포함).
   - `!opts.EnableRDB && !opts.EnableAOF`인 경우 `loadSchemaSnapshotLocked()`를 호출하여 `<dataDir>/schemas.mpk`로부터 빈 테이블 및 인덱스 스키마 복원.
3. `ddl.go` 및 `ddl_alter.go`의 `CreateTable`, `DropTable`, `RenameTable`, `CreateIndex`, `DropIndex`, `AlterTable`에서 `!e.opts.EnableAOF && !e.opts.EnableRDB && e.dataDir != ""`일 때 `saveSchemaSnapshotLocked()`를 호출(실패 시 인메모리 DDL 상태 롤백 보장).
4. `Close()` 시에도 Ephemeral Schema-Only 모드(`e.dataDir != ""`)면 `saveSchemaSnapshotLocked()`를 호출하여 스키마 일관성 보장.

### Step 2: `cmd/lionsql/server.go` 연동
1. `-ephemeral` 모드에서도 `opts.DataDir = cfg.Server.DataDir`를 유지하고 `cat.SetPersistenceDir(cfg.Server.DataDir)`를 활성화.
2. 기동 배너 및 로그 메시지를 `"EPHEMERAL SCHEMA_ONLY mode (persistent schema & catalog, pure in-memory ephemeral rows)"`로 명확히 표시.

### Step 3: 단위 및 통합 회귀 테스트 작성 (`pkg/storage/memengine/ephemeral_test.go`)
1. **`TestEphemeralEngine_SchemaPersistsAcrossRestartWithZeroRows`**:
   - `DataDir: tempDir, EnableAOF: false, EnableRDB: false`로 엔진 오픈.
   - 테이블(`users`), 보조 유니크 인덱스(`idx_users_email`) 생성 및 100건 `INSERT` 커밋.
   - `wal.aof` 및 `tables.mpk`는 디스크에 생성되지 않고 `schemas.mpk`만 존재하는지 검증.
   - 엔진 `Close()` 후 새 엔진 인스턴스로 동일 `tempDir` 재오픈.
   - `users` 테이블 스키마와 `idx_users_email` 인덱스가 100% 그대로 존재하며, 로우 수는 `0`건인지 검증.
   - 재오픈 직후 `CREATE TABLE` 없이 곧바로 `INSERT` 및 유니크 인덱스 제약 조건 검사가 정상 작동하는지 검증.
2. **`TestEphemeralEngine_CorruptedSchemaSnapshotFailsClosed`**:
   - `schemas.mpk`가 0바이트이거나 손상되었을 때 `OpenWithOptions`가 무음 진행하지 않고 즉시 에러를 반환하는지 검증.
3. **`TestEphemeralEngine_PureMemoryNoDir`**:
   - `DataDir: ""`인 순수 익명 메모리 모드는 기존과 동일하게 디스크 접근 없이 동작하는지 검증.
