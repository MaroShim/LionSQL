# IOTA-SDK Go ERP 라이브 통합 및 PostgreSQL 와이어 호환성 검증 플랜 (`IOTA-ERP-1`)

> - **최초 작성일**: 2026-10-30
> - **목표**: 우즈베키스탄 엔터프라이즈 모듈형 Go ERP 시스템인 [IOTA SDK](https://github.com/iota-uz/iota-sdk)를 LionSQL PostgreSQL 와이어 프로토콜(TCP `:5433`)에 연결하여 빌드, 스키마 마이그레이션(47개 SQL), 비즈니스 모듈 구동 및 통합 테스트 검증
> - **상태**: 완료 (`Completed`) - 47개 마이그레이션 100% 통과, ERP 백엔드 및 웹 UI 정상 기동, 비동기 워커 무에러 구동 확인

---

## 1. 개요 및 배경

`iota-sdk`는 Go 언어로 구현된 모듈형 ERP(Enterprise Resource Planning) 프레임워크로, 재무(Finance), 인사/인증(IAM), 제조(Manufacturing), 창고/재고(Warehouse/Inventory) 등의 전사적 자원 관리를 지원합니다.
- **백엔드 기술 스택**: Go, `jackc/pgx/v5` (`pgxpool`), `jmoiron/sqlx`, `rubenv/sql-migrate` (gorp), `go-chi/chi/v5`
- **데이터베이스 의존성**: PostgreSQL 14+ (현재 LionSQL PGWire v3 TCP `:5433` 목표)
- **마이그레이션 구조**: `migrations/` 내 47개의 순차 SQL 마이그레이션 스크립트

---

## 2. 통합 아키텍처 및 검증 결과

```
+─────────────────────────────────────────────────────────────────────────────────+
|                  IOTA SDK ERP Application (github.com/iota-uz/iota-sdk)         |
|  - CLI & Migrations: bin/iota migrate up (sql-migrate / gorp) -> 47개 100% 통과  |
|  - Web ERP Server:   bin/server (Go HTTP, Templ, HTMX, SolidJS) -> HTTP 200 OK  |
|  - Driver Layer:     jackc/pgx/v5/pgxpool over TCP :5433 (SSL disabled)         |
+─────────────────────────────────────────────────────────────────────────────────+
                                       │
                                       ▼ PostgreSQL Wire Protocol v3 (TCP :5433)
+─────────────────────────────────────────────────────────────────────────────────+
|                        LionSQL Database Engine (cmd/lionsql)                    |
|  - PGWire Listener:  pkg/wire/server.go (Extended Query Protocol & Binary Formats)  |
|  - Coordinator:      pkg/engine/coordinator.go                                  |
|  - SQL Execution:    pkg/engine/sqlexec.go (DDL, DML, Indexes, Sequences)       |
|  - Storage Engine:   In-Memory OCC (memengine) / WiredTiger Disk MVCC           |
+─────────────────────────────────────────────────────────────────────────────────+
```

---

## 3. 세부 단계별 구현 및 해결 내역

### 1단계: 마이그레이션 러너 호환성 해결 (`gorp_migrations` 타임스탬프)
- `pkg/storage/types_extended.go`의 `DateTimeValueFromAny`에 Go `time.Time.String()` 포맷, 마이크로초 + UTC 포맷 등 추가.

### 2단계: 47개 마이그레이션 전체 적용 (`bin/iota migrate up`)
- `ALTER TABLE` 구문 완전성 확보: `DROP DEFAULT`, `SET DEFAULT`, `TYPE ... USING`, `RENAME COLUMN`, `ADD PRIMARY KEY`, 복수 컬럼 `DROP COLUMN`, `ADD COLUMN IF NOT EXISTS`.
- `CREATE INDEX` 구문 확장: `USING GIN/BTREE/HASH`, `IF NOT EXISTS`, 복합 및 함수형 인덱스 지원.
- `UPDATE` / `DELETE` 구문의 `[AS] <alias>` 별칭 파싱 지원.
- `GRANT` / `REVOKE` 및 `ALTER DEFAULT PRIVILEGES` 내성 처리.
- 결과: `Applied 47 migrations` (100% 성공).

### 3단계: ERP 메인 서버 구동 (`bin/server`) 및 엔드포인트 검증
- PGWire 바이너리 프로토콜 고도화: `RowDescription` 바이너리 포맷 코드 전달, UUID 16바이트 원시 바이너리 인코딩, `TIMESTAMPTZ` 마이크로초 인코딩.
- `TIMESTAMPTZ` 및 PostgreSQL 표준 타입(`INT2/4/8`, `FLOAT4/8`, `TIMETZ` 등) 파서 등록.
- 결과:
  - `notification_dispatch_worker` 무에러 정상 동작 (테넌트 스캔 100% 통과).
  - `http://localhost:3200` 로그인 화면(HTML) 정상 렌더링 확인 (`HTTP/1.1 200 OK`).

### 4단계: 회귀 테스트 및 3단계 파이프라인 완수
- LionSQL 전 패키지 레이스 프리 검증 (`go test -tags wiredtiger -count=1 -race $(go list ./... | grep -v /scratch)`) 100% 통과.
- `TODO.md` 및 계획 문서 업데이트 완료.
