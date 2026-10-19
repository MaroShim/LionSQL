# LionSQL 트랜잭션 성능 및 워크로드별 옵션 튜닝 가이드

본 문서는 LionSQL 데이터베이스 엔진의 스토리지 백엔드, WAL 동기화 정책, 메모리 거버너 및 워커 풀 설정 옵션이 트랜잭션 처리량(TPS), 지연 시간(Latency), 데이터 내구성(Durability/RPO)에 미치는 영향을 분석하고, 워크로드 유형에 따른 최적의 옵션 조합 가이드를 제공합니다.

---

## 1. 개요 및 성능-내구성 트레이드오프 (Trade-off Matrix)

데이터베이스 트랜잭션 성능은 **"물리적 디스크 동기화 배리어(Fsync Barrier)"**와 **"동시성 제어 모델(OCC vs MVCC)"**의 선택에 따라 수십 배까지 달라질 수 있습니다. LionSQL은 단일 바이너리 내에서 극단적인 마이크로초 단위의 인메모리 처리량부터 하드웨어 장애 시에도 단 1바이트의 손실을 허용하지 않는 절대 무손실 금융 원장 수준까지 유연한 스펙트럼을 제공합니다.

```
[ 높은 처리량 / 낮은 지연 시간 ]
   ▲  -engine memory, -ephemeral, -fsync off       (20,000 ~ 25,000+ TPS / <1ms P99)
   │  -engine memory, -fsync everysec              (14,000 ~ 16,000 TPS / ~1ms P99)
   │  -engine wiredtiger, -fsync everysec (기본값)   (12,000 ~ 16,800 TPS / <10ms P99)
   │  -engine wiredtiger, -fsync always            (1,700 ~ 1,860 TPS / 15~25ms P99)
   ▼
[ 완벽한 영속성 / RPO = 0 (무손실) ]
```

---

## 2. 워크로드별 4대 권장 옵션 프로파일 (Workload Profiles)

실무에서 가장 빈번하게 요구되는 4가지 워크로드 유형에 맞춘 표준 옵션 레시피는 다음과 같습니다:

### 2.1. 프로파일 1: 초저지연 인메모리 캐시 및 실시간 세션 (Ultra Low-Latency)
디스크 I/O 병목을 배제하고 서브밀리초 응답 속도가 최우선인 환경에 적합합니다.

* **권장 옵션 조합**:
  ```bash
  lionsql serve \
    -engine memory \
    -ephemeral \
    -fsync off \
    -max-memory 4GB \
    -max-workers 256
  ```
* **성능 지표**:
  - **처리량**: **20,000 ~ 25,000+ TPS** (TPC-C 기준 > 540,000 tpmC)
  - **지연 시간**: Median P50 **0.05 ms**, Tail P99 **< 1.0 ms**
  - **데이터 내구성 (RPO)**: **휘발성 (N/A)** (프로세스 크래시 시 데이터 복구 불가)
* **적합한 유스케이스**:
  - 실시간 게임 랭킹, 플레이어 세션 및 로비 상태 관리
  - 고빈도 사용자 장바구니 및 토큰 캐싱 레이어
  - CI/CD 파이프라인 상의 고속 단위/통합 테스트 자동화 환경
  - 마이크로서비스 간 임시 큐잉 및 고속 집계 버퍼

### 2.2. 프로파일 2: 표준 프로덕션 OLTP (권장 기본 설정)
대다수 엔터프라이즈 상용 서비스에서 처리량과 데이터 안전성의 최적 균형을 제공하는 표준 설정입니다.

* **권장 옵션 조합**:
  ```bash
  lionsql serve \
    -dir /var/lib/lionsql/data \
    -engine wiredtiger \
    -fsync everysec \
    -max-memory 4GB \
    -work-mem 64MB \
    -max-workers 256 \
    -max-idle-sessions 64
  ```
* **성능 지표**:
  - **처리량**: **12,000 ~ 16,800+ TPS** (TPC-C 기준 ~ 457,000 tpmC)
  - **지연 시간**: Median P50 **0.12 ~ 0.85 ms**, Tail P99 **< 10 ms**
  - **데이터 내구성 (RPO)**: **최대 1초 (1s Periodic Durability)** (1초 주기 백그라운드 fsync)
* **적합한 유스케이스**:
  - 전자상거래(E-Commerce) 주문, 결제 요청 및 상품 카탈로그 관리
  - SaaS 플랫폼의 멀티테넌트 고객 데이터베이스
  - 물류 및 배송 상태 추적 시스템
  - 일반 기업용 ERP / CRM 비즈니스 트랜잭션

### 2.3. 프로파일 3: 절대 무손실 금융 코어 원장 (Strict Zero-Loss)
정전, 하드웨어 패닉, OS 크래시가 발생하더라도 커밋 완료된 단 1건의 트랜잭션도 유실되어서는 안 되는 환경입니다.

* **권장 옵션 조합**:
  ```bash
  lionsql serve \
    -dir /var/lib/lionsql/data \
    -engine wiredtiger \
    -fsync always \
    -max-memory 8GB \
    -work-mem 64MB \
    -max-workers 128 \
    -max-idle-sessions 128
  ```
* **성능 지표**:
  - **처리량**: **1,700 ~ 1,860+ TPS** (Leader/Follower Group Commit 활성화)
  - **지연 시간**: Median P50 **1.5 ~ 2.5 ms**, Tail P99 **15 ~ 25 ms**
  - **데이터 내구성 (RPO)**: **RPO = 0 (Zero Data Loss)** (물리 NVMe `F_FULLFSYNC` 배리어 동기화)
* **적합한 유스케이스**:
  - 은행 계좌 이체, 가상자산 지갑 원장, 결제 승인 원장
  - 법적 증빙이 요구되는 불변 감사 추적(Audit Trail) 및 전자 계약
  - 주식 및 외환 매매 체결 기록 보존

### 2.4. 프로파일 4: 대용량 분석, 마이그레이션 및 정기 배치 (Heavy Batch / ETL)
수백만 건의 대용량 데이터를 일괄 적재하거나 대규모 조인/정렬 리포트를 단시간에 추출해야 하는 배치 워크로드입니다.

* **권장 옵션 조합**:
  ```bash
  lionsql serve \
    -dir /var/lib/lionsql/data \
    -engine wiredtiger \
    -work-mem 256MB \
    -max-memory 16GB \
    -max-workers 64
  ```
* **성능 특성**:
  - 단일 연산자 정렬 메모리 예산을 `256MB`로 확대하여 디스크 스필(External Merge Sort)을 원천 차단하고 100% 인메모리 파이프라인 유지
  - 동시 워커 수를 64개로 제한하여 CPU 코어가 분석 집계 연산에 집중되도록 스케줄링
* **적합한 유스케이스**:
  - 타 DBMS(PostgreSQL, MSSQL, Oracle)로부터의 초기 대용량 데이터 마이그레이션
  - 야간 일마감 회계 정산 및 월간 통계 집계 배치
  - 대규모 인덱스 재생성(`CREATE INDEX`) 및 스키마 재구성

---

## 3. 핵심 파라미터별 성능 영향 분석

### 3.1. `-engine` (스토리지 엔진 선택)
* **`wiredtiger` (기본값)**:
  - 디스크 기반 Lock-free B+Tree 및 MVCC 아키텍처.
  - 가용 RAM보다 훨씬 큰 대용량 데이터셋(수십~수백 GB)을 안전하게 처리.
  - 페이지 압축(Snappy)을 통해 디스크 용량을 50~70% 절감.
* **`memory`**:
  - 순수 힙 메모리 기반 동시성 스킵리스트(Concurrent SkipList) 및 낙관적 동시성 제어(OCC).
  - 포인터 간접 참조 및 디스크 I/O가 제거되어 단순 읽기/쓰기 시 서브마이크로초(~100ns) 레이턴시 달성.
  - 쓰기 충돌이 빈번한 워크로드에서는 OCC 재시도 오버헤드가 발생할 수 있으므로 격리된 키 공간 처리에 최적.

### 3.2. `-fsync` (WAL 및 트랜잭션 저널 동기화 모드)
| 모드 | 동작 방식 | TPS 영향 | 정전 시 데이터 손실 위험 (RPO) |
| :--- | :--- | :---: | :--- |
| **`everysec`** (기본) | 백그라운드 고루틴이 1초 주기로 WAL을 디스크에 플러시 | **최상 (100%)** | 최대 1초 이내 커밋 데이터 유실 가능 |
| **`always`** | 커밋 시점마다 동기식 `F_FULLFSYNC` 디스크 배리어 호출 (Group Commit 결합) | **~12%** | **유실 없음 (RPO = 0)** |
| **`off`** | OS 커널의 페이지 캐시 플러시 주기에 위임 | **최고 (>120%)** | OS 크래시 시 수 초~수십 초 유실 가능 |

> [!NOTE]
> LionSQL은 `-fsync always` 모드 구동 시 **Leader/Follower Group Commit** 알고리즘을 자동으로 적용합니다. 동시간대에 몰려든 수십 개의 트랜잭션 커밋 요청을 단일 물리적 디스크 배리어로 묶어 처리함으로써 순수 단일 스레드 대비 TPS를 4배 이상 향상시킵니다.

### 3.3. `-work-mem` (정렬 및 조인 작업 메모리 예산)
* **기본값**: `64MB`
* **동작 원리**: `ORDER BY`, `GROUP BY`, `HashJoin` 연산자는 실행 중에 레코드를 메모리에 누적합니다. 데이터 크기가 `work-mem`을 초과하면 디스크 임시 파일로 분할 기록하는 **외부 정렬(External Merge Sort)** 모드로 자동 전환됩니다.
* **튜닝 지침**:
  - 메모리가 제한된 환경(1~2GB RAM): `-work-mem 16MB`로 낮추어 여러 세션이 동시에 정렬을 수행할 때 OOM 킬러 유발을 방지합니다.
  - 분석 리포트 세션: 세션별로 `SET work_mem = '256MB';`를 동적 설정하여 디스크 I/O 없이 초고속 메모리 정렬을 수행합니다.

### 3.4. `-max-memory` (글로벌 힙 메모리 상한 및 거버너)
* LionSQL의 메모리 거버너는 프로세스 전체의 힙 할당량을 감시합니다.
* 사용량이 임계치(85%)에 도달하면 신규 대규모 쿼리의 메모리 할당을 일시 지연시키고 플랜 캐시를 정리합니다.
* 컨테이너(Docker, Kubernetes) 환경에서는 컨테이너 메모리 제한(cgroup limit)의 70~80%로 설정하여 OOM 킬러에 의한 컨테이너 강제 재기동을 방지해야 합니다.

---

## 4. 하드웨어 스펙별 권장 배포 매트릭스

| 환경 규모 | 하드웨어 사양 | 권장 LionSQL 설정 인자 | 기대 트랜잭션 처리량 |
| :--- | :--- | :--- | :---: |
| **Edge / 소형 VM** | 1~2 vCPU, 2GB RAM<br>(Cloud e2-small, T4g.small) | `-max-memory 1200MB`<br>`-work-mem 16MB`<br>`-max-workers 64`<br>`-max-idle-sessions 32` | **3,000 ~ 5,000 TPS** |
| **표준 프로덕션** | 4~8 vCPU, 8~16GB RAM<br>NVMe SSD | `-max-memory 8GB`<br>`-work-mem 64MB`<br>`-max-workers 256`<br>`-max-idle-sessions 64`<br>`-fsync everysec` | **12,000 ~ 16,800 TPS** |
| **고성능 베어메탈** | 16~32 vCPU, 64GB RAM<br>PCIe 4.0 NVMe Enterprise | `-max-memory 32GB`<br>`-work-mem 128MB`<br>`-max-workers 512`<br>`-max-idle-sessions 256`<br>`-fsync everysec` | **25,000+ TPS** |

---

## 5. 실무 모니터링 및 성능 진단 체크리스트

1. **지연 쿼리 감지 (`-slow-threshold-ms`)**:
   - 프로덕션에서는 기본값 `100.0ms`를 권장하며, 고성능 금융 환경에서는 `-slow-threshold-ms 20.0`으로 강화하여 인덱스 미적용 풀스캔을 즉시 식별합니다.
2. **트랜잭션 직렬화 충돌 (`40001 ErrWriteConflict`)**:
   - OCC 또는 WiredTiger에서 동시 쓰기 경합이 발생하면 클라이언트에 `40001` 에러가 반환됩니다.
   - 애플리케이션 계층에서는 지수 백오프(Exponential Backoff)를 적용한 재시도(Retry) 로직을 갖추어야 합니다.
3. **디스크 I/O 대기율 감지**:
   - `GET /api/metrics`를 통해 `sqlion_wal_sync_duration_seconds` 히스토그램을 모니터링하여 물리 디스크 병목을 조기에 진단하십시오.
