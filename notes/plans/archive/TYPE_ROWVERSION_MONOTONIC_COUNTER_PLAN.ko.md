# [PLAN] ROWVERSION 데이터 타입 DDL 파싱 및 INSERT/UPDATE 시 @@DBTS 단조 증가 바이너리 자동 스탬핑

## 1. 개요 및 배경
- **태스크 ID**: `FEAT-TYPE-ROWVERSION-MONOTONIC-COUNTER-1` (Defect 104)
- **목적**:
  - Microsoft SQL Server의 낙관적 동시성 제어(OCC, Optimistic Concurrency Control) 핵심 데이터 타입인 `ROWVERSION`을 지원.
  - DDL 파서에서 `ROWVERSION` 타입을 인식하여 테이블 스키마에 8-byte 바이너리 자동 버전 컬럼(`IsRowVersion`)으로 등록.
  - 행 생성(INSERT) 및 수정(UPDATE) 시 데이터베이스 전역의 단조 증가 카운터(`@@DBTS`)를 1 증가시키고 해당 8바이트 Big-Endian 바이너리(`0x00000000000007D1...`)를 자동으로 스탬핑.
  - 시스템 카탈로그(`sys.types`, `sys.columns`)에서 `ROWVERSION` (system_type_id: 189, user_type_id: 189, max_length: 8)으로 정확히 표출.
  - 단일 테이블당 최대 1개의 ROWVERSION 컬럼 제약 준수.

---

## 2. 상세 변경 설계

### 2.1 스토리지 모델 및 컬럼 스키마 (`pkg/storage`)
1. **`Column` 구조체 확장 (`pkg/storage/engine.go`)**:
   - `IsRowVersion bool` 필드 추가.
   - `TableSchema.RowVersionColumn() (*Column, int, bool)` 헬퍼 메서드 추가: 테이블 내 `IsRowVersion == true`인 컬럼을 빠르게 조회.
2. **글로벌 DBTS 시퀀스 발급 (`pkg/proc/builtins_sys.go`)**:
   - `NextRowVersion() []byte`: 원자적으로 `AdvanceDBTS()`를 호출하고 8바이트 Big-Endian 바이너리 슬라이스를 생성하여 반환.

### 2.2 DDL 파서 (`pkg/engine`, `pkg/proc`)
1. **DDL 파서 (`pkg/engine/sqlexec_ddl.go`)**:
   - `parseSQLDataType("ROWVERSION")` -> `storage.TypeBinary`.
   - 컬럼 파싱 시 `rawType == "ROWVERSION"`인 경우:
     - `c.Type = storage.TypeBinary`
     - `c.IsRowVersion = true`
     - `c.Nullable = false`
     - `c.Precision = 8`
     - 한 테이블 내 중복 ROWVERSION 컬럼 정의 방지 (최대 1개).
2. **프로시저 파서 (`pkg/proc/parser.go`, `pkg/proc/parser/antlr/ast_builder.go`)**:
   - `parseDataType`: `"ROWVERSION"` -> `storage.TypeBinary`.

### 2.3 INSERT / UPDATE DML 파이프라인 자동 스탬핑
1. **INSERT 경로 (`pkg/engine/sqlexec_insert.go`, `pkg/proc/runtime/vm_dispatcher.go`)**:
   - 행 삽입 시 스키마에 `IsRowVersion` 컬럼이 존재하면, 사용자가 값을 제공하지 않았거나 임의의 값을 지정했더라도 무조건 `proc.NextRowVersion()`으로 8바이트 바이너리 값을 자동 할당.
2. **UPDATE 경로 (`pkg/engine/sqlexec_update.go`, `pkg/storage/memengine/tx.go`, `pkg/storage/wiredtiger/tx.go`, `pkg/proc/runtime/vm_dispatcher.go`)**:
   - 행 갱신 시 `IsRowVersion` 컬럼이 존재하면, 해당 컬럼의 값을 새로운 `proc.NextRowVersion()`으로 갱신하여 낙관적 동시성 검증을 보장.

### 2.4 시스템 카탈로그 및 와이어 프로토콜
1. **`sys.types` (`pkg/catalog/virtual/mssql_single.go`)**:
   - `rowversion` (system_type_id: 189, user_type_id: 189, max_length: 8, precision: 0, scale: 0) 등록.
2. **`sys.columns` (`pkg/catalog/virtual/mssql_single.go`)**:
   - `c.IsRowVersion`인 경우 `system_type_id: 189, user_type_id: 189, max_length: 8, data_type: "rowversion"` 반환.

---

## 3. 검증 계획
1. **단위 테스트 (`pkg/engine/rowversion_test.go`)**:
   - `CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(50), rv ROWVERSION);`
   - 한 테이블에 2개의 ROWVERSION 컬럼 생성 시도 시 오류 확인.
   - `INSERT INTO t (id, name) VALUES (1, 'Alice');`
     - `rv` 컬럼에 `0x00...` 8바이트 바이너리가 자동 할당되고, `SELECT @@DBTS`와 일치하는지 확인.
   - `INSERT INTO t (id, name) VALUES (2, 'Bob');`
     - 행 2의 `rv` 값이 행 1의 `rv` 값보다 엄격하게 큰지(단조 증가) 확인.
   - `UPDATE t SET name = 'Alice_v2' WHERE id = 1;`
     - 행 1의 `rv` 값이 갱신 전보다 엄격하게 증가했는지 확인.
2. **품질 파이프라인 검증 (`[RULE:PipelineVerification]`)**:
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
   - `go test -v -race ./pkg/storage ./pkg/engine ./pkg/wire/tds` (100% PASS, 0 races)
