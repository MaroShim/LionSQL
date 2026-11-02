# Vikunja 연동 가이드 (LionSQL PGWire)

본 문서는 Trello, Asana, Todoist 등을 대체하는 직관적인 오픈소스 태스크 관리 및 프로젝트 칸반 플랫폼인 **Vikunja**를 LionSQL의 PostgreSQL Wire Protocol (PGWire v3, 기본 포트 `:5433`, WiredTiger 스토리지 엔진) 백엔드로 연동하는 방법을 설명합니다.

---

## 1. 개요 및 호환성 아키텍처

Vikunja는 Go 언어의 ORM인 **XORM** (`xorm.io/xorm`)과 `lib/pq` / `pgx` 드라이버를 통해 데이터베이스 계층과 통신합니다.

- **지원 프로토콜**: PostgreSQL Wire Protocol v3 (`lib/pq`, `pgx`)
- **LionSQL 권장 스토리지 엔진**: WiredTiger (MVCC 기반 행 버전 관리 및 계층 쿼리 트랜잭션 보장)
- **적용 마이그레이션**: 총 80단계 이상의 고유 마이그레이션 (0.6초 이내 완료)
- **주요 데이터 모델**:
  - `users`, `projects`, `tasks`, `project_views` (List, Gantt, Table, Kanban), `project_task_counters`, `teams`, `users_projects`, `project_ancestors` 등
- **핵심 쿼리 패턴**:
  - 사용자 권한 산출을 위한 다중 파생 테이블 유니온(`UNION ALL`) 및 공통 테이블 표현식(`WITH ... AS`)
  - 무한 깊이 프로젝트 계층 탐색을 위한 재귀 CTE(`WITH RECURSIVE project_hierarchy AS ...`)
  - 로그인 및 사용자 검증을 위한 복합 인덱스 컬럼 `OR` 술어(`WHERE username = $1 OR email = $2`)
  - 태스크 순번 관리를 위한 카운터 원자적 갱신(`UPDATE project_task_counters SET ...`)

---

## 2. 사전 준비 사항

- **LionSQL 바이너리 빌드** (WiredTiger 엔진 포함):
  ```bash
  go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
  ```
- **공식 Vikunja Docker 이미지**:
  ```bash
  docker pull vikunja/vikunja:latest
  ```

---

## 3. 실행 및 연동 단계

### 1단계: LionSQL 서버 구동
```bash
./bin/lionsql -dir data_vikunja_pg -engine wiredtiger -pg 0.0.0.0:5433 -http 127.0.0.1:8096 -admin-pass="VikunjaPass2026!"
```

### 2단계: Vikunja 서버 컨테이너 구동
```bash
docker run -d \
  --name vikunja_live \
  --network host \
  -e VIKUNJA_DATABASE_TYPE=postgres \
  -e VIKUNJA_DATABASE_HOST=127.0.0.1 \
  -e VIKUNJA_DATABASE_PORT=5433 \
  -e VIKUNJA_DATABASE_USER=postgres \
  -e VIKUNJA_DATABASE_PASSWORD="VikunjaPass2026!" \
  -e VIKUNJA_DATABASE_DATABASE=vikunja \
  -e VIKUNJA_DATABASE_SSLMODE=disable \
  -e VIKUNJA_SERVICE_JWTSECRET="lion-sql-vikunja-secret-jwt-key-32bytes" \
  -e VIKUNJA_SERVICE_FRONTENDURL="http://localhost:3456" \
  vikunja/vikunja:latest
```

---

## 4. E2E 기능 및 API 검증

### 4.1 서비스 헬스체크 및 정보 확인
```bash
# 서버 및 API 버전 확인 (HTTP 200, v2.7.1)
curl -s http://127.0.0.1:3456/api/v1/info

# 웹 프론트엔드 UI 서빙 확인 (HTTP 200 OK)
curl -I http://127.0.0.1:3456/
```

### 4.2 사용자 등록 (Register)
```bash
curl -X POST http://127.0.0.1:3456/api/v1/register \
  -H "Content-Type: application/json" \
  -d '{"username":"maro","email":"maro@example.com","password":"Password123!"}'
```
- **응답**: `{"id":1,"username":"maro","email":"maro@example.com", ...}` (HTTP 200 OK)

### 4.3 JWT 인증 로그인 (Login)
```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:3456/api/v1/login \
  -H "Content-Type: application/json" \
  -d '{"username":"maro","password":"Password123!"}' | jq -r '.token')
```
- **결과**: `WHERE (username = $1 OR email = $2)` 복합 술어 쿼리를 통해 사용자 인증 완료 및 JWT 발급.

### 4.4 프로젝트 및 기본 뷰 생성
```bash
curl -X PUT http://127.0.0.1:3456/api/v1/projects \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"LionSQL Roadmap","description":"WiredTiger & PGWire Integration"}'
```
- **응답**: HTTP 201 Created (List, Gantt, Table, Kanban 4대 기본 뷰 자동 생성 및 부모-자식 계층 트랜잭션 반영).

### 4.5 칸반 태스크 생성 및 상태 변경
```bash
# 태스크 생성
curl -X PUT http://127.0.0.1:3456/api/v1/projects/2/tasks \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Verify Woodpecker & Vikunja Integration"}'

# 태스크 완료 처리 (done: true)
curl -X POST http://127.0.0.1:3456/api/v1/tasks/1 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"done":true}'
```
- **응답**: HTTP 200 OK (`project_task_counters` 원자적 증가 및 완료 플래그 저장).

---

## 5. 핵심 엔진 개선 사항

1. **재귀 CTE(`WITH RECURSIVE`) 지원**:
   - `ParseCTEs`에서 PostgreSQL 표준의 `RECURSIVE` 키워드를 안전하게 제거하고, 초기 집합(Base Case)과 재귀 집합(Recursive Case)을 `UNION ALL` 루프로 반복 수렴 평가(최대 100계층).
2. **다중 파생 테이블(`Derived Tables`) 스코프 격리**:
   - CTE 블록 내부에서 `UNION ALL`로 중첩된 다중 서브쿼리가 포함될 때, 외부 쿼리의 CTE 정의를 내부 서브쿼리 컨텍스트에 투명하게 전달하고 고유한 임시 테이블(`#__cte_<name>_<seq>`)로 물질화하여 고립성 보장.
3. **복합 인덱스 `OR` 술어 최적화**:
   - `WHERE (username = $1 OR email = $2)`와 같이 `OR` 논리 연산자가 포함된 술어가 단일 컬럼 인덱스 Seek 범위로 잘못 매핑되는 현상을 방어하고, 테이블 스캔 필터 및 파라미터 바인딩으로 안전하게 평가.
4. **VDBE DML 커맨드 태그 정규화**:
   - `UPDATE` 쿼리 실행 후 PGWire 응답 태그가 `"DML 1"` 대신 표준 `"UPDATE 1"`로 반환되도록 보정하여 XORM 및 `pgx`의 영향 받은 행 수(`RowsAffected`) 계산 무결성 보장.
