## 3. 정량적 엔지니어링 성과 및 벤치마크 지표

| 벤치마크 / 지표 항목 | 측정 수치 및 달성 결과 | 비고 |
| :--- | :--- | :--- |
| **Go Native TPC-C Throughput** | **763,496 tpmC / 28,300 TPS** | 2 Warehouses, 20 Workers, 15s (New-Order 평균 0.82ms) |
| **T-SQL VDBE TPC-C Throughput** | **475,781 tpmC / 17,803 TPS** | 동일 조건 가상머신 환경 (New-Order P50 0.56ms) |
| **VDBE 가상머신 성능 유지율** | **네이티브 드라이버 대비 63.0%** | 바이트코드 인터프리터 오버헤드 속에서도 단일 노드 1.78만 TPS 사수 |
| **인메모리 SkipList 삽입 속도** | **초당 280만 회 (2.8M inserts/sec)** | 동시성 락프리 구조 |
| **Go 인프로세스 스칼라 UDF 지연** | **5 나노초 미만 (< 5ns)** | `proc.RegisterScalarFunction` 인라인 직결 호출 |
| **단위/통합 테스트 코드 규모** | **28,587 라인** | 전체 패키지 동시성 테스트 `go test -race` 100% 패스 |
| **외부 의존성** | **0개 (Zero External Dependency)** | 단일 정적 바이너리 배포 원칙 준수 |

---

## 4. 구조적 한계 및 아키텍처적 부채 (Technical Debt & Bottlenecks)

현재의 높은 구현 성숙도에도 불구하고, Phase 5 및 프로덕션 LTS 릴리즈를 위해 반드시 해결해야 할 핵심 과제들은 다음과 같습니다:

### 4.1. 계층 구조 역전: SQL 실행 코디네이터 패키지 분리 (`pkg/api` ➔ `pkg/engine`)
* **현상**: HTTP REST 웹 핸들러를 담당해야 하는 `pkg/api`에 15,000+ 라인의 SQL 실행 오케스트레이션 로직(`sqlexec*.go`)이 여전히 집중되어 있습니다.
* **부채**: PGWire(`pkg/wire`)와 TDS(`pkg/wire/tds`)가 코어 엔진이 아닌 상위 어댑터인 `pkg/api`를 역참조하는 계층 역전 현상이 존재합니다.
* **대응책**: SQL 오케스트레이션 로직을 `pkg/engine` 코어로 독립 승격시켜 `pkg/api`, `pkg/wire`, `pkg/wire/tds`가 동등하게 코어를 단방향 호출하도록 리팩토링해야 합니다.

### 4.3. CBO 조인 알고리즘 및 대규모 조인 탐색 확장
* **현상**: 현재 Hash Join과 Index Nested Loop Join은 완비되었으나, 정렬된 입력을 $O(M+N)$ 단일 패스로 처리하는 Sort-Merge Join(`MergeJoinIterator`)이 미구현 상태입니다.
* **부채**: $N > 4$ 체인 조인에 대해 전체 순열 탐색을 수행하므로, 5개 이상 테이블 조인 시 Left-Deep DP 및 Bushy Tree 동적 탐색(Selinger)이 필요합니다.

### 4.4. 타 언어 클라이언트 인증 및 TDS 고급 직렬화 미완비
* **현상**: Go 생태계 드라이버(`pgx`, `go-mssqldb`)는 완벽히 지원하나, Python(`psycopg3`, `pymssql`), Node.js(`pg`, `tedious`), .NET(`Microsoft.Data.SqlClient`)에 대한 E2E 통합 하네스가 부족합니다.
* **부채**: TDS 프로토콜의 `NBCROW`(0xD2 NULL 비트맵 행 압축) 및 대용량 LOB 스트리밍(PLP)이 남아 있습니다.

### 4.5. 엔터프라이즈 보안 및 거버넌스
* **현상**: 단일 데이터베이스 프로세스 기반의 인스턴스 격리는 확립되었으나, 정교한 SQL 객체 레벨 RBAC(`GRANT`/`REVOKE`), 투명 데이터 암호화(TDE), 불변 감사 로그는 Phase 5 로드맵에 대기 중입니다.

---

## 5. 향후 우선순위 로드맵 및 권장 대응 전략

SQLion은 현재 새로운 이색 기능을 무리하게 추가하는 시기보다, **엔진의 코어 아키텍처 정립과 프로덕션 신뢰성을 굳히는 시기**입니다. 다음 우선순위 순서로 개발을 진행하는 것이 가장 효과적입니다:

1. **최우선 과제 (Sprint 1)**:
   * **SQL 실행 코디네이터 독립 분리**: `pkg/api` 내의 SQL 오케스트레이터를 `pkg/engine`으로 추출하여 클린 아키텍처 확립.
2. **차우선 과제 (Sprint 2)**:
   * **Sort-Merge Join 연산자 및 CBO 비용 모델 반영**: 정렬 인덱스 기반 대량 조인 가속.
   * **TDS NBCROW(0xD2) 압축 및 타 언어(Python/Node.js) 클라이언트 E2E 하네스 구축**.
3. **중기 과제 (Phase 5 후반부 ~ Phase 6)**:
   * **엔터프라이즈 보안 (RBAC, TDE)** 및 백그라운드 MVCC Vacuuming.
   * **공유 메모리(POSIX shm / mmap) Apache Arrow IPC 스냅샷**을 통한 DuckDB/Polars 무복사(Zero-Copy) HTAP 분석 연동.

---

## 6. 결론

SQLion은 현존하는 오픈소스 임베디드/경량 DB 중에서도 독보적인 아키텍처(T-SQL VDBE VM + 듀얼 스토리지 + PGWire/TDS 멀티 프로토콜 + 단일 바이너리)를 갖추고 있으며, 
이미 **단일 노드 1.7만~2.8만 TPS를 소화하는 강력한 실전 완성도**를 증명했습니다.

계층 역전(`pkg/api` ➔ `pkg/engine`) 해소와 VDBE 인스트럭션 컴팩션이라는 마지막 아키텍처 정비가 완료되면, 
임베디드 및 경량 엔터프라이즈 시장에서 대체 불가능한 고성능 독립형 데이터베이스 엔진으로 안착할 수 있을 것으로 판단됩니다.
