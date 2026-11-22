# LionSQL 프로덕션 배포 및 운영 런북 (`doc/OPERATIONS.ko.md`)

> - **최초 작성일**: 2026-09-22
> - **최종 갱신일**: 2026-11-22
> [English Version](OPERATIONS.md)

본 문서는 실제 엔터프라이즈 프로덕션 환경에서 LionSQL을 안정적으로 배포하고, 무중단으로 운영하며, 프로세스를 감시하고, 장애 복구 및 보안 하드닝(Hardening)을 수행하기 위한 공식 운영 런북입니다.

---

## 1. 시스템 요구사항 및 용량 계획 (System Requirements & Capacity Planning)

### 1.1 하드웨어 권장 가이드라인

| 워크로드 등급 | 동시 연결 수 | CPU 코어 | 메모리 (RAM) | 스토리지 아키텍처 |
|---|---|---|---|---|
| **엣지 / 개발 (Edge / Dev)** | 1 ~ 100 | 2 코어 | 2 GB | 표준 SSD |
| **표준 OLTP (Standard OLTP)** | 100 ~ 2,000 | 4 ~ 8 코어 | 8 ~ 16 GB | NVMe SSD (Ext4 / XFS) |
| **초고성능 엔터프라이즈 (Enterprise)** | 2,000 ~ 20,000+ | 16 ~ 64 코어 | 32 ~ 128 GB | 전용 NVMe 어레이 (RAID 10) |

### 1.2 메모리 할당 계층 구조
프로세스의 총 메모리 소비량은 다음 수식에 의해 바운딩됩니다:
$$\text{Total Memory} \approx \text{WiredTiger Cache} + (\text{Active Workers} \times \text{work\_mem}) + (\text{Client Connections} \times 4\text{KB})$$
- **WiredTiger 캐시**: 기본 500MB (설정 또는 코드로 동적 조정).
- **워커 실행 메모리**: `MaxWorkers * work_mem`. 64MB `work_mem` 기준 32 워커 구동 시 최대 2GB로 캡(Cap).
- **클라이언트 연결**: 경량 Go 고루틴으로 처리 (연결당 약 4KB 스택 오버헤드).

---

## 2. 프로덕션 배포 모델 (Deployment Models)

### 2.1 Linux Systemd 서비스 (베어메탈 및 VM 권장)

`/etc/systemd/system/lionsql.service` 파일 생성:

```ini
[Unit]
Description=LionSQL High-Concurrency OLTP Database Server
After=network.target local-fs.target
Documentation=https://github.com/MaroShim/LionSQL

[Service]
Type=simple
User=lionsql
Group=lionsql
WorkingDirectory=/var/lib/lionsql
ExecStart=/usr/local/bin/lionsql serve \
    -dir=/var/lib/lionsql/data \
    -engine=wiredtiger \
    -http=127.0.0.1:8090 \
    -pg=0.0.0.0:5433 \
    -tds="" \
    -no-studio \
    -work-mem=64MB \
    -max-workers=64 \
    -max-idle-sessions=128 \
    -auth=true \
    -admin-email=dba@company.com \
    -admin-pass=EnvSecurePassString!#

Restart=always
RestartSec=5s
LimitNOFILE=65536
LimitNPROC=32768
TimeoutStopSec=30s
KillMode=mixed
KillSignal=SIGTERM

[Install]
WantedBy=multi-user.target
```

서비스 활성화 및 기동:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lionsql
sudo systemctl status lionsql
```

---

### 2.2 Docker 컨테이너 배포

`Dockerfile`:

```dockerfile
FROM golang:1.26-bookworm AS builder
WORKDIR /app
COPY . .
RUN apt-get update && apt-get install -y libwiredtiger-dev
RUN go build -tags wiredtiger -buildvcs=false -o /bin/lionsql ./cmd/lionsql

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y libwiredtiger0 ca-certificates curl && rm -rf /var/lib/apt/lists/*
COPY --from=builder /bin/lionsql /usr/local/bin/lionsql

EXPOSE 8090 5433 1433
VOLUME ["/data"]

ENTRYPOINT ["/usr/local/bin/lionsql"]
CMD ["serve", "-dir=/data", "-http=127.0.0.1:8090", "-pg=0.0.0.0:5433", "-tds=0.0.0.0:1433"]
```

`docker-compose.yml`:

```yaml
version: '3.8'

services:
  lionsql:
    image: lionsql:latest
    container_name: lionsql-db
    restart: unless-stopped
    ports:
      - "127.0.0.1:8090:8090"
      - "5433:5433"
      - "1433:1433"
    volumes:
      - lionsql-data:/data
    environment:
      - LIONSQL_AUTH=true
      - LIONSQL_ADMIN_EMAIL=dba@company.com
      - LIONSQL_ADMIN_PASS=ProdSecurePass123!#
    healthcheck:
      test: ["CMD", "curl", "-f", "http://127.0.0.1:8090/api/health"]
      interval: 10s
      timeout: 3s
      retries: 3

volumes:
  lionsql-data:
```

---

## 3. 백그라운드 데몬 라이프사이클 제어 (Daemon Lifecycle Controls)

LionSQL은 외부 프로세스 래퍼 없이 자체 내장 데몬 명령어를 지원합니다:

```bash
# 1. 백그라운드 데몬으로 기동
./bin/lionsql start -dir=/var/lib/lionsql/data

# 2. 실행 중인 데몬 상태 및 PID 확인
./bin/lionsql status -dir=/var/lib/lionsql/data

# 3. 우아한 재시작 (Graceful Restart: 설정 재로드, 활성 데이터 보존)
./bin/lionsql restart -dir=/var/lib/lionsql/data

# 4. 데몬 정상 종료 (Clean Teardown)
./bin/lionsql stop -dir=/var/lib/lionsql/data
```

### 클린 종료 및 시그널 핸들링 (`[RULE:CleanTeardown]`)
`SIGTERM` 또는 `SIGINT` 수신 시:
1. TDS 및 PostgreSQL 와이어 리스너를 즉시 닫아 신규 접속 차단.
2. 활성 클라이언트 트랜잭션을 수습하고 HTTP 서버의 15초 드레인(Drain) 윈도우 진행.
3. 인플라이트(In-flight) 쓰기 버퍼 플러시, B+Tree 인덱스 체크포인트 및 `engine.Close()` 수행.
4. 리턴 코드 `0`으로 정상 종료.

---

## 4. 재해 복구 및 백업 자동화 (Disaster Recovery & Backup Automation)

### 4.1 Cron 기반 일일 원터치 스냅샷 백업

`/etc/cron.daily/lionsql-backup` 스크립트 작성:

```bash
#!/usr/bin/env bash
set -euo pipefail

BACKUP_DIR="/var/backups/lionsql"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
TARGET_FILE="${BACKUP_DIR}/lionsql_backup_${TIMESTAMP}.tar.gz"

mkdir -p "${BACKUP_DIR}"

# 온라인 무중단 스냅샷 아카이브 생성
/usr/local/bin/lionsql backup "${TARGET_FILE}" -dir=/var/lib/lionsql/data

# 7일 이상 지난 백업본 자동 회전(Purge)
find "${BACKUP_DIR}" -type f -name "lionsql_backup_*.tar.gz" -mtime +7 -delete
```

### 4.2 스냅샷 백업으로부터의 복원

```bash
# 1. 기존 서비스 정지
sudo systemctl stop lionsql

# 2. 백업 아카이브로부터 데이터 복원
/usr/local/bin/lionsql restore /var/backups/lionsql/lionsql_backup_20261122_000000.tar.gz -dir=/var/lib/lionsql/data

# 3. 서비스 재개 및 헬스 검증
sudo systemctl start lionsql
curl -fsS http://127.0.0.1:8090/api/health
```

---

## 5. 보안 및 네트워크 강화 (Security & Network Hardening)

### 5.1 네트워크 바인딩 및 리버스 프록시 (Reverse Proxy)

공개 프로덕션 환경의 권장 구성:
1. LionSQL HTTP API (`-http`)는 반드시 `127.0.0.1:8090` 로컬호스트에 바인딩합니다.
2. 외부 공개 웹 접속은 Nginx 또는 Caddy를 통해 HTTPS 및 웹소켓을 종단(Terminate)합니다.

### 5.2 방화벽 포트 노출 규칙

| 포트 | 프로토콜 | 기본 노출 권장 범위 | 용도 |
|---|---|---|---|
| `5433` | TCP | 프라이빗 VPC / 내부망 | PostgreSQL Wire Protocol (psql, 앱 ORM) |
| `1433` | TCP | 프라이빗 VPC / 내부망 | Microsoft TDS Protocol (SSMS, sqlcmd, C# 드라이버) |
| `8090` | TCP | 로컬호스트 (`127.0.0.1`) / 리버스 프록시 | 내장 Web Studio, REST API, 헬스체크 및 메트릭 |

### 5.3 프로덕션 필수 보안 및 포트 하드닝 체크리스트 (Production Pre-Flight Checklist)

> [!CAUTION]
> **제로 트러스트 프로덕션 잠금 필수 요건 (Zero-Trust Production Lockdown)**:
> 개발 편의를 위해 LionSQL은 기본적으로 로컬호스트(`127.0.0.1`) 바인딩과 기본 관리자 계정(`admin@lionsql.local` / `1234567890`)을 내장하고 있습니다. **이 기본 설정을 변경하지 않고 스테이징이나 프로덕션에 그대로 배포하는 것은 전면 금지됩니다.**

외부 트래픽에 노출하기 전, 반드시 다음 4가지 핵심 보안 제어를 검증하십시오:

1. **관리자 기본 자격증명(Superuser Password) 강제 변경**:
   - 기본 비밀번호가 유지된 상태로 기동되면 LionSQL은 콘솔에 즉시 경고를 출력합니다:  
     `WARNING: DEFAULT SUPERUSER PASSWORD HAS NOT BEEN CHANGED! CHANGE PASSWORD IMMEDIATELY TO PREVENT UNAUTHORIZED ACCESS!`
   - CLI 플래그 또는 환경 변수를 통해 강력한 비밀번호로 즉시 덮어쓰십시오:
     ```bash
     -admin-email="dba@company.com" -admin-pass="StrongUniquePassphrase123!#"
     # 또는 환경 변수 주입:
     export LIONSQL_ADMIN_EMAIL="dba@company.com"
     export LIONSQL_ADMIN_PASS="StrongUniquePassphrase123!#"
     ```

2. **사용하지 않는 와이어 프로토콜 포트 비활성화 (공격 표면 및 포트 충돌 최소화)**:
   - LionSQL은 기본적으로 3개의 리스너(PGWire `:5433`, TDS `:1433`, HTTP/Studio `:8090`)를 동시에 엽니다.
   - 아키텍처상 한 종류의 프로토콜만 사용하는 경우, **사용하지 않는 포트를 완전히 꺼서 포트 충돌을 방지하고 불필요한 네트워크 노출을 원천 차단**하십시오:
     ```bash
     # Case A: 순수 PostgreSQL / ORM 마이크로서비스 (TDS 1433 및 웹 스튜디오 비활성화)
     lionsql serve -pg=0.0.0.0:5433 -tds="" -no-studio
     # (REST/헬스체크도 필요 없는 순수 DB 환경인 경우: -http="")

     # Case B: 순수 MS SQL Server / SSMS 워크로드 (PostgreSQL 5433 및 웹 스튜디오 비활성화)
     lionsql serve -tds=0.0.0.0:1433 -pg="" -no-studio

     # Case C: 고보안 헤드리스 DB (헬스체크 API는 유지하되 Web Studio UI 비활성화)
     lionsql serve -pg=0.0.0.0:5433 -tds=0.0.0.0:1433 -http=127.0.0.1:8090 -no-studio
     ```

3. **인터페이스 바인딩 격리 (Localhost vs. Private VPC)**:
   - `-http`를 공인 인터넷(`0.0.0.0`)에 직접 바인딩하지 마십시오. 웹 API는 `127.0.0.1:8090`에 두고 HTTPS 리버스 프록시 뒤에 배치하십시오.
   - 데이터베이스 와이어 포트(`-pg`, `-tds`)는 공인망이 아닌 내부 사설 VPC IP(예: `-pg=10.0.1.20:5433`)에 한정하여 바인딩하십시오.

4. **SSL/TLS 암호화 강제 (`-require-ssl`)**:
   - 전송 계층 암호화를 강제하여 네트워크 스니핑을 방어하십시오:
     ```bash
     -tls -tls-cert=/etc/lionsql/certs/server.crt -tls-key=/etc/lionsql/certs/server.key -require-ssl
     ```

---

## 6. 헬스 모니터링 및 관측성 (Health Monitoring & Observability)

### 6.1 Kubernetes 프로브 사양

```yaml
livenessProbe:
  httpGet:
    path: /api/health
    port: 8090
  initialDelaySeconds: 5
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /api/pool/stats
    port: 8090
  initialDelaySeconds: 2
  periodSeconds: 5
```

### 6.2 핵심 운영 메트릭 (`/api/pool/stats`)

| 메트릭 키 | 목표 임계치 | 임계치 초과 시 조치 |
|---|---|---|
| `queued_requests` | `0` ~ `5` | 지속적으로 > 50 초과 시 `-max-workers` 증설 또는 CPU 스케일업 |
| `active_workers` | `< max_workers * 0.8` | 정상 작업 범위 |
| `idle_sessions` | `> 5` | 0으로 고갈될 경우 `-max-idle-sessions` 증설 |

---

## 7. 고가용성 및 1-Writer Multi-Reader 복제 런북 (Replication Runbook)

LionSQL은 단일 프라이머리 노드가 모든 쓰기 트랜잭션을 처리하고, 복제본(Replica) 노드들이 백그라운드에서 WAL 레코드를 지속적으로 수신하여 리플레이하는 **1-Writer Multi-Reader 고가용성 아키텍처**를 지원합니다.

### 7.1 프라이머리 노드 기동

```bash
/usr/local/bin/lionsql serve \
    -node-id=prod-primary-01 \
    -dir=/var/lib/lionsql/data \
    -engine=wiredtiger \
    -http=0.0.0.0:8090 \
    -pg=0.0.0.0:5433 \
    -tds="" \
    -cluster-token="prod-cluster-secret-token"
```

### 7.2 읽기 전용 복제본(Read Replica) 기동

```bash
/usr/local/bin/lionsql serve \
    -node-id=prod-replica-01 \
    -replica-of=http://primary-host.internal:8090 \
    -dir=/var/lib/lionsql/replica_data \
    -engine=wiredtiger \
    -http=0.0.0.0:8090 \
    -pg=0.0.0.0:5433 \
    -tds="" \
    -cluster-token="prod-cluster-secret-token"
```

복제본 노드는 부팅 즉시:
1. 프라이머리의 `/api/replication/stream`으로 지속 스트림 연결.
2. 최종 리플레이 LSN부터 순차 재생.
3. 읽기 전용 제약(`ErrReadOnlyReplica`)을 강제하여 무결성 유지.

### 7.3 무중단 승격(Promotion) 및 장애조치

프라이머리 노드 장애 시, 가장 최신 LSN을 보유한 복제본에 승격 API를 호출합니다:
```bash
curl -fsS -X POST \
    -H "X-Cluster-Token: prod-cluster-secret-token" \
    http://replica-host.internal:8090/api/replication/promote
```
승격된 복제본은 즉시 읽기 전용 제약을 해제하고 프라이머리로 전환되어 신규 쓰기 트래픽을 처리합니다.
