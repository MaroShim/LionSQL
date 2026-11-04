# 신규 실전 앱 4종 호환성 고속 검증 계획서 (`notes/EXTERNAL_APP_COMPATIBILITY_EXPLORATION_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-04
> - **상태**: 진행 중 (In Progress)
> - **대상 앱**: NocoDB, Mattermost, Ghost, Directus
> - **전략**: Fast-Fail (과도한 엔진 수정이나 구조 변경이 요구될 경우 즉시 Spec-Out 포기하고 다음 앱으로 신속 전환)
> - **원칙**: Radical KISS, Rule of Two, 표준 프로토콜(PGWire / TDS) 및 가상 카탈로그 경미 보완 수준으로 수렴

---

## 1. 추진 배경 및 Fast-Fail 기준
기존 10개 앱(Gitea, Casdoor, Grafana, Answer, Vaultwarden, Metabase, Umami 등)의 연동 자산을 바탕으로 신규 4종 앱의 실제 연동 가능성을 탐색합니다.
만약 특정 앱이 엔진 코어 구조를 뒤흔들거나 미지원 영역(예: 독자 PL/pgSQL 엔진, PostgreSQL 전용 C 확장 모듈 등)을 강제할 경우, 엔진을 무리하게 왜곡하지 않고 즉시 **Spec-Out**으로 판정하고 다음 후보로 넘어갑니다.

---

## 2. 검증 대상 4종 파이프라인

| 순번 | 대상 앱 | 스택 / ORM | 주요 프로토콜 | 예상 난이도 | Fast-Fail 체크포인트 |
|:---:|:---|:---|:---:|:---:|:---|
| **1** | **NocoDB** | Node.js / Knex.js | PGWire (`:5433`) | 낮음 | 메타데이터 인트로스펙션 및 UI CRUD |
| **2** | **Mattermost** | Go / morph 마이그레이션 | PGWire (`:5433`) | 중간 | 대량 마이그레이션 DDL & 세이브포인트 |
| **3** | **Ghost** | Node.js / Knex.js | PGWire / MySQL 편의 | 낮음 | 초기 블로그 스키마 마이그레이션 |
| **4** | **Directus** | Node.js / Knex.js | PGWire (`:5433`) | 중간 | 다이나믹 컬럼 스키마 인트로스펙션 |

---

## 3. 검증 단계별 절차
1. 앱의 DB 설정(환경변수)을 LionSQL PGWire/TDS 포트로 지정
2. 마이그레이션 및 초기 부팅 로그 실시간 모니터링
3. 미지원 구문이나 카탈로그 누락 발생 시:
   - 가상 카탈로그나 경미한 구문 보완(10줄 내외)으로 해결 가능한지 진단
   - 1시간 이상 코어 재설계가 필요하거나 Spec-Out 원칙에 위배되면 즉시 중단(Abort) 판정
4. 기본 CRUD 및 핵심 워크플로 E2E 통과 시 연동 성공 판정
