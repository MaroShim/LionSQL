# LionSQL Change Data Capture (CDC) 및 DuckDB 연동 가이드

> - **최초 작성일**: 2026-10-14
> - **최종 갱신일**: 2026-11-21
> - [English](CDC_DUCKDB_INTEGRATION.md)

LionSQL은 ACID 트랜잭션 내구성과 16,000+ TPS 이상의 고동시성 처리량을 보장하는 엔터프라이즈급 임베디드 OLTP 데이터베이스 엔진입니다.

본 문서는 LionSQL의 코어 OLTP 트랜잭션 성능과 메모리 안전성을 전혀 저해하지 않으면서, 외부 분석 애플리케이션(Python, BI 대시보드, 백엔드 마이크로서비스)에서 임베디드 DuckDB를 활용해 서브-밀리초 벡터화 OLAP 집계를 수행할 수 있도록 하는 **비결합 Change Data Capture (CDC) 연동 아키텍처**를 규정합니다.

---

## 1. 아키텍처 개요 (비결합 분석 아키텍처)

```
[ OLTP 도메인: LionSQL 서버 ]
   ├── Port :5433 (PostgreSQL Wire Protocol v3)
   ├── Port :1433 (Microsoft SQL Server TDS 7.x/8.0)
   └── Port :8090 (HTTP API & 실시간 CDC 스트림)
          │
          │ (모든 프로토콜을 통해 커밋된 트랜잭션 변경분 캡처)
          ▼
   [ WAL / CDC 스트리머 ] ──(초저지연 비동기 HTTP 스트림)──────┐
                                                               │
                                                               ▼
                           [ 분석 도메인: 외부 애플리케이션 ]
                              ├── Python (FastAPI / Streamlit / Pandas)
                              └── Go / Node.js 분석 마이크로서비스
                                     │ (HTTP 스트림 구독: NDJSON / SSE)
                                     ▼
                               [ DuckDB (임베디드 엔진) ]
                                     │ (인메모리 또는 단일 파일 컬럼 지향 저장소)
                                     ▼
                               [ 서브-밀리초 벡터화 집계 / 실시간 OLAP 대시보드 ]
```

### 비결합(Decoupled) 아키텍처의 설계 이점

1. **장애 격리 및 자원 보호 (Zero Blast Radius)**:
   - 수백만 건의 조인이나 풀 테이블 스캔으로 인한 DuckDB 분석 쿼리의 메모리 초과(OOM)가 발생하더라도, LionSQL 코어 OLTP 엔진 프로세스에는 일체 영향을 주지 않습니다.
2. **단일 경량 바이너리 유지 (KISS 원칙 및 규칙 [RULE:RadicalKISS], [RULE:HermeticParity])**:
   - 무거운 C++17 DuckDB 컴파일 의존성을 LionSQL 바이너리에 직접 링크하지 않음으로써, 수 초 이내의 고속 빌드와 50MB 미만의 단일 정적 실행 파일 구조를 유지합니다.
3. **독립적 분석 스케일아웃 (Scale-Out)**:
   - 복수의 분석 워커, 데이터 사이언스 주피터 노트북, ETL 파이프라인이 단일 LionSQL CDC 엔드포인트를 동시에 구독하더라도 기본 스토리지 엔진에 락 경합이나 스레드 부하가 발생하지 않습니다.

---

## 2. LionSQL CDC 스트리밍 API 명세

LionSQL 서버가 HTTP 서비스(`:8090` 기본값)와 함께 실행되면 다음 실시간 CDC 스트리밍 엔드포인트가 노출됩니다:

- **엔드포인트**: `GET /api/cdc/stream`
- **쿼리 파라미터 (Query Parameters)**:
  - `table`: 이벤트를 필터링할 대상 테이블 이름 (예: `?table=orders`). 스냅샷 생성을 위해 필수 권장.
  - `consumer_id`: 영속 소비자 그룹 또는 워커 식별자 (예: `?consumer_id=duckdb_analytics_node1`). 서버 측 LSN 체크포인팅을 활성화합니다.
  - `from_lsn`: 특정 로그 시퀀스 번호(LSN) 이후에 커밋된 이벤트부터 스트리밍 (예: `?from_lsn=1000`). 생략되고 `consumer_id`가 지정된 경우, 서버에 저장된 마지막 체크포인트부터 자동 재개됩니다.
  - `snapshot`: 기존 테이블 데이터의 초기 베이스라인 스냅샷 모드:
    - `auto` (기본값): 최초 접속(`from_lsn <= 0` 또는 저장된 체크포인트 없음) 시 기존 적재된 모든 레코드를 `op="SNAPSHOT"` 이벤트로 먼저 방출한 뒤, 이후 발생하는 실시간 CDC 이벤트로 매끄럽게 전환합니다.
    - `never`: 초기 스냅샷을 건너뛰고, 지정된 `from_lsn` 이후의 신규 트랜잭션 변경분만 스트리밍합니다.
    - `always`: 기존 체크포인트와 무관하게 항상 전체 테이블 스냅샷을 먼저 방출한 후 실시간 스트림으로 전환합니다.
  - `format`: 페이로드 인코딩 형식 (`ndjson` 기본값, 또는 `sse` Server-Sent Events).

### 이벤트 작업 유형 및 페이로드 규격

스트림은 단조 증가하는 LSN 번호가 부여된 줄 단위 JSON(`ndjson`) 또는 Server-Sent Events(`sse`)로 전송됩니다:

| 작업 유형 (`op`) | 설명 | 포함 필드 |
|---|---|---|
| `SNAPSHOT` | 테이블 초기 기동 시 기존에 적재되어 있던 기준(Baseline) 레코드 | `lsn`, `timestamp`, `table`, `key`, `data` |
| `INSERT` | 신규 커밋된 행 삽입 이벤트 | `lsn`, `timestamp`, `table`, `key`, `data` |
| `UPDATE` | 커밋된 행 수정 이벤트 (변경된 컬럼 값 포함) | `lsn`, `timestamp`, `table`, `key`, `data`, `base_ver` |
| `DELETE` | 커밋된 행 삭제 이벤트 | `lsn`, `timestamp`, `table`, `key` |

#### 페이로드 예시 (NDJSON)

```json
{"lsn":100,"timestamp":1773574700000000000,"table":"orders","op":"SNAPSHOT","key":"1","data":{"id":1,"customer_id":101,"amount":45.0,"status":"SHIPPED"}}
{"lsn":101,"timestamp":1773574800000000000,"table":"orders","op":"INSERT","key":"2","data":{"id":2,"customer_id":102,"amount":150.5,"status":"COMPLETED"}}
{"lsn":102,"timestamp":1773574805000000000,"table":"orders","op":"UPDATE","key":"2","data":{"id":2,"customer_id":102,"amount":150.5,"status":"REFUNDED"},"base_ver":1}
{"lsn":103,"timestamp":1773574810000000000,"table":"orders","op":"DELETE","key":"2"}
```

---

## 3. 프로덕션급 Python + DuckDB 연동 워커 (`duckdb_cdc_worker.py`)

다음 파이썬 워커 스크립트는 외부 미들웨어(Kafka, Debezium, Zookeeper) 없이 순수 Python 표준 라이브러리와 `duckdb` 패키지만을 사용하여 제작되었습니다.

### 주요 핵심 기능:
1. **초기 기준 스냅샷 동기화**: `SNAPSHOT` 이벤트를 수신하여 기존 데이터를 DuckDB에 원자적으로 삽입합니다.
2. **영속 LSN 체크포인팅**: 수신된 마지막 LSN을 디스크에 원자적으로 저장하며, 서버 측 `consumer_id`와 연계하여 중복 재수신을 방지합니다.
3. **지수 백오프 기반 자동 재연결**: 네트워크 순단이나 DB 재기동 시 자동 복구 루프를 수행합니다.
4. **멱등적 Upsert**: `INSERT OR REPLACE` 구문을 통해 이벤트가 재전송되더라도 DuckDB 분석 테이블의 정합성을 100% 보장합니다.

```python
#!/usr/bin/env python3
"""
LionSQL - DuckDB 실시간 CDC 수집 워커
사전 요구 사항: pip install duckdb
"""
import json
import logging
import os
import time
import urllib.request
import urllib.error
import duckdb

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s"
)
logger = logging.getLogger("CDC-DuckDB")

LIONSQL_BASE_URL = os.environ.get("LIONSQL_URL", "http://127.0.0.1:8090")
TARGET_TABLE = os.environ.get("CDC_TABLE", "orders")
CONSUMER_ID = os.environ.get("CDC_CONSUMER_ID", "duckdb_analytics_node1")
DUCKDB_PATH = os.environ.get("DUCKDB_PATH", "analytics.duckdb")
CHECKPOINT_FILE = os.environ.get("CHECKPOINT_FILE", f".checkpoint_{TARGET_TABLE}.lsn")

def init_duckdb():
    con = duckdb.connect(DUCKDB_PATH)
    con.execute(f"""
        CREATE TABLE IF NOT EXISTS {TARGET_TABLE} (
            id BIGINT PRIMARY KEY,
            customer_id BIGINT,
            amount DOUBLE,
            status VARCHAR,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
    """)
    logger.info(f"DuckDB 연결 완료 ('{DUCKDB_PATH}'), 테이블 '{TARGET_TABLE}' 준비 완료.")
    return con

def load_local_checkpoint() -> int:
    if os.path.exists(CHECKPOINT_FILE):
        try:
            with open(CHECKPOINT_FILE, "r") as f:
                val = f.read().strip()
                if val.isdigit():
                    return int(val)
        except Exception as e:
            logger.warning(f"로컬 체크포인트 파일 읽기 실패: {e}")
    return 0

def save_local_checkpoint(lsn: int):
    # [RULE:AtomicSaves] 원자적 파일 저장을 통한 손상 방지
    tmp_path = f"{CHECKPOINT_FILE}.tmp"
    with open(tmp_path, "w") as f:
        f.write(str(lsn))
    os.replace(tmp_path, CHECKPOINT_FILE)

def process_stream():
    con = init_duckdb()
    last_lsn = load_local_checkpoint()
    logger.info(f"'{TARGET_TABLE}' 테이블 CDC 워커를 시작합니다. 시작 LSN: {last_lsn}")

    backoff = 1.0
    max_backoff = 30.0

    while True:
        query_params = [
            f"table={TARGET_TABLE}",
            f"consumer_id={CONSUMER_ID}",
            "format=ndjson"
        ]
        # 로컬 체크포인트가 존재할 경우 명시적으로 전달
        if last_lsn > 0:
            query_params.append(f"from_lsn={last_lsn}")
            query_params.append("snapshot=never")
        else:
            query_params.append("snapshot=auto")

        url = f"{LIONSQL_BASE_URL}/api/cdc/stream?{'&'.join(query_params)}"
        logger.info(f"LionSQL CDC 엔드포인트 연결 시도: {url}")

        try:
            req = urllib.request.Request(url, headers={"User-Agent": "LionSQL-DuckDB-Worker/1.0"})
            with urllib.request.urlopen(req, timeout=3600) as resp:
                backoff = 1.0  # 연결 성공 시 백오프 초기화
                logger.info("CDC 스트림 연결 수립 완료. 이벤트를 수집합니다...")

                for raw_line in resp:
                    if not raw_line:
                        continue
                    line = raw_line.decode("utf-8").strip()
                    if not line or line.startswith(":"):
                        continue  # 하트비트 핑 프레임 건너뛰기

                    try:
                        event = json.loads(line)
                    except json.JSONDecodeError:
                        logger.error(f"잘못된 JSON 페이로드: {line}")
                        continue

                    if event.get("type") == "ping":
                        continue

                    op = event.get("op")
                    lsn = event.get("lsn", 0)
                    data = event.get("data", {})

                    if op in ("SNAPSHOT", "INSERT"):
                        con.execute(f"""
                            INSERT OR REPLACE INTO {TARGET_TABLE} (id, customer_id, amount, status)
                            VALUES (?, ?, ?, ?)
                        """, [data.get("id"), data.get("customer_id"), float(data.get("amount", 0.0)), data.get("status")])

                    elif op == "UPDATE":
                        con.execute(f"""
                            UPDATE {TARGET_TABLE}
                            SET customer_id = ?, amount = ?, status = ?, updated_at = CURRENT_TIMESTAMP
                            WHERE id = ?
                        """, [data.get("customer_id"), float(data.get("amount", 0.0)), data.get("status"), data.get("id")])

                    elif op == "DELETE":
                        key = event.get("key")
                        con.execute(f"DELETE FROM {TARGET_TABLE} WHERE id = ?", [key])

                    if lsn > last_lsn:
                        last_lsn = lsn
                        save_local_checkpoint(last_lsn)

        except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, ConnectionResetError) as err:
            logger.warning(f"연결이 끊어졌습니다 ({err}). {backoff:.1f}초 후 재연결합니다...")
            time.sleep(backoff)
            backoff = min(backoff * 2.0, max_backoff)
        except Exception as e:
            logger.error(f"수집 루프 중 예외 발생: {e}", exc_info=True)
            time.sleep(backoff)
            backoff = min(backoff * 2.0, max_backoff)

if __name__ == "__main__":
    process_stream()
```

---

## 4. 체크포인팅 및 장애 복구 보장 메커니즘

LionSQL은 실시간 CDC 구독 애플리케이션의 무결성을 위해 2중 체크포인팅 방식을 제공합니다:

1. **서버 측 체크포인트 저장소 (`FileCheckpointStore`)**:
   - `consumer_id`가 지정되면, LionSQL 서버는 데이터 디렉토리 내의 `checkpoints.json`에 소비자별 최신 전달 LSN을 원자적으로 기록합니다.
   - 워커가 재시작되어 동일한 `consumer_id`로 재연결하면, 서버가 해당 체크포인트를 자동으로 로드하여 초기 스냅샷 재전송 없이 누락된 트랜잭션부터 즉시 스트리밍을 이어갑니다.
2. **클라이언트 측 원자적 체크포인트 파일 (`.checkpoint_<table_name>.lsn`)**:
   - 워커 스크립트는 수신된 마지막 LSN을 임시 파일 및 원자적 이름 변경(`os.replace`)을 통해 로컬 디스크에 안전하게 기록합니다.
   - 서버 측 체크포인트가 초기화되더라도, 클라이언트가 `from_lsn=<last_lsn>`을 명시적으로 전달하여 빈틈없는 데이터 수집이 가능합니다.
3. **목적지 멱등성 (Idempotent Destination State)**:
   - DuckDB에서 기본키 기반의 `INSERT OR REPLACE` (또는 `UPSERT`) 구문을 사용하므로, 일시적인 네트워크 장애로 인해 동일 이벤트가 중복 전달(At-Least-Once Delivery)되더라도 분석 테이블은 언제나 단일한 최종 상태를 유지합니다.

---

## 5. 터미널 cURL 실시간 검증

LionSQL 서버가 실행 중인 상태에서 터미널 명령을 통해 실시간 스트림을 즉시 검증할 수 있습니다:

```bash
# 1. consumer_id를 지정하여 초기 스냅샷 + 실시간 스트림 수신
curl -N "http://127.0.0.1:8090/api/cdc/stream?table=orders&consumer_id=test_worker_1"

# 2. 스냅샷 없이 특정 LSN(500) 이후 변경분만 재생
curl -N "http://127.0.0.1:8090/api/cdc/stream?table=orders&from_lsn=500&snapshot=never"

# 3. 표준 Server-Sent Events (SSE) 포맷으로 실시간 수신
curl -N "http://127.0.0.1:8090/api/cdc/stream?table=orders&format=sse"
```

---

## 관련 참고 문서

- [LionSQL 전체 사용자 매뉴얼](USER_MANUAL.ko.md)
- [LionSQL Auto-REST Records 가이드](REST_API_RECORDS_GUIDE.md)
- [LionSQL Web Studio 가이드](WEB_STUDIO_GUIDE.ko.md)
