# Dual-Path Wire Protocol Symmetry Universal Gate Test Suite 아키텍처 계획 (`GATE-PROTOCOL-SYMMETRY-1`)

## 1. 개요 및 배경 (Context & Motivation)

### 1.1 배경
LionSQL은 엔터프라이즈 환경과의 100% 호환을 위해 두 가지 핵심 와이어 프로토콜을 동시에 제공합니다:
1. **Microsoft SQL Server TDS 7.x/8.0 (포트 1433)**: SSMS, DBeaver, ADO.NET, Go `go-mssqldb`
2. **PostgreSQL Wire Protocol v3.0 (포트 5433)**: DBeaver, GORM, Prisma, Go `pgx`, `lib/pq`

그러나 각 프로토콜은 단일한 호출 방식이 아닌 **이원화된 진입 경로(Dual-Path)**를 가집니다:
- **TDS**: SQL Batch (`PacketSQLBatch 0x01`, e.g. `EXEC sp @a = 1`) vs RPC Fast-Path (`PacketRPC 0x03`, e.g. `db.Query("sp", sql.Named("a", 1))`)
- **PGWire**: Simple Query (`'Q'`, e.g. `EXEC sp 1;`) vs Extended Query (`'P' -> 'B' -> 'D' -> 'E' -> 'S'`, e.g. `db.Query("EXEC sp $1", 1)`)

### 1.2 문제의 본질
과거 개발 및 단위 테스트 과정에서는 대부분 직관적인 **단일 경로(SQL Batch 또는 Simple Query)**만을 사용하여 기능을 작성하고 검증했습니다. 그 결과, 실제 프레임워크(GORM, Hibernate, DBeaver)가 **다른 경로(RPC 또는 Extended Query)**를 호출하거나, 매개변수가 0개이거나, 세미콜론이 붙는 등의 경계 조건에서:
- 다중 결과셋의 후속 결과셋 유실 (`res.Next` 미처리)
- 바이트코드 컴파일 결과셋 메타데이터 누락으로 인한 `NoData ('n')` 반환
- `sp_` 접두사를 가진 사용자 정의 프로시저의 무조건적 시스템 프로시저 오분류
등 치명적인 프로토콜 비대칭 결함이 발생했습니다.

### 1.3 해결책: 1.0 릴리스 차단용 유니버설 게이트 테스트 (Universal Gate Test)
1.0 배포 전, 모든 쿼리/프로시저/트랜잭션 시나리오가 **어느 경로로 인입되든 100% 동일한 결과셋, 동일한 메타데이터, 동일한 트랜잭션 격리성을 보장**하도록 강제하는 **단일 통합 게이트 테스트 스위트 (`gate_protocol_symmetry_test.go`)**를 구축합니다.

---

## 2. 4-분면 대칭성 매트릭스 (4-Quadrant Symmetry Matrix)

| 구분 | TDS 프로토콜 (포트 1433) | PGWire 프로토콜 (포트 5433) |
| :--- | :--- | :--- |
| **Path A (텍스트/배치 경로)** | **SQL Batch (`PacketSQLBatch 0x01`)**<br>`EXEC sp_name @p1 = 10, @p2 = 'abc';` | **Simple Query (`'Q'`)**<br>`EXEC sp_name 10, 'abc';` |
| **Path B (바이너리/준비된 경로)** | **RPC Fast-Path (`PacketRPC 0x03`)**<br>`db.Query("sp_name", sql.Named("p1", 10), ...)` | **Extended Query (`'P'->'B'->'D'->'E'->'S'`)**<br>`db.Query("EXEC sp_name $1, $2;", 10, "abc")` |
| **동등성 불변식** | 결과셋 수, 컬럼 수, 데이터 타입, 로우 데이터, 에러 상태가 **Path A == Path B** 100% 일치해야 함. | 결과셋 수, 컬럼 수, 데이터 타입, 로우 데이터, 에러 상태가 **Path A == Path B** 100% 일치해야 함. |

---

## 3. 게이트 테스트 검증 시나리오 (6대 핵심 워크플로)

게이트 테스트는 다음 6가지 핵심 시나리오를 TDS와 PGWire 각각에 대해 Path A와 Path B로 동시 실행하고 교차 검증합니다:

### 시나리오 1: 단일 결과셋 및 다양한 매개변수 타입 (Scalar & Types)
- **대상**: 정수(`INT`), 문자열(`VARCHAR`), `NULL` 값을 인수로 받는 프로시저.
- **검증**: 단일 결과셋 반환 시 컬럼 이름, OID/TDS 타입, 레코드 값의 1:1 일치.

### 시나리오 2: 다중 결과셋 (Multiple Result Sets - 2셋 및 3셋)
- **대상**: 서로 다른 스키마를 가진 2개 이상의 `SELECT`를 순차 실행하는 프로시저.
- **검증**: `rows.NextResultSet()`를 통한 순차 스트리밍, 결과셋별 컬럼 메타데이터 분리, 전체 결과셋 완주 보장.

### 시나리오 3: 제로 파라미터 및 구문 정규화 (Zero-Param & Semicolon)
- **대상**: `sp_` 접두사를 가진 0-파라미터 프로시저(`sp_status_summary`), 끝에 세미콜론이 붙은 호출(`EXEC sp_name;`).
- **검증**: VDBE 바이트코드 컴파일 캐시 정상 적중 및 빈 파라미터 슬롯 처리.

### 시나리오 4: 클라이언트 트랜잭션 내 프로시저 DML + COMMIT (Tx Commit)
- **대상**: `tx.Begin()` -> `EXEC sp_update_account` -> `tx.Commit()`.
- **검증**: 트랜잭션 변경 사항이 물리 저장소에 영구 커밋되고, 커넥션 풀이 안전하게 반환됨.

### 시나리오 5: 클라이언트 트랜잭션 내 프로시저 DML + ROLLBACK (Tx Rollback)
- **대상**: `tx.Begin()` -> `EXEC sp_update_account` -> `tx.Rollback()`.
- **검증**: 프로시저 내부의 모든 변경 사항이 원자적으로 취소(0건 반영)되고, 스토리지 정합성 유지.

### 시나리오 6: 프로시저 실패 시 에러 전파 및 커넥션 안전성 (Error Propagation & Safety)
- **대상**: 중복 키 위반 또는 명시적 에러(`RAISERROR` / `THROW`)를 발생시키는 프로시저.
- **검증**: 에러 메시지가 클라이언트에 명시적으로 전달되고, 와이어 프로토콜 상태가 오염되지 않아(No Connection Poisoning) 동일 커넥션에서 후속 쿼리가 정상 실행됨.

---

## 4. 구조 설계 및 구현 전략

### 4.1 파일 배치
- **위치**: `test/spec_defects/gate_protocol_symmetry_test.go`
- **선정 이유**:
  - `test/spec_defects/` 패키지는 이미 TDS와 PGWire 드라이버(`go-mssqldb`, `pgx`)를 모두 임포트하고 있으며 독립 포트 바인딩 테스트 인프라가 구축되어 있음.
  - `scripts/release.sh` 및 CI 파이프라인의 핵심 검증 단계(`go test -tags wiredtiger -race -count=1 ./test/spec_defects/...`)에 기본 포함되어 있어 **배포 시 무조건 실행되는 차단 게이트** 역할을 수행함.

### 4.2 공통 대칭성 검증 엔진 (`assertResultSetSymmetry`)
```go
type CanonicalResultSet struct {
    Columns []string
    Rows    [][]any
}

func extractResultSets(t *testing.T, rows *sql.Rows) []CanonicalResultSet
func assertResultSetSymmetry(t *testing.T, modeA string, resA []CanonicalResultSet, modeB string, resB []CanonicalResultSet)
```
- 결과셋의 개수(`len(resA) == len(resB)`).
- 각 결과셋의 컬럼 메타데이터(`Columns`).
- 각 행의 로우 수 및 셀 데이터(`fmt.Sprintf("%v", val)` 기반 정규화 비교).
- 두 경로의 결과가 단 1비트라도 다르면 `t.Fatalf`로 즉시 게이트 차단.

---

## 5. TDD 실행 일정 (Pipeline)

1. **Step 1 (Red)**: `test/spec_defects/gate_protocol_symmetry_test.go` 작성 및 6대 시나리오 테스트 골격 구현.
2. **Step 2 (Green)**: 게이트 테스트 실행하여 TDS(Batch vs RPC) 및 PGWire(Simple vs Extended) 100% PASS 확인.
3. **Step 3 (Refactor/Verification)**:
   - `scripts/release.sh`의 테스트 파이프라인에 반영 확인.
   - `[RULE:PipelineVerification]` 준수 (Build -> Test -race -> Commit).
