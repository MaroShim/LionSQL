# [PLAN] REAL(4-byte float32) vs FLOAT(8-byte float64) 데이터 타입 분리 및 스토리지/프로토콜 정합성 보장

## 1. 개요 및 배경
- **태스크 ID**: `FIX-TYPE-REAL-FLOAT-DISTINCTION-1` (Defect 101)
- **목적**:
  - 기존 LionSQL에서 `REAL`과 `FLOAT`이 `TypeFloat` (8-byte IEEE 754 float64) 단일 타입으로 병합되어 있던 것을 완전히 분리하여, Microsoft SQL Server T-SQL 및 PostgreSQL 표준 스펙에 맞춰 `REAL` (4-byte IEEE 754 float32)과 `FLOAT` (8-byte IEEE 754 float64)을 명확히 구분.
  - V2 바이너리 튜플 코덱(`TupleCodecV2`)에서 디스크/메모리 저장 크기를 `REAL`은 정확히 4바이트, `FLOAT`은 8바이트로 격리하여 1.0-GA 이전 온디스크 바이너리 포맷의 영구 불변성(`[RULE:CharsetInvariance]`, Invariant 13) 확립.
  - 세컨더리 인덱스 B-Tree 키 인코딩(`storage.EncodeIndexKeyPart`)에서 `REAL`은 8-자리 16진수(4-byte sign-flipped hex), `FLOAT`은 16-자리 16진수(8-byte sign-flipped hex)로 분리 인코딩.
  - TDS 7.x/8.0 와이어 프로토콜 및 PGWire 프로토콜에서 올바른 타입 OID/타입 인포와 바이너리 크기를 반환하여 SSMS, go-mssqldb, pgx 등 클라이언트 드라이버와의 100% 호환성 확보.
  - 시스템 카탈로그 뷰(`sys.types`, `sys.columns`)에서 `REAL` (system_type_id: 59, max_length: 4, precision: 24)과 `FLOAT` (system_type_id: 62, max_length: 8, precision: 53)를 정확히 분리 표출.

---

## 2. 상세 변경 설계

### 2.1 스토리지 엔진 (`pkg/storage`)
1. **`DataType` 상수 정의 (`pkg/storage/engine.go`)**:
   - `TypeReal DataType = ...` 추가 (String(): `"REAL"`).
   - `RealValue(v float32) Value` 생성자 추가 (`FVal: float64(v)`, `IVal: int64(v)`).
   - `Value.Interface()`: `TypeReal`일 때 `float32(v.FVal)` 반환.
   - `FormatValue(v Value)`: `TypeReal` 지원 (`strconv.FormatFloat(v.FVal, 'g', -1, 32)`).
   - `ParsePKValue`, `CompareValues`, `CoerceValues`: `TypeReal`을 산술 연산 및 비교에서 일관되게 지원.
2. **V2 튜플 코덱 (`pkg/storage/tuple.go`)**:
   - `EncodeTupleV2`: `case TypeReal:` -> 4-byte LittleEndian `math.Float32bits(float32(val.FVal))` 기록.
   - `DecodeColumnValueV2`: `case TypeReal:` -> 4-byte LittleEndian `math.Float32frombits` 디코딩 후 `RealValue(...)` 반환.
   - `SkipTupleColumn`: `case TypeReal:` -> `offset + 4`.
   - `decodeFixedColumnOffset`: `case TypeReal:` -> 고정 4바이트 전진.
3. **세컨더리 인덱스 코덱 (`pkg/storage/value_codec.go`)**:
   - `EncodeIndexKeyPart`: `case TypeReal:` -> `EncodeFloat32(float32(v.FVal))` 구현 (부호 비트 반전 후 8-자리 hex 문자열).

### 2.2 SQL 엔진 및 DDL 파서 (`pkg/engine`)
1. **DDL 타입 파싱 (`pkg/engine/sqlexec_ddl.go`)**:
   - `parseSQLDataType(s string)`:
     - `"REAL"`, `"FLOAT4"` -> `storage.TypeReal`
     - `"FLOAT"`, `"DOUBLE"`, `"FLOAT8"`, `"DOUBLE PRECISION"` -> `storage.TypeFloat`
     - `FLOAT(n)`: $n \le 24$ 이면 `TypeReal`, $n > 24$ 이면 `TypeFloat`.
2. **값 평가 및 변환 (`pkg/engine/evaluator.go`, `sqlexec_eval.go`)**:
   - `TypeReal`을 산술 연산(+, -, *, /, %) 및 조건 평가(=, <, >, <=, >=, <>)에서 `TypeFloat`와 동등한 부동소수점 산술 계열로 처리.
   - `CAST(... AS REAL)` 지원.

### 2.3 와이어 프로토콜 및 시스템 카탈로그
1. **TDS 와이어 프로토콜 (`pkg/wire/tds`)**:
   - `inferColumnInfos`: `col.Type == storage.TypeReal` 또는 값이 `float32`인 경우 `TypeFltN` (`0x6D`)에 `col.Size = 4` 설정.
   - `token.go`: `TypeFltN` 직렬화 시 `col.Size == 4`이면 4바이트 IEEE 754 기록, `col.Size == 8`이면 8바이트 IEEE 754 기록.
2. **PostgreSQL 와이어 프로토콜 (`pkg/wire`)**:
   - `typeToPgOID`: `storage.TypeReal` -> `700` (`FLOAT4`), `storage.TypeFloat` -> `701` (`FLOAT8`).
   - Row 직렬화 시 4바이트 IEEE 754 BigEndian 기록.
3. **시스템 카탈로그 (`pkg/catalog/virtual/mssql_single.go`)**:
   - `sys.types`: `real` (system_type_id: 59, user_type_id: 59, max_length: 4, precision: 24, scale: 0) 등록.
   - `sys.columns`: `TypeReal` 컬럼에 대해 `system_type_id: 59, user_type_id: 59, max_length: 4, precision: 24` 반환.

---

## 3. 검증 계획
1. **스토리지 단위 테스트 (`pkg/storage/real_float_test.go`)**:
   - V2 바이너리 튜플 인코딩 크기 검증: `REAL`이 정확히 4바이트를 차지하는지 확인 (전체 튜플 크기 델타 검증).
   - 인덱스 키 인코딩 검증: 양수, 음수, 0.0, NULL 정렬 순서 불변성 확인.
2. **TDS 와이어 프로토콜 단위 테스트 (`pkg/wire/tds/real_float_wire_test.go`)**:
   - `REAL`과 `FLOAT` 컬럼을 포함하는 테이블 생성 및 데이터 삽입 후 TDS 프로토콜 토큰 직렬화 검증.
   - `col.Size`가 각각 4와 8로 분리되어 클라이언트로 전송되는지 확인.
3. **엔진 통합 및 카탈로그 검증**:
   - `CREATE TABLE t (r REAL, f FLOAT);`
   - `SELECT name, system_type_id, max_length, precision FROM sys.columns WHERE object_id = OBJECT_ID('t');`
   - `sys.types` 조회 검증.
4. **품질 파이프라인 검증 (`[RULE:PipelineVerification]`)**:
   - `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
   - `go test -v -race ./pkg/storage ./pkg/wire/tds ./pkg/engine` (100% PASS, 0 races)
