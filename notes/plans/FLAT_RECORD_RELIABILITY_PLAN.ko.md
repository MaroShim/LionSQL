# OCC-HYPER-2 Flat Record Reliability Hardening Plan (`FLAT_RECORD_RELIABILITY_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-06
> - **대상 과제**: `REFACTOR-OCC-FLAT-1` (Flat Record 내부 안정성 및 동시성 결함 보강)
> - **관련 상위 태스크**: `OCC-HYPER-2` (In-Memory OCC Pointer-Free Flat Record)
> - **적용 불변식**: 
>   - `[RULE:LockHierarchy]` & `[RULE:ConcurrencySafety]` (동시성 락 계층 및 Data Race 원천 차단)
>   - `[RULE:ExplicitError]` (디코딩 에러 무음 처리 방지 및 명시적 에러 전파)
>   - `[RULE:ModularAbstraction]` (Rule of Two: 반복 분기 단일 메서드 `materializeRow` 추상화)
>   - `[RULE:PipelineVerification]` (Build ➔ Test ➔ Commit 3단계 검증)

---

## 1. 개요 및 배경

`OCC-HYPER-2` 구현 후 정밀 코드 리뷰를 통해 발견된 3대 결함을 보강하여, 신뢰성 검증 게이트(`TEST-OCC-FLAT-1~4`) 착수 전 Flat 모드의 코어 완성도를 엔터프라이즈 수준으로 격상합니다:

1. **A-2. `td.layout` 지연 초기화 Race Condition 원천 제거**:
   - 기존: 개별 샤드 락(`sh.mu.Lock()`)만 잡은 상태에서 `if td.layout == nil { td.layout = ... }` 호출. 다중 샤드 동시 쓰기 시 `td.layout` 할당 경쟁(Data Race) 발생 위험.
   - 개선: `CreateTable` 및 테이블 생성/로드 시점에 `td.layout`을 불변(Immutable) 객체로 사전 빌드하여 지연 초기화 및 런타임 레이스 완전 제거.
2. **A-1. 디코딩/인코딩 에러 무음 처리 제거 (`[RULE:ExplicitError]`)**:
   - 기존: `DecodeFlatToRow` 실패 시 빈 행을 반환하거나, 인코딩 실패 시 조용히 raw 행으로 폴백.
   - 개선: 디코딩 실패 시 에러를 상위 호출자로 즉시 전파하여 침묵의 데이터 왜곡 방지. 인코딩 폴백 시 로깅 및 명시적 에러 처리.
3. **A-3. 반복 분기 로직 추상화 (`[RULE:ModularAbstraction]`)**:
   - 기존: 5개 이상의 호출 지점에 `if td.mode == OCCModeFlat && len(entry.flatBuf) > 0 && len(entry.row.Values) == 0` 중복 산재.
   - 개선: `shardEntry.materialize(td *TableData) (storage.Row, error)` 단일 헬퍼 메서드로 통합하여 단일 책임 원칙(SRP) 준수.

---

## 2. 상세 작업 항목

### Task 1: `shardEntry.materialize` 헬퍼 및 에러 전파 구현
- `pkg/storage/memengine/table_data.go`에 `materialize(td *TableData) (storage.Row, error)` 정의.
- `getRowAndVer`, `deleteRowAndVer`, `forEachRow` 등에서 `materialize`를 호출하도록 리팩토링.
- 디코딩 에러 발생 시 명시적으로 에러 반환 또는 호출부 전파.

### Task 2: `TableData.layout` 불변 사전 초기화 (Data Race 제거)
- `pkg/storage/memengine/ddl.go` (`CreateTable`) 및 초기화 경로에서 `td.mode == OCCModeFlat`일 경우 `td.layout = BuildFlatRecordLayout(&td.Schema)` 선행 실행.
- `tx.go` 및 `table_data.go` 쓰기 경로의 `if td.layout == nil` 지연 초기화 블록 전면 제거.

### Task 3: 트랜잭션 커밋 경로(`tx.go`) 정리 및 인코딩 가시화
- `tx.go`의 `changeInsert`, `changeUpdate` 경로에서 `td.layout.EncodeRowToFlat` 호출 로직을 정리하고 비정상 인코딩 실패 시 명시적 에러 반환.

### Task 4: 동시성 Race Detector 및 경계 검증 테스트 추가
- `pkg/storage/memengine/flat_record_test.go`에 다중 고루틴 동시 쓰기/읽기 레이스 검증 테스트(`TestFlatRecord_ConcurrentReadWriteRace`) 추가 (`go test -race` 검증).
- 손상된 버퍼 디코딩 시 에러 반환 검증 테스트 추가.

---

## 3. 검증 기준 (Definition of Done)
1. `go build -tags wiredtiger -buildvcs=false ./...` 컴파일 성공
2. `go test -tags wiredtiger -count=1 -v -race ./pkg/storage/memengine` 100% 통과 (0 Data Race)
3. 모든 모드 분기 중복 제거 및 지연 초기화 코드 0건 달성
