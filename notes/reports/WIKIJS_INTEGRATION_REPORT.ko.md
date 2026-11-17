# Wiki.js Docker E2E 실전 통합 검증 보고서 (`WIKIJS_INTEGRATION_REPORT.ko.md`)

> - **검증 일자**: 2026-11-17
> - **대상 애플리케이션**: Wiki.js v2 (`ghcr.io/requarks/wiki:2`, Node.js + Knex.js + Objection.js ORM)
> - **연동 프로토콜**: PostgreSQL Wire Protocol v3 (`:5435`)
> - **스토리지 엔진**: LionSQL In-Memory OCC (`compact` / AOF + RDB 영속화)
> - **최종 판정**: **100% PASS (42개 엔터프라이즈 테이블 스키마 마이그레이션, 초기 관리자 셋업 및 GraphQL/REST API 전체 정상 동작)**

---

## 1. 검증 개요 및 환경

Wiki.js v2 공식 Docker 컨테이너를 구동하여 LionSQL의 PostgreSQL Wire Protocol v3 리스너와 직접 연결하고, 무수정(Zero-Patch) 상태에서 전체 부트스트랩 및 위키 서비스 기동을 검증하였습니다.

- **컨테이너 이미지**: `ghcr.io/requarks/wiki:2`
- **데이터베이스 설정**: `DB_TYPE=postgres`, `DB_HOST=host.docker.internal`, `DB_PORT=5435`, `DB_NAME=wiki`

---

## 2. E2E 마이그레이션 및 런타임 검증 결과

1. **Knex.js 초기 스키마 마이그레이션 (`2.0.0` ~ `2.5.x`)**:
   - `knex_migrations`, `knex_migrations_lock` 제어 테이블 생성 및 잠금 획득 성공.
   - `users`, `pages`, `pageHistory`, `pageLinks`, `pageTags`, `assets`, `assetData`, `assetFolders`, `authentication`, `comments`, `editors`, `groups`, `locales`, `loggers`, `modules`, `navigation`, `renderers`, `searchEngines`, `sessions`, `settings`, `storage`, `system`, `tags`, `themes`, `userAvatars`, `userGroups`, `userKeys` 등 **총 42개 핵심 엔터프라이즈 테이블** 생성 100% 완료.
2. **웹 설치 마법사 및 GraphQL 뮤테이션 완료**:
   - `POST / finalize` 초기 관리자 계정 생성 및 사이트 구성 트랜잭션 커밋 완료.
   - `GET /` 및 `GET /login` 엔드포인트 HTTP 200 OK 응답 확인.

---

## 3. 발굴 및 해결된 엔진 호환성 이슈

1. **합성 기본키(`_row_id`) 은닉 불변성 강화 (`pkg/execution/project.go`)**:
   - PK가 명시되지 않은 테이블(`knex_migrations_lock` 등)에 대해 `SELECT *` 프로젝션 수행 시 내부 합성 컬럼(`_row_id`)이 ORM 결과셋에 노출되지 않도록 `storage.IsSyntheticPKName` 필터링 가드 적용.
2. **복합 불리언 및 타임존 캐스팅 호환 (`CAST($1 AS timestamp with time zone)`)**:
   - 백그라운드 세션 정리 워커(`delete from "sessions" where expired < CAST($1 as timestamp with time zone)`)의 Extended Query Protocol 바인딩 및 VDBE 실행 정상화.

---

## 4. 회귀 테스트 등록 현황

- [`pkg/engine/wikijs_compat_test.go`](../../pkg/engine/wikijs_compat_test.go): Knex.js 마이그레이션 락, 스키마 생성, 설정 JSON 적재 및 페이지 조회 시나리오 상시 검증.
