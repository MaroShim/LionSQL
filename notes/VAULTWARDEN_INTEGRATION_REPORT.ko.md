# Vaultwarden 연동 검증 보고서 (`VAULTWARDEN_INTEGRATION_REPORT.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **과제 식별 코드**: `INTEG-VAULTWARDEN-1`
> - **연동 대상**: [Vaultwarden](https://github.com/dani-garcia/vaultwarden) (Rust + Diesel ORM, Bitwarden API 호환)
> - **연동 프로토콜**: PostgreSQL Wire Protocol v3 (`TCP :5433`)
> - **스토리지 엔진**: WiredTiger Lock-Free B+Tree MVCC Engine
> - **검증 결과**: **100% 통과 (Pass)** (Vaultwarden 소스 코드 변경: **0줄**)

---

## 1. 개요 및 연동 목표

Vaultwarden은 공식 Bitwarden 클라이언트(웹 볼트, 브라우저 확장 프로그램, 모바일 앱, 데스크톱 앱)와 100% 호환되는 Rust 기반의 경량 패스워드 관리 서버입니다. 백엔드 데이터베이스 계층으로 **Diesel ORM**(PostgreSQL 드라이버)을 사용하며, 엄격한 바이너리 직렬화와 복합 외래 키/기본 키 제약 조건을 활용합니다.

LionSQL의 PostgreSQL Wire 프로토콜 호환성 검증 트랙(`INTEG-VAULTWARDEN-1`)의 핵심 목표는 다음과 같습니다:
1. **Zero Source Modification**: Vaultwarden의 원본 Rust 코드 및 Diesel 마이그레이션 SQL을 단 한 줄도 수정하지 않고 표준 PostgreSQL 설정으로 100% 구동.
2. **Schema & Migration Integrity**: Vaultwarden의 47개 Diesel ORM 마이그레이션 스크립트 전수 무오류 자동 실행 및 29개 핵심 테이블 스키마 생성 완료.
3. **Core Workflow E2E**: 계정 등록, 사전 로그인(KDF 파라미터 교환), JWT 토큰 발급, 암호화된 사이퍼(비밀번호/보안노트) CRUD, 폴더 관리, 전체 동기화(`GET /api/sync`) 완벽 동작 검증.

---

## 2. 연동 아키텍처 및 설정

```
+-----------------------------------------------------------+
|             Vaultwarden Server (:8080)                    |
|      (Rust + Rocket Web Framework + Diesel ORM)           |
+-----------------------------------------------------------+
                             │
              (PostgreSQL Wire Protocol v3)
                             ▼
+-----------------------------------------------------------+
|             LionSQL PGWire Listener (:5433)               |
|  - Extended Query Protocol (Parse / Bind / Describe / Exec)|
|  - Tolerant Virtual USE & Catalog View Engine             |
|  - Zero-Allocation VDBE & DDL/DML Coordination Engines    |
|  - WiredTiger Lock-Free B+Tree MVCC Engine                |
+-----------------------------------------------------------+
```

### 환경 구성
- **LionSQL 데몬**:
  ```bash
  ./bin/lionsql -dir data_vaultwarden_pg -engine wiredtiger -pg 0.0.0.0:5433 -admin-pass="VaultwardenSecurePass2026!" -dev
  ```
- **Vaultwarden 컨테이너 (`vaultwarden/server:latest`)**:
  ```bash
  docker run -d --name vaultwarden_test \
    -e DATABASE_URL="postgresql://admin:VaultwardenSecurePass2026!@host.docker.internal:5433/vaultwarden" \
    -e SIGNUPS_ALLOWED="true" \
    -p 8080:80 \
    vaultwarden/server:latest
  ```

---

## 3. 발견된 호환성 이슈 및 LionSQL 해결 내역

Vaultwarden의 Diesel ORM 마이그레이션 및 런타임 질의 수행 중 발견된 5가지 프로토콜 및 엔진 결함을 LionSQL 커널 내부에서 완전하게 해결하였습니다:

### 이슈 1: 다중 액션 복합 `ALTER TABLE` 구문 파싱
- **현상**: Diesel 마이그레이션 중 단일 구문 내에 쉼표로 연결된 다중 액션(`ALTER TABLE attachments ALTER COLUMN file_size TYPE BIGINT, ALTER COLUMN file_size SET NOT NULL;`) 실행 시 문법 오류 발생.
- **해결**: `pkg/engine/sqlexec_ddl.go`에 다중 액션 복합 구문 분할기(`reAlterTablePrefix`, `reLeadingAction`)를 구현하여 개별 서브 액션으로 분할 순차 실행하도록 지원.

### 이슈 2: `ADD COLUMN` 내 줄바꿈 및 다중 공백 정규식 매칭
- **현상**: 마이그레이션 스크립트에 `ADD COLUMN\n client_kdf_memory`와 같이 개행문자가 포함된 경우, `strings.HasPrefix(s, "ADD ")`가 단순 공백만 체크하여 `"COLUMN"`을 컬럼명으로 오인식, `column "COLUMN" already exists` 오류 발생.
- **해결**: `pkg/engine/sqlexec_ddl.go`의 정규식을 `\s+` 기반(`reLeadingAdd`, `reLeadingColumn`, `reLeadingIfNotExists`)으로 개편하여 공백/개행/탭 변형에 완벽 대응.

### 이슈 3: 복합 기본 키(Composite PK)에 대한 `ON CONFLICT DO UPDATE` 처리
- **현상**: 디바이스 토큰 갱신 시 `devices (uuid, user_uuid)` 테이블과 같은 복합 기본 키 대상 `ON CONFLICT DO UPDATE` 실행 시 `update row error in ON CONFLICT: key not found` 발생.
- **해결**: `pkg/engine/sqlexec_dml.go`에서 `schema.IsCompositePK()` 검사를 추가하고 `schema.ResolveRecordPK(rec)`를 호출하여 복합 기본 키 값을 올바르게 조합/해결하도록 수정.

### 이슈 4: `INSERT INTO ... VALUES (..., DEFAULT, ...)` 키워드 처리
- **현상**: 사이퍼 생성 시 `organization_uuid`에 기본값이 없어 SQL에서 `DEFAULT` 키워드가 전달될 때, 문자열 `"DEFAULT"`로 삽입되어 외래 키 제약 조건 위반 발생.
- **해결**: `pkg/engine/sqlexec_dml.go` 및 `pkg/storage/literal.go`에서 `DEFAULT` 키워드 감지 시 컬럼의 기본값 식을 평가하거나 명시적 기본값이 없으면 `NullValue`를 할당하도록 처리.

### 이슈 5: PGWire `TIMESTAMP` 바이너리 인코딩(OID 1114) 및 괄호 중첩 FROM 절 해석
- **현상**: Diesel이 `GET /api/sync` 호출 시 `SELECT DISTINCT "ciphers"."uuid", ... FROM ((("ciphers" ...` 쿼리를 실행하는데, 중첩 괄호 및 `DISTINCT` 키워드로 인해 테이블 메타데이터 조회가 실패하여 `created_at` 컬럼이 Text OID(25)로 반환됨. Diesel은 8바이트 정수(`i64`)를 기대하므로 `DeserializationError: Received more than 8 bytes while decoding an i64` 역직렬화 실패 발생.
- **해결**:
  1. `pkg/proc/parser_dml.go` 및 `pkg/planner/planner.go`에서 ANSI 괄호 중첩 FROM 절(`FROM ("t1" JOIN "t2")`)을 투명하게 언래핑.
  2. `pkg/wire/server.go`의 `cleanColumnIdentifier`에서 `DISTINCT` 및 외곽 괄호를 정제하고, `TypeDateTime`을 PostgreSQL OID 1114(`TIMESTAMP`)로 바인딩.
  3. 조인 테이블 및 카탈로그 전역 컬럼 타입 해석 폴백 메커니즘을 추가하여 바이너리 8바이트 정수 전송 보장.

---

## 4. E2E 검증 결과 상세

### 4.1 Diesel ORM 마이그레이션 완주 (47개 마이그레이션)
Vaultwarden의 모든 47개 PostgreSQL 마이그레이션이 단 하나의 에러 없이 완주되었습니다:
- **마이그레이션 완료 확인**: `SELECT count(*) FROM __diesel_schema_migrations` ➔ **47개 완료 (실행 시간 280µs)**
- **생성된 테이블 (29개)**:
  `__diesel_schema_migrations`, `attachments`, `ciphers`, `ciphers_collections`, `collection_users`, `collections`, `devices`, `emergency_access`, `favorites`, `folders`, `folders_ciphers`, `group_users`, `groups`, `invitations`, `org_policies`, `organization_api_keys`, `organizations`, `sends`, `twofactor`, `twofactor_incomplete`, `user_kdf`, `users`, `users_collections`, `users_organizations`, `webauthn_credentials`, etc.

### 4.2 Bitwarden 표준 REST API 워크플로 검증
| 순번 | 작업 | API 엔드포인트 | 메소드 | 검증 결과 |
|---|---|---|---|---|
| 1 | 계정 인증 메일 발송 토큰 발급 | `/identity/accounts/register/send-verification-email` | `POST` | **200 OK** (인증 토큰 정상 발급) |
| 2 | 계정 최종 생성 (KDF / Salt 등록) | `/identity/accounts/register/finish` | `POST` | **200 OK** (`{"object":"registerFinish"}`) |
| 3 | 로그인 전 파라미터 협상 | `/identity/accounts/prelogin` | `POST` | **200 OK** (KDF 타입 및 솔트 반환) |
| 4 | 사용자 인증 및 JWT 발급 | `/identity/connect/token` | `POST` | **200 OK** (Bearer Access / Refresh Token 발급) |
| 5 | 신규 비밀번호 사이퍼 생성 | `/api/ciphers` | `POST` | **200 OK** (사이퍼 ID `ab4bda1a-...` 발급) |
| 6 | 사이퍼 단건 조회 | `/api/ciphers/<id>` | `GET` | **200 OK** (암호화 페이로드 검증) |
| 7 | 사이퍼 내용 수정 및 업데이트 | `/api/ciphers/<id>` | `PUT` | **200 OK** (수정 일시 동기화) |
| 8 | 새 폴더 생성 | `/api/folders` | `POST` | **200 OK** (폴더 `"Production Databases"` 생성) |
| 9 | 전체 볼트 동기화 | `/api/sync` | `GET` | **200 OK** (1 cipher, 1 folder 정상 직렬화) |
| 10 | 사이퍼 삭제 | `/api/ciphers/<id>` | `DELETE` | **200 OK** (동기화 확인 시 cipher 0개 반영) |

---

## 5. 결론 및 산출물

- Vaultwarden(Rust + Diesel ORM)은 LionSQL의 PostgreSQL 유선 프로토콜 및 WiredTiger B+Tree 엔진 상에서 상용 수준의 완벽한 호환성으로 동작함을 입증하였습니다.
- 단위 회귀 테스트 `pkg/engine/vaultwarden_compat_test.go`를 추가하여 지속적인 품질 유지를 보장합니다.
- 본 검증을 통해 작성된 산출물:
  - 연동 보고서: `notes/VAULTWARDEN_INTEGRATION_REPORT.ko.md`
  - 배포 및 가이드: `doc/VAULTWARDEN_INTEGRATION_GUIDE.ko.md`, `doc/VAULTWARDEN_INTEGRATION_GUIDE.md`
