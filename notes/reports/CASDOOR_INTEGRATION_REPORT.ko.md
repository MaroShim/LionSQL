# Casdoor 듀얼 프로토콜 실전 연동 검증 보고서 (`CASDOOR-INTEGRATION-1`)

> - **최종 갱신일**: 2026-10-31
> - **최초 작성일**: 2026-10-31
> - **작업 식별자**: `CASDOOR-INTEGRATION-1`
> - **상태**: 완료 (`[x]`)

---

## 1. 개요 및 목표

오픈소스 IAM/SSO/OIDC 플랫폼인 **Casdoor**(`github.com/casdoor/casdoor`, Beego + XORM + Casbin 기반)를 **LionSQL**과 연동하여:
1. **PostgreSQL Wire Protocol v3 (`:5433`, `driverName = postgres`)**
2. **Microsoft SQL Server TDS 7.x/8.0 (`:1433`, `driverName = mssql`)**
두 가지 프로토콜 모두에서 Casdoor 소스 코드 수정 없이 44개 테이블 자동 마이그레이션(`Sync2`), 초기 시드 데이터 적재(조직, 관리자, RSA-4096 X.509 인증서, Casbin 정책), OIDC/JWKS 엔드포인트 및 REST CRUD API가 100% 동작함을 검증했습니다.

---

## 2. 발굴된 핵심 엔진 병목 및 해결 내역

### 2.1 복합 기본 키(`Composite Primary Key`) 스토리지 키 합성 지원
- **원인**: Casdoor는 거의 모든 핵심 테이블(`organization`, `user`, `application`, `provider`, `cert`, `session` 등 ~40개 테이블)을 `PRIMARY KEY (owner, name)` 또는 `PRIMARY KEY (owner, name, application)` 형태의 **복합 기본 키(Composite Primary Key)** 로 정의합니다. 기존 `TableSchema.ResolveRecordPK`와 스토리지 엔진(`wiredtiger`, `memengine`)의 `Insert` 경로는 첫 번째 PK 컬럼(`owner`)만 추출하여 스토리지 키로 사용했기 때문에, 동일한 `owner`(`admin`)를 가진 두 번째 행(`admin/provider_balance`) 삽입 시 `duplicate key value violates unique constraint` 충돌이 발생했습니다.
- **해결**:
  - `pkg/storage/engine.go`: `TableSchema.IsCompositePK() bool`를 추가하고, `TableSchema.ResolveRecordPK(rec)`가 복합 PK 테이블의 경우 모든 PK 컬럼 값을 ASCII Unit Separator(`"\x1f"`, WiredTiger C-문자열 `key_format=S` 안전 구분자)로 결합한 복합 키를 반환하도록 개선했습니다.
  - `pkg/storage/wiredtiger/wt_engine.go`, `pkg/storage/memengine/tx.go`: `Insert`, `Update`, `checkUniqueIndexesLocked`에서 단일 `PrimaryKeyColumn()` 대신 `ResolveRecordPK(record)`를 사용하도록 일원화했습니다.
  - `pkg/proc/runtime/vm.go`, `pkg/engine/pk_bypass.go`, `pkg/engine/sqlexec_ddl.go`: 복합 PK 테이블에서 단일 컬럼 조건(`WHERE name = 'provider_balance'`)이 전체 PK 단건 조회(`tx.Get`)로 오인되어 누락되지 않도록 `schema.IsCompositePK()` 가드를 추가했습니다.

### 2.2 MSSQL TDS `0x` 바이너리 리터럴 토크나이징 및 `TypeBigVarBin` 라운드트립 보정
- **원인**: XORM의 MSSQL 방언은 Go 구조체 슬라이스(`Addresses`, `WebauthnCredentials`, `ManagedAccounts`, `MfaAccounts`)를 `VARBINARY(50)` 컬럼에 JSON 바이트(`[]byte("null")` -> `0x6e756c6c`)로 저장합니다.
  1. Tier-1 렉서(`pkg/proc/lexer.go`)에서 언쿼티드 `0x6e756c6c` 리터럴을 단일 토큰으로 인식하지 못해 VDBE DML에서 바이너리 변환이 누락되었습니다.
  2. TDS 응답 인코더(`pkg/wire/tds/server.go`, `token.go`)에서 `"0x6e756c6c"` 문자열 표현을 `TypeNVarChar`로 송신하여 `go-mssqldb`가 원시 바이트(`null`) 대신 `"0x6e756c6c"` 바이트열을 반환함으로써 `json.Unmarshal` 시 `invalid character 'x' after top-level value` 에러가 발생했습니다.
- **해결**:
  - `pkg/proc/lexer.go`, `pkg/proc/runtime/vm_dispatcher.go`, `pkg/proc/runtime/vm.go`: `0x[0-9a-fA-F]+` 리터럴을 토큰화하고 `TypeBinary` 컬럼 대입 시 `storage.BinaryValueFromString`으로 변환하도록 보강했습니다.
  - `pkg/wire/tds/server.go`, `pkg/wire/tds/token.go`: `decodeHexBinaryString`을 통해 `"0x..."` 바이너리 데이터를 `TypeBigVarBin`(`0xA5`) 원시 바이트로 디코딩하여 전송하고, 일반 문자열(`created_time`, `id`)이 `TypeTimeN`이나 `TypeGuid` 원시 바이트로 왜곡되지 않도록 타입 추론 정확도를 높였습니다.

---

## 3. 검증 요약

| 검증 항목 | Mode A: PostgreSQL (`:5433`) | Mode B: MSSQL TDS (`:1433`) |
| :--- | :--- | :--- |
| **XORM `Sync2` 44개 테이블 및 인덱스 생성** | PASS (100%) | PASS (100%) |
| **초기 시드 데이터 (`built-in`, `admin`, `cert-built-in`, Casbin 정책)** | PASS (100%) | PASS (100%) |
| **OIDC Discovery (`/.well-known/openid-configuration`) & JWKS** | PASS (`HTTP 200 OK`) | PASS (`HTTP 200 OK`) |
| **관리자 로그인 (`POST /api/login`) 및 세션 조회 (`/api/get-account`)** | PASS (`status: "ok"`) | PASS (`status: "ok"`) |
| **신규 조직/애플리케이션/사용자 생성 및 조회 CRUD** | PASS (`status: "ok"`) | PASS (`status: "ok"`) |
| **3-Stage Verification (`go test -tags wiredtiger -race ./...`)** | PASS (0 Race) | PASS (0 Race) |
