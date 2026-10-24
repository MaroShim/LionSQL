# LionSQL 스크립트 도구 가이드 (Scripts Directory)

LionSQL의 테스트, 데이터 시딩, 자동화 검증을 위한 보조 스크립트 도구 모음입니다.

---

## 1. 확장 통합 스모크 테스트 (`scripts/verify_report.sh`)

[`notes/TEST_VERIFICATION_REPORT.ko.md`](../notes/TEST_VERIFICATION_REPORT.ko.md)에 기술된 **13대 핵심 아키텍처 및 무결성 도메인**을 단일 스크립트로 신속하게 일괄 검증하는 확장 스모크 테스트 도구입니다.

### 1.1 기본 전제 조건
`bin/` 디렉터리에 핵심 바이너리가 컴파일되어 있어야 합니다:
```bash
go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
go build -tags wiredtiger -buildvcs=false -o bin/tpcc-runtime ./cmd/tpcc_runtime
```

### 1.2 실행 방법
```bash
# 1. 기본 실행 (In-Memory OCC 초고속 검증, 약 3초 소요)
./scripts/verify_report.sh

# 2. WiredTiger 영속 B+Tree 엔진 기반 전체 검증
./scripts/verify_report.sh --engine=wiredtiger

# 3. 상세 디버깅 로깅 모드
./scripts/verify_report.sh -v

# 4. 도움말 확인
./scripts/verify_report.sh --help
```

### 1.3 자동 검증 13대 도메인
| Phase | 검증 도메인 | 주요 점검 내용 |
| :---: | :--- | :--- |
| **Phase 1** | TDS 7.3 핸드셰이크 & 라이프사이클 | DDL/DML, T-SQL 저장 프로시저 컴파일/실행, 원자적 롤백 |
| **Phase 2** | 복합 엔터프라이즈 T-SQL 쿼리 | 3-Way JOIN, 윈도우 함수(`ROW_NUMBER`), CTE, `OFFSET...FETCH` 페이징 |
| **Phase 3** | 고동시성 TPC-C VDBE 스트레스 | 4개 워커 고동시성 New-Order/Payment 트랜잭션 1초 연속 부하 (13,000+ TPS) |
| **Phase 4** | TDS 프로토콜 극한 경계 | 25KB 초과 PLP 대용량 청크 스트리밍 및 SHA-256 무손실 일치 |
| **Phase 5** | 이종 프로토콜 트라이브릿지 | PGWire, TDS, HTTP REST 인터페이스 간의 실시간 스토리지 카탈로그 일치 |
| **Phase 6** | ACID 크래시 복구성 | `kill -9` (SIGKILL) 돌발 사살 후 콜드 재기동 시 50건 100% 무손실 복구 |
| **Phase 7** | Always On HA 복제 및 페일오버 | 복제 카탈로그 일치 및 Replica 노드 읽기 전용 가드레일 |
| **Phase 8** | 디커플드 HTAP 분석 파이프라인 | CDC 트랜잭션 이벤트 스트림 기반 벡터 집계 쿼리 |
| **Phase 9** | CBO 비용 모델 및 커버링 인덱스 | `EXPLAIN` 기반 `Index-Only Scan` 선택 및 테이블 힙 조회 0건 입증 |
| **Phase 10** | 반정형 JSON B-Tree 인덱스 | `((doc->>'city'))` 함수형 인덱스 탐색 및 쿼리 플랜 가속 매칭 |
| **Phase 11** | 세분화된 SQL RBAC 보안 거버넌스 | `SETUSER` Default Deny 원칙 및 비인가 테이블 접근 100% 차단 |
| **Phase 12** | 선언적 스키마 마이그레이션 CLI | `lionsql migrate new` -> `status` (Pending) -> `up` (Applied) -> `down` (Pending) |
| **Phase 13** | 롤링 테이블 파티셔닝 | `toYYYYMM` 자동 파티셔닝, `EXPLAIN` 파티션 프루닝, `DROP PARTITION` O(1) 파기 |

---

## 2. 데모 시드 데이터 (`scripts/demo_seed.sql`)

LionSQL 초기 기동 후 실습용 전자상거래/ERP 예제 데이터베이스(고객, 상품, 주문, 재고, 계좌 등)를 적재하기 위한 SQL 시드 스크립트입니다:

```bash
./bin/lionsql sql "source scripts/demo_seed.sql;"
# 또는 서버 기동 시 -seed 플래그 사용:
./bin/lionsql serve -seed
```
