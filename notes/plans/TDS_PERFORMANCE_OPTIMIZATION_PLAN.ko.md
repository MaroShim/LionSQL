# TDS 프로토콜 성능 최적화 아키텍처 설계 계획서 (`PERF-TDS-OPTIMIZATION-1`)

## 1. 개요 및 배경
- **현상**: 동일 워크로드(TPC-C 32 Workers, 16 Wh) 기준, 네트워크 클라이언트 벤치마크에서 TDS(:1433)가 PGWire(:5433) 대비 In-Memory OCC에서 약 20%, WiredTiger에서 약 54% 낮은 처리량을 기록.
  - PGWire: 376,538 tpmC (13,927 TPS) on In-Memory OCC / 352,568 tpmC (13,018 TPS) on WiredTiger.
  - TDS: 313,999 tpmC (11,536 TPS) on In-Memory OCC / 228,680 tpmC (8,465 TPS) on WiredTiger.
- **원인 분석**:
  1. `sp_executesql` (RPC 10) 파라미터화 쿼리 실행 시, VDBE PlanCache를 타지 않고 문자열 역포맷팅(`formatSQLValue`) 및 문자열 치환(`substituteParamsSinglePass`) 후 매번 SQL 렉서/파서/컴파일러를 재실행.
  2. 쿼리 결과 반환(`respondExecutionResult`) 시 매번 `s.inferColumnInfos`와 `BuildColMetaData`를 호출하여 전체 컬럼 문자열 분석, 샘플 로우 스캔, UTF-16LE 재인코딩(힙 할당)을 반복.
  3. TDS 프레임 송신(`writeFrame`) 시 개별 `net.Conn.Write` 시스템 콜이 발생하여 TCP 패킷 통합 및 버퍼링이 부재.

## 2. 3단계 최적화 실행 계획

### Phase 1: `sp_executesql` VDBE Fast-Path 직결
- **대상**: `pkg/wire/tds/server.go` (`handleRPCExecuteSQL`)
- **구현 내용**:
  1. `stmt` 쿼리 텍스트를 `s.BytecodePlanCache().GetOrCompile(stmt)`로 즉시 조회/컴파일.
  2. `paramsDecl`과 `boundParams`를 파싱하여 VDBE `paramMap` (`map[string]any`)에 직접 바인딩.
  3. `s.executeVDBEPreparedFastPath`를 직접 호출하여 SQL 파서/컴파일러 및 문자열 치환을 100% 우회.
  4. 컴파일 실패 또는 복합 동적 배치일 경우에만 기존 `buildBoundSQL` + `executeRPCAndRespond`로 정상 폴백.

### Phase 2: `COLMETADATA` 토큰 및 ColumnInfo 캐싱
- **대상**: `pkg/wire/tds/server.go`, `token.go`, `row_stream.go`
- **구현 내용**:
  1. `TdsPreparedStatement` 및 `proc.Program` 메타데이터에 `CachedColMeta []ColumnInfo` 및 `CachedColMetadataBytes []byte` 슬라이스 필드 추가.
  2. 첫 쿼리 실행 시 추론된 메타데이터 바이트를 원자적/스레드-세이프하게 캐싱.
  3. 후속 실행 시 `inferColumnInfos` 및 `BuildColMetaData`를 완전히 건너뛰고 캐시된 바이트를 `$O(1)$` 복사.

### Phase 3: TDS 송신 스트림 Pooled `bufio.Writer` 버퍼링
- **대상**: `pkg/wire/tds/row_stream.go` (`tdsStreamWriter`)
- **구현 내용**:
  1. PGWire의 `getBufioWriter` / `putBufioWriter` 풀링 패턴을 TDS 세션 및 스트림 라이터에 결합.
  2. RPC 및 배치 실행 완료 시까지 버퍼에 패킷 프레임들을 누적하고 마지막 `Close()` 시 1회 일괄 `Flush()` 수행.
  3. OS 소켓 시스템 콜 호출 횟수를 대폭 축소하고 TCP 지연 해소.

## 3. 검증 계획
1. `go test -v -race ./pkg/wire/tds/...`: 기존 TDS 유닛/통합/SSMS/드라이버 호환성 테스트 100% PASS 확인.
2. `cmd/tpcc_client`를 통한 TDS 벤치마크 처리량(tpmC/TPS) 및 CPU 프로파일 전후 비교 검증.
