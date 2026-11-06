# LionSQL v0.91.0 릴리즈 노트 (`notes/RELEASE_NOTES_v0.91.0.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **릴리즈 버전**: `v0.91.0` (Minor Release)
> - **마일스톤 주제**: **단일 바이너리 내장 SPA 웹 서버 호스팅 (`WEB-SPA-1`), 전역 정규식 완전 박멸 (`GA-REGEX`), 공식 호환성 규격 표준화 및 In-Memory OCC Flat Record 고도화**

---

## 1. 릴리즈 하이라이트 개요

LionSQL `v0.91.0` 버전은 고성능 관계형 데이터베이스를 넘어 단일 실행 바이너리 기반의 완결형 애플리케이션 플랫폼으로 도약하고, 코어 쿼리 엔진의 안전성을 비약적으로 강화한 마일스톤 릴리즈입니다:

- **내장 SPA 웹 서버 호스팅 (`WEB-SPA-1`)**:
  - Nginx, Caddy 등 외부 리버스 프록시 없이 포트 `:8090`에서 단일 HTML 파일 및 React, Vue, Svelte 프로덕션 번들을 직접 고속 서빙.
  - Zero-Hop 인프로세스 버스로 루프백 TCP 지연 및 CORS 오버헤드 0 달성.
  - OS 커널 `sendfile(2)` 제로카피 리소스 격리로 웹 트래픽 폭주 시에도 DB OLTP 엔진에 무간섭(Noisy Neighbor 배제) 보장.
  - 공식 가이드 문서 발간: [`doc/WEB_APP_HOSTING_GUIDE.ko.md`](doc/WEB_APP_HOSTING_GUIDE.ko.md) 및 [`doc/WEB_APP_HOSTING_GUIDE.md`](doc/WEB_APP_HOSTING_GUIDE.md).
- **전역 정규식 완전 박멸 (`GA-REGEX`)**:
  - Volcano CBO 플래너, VDBE 런타임, 실행 LIKE 필터, PGWire 달러 파라미터(`$N`) 전역에서 정규식을 100% 제거.
  - `pkg/sqlutil` 내 0-할당 결정론적 토크나이저 및 상태머신(`MatchLike`, `ReplacePgParams`, `FindTopLevelKeyword`)으로 전면 전환하여 문맥 자유 문법 괄호 중첩 엣지케이스 완전 정복.
  - `sqlutil.MatchLike`에 T-SQL 대괄호 와일드카드(`[...]`, `[^...]`, `[_]`, `[%]`) 지원 보강.
- **프로젝트 루트 독립 호환성 규격서 제정**:
  - Microsoft SQL Server 2019 (v15.0) 및 PostgreSQL 14~16 와이어 프로토콜 표준 호환성 명세 공표 ([`COMPATIBILITY.ko.md`](COMPATIBILITY.ko.md) / [`COMPATIBILITY.md`](COMPATIBILITY.md)).
- **WiredTiger 정전/SIGKILL 무손실 내구성 하드닝 (`wt_engine`)**:
  - `wt_engine.go` 설정 내 `transaction_sync=(enabled=true,method=none)`을 영구 복구하여 커밋 시 사용자 영역 로그를 OS 버퍼로 즉시 플러시, 불시의 `kill -9` 전원 차단 시에도 0건 유실 무손실 내구성 확립 (`bin/crashtest` 100% PASS 검증).
- **6대 골든 릴리즈 검증 게이트 영구 탑재 (`scripts/release.sh`)**:
  - 단위(100k TPS), 13대 도메인 스모크, TPC-C(>70만 tpmC), SQLLogicTest(41만 쿼리), T-SQL 프로시저 14대 스위트, SIGKILL 크래시 복구 오라클을 릴리즈 파이프라인에 영구 통합하고 기술 백서([`notes/ARTICLE_HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.ko.md`](ARTICLE_HOW_RELEASE_GATES_ELEVATE_LIONSQL_VALUE.ko.md)) 발간.
- **In-Memory OCC Pointer-Free Flat Record 고도화 (`OCC-HYPER-2`)**:
  - 포인터 프리 플랫 레코드 레이아웃 코덱 적용으로 67.5% 메모리 절감 및 Go GC 부하 배제, 서브 나노초 $O(1)$ 읽기 성능 확립.
- **코어 내부 명세 및 히스토리 아카이브 체계화**:
  - `doc/internal/` 7대 코어 아키텍처 명세 정식 색인 연동 및 `notes/archive/` 18종 완료 계획서 체계화.

---

## 2. 검증 결과 요약

1. **3-Stage 검증 파이프라인 통과 (`[RULE:PipelineVerification]`)**:
   - macOS (`darwin-arm64`), Linux (`linux-amd64`), Windows (`windows-amd64.exe`) 3대 플랫폼 공식 릴리즈 바이너리 무결성 100% 빌드 성공.
   - `go test -tags wiredtiger -count=1 ./pkg/...` 23개 패키지 전수 100% PASS (0 data races).
2. **에코시스템 및 툴 인트로스펙션 검증**:
   - 공식 SQL Server JDBC 드라이버, `go-mssqldb`, XORM, DBeaver/SSMS 메타데이터 조회 검증 통과.
