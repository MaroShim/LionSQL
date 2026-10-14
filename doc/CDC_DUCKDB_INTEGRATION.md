# SQLion CDC (Change Data Capture) & DuckDB Integration Guide

SQLion은 고성능 OLTP 데이터베이스로서 트랜잭션의 신뢰성(ACID)과 초당 16,000+ TPS 이상의 처리량을 최우선으로 보장합니다. 본 문서는 SQLion의 코어 엔진을 가볍고 안전하게 유지하면서, **외부 분석 애플리케이션(Python, BI 툴, 분석 서버)에서 DuckDB를 임베딩하여 SQLion의 데이터를 실시간으로 고속 분석하는 분리형(Decoupled) CDC 연동 아키텍처**를 설명합니다.

---

## 1. 아키텍처 개요 (Decoupled Analytics Architecture)

```
[ OLTP 영역: SQLion Server ]
   ├── Port :5433 (PostgreSQL Wire Protocol)
   ├── Port :1433 (TDS / MSSQL Wire Protocol)
   └── Port :8090 (HTTP API & CDC Stream)
          │
          │ (모든 프로토콜의 커밋 트랜잭션을 포착)
          ▼
   [ WAL / CDC Streamer ] ──(초저지연 비동기 스트림)──┐
                                                        │
                                                        ▼
                        [ 분석 영역: 데이터 분석 어플리케이션 ]
                           ├── Python (FastAPI / Streamlit / Pandas)
                           └── Go / Node.js 분석 백엔드
                                  │ (HTTP 스트림 구독: NDJSON / SSE)
                                  ▼
                            [ DuckDB (Embedded Library) ]
                                  │ (인메모리 또는 파일 기반 컬럼 스토어)
                                  ▼
                            [ 0.001초 초고속 벡터 집계 / OLAP 쿼리 ]
```

### 왜 분리형 아키텍처인가?
1. **장애 격리 (Zero Blast Radius)**:
   - 분석 쿼리가 수천만 건의 데이터를 조인/집계하다가 OOM(메모리 고갈)이 발생해도, SQLion OLTP 코어 엔진은 영향을 받지 않고 정상 동작합니다.
2. **단일 바이너리 경량성 유지 (KISS & Golden Rule 15, 36)**:
   - SQLion 바이너리에 수십 MB 크기의 C++17 DuckDB 엔진을 얹지 않으므로 빌드 속도와 정적 실행 파일의 순수성이 유지됩니다.
3. **분석 스케일아웃(Scale-Out)**:
   - 동일한 SQLion CDC 엔드포인트를 여러 분석 워커나 데이터 과학자가 각자의 로컬 DuckDB에서 자유롭게 구독할 수 있습니다.

---

## 2. SQLion CDC 스트리밍 API 명세

SQLion 서버 기동 시 (`:8090` HTTP 활성화), 다음과 같은 실시간 CDC 스트림 엔드포인트가 제공됩니다:

- **엔드포인트**: `GET /api/cdc/stream`
- **쿼리 파라미터**:
  - `table`: 특정 테이블만 필터링 (예: `?table=orders`, 미지정 시 전체 테이블)
  - `from_lsn`: 특정 LSN 이후의 변경분부터 재전송 (미지정 시 0, 버퍼 내 과거 이력 자동 재생)
  - `format`: `ndjson` (기본값, 개행 구분 JSON) 또는 `sse` (Server-Sent Events)

### 응답 포맷 (NDJSON 예시)
```json
{"lsn":101,"timestamp":1773574800000000000,"table":"orders","op":"INSERT","key":"ORD_001","data":{"id":1,"customer_id":42,"amount":"150.50","status":"COMPLETED"}}
{"lsn":102,"timestamp":1773574805000000000,"table":"orders","op":"UPDATE","key":"ORD_001","data":{"id":1,"customer_id":42,"amount":"150.50","status":"SHIPPED"},"base_ver":1}
{"lsn":103,"timestamp":1773574810000000000,"table":"orders","op":"DELETE","key":"ORD_001"}
```

---

## 3. Python + DuckDB 연동 샘플 코드 (`duckdb_cdc_worker.py`)

Python 분석 환경에서 추가 라이브러리(Kafka, Debezium 등) 없이 표준 라이브러리와 `duckdb`만으로 SQLion의 변경 사항을 실시간 반영하는 예제입니다:

```python
#!/usr/bin/env python3
import json
import urllib.request
import duckdb

def run_cdc_ingestion():
    # 1. 로컬 DuckDB 연결 (인메모리 또는 파일 기반)
    con = duckdb.connect("analytics.duckdb")

    # 분석 대상 테이블 초기화 (필요 시)
    con.execute("""
        CREATE TABLE IF NOT EXISTS orders (
            id BIGINT PRIMARY KEY,
            customer_id BIGINT,
            amount DOUBLE,
            status VARCHAR,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
    """)
    print("[DuckDB] Analytics database connected and table prepared.")

    # 2. SQLion CDC 스트림 연결 (orders 테이블 구독)
    url = "http://127.0.0.1:8090/api/cdc/stream?table=orders"
    print(f"[CDC Worker] Subscribing to SQLion CDC stream: {url}")

    req = urllib.request.Request(url)
    with urllib.request.urlopen(req) as resp:
        for line in resp:
            if not line:
                continue
            line_str = line.decode("utf-8").strip()
            if not line_str or line_str.startswith(":"):
                continue  # Heartbeat ping

            event = json.loads(line_str)
            if event.get("type") == "ping":
                continue

            op = event.get("op")
            data = event.get("data", {})
            lsn = event.get("lsn")

            if op == "INSERT":
                con.execute("""
                    INSERT OR REPLACE INTO orders (id, customer_id, amount, status)
                    VALUES (?, ?, ?, ?)
                """, [data.get("id"), data.get("customer_id"), float(data.get("amount", 0)), data.get("status")])
                print(f"[CDC Event LSN:{lsn}] Inserted order id={data.get('id')}")

            elif op == "UPDATE":
                con.execute("""
                    UPDATE orders
                    SET customer_id = ?, amount = ?, status = ?
                    WHERE id = ?
                """, [data.get("customer_id"), float(data.get("amount", 0)), data.get("status"), data.get("id")])
                print(f"[CDC Event LSN:{lsn}] Updated order id={data.get('id')}")

            elif op == "DELETE":
                key = event.get("key")
                con.execute("DELETE FROM orders WHERE id = ?", [key])
                print(f"[CDC Event LSN:{lsn}] Deleted order key={key}")

if __name__ == "__main__":
    run_cdc_ingestion()
```

---

## 4. 터미널에서 즉시 확인하는 방법 (cURL)

서버 기동 후 터미널에서 cURL을 실행하면 커밋되는 모든 변경 이벤트를 실시간으로 확인할 수 있습니다:

```bash
# 전체 변경 이벤트 스트림 확인
curl -N http://127.0.0.1:8090/api/cdc/stream

# 특정 테이블(users)만 구독
curl -N "http://127.0.0.1:8090/api/cdc/stream?table=users"
```
