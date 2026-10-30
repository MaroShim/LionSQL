# Gitea - LionSQL PGWire (:5433) 실전 연동 검증 및 호환성 강화 계획서

> - **최초 작성일**: 2026-10-30
> - **문서 상태**: 검증 완료 (Completed & Verified `[x]`)
> - **목표 프로토콜**: PostgreSQL Wire Protocol v3 (TCP `:5433`)
> - **연동 대상**: Gitea v1.27.3 (`http://localhost:3000`)
> - **참조 문서**: [`notes/ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md`](ORM_SQL_COMPATIBILITY_AND_TEST_BLIND_SPOTS_POSTMORTEM.ko.md)

---

## 1. 개요 및 목표

어제 TDS(`:1433`) 프로토콜을 통한 Gitea 종합 모의 시딩 및 PR 머지 성공에 이어, 오늘 목표인 **Gitea의 데이터베이스 백엔드를 PostgreSQL(`postgres`, TCP `:5433`)로 전환**하여 전 과정(초기 자동 마이그레이션, PBKDF2 인증, 종합 모의 시딩, 브랜치/PR/릴리스)을 성공적으로 완수하였다.

### 핵심 성공 기준 (Definition of Done)
1. **Gitea 환경 격리**: `/Users/maro/Projects/gitea-pg` 및 LionSQL `data_pg/`로 기존 TDS 환경과 100% 격리 구동 완료.
2. **Gitea 구동 및 마이그레이션 완료**: Gitea 기동 시 XORM이 실행하는 PostgreSQL 전용 테이블/인덱스 DDL(`BIGSERIAL`, `RETURNING`, `pg_indexes`) 100% 수용 완료.
3. **Web UI 정상 로딩 및 로그인**: `http://localhost:3000`에서 관리자(`bob`) 및 사용자 인증 정상 통과.
4. **PGWire 전용 모의 시딩 및 회귀 검증**: `scripts/seed_gitea_mock_data.go`를 통한 API 시딩(이슈, 커밋, PR, 릴리스) 100% 성공 확인.
5. **3단계 검증 파이프라인(Build ➔ Test ➔ Commit)** 완수: 22개 전 패키지 0 data races 유지 및 Conventional Commits 완료.

---

## 2. 단계별 실행 로드맵 (Action Plan)

### Stage 1: 현황 파악 및 Gitea 환경 격리 준비
- [x] 현재 실행 중인 Gitea 및 LionSQL 프로세스 상태 점검
- [x] 격리 디렉토리 `/Users/maro/Projects/gitea-pg` 생성 및 바이너리/템플릿 복제
- [x] PostgreSQL 접속용 `custom/conf/app.ini` 작성:
  ```ini
  DB_TYPE  = postgres
  HOST     = 127.0.0.1:5433
  NAME     = gitea
  USER     = admin
  PASSWD   = 1234567890
  SCHEMA   = public
  SSL_MODE = disable
  ```
- [x] LionSQL 독립 데이터 디렉토리(`data_pg/`)로 TDS 환경(`data/`)과 완전 분리

### Stage 2: LionSQL PGWire 데몬 가동 및 초기 핸드셰이크 검증
- [x] `./bin/lionsql -data-dir=data_pg -dev` 데몬 가동
- [x] Extended Query Protocol (`Parse`, `Bind`, `Execute`, `Sync`) 핸드셰이크 검증

### Stage 3: Gitea 기동 및 XORM PostgreSQL DDL/인덱스 호환성 디버깅
- [x] Gitea 웹 서버 기동 (`./gitea web`)
- [x] Gitea 서버 로그(`gitea_server.log`) 및 LionSQL `[DEV][PGWire]` 쿼리 트레이스 실시간 모니터링
- [x] 발생한 핵심 호환성 이슈 해결:
  1. `BIGSERIAL` / `SERIAL` / `SMALLSERIAL` DDL 파서 지원 (`Identity = true`, `Type = TypeInt`)
  2. `INSERT ... RETURNING "id"` 구문 파싱 및 VDBE 우회/OutputRow 프로젝션 구현
  3. `pg_catalog.pg_indexes` 가상 시스템 뷰 구현
  4. 복합 JOIN 쿼리 인터셉터 우선순위 조정 (`PG_ATTRIBUTE` > `PG_CONSTRAINT`)
  5. Describe 메시지 스키마 프로젝션 전달 (`WriteRowDescription` for `RETURNING`)
  6. 단일 패스 인용구 인식 파라미터 치환기 (`$5` 치환 시 `'pbkdf2$50000$50'` 비밀번호 해시 파손 방지)
  7. untyped PGWire 파라미터(OID 0) 숫자 디코딩 및 `ParseLiteralDefault` 문자열 리터럴 숫자 타입 자동 변환

### Stage 4: 엔드투엔드 기능 및 모의 데이터 시딩 검증
- [x] `http://localhost:3000` 접속 및 Web UI 렌더링 확인 (HTTP 200 OK)
- [x] 관리자 `bob` 계정 생성 및 BasicAuth 로그인 검증 (HTTP 200 OK)
- [x] `go run scripts/seed_gitea_mock_data.go` 실행하여 API 시딩 통과:
  - 4명 사용자 생성 (`developer1`, `reviewer1`, `qa_lead`, `perf_eng`)
  - 조직 생성 (`lionsql-core`, type=1)
  - 4개 저장소 생성 (`lion-engine`, `lion-bench`, `lion-docs`, `personal-scripts`)
  - 마일스톤 및 5개 레이블 생성
  - 이슈 3건 및 리뷰어/개발자 댓글 스레드 등록
  - 기능 브랜치 `feature/flat-hash-join` 생성 및 `docs/HASH_JOIN.md` 커밋
  - 풀 리퀘스트 PR #5 생성, 리뷰 승인 및 `main` 머지
  - 릴리스 `v1.0.0-rc1` 태그 배포 완료

### Stage 5: 3단계 검증 및 커밋
- [x] 3개 바이너리 빌드 확인
- [x] `go test -tags wiredtiger -count=1 -race ./pkg/...` 100% PASS 확인
- [x] `TODO.md` 및 `CHANGELOG.md` 동기화 후 Git Commit
