# Woodpecker CI 및 Vikunja 연동 실행 계획서 (`notes/WOODPECKER_AND_VIKUNJA_INTEGRATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-01
> - **과제 식별 코드**: `INTEG-WOODPECKER-1`, `INTEG-VIKUNJA-1`
> - **상태**: 구현 및 검증 완료 (Completed)
> - **목표**:
>   1. **Woodpecker CI** (`INTEG-WOODPECKER-1`): GitHub Actions/Drone CI를 대체하는 완전 오픈소스 CI/CD 서버. Go + XORM 스택을 재사용하여 LionSQL PGWire(`:5433`) 상에서 소스 무수정 구동, 스키마 마이그레이션 완주, Gitea/GitHub 연동 및 웹 UI/API 동작 검증.
>   2. **Vikunja** (`INTEG-VIKUNJA-1`): Trello/Asana를 대체하는 직관적인 오픈소스 태스크 관리 및 칸반 보드 플랫폼. Go + XORM 스택을 재사용하여 LionSQL PGWire(`:5433`) 상에서 소스 무수정 구동, 사용자 등록, 프로젝트/태스크/칸반 CRUD 검증.
> - **스토리지 엔진**: WiredTiger MVCC 디스크 B+Tree 엔진

---

## 1. 기술 스택 및 아키텍처 특성

| 항목 | Woodpecker CI (`INTEG-WOODPECKER-1`) | Vikunja (`INTEG-VIKUNJA-1`) |
| :--- | :--- | :--- |
| **도메인** | 지속적 통합 및 배포 (CI/CD Engine) | 프로젝트 관리, 칸반 & To-Do 관리 |
| **공식 이미지** | `woodpeckerci/woodpecker-server:latest` | `vikunja/vikunja:latest` |
| **언어 및 ORM** | Go + XORM (`xorm.io/xorm`) | Go + XORM (`xorm.io/xorm`) |
| **드라이버 / 프로토콜** | `lib/pq` / PostgreSQL Wire (`TCP :5433`) | `lib/pq` / PostgreSQL Wire (`TCP :5433`) |
| **기존 자산 활용** | Gitea/Casdoor/Grafana의 검증된 XORM 인트로스펙션 및 DDL 호환성 자산 재사용 | Gitea/Casdoor의 XORM 트랜잭션, 복합 키, 외래 키 호환성 재사용 |
| **예상 난이도** | 낮음 (검증된 XORM 스택) | 낮음 (검증된 XORM 스택) |

---

## 2. 단계별 실행 로드맵

```
[Phase 1] Woodpecker CI 연동 (INTEG-WOODPECKER-1)
  ├── 1.1 LionSQL WiredTiger 데몬 구동 (0.0.0.0:5433)
  ├── 1.2 Woodpecker CI Server 컨테이너 기동 및 XORM 마이그레이션 실행
  ├── 1.3 관리자 설정, 레포지토리 동기화 및 웹 UI (http://localhost:8000) 검증
  ├── 1.4 회귀 테스트 추가 (pkg/engine/woodpecker_compat_test.go)
  └── 1.5 3-Stage Pipeline (Build ➔ Test ➔ Commit)

[Phase 2] Vikunja 연동 (INTEG-VIKUNJA-1)
  ├── 2.1 Vikunja 컨테이너 기동 및 XORM 마이그레이션 실행
  ├── 2.2 사용자 회원가입, 프로젝트 생성, 태스크/칸반 보드 CRUD 검증 (http://localhost:3456)
  ├── 2.3 회귀 테스트 추가 (pkg/engine/vikunja_compat_test.go)
  └── 2.4 3-Stage Pipeline (Build ➔ Test ➔ Commit)

[Phase 3] 산출물 및 문서화
  ├── 3.1 연동 보고서 작성 (notes/WOODPECKER_INTEGRATION_REPORT.ko.md, notes/VIKUNJA_INTEGRATION_REPORT.ko.md)
  ├── 3.2 운영 가이드 작성 (doc/*_GUIDE.ko.md, doc/*_GUIDE.md)
  ├── 3.3 TODO.md 및 CHANGELOG.md 갱신
  └── 3.4 최종 완료 커밋
```

---

## 3. 세부 검증 시나리오

### 3.1. Woodpecker CI
- **환경 변수**:
  - `WOODPECKER_DATABASE_DRIVER=postgres`
  - `WOODPECKER_DATABASE_DATASOURCE=postgres://admin:Password@host.docker.internal:5433/lionsql?sslmode=disable`
  - `WOODPECKER_OPEN=true`
  - `WOODPECKER_ADMIN=admin`
- **검증 항목**:
  1. XORM 자동 테이블 생성 및 마이그레이션 전체 무오류 적용 (`users`, `repos`, `builds`, `steps`, `logs` 등).
  2. 서버 헬스체크 및 웹 UI 서빙 (`HTTP 200`).
  3. API 엔드포인트 응답 검증 (`/api/user`, `/api/version`).

### 3.2. Vikunja
- **환경 변수**:
  - `VIKUNJA_DATABASE_TYPE=postgres`
  - `VIKUNJA_DATABASE_HOST=host.docker.internal`
  - `VIKUNJA_DATABASE_PORT=5433`
  - `VIKUNJA_DATABASE_USER=admin`
  - `VIKUNJA_DATABASE_PASSWORD=Password`
  - `VIKUNJA_DATABASE_DATABASE=lionsql`
  - `VIKUNJA_DATABASE_SSLMODE=disable`
  - `VIKUNJA_SERVICE_ENABLEREGISTRATION=true`
- **검증 항목**:
  1. 초기 기동 시 XORM 마이그레이션 적용.
  2. 신규 사용자 등록 및 JWT 인증 발급 (`POST /api/v1/register`, `POST /api/v1/login`).
  3. 프로젝트 생성 (`PUT /api/v1/projects`) 및 태스크 등록/조회/상태 변경 (`PUT /api/v1/projects/{id}/tasks`).
  4. 웹 프론트엔드 UI 접속 및 칸반 뷰 렌더링 확인.
