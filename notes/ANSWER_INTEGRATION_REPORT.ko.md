# Apache Answer 연동 검증 보고서 (`ANSWER_INTEGRATION_REPORT.ko.md`)

> - **최종 갱신일**: 2026-11-01
> - **최초 작성일**: 2026-11-01
> - **연동 대상**: [Apache Answer](https://github.com/apache/answer) (Go + XORM)
> - **연동 프로토콜**: PostgreSQL Wire Protocol v3 (`TCP :5433`)
> - **스토리지 엔진**: WiredTiger Lock-Free B+Tree MVCC Engine
> - **검증 결과**: **100% 통과 (Pass)** (Apache Answer 소스 코드 변경: **0줄**)

---

## 1. 개요 및 연동 목표

Apache Answer는 Apache Software Foundation 산하의 오픈소스 지식 기반 Q&A 커뮤니티 플랫폼으로, 백엔드는 Go 언어와 XORM (`xorm.io/xorm v1.3.2`), `github.com/lib/pq v1.10.9` 드라이버를 사용합니다.

LionSQL의 PostgreSQL Wire 프로토콜 호환성 검증 트랙(`INTEG-ANSWER-1`)의 핵심 목표는 다음과 같습니다:
1. **Zero Source Modification**: Apache Answer의 원본 소스 코드를 단 한 줄도 수정하지 않고 표준 PostgreSQL 설정으로 100% 구동.
2. **Schema & Migration Integrity**: Answer의 35개 테이블 스키마 자동 생성(`Sync`) 및 시드 데이터 초기화 무오류 완료.
3. **Core Workflow E2E**: 관리자 가입/로그인(JWT), 질문 작성, 답변 작성, 태그/활동 로그 동기화의 완벽한 동작 검증.

---

## 2. 연동 아키텍처 및 설정

```
+-----------------------------------------------------------+
|              Apache Answer Daemon (:9080)                 |
|   (REST API, Admin / Question / Answer / Tag Services)    |
+-----------------------------------------------------------+
                             │
                  (lib/pq PostgreSQL v3)
                             ▼
+-----------------------------------------------------------+
|             LionSQL PGWire Listener (:5433)               |
|  - Extended Query Protocol (Parse / Bind / Describe / Exec)|
|  - Tolerant Virtual USE & Catalog View Engine             |
|  - Zero-Allocation VDBE Bytecode VM Engine                |
|  - WiredTiger Lock-Free B+Tree MVCC Engine                |
+-----------------------------------------------------------+
```

### Answer 자동 설치 환경변수 (`AUTO_INSTALL`)
```bash
AUTO_INSTALL=true \
DB_TYPE=postgres \
DB_HOST=127.0.0.1:5433 \
DB_USERNAME=admin \
DB_PASSWORD=1234567890 \
DB_NAME=answer \
LANGUAGE=en_US \
SITE_NAME="LionSQL Q&A" \
SITE_URL="http://127.0.0.1:9080" \
CONTACT_EMAIL=admin@lionsql.local \
ADMIN_NAME=admin \
ADMIN_PASSWORD=adminadmin \
ADMIN_EMAIL=admin@lionsql.local \
EXTERNAL_CONTENT_DISPLAY=always_display \
answer init -C /tmp/answer_data_fresh
```

---

## 3. 발견된 호환성 이슈 및 LionSQL 해결 내역

Apache Answer 연동 과정에서 발견된 PGWire/VDBE 관련 결함들을 LionSQL 엔진 내부에서 완전히 해결하였습니다:

### 이슈 1: 메타데이터 인트로스펙션 쿼리의 VDBE 라우팅 충돌
- **현상**: Answer가 테이블 존재 여부를 확인할 때 `SELECT tablename FROM pg_tables WHERE schemaname = $1 AND tablename = $2`를 질의하는데, VDBE 바이트코드로 컴파일되어 물리 스토리지에 스캔을 시도하면서 `table "pg_tables" not found` 오류 발생.
- **해결**: `pkg/wire/server.go`에 `isVirtualCatalogQuery()` 판별기를 추가하여 `pg_tables`, `sys.*`, `information_schema.*` 가상 뷰 쿼리는 VDBE 바이트코드 컴파일을 우회하고 LionSQL 가상 카탈로그 뷰 엔진(`execVirtualCatalogSelect`)으로 자동 라우팅되도록 수정.

### 이슈 2: `SERIAL` Primary Key에 대한 Explicit ID INSERT 거부
- **현상**: Answer 초기화 시 `INSERT INTO "version" ("id", "version_number") VALUES (1, 0)`과 같이 `SERIAL PRIMARY KEY` 컬럼에 명시적으로 ID를 지정하는 쿼리를 실행할 때, T-SQL의 `IDENTITY_INSERT OFF` 검사에 걸려 실패.
- **해결**: PostgreSQL에서는 `SERIAL` 컬럼에 명시적인 ID INSERT가 기본 허용되므로, PGWire 세션(`pg-`)에 대해 `identity_insert_all: true` 플래그를 세션 컨텍스트 및 VDBE 실행기에 기본 주입하여 호환성 보장.

### 이슈 3: `RETURNING "id"` 절에 대한 VDBE 빈 결과셋 반환
- **현상**: `INSERT INTO "version" ... RETURNING "id"` 실행 시 VDBE 바이트코드 실행기가 `RETURNING` 절을 처리하지 못해 `sql: no rows in result set` 오류 발생.
- **해결**: `canCompileToVDBE()`에 `RETURNING`, `OUTPUT`, `ON CONFLICT` 절 검사를 추가하여, 결과 튜플 반환이 필요한 DML은 `sqlexec_dml.go` 전용 실행 엔진으로 라우팅되도록 수정.

### 이슈 4: VDBE 다중 행 배치 INSERT 시 Identity Key 자동 생성 누락 (핵심 해결)
- **현상**: `role_power_rel` 테이블에 권한 매핑을 배치 삽입할 때 `INSERT INTO "role_power_rel" ("created_at", "updated_at", "role_id", "power_type") VALUES ($1..$4), ($5..$8), ...` 쿼리 실행 시 `id` 컬럼이 생략되어 `not null constraint violation: missing primary key id` 오류 발생.
- **원인**: VDBE `OpInsert` 실행기(`vm_dispatcher.go`)에서 대상 컬럼 리스트에 `identityCol`이 빠져 있을 때 순차 번호를 자동 발급하는 로직이 누락되어 있었음.
- **해결**:
  1. `pkg/proc/runtime/vm.go`에 `IdentityProvider` SPI(`GetTableLastIdentity`, `SetTableLastIdentity`) 추상화 도입.
  2. `Executor`와 `Coordinator` 간 ID 발급 프로바이더 상호 주입.
  3. `vm_dispatcher.go`의 `OpInsert` 핸들러에서 `identityCol` 누락 시 테이블의 최대 ID를 스캔/조회하여 각 배치 튜플마다 `nextID++`로 자동 생성 및 저장.
  4. 83개의 권한 매핑 튜플이 `id = 1 ~ 83`으로 일괄 정상 삽입 완료.

---

## 4. E2E 검증 결과 상세

### 4.1 스키마 생성 및 초기화 (35개 테이블)
Answer의 35개 핵심 테이블이 WiredTiger 스토리지에 완전하게 생성됨:
- `activity`, `ai_conversation`, `ai_conversation_record`, `answer`, `api_key`, `badge`, `badge_award`, `badge_group`, `collection`, `collection_group`, `comment`, `config`, `file_record`, `meta`, `notification`, `plugin_config`, `plugin_kv_storage`, `plugin_user_config`, `power`, `question`, `question_link`, `report`, `review`, `revision`, `role`, `role_power_rel`, `site_info`, `tag`, `tag_rel`, `uniqid`, `user`, `user_external_login`, `user_notification_config`, `user_role_rel`, `version`

### 4.2 REST API 통합 동작 검증
| API 엔드포인트 | 메소드 | 페이로드 / 내용 | 결과 |
|---|---|---|---|
| `/answer/api/v1/siteinfo` | `GET` | 사이트 설정 및 정보 조회 | **200 OK** (LionSQL Q&A 메타데이터 정상 반환) |
| `/answer/api/v1/user/login/email` | `POST` | `admin@lionsql.local` / `adminadmin` 로그인 | **200 OK** (JWT Access Token 발급 완료) |
| `/answer/api/v1/question` | `POST` | 새 질문 등록 ("How does LionSQL provide wire compatibility with PostgreSQL?") | **200 OK** (Question ID `10010000000000020` 발급) |
| `/answer/api/v1/answer` | `POST` | 답변 작성 ("LionSQL runs seamlessly with zero modifications...") | **200 OK** (Answer ID `10020000000000022` 발급) |
| `/answer/api/v1/question/info` | `GET` | 질문 상세 조회 (`id=10010000000000020`) | **200 OK** (조회수 1, 최신 답변 ID 갱신 확인) |
| `/answer/api/v1/answer/page` | `GET` | 질문에 대한 답변 목록 조회 | **200 OK** (등록된 답변 1건 완벽 반환) |

---

## 5. 결론 및 향후 계획

Apache Answer는 엔터프라이즈 레벨의 Go ORM인 XORM과 다양한 복합 쿼리(복합 배치 INSERT, 트랜잭션 내 `SERIAL` 컬럼 핸들링, `LIMIT` 쿼리, 다중 테이블 조인)를 집중적으로 사용하는 시스템입니다.

이번 연동 성공을 통해:
1. LionSQL PGWire v3 레이어가 표준 Go ORM(XORM)을 완벽하게 수용함을 입증하였습니다.
2. VDBE 가상 머신의 다중 행 배치 Identity 자동 생성 능력이 완성되었습니다.
3. 다음 트랙인 `INTEG-VAULTWARDEN-1` (Rust + Diesel ORM) 연동을 위한 탄탄한 프로토콜 기반이 확립되었습니다.
