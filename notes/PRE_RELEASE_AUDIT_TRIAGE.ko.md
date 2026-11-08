# LionSQL 1.0 전수 감사 결과 공식 Triage(결함 분류) 보고서 (`notes/PRE_RELEASE_AUDIT_TRIAGE.ko.md`)

> - **최초 작성일**: 2026-11-07
> - **과제 식별 코드**: `AUDIT-TRIAGE-POST`
> - **참조 원천 SSOT**: [`notes/PRE_RELEASE_AUDIT_FINDINGS.ko.md`](PRE_RELEASE_AUDIT_FINDINGS.ko.md) (총 206건 결함 누적: 1단계 134건 + 2단계 72건)
> - **분류 원칙**: `[RULE:RadicalKISS]`, `[RULE:AtomicSaves]`, `[RULE:ExplicitError]`, `[RULE:ConcurrencySafety]`
> - **목적**: 1단계 광역 전수 감사(134건) 및 2단계 15대 핵심 코어 심층 감사(72건)로 발굴된 총 206건의 결함에 대해 1.0 릴리스 대비 우선순위 등급(P0/P1/P2/P3)을 확정하고, 1.0 GA 품질 게이트를 위한 TDD 패치 실행 계획을 수립함.

---

## 1. 전역 결함 분류 총괄 매트릭스 (Summary Matrix)

| 등급 | 정의 및 처리 방침 | 선별 건수 | 비중 | 1.0 릴리스 조치 |
| :---: | :--- | :---: | :---: | :--- |
| **P0 (Must Fix)** | **데이터 손상/유실, 서버 크래시/OOM, 보안 백도어, 카탈로그 영속화 누락**<br>상용 프로덕션 가동 시 즉각적인 장애나 데이터 재앙을 초래하는 결함 | **31건** | 15.0% | **1.0 릴리스 절대 필수 패치 (TDD 회귀 테스트 의무)** |
| **P1 (Should Fix)** | **표준 SQL 동작 불일치, 일반적 메모리 누수, 경계값 방어, 네트워크 락 해제**<br>운영 안정성에 유의미한 영향을 주나 치명적 데이터 파괴는 없는 결함 | **53건** | 25.7% | 1.0 안정화 패치 세트 편입 (시간 여건에 따라 순차 적용) |
| **P2 (Deferred)** | **실무 발생 가능성이 극히 희박한 극한의 엣지 케이스, 경미한 최적화**<br>복잡도 증가 대비 실익이 낮아 코드를 과도하게 비대하게 만들 우려가 있는 항목 | **97건** | 47.1% | **v1.1.0 마이너 릴리스로 이관 (`[RULE:RadicalKISS]`)** |
| **P3 (By-Design)** | **LionSQL 아키텍처 원칙(Invariants) 및 Fast-Fail 설계에 입각한 의도적 동작**<br>Spec-Out 결정에 따라 의도적으로 지원하지 않거나 바이너리 단순성을 지키는 항목 | **25건** | 12.1% | 버그가 아님을 문서에 명시하고 패치 제외 (No Action) |
| **합계** | **전체 전수 감사 발굴 결함 총계** | **206건** | **100.0%** | **P0 31건 우선 완치 후 1.0 GA 릴리스 선언** |

---

## 2. P0 (Must Fix, 1.0 필수 패치 30선) 상세 명세

아래 30건은 데이터베이스 엔진의 핵심 존립 근거인 **데이터 내구성(Durability), 무결성(Integrity), 보안(Security), 가용성(Availability)**을 위협하므로 1.0 출시 전 반드시 TDD(Test-Driven Development)로 완치되어야 합니다.

### 그룹 A: 스토리지, WAL 및 복구 코어 (8건)
1. **`P0-01` [FlatRecord 힙 오프셋 충돌 데이터 영구 오염]** [완료] (`flat_record.go:163-166, 267-283` / 결함 11-1)
   - 가변 힙 오프셋 `vOff`의 2번째 바이트가 `0xFF`일 때 디코더가 인라인 스몰 스트링으로 오인하여 데이터 영구 파괴되던 결함 완치 (`slot[7]` 태그/길이 전용 바이트 격리 및 SSO 7바이트 표준화).
2. **`P0-02` [스냅샷 동기화 시 FlatRecord 빈 로 대입 데이터 전량 증발]** [완료] (`table_data.go:292-307` / 결함 11-2)
   - `syncShardsToMapsLocked`에서 `entry.materialize(td)`를 호출하여 플랫 버퍼 데이터를 역직렬화 대입하도록 수정함으로써 디스크 스냅샷 및 체크포인트 동기화 시 데이터 유실 결함 완치.
3. **`P0-03` [WAL 재생 I/O 에러 시 정상 후속 레코드 영구 절삭 삭제]** [완료] (`wal.go:343-345, 377-379` / 결함 11-3)
   - 일시적 파일 읽기 I/O 오류 또는 중간 손상 발생 시 정상 후속 트랜잭션을 디스크에서 임의로 Truncate하던 결함 완치 (4-State MECE 분류, 물리적 파일 끝 입증 시에만 테일 절삭 허용 및 Fail-Closed 보호, Invariant ㉒ & ADR-027 확립).
4. **`P0-04` [ALTER TABLE ADD COLUMN 시 FlatRecord 기존 데이터 전량 파괴]** [완료] (`table_data.go:359-372` / 결함 11-4)
   - FlatRecord 모드에서 컬럼 추가 시 기존 행들을 구 스키마로 안전하게 역직렬화한 뒤 신규 컬럼 값을 덧붙여 새 레이아웃으로 재인코딩하도록 수정함으로써 기존 데이터 전량 증발 결함 완치.
5. **`P0-05` [파티션 키 갱신 시 `partMu` 미보호 동시 맵 쓰기 크래시]** [완료] (`table_data.go:335-357` / 결함 11-5)
   - `addPartitionKey`/`removePartitionKey` 내부 자체 락 캡슐화 및 `ddl.go:GetPartitions`/`DropPartition`의 `partMu` RLock/Lock 보호 적용으로 동시 트랜잭션 시 `concurrent map read and map write` 패닉 및 Data Race 완치.
6. **`P0-15` [WAL 아카이브 회전 중 신규 트랜잭션 Truncate 절삭 유실]** [완료] (`wal_archive.go:154-173` / 결함 9-1)
   - `RotateAndArchive` 실행 중 신규 트랜잭션 유입 시 `Truncate()`에 의해 잘려나가던 결함 완치 (`WALWriter.Rotate` 원자적 파일 교체 메서드 도입 및 동결 세그먼트 완전 격리 복사/회수 아키텍처 확립, TDD 회귀 테스트 통과).
7. **`P0-16` [백업 복원 실패 시 이전 상태 롤백 부재 반파 DB 방치]** [완료] (`restore.go:121-220` / 결함 9-2)
   - 복원 시작 전 기존 엔진 상태를 임시 파일에 무손실 스냅샷 격리하고 복원 실패 시 생성된 테이블 제거 및 원본 스냅샷 원자적 롤백 복원을 수행함으로써 반파 DB 방치 결함 완치 (`[RULE:AtomicSaves]`).
8. **`P0-17` [백업 복원 시 외래키(FK) 의존 위상 정렬 누락 복원 실패]** [완료] (`restore.go:121-140, 150-200` / 결함 9-3)
   - 외래키 참조 관계를 분석하여 Kahn's 알고리즘 기반 위상 정렬(Topological Sort)을 수행, 부모 ➔ 자식 순 생성 및 레코드 삽입, 자식 ➔ 부모 순 드롭을 보장하여 외래키 위반 복원 실패 완치 (`[RULE:RadicalKISS]`).

### 그룹 B: 보안, 인증 및 CDC 신뢰성 (6건)
9. **`P0-06` [빈 사용자(`normUser == ""`) 최고 관리자 권한 우회 백도어]** [완료] (`auth/rbac.go:380-383` / 결함 12-1)
   - 미인증/익명 세션에 대해 관리자 권한을 부여하여 모든 테이블 DML/DDL을 무제한 허용하는 치명적 보안 취약점.
10. **`P0-07` [다중 롤 검사 시 첫 번째 GRANT 승인으로 후속 DENY 무시]** [완료] (`auth/rbac.go:440-451` / 결함 12-2)
    - 롤 순회 중 GRANT가 먼저 발견되면 즉시 허용하여 후속 롤의 명시적 `DENY`를 무력화하는 보안 원칙 파괴.
11. **`P0-08` [Outbox 브로커 전송 실패 시 메시지 무음 폐기 At-Least-Once 파괴]** [완료] (`cdc/outbox.go:223-228` / 결함 12-3)
    - 외부 브로커 전송 실패 시 채널에서 이미 꺼내진 메시지를 재시도하지 않고 폐기하여 비즈니스 이벤트 영구 유실되던 결함 완치 (헤드오브라인 지수 백오프 무한 재시도 및 Backpressure Pause, ADR-026 확립).
12. **`P0-09` [보안 계정/권한 메타데이터 인메모리 방치 재부팅 시 증발]** [완료] (`auth/rbac.go:74-103, 220-253` / 결함 12-4)
    - `logins`, `dbUsers`, `grants`, `apiKeys`, `superusers`가 디스크에 영속화되지 않아 데몬 재시작 시 커스텀 보안 설정 전량 증발하던 결함 완치 (`auth.json` 원자적 세이브 및 부팅 시 자동 로드 복원, Rule 1 준수).
13. **`P0-10` [CDC 히스토리 재생 시 버퍼 오버플로 무음 드롭 데이터 갭 유발]** [완료] (`cdc/streamer.go:119-130` / 결함 12-5)
    - 과거 LSN 재생 시 수신 채널 버퍼 초과분을 무음 드롭(`default:`)하여 구독자에게 데이터 불일치 초래하던 결함 완치 (Lossless Bridge 2단계 핸드오버 및 무유실 순차 스트리밍, Rule 29 준수).
14. **`P0-31` [RLS 필터 괄호 미격리로 인한 테넌트 인젝션 보안 우회]** (`api/rls.go:115` / 결함 13-2)
    - `ApplyListRule`에서 테넌트 룰과 클라이언트 전달 필터를 문자열 단순 결합(`rule && clientFilter`)할 때 최상위 괄호 미격리로 인해 `?filter=status=active) || (1=1` 주입 시 타사 테넌트 레코드가 전량 유출되는 치명적 보안 취약점. AST 레벨의 `&AndFilter{}` 결합으로 완치 필요.

### 그룹 C: T-SQL 렉서, 파서 및 카탈로그 메타데이터 (6건)
14. **`P0-20` [AlterTrigger 영속화 누락 재부팅 시 DDL 수정사항 유실]** [완료] (`catalog/catalog.go:376-390` / 결함 7-1)
    - `AlterTrigger` 실행 시 `c.persistenceDir`에 대한 `persistObjectsLocked()` 호출을 연동하여 서버 재시작 시에도 수정된 트리거 DDL 정의가 100% 보존되도록 완치.
15. **`P0-21` [CreateStatistics 영속화 및 직렬화 필드 누락 통계 증발]** [완료] (`catalog/catalog.go:790-796` / 결함 7-2)
    - `CatalogObjects` 직렬화 구조체에 `Statistics []*StatisticDef` 필드를 추가하고 `CreateStatistics`/`DropStatistics` 시 `persistObjectsLocked()` 자동 동기화 및 부팅 시 자동 로드 복원 완치.
16. **`P0-22` [GetView 내부 포인터 직접 반환 동시 실행 시 Data Race]** [완료] (`catalog/catalog.go:752-761` / 결함 7-4)
    - `GetView` 및 `ListViews` 호출 시 내부 카탈로그 포인터 대신 방어적 복사본(`cp := *v`)을 반환하도록 수정하여 동시 쿼리 실행 시 Data Race 원천 차단 완치 (`[RULE:ConcurrencySafety]`).
17. **`P0-23` [DECIMAL/NUMERIC을 Float 매핑하여 금융 부동소수점 오차 유발]** [완료] (`proc/parser.go:255-280` / 결함 6-1)
    - 파서가 정밀 금융 타입을 `TypeFloat`으로 매핑하던 결함 완치 (Native/ANTLR4 파서에서 `DECIMAL`/`NUMERIC`/`MONEY`를 `TypeDecimal`로 분리 매핑 및 `Precision/Scale` 파싱 보존 연동 완료).
18. **`P0-24` [UTF-8 멀티바이트 비-ASCII 식별자 파편화]** [완료] (`proc/lexer.go:497-499` / 결함 6-2)
    - 바이트 단위 검사로 인해 한글/한자 식별자 첫 바이트가 깨지던 결함 완치 (`utf8.DecodeRuneInString` 기반 완전한 룬 렉서 개편 및 다국어 식별자 회귀 테스트 완료).
19. **`P0-25` [이항 연산자 오른쪽 파싱 실패 시 Nil Dereference 패닉]** [완료] (`proc/parser_expr.go:180-194` / 결함 6-3)
    - 표현식 파싱 실패 시 `&BinaryExpr{Right: nil}` 생성 차단, 파서 명시적 구문 에러 기록 및 `nil` 안전 반환으로 다운스트림 Nil Dereference 패닉 원천 차단 완치.

### 그룹 D: SQL 실행 엔진, CBO 플래너 및 조인/집계 오퍼레이터 (7건)
21. **`P0-11` [GROUP BY CUBE 컬럼 수 상한 미검사 $2^N$ 할당 OOM DoS]** [완료] (`execution/aggregate.go:538-545` / 결함 10-1)
    - `MaxCubeColumns = 12` (최대 4,096개 그룹 세트, MSSQL 표준 호환) 상한선 가드레일을 설치하고 12개 초과 시 `ErrTooManyCubeColumns` 에러를 즉시 반환하여 힙 고갈 프로세스 크래시 DoS 원천 차단 완치 (`[RULE:BoundedBuffers]`).
22. **`P0-12` [Window 함수 하위 스트림 무제한 힙 적재 OOM 크래시]** [완료] (`execution/window.go:42-53` / 결함 10-2)
    - `WindowIterator`에 `workMem` (기본 64MB, `DefaultWorkMem`) 및 `accumMemory` 실시간 튜플 크기 추적(`EstimateTupleSize`) 가드레일을 설치하고, 메모리 한도 초과 시 `ErrWindowWorkMemExceeded` 에러를 즉시 반환하여 힙 고갈 OOM 크래시 DoS 원천 차단 완치 (`[RULE:BoundedBuffers]`).
22. **`P0-13` [Merge Join 우측 중복 키 무제한 슬라이스 팽창 메모리 스톨]** (`execution/merge_join.go:335-356` / 결함 10-3)
    - 우측 동일 조인 키 대량 중복 시 버퍼가 기가바이트 단위로 팽창하여 GC 압박 및 엔진 스톨.
23. **`P0-14` [PullChunk I/O 에러 시 불완전 누적 그룹 부분 집계 왜곡 반환]** (`execution/aggregate.go:575-578` / 결함 10-4)
    - 스트리밍 I/O 읽기 에러 발생 시 에러 전파 대신 부분 데이터 집계 결과를 정상치로 반환하는 정합성 왜곡.
24. **`P0-18` [MERGE 문 타깃 테이블 무제한 풀스캔 힙 적재 OOM DoS]** (`engine/sqlexec_merge.go:106-114` / 결함 8-1)
    - 대용량 타깃 테이블에 대해 비인덱스 풀스캔 및 $O(M \times N)$ 비효율 루프로 인한 런타임 락업.
25. **`P0-19` [UPDATE/DELETE 스캔 중 I/O 에러 시 무음 탈출 부분 커밋 데이터 오염]** (`engine/sqlexec_dml.go:1128-1132` / 결함 8-2)
    - 중간 행 스캔 중 에러 발생 시 `break`로 빠져나가 앞부분 행들만 수정/삭제 커밋되어 데이터 정합성 파괴.
26. **`P0-28` [빈 테이블 유니크 인덱스 Zero-Division NaN 및 LEFT JOIN 보존]** [완료] (`planner/planner.go:1482, 1604`, `planner/pushdown.go:342-365` / 결함 4-1)
    - 빈 테이블/Zero 카디널리티 유니크 인덱스 비용 계산 시 `1.0 / 0.0`으로 인한 `+Inf` 및 `NaN` 전파 방어벽 구축, `math.IsNaN`으로 인한 인덱스 탈락 및 풀 테이블 스캔 왜곡 완치, 외부 조인(LEFT JOIN) 보존 무결성 검증 완료.

### 그룹 E: 가상머신, 와이어 프로토콜 및 동시성 트랜잭션 (4건)
27. **`P0-26` [네트워크 비정상 단절 시 트랜잭션 롤백 누락 락 영구 누수]** (`wire/server.go:178-195` / 결함 5-1)
    - 클라이언트 연결 끊김 시 열려 있던 활성 트랜잭션이 롤백되지 않고 방치되어 잠금 누수 발생.
28. **`P0-27` [TDS 패킷 길이 검증 누락 분할 슬라이스 범위 초과 패닉]** (`wire/tds/packet.go:88-102` / 결함 5-2)
    - 잘못된 헤더 길이 패킷 유입 시 경계 검사 없이 슬라이싱하여 서버 고루틴 패닉.
29. **`P0-29` [sync.Pool 레지스터 재활용 잔류 포인터 메모리 레이스]** (`proc/runtime/vm.go:112-124` / 결함 3-1)
    - VDBE VM 풀링 시 레지스터 포인터를 명시적으로 클리어하지 않아 다른 세션과 데이터 포인터 경합.
30. **`P0-30` [타임아웃 롤백 중 동시 커밋 시 고아 트랜잭션 방치]** (`engine/coordinator_impl.go:275-295` / 결함 2-1)
    - 타임아웃 감지 고루틴과 클라이언트 요청 고루틴의 상태 전이 레이스로 인해 트랜잭션 핸들 누수.

---

## 3. P1 / P2 / P3 분류 기준 및 대표 항목 요약

### P1 (Should Fix, 1.0 안정화 패치 세트 - 53건)
* **기존 주요 항목**:
  - `WITHOUT HOLD` 커서 트랜잭션 종료 시 자동 정리 누락 (`sqlexec_cursor.go:87-97`)
  - `MERGE` 구문 작은따옴표 이스케이프(`''`) 언이스케이프 누락 (`sqlexec_merge.go:364`)
  - 뷰(View) 수정 시 디스크 영속화 동기화 누락 (`catalog.go:340-355`)
  - 윈도우 프레임 경계 오프셋 슬라이스 인덱스 경계 방어 (`window.go:525-528`)
  - SkipList 복원 시 루프 내 반복 Lock 경합 완화 (`skiplist.go:325`)
* **신규 편입 3건 (구역 13~14 심층 감사)**:
  - `결함 13-1`: `GET /api/records/:table` 대용량 테이블 페이징 시 무제한 힙 적재 OOM DoS 방어 (1,000건 제한 상한 및 스트리밍 조기 탈출, `handlers_records.go:186-221`)
  - `결함 13-4`: SPA 빌드 서브디렉토리 에셋(`/assets/*.js`) 404 차단 해제 (경로 라우팅 화이트리스트 확장, `spa.go:35-37`)
  - `결함 14-1`: `CROSS/OUTER APPLY` 카르테시안 곱 힙 적재 OOM 방어 (10만 행 안전 상한선 가드레일, `sqlexec_apply.go:72-100`)
* **방침**: P0 31건 완치 후, 릴리스 안정성 확보를 위해 도메인별 일괄 보정 패치 세트로 반영.

### P2 (Deferred, v1.1.0 이관 - 97건)
* **기존 주요 항목**:
  - 극단적인 128단 중첩 서브쿼리 스택 방어
  - 10만 건 이상의 복합 IN-List 파싱 메모리 한계
  - 초당 1회 AOF fsync 정책 전환 시 고루틴 종료 동기화 지연
  - 극한의 네트워크 지연 환경에서의 TDS 패킷 정밀 타임아웃
* **신규 이관 5건 (구역 13~15 심층 감사)**:
  - `결함 13-3`: 웹훅/크론 메타데이터 디스크 미영속화 (BaaS 부가기능 편의 스펙, 코어 DB 무영향, `webhook.go:190`, `cron.go:88`)
  - `결함 14-2`: Volcano 서브쿼리 `ExecuteSessionSQL` 재귀 호출 시 트랜잭션 경합 (극단적 중첩 서브쿼리 마진, `sqlexec_select.go:136-148`)
  - `결함 14-3`: PIVOT 절 매칭 괄호 탐색 시 특수문자 오인식 엣지케이스 (`sqlexec_pivot.go:88-92`)
  - `결함 15-1`: 리더 선출 후 `broadcastLeaderAnnouncement` 고아 고루틴 방치 (페일오버 희귀 빈도 및 1회성 고루틴 메모리 무시 가능, `failover.go:192`)
  - `결함 15-2`: 데몬 기동 시 PID 파일 직접 덮어쓰기 비원자적 손상 위험 (상용 배포 systemd/Docker 관리로 실무 영향 제로, `daemon.go:207`)
* **방침**: 일반적인 상용 OLTP 및 TPC-C 부하에서 발생하지 않는 극단적 마진 및 부가 편의 항목이므로, 1.0 코드베이스의 복잡도를 억제(`[RULE:RadicalKISS]`)하기 위해 v1.1.0 로드맵으로 안전하게 이관.

### P3 (By-Design / Spec-Out - 25건 전수 목록)
버그가 아닌 LionSQL 엔진 아키텍처 불변 규칙([`GEMINI.md`](../GEMINI.md)) 및 의도적 Fast-Fail 설계에 따른 정상 동작 (No Action):
1. **PL/pgSQL 인터프리터 미지원**: T-SQL VDBE VM으로 단일 통합된 의도적 단일 엔진 아키텍처.
2. **2PL 비관적 잠금 (SELECT FOR UPDATE 레코드 락 체인) 미지원**: Non-blocking OCC/MVCC 단일 채택.
3. **READ UNCOMMITTED (더티 리드) 미지원**: ACID 데이터 정합성 보장을 위한 Non-blocking Read Committed/Snapshot 단일 강제.
4. **인메모리 OCC 백그라운드 진공 청소(Vacuum) 미지원**: 작업공간 원자적 스왑/폐기 방식으로 데드 튜플 체인이 발생하지 않아 불필요 (YAGNI).
5. **분산 직렬화 그래프 (SSI) 미지원**: 단일 노드 20,000+ TPS 수직 스케일업 우선주의.
6. **멀티 마스터 분산 샤딩 및 2PC 합의 (Raft/Paxos) 미지원**: 1-Writer Multi-Reader 비동기 WAL 복제 불변원칙.
7. **프로세스 내 복수 독립 데이터베이스 동시 마운트 미지원**: 프로세스당 단일 카탈로그 데이터 디렉터리 격리.
8. **가상 `USE <dbname>` 물리 카탈로그 분리 미지원**: SSMS/DBeaver 호환성을 위한 Tolerant Virtual No-Op 세션 동기화.
9. **사용자 정의 CGo 동적 플러그인(`.so`) 로딩 미지원**: 단일 정적 독립 실행형 단독 바이너리 배포 원칙 (`[RULE:StaticBinary]`).
10. **외부 사이드카(Redis, Nginx, PgBouncer, ZooKeeper) 연동 미지원**: 제로 사이드카 완전 자립형 단일 바이너리.
11. **복합 정규식 기반 SQL DDL/DML 파싱 미지원**: Context-Free 문법 파괴 방지를 위한 결정론적 2-Tier AST 단일 채택.
12. **인메모리 OCC B-Tree 분할 병합 인덱스 미지원**: 캐시 친화적 SkipList 인메모리 인덱스 단일 채택.
13. **비-ANSI T-SQL `GOTO` 라벨 점프 미지원**: 구조적 제어 흐름(`IF`, `WHILE`, `TRY...CATCH`) 단일 통합.
14. **Legacy XML DML / XQuery 미지원**: 현대적 JSON 함수(`ISJSON`, `JSON_VALUE`, `JSON_MODIFY`)로 단일 표준화.
15. **POSIX Shared Memory (Arrow IPC) 스냅샷 미지원**: 64MB 한계 및 OS 이식성 문제를 배제하고 CDC/WAL로 표준화.
16. **분산 2PC 코디네이터 (XA / MS DTC) 미지원**: 싱글 노드 극한 성능 보존 및 비동기 복제로 단순화.
17. **인커널 OAuth2 웹 리다이렉트 플로우 미지원**: 클라이언트 사이드 OIDC 토큰 교환 표준화로 인커널 보안 유출 원천 차단.
18. **인커널 Blob 스토리지 및 이미지 리사이징 미지원**: S3 호환 스토리지 및 Pre-signed URL 위임.
19. **미성숙 Pure-Go 스토리지 엔진(Pebble/bbolt) 미지원**: 검증된 WiredTiger MVCC 및 In-Memory OCC 듀얼 엔진 집중.
20. **인커널 실시간 슬로우 쿼리 웹훅(`OBS-3`) 미지원**: 외부 HTTP 지연을 배제하고 Prometheus(`/metrics`) 및 OTLP 표준화.
21. **인커널 Wasm UDF 샌드박스(`wazero`) 미지원**: 바이너리 비대화 방지 및 Go 네이티브 고속 UDF SPI 채택.
22. **복합 관계대수 전체 바이트코드 변환(`ARCH-2TIER-1`) 미지원**: 복합 쿼리는 Volcano CBO(Tier 2), 단순 OLTP는 VDBE(Tier 1) 2-Tier 분업.
23. **순수 컬럼형 벡터화 엔진 전면 재작성 미지원**: 1,024행 청크 스트리밍(`ChunkedIterator`) 최적화로 대체.
24. **MySQL 고유 문법(`ON DUPLICATE KEY UPDATE`, Comma `LIMIT offset, count`) 미지원**: PG `ON CONFLICT` 및 표준 `MERGE` 단일 지원.
25. **인메모리 Varint/Bit-Packing 비트 압축 미지원**: 0.5ns $O(1)$ 초고속 메모리 접근을 위한 Pointer-Free FlatRecord 단일 채택.

---

## 4. TDD 기반 P0 순차 패치 로드맵 (Execution Plan)

선별된 31건의 P0 결함은 다음과 같이 **5개 집중 패키지(Batch 1 ~ Batch 5)**로 분할하여 TDD(Test-Driven Development) 원칙에 따라 순차 격파합니다:

| 배치 번호 | 대상 도메인 | 포함 결함 (P0) | 핵심 목표 |
| :---: | :--- | :---: | :--- |
| **Batch 1** | **보안 및 접근 제어 (Security & RBAC)** | P0-06 ~ P0-10 (5건) [완료] | 빈 유저 백도어 차단, DENY 우선순위 확립, Outbox 메시지 유실 방지 |
| **Batch 2** | **스토리지 & 영속성 (Storage & WAL)** | P0-01 ~ P0-05 (5건) [완료] | FlatRecord 오프셋 충돌 해소, 스냅샷 빈 로 증발 방지, WAL 절삭 차단, ALTER TABLE 보존, 파티션 락 동시성 완치 |
| **Batch 3** | **백업, 복원 & 카탈로그 (Backup & Catalog)** | P0-15 ~ P0-17, P0-20 ~ P0-22 (6건) [완료] | 복원 트랜잭션 롤백, FK 위상 정렬 복원, 트리거/통계 영속화, 뷰 동시성 딥카피 |
| **Batch 4** | **T-SQL 렉서, 파서 & CBO (Parser & CBO)** | P0-23 ~ P0-25, P0-28 (4건) [완료] | DECIMAL 실수 오차 퇴치, UTF-8 식별자 보장, 유니크 인덱스 NaN 왜곡 차단, LEFT JOIN 보존 |
| **Batch 5** | **실행 오퍼레이터 & VM 런타임 & RLS 보안** | P0-11 ~ P0-14, P0-18 ~ P0-19, P0-26, P0-27, P0-29, P0-30, P0-31 (11건) | CUBE/Window OOM DoS 방어, Merge 풀스캔 해소, 와이어 락 누수 차단, RLS 테넌트 인젝션 차단 |

---

## 5. 결론 및 사인오프 기준

- 본 보고서 확정 즉시 `AUDIT-TRIAGE-POST`를 완료 처리하고, `AUDIT-PATCH-P0` 태스크를 가동합니다.
- **1.0 GA 사인오프 기준**: Batch 1부터 Batch 5까지 30건의 P0 결함에 대해 **100% Red ➔ Green 회귀 테스트 통과 및 `go test -race` 0건 검증 완료** 시 1.0 신뢰성 게이트 최종 통과를 선언합니다.
