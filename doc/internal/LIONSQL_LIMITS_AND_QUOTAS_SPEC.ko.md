# LionSQL 공식 제한 스펙(Limits & Quotas) 아키텍처 명세서 (`doc/internal/LIONSQL_LIMITS_AND_QUOTAS_SPEC.ko.md`)

- **작성일자**: 2026-11-19
- **대상 버전**: LionSQL v0.93.0+ (WiredTiger / MemEngine)
- **문서 목적**: 상용 DBMS(Microsoft SQL Server, PostgreSQL) 수준의 명확한 용량, 크기, 시간, 동시성 한도를 정의하고 실제 런타임 강제 기준을 공식화하여 시스템 무중단 고동시성 운영 및 데이터 손상 방지를 보장함.
- **적용 규칙**: `[RULE:BoundedBuffers]`, `[RULE:ExplicitError]`, `[RULE:HermeticParity]`, `[Invariant 10]`, `[Invariant 13]`

---

## 1. 긴급 핵심 제한 스펙 Top 10 현황 및 정렬 목표

| 순위 | 항목 | 취약 상태 및 실사 결과 | 정렬 목표 스펙 | 시급한 이유 및 영향 |
| :---: | :--- | :--- | :--- | :--- |
| **1** | **`max_connections` 실제 강제** | `SHOW`만 `"100"` 하드코딩 반환, 와이어 계층 연결 수 무제한 수용 | 와이어 리스너 세마포어로 상한(`200`) 강제, 초과 시 `53300` 반환 | 무제한 TCP 연결 수용 시 파일 디스크립터 고갈 및 고루틴 폭증으로 프로세스 크래시 방지 |
| **2** | **`idle_in_transaction_session_timeout` 세션 격리** | `Coordinator` 전역 단일 변수(5m)로 동작해 한 세션이 `SET`하면 전체 세션 오염 | `ClientSession`별 독립 필드로 격리, `cs.Tx() != nil` 상태에만 적용 | 타 정상 세션의 트랜잭션이 예기치 않게 강제 롤백되는 데이터 정합성 파괴 방지 |
| **3** | **활성 트랜잭션 수명 한도 (`transaction_timeout`)** | **미정의 (무제한)** | 세션/서버 기본값(예: `30m`) 초과 시 트랜잭션 강제 취소/롤백 | 장기 미종료 트랜잭션이 WiredTiger 스냅샷/캐시를 점유해 체크포인트 및 업데이트 정체 방지 |
| **4** | **식별자 최대 길이 검증** | **미정의 (무제한)** | 테이블/컬럼/인덱스명 최대 `128` 문자 (UTF-8). 복합 네임스페이스 고려 각 계층 64자 이내 권장 | WiredTiger 물리 파일명(`table:<name>`)이 OS 경로/파일명 한도(255B)를 넘어 I/O 실패 방지 |
| **5** | **WiredTiger 캐시 크기 설정화** | 소스코드에 `cache_size=500M` 하드코딩 (`wt_engine.go:558`) | `config.json`의 `wt_cache_size` 연동 (단, Invariant 10 `transaction_sync` 금지 절대 유지) | 500MB 캐시 초과 시 캐시 스래싱 발생 및 소형 컨테이너 OOM 방지 |
| **6** | **와이어 메시지(64MB) vs 로우 크기(16MB) 정합** | 메시지 수신은 64MB 허용, 스토리지 인코더는 16MB 하드 캡 | 단일 튜플 상한(16MB) 검증 유지, 배치 다중 행 INSERT는 64MB 허용 | 16~64MB 대형 쿼리가 메모리 소모 후 스토리지 커밋 직전 실패하는 자원 낭비 방지 |
| **7** | **인라인 로우 크기(`8060B`) 실제 강제** | `FixedRowSize`가 고정 타입만 합산하고 `CHAR(n)`/가변 오프셋 누락 | 고정 컬럼 + `CHAR(n)` + 가변 컬럼 오프셋 메타데이터(24B)를 합산 검증 | 페이지 크기 불일치로 인한 스토리지 손상 및 SQL Server 호환 DDL 실패 방지 |
| **8** | **인덱스 키 크기 / 컬럼 수 상한** | **미정의 (무제한)** | 인덱스 키 최대 `1,700 B`, 인덱스당 컬럼 최대 `32`개 | 비정상적인 거대 문자열 인덱싱으로 인한 WiredTiger B-Tree 팬아웃 저하 및 메모리 폭발 차단 |
| **9** | **Prepared Statement 세션 상한 일치** | PG는 256개 LRU 축출 방식, TDS는 256개 핸들 고갈 방식 | 양 프로토콜 공통 기본 `256`개 상한 동기화 및 명시적 설정화 (`53200`) | 커넥션 풀 환경에서 무한정 누적되는 메모리 누수 방지 |
| **10** | **`LOCK_TIMEOUT` 실제 대기 연동** | `-1`로 하드코딩 반환되며 실제 락 대기 제어 불가 | 밀리초 단위 세션별 타이머 연동 (`-1`=무제한, `0`=즉시 실패, `>0`=타임아웃 에러) | 동시 트랜잭션 경쟁 시 무한 블로킹 또는 즉시 실패로 인한 재시도 폭풍 제어 |

---

## 2. 설계 원칙 (Design Principles)

1. **단일 진실 공급원 (Single Source of Truth)**: `SHOW`, `sys.*`, `information_schema.*`, `DBCC USEROPTIONS`가 노출하는 값은 실제 런타임 강제값과 반드시 일치해야 한다 (`[RULE:ExplicitError]`).
2. **계층 간 정합성**: 와이어(Wire) -> 파서/플래너 -> 실행기(Execution) -> 스토리지(Storage) 계층의 상한이 서로 모순되지 않도록 정렬한다.
3. **세션 vs 전역 스코프 엄격 분리**: 세션 속성은 전역 상태를 오염시키지 않고 `ClientSession` 단위로 격리하되, 서버 전역 기본값과 상한은 `config.json`에서 제어한다.
4. **명확한 오류 계약 (Error Contract)**: 모든 한도 초과는 사일런트 드롭이나 패닉 대신 표준 SQLSTATE 및 SQL Server 오류 번호를 반환한다.

---

## 3. 스토리지 및 레코드 크기 한도 (선택지 B)

| 항목 | 공식 규격 | 강제 계층 | 초과 시 오류 (SQLSTATE / Msg) |
| :--- | :--- | :--- | :--- |
| **테이블당 최대 컬럼 수** (`MaxColumnsPerTable`) | `1,024`개 (`storage.MaxColumnsPerTable`) | DDL (`CREATE`/`ALTER`), Tuple 인코더 | `54011` / Msg 1702 (`ErrTooManyColumns`) |
| **인라인 로우 최대 크기** (`MaxInlineRowSize`) | `8,060 B` (고정 컬럼 + `CHAR(n)` + 가변 컬럼 오프셋 메타데이터 24B 합산) | DDL + DML (`INSERT`/`UPDATE`) | `54000` / Msg 511 (`ErrInlineRowTooLarge`) |
| **단일 로우 총 최대 크기** (`MaxRowSize`) | `16 MB` (`storage.MaxRowSize = 16 * 1024 * 1024`) | `EncodeRowToBuffer` + `memengine` 쓰기 경로 공통 | `54000` / Msg 511 (`ErrRowTooLarge`) |
| **식별자 최대 길이** | UTF-8 `128` 문자 (각 파트별 64자 권장) | DDL 파서 및 카탈로그 등록 시 | `42622` / Msg 103 (`ErrIdentifierTooLong`) |
| **인덱스 키 최대 바이트** | `1,700 B` (`storage.MaxIndexKeyBytes`) | `CREATE INDEX`, DDL 검증 | `54000` / Msg 1946 (`ErrIndexKeyTooLarge`) |
| **인덱스당 최대 컬럼 수** | `32`개 (`storage.MaxIndexColumns`) | `CREATE INDEX`, `CREATE TABLE` 제약 | `54011` / Msg 1904 (`ErrTooManyIndexColumns`) |

---

## 4. 연결 수, 프로토콜 및 세션 타임아웃 한도 (선택지 A)

| 항목 | 공식 규격 | 강제 계층 | 초과 시 동작 및 오류 |
| :--- | :--- | :--- | :--- |
| **최대 동시 클라이언트 연결** (`max_connections`) | 기본 `200` (최대 `10,000`, `config.json` 연동) | PG/TDS 리스너 `Accept()` 세마포어 | 즉시 연결 거부 및 `53300` (`too many connections`) 반환 |
| **유휴 트랜잭션 타임아웃** (`idle_in_transaction_session_timeout`) | 기본 `5m` (`300000ms`), `0`=비활성 (`ClientSession` 독립) | 세션 풀 스위퍼 (`cs.Tx() != nil` 시) | 트랜잭션 강제 롤백 및 `25P03` 통지 |
| **활성 트랜잭션 타임아웃** (`transaction_timeout`) | 기본 `0` (운영 권장 `30m`) | 세션 풀 스위퍼 | 트랜잭션 강제 롤백 및 `25P04` 통지 |
| **WiredTiger 캐시 크기** (`wt_cache_size`) | 기본 `500M` (`config.json` 연동) | `wt_engine.go` 오픈 설정 | Invariant 10 (`transaction_sync` 금지) 100% 준수 |
| **세션당 Prepared Statement 수** | 기본 `256`개 (최대 `4,096`) | PGWire 및 TDS 세션 핸들러 | 상한 초과 방지 및 메모리 누수 차단 |

---

## 5. 단계별 구현 및 롤아웃 계획

- **Stage 1**: 스펙 문서화 및 스프린트 백로그 등록 (`TASKS.md`, `./bin/sprint`)
- **Stage 2 (선택지 A)**: 네트워크 연결 및 세션 타임아웃 보호
  - `max_connections` 세마포어 및 `SHOW max_connections` 동적 연동
  - `ClientSession.idleInTxTimeout` 세션 격리
  - WiredTiger `cache_size` / `session_max` 설정화 및 Invariant 10 검증
- **Stage 3 (선택지 B)**: DDL 파서 및 스토리지 물리 한도 가드
  - 식별자 128자 제한
  - `FixedRowSize()` `CHAR(n)` 및 가변 오프셋 메타데이터 보완
  - 인덱스 키 1,700B 및 컬럼 32개 상한 가드
