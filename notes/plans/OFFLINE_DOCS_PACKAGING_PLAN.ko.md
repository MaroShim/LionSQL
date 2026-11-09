# LionSQL 배포 패키지 오프라인 문서 번들링 설계 및 구현 계획서 (`notes/OFFLINE_DOCS_PACKAGING_PLAN.ko.md`)

> - **최종 갱신일**: 2026-11-04
> - **최초 작성일**: 2026-11-04  
> - **문서 상태**: Approved Plan (Post-1.0 Packaging Enhancement)  
> - **과제 식별 코드**: `PKG-OFFLINE-DOCS-1`  
> - **목표**: Go 바이너리에 문서를 억지로 임베딩하지 않고, 공식 배포 압축 패키지(`tar.gz` / `zip`) 내에 소스코드를 제외한 순수 배포용 문서(`docs/`)와 오프라인 브라우저 뷰어를 함께 번들링하여 폐쇄망/에어갭 환경에서도 완벽한 브라우징 경험을 제공.  
> - **연계 문서**:  
>   - 릴리스 자동화 가이드: [`doc/RELEASE_AUTOMATION_GUIDE.ko.md`](../../doc/RELEASE_AUTOMATION_GUIDE.ko.md)  
>   - 릴리스 스크립트: [`scripts/release.sh`](../../scripts/release.sh)  
>   - 포스트 1.0 후보 과제(Candidates): [`notes/POST_1.0_CANDIDATES.ko.md`](../POST_1.0_CANDIDATES.ko.md)  

---

## 1. 배경 및 문제 제기 (Context & Motivation)

### 1.1 "사람들은 터미널이 아니라 브라우저로 문서를 본다"
현대 소프트웨어 엔지니어링 환경에서 개발자들은 터미널에서 마크다운 파일을 하나씩 열어보지 않고, **웹 브라우저를 통해 목차를 탐색하고 키워드를 검색하며 문서를 소비**합니다.

### 1.2 "DB 엔진 바이너리에 문서를 내장하지 않는다" (안티패턴 배제)
- DB 엔진 바이너리(`lionsql`)는 **초경량, 고성능 OLTP 런타임**으로 순수하게 유지되어야 합니다.
- 수십 개에 달하는 문서 텍스트와 웹 렌더러를 Go 바이너리(`embed.FS`)에 쑤셔 넣는 것은 바이너리 비대화와 유지보수 라이프사이클의 불일치(문서 오타 수정 시 DB 엔진 전체 재컴파일)를 초래합니다.
- PostgreSQL, Go 공식 배포판, JDK 등 업계의 검증된 표준은 **"순수 바이너리와 공식 문서를 단일 배포 아카이브(Archive)로 묶어 제공하는 방식"**입니다.

---

## 2. 듀얼 채널 릴리스 아키텍처 (Dual-Channel Release Strategy)

배포 파이프라인은 사용자의 목적과 시나리오에 따라 **"순수 단독 바이너리(Standalone Binary)"**와 **"풀 배포 아카이브(Full Distribution Archive)"**를 **GitHub Releases에 둘 다 별도로 동시 제공**합니다.

### 2.1 듀얼 채널 제공의 근거 (User Persona & Scenarios)
1. **채널 A: 순수 단독 바이너리 (CI/CD, Docker, 서버 자동화 엔지니어)**
   - *사용자 요구*: *"curl 한 줄로 바이너리만 받아서 `/usr/local/bin/`에 넣고 서비스 띄우고 싶다."*
   - *제공 형태*: `lionsql-<os>-<arch>` 단독 실행 파일 (압축 없음).
   - *장점*: 다운로드 즉시 실행 가능, `tar` 해제 및 임시 파일 정리 스크립트 불필요.
2. **채널 B: 풀 배포 아카이브 (일반 개발자, DBA, 온프레미스·폐쇄망 환경)**
   - *사용자 요구*: *"처음 설치해보는데 오프라인 매뉴얼과 설정 파일이 함께 묶인 완성된 패키지가 필요하다."*
   - *제공 형태*: `lionsql-v1.x.x-<os>-<arch>.tar.gz` (Windows는 `.zip`).
   - *장점*: 인터넷이 단절된 에어갭 환경에서도 압축 해제 후 `docs/index.html`을 더블 클릭하여 로컬 브라우저에서 공식 문서를 열람하며 작업 가능.

### 2.2 GitHub Release 산출물(Assets) 청사진
```text
[GitHub Release v1.x.x Assets 목록]

1. 순수 단독 실행 바이너리 (스크립트/자동화용)
   ├── lionsql-darwin-arm64
   ├── lionsql-linux-amd64
   └── lionsql-windows-amd64.exe

2. 풀 배포 아카이브 (오프라인 문서 포함 패키지)
   ├── lionsql-v1.x.x-darwin-arm64.tar.gz
   ├── lionsql-v1.x.x-linux-amd64.tar.gz
   └── lionsql-v1.x.x-windows-amd64.zip

3. 무결성 검증
   └── checksums.txt (전체 파일에 대한 SHA-256 해시)
```

---

## 3. 풀 배포 아카이브 내부 레이아웃 (Archive Directory Structure)

플랫폼별 아카이브(`lionsql-v1.x.x-<os>-<arch>.tar.gz` 및 `.zip`)는 다음과 같은 레이아웃을 갖습니다:

```text
lionsql-v1.x.x-darwin-arm64/
├── bin/
│   └── lionsql                     # [0-Dependency 순수 고성능 DB 엔진 바이너리]
├── docs/                           # [소스코드를 제외한 순수 오프라인 문서 패키지]
│   ├── index.html                  # 브라우저에서 더블 클릭 시 즉시 열리는 로컬 뷰어
│   ├── USER_MANUAL.html / .md      # 사용자 입문 및 운영 매뉴얼
│   ├── REFERENCE_MANUAL.html / .md # T-SQL, 빌트인 함수, REST API 전수 레퍼런스
│   ├── ARCHITECTURE.html / .md     # 엔진 아키텍처 및 스토리지 스펙
│   ├── BENCHMARK.html / .md        # 공인 성능 실측 데이터
│   ├── guides/                     # 10대 실전 앱 연동 가이드
│   └── assets/                     # 오프라인 CSS 스타일시트 (외부 CDN 의존 0)
├── README.md                       # 패키지 시작 가이드
└── LICENSE                         # 라이선스
```

### 핵심 사용자 경험 (UX Flow)
1. 사용자가 오프라인/폐쇄망 환경에서 `tar.gz` 압축을 해제합니다.
2. 터미널에서 `./bin/lionsql`을 실행하여 데이터베이스를 기동합니다.
3. 브라우저에서 `docs/index.html`을 **더블 클릭**하여 엽니다.
4. **인터넷 연결이 전혀 없는 환경에서도 로컬 브라우저에서 목차 트리, 전체 검색, 코드 블록 복사 기능을 갖춘 완전한 공식 문서를 탐색**할 수 있습니다.

---

## 4. 단계별 구현 계획 (Phased Execution Plan)

### Phase 1: 오프라인 HTML 뷰어 템플릿 설계 (`docs/index.html`)
- **외부 인터넷 의존성 0 (Zero CDN)**:
  - 구글 폰트나 외부 CDN(CDNJS, unpkg 등) 호출을 완전히 배제하고, 순수 인라인 CSS 및 바닐라 JS로 동작하는 모던 미니멀리스트 문서 뷰어 템플릿 작성.
- **좌측 내비게이션 & 우측 본문**:
  - 좌측: 4대 계층(핵심 매뉴얼, 개발자 가이드, 운영/보안, 앱 연동) 목차 트리.
  - 우측: 선택한 마크다운/HTML 본문 렌더링 영역 및 코드 하이라이팅.
  - 상단: 오프라인 클라이언트 사이드 즉시 텍스트 검색창(Quick Search).

### Phase 2: 릴리스 스크립트 패키징 자동화 (`scripts/release.sh`)
- `scripts/release.sh` 내에 `package_distribution()` 단계 추가:
  ```bash
  # 1. 대상 바이너리 빌드 (bin/lionsql)
  # 2. 배포 스테이징 디렉토리 구성 (dist/)
  # 3. doc/ 및 guide/ 마크다운 문서 정제 복사 (소스코드 및 내부 개발 메모 제외)
  # 4. docs/index.html 오프라인 브라우저 뷰어 번들링
  # 5. OS/Arch별 tar.gz 및 zip 아카이빙
  ```

### Phase 3: GitHub Actions 및 릴리스 배포 파이프라인 연동
- GitHub Release 생성 시 기존에 바이너리 3개만 올리던 방식에서, 완성된 배포 아카이브(`lionsql-*.tar.gz`, `lionsql-*.zip`)를 함께 업로드하도록 CI/CD 확장.

---

## 5. 기대 효과 및 불변식 검증 (Benefits & Invariants)

1. **바이너리 순수성 불변 ([`[RULE:StaticBinary]`])**:
   - `lionsql` 실행 파일의 크기는 1바이트도 늘어나지 않으며, 불필요한 마크다운 에셋이 Go 컴파일에 엮이지 않습니다.
2. **폐쇄망 완전 자립 ([`[RULE:PlatformPrimitives]`])**:
   - 웹 서버(Nginx, Node.js)나 인터넷 망 연결 없이도 브라우저 더블 클릭만으로 완벽한 문서를 열람할 수 있습니다.
3. **유지보수 분리 (Decoupled Lifecycles)**:
   - 문서가 수정되어도 DB 엔진의 Go 코드를 재컴파일할 필요가 없으며, 릴리스 아카이브 생성 시점의 최신 문서가 자동으로 포함됩니다.
