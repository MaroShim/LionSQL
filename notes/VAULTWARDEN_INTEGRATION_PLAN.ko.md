# Vaultwarden 연동 검증 계획서 (`notes/VAULTWARDEN_INTEGRATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **과제 식별 코드**: `INTEG-VAULTWARDEN-1`
> - **연동 대상**: [Vaultwarden](https://github.com/dani-garcia/vaultwarden) (Rust + Diesel ORM, Bitwarden API 호환)
> - **지원 유선 프로토콜**: PostgreSQL Wire Protocol v3 (`TCP :5433`)
> - **스토리지 엔진**: WiredTiger Lock-Free B+Tree MVCC Engine
> - **핵심 불변 원칙**:
>   1. **Zero Source Modification**: Vaultwarden 원본 소스 코드 무수정 연동 (0줄 수정)
>   2. **Single Standalone Static Binary**: 외부 미들웨어(Redis, PgBouncer 등) 없이 LionSQL 단일 바이너리 연동
>   3. **3-Stage Pipeline 준수**: Build ➔ Test (`-race` 100% 통과, 0 Data Race) ➔ Commit (Conventional Commits, 이모지 금지)

---

## 1. 개요 및 연동 목표

Vaultwarden은 오픈소스 비밀번호 관리자 Bitwarden의 공식 API 규격을 Rust 언어로 구현한 초경량 백엔드입니다. 데이터베이스 계층으로 **Diesel ORM**과 PostgreSQL 네이티브 클라이언트(`libpq` / Rust diesel postgres feature)를 사용합니다.

### 검증 목표
1. **Diesel ORM 스키마 마이그레이션 100% 완주**:
   - Vaultwarden의 `migrations/postgresql/` 내의 모든 DDL/DML 마이그레이션을 LionSQL PGWire v3를 통해 무오류 수행.
2. **핵심 보안 및 암호화 워크플로 E2E 검증**:
   - 신규 사용자 계정 등록 (Master Password Hash & Salt)
   - 사용자 인증 및 JWT Bearer 토큰 발급 (`/api/accounts/prelogin`, `/identity/connect/token`)
   - 암호화된 사이퍼(비밀번호, 보안 노트, 카드 등) 생성 및 동기화 (`/api/ciphers`, `/api/sync`)
   - 폴더 및 컬렉션 생성/조회
3. **Rust Diesel ORM 특유의 PGWire 쿼리 패턴 수용**:
   - Diesel의 엄격한 타입 인코딩, 파라미터 바인딩, `RETURNING` 절 및 시스템 인트로스펙션 호환성 보장.

---

## 2. 단계별 실행 계획

### 1단계: Vaultwarden 소스 클론 및 빌드 환경 준비
- 저장소 클론: `git clone --depth=1 https://github.com/dani-garcia/vaultwarden.git /Users/maro/Projects/vaultwarden`
- PostgreSQL 지원 활성화 빌드: `cargo build --features postgresql` (또는 필요 시 릴리즈 바이너리)

### 2단계: 스키마 및 마이그레이션 분석
- `migrations/postgresql/` 내 SQL 마이그레이션 스크립트 전수 조사
- 주요 테이블 구조(약 20개 테이블: `users`, `ciphers`, `folders`, `devices`, `organizations` 등) 및 인덱스 파악

### 3단계: LionSQL WiredTiger 데몬 기동 및 DB 연결
- 데이터 디렉토리: `data_vaultwarden_pg`
- 바인드: `-pg 127.0.0.1:5433 -engine wiredtiger -dev`
- 연결 문자열: `DATABASE_URL=postgresql://admin:1234567890@127.0.0.1:5433/vaultwarden`

### 4단계: 마이그레이션 수행 및 실시간 쿼리 결함 디버깅
- Vaultwarden 기동 시 Diesel 마이그레이션 자동 트리거
- PGWire 로그(`[DEV][PGWire]`) 추적을 통해 에러 발생 지점 즉시 감지
- 발생한 결함은 LionSQL 커널(`pkg/wire`, `pkg/engine`, `pkg/proc`)에서 완벽하게 해결 (Vaultwarden 소스 0줄 수정)

### 5단계: E2E 기능 검증 (REST API)
- Bitwarden 공식 규격 API 테스트:
  1. `POST /api/accounts/prelogin`
  2. `POST /api/accounts/register`
  3. `POST /identity/connect/token`
  4. `GET /api/sync`
  5. `POST /api/ciphers` (비밀번호 아이템 저장)
  6. `GET /api/ciphers/:id`

### 6단계: 품질 검증 및 문서화 (3-Stage Pipeline)
- 회귀 테스트 작성: `pkg/engine/vaultwarden_compat_test.go`
- 전체 테스트 검증: `go test -tags wiredtiger -count=1 -race ./...` (100% PASS, 0 Data Race)
- 문서 작성:
  - `notes/VAULTWARDEN_INTEGRATION_REPORT.ko.md`
  - `doc/VAULTWARDEN_INTEGRATION_GUIDE.ko.md` / `doc/VAULTWARDEN_INTEGRATION_GUIDE.md`
  - `TODO.md` 및 `CHANGELOG.md` 갱신
