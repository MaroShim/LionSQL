# LionSQL 인스턴트 웹 앱 예제 저장소 (`examples/README.ko.md`)

> - **최초 작성일**: 2026-11-12
> - **언어**: 한국어 ([English](README.md))

이 디렉터리는 LionSQL의 내장 단일 바이너리 정적 웹 서버(`--app` 플래그)를 통해 즉시 호스팅할 수 있는 프론트엔드 실전 예제들을 제공합니다. 별도의 Nginx, Node.js, Caddy 없이 데이터베이스와 웹 프론트엔드를 동시에 서비스합니다.

---

## 예제 1: 단일 파일 대시보드 (`examples/single-file-dashboard`)

Tailwind CSS CDN과 LionSQL REST API 호출을 단 하나의 HTML 파일에 압축 구현한 독립형 대시보드입니다.

### 실행 방법:
```bash
./bin/lionsql server --app=./examples/single-file-dashboard/index.html
```

브라우저에서 `http://localhost:8090/`에 접속합니다. API 경로(`/api/...`)와 Web Studio(`/_/`)를 제외한 모든 요청이 단일 HTML로 응답되며, 동일 오리진(Same-Origin)에서 즉시 데이터 조회가 가능합니다.

---

## 예제 2: Flat 디렉터리 SPA 앱 (`examples/flat-spa-app`)

HTML5 해시 라우팅, 실시간 테이블 목록 탐색 및 대화형 SQL 쿼리 실행을 지원하는 3개 파일(Flat 구조) 싱글 페이지 애플리케이션입니다.

### 파일 구성:
- `index.html`: 진입 HTML 문서
- `app.js`: 클라이언트 라우팅 및 비동기 쿼리 로직
- `style.css`: 모던 다크 테마 스타일시트

### 실행 방법:
```bash
./bin/lionsql server --app=./examples/flat-spa-app
```

브라우저에서 `http://localhost:8090/`에 접속합니다. 확장자가 있는 정적 에셋(`style.css`, `app.js`)은 루트에서 직접 서빙되고, 일반 라우팅 요청은 `index.html`로 자동 폴백됩니다.
