# [DEPRECATED] LionSQL Legacy Task Dashboard (`TODO.md`)

> **[DEPRECATED NOTICE / 문서 지원 종료 및 단일 창구 이관]**
> **본 `TODO.md` 문서는 2026-11-11부로 공식 지원 종료(Deprecated)되었으며, 더 이상 태스크나 진행 현황을 보관하지 않습니다.**
>
> 이중 관리(Dual-Truth)로 인한 정보 노후화를 원천 차단하기 위해, LionSQL의 모든 구현 과제·버그 패치·아키텍처 결정·로드맵 추적은 **SQLite 스프린트 DB(`./bin/sprint`)**와 자동 미러링 문서인 **[`TASKS.md`](TASKS.md)** 단일 창구(SSOT)로 완전 통합되었습니다.

---

## 공식 태스크 조회 및 관리 안내

- **전체 태스크 마스터 레지스터 (자동 동기화)**: [`TASKS.md`](TASKS.md)
- **CLI 조회 및 관리**:
  ```bash
  ./bin/sprint list                              # 전체 태스크 및 상태 일괄 조회
  ./bin/sprint show <ID>                         # 특정 태스크 상세 및 설계서 조회
  ./bin/sprint add <ID> "<TITLE>" <DOMAIN> <PRIO> [PLAN_DOC]
  ./bin/sprint set <ID> <PENDING|IN_PROGRESS|DONE> && ./bin/sprint backup TASKS.md
  ```
- **연계 핵심 문서**:
  - 전수 감사 결함 기록부 (40대 결함 패치 추적): [`notes/FULL_CODEBASE_ADVERSARIAL_AUDIT.ko.md`](notes/FULL_CODEBASE_ADVERSARIAL_AUDIT.ko.md)
  - Post-1.0 후보 & P2 이관 백로그 (92건): [`notes/POST_1.0_CANDIDATES.ko.md`](notes/POST_1.0_CANDIDATES.ko.md)
  - 영구 Spec-Out 및 아키텍처 헌법: [`GEMINI.md`](GEMINI.md), [`notes/ENGINEERING_CONTEXT.ko.md`](notes/ENGINEERING_CONTEXT.ko.md)
  - 전략 로드맵: [`ROADMAP.md`](ROADMAP.md)
