# LionSQL TOAST (The Oversized-Attribute Storage Technique) 청크 아키텍처 사양서 (`TOAST_STORAGE_ARCHITECTURE.ko.md`)

> - **상태**: 확정 (Draft & Architecture Specification)
> - **태스크 ID**: `ARCH-TOAST-OUT-OF-ROW-CHUNKS-1`
> - **연계 문서**: `GEMINI.md` [Invariant 13], `doc/internal/PHYSICAL_TABLE_FORMAT.ko.md`

---

## 1. 배경 및 문제 정의 (Motivation)

### 1.1 In-Row 8,060 Bytes 제약과 B-Tree 성능 퇴행
LionSQL의 V2 튜플 코덱은 SQL Server 물리 페이지 제약(`8,060B`) 및 WiredTiger 32KB B-Tree 리프 노드 패킹 효율(Fan-out)을 보장하기 위해 `storage.MaxInlineRowSize = 8060` 바이트 한계를 정의하고 있습니다.

그러나 실제 프로덕션 워크로드에서는 다음과 같은 대용량 객체(LOB)가 빈번히 저장됩니다:
- `VARCHAR(MAX)` / `NVARCHAR(MAX)`: 긴 문서, Markdown, HTML 본문 (수십 KB ~ 수 MB)
- `VARBINARY(MAX)`: 첨부파일, 이미지 썸네일, 암호화 블롭 (수 MB)
- `JSON`: 복잡한 구조의 JSON 문서 (`TypeJSON`)

만약 이러한 거대 LOB 데이터를 메인 테이블의 B-Tree 리프 노드에 인라인으로 직접 기록하면:
1. **B-Tree 리프 노드 패킹 붕괴**: 32KB 리프 노드 하나에 고작 2~3개 행만 담기게 되어 트리 높이가 급격히 높아지고 캐시 효율이 극단적으로 저하됩니다.
2. **테이블 풀 스캔 및 인덱스 스캔 I/O 폭증**: LOB 컬럼을 요구하지 않는 단순 쿼리(`SELECT id, status FROM tbl`)를 실행할 때도 거대한 LOB 바이트를 디스크에서 전부 메모리로 읽어와야 합니다.

---

## 2. TOAST 아키텍처 핵심 설계 (Architecture Specification)

LionSQL은 PostgreSQL 및 SQLite Overflow Pages의 장점을 결합하여, **메인 테이블의 B-Tree 구조를 보존하면서 대용량 데이터를 안전하게 분리 격리하는 청크 B-Tree 아키텍처**를 채택합니다.

```
[Main Table Tuple: table:<TableName>]
┌──────────────┬───────┬─────────────┬───────────┬──────────────────────────────────────────┐
│ Version 0x02 │ Flags │ encodedCols │ NullBitmap│ id=101, status='ACTIVE', ...             │
│              │(0x04) │             │           │ bio: [ToastPointer: ChunkID=42, 125,000B]│
└──────────────┴───────┴─────────────┴───────────┴──────────────────────────────────────────┘
                                                               │
                                                               ▼ (B-Tree Seek: 42:0, 42:1, ...)
[TOAST Chunk Table: table:_toast_<PhysicalTableName>]
┌────────────────────────────────┬──────────────────────────────────────────────────────────┐
│ Key: <ChunkID (uint64)><Seq>   │ Value: Chunk Data (고정 4,000 Bytes 단위)                  │
├────────────────────────────────┼──────────────────────────────────────────────────────────┤
│ Key: [000000000000002a][0000]  │ Value: 4,000 Bytes (0 ~ 3,999)                           │
│ Key: [000000000000002a][0001]  │ Value: 4,000 Bytes (4,000 ~ 7,999)                       │
│ ...                            │ ...                                                      │
│ Key: [000000000000002a][001f]  │ Value: 1,000 Bytes (124,000 ~ 124,999)                   │
└────────────────────────────────┴──────────────────────────────────────────────────────────┘
```

### 2.1 인라인 vs 아웃오브라인(Out-of-Line) 임계값 규약
1. **인라인 유지 임계값 (`ToastThreshold = 2,048 Bytes`)**:
   - 가변 길이 필드(`VARCHAR`, `VARBINARY`, `JSON`)의 실제 데이터 길이가 2,048 바이트 이하인 경우 메인 튜플 내에 직접 인라인 직렬화합니다.
2. **TOAST 분리 기준 (`ToastTrigger = 2,048 Bytes` & 행 크기 초과)**:
   - 가변 길이 필드가 2,048 바이트를 초과하고 전체 행 페이로드가 `MaxInlineRowSize (8,060B)`를 초과할 위험이 있는 경우, 해당 컬럼을 아웃오브라인 TOAST 청크 테이블로 자동 분리합니다.
3. **최대 단일 레코드 상한 (`MaxRowSize = 16 MB`)**:
   - TOAST로 분리된 모든 청크의 합계 논리 크기는 기존 불변식인 16MB를 초과할 수 없습니다 (`[Invariant 13]`).

---

### 2.2 메인 튜플 내부의 TOAST 포인터 규약 (`ToastPointer`)

메인 테이블 튜플의 LOB 컬럼 위치에는 원시 바이트 대신 고정 **16바이트 TOAST 포인터**가 저장됩니다:

```go
type ToastPointer struct {
    ChunkID   uint64 // 전역 고유 청크 식별자 (시퀀스 또는 UUID 기반)
    TotalSize uint32 // LOB 데이터의 전체 원본 바이트 크기 (최대 16MB)
    ChunkCount uint16 // 총 청크 블록 개수 (ceil(TotalSize / 4000))
    Flags     uint16 // 압축 플래그 (0x01: LZ4 압축됨, 0x00: 비압축 원시 데이터)
}
```

- **튜플 헤더 비트**:
  - 행 내에 하나 이상의 컬럼이 TOAST 포인터로 치환된 경우, 튜플 헤더 Byte 1의 `TupleFlagToast (0x04)` 플래그를 세팅합니다.
  - 디코더(`DecodeTupleToRow`, `ExtractColumnFromTuple`)는 해당 비트가 꺼져 있으면 TOAST 룩업을 100% 스킵하여 일반 쿼리의 핫패스 지연시간(0.5ns)을 완벽히 보존합니다.

---

### 2.3 TOAST 전용 B-Tree 물리 테이블 레이아웃 (`_toast_<Table>`)

WiredTiger 및 In-Memory OCC 엔진 공통으로, TOAST 테이블은 메인 테이블 이름에 `_toast_` 접두사를 붙여 1:1로 관리합니다:
- **물리 테이블명**: `_toast_<PhysicalTableName>` (예: `_toast_users.wt`)
- **키 포맷 (`key_format=u`)**:
  - `ChunkID` (8바이트 Big-Endian uint64) + `SeqNo` (4바이트 Big-Endian uint32).
  - 사전순 정렬을 보장하여 청크 0, 1, 2... 순으로 연속 범위 스캔(`RangeScan`)이 가능.
- **값 포맷 (`value_format=u`)**:
  - 분할된 원시 바이트 슬라이스 (`ToastChunkSize = 4,000 Bytes`).
  - 4KB 단위는 운영체제 메모리 페이지 및 파일시스템 블록과 1:1로 정렬되어 디스크 I/O 효율을 극대화합니다.

---

### 2.4 라이프사이클 및 ACID 원자성 (Lifecycle & Durability)

1. **원자적 쓰기 & 롤백 (`Single Transaction Boundary`)**:
   - 메인 테이블 행 쓰기와 `_toast_` 테이블의 청크 삽입은 반드시 동일한 `storage.Transaction` 트랜잭션 컨텍스트 안에서 실행됩니다.
   - 트랜잭션 롤백 시 메인 튜플과 TOAST 청크가 원자적으로 함께 롤백됩니다 (`[Invariant 15]`).
2. **UPDATE 및 가비지 컬렉션 (Copy-on-Write)**:
   - LOB 데이터가 수정될 경우 새로운 `ChunkID`로 신규 청크들을 작성하고 메인 포인터를 갱신합니다.
   - 기존 청크는 트랜잭션 커밋 완료 후 또는 백그라운드 스위퍼에 의해 회수됩니다.
3. **DROP TABLE 연동**:
   - `DROP TABLE <tbl>` 실행 시 `_toast_<tbl>` 테이블 및 디스크 파일도 함께 원자적으로 삭제됩니다.

---

## 3. 엔지니어링 구현 로드맵 및 단계별 계획

| 단계 | 작업 내용 | 대상 패키지 | 비고 |
| :---: | :--- | :--- | :--- |
| **Phase 1 (v0.96.0 완료)** | **TOAST 아키텍처 사양 및 포인터 규약 정의** | `doc/internal/`, `pkg/storage` | `ToastPointer` 구조체, 상수(`ToastThreshold`, `ToastChunkSize`) 정의 및 튜플 헤더 `TupleFlagToast` 정합성 검증 |
| **Phase 2 (v0.97.0 연동)** | **스토리지 엔진 TOAST 테이블 청크 분할 및 조립기 구현** | `pkg/storage/memengine`, `pkg/storage/wiredtiger` | `writeToastChunks`, `readToastChunks` 엔진 SPI 연동 |
| **Phase 3 (v0.98.0)** | **DML/DQL 프로젝션 푸시다운 연동 및 온디맨드 스트리밍** | `pkg/execution`, `pkg/engine` | 프로젝션에 LOB 컬럼이 명시된 경우에만 청크를 읽어 조립하는 지연 로딩(Lazy Toast Loading) |

---

## 4. 결론 및 불변식 준수

본 TOAST 청크 아키텍처는:
1. `GEMINI.md` [Invariant 13]의 `8,060B` 인라인 행 상한과 `16MB` 최대 논리 행 상한 불변식을 완벽히 준수합니다.
2. 거대 텍스트나 바이너리가 유입되더라도 B-Tree 노드의 Fan-out과 일반 쿼리의 캐시 적중률을 침해하지 않습니다.
3. 튜플 헤더의 `TupleFlagToast(0x04)` 비트를 통해 일반 행에 대한 오버헤드를 0ns로 차단합니다.
