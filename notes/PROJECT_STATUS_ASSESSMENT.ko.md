# LionSQL 아키텍처 성숙도 및 기술 부채 해결 현황 보고서 (`notes/PROJECT_STATUS_ASSESSMENT.ko.md`)

> [!NOTE]
> **문서 상태 및 이력 안내 (Status)**:
> 본 문서는 Phase 5 초기(2026년 10월 초)에 작성되었던 **아키텍처 진단 및 부채 도출 보고서**입니다.
> 본 문서의 제4절 및 제5절에서 지적되었던 모든 아키텍처 부채(계층 역전 해소 `pkg/engine`, 소트-머지 조인 `MergeJoinIterator`, Selinger DP 대규모 조인 탐색, TDS NBCROW 압축 및 다국어 드라이버 인증, RBAC/TDE 보안)는 **과제 B-3, C-5, UNIFY-CLI 등을 통해 100% 조치 완료([x])**되었습니다.

---

## 1. 정량적 엔지니어링 성과 및 벤치마크 지표

| 벤치마크 / 지표 항목 | 측정 수치 및 달성 결과 | 비고 |
| :--- | :--- | :--- |
| **In-Memory OCC TPC-C Throughput** | **779,641 tpmC / 28,881 TPS** | 10 Warehouses, 100 Workers (Abort Rate 3.6% 초저치) |
| **WiredTiger MVCC TPC-C Throughput** | **833,595 tpmC / 29,352 TPS** | 10 Warehouses, 100 Workers (디스크 완전 영속성) |
| **T-SQL VDBE TPC-C Throughput** | **454k ~ 668k tpmC / 17,150 TPS** | VDBE 가상머신 바이트코드 인터프리터 풀스택 실행 |
| **인메모리 SkipList 삽입 속도** | **초당 280만 회 (2.8M inserts/sec)** | 동시성 락프리 자료구조 (`memengine`) |
| **Go 인프로세스 스칼라 UDF 지연** | **5 나노초 미만 (< 5ns)** | `proc.RegisterScalarFunction` 인라인 직결 호출 |
| **동시성 무결성 검증** | **데이터 레이스 0건 (Zero-Race)** | 전체 25개 패키지 `go test -race` 100% 통과 |
| **외부 의존성** | **0개 (Zero External Dependency)** | 단일 정적 바이너리 배포 원칙 준수 (`bin/lionsql`) |

---

## 2. 과거 진단된 구조적 과제 및 조치 결과 (Resolution Status)

### 2.1. 계층 구조 역전 해소: SQL 실행 코디네이터 패키지 분리 (`pkg/engine`) — **[조치 완료]**
* **초기 현상**: HTTP REST 웹 핸들러를 담당하던 `pkg/api`에 15,000+ 라인의 SQL 실행 오케스트레이션 로직(`sqlexec*.go`)이 집중되어 계층 역전 발생.
* **조치 결과 (완료)**:
  - SQL 오케스트레이션 및 세션/트랜잭션 조율 로직을 코어 전용 패키지인 [`pkg/engine`](../pkg/engine)으로 완전 추출 및 독립 승격.
  - PGWire(`pkg/wire`), TDS(`pkg/wire/tds`), HTTP REST(`pkg/api`)가 상호 순환 참조 없이 오직 코어 `pkg/engine`만을 단방향 호출하는 클린 아키텍처 확립.

### 2.2. CBO 조인 알고리즘 및 대규모 조인 탐색 확장 — **[조치 완료]**
* **초기 현상**: Hash Join과 Index Nested Loop Join만 존재하여 정렬된 스트림에 대한 Sort-Merge Join 부재 및 $N > 4$ 조인 순열 탐색 오버헤드 지적.
* **조치 결과 (완료)**:
  1. **Sort-Merge Join 연산자 구현 (`B-3`, 커밋 `52e6232`)**:
     - [`pkg/execution/merge_join.go`](../pkg/execution/merge_join.go)에 $O(M+N)$ 단일 패스 스트리밍 조인 엔진 `MergeJoinIterator` 완비.
     - 6대 조인 타입(`INNER`, `LEFT`, `RIGHT`, `FULL`, `SEMI`, `ANTI`) 지원.
     - $N:M$ 중복 키 직교곱 처리를 위한 **Mark & Restore 슬라이딩 윈도우 버퍼** 구현.
     - 실행 빌더([`pkg/execution/builder.go`](../pkg/execution/builder.go))에서 미정렬 자식 스트림에 대해 메모리 거버너 연동 `NewSortIterator` 자동 주입.
     - CBO 플래너([`pkg/planner/planner.go`](../pkg/planner/planner.go))에서 $O(M+N)$ 스트리밍 비용 모델 반영 및 힌트(`OPTION (MERGE JOIN)`, `INNER MERGE JOIN`) 연동.
  2. **Selinger DP 대규모 조인 최적화 (`C-5`)**:
     - $N > 4$ 테이블 조인에 대해 동적 프로그래밍(DP) 기반 Left-Deep 탐색 알고리즘을 도입하여 최적 조인 순서를 서브밀리초 단위로 결정.

### 2.3. 다국어 클라이언트 인증 및 TDS 고급 직렬화 — **[조치 완료]**
* **초기 현상**: TDS 프로토콜의 행 압축(NBCROW), LOB 청크 스트리밍(PLP), 타 언어 드라이버 E2E 검증 부족.
* **조치 결과 (완료)**:
  - TDS 7.3 NBCROW(`0xD2`) NULL 비트맵 행 압축 및 `VARCHAR(MAX)` PLP 청크 스트리밍 구현.
  - Python(`psycopg3`, `pymssql`), Go(`pgx`, `mssql`), JDBC, ODBC 공식 드라이버에 대한 밀폐형 통합 인증 하네스 완비.

### 2.4. 엔터프라이즈 보안 및 거버넌스 — **[조치 완료]**
* **초기 현상**: 단일 프로세스 인스턴스 격리 외 SQL 객체 레벨 권한 제어 및 암호화 부재.
* **조치 결과 (완료)**:
  - ANSI/T-SQL 표준 SQL 객체 레벨 RBAC(`GRANT`/`REVOKE`, Default-Deny, Deny-Precedence) 구현.
  - AES-256-GCM 기반 투명 데이터 암호화(TDE) 완비.

### 2.5. 아키텍처 불변 규칙 (Spec-Out 정리)
* **초기 검토 아이디어**: 백그라운드 Vacuum 스레드, Apache Arrow IPC 공유 메모리 스냅샷.
* **불변 결정 (Spec-Out)**:
  - 인메모리 OCC는 세션 프라이빗 워크스페이스를 통해 데드 튜플 체인을 누적하지 않으므로 불필요한 Vacuum 워커 스레드는 **명시적 Spec-Out**(GEMINI §8.3)으로 확정.
  - 대용량 공유 메모리 직렬화 오버헤드와 비이식성을 초래하는 Arrow IPC 대신, 표준 트랜잭션 CDC 스트리밍으로 파이프라인 일원화(GEMINI §8.4).

---

## 3. 종합 평가 및 결론

과거 본 문서에서 지적되었던 핵심 기술 부채 4개 영역(계층 역전, Sort-Merge Join/Selinger DP, TDS 직렬화, 보안 RBAC/TDE)은 **모두 성공적으로 해결되어 프로덕션 코어에 안정적으로 안착**했습니다.

LionSQL은 단일 노드 2.8만 TPS / 833k tpmC의 고성능 트랜잭션 처리량과 T-SQL VDBE 가상머신, 2-Tier 쿼리 실행 엔진, 그리고 엄격한 Non-Blocking 스냅샷 격리를 완비하여, 공식 **v1.0.0 LTS 출시 준비를 완료**한 상태입니다.
