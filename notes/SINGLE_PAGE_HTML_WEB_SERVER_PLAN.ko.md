# LionSQL 단일 페이지 HTML (SPA) 호스팅 & 자체 DB 통합 웹서버 플랜 (`notes/SINGLE_PAGE_HTML_WEB_SERVER_PLAN.ko.md`)

## 1. 개요 및 비전 (Executive Summary & Vision)

> **"단 하나의 바이너리로 데이터베이스와 웹 애플리케이션을 동시에 서비스한다."**

LionSQL은 이미 고성능 스토리지 엔진(In-Memory OCC / WiredTiger), T-SQL 프로시저 VM, RESTful BaaS API(`GET/POST /api/records/...`, `POST /api/sql`, `GET /api/realtime`), 그리고 포트 `:8090`의 임베디드 Web Studio를 단일 정적 실행 파일로 제공하고 있습니다.

본 플랜은 LionSQL을 확장하여 사용자가 작성한 **단일 페이지 HTML(Single Page Application) 또는 정적 웹 에셋을 별도의 Nginx, Node.js, Caddy 없이 자체 웹서버로 직접 서빙하고, 동일 호스트에서 자체 DB를 즉시 활용할 수 있도록 지원하는 아키텍처**를 수립합니다.

```
+-------------------------------------------------------------------------------+
|                           LionSQL Single Binary (:8090)                       |
|                                                                               |
|   [ Browser / Client ]                                                        |
|           │                                                                   |
|           ├──> GET / (or /app/) ───> Embedded SPA Web Server                  |
|           │                         (index.html / static assets)              |
|           │                                                                   |
|           └──> /api/... ───────────> Native BaaS & SQL Engine                 |
|                                     (Same-Origin, Zero CORS, In-Memory/Disk)  |
+-------------------------------------------------------------------------------+
```

---

## 2. 타당성 분석 (Feasibility & Synergies)

### 2.1. 100% 실현 가능한 근거
1. **이미 완성된 HTTP 인프라**:
   - `pkg/api`는 Go의 고성능 `net/http` 라우터와 Gzip 압축, 보안 헤더, 요청 로거 미들웨어를 이미 완비하고 있습니다.
   - 정적 리소스를 제공하는 `embed.FS` 기반 Web Studio(`GET /_/`)가 이미 안정적으로 가동 중입니다.
2. **Same-Origin의 절대적 우위**:
   - 웹 애플리케이션(HTML)과 데이터베이스 API가 동일한 포트(`:8090`)에서 서빙되므로, **CORS(Cross-Origin Resource Sharing) 설정이나 복잡한 리버스 프록시가 전혀 필요 없습니다**.
   - 쿠키/로컬스토리지 기반 인증(`Bearer` 토큰, API 키)이 단일 도메인에서 완벽히 작동합니다.
3. **내장 JavaScript SDK (`lionsql.js`) 직결**:
   - LionSQL은 이미 단일 파일 JS SDK(`pkg/api/sdk/lionsql.js`)를 보유하고 있으므로, HTML 상단에 `<script src="/api/sdk/lionsql.js"></script>` 한 줄만 선언하면 즉시 풀스택 BaaS 앱이 완성됩니다.
4. **PocketBase / SQLite 패러다임의 발전형**:
   - PocketBase나 CouchDB처럼 "데이터와 앱이 하나로 묶인" 극한의 배포 편의성을 엔터프라이즈급 T-SQL과 OCC/WiredTiger 엔진 위에서 제공하게 됩니다.

---

## 3. 서빙 모델 비교 및 3단계 아키텍처 설계

LionSQL이 단일 페이지 HTML을 서빙하는 방식은 사용자 경험과 배포 환경에 따라 3가지 모델로 나눌 수 있습니다:

| 모델 (Model) | 서빙 소스 | CLI 구동 예시 | 장점 | 권장 시나리오 |
| :--- | :--- | :--- | :--- | :--- |
| **Model 1: 파일 기반 바인딩 (File-Backed)** | 로컬 디스크 파일/디렉토리 | `lionsql serve --app=./index.html`<br>`lionsql serve --public=./dist` | 코드 수정 즉시 새로고침 반영, 빌드 도구(Vite/Webpack) 연동 용이 | 일반 웹 개발, 사내 대시보드, 지속적 변경 |
| **Model 2: DB 내장 앱 (DB-Native App)** | LionSQL 시스템 테이블 (`sys_web_apps`) | `lionsql app deploy index.html`<br>`lionsql serve` | **DB 파일 하나에 프론트엔드와 데이터가 100% 통합 패키징**, 완전한 단일 파일 이전 | 프로덕션 배포, 에어갭(폐쇄망), 무설치 배포 |
| **Model 3: Web Studio 드래그앤드롭 (Studio UI)** | Web Studio 전용 관리 탭 | 브라우저 UI에서 `.html` 파일 업로드 | CLI 없이 브라우저만으로 즉시 배포 가능 | 비개발자, 운영자, 프로토타이핑 |

---

## 4. 세부 구현 아키텍처

```mermaid
flowchart TD
    Req["Incoming HTTP Request (:8090)"] --> Mux{"Path Routing"}
    
    Mux -->|"/_/"| Studio["Web Studio (Embedded Admin UI)"]
    Mux -->|"/api/..."| API["BaaS REST & SQL API Engine"]
    Mux -->|"/api/realtime"| SSE["Realtime SSE Change Stream"]
    
    Mux -->|"/ or /app/*"| SPAHandler["SPA Static Web Server"]
    SPAHandler --> CheckMode{"Serving Mode"}
    
    CheckMode -->|"--app / --public"| DiskFS["OS Local File / Directory"]
    CheckMode -->|"DB-Native"| TableFS["sys_web_apps Table (BLOB/CLOB)"]
    
    DiskFS --> SPAFallback{"File Exists?"}
    SPAFallback -->|Yes| ServeFile["Serve Static File (MIME + ETag + Gzip)"]
    SPAFallback -->|No (Route)| ServeIndex["Fallback to index.html (HTML5 History API)"]
```

### 4.1. Pillar 1: 정적 파일 서빙 및 HTML5 History API Fallback
Vue, React, Svelte 등 최신 SPA 프레임워크는 브라우저 단에서 URL 라우팅(`/users/123`, `/dashboard/settings`)을 처리합니다.
따라서 사용자가 하위 URL을 직접 입력하거나 새로고침했을 때 404가 뜨지 않고 `index.html`을 서사하는 **SPA Fallback 라우터**가 필수적입니다:

```go
func (s *Server) handleSPAServe(w http.ResponseWriter, r *http.Request) {
    // 1. 요청된 정적 에셋(JS, CSS, PNG 등)이 실제 존재하는지 확인
    // 2. 존재하면 적절한 MIME Content-Type 및 ETag/Cache-Control과 함께 전송
    // 3. 존재하지 않는 경로이거나 디렉토리 요청이면 index.html 내용을 전송 (HTML5 History fallback)
}
```

### 4.2. Pillar 2: CLI 플래그 및 환경 설정 확장
사용자가 원하는 파일이나 폴더를 손쉽게 지정할 수 있도록 CLI 인자를 추가합니다:
* `--app <file>`: 단일 HTML 파일 지정 (예: `lionsql serve --app=./index.html`)
  * 루트 경로(`GET /`) 접속 시 해당 HTML 파일을 즉시 렌더링.
  * 기존 Web Studio는 `GET /_/`에 온전히 유지되어 관리자 대시보드와 사용자 앱이 충돌 없이 공존.
* `--public <dir>`: 정적 웹 디렉토리 지정 (예: `lionsql serve --public=./dist`)
  * `index.html`을 비롯한 번들 에셋(CSS, JS, 이미지, 폰트)을 일괄 서빙.

### 4.3. Pillar 3: 데이터베이스 내장 앱 (DB-Native Stored Web Apps)
단일 파일 데이터베이스의 정체성을 극대화하기 위해, HTML 코드를 DB 내에 저장하는 기능을 제공합니다:
* 시스템 카탈로그 테이블: `__sys_apps`
  ```sql
  CREATE TABLE __sys_apps (
      name VARCHAR(64) PRIMARY KEY,
      content TEXT NOT NULL,       -- Single-page HTML 전체
      mime_type VARCHAR(64) NOT NULL,
      updated_at BIGINT NOT NULL
  );
  ```
* 전용 CLI 명령어:
  ```bash
  # 로컬 index.html을 DB 내 'main' 앱으로 등록
  lionsql app deploy ./index.html --name=main
  
  # DB 내 저장된 앱 확인
  lionsql app list
  ```
* 이렇게 배포된 앱은 `--data-dir` 데이터 디렉토리(또는 단일 DB 파일)를 복사하는 것만으로 **프론트엔드와 백엔드 데이터가 통째로 복제·마이그레이션**됩니다.

### 4.4. Pillar 4: 프론트엔드와의 Zero-Setup 인터랙션
단일 HTML 파일 안에서 LionSQL 자체 DB와 상호작용하는 예시:

```html
<!DOCTYPE html>
<html lang="ko">
<head>
  <meta charset="UTF-8">
  <title>LionSQL Self-Contained App</title>
  <!-- Same-origin SDK 자동 로드 -->
  <script src="/api/sdk/lionsql.js"></script>
</head>
<body>
  <h1>내장 LionSQL 앱</h1>
  <div id="data"></div>

  <script>
    // 호스트 주소 불필요 (Same-Origin)
    const db = new LionSQL();

    async function loadData() {
      // 1. REST BaaS API로 테이블 레코드 조회
      const users = await db.records('users').list();
      
      // 2. 또는 직접 T-SQL 실행
      const res = await db.sql('SELECT COUNT(*) as cnt FROM orders WHERE status = @s', { s: 'PAID' });
      
      document.getElementById('data').innerText = JSON.stringify(users);
    }
    loadData();

    // 3. 실시간 변경 스트림 (SSE) 구독
    db.realtime.subscribe('users', (event) => {
      console.log('실시간 데이터 변경 감지:', event);
      loadData();
    });
  </script>
</body>
</html>
```

---

## 5. 단계별 구현 로드맵 (Phased Roadmap)

### Phase 1: 파일 기반 단일 HTML & SPA 디렉토리 서빙 (`--app`, `--public`)
1. `pkg/config`: `NetworkConfig.AppFile` (`--app`), `NetworkConfig.PublicDir` (`--public`) 추가.
2. `pkg/api/server.go`:
   - `GET /` 라우팅을 확장하여, `--app` 또는 `--public`이 설정되어 있으면 해당 SPA 서빙 핸들러 구동.
   - 설정되지 않은 기본 상태에서는 기존대로 `/_/`(Web Studio)로 리다이렉트.
   - HTML5 History API Fallback 및 Gzip/MIME 자동 판별 로직 추가.
3. 테스트 작성: 단일 HTML 서빙, 하위 경로 Fallback, JS/CSS 에셋 서빙 통합 테스트.

### Phase 2: DB 내장 앱 저장소 (`DB-Native Stored Web Apps`)
1. `pkg/catalog` 또는 `pkg/api`: `__sys_apps` 시스템 테이블 스키마 초기화.
2. `cmd/lionsql`: `lionsql app deploy <file>`, `lionsql app list`, `lionsql app remove` CLI 추가.
3. `pkg/api`: 로컬 파일이 지정되지 않았을 때 DB의 `main` 앱을 우선 조회하여 서빙하는 엔진 결합.

### Phase 3: Web Studio 내 배포 UI 지원
1. Web Studio 사이드바에 `Apps` 탭 추가.
2. 브라우저에서 HTML 파일을 드래그앤드롭하여 즉시 배포 및 원클릭 바로가기 URL(`/app/`) 제공.

---

## 6. 결론: LionSQL의 차별화된 가치

이 기능이 도입되면 사용자는:
1. **서버 세팅 0초**: Nginx 설정, Node.js 설치, Docker Compose 구성 없이 `lionsql serve --app=index.html` 단 한 줄로 데이터베이스와 웹사이트를 동시 서비스할 수 있습니다.
2. **배포 아티팩트의 극단적 단순화**: 단 하나의 바이너리와 단 하나의 데이터 파일만으로 전 세계 어디서든 즉시 웹 기반 사내 시스템이나 서비스를 구동할 수 있습니다.
3. **완벽한 데이터 격리 및 로컬 퍼스트**: 고성능 In-Memory OCC와 WiredTiger의 ACID 트랜잭션을 웹 프론트엔드가 레이턴시 0ms 수준으로 온전히 향유하게 됩니다.

본 계획서는 Golden Rule 42에 의거하여 영구 보존됩니다.
