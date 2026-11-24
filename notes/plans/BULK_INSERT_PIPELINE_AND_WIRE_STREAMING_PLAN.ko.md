# Bulk Insert Pipeline & Dual-Protocol Streaming Architecture Plan
(`notes/plans/BULK_INSERT_PIPELINE_AND_WIRE_STREAMING_PLAN.ko.md`)

## 1. 아키텍처 비전 및 범위 (Scope & Objectives)
LionSQL은 단일 정적 바이너리 내에서 PostgreSQL과 Microsoft SQL Server의 대용량 데이터 적재(Bulk Ingestion)를 지원합니다.
본 계획서는 3대 벌크 인서트 파이프라인을 체계적으로 완성하여 1.0-GA 이전 대량 데이터 적재 기능을 100% 완성하는 마스터플랜입니다:

1. **`PERF-BULK-INSERT-PIPELINE-1`**: 스토리지 계층 `BatchInsertRows` 파이프라인 및 멀티로우 `INSERT INTO ... VALUES (...), (...)` 배치 인덱싱 가속.
2. **`FEAT-WIRE-PG-COPY-STREAM-1`**: PGWire 대량 `COPY FROM STDIN / TO STDOUT` 스트리밍 고속화 (`psql \copy`, `pg_dump`, `pgx.CopyFrom`).
3. **`FEAT-WIRE-TDS-BCP-BULK-LOAD-1`**: TDS `0x07 BCP (Bulk Load Protocol)` 스트리밍 적재 지원 (`bcp.exe`, .NET `SqlBulkCopy`, SSMS 대량 가져오기).

---

## 2. 3대 인터페이스와 스토리지 코어 연계도

```
 [PostgreSQL 생태계]                [표준 SQL / T-SQL]                [SQL Server 생태계]
  psql \copy / pg_dump           INSERT INTO t VALUES (..)          bcp.exe / SqlBulkCopy
         │                                   │                                 │
         ▼                                   ▼                                 ▼
   [PGWire :5433]                     [SQL Exec DML]                     [TDS :1433]
  COPY FROM STDIN                     sqlexec_dml.go                   0x07 BCP Packet
         │                                   │                                 │
         └─────────────────┬─────────────────┴─────────────────────────────────┘
                           │
                           ▼ (배치 청크 슬라이스: []storage.Row)
             ┌───────────────────────────────────────────┐
             │   tx.BatchInsertRows(tableName, rows)     │
             │   (pkg/storage: memengine & wiredtiger)   │
             └─────────────────────┬─────────────────────┘
                                   │
         ┌─────────────────────────┴─────────────────────────┐
         ▼                                                   ▼
[Primary B-Tree 연속 스트리밍]                 [Secondary Indexes 사전 정렬 배치 주입]
- Primary Cursor 1회 획득                       - 각 인덱스별 (IndexKey, PK) 메모리 수집
- IDENTITY/PK 순차 리프 노드 패킹                - IndexKey 순으로 in-memory 정렬 (sort.Slice)
- B-Tree 탐색/루트 경합 0                       - 인덱스 커서 1회 열어 정렬 순서대로 순차 적재
```

---

## 3. 태스크별 상세 설계

### Phase 1: `PERF-BULK-INSERT-PIPELINE-1` (Storage 계층 배치 인덱싱)
1. **`storage.Transaction` 인터페이스 확장**:
   - `BatchInsertRows(tableName string, rows []Row) error`
   - `BatchInsert(tableName string, records []Record) error`
2. **`WTTx.BatchInsertRows` (WiredTiger 엔진)**:
   - 기본 테이블 커서 1회 획득 후 배치 전체를 순차 삽입.
   - 각 보조 인덱스(`tbl.Indexes`)별로 `(indexKey, pk)` 슬라이스 수집 후 메모리 정렬(`storage.CompareIndexKeys`).
   - 정렬된 키를 인덱스 커서로 $O(1)$ 리프 전진 삽입하여 B-Tree 페이지 스플릿 억제 및 탐색 오버헤드 $O(N \log K) \to O(N)$ 단축.
3. **`memengine.Transaction.BatchInsertRows` (In-Memory OCC 엔진)**:
   - Primary Map / SkipList 순차 적재 및 보조 인덱스 정렬 배치 삽입.
4. **`sqlexec_dml.go` 연동**:
   - `INSERT INTO ... VALUES` 멀티로우 파싱 시 단건 루프를 `tx.BatchInsertRows`로 전환.

### Phase 2: `FEAT-WIRE-PG-COPY-STREAM-1` (PGWire 대용량 스트리밍)
1. **PGWire `CopyData ('d')` 청크 디코딩**:
   - 64KB 단위 버퍼 스트리밍 및 빠른 탭/쉼표 구분자 파싱 (`splitCopyRow`).
2. **배치 청킹 버퍼 (ChunkSize = 2,000행)**:
   - 2,000행마다 `tx.BatchInsertRows`를 호출하여 메모리 사용량 상한 보장 (`[RULE:BoundedBuffers]`).
3. **`COPY ... TO STDOUT` 고속 직렬화**:
   - 테이블 커서를 통해 버퍼 단위로 `CopyData` 메시지 스트리밍 전송.

### Phase 3: `FEAT-WIRE-TDS-BCP-BULK-LOAD-1` (TDS 0x07 BCP 패킷 적재)
1. **TDS 0x07 패킷 디스패처**:
   - TDS 헤더 `PacketType = 0x07 (TDS_BULK)` 수신 핸들러 구현.
2. **BCP 메타데이터 및 토큰 파싱**:
   - `COLMETADATA` 토큰 파싱 후 로우 토큰(`0xD1` / `0xD2`) 스트림 디코딩.
3. **스토리지 직결**:
   - 디코딩된 바이너리 행들을 `tx.BatchInsertRows`로 파이프라이닝.
   - BCP 완료 시 `DONE` 토큰(`0xFD`) 및 적재 행 수 반환.

---

## 4. 완료 기준 및 검증 계획
1. **단위 및 통합 테스트**:
   - `TestStorage_BatchInsertRows_WiredTiger_And_MemEngine` (보조 인덱스 3개 이상 환경에서 10,000건 정합성 검증).
   - `TestWire_PG_CopyStream_E2E` (`psql \copy` 시뮬레이션 및 데이터 무결성 검증).
   - `TestWire_TDS_BCP_BulkLoad_E2E` (TDS 0x07 바이너리 스트림 적재 및 100% 무결성 검증).
2. **성능 게이트 검증**:
   - 10,000행 일괄 삽입 시 기존 대비 300% 이상 처리량(TPS) 향상 및 할당량 절감.
3. **파이프라인 통과 (`[RULE:PipelineVerification]`)**:
   - Build ➔ Test (`-race` 0 data races) ➔ Commit.
