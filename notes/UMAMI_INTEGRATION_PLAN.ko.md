# Umami 웹 애널리틱스 연동 계획서 (`UMAMI_INTEGRATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **상태**: 진행 중 (In Progress)
> - **과제 식별 코드**: `INTEG-UMAMI-1`
> - **연동 대상**: [Umami](https://github.com/umami-software/umami) (공식 Docker 이미지 `ghcr.io/umami-software/umami:postgresql-latest`, Next.js + Prisma ORM)
> - **연동 프로토콜**: PostgreSQL Wire Protocol v3 (`TCP :5433`)
> - **스토리지 엔진**: WiredTiger Lock-Free B+Tree MVCC Engine (`-engine wiredtiger`)
> - **연동 원칙**: Umami 소스 코드 수정 **0줄** (Zero Source Modification)

---

## 1. 개요 및 연동 목표

Umami는 Google Analytics를 대체하는 가장 인기 있는 오픈소스 프라이버시 중심 웹 애널리틱스 플랫폼입니다.
**Next.js 14+**(Node.js 런타임)와 **Prisma ORM**(PostgreSQL 드라이버)을 기반으로 동작하며, 다음과 같은 기술적 특징을 가집니다:

1. **Prisma ORM 마이그레이션**:
   - `_prisma_migrations` 테이블을 통한 마이그레이션 이력 관리.
   - 복합 인덱스, 고유 키 제약 조건, UUID / CUID 기본 키 및 타임스탬프 필드 관리.
2. **실시간 트래픽 수집 파이프라인**:
   - `POST /api/send`: 방문자 브라우저 스크립트(`script.js`)로부터 페이지뷰 및 커스텀 이벤트 수집.
   - 동시 다발적인 세션(`session`) 및 웹사이트 방문(`website_event`) 생성/업서트.
3. **분석 쿼리 (Analytics Aggregations)**:
   - `date_trunc('hour', ...)` / `date_trunc('day', ...)` 기반 시계열 트래픽 집계.
   - 브라우저, OS, 기기, 국가, URL 경로별 `COUNT(*)`, `COUNT(DISTINCT session_id)` 집계 질의.

### 연동 목표
- **목표 1 (Zero Modification)**: 순정 `ghcr.io/umami-software/umami:postgresql-latest` 컨테이너를 LionSQL PGWire 리스너에 직접 연결.
- **목표 2 (Prisma 마이그레이션 완주)**: Umami 기동 시 Prisma 자동 스키마 마이그레이션 100% 무오류 완료.
- **목표 3 (코어 워크플로우 완결)**: 관리자 로그인(`admin`/`umami`), 비밀번호 변경, 신규 웹사이트 등록, 트래픽 이벤트 수집(`POST /api/send`), 대시보드 통계 차트 조회 E2E 검증.
- **목표 4 (3단계 검증 파이프라인)**: Build ➔ Test(`-race` 100% pass) ➔ Commit 준수.

---

## 2. 연동 아키텍처

```
+-----------------------------------------------------------+
|             Umami Server (:3000)                          |
|         (Next.js 14 + Node.js + Prisma ORM)               |
+-----------------------------------------------------------+
                              │
               (PostgreSQL Wire Protocol v3)
                              ▼
+-----------------------------------------------------------+
|             LionSQL PGWire Listener (:5433)               |
|  - Extended Query Protocol (Parse / Bind / Describe / Exec)|
|  - Parameterized Prepared Statements & Type Encodings     |
|  - Virtual Catalog View Engine (_prisma_migrations)       |
|  - Zero-Allocation VDBE & Volcano CBO Engine              |
|  - WiredTiger Lock-Free B+Tree MVCC Engine                |
+-----------------------------------------------------------+
```

---

## 3. 단계별 실행 계획

### Phase 1: 환경 구성 및 초기화
1. LionSQL 독립 데이터 디렉토리(`data_umami_pg`)로 WiredTiger 엔진 기동 (`:5433`).
2. 공식 Docker 컨테이너 실행:
   ```bash
   docker run -d --name umami_test \
     -e DATABASE_URL="postgresql://admin:UmamiSecurePass2026!@host.docker.internal:5433/umami" \
     -e APP_SECRET="c03264eb-d7b1-4f10-91a5-83c31e97d1bc" \
     -p 3001:3000 \
     ghcr.io/umami-software/umami:postgresql-latest
   ```
3. Prisma 마이그레이션 실행 로그 모니터링 (`docker logs -f umami_test`).

### Phase 2: 스키마 생성 및 결함 해결
1. Umami의 기본 테이블군 생성 검증:
   - `user`, `team`, `team_user`, `website`, `session`, `website_event`, `event_data` 등.
2. Prisma DDL 구문에서 발생할 수 있는 쿼리 결함 감지 및 LionSQL 커널 내 신속 수정.

### Phase 3: E2E 워크플로우 검증
1. 관리자 로그인 (`POST /api/auth/login` -> `admin` / `umami`).
2. 신규 웹사이트 생성 (`POST /api/websites` -> `LionSQL Benchmark Portal`).
3. 페이지뷰 트래픽 수집 (`POST /api/send` -> 모의 페이지뷰 및 방문자 세션 전송).
4. 대시보드 지표 질의 (`GET /api/websites/{id}/stats`, `GET /api/websites/{id}/pageviews`).

### Phase 4: 자동화 회귀 테스트 및 문서화
1. `pkg/engine/umami_compat_test.go`에 Prisma 쿼리 및 분석 집계 질의 회귀 테스트 작성.
2. `notes/UMAMI_INTEGRATION_REPORT.ko.md` 작성.
3. `doc/UMAMI_INTEGRATION_GUIDE.ko.md` 및 `doc/UMAMI_INTEGRATION_GUIDE.md` 작성.
4. `TODO.md` 및 `CHANGELOG.md` 업데이트.

### Phase 5: 3-Stage Pipeline 검증 및 커밋
1. `go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql`
2. `go test -tags wiredtiger -count=1 -race ./...` (전 패키지 무경합 통과)
3. Conventional Commit: `feat(wire): integrate Umami web analytics over PGWire (INTEG-UMAMI-1)`
