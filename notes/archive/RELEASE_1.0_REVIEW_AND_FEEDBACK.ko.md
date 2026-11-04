# LionSQL 1.0 릴리즈 사전 아키텍처 리뷰 및 종합 피드백 보고서

> [!NOTE]
> **검토 결과 조치 완료 안내 (Resolution Status)**:
> 본 보고서는 공식 1.0 릴리스 이전에 진행된 사전 종합 진단서입니다.
> 본 문서의 제2절에서 지적되었던 **5대 핵심 문제점(브랜드 통일 `BRAND-UNIFY-1`, 세션 변수 이스케이프 `SEC-ESC-1`, 쿼리 3갈래 경로의 2-Tier 단일화 `KISS-5/ARCH-2TIER-1`, 프로덕션 기본 보안 가드레일 `PROD-SEC-1`, 1.0 출시 기준 동기화)**은 **v0.90.0 Betas 스프린트(Beta 1 ~ Beta 3)에서 모두 100% 조치 완료([x])**되었습니다.

## 1. 개요 (Executive Summary)

본 보고서는 LionSQL의 공식 1.0 LTS(Long-Term Support) 릴리즈를 앞두고, 전체 코드베이스(311개 Go 파일, 약 12.6만 라인의 직접 구현 코드)와 아키텍처, 와이어 프로토콜, 보안 정책, 문서 정합성을 전수 점검하여 도출한 기술적 총평 및 개선 과제 명세서입니다.

### 핵심 강점
1. **독보적인 제품 포지셔닝 (Product-Market Fit)**:
   - 약 50MB 단일 독립 실행형 정적 바이너리(Zero-Dependency) 하나로 **Microsoft SQL Server T-SQL 및 저장 프로시저(VDBE VM)**, **PostgreSQL Wire v3(:5433) 및 TDS(:1433) 이중 와이어 프로토콜**, **WiredTiger MVCC 및 Pure-Go In-Memory OCC 이중 스토리지**, 그리고 **내장 Web Studio(:8090) 및 REST BaaS API**를 제공하는 독보적인 완성도를 확보했습니다.
2. **명확한 설계 절제력 (Spec-Out 원칙)**:
   - 단일 프로세스 내 멀티 DB 카탈로그 배제(인스턴스 격리), PL/pgSQL 중복 엔진 배제, 인메모리 Vacuum 스레드 배제 등 핵심 OLTP 성능과 단일 바이너리 순수성을 지키기 위해 배제할 영역을 명확히 정의했습니다.
3. **엔지니어링 밀도와 하드웨어 포화**:
   - VDBE 레지스터/아레나 `sync.Pool` 재사용, 프리페어드 스테이트먼트 바이트코드 직결 캐싱, 글로벌 메모리 거버너(`debug.SetMemoryLimit` 70% + `work_mem` 외부 정렬 디스크 스필), 140개의 철저한 테스트 파일 등으로 높은 런타임 안정성을 갖추었습니다.

---

## 2. 1.0 릴리즈 직전 5대 핵심 문제점 및 분석

### ① 브랜드/네이밍 분열 (LionSQL vs 레거시 명칭) — [조치 완료: `BRAND-UNIFY-1`]
- **현상**: 문서 및 UI 가이드라인상 브랜드 명칭을 `LionSQL`로 단일화했으나, 실제 런타임 코드와 프로토콜 응답에 레거시 명칭이 일부 남아 있어 문서와 실제 동작이 불일치함.
  - **웹훅 헤더 일치화 (`pkg/api/webhook.go`)**: 웹훅 헤더를 `X-LionSQL-Signature`, `X-LionSQL-Event`로 단일화 완료.
  - **클라이언트 SDK (`pkg/api/sdk/lionsql.js`)**: `/sdk/lionsql.js` 및 `class LionSQL` 일원화 완료.
  - **시스템 함수/프로토콜 응답**: `DB_NAME()`, `APP_NAME()`, `SHOW server_version`, `SELECT @@VERSION`, TDS `LOGINACK` 서버명을 `LionSQL`로 완전 단일화.

### ② 세션 변수(`@var`) 치환 이스케이프 취약점 및 임시 테이블 누수 위험
- **세션 변수 문자열 치환 시 작은따옴표 미이스케이프 (`pkg/engine/sqlexec.go:911-919`)**:
  - `varRep := fmt.Sprintf("'%v'", v)`로 단순 대입하여, 변수 값에 작은따옴표(예: `O'Reilly`) 포함 시 SQL 문법 오류 또는 2차 SQL 인젝션 유발 가능.
- **테이블 변수(`DECLARE @t TABLE`) 물리 테이블 잔류 위험 (`pkg/engine/sqlexec.go:762-783`)**:
  - Ad-hoc 세션에서 테이블 변수 선언 시 생성되는 `__tv_<sessionID>_<varName>` 물리 테이블이 세션 종료(`CloseSession`) 시 확실히 드롭되는지 보장 필요.

### ③ 쿼리 실행 경로의 3중 구조
- **현상**: 모든 쿼리가 단일 VDBE VM에서 실행된다고 기술되어 있으나, 실제 Ad-hoc 쿼리는 ① `sqlexec_select.go`(자체 파싱/평가), ② Volcano `execution` 반복자(`execVolcanoSelect`), ③ VDBE VM(`proc`)의 3갈래 경로가 공존함.
- **방향**: 1.0 릴리즈 직전에 대규모 통합 리팩토링은 회귀 위험이 높으므로 현행을 유지하되, 함수 평가/NULL 전파 일관성을 회귀 테스트로 고정하고 Post-1.0에서 단일화 추진.

### ④ 프로덕션 기본 보안 가드레일 (Zero-Config vs Production Safety)
- **현상**: 기본 관리자 계정(`admin@lionsql.local` / `1234567890`)이 하드코딩되어 있으며 기동 시 경고만 출력됨.
- **방향**: 외부 네트워크 인터페이스(`0.0.0.0` 등) 바인딩 시 기본 비밀번호 사용을 제한하거나 경고를 강화하는 방어책 마련 권장.

### ⑤ 문서 간 1.0 출시 기준(Exit Criteria) 불일치 및 파일 권한
- **현상**: `ROADMAP.md`의 Gate 4(암호화 불변 감사 로그)는 1.0 필수 조건으로 기술되어 있으나, `TODO.ko.md`에서는 `Post-1.0 연기`로 표기되어 상충됨.
- **파일 권한**: `doc/README.md`, `doc/REFERENCE_MANUAL.ko.md`의 권한이 `0600`으로 되어 있어 `0644` 정규화 필요.

---

## 3. 1.0 릴리즈를 위한 단계별 액션 플랜

1. **Step 1 — 세션 변수 이스케이프 및 임시 객체 라이프사이클 핫픽스**:
   - 세션 변수(`@var`) 치환 시 작은따옴표 이스케이프(`''`) 적용.
   - 세션 종료(`CloseSession`) 시 `TableVars` 물리 테이블 완전 정리 로직 추가.
2. **Step 2 — 브랜드/헤더 일관성 정돈**:
   - 웹훅 헤더 `X-LionSQL-*` 적용 (하위 호환용 `X-LionSQL-*` 동시 지원).
   - `/sdk/lionsql.js` 및 `window.LionSQL` SDK 별칭 추가.
3. **Step 3 — 1.0 로드맵/백로그 출시 기준 일치화 및 파일 권한 정규화**:
   - `ROADMAP.md`와 `TODO.ko.md`의 Gate 4 항목 일치.
   - 모든 문서 파일 권한 `0644` 보정.
4. **Step 4 — 전수 회귀 테스트 및 1.0 태깅**:
   - `go test -tags wiredtiger -count=1 -race ./...` 100% 통과 확인 후 `v1.0.0 LTS` 배포.
