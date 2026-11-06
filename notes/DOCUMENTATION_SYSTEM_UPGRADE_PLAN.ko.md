# LionSQL 공식 문서 및 엔지니어링 지식 체계 고도화 마스터 플랜 (`notes/DOCUMENTATION_SYSTEM_UPGRADE_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **최종 갱신일**: 2026-11-06
> - **과제 식별자**: `DOC-REVAMP-10`
> - **상태**: 계획 수립 (Planned / Dashboard Registered)

---

## 1. 개요 및 목적 (Executive Summary)

LionSQL은 단일 정적 바이너리 내에 고성능 OLTP 엔진(In-Memory OCC / WiredTiger), T-SQL VM, 멀티 와이어 프로토콜(PGWire :5433, TDS :1433), Web Studio, 자체 SPA 웹 서버(`--app`)까지 완비한 엔터프라이즈 데이터베이스입니다.

문서의 양만 인위적으로 부풀리는 오버스펙(VitePress 별도 웹사이트, 중복 Swagger 번들링 등 YAGNI 위반 항목)을 과감히 배제하고, **개발자와 협업자가 실제로 매일 활용할 수 있는 실질적 가치 중심의 10대 알짜배기 고도화 과제(`DOC-REVAMP-10`)**를 확정하여 실행합니다.

---

## 2. 10대 핵심 과제 종합 일람표 (Top 10 Essential Upgrades)

| 순위 | 과제 ID | 과제명 (Task Name) | 도메인 | 기대 효과 (ROI) |
| :---: | :--- | :--- | :---: | :--- |
| **1** | `DOC-KO-CORE-1` | **핵심 영문 문서 4종 한글 공식화 (`*.ko.md`)** | 국문화 | 진입 장벽 완전 제거 (아키텍처, 벤치마크, 툴 연동, VDBE) |
| **2** | `DOC-SPEC-OUT-1` | **14대 기각 결정(Negative Knowledge) 전용 카탈로그 신설** | 아키텍처 | 헛바퀴 개발 방지 및 아키텍처 불변성 단일 SSOT 확립 (`notes/SPEC_OUT_DECISIONS.ko.md`) |
| **3** | `DOC-EX-STARTER-1` | **내장 SPA 웹 서버 1분 스타터 예제 저장소 (`examples/`)** | DX/체험 | `lionsql server --app=...` 즉시 체험 가능한 단일 파일/Flat 폴더 예제 |
| **4** | `DOC-LINT-CI-1` | **로컬 마크다운 링크 깨짐(Broken Links) 검사기 도입** | 품질/무결성 | 수작업 링크 누락 방지 및 단위 테스트 연동 (`test/doc_links_test.go`) |
| **5** | `DOC-TSQL-CHEAT-1` | **T-SQL 함수/문법 3초 검색 치트시트 (`doc/TSQL_CHEATSHEET.ko.md`)** | 레퍼런스 | 개발자 검색 시간 90% 단축 (함수별 지원 여부/대체안 매트릭스) |
| **6** | `DOC-ERR-CODE-1` | **에러 코드 & SQLSTATE 트러블슈팅 사전 신설** | 운영/장애 | `40001`, `42P01` 등 실전 장애 대응 원클릭 해결 가이드 |
| **7** | `DOC-JOURNEY-NAV-1` | **문서 간 선형 사용자 여정(Next Steps) 네비게이션 표준화** | 가독성 | 가이드 하단 `← 이전 단계 \| 다음 단계 →` 일관된 흐름 구성 |
| **8** | `DOC-GUIDE-BADGE-1` | **10대 오픈소스 연동 가이드(`guide/`) 메타데이터 카드 표준화** | 실전 연동 | 대상 오픈소스 버전, 프로토콜, 검증 일자 일목요연화 |
| **9** | `DOC-CHART-MERMAID-1` | **실측 벤치마크 데이터의 Mermaid XYChart / 다이어그램 시각화** | 벤치마크 | 104만 tpmC 및 타사 DB 대비 성능 우위의 시각적 각인 |
| **10** | `DOC-MIGRATE-CHEAT-1` | **MSSQL ➔ LionSQL 마이그레이션 실무 체크리스트 신설** | 도입/전환 | 기존 T-SQL 프로시저 및 테이블 이전 체크리스트 제공 |

---

## 3. 세부 과제별 실행 명세

### 1단계: 즉시 실행 핵심 과제 (Top 1 ~ 4)

#### 과제 1. `DOC-KO-CORE-1`: 핵심 영문 문서 4종 한글 공식화 (`*.ko.md`)
- **대상 파일**:
  1. [`doc/ARCHITECTURE.md`](../doc/ARCHITECTURE.md) ➔ `doc/ARCHITECTURE.ko.md`
  2. [`doc/BENCHMARK.md`](../doc/BENCHMARK.md) ➔ `doc/BENCHMARK.ko.md`
  3. [`doc/CLIENT_TOOL_INTEGRATION.md`](../doc/CLIENT_TOOL_INTEGRATION.md) ➔ `doc/CLIENT_TOOL_INTEGRATION.ko.md`
  4. [`doc/internal/VDBE.md`](../doc/internal/VDBE.md) ➔ `doc/internal/VDBE.ko.md`
- **목표**: 영문 원본의 기술적 깊이를 100% 보존하면서 자연스러운 한글 공식 문서로 구축하고 카탈로그 동기화.

#### 과제 2. `DOC-SPEC-OUT-1`: 14대 기각 결정(Negative Knowledge) 전용 카탈로그 신설
- **신설 파일**: `notes/SPEC_OUT_DECISIONS.ko.md`
- **목표**: PL/pgSQL 중복 인터프리터 기각, XML DML 기각, OCC 백그라운드 진공 기각, 분산 2PC(XA) 기각, Wasm UDF 기각, MySQL DML 기각, 인메모리 압축(Option B) 기각, 빈 괄호 `IN ()` 무분별한 토글 플래그 기각 등 14대 Spec-Out 항목의 이론적·실전적 배경(Why Not)을 집대성하여 AI와 협업자의 헛바퀴 완벽 차단.

#### 과제 3. `DOC-EX-STARTER-1`: 내장 SPA 웹 서버 1분 스타터 예제 저장소 (`examples/`)
- **신설 경로**: 
  - `examples/single-file-dashboard/index.html` (Tailwind CDN + LionSQL JS SDK 단일 파일 대시보드)
  - `examples/flat-spa-app/` (`index.html`, `app.js`, `style.css` Flat 구조 SPA)
- **목표**: `lionsql server --app=./examples/single-file-dashboard/index.html` 명령어로 클론 즉시 체험할 수 있는 환경 제공.

#### 과제 4. `DOC-LINT-CI-1`: 로컬 마크다운 링크 깨짐(Broken Links) 자동 검사기
- **신설 파일**: `test/doc_links_test.go`
- **목표**: `doc/`, `notes/`, `guide/`, 최상위 README 내의 모든 마크다운 상대 경로 링크(`[text](path.md)`)를 정규식 없이 순회 파싱하여, 실제 물리 파일 존재 여부를 `go test -v ./test`로 1초 만에 전수 검증.

---

### 2단계: 실무 생산성 및 시각화 과제 (Top 5 ~ 10)

#### 과제 5. `DOC-TSQL-CHEAT-1`: T-SQL 함수/문법 3초 검색 치트시트 (`doc/TSQL_CHEATSHEET.ko.md`)
- 알파벳 A-Z 순서로 지원 함수를 정렬한 1페이지 압축 매트릭스 (반환 타입, 지원 시작 버전, 간단한 예시 및 미지원 함수의 완벽한 대체안 제시).

#### 과제 6. `DOC-ERR-CODE-1`: 에러 코드 & SQLSTATE 트러블슈팅 사전
- `SQLSTATE 40001` (Serialization Failure / Write Conflict), `42P01` (Undefined Table), `23505` (Unique Violation), TDS 에러 50000번대의 발생 원인과 해결 레시피를 일목요연하게 정리.

#### 과제 7. `DOC-JOURNEY-NAV-1`: 사용자 여정 네비게이션 표준화
- 모든 `doc/` 가이드 하단에 `← 이전 단계 | 다음 단계 →` 푸터 네비게이션 바를 통일 적용하여 독자의 학습 흐름 유도.

#### 과제 8. `DOC-GUIDE-BADGE-1`: 10대 오픈소스 연동 가이드 메타데이터 카드 표준화
- `guide/*.ko.md` 최상단에 대상 앱 공식 지원 버전, 권장 포트, 기본 관리자 계정, 검증 완료일을 표준 테이블 카드로 통일.

#### 과제 9. `DOC-CHART-MERMAID-1`: 실측 벤치마크 데이터의 Mermaid XYChart 그래픽 시각화
- 텍스트 숫자로만 표현된 TPC-C 104만 tpmC, 송금 SP 16만 TPS, WiredTiger vs OCC 비교를 GitHub 네이티브 `mermaid xychart-beta`로 시각화.

#### 과제 10. `DOC-MIGRATE-CHEAT-1`: MSSQL ➔ LionSQL 마이그레이션 실무 체크리스트
- 기존 MS SQL Server 프로시저 및 PostgreSQL DDL을 LionSQL로 손쉽게 이전하는 실무 변환 팁 및 주의사항 가이드.

---

## 4. 백로그 동기화 (`TODO.md`)

본 계획서의 10대 과제는 `[RULE:ContinuousBacklog]`에 따라 최상위 [`TODO.md`](../TODO.md)의 **`1.2 1.0 신뢰성 검증 게이트 (Release Gates)`**에 **`DOC-REVAMP-10`**으로 등록되어 추적 관리됩니다.
