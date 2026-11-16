# LionSQL Chroot 샌드박스 SPA 핸들러 고도화 계획서 (`STANDALONE_SANDBOX_SPA_HANDLER_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-16
> - **문서 버전**: v1.0
> - **상태**: 승인 완료 / 차기 마이너 구현 대기 (Approved - Scheduled for v0.94.0)
> - **대상 릴리즈**: LionSQL `v0.94.0`
> - **관련 표준 규약**: `[RULE:PlanPersistence]`, `[RULE:PathSecurity]`, `[RULE:RadicalKISS]`, `[RULE:PlatformPrimitives]`, `GEMINI.md` (Invariant 4)

---

## 1. 개요 및 목적

LionSQL은 단일 정적 바이너리 하나로 데이터베이스 커널, 와이어 리스너, 그리고 관리용 Web Studio를 통합 제공합니다. 

현재 서버에는 1인 풀스택 개발자 및 인디 해커를 위한 **정적 웹 에셋 / SPA(Single-Page Application) 호스팅 기능(`-app` 플래그)**이 탑재되어 있습니다. 이를 통해 사용자는 Nginx나 Caddy 같은 별도의 리버스 프록시나 웹 서버를 구축하지 않고도, LionSQL 단일 프로세스로 React/Vue/Svelte 프론트엔드와 REST API 백엔드를 한 번에 서비스할 수 있습니다 (Zero-Config DX).

그러나 현재 구현체(`pkg/api/spa.go`)는 **"과거의 엄격한 Flat 디렉토리 불변식"**이라는 이름(`flatDirectoryHandler`)을 유지하면서도, 실제로는 서브디렉토리를 허용하는 **명칭과 동작의 불일치(Naming Inconsistency)** 및 수동 문자열 검사 기반의 임시방편 경로 가드를 지니고 있습니다.

본 계획서는 Go 표준 라이브러리(`io/fs`, `os.DirFS`, `http.FS`)가 제공하는 **공식 Chroot 파일시스템 샌드박스를 도입**하여:
1. 현대 번들러(Vite, Webpack, Next.js 정적 익스포트)의 기본 서브디렉토리(`dist/assets/`, `dist/static/`)를 **아무런 추가 빌드 설정 없이 즉시 서빙**하고,
2. 경로 순회(`../../`), 숨김 파일(`.env`, `.git`) 접근을 **Go 표준 라이브러리 레벨에서 100% 원천 차단**하는 차세대 `SandboxSPAHandler` 아키텍처를 수립합니다.

---

## 2. 현행 구현체의 한계 및 기술 부채

```go
// pkg/api/spa.go 현행 코드의 모순
type flatDirectoryHandler struct { ... } // 이름은 Flat(1계층 전용)

// isSafeFlatAsset 내부 주석
// 3. Subdirectory paths are safely permitted (e.g. "/assets/*.js", "/static/*") provided they do not escape the root
```

1. **개념적 모순**: 구조체와 생성자 이름은 `FlatDirectoryHandler`인데, 내부 주석과 조건문은 서브디렉토리를 허용하고 있어 코드 가독성과 설계 의도를 왜곡함.
2. **수동 문자열 파싱 위험**: `strings.Contains(clean, "..")` 및 `FieldsFunc` 기반의 수동 문자열 분할 검사는 슬래시 인코딩, Windows 역슬래시(`\`), 유니코드 정규화 등 미세한 엣지 케이스에서 방어 누락 위험이 존재함.
3. **MIME 타입 및 정적 캐싱 미흡**: SPA 라우팅 시 확장자가 없는 URL(`/*`)과 실제 정적 에셋(`.js`, `.css`, `.svg`, `.wasm`) 간의 캐시 제어 헤더(`Cache-Control`, `ETag`) 및 MIME 스푸핑 방어가 체계화되지 않음.

---

## 3. 핵심 아키텍처 설계 (`SandboxSPAHandler`)

```
┌────────────────────────────────────────────────────────────────────────┐
│                        HTTP Request (GET /assets/app.js)               │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│  SandboxSPAHandler (pkg/api/spa.go)                                    │
│  ├─ 1. Hidden File Firewall                                            │
│  │     - "."으로 시작하는 모든 세그먼트 (.env, .git) 404 즉시 차단       │
│  ├─ 2. Go Standard Library Sandbox (os.DirFS)                          │
│  │     - fs.ValidPath 검증 (상위 디렉터리 ".." 및 절대경로 완전 감금)  │
│  │     - http.FS(os.DirFS(root))를 통한 안전한 Chroot I/O              │
│  ├─ 3. Asset vs Navigation Dispatcher                                  │
│  │     - 확장자 보유 파일: http.FileServer 직접 서빙 (MIME, 304 NotMod)│
│  │     - 확장자 미보유 경로 (/dashboard, /users/42): index.html 폴백  │
│  └─ 4. Security & Caching Headers                                      │
│        - index.html: Cache-Control: no-cache, must-revalidate           │
│        - /assets/* : Cache-Control: public, max-age=31536000, immutable│
└────────────────────────────────────────────────────────────────────────┘
```

### 1) Go 표준 `os.DirFS` Chroot 샌드박스 채택 (`[RULE:PlatformPrimitives]`)
- Go 1.16+의 `os.DirFS(dirPath)`는 파일시스템을 특정 디렉터리에 엄격히 가두는(Chroot) 표준 메커니즘입니다.
- 경로에 `..`가 포함되거나, 선행 슬래시(`/`), Windows 볼륨 문자(`C:`)가 유입될 경우, 운영체제 시스템 콜을 호출하기도 전에 `fs.ErrInvalid`를 반환하여 **디렉터리 탈출 공격을 원천 무력화**합니다.

### 2) 숨김 파일 방화벽 (Dot-File Firewall)
- `.env`, `.git`, `.DS_Store`, `.htaccess` 등 온갖 민감 파일에 대한 접근은 URL 경로에 `.` 접두사 세그먼트가 발견되는 즉시 `404 Not Found`로 차단합니다.

### 3) SPA HTML5 History API 스마트 폴백 라우팅
- 브라우저 클라이언트 사이드 라우팅(`vue-router`, `react-router`) 지원:
  - 실제 디스크에 존재하는 파일(확장자 포함): 해당 정적 파일 서빙.
  - 디스크에 없는 가상 URL(예: `/settings/profile`): `index.html`을 200 OK로 서빙하여 클라이언트 라우터가 페이지를 렌더링하도록 지원.
  - 디스크에 없는 자산(예: `/assets/missing-image.png`): `index.html`을 반환하지 않고 **정직하게 404 Not Found**를 반환하여 스크립트/이미지 태그 구문 오류 방지.

---

## 4. 변경 대상 패키지 및 명세

### 1) `pkg/api/spa.go` 리팩토링
- `flatDirectoryHandler` 구조체 및 생성자 폐기 ➔ `sandboxSPAHandler` / `NewSandboxSPAHandler`로 대체.
- `http.FS(os.DirFS(cleanPath))` 기반 단일 핸들러 통합.
- 불필요한 수동 문자열 검사 함수 제거 및 `fs.ValidPath` 표준 준수.

### 2) `pkg/api/server.go` 연동
- `SetAppPath(appPath string)`에서 파일/디렉터리 판단 후 `NewSingleFileHandler` 또는 `NewSandboxSPAHandler` 바인딩.
- 서버 기동 배너 출력 정비:
  ```
  [Web Studio]     Active (http://127.0.0.1:8090/_/)
  [SPA App Host]   Active (http://127.0.0.1:8090/ -> ./dist, Sandbox: os.DirFS)
  ```

---

## 5. 단계별 구현 및 검증 계획 (v0.94.0)

### Phase 1: `pkg/api/spa.go` 샌드박스 코어 개편
- `os.DirFS` 기반 핸들러 작성 및 숨김 파일 방화벽 미들웨어 장착.

### Phase 2: 단위 및 보안 침투 테스트 (`pkg/api/spa_test.go`)
- **보안 검증**:
  - `GET /../../etc/passwd` ➔ 404 차단
  - `GET /assets/../../../etc/shadow` ➔ 404 차단
  - `GET /.env`, `GET /assets/.git/config` ➔ 404 차단
  - Windows 역슬래시(`..\..\windows\system32`) ➔ 404 차단
- **기능 검증**:
  - `GET /` ➔ `index.html` (200 OK)
  - `GET /assets/index-Dk39f.js` ➔ 서브디렉토리 정상 서빙 (Content-Type: `application/javascript`)
  - `GET /dashboard/analytics` ➔ `index.html` SPA 폴백 서빙

### Phase 3: 공식 예제 SPA 애플리케이션 번들 검증
- `examples/flat-spa-app` 및 실제 Vite 번들 프로젝트(`dist/`)를 타깃으로 통합 스모크 테스트 실행.

---

## 6. 기대 효과

1. **최고 수준의 1인 개발자 DX**: Vite/React 빌드 후 `lionsql server -app=./dist` 한 줄로 완벽한 풀스택 서빙 실현.
2. **보안 신뢰성 보장 (`[RULE:PathSecurity]`)**: 표준 라이브러리 레벨의 Chroot 가드로 경로 탈출 공격 면적 완전 제거.
3. **코드베이스 단순화 (`[RULE:RadicalKISS]`)**: 낡은 임시방편 문자열 검사 로직을 Go 표준 `os.DirFS` 1줄로 통합.
