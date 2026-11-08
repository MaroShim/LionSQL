# LionSQL 확장 스모크 & 아키텍처 검증 스크립트 구축 계획 (`EXTENDED_SMOKE_TEST_PLAN.md`)

> **상태 (Status)**: 완료됨 (Completed) `[x]`  
> **적용 일자**: 2026-10-24  
> **구현 산출물**: `scripts/verify_report.sh`, `scripts/README.md` (`SMOKE-EXT-1`)

- **문서 목적**: `notes/TEST_VERIFICATION_REPORT.ko.md`에 명시된 13대 핵심 검증 도메인의 전 항목을 단일 스크립트로 신속하고 결정론적으로 자동 검사하는 확장 스모크 테스트 하네스(`scripts/verify_report.sh`)의 설계 및 실행 계획 수립.
- **기본 가정**: `bin/` 디렉터리에 핵심 바이너리(`bin/lionsql`, `bin/server`, `bin/bench`, `bin/tpcc-runtime`)가 이미 컴파일되어 있음.
- **성격**: 단위 테스트(`go test`)와 무거운 카오스 벤치마크 사이의 가교 역할을 하는 **확장형 통합 스모크 테스트(Extended Integration Smoke Test)**.

---

## 1. 핵심 설계 원칙 및 요구사항

### 1.1 하드웨어 & 의존성 독립성 (Hermetic & Zero-Dependency First)
- Docker 데몬 실행 여부와 무관하게 동작:
  - Docker가 실행 중인 경우: 공식 `sqlcmd` / `psql` 컨테이너를 활용한 와이어 프로토콜 실제 통신 검증 수행.
  - Docker가 비활성화된 경우: `bin/lionsql sql`, HTTP REST 엔드포인트(`curl`), TCP 소켓 프로브(`nc`), 네이티브 Go/Python 클라이언트로 매끄럽게 자동 폴백(Graceful Fallback).
- 외부 도구 미설치로 인한 스크립트 중단 방지.

### 1.2 간결하고 명확한 출력 (Succinct Check Output)
- 수백 줄의 패킷 덤프나 로그를 화면에 쏟아내지 않고, 13개 도메인별 핵심 assertion 결과만 간결하게 표시:
  - `[PASS]` / `[SKIP]` / `[FAIL]` 형태의 정돈된 ANSI 컬러 출력.
  - 세부 실행 로그는 임시 로그 파일(`/tmp/lionsql_verify_*.log`)에 캡처하여 디버깅 시에만 참조.

### 1.3 클린업 보장 & 자원 격리 (Trap & Isolated Teardown)
- 테스트용 전용 임시 디렉터리 사용 (`/tmp/lionsql_verify_$$`).
- `trap 'cleanup' EXIT INT TERM`을 통해 스크립트 정상 종료 또는 비정상 중단(`Ctrl+C`) 시 백그라운드로 실행된 서버 프로세스(`kill -9`)와 임시 데이터를 100% 자동 정리.
- 기존 개발 환경 포트(`8090`, `5433`, `1433`)와의 충돌을 피하기 위해 격리된 테스트 전용 포트 사용 (예: HTTP `18090`, PG `15433`, TDS `11433`).

---

## 2. 13대 도메인별 검증 매핑 명세

| Phase | 검증 도메인 | 검증 방식 및 Assertions |
| :--- | :--- | :--- |
| **Phase 1** | **기본 TDS 7.3 핸드셰이크 & T-SQL 라이프사이클** | `bin/server` 기동 후 `SELECT @@VERSION`, `DB_NAME()`, 테이블 CRUD, 저장 프로시저 컴파일/실행, 원자적 롤백 확인 |
| **Phase 2** | **복합 엔터프라이즈 T-SQL 쿼리 & MSSQL 호환성** | 3-Way JOIN + HAVING, ROW_NUMBER() 윈도우 함수, CTE(`WITH`), CROSS APPLY, JSON_VALUE, OFFSET...FETCH, SAVE TRANSACTION 세이브포인트 부분 롤백 |
| **Phase 3** | **고동시성 TPC-C VDBE 극한 스트레스** | `bin/tpcc-runtime` 또는 `bin/bench -tpcc` 1~2초 고속 실행, commit rate 100% 및 abort 0건 확인 |
| **Phase 4** | **TDS 프로토콜 극한 경계 테스트** | 25KB 초과 PLP 청크 스트리밍(해시 일치), NBCROW NULL 비트맵 압축 행, 다중 동시 트랜잭션 정상 커밋 |
| **Phase 5** | **이종 프로토콜 트라이브릿지** | 단일 서버 인스턴스에서 PGWire/TDS/REST 동시 데이터 쓰기 및 즉시 공유 무결성 검증 |
| **Phase 6** | **WiredTiger ACID 크래시 복구 & 콜드 리스타트** | 디스크 B+Tree에 데이터 삽입 후 `kill -9` 강제 사살 $\rightarrow$ 콜드 재기동 후 WAL 롤포워드 무손실(100% Intact) 확인 |
| **Phase 7** | **Always On HA 복제 및 무중단 페일오버** | Primary(`18090`) $\rightarrow$ Replica(`18091`) 비동기 스트리밍, Replica Read-Only 차단 확인, `/api/replication/promote` 후 쓰기 가능 검증 |
| **Phase 8** | **디커플드 HTAP 분석 파이프라인 (CDC)** | 커밋 발생 시 CDC 이벤트 스트림 엔드포인트(`/api/cdc/stream`)로 실시간 이벤트 발행 확인 |
| **Phase 9** | **CBO 비용 모델링, 쿼리 힌트 & 커버링 인덱스** | `EXPLAIN`으로 `OPTION (HASH JOIN)`, `OPTION (MERGE JOIN)` 힌트 반영 및 `INCLUDE` 커버링 인덱스의 `Index-Only Scan` 확인 |
| **Phase 10**| **반정형 JSON 함수형 B-Tree 인덱스** | `((doc->>'city'))` B+Tree 인덱스 생성 후 `EXPLAIN`에서 `Index Scan using idx_user_city` 자동 선택 확인 |
| **Phase 11**| **세분화된 SQL RBAC 보안 권한 통제** | Default Deny(`SETUSER`), GRANT SELECT 성공, UPDATE 권한 차단, GRANT ALL + DENY DELETE 시 DELETE 우선 차단 확인 |
| **Phase 12**| **선언적 스키마 마이그레이션 CLI** | `bin/lionsql migrate new` $\rightarrow$ `status` (Pending) $\rightarrow$ `up` (Applied) $\rightarrow$ `down` (Pending 롤백) 전체 사이클 확인 |
| **Phase 13**| **롤링 테이블 파티셔닝 & O(1) 파티션 파기** | `toYYYYMM` 파티셔닝, 동적 파티션 생성, `EXPLAIN` 파티션 프루닝(`Partitions: [202410]`), `DROP PARTITION` O(1) 파기 확인 |

---

## 3. 스크립트 아키텍처 및 구현 파일

- **스크립트 파일명**: `scripts/verify_report.sh` (실행 권한 `chmod +x` 부여)
- **실행 방법**:
  ```bash
  ./scripts/verify_report.sh
  # 또는 특정 엔진 지정 (기본값: wiredtiger)
  ./scripts/verify_report.sh --engine=memory
  ```
- **주요 헬퍼 함수 구조**:
  - `log_info()`, `log_pass()`, `log_fail()`, `log_skip()`
  - `start_server()`, `stop_server()`
  - `run_sql()` (`bin/lionsql sql` 래퍼)
  - `run_http_query()` (`curl` JSON API 래퍼)
  - `check_docker_available()` (Docker 유무 및 데몬 반응 자동 판별)

---

## 4. 검증 및 배포 절차

1. **스크립트 생성 및 구현 (`scripts/verify_report.sh`)**:
   - 13개 Phase별 assertion 로직 작성 및 에러 핸들링 구성.
2. **실제 실행 검증**:
   - macOS 로컬 샌드박스 환경에서 `./scripts/verify_report.sh` 직접 실행하여 13개 도메인 전원 통과 확인.
3. **문서 동기화 & 커밋**:
   - `TODO.ko.md` 및 `CHANGELOG.md` 업데이트.
   - 3-Stage Pipeline (Build ➔ Test with Race ➔ Conventional Commit) 수행.
