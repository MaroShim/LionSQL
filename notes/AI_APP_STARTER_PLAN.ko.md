# AI 앱 스타터 포지셔닝 및 에이전트 친화 문서 계획서 (`notes/AI_APP_STARTER_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-09
> - **상태**: Pending (Post-1.0 후보)
> - **과제 식별 코드**: `AI-STARTER-1` ~ `AI-STARTER-4`
> - **목표**: AI로 서비스를 생성할 때 "SQLite처럼 가벼운 PostgreSQL 호환 서버 DB"로 LionSQL을 채택하게 하는 문서/포지셔닝/임베디드 경로 정비

---

## 1. 배경 및 판단

- 웹 서버와 DB 연동(Gitea, Grafana, Casdoor, Iota 등) 및 SQLLogicTest 100% 통과로 PGWire/TDS 호환성이 입증되었다.
- 바이너리 1개로 DB, Web Studio, REST(`/api/records`), 웹훅, RLS, SSE, 크론, 메일러가 동작하므로 AI 생성 서비스의 백엔드로 적합하다.
- SQLite 대비 우위: 다중 프로세스/네트워크 접속, 동시 쓰기. 열위: 라이브러리 링크형 임베딩 부재, beta 성숙도.
- 따라서 포지셔닝은 "SQLite 대체"가 아니라 **"SQLite처럼 가벼운 Postgres 호환 서버"**로 한다.

## 2. 과제 목록

| ID | 과제 | 우선순위 | 산출물 |
| :--- | :--- | :---: | :--- |
| `AI-STARTER-1` | AI 에이전트용 컨텍스트 문서 (`llms.txt`, `AGENTS.md` 템플릿): 접속 문자열, 지원 문법, 제약 사항, 권장 드라이버 | 2 | `llms.txt`, `doc/AI_AGENT_GUIDE.md` |
| `AI-STARTER-2` | 랜딩 페이지 "AI 앱 스타터" 섹션 및 Go/Node/Python 최소 예제 (`doc/`) | 2 | `LionSQL.com/index.html`, `doc/AI_APP_QUICKSTART.md` |
| `AI-STARTER-3` | 호환성 매트릭스 문서 (되는 것/안 되는 것, SQLLogicTest 근거 연결) | 2 | `doc/COMPATIBILITY_MATRIX.md` |
| `AI-STARTER-4` | Go 임베디드 모드 타당성 조사 (프로세스 내 `import` 구동, `database/sql` 드라이버, SQLite 대비 비교) | 3 | 연구 노트 (아키텍처 불변식 4 `Zero Sidecars`와의 정합성 검토 포함) |

## 3. 실행 순서

1. `AI-STARTER-1`: 비용 대비 효과가 가장 크므로 최우선.
2. `AI-STARTER-3`: 1번의 입력 자료.
3. `AI-STARTER-2`: 1, 3번 완료 후 랜딩 반영.
4. `AI-STARTER-4`: 별도 연구 트랙. 구현 착수 전 별도 PLAN 문서 작성.

## 4. 제약

- 단일 카탈로그/단일 프로세스 불변식(Invariant 2, 4)을 깨지 않는다. 임베디드 모드는 동일 엔진을 프로세스 내에서 구동하는 형태로만 검토한다.
- 호환 범위 밖 기능을 홍보 문구에 포함하지 않는다.

