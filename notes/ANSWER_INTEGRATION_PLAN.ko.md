# Apache Answer 사내 Q&A/지식베이스 PGWire 연동 및 검증 계획서 (`notes/ANSWER_INTEGRATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-10-31
> - **과제 식별 코드**: `INTEG-ANSWER-1`
> - **목표**: 오픈소스 Q&A/지식베이스 플랫폼 [Apache Answer](https://github.com/apache/answer)(Go)를 LionSQL의 **PostgreSQL Wire Protocol (`:5433`)** 위에서 소스 수정 없이 완전 기동시키고, 핵심 기능을 E2E 검증
> - **상태**: 계획 수립 (`Pending`)
> - **선행 연동 참조**:
>   - Casdoor 마일스톤: [`CASDOOR_INTEGRATION_MILESTONE.ko.md`](CASDOOR_INTEGRATION_MILESTONE.ko.md)
>   - Gitea 마일스톤: [`GITEA_INTEGRATION_MILESTONE.ko.md`](GITEA_INTEGRATION_MILESTONE.ko.md)
> - **작업 순서**: 연동 후보 1순위 (Answer ➔ Vaultwarden ➔ Metabase ➔ Umami ➔ NocoDB ➔ Mattermost)

---

## 1. 개요 및 범위 결정

### 1.1. 프로토콜 범위: PGWire 단독 (TDS 제외)
공식 문서 기준 Answer가 지원하는 DB 드라이버는 `mysql`, `postgres`, `sqlite3`이며 **SQL Server는 지원하지 않는다**. 소스를 포크해 드라이버를 주입하는 것은 "소스 무수정" 원칙에 어긋나므로 본 과제에서는 제외한다.

| 모드 | 드라이버 | 대상 포트 | 포함 여부 |
| :--- | :--- | :---: | :---: |
| Mode A | `postgres` | `:5433` | 포함 |
| Mode B | `sqlserver` | `:1433` | 제외 (Answer 미지원) |

> 주의: 위 지원 목록은 웹 검색 요약에 기반한 것이다. 1단계에서 `go.mod`와 `internal/base/data` 소스로 지원 드라이버와 ORM(XORM 여부)을 직접 확인하고 본 표를 확정한다.

### 1.2. 예상 아키텍처

```
+--------------------------------------------------------------+
|              Apache Answer (Go, :9080 기본)                  |
|  - ORM: XORM 추정 (Gitea/Casdoor와 동일 계열, 1단계에서 확인)|
|  - 엔티티: user, question, answer, comment, tag, config 등    |
+--------------------------------------------------------------+
                          | driver: postgres
                          | (TCP :5433)
                          v
+--------------------------------------------------------------+
|                LionSQL (Single Binary)                       |
|  PGWire Listener -> VDBE / Volcano CBO -> WiredTiger / OCC   |
+--------------------------------------------------------------+
```

### 1.3. 위험 요소 (사전 추정)
1. **설치 마법사 및 DB 초기화**: Answer는 최초 기동 시 웹 설치 화면(또는 `answer init`/환경 변수)으로 `config.yaml`을 생성하고 마이그레이션을 실행한다. 이 경로의 DDL 호환성이 첫 관문이다.
2. **마이그레이션 SQL**: 버전별 마이그레이션이 raw SQL(`ALTER TABLE`, 인덱스 변경 등)을 포함할 수 있다.
3. **검색 기능**: 질문/답변 검색이 `LIKE`/`ILIKE` 또는 DB 전문 검색 기능을 쓰는지 확인 필요.
4. **타임스탬프/불리언/텍스트 타입**: XORM `postgres` 방언의 컬럼 타입(`TIMESTAMP`, `BOOL`, `TEXT`)과 스캔 호환성.
5. 선행 연동(Gitea, Casdoor)에서 이미 검증된 XORM 경로(`information_schema`, `pg_catalog` 인트로스펙션, `RETURNING`, `BIGSERIAL`)는 재사용 가능할 것으로 기대한다.

---

## 2. 세부 단계별 계획

### 1단계: 저장소 준비 및 사전 조사
- `/Users/maro/Projects/answer` 위치에 공식 저장소 shallow clone (`--depth 1`).
- `go.mod`, DB 초기화 코드, 마이그레이션 디렉토리를 정독하여 지원 드라이버, ORM, 사용하는 SQL 기능(특수 함수, 확장, 전문 검색)을 목록화.
- 프론트엔드 빌드 필요 여부 확인(번들된 UI 사용 가능 시 생략). Go 바이너리 빌드 검증.
- 산출물: 본 문서 1.1/1.3 절의 추정 사항을 실측으로 갱신.

### 2단계: LionSQL 기동 및 Answer 설치
- `data_answer_pg` 디렉토리로 LionSQL 기동 (`:5433`).
- Answer 설치 설정:
  - `driver: "postgres"`
  - `connection: "host=127.0.0.1 port=5433 user=admin password=<pw> dbname=answer sslmode=disable"`
- 설치 마법사 또는 `answer init` 실행, 마이그레이션 전체 통과 확인.
- 실패 시 오류 로그를 근거로 LionSQL 호환성 결함을 분류(DDL / 카탈로그 / 타입 / 함수).

### 3단계: E2E 기능 검증
- 관리자 계정 생성 및 로그인.
- 질문 작성 ➔ 답변 작성 ➔ 댓글 ➔ 태그 부여 ➔ 투표/채택.
- 질문 목록 정렬/페이징, 태그 필터, 검색.
- 사용자 프로필, 관리자 설정 변경.
- 재기동 후 데이터 영속성 확인.
- REST API 또는 UI 자동 호출로 재현 가능한 시나리오 스크립트화(가능한 경우).

### 4단계: 커널 보완 및 3-Stage Verification Pipeline
- 발견된 호환성 이슈를 LionSQL 측에서 수정하고, 각 이슈마다 `pkg/engine/answer_compat_test.go` 등에 회귀 테스트 추가.
- Build ➔ Test (`go test -tags wiredtiger -count=1 -v -race ./...`) ➔ Commit (Conventional Commits, 이모지 금지).
- 임시 디렉토리(`data_answer_*`, `/tmp/answer_*`) 및 백그라운드 프로세스 정리.

### 5단계: 문서화 및 백로그 동기화
- 실전 가이드: `doc/ANSWER_INTEGRATION_GUIDE.ko.md`, `doc/ANSWER_INTEGRATION_GUIDE.md`
- 마일스톤 보고서: `notes/ANSWER_INTEGRATION_MILESTONE.ko.md`, `notes/ANSWER_INTEGRATION_MILESTONE.md`
- `TODO.md`(`INTEG-ANSWER-1` ➔ `[x]`), `CHANGELOG.md`, `notes/README.ko.md`, `notes/README.md` 갱신 후 커밋.

---

## 3. 완료 기준 (Definition of Done)

| 항목 | 기준 |
| :--- | :--- |
| 설치 및 마이그레이션 | PGWire(`:5433`)에서 Answer 설치/마이그레이션 100% 통과 |
| 핵심 기능 | 질문/답변/댓글/태그/투표/검색 E2E 정상 |
| 소스 무수정 | Answer 소스 코드 변경 0건 |
| 회귀 테스트 | 발견된 결함마다 LionSQL 테스트 추가 |
| 파이프라인 | Build 및 `-race` 전체 테스트 100% PASS |
| 문서 | 한/영 가이드 및 마일스톤, TODO/CHANGELOG/README 인덱스 갱신 |

## 4. 후속 작업
- 완료 후 다음 후보 `INTEG-VAULTWARDEN-1`(Rust + Diesel, libpq 바이너리 프로토콜 검증)로 진행한다.
