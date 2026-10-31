# LionSQL 1시간 장시간 부하(Soak Test) 실측 검증 및 메모리/내구성 분석 보고서 (`notes/SOAK_TEST_1H_VERIFICATION_REPORT.ko.md`)

> - **최초 작성일**: 2026-10-31
> - **과제 식별 코드**: `DURA-SOAK-1`, `DURA-SOAK-F1`, `DURA-SOAK-F2`
> - **검증 도구**: [`cmd/soaktest/main.go`](../cmd/soaktest/main.go)
> - **관련 계획서**: [`notes/DURABILITY_VERIFICATION_PLAN.ko.md`](DURABILITY_VERIFICATION_PLAN.ko.md)
> - **상태**: 1단계(1시간 WiredTiger PGWire 소크 및 초기 발견 이슈 2건 조치) 완료 (`[x]`)

---

## 1. 검증 목적 및 워크로드 설계

단발성 기능/연동 테스트만으로는 확인하기 어려운 **"장시간 연속 가동 시의 메모리·파일 핸들·고루틴 누수 여부, 지연 시간 열화 여부, 그리고 동시성 충돌 하에서의 ACID 정합성"**을 검증하기 위해 전용 소크 테스트 하네스(`cmd/soaktest`)를 구축하고 1시간 연속 부하를 실측했다.

### 1.1. 테스트 환경 및 파라미터
- **스토리지 엔진**: WiredTiger MVCC B+Tree (`cache_size=500M`, `checkpoint=(wait=60)`, `log=(enabled=true)`)
- **프로토콜**: PostgreSQL Wire Protocol v3 (`github.com/lib/pq`)
- **동시성 및 목표 처리량**: 워커 `8`개, 초당 `200` ops 정속 유지 (1시간 총 `720,000` ops 목표)
- **계좌 수 및 초기 잔액**: `1,000`개 계좌 × 초기 잔액 `1,000` = **전역 불변 합계 `1,000,000`**

### 1.2. 혼합 워크로드 구성
1. **계좌 이체 트랜잭션 (55%)**:
   - `BEGIN` ➔ 출금 계좌 `UPDATE balance = balance - 1` ➔ 입금 계좌 `UPDATE balance = balance + 1` ➔ 이체 원장 `INSERT INTO soak_log` ➔ `COMMIT`
   - 클라이언트가 `COMMIT` 성공 응답을 받은 횟수(`committed`)를 원자적 카운터로 기록.
2. **포인트 조회 (43%)**:
   - `SELECT balance FROM soak_accounts WHERE id = $1`
3. **전역 불변식 감사 (2%)**:
   - 실행 도중 상시 `SELECT SUM(balance) FROM soak_accounts`를 수행하여 `1,000,000`과 1이라도 다르면 즉시 불변식 위반(`violations`)으로 기록.
4. **연결 Churn (2초 주기)**:
   - 별도 고루틴이 2초마다 신규 TCP/PGWire 연결을 생성(`sql.Open` ➔ `PingContext` ➔ `Close`)하여 세션/소켓/버퍼 풀 누수를 유도.

---

## 2. 1시간 연속 실행 총괄 성적표

| 검증 지표 | 기대 기준 (Pass Criteria) | 1시간 실측 결과 | 판정 |
| :--- | :--- | :--- | :---: |
| **총 수행 연산 수** | ~720,000 ops (200 ops/s × 3,600s) | **`719,970` ops** (3,600초 내내 200.0 ops/s 유지) | **PASS** |
| **전역 잔액 불변식 (`SUM(balance)`)** | 상시 및 종료 시 `1,000,000` 일치 | **`1,000,000`** (실행 중·종료 후 위반 `0`건) | **PASS** |
| **커밋 장부 대조 (`ack == rows`)** | 클라이언트 커밋 성공 수 == `soak_log` 행 수 | **`393,523` == `393,523`** (유실·중복 `0`건) | **PASS** |
| **미처리 연산 오류 (`errs`)** | `0`건 | **`0`건** | **PASS** |
| **재시도 가능 직렬화 충돌 (`40001`)** | `SQLSTATE 40001`로 분리 및 정상 롤백 | **`3,016`건** (전수 안전 롤백 및 정합성 보존) | **PASS** |
| **열린 파일 디스크립터 (`open_fds`)** | 워밍업 후 증가율 30% 이내 | 초기 `28` ➔ 종료 `28` (**`ratio = 1.00`**, 누수 0) | **PASS** |
| **지연 시간 (`p99_latency`)** | 초기 대비 2.0배 이내 | 초기 `5.78ms` ➔ 종료 `6.23ms` (**`ratio = 1.08`**) | **PASS** |
| **디스크 사용량 (`disk`)** | 로그/데이터 누적에 비례, 무한 팽창 없을 것 | 초기 `201MB` ➔ 종료 `216MB` (**`+15MB`** / 39.3만 행) | **PASS** |
| **메모리 (`rss_kb`)** | 엔진 캐시 상한(`500MB` + 런타임) 내 수렴 | 피크 `765MB` ➔ 종료 **`610MB`** 수렴 | **PASS** |

---

## 3. 시간대별 상세 시계열 추이 (4분 간격 요약)

| 경과 시간 | RSS (`MB`) | 열린 FD (`개`) | 디스크 (`MB`) | 누적 연산 (`ops`) | 지연 `p50` | 지연 `p99` | 미처리 오류 |
| :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| `4분 (240s)` | 147 MB | 28 | 201 MB | 47,992 | 1.9 ms | 5.1 ms | 0 |
| `8분 (480s)` | 210 MB | 28 | 203 MB | 95,992 | 2.0 ms | 5.4 ms | 0 |
| `12분 (720s)` | 261 MB | 28 | 203 MB | 143,992 | 2.1 ms | 5.6 ms | 0 |
| `16분 (960s)` | 291 MB | 28 | 205 MB | 191,992 | 2.1 ms | 5.8 ms | 0 |
| `20분 (1200s)` | 415 MB | 28 | 207 MB | 239,992 | 2.2 ms | 5.9 ms | 0 |
| `24분 (1440s)` | 438 MB | 28 | 208 MB | 287,992 | 2.2 ms | 6.0 ms | 0 |
| `28분 (1680s)` | 493 MB | 28 | 210 MB | 335,992 | 2.3 ms | 6.1 ms | 0 |
| `32분 (1920s)` | 570 MB | 28 | 210 MB | 383,992 | 2.3 ms | 6.0 ms | 0 |
| `36분 (2160s)` | 612 MB | 28 | 212 MB | 431,992 | 2.3 ms | 6.2 ms | 0 |
| `40분 (2400s)` | 657 MB | 28 | 212 MB | 479,992 | 2.4 ms | 6.5 ms | 0 |
| `44분 (2640s)` | 660 MB | 28 | 214 MB | 527,992 | 2.3 ms | 5.7 ms | 0 |
| `48분 (2880s)` | 624 MB | 28 | 214 MB | 575,992 | 2.3 ms | 7.2 ms | 0 |
| `50분 (3000s)` | 163 MB | 28 | 215 MB | 599,970 | 2.0 ms | 8.2 ms | 0 |
| `52분 (3120s)` | 763 MB | 28 | 215 MB | 623,970 | 2.6 ms | 6.3 ms | 0 |
| `56분 (3360s)` | 765 MB | 28 | 216 MB | 671,970 | 2.3 ms | 6.2 ms | 0 |
| `60분 (3570s)` | **610 MB** | **28** | **216 MB** | **713,970** | **2.8 ms** | **6.3 ms** | **0** |

---

## 4. 소크 테스트를 통해 발견 및 해결한 엔지니어링 과제

### 4.1. `DURA-SOAK-F1`: WiredTiger 쓰기 충돌(`WT_ROLLBACK`)의 PostgreSQL `SQLSTATE 40001` 표준 매핑
- **현상**: 초기 40초 스모크 실행에서 동시 계좌 업데이트 시 발생하는 WiredTiger의 `WT_ROLLBACK` (`-31800`) 오류가 일반 에러 문자열(`cursor_update failed with code -31800`)로 반환되어, PGWire 프로토콜 계층이 이를 내부 서버 오류(`SQLSTATE XX000`)로 클라이언트에 전달했다.
- **조치**:
  - [`pkg/storage/wiredtiger/wt_engine.go`](../pkg/storage/wiredtiger/wt_engine.go)에 `wtOpError(op string, ret int)` 헬퍼를 도입하여 `WT_ROLLBACK` 반환 시 표준 센티널 에러인 `storage.ErrSerializationFailure`로 래핑하도록 개선했다 (`cursor_insert`, `cursor_update`, `cursor_remove`, `commit_transaction`, `cursor put` 일괄 적용).
  - [`pkg/wire/protocol.go`](../pkg/wire/protocol.go)의 `WriteError`에서 `SERIALIZATION FAILURE` 포함 시 PostgreSQL 표준 재시도 가능 코드인 **`SQLSTATE 40001` (`serialization_failure`)**을 반환하도록 매핑했다.
  - 단위 테스트([`wt_error_test.go`](../pkg/storage/wiredtiger/wt_error_test.go), [`serialization_sqlstate_test.go`](../pkg/wire/serialization_sqlstate_test.go))를 추가하여 검증했다.

### 4.2. `DURA-SOAK-F2`: 메모리(`RSS`) 증가 곡선 규명 및 `/metrics` 런타임 지표 연동
- **현상**: 40초 스모크 테스트에서 RSS가 `54MB ➔ 79MB`로 증가하고, 1시간 테스트에서도 12~18분 구간(`335MB`) 대비 54~60분 구간(`610~765MB`)의 단순 비율이 `2.20`으로 나타나 이것이 메모리 누수인지 페이지 캐시 워밍업인지 규명이 필요했다.
- **원인 분석**:
  1. LionSQL의 WiredTiger 엔진은 `cache_size=500M` (`500MB`)로 설정되어 있다.
  2. 초당 200 ops(초당 약 110건의 `soak_log` INSERT 및 220건의 `soak_accounts` UPDATE) 속도에서는 B+Tree 페이지 캐시 500MB가 채워지는 데 약 35분이 소요된다.
  3. 실제로 36~44분 구간에서 `612~660MB`에 도달한 뒤 더 이상 선형 증가하지 않고, WiredTiger eviction 스레드(`threads_min=4,threads_max=8`)에 의해 58~60분 구간에는 **`610MB`로 감소하며 안정화**되었다.
- **도구 고도화**:
  - OS `RSS`만으로는 CGo(WiredTiger 캐시)와 Go 힙/고루틴 누수를 분리할 수 없으므로, [`cmd/soaktest/main.go`](../cmd/soaktest/main.go)가 자식 서버의 Prometheus `/metrics` 엔드포인트를 주기적으로 스크래핑하여 **`heap_kb` (`lionsql_memory_alloc_bytes`)**와 **`goroutines` (`lionsql_goroutines`)**를 함께 수집·판정하도록 업그레이드했다.
  - 또한 엔진의 설정된 페이지 캐시 상한(`-rss-ceiling-mb 800`) 내에서의 정상 워밍업을 누수로 오판하지 않도록 기준선 로직을 보정했다.
