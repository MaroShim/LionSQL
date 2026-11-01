# Woodpecker CI 연동 가이드 (LionSQL PGWire)

본 문서는 GitHub Actions 및 Drone CI를 대체하는 경량 컨테이너 기반 CI/CD 플랫폼인 **Woodpecker CI**를 LionSQL의 PostgreSQL Wire Protocol (PGWire v3, 기본 포트 `:5433`, WiredTiger 스토리지 엔진) 백엔드로 연동하는 방법을 설명합니다.

---

## 1. 개요 및 호환성 아키텍처

Woodpecker CI는 Go 언어의 ORM인 **XORM** (`github.com/go-xorm/xorm` / `xorm.io/xorm`)과 `lib/pq` / `pgx` 드라이버를 통해 PostgreSQL 데이터베이스와 통신합니다.

- **지원 프로토콜**: PostgreSQL Wire Protocol v3 (`pgx`, `lib/pq`)
- **LionSQL 권장 스토리지 엔진**: WiredTiger (MVCC 기반 ACID 트랜잭션 및 고동시성 B+Tree 지원)
- **적용 마이그레이션**: 총 19개 테이블, 32단계 마이그레이션 (0.5초 이내 완료)
- **주요 테이블**:
  - `repos`, `builds`, `steps`, `logs`, `users`, `secrets`, `registries`, `tasks`, `orgs`, `agents` 등

---

## 2. 사전 준비 사항

- **LionSQL 바이너리 빌드** (WiredTiger 엔진 포함):
  ```bash
  go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
  ```
- **공식 Woodpecker Docker 이미지**:
  ```bash
  docker pull woodpeckerci/woodpecker-server:v3
  ```
  *(참고: 최신 3.x 릴리스 라인은 `woodpeckerci/woodpecker-server:v3` 롤링 태그를 사용합니다)*

---

## 3. 실행 및 연동 단계

### 1단계: LionSQL 서버 구동
```bash
./bin/lionsql -dir data_woodpecker_pg -engine wiredtiger -pg 0.0.0.0:5433 -http 127.0.0.1:8095 -admin-pass="WoodpeckerPass2026!"
```

### 2단계: Woodpecker CI 서버 컨테이너 구동
```bash
docker run -d \
  --name woodpecker_server \
  --network host \
  -e WOODPECKER_DATABASE_DRIVER=postgres \
  -e WOODPECKER_DATABASE_DATASOURCE="postgres://postgres:WoodpeckerPass2026!@127.0.0.1:5433/woodpecker?sslmode=disable" \
  -e WOODPECKER_SERVER_ADDR=":8000" \
  -e WOODPECKER_HOST="http://localhost:8000" \
  -e WOODPECKER_DEV_ALWAYS_AUTHENTICATE=true \
  woodpeckerci/woodpecker-server:v3
```

---

## 4. 연동 검증

### 헬스체크 및 버전 확인
```bash
# 헬스체크 (HTTP 204 No Content 반환)
curl -I http://127.0.0.1:8000/healthz

# 버전 정보 확인 (v3.20.1)
curl http://127.0.0.1:8000/version

# 웹 UI 대시보드 접근 (HTTP 200 OK)
curl -I http://127.0.0.1:8000/
```

---

## 5. 아키텍처 특이사항 및 최적화 내역

1. **XORM 파라미터화 마이그레이션 (`pg_tables`, `pg_attribute`)**:
   - `SELECT tablename FROM pg_tables WHERE schemaname = $1` 및 `SELECT attname FROM pg_attribute ...` 시스템 카탈로그 뷰 쿼리가 가상 카탈로그 라우터를 통해 zero-overhead로 실행됩니다.
2. **트랜잭션 세션 격리**:
   - 마이그레이션 실행 도중 DDL과 인덱스 생성(`CREATE INDEX IF NOT EXISTS`)이 연속 실행되어도 트랜잭션 일관성이 완벽하게 보장됩니다.
