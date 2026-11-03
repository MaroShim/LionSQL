# LionSQL v0.90.0-beta3 릴리즈 노트 (`notes/RELEASE_NOTES_v0.90.0-beta3.ko.md`)

> - **릴리즈 일자**: 2026-10-25
> - **버전**: `v0.90.0-beta3`
> - **마일스톤 주제**: **인메모리 OCC 100만 tpmC 돌파, Windows 네이티브 데몬 1ms 고해상도 타이머 탑재 및 웹 스튜디오 동시성 격리**

---

## 1. 릴리즈 하이라이트 개요

LionSQL `v0.90.0-beta3`은 **In-Memory OCC 스토리지 엔진이 역사적인 1,000,000 tpmC 장벽을 돌파(1.04M~1.44M tpmC / 39k~53k TPS)**하고, **1ms 고해상도 멀티미디어 타이머가 내장된 순수 Go 기반 Windows 네이티브 데몬**, **브라우저 탭 격리 웹 스튜디오 동시성**, 그리고 실시간 네트워크 **TPC-C 전용 벤치마크 클라이언트(`bin/tpcc-client`)**를 공식 도입한 기념비적인 성능 도약 릴리즈입니다.

- **초고성능 In-Memory OCC 100만 tpmC 돌파 (`VPERF-19` ~ `VPERF-22`)**:
  - 256개 샤드 Go 네이티브 해시 맵 아키텍처를 복원하여 수백만 건 행(`order_line`)에서의 포인터 체이싱 병목을 제거.
  - 스토리지 커널 실측 **1,046,121 tpmC (39,020 TPS)** sustained 달성 및 네이티브 T-SQL VDBE 런타임 **603,241 tpmC (22,294 TPS)** (중단율 0.08%).
  - 커널 피크 **1,442,347 tpmC (53,794 TPS)** (WiredTiger 대비 +73% 압도) 및 계좌 이체 **210,934 TPS** 달성.
- **1ms 고해상도 타이머 내장 Windows 네이티브 데몬 (`CLI-WINDOWS-DAEMON-1`)**:
  - CGo 의존성이 전혀 없는 순수 Go 기반 Windows 실행 파일(`bin/lionsql.exe`, `bin/lionsql-arm64.exe`) 크로스 컴파일 지원.
  - `winmm.dll`의 `timeBeginPeriod(1)`을 연동하여 15.6ms OS 스케줄러 슬립 지연을 1ms로 극복.
- **웹 스튜디오 브라우저 탭 세션 격리 & 쿼리 취소 (`UI-CONCURRENCY-1`)**:
  - 탭별 고유 세션 ID(`sessionStorage`)를 바인딩하여 복수 탭 간 인터랙티브 트랜잭션(`BEGIN TRAN`) 및 세션 변수 충돌 완전 차단.
  - 실시간 쿼리 취소 시그널링(`POST /api/query/cancel`) 및 대시보드 폴링 시 STW를 방지하는 1초 메모리 통계 캐싱 탑재.
- **실시간 네트워크 TPC-C 벤치마크 클라이언트 (`BENCH-TPCC-CLIENT-1`)**:
  - TDS(:1433) 및 PGWire(:5433) 소켓을 통해 다중 워커 트랜잭션을 실시간 발생시키는 독립 벤치마크 CLI(`bin/tpcc-client`) 구축.
- **13대 코어 아키텍처 도메인 통합 스모크 테스트 자동화 (`SMOKE-EXT-1`)**:
  - 단 3초 만에 13대 코어 아키텍처 도메인을 전수 검증하는 자체 검증 스크립트(`scripts/verify_report.sh`) 내장.

---

## 2. 세부 변경 사항

### 2.1. In-Memory OCC 초고성능 엔진 아키텍처
- **256 샤드 파티션 병렬성 (`table_data.go`)**: 샤드 파티션을 256개로 확장하고 32바이트 캐시라인 패딩을 적용하여 100 워커 하의 락 경합을 70% 이상 절감.
- **제로 할당 행 변경 패스트패스**: 무인덱스 테이블 대상 중간 `Record` 맵 할당을 우회하는 직접 `Row` 변경 경로 도입.
- **선택적 컬럼 무할당 추출 (`pkg/storage/tuple.go`)**: 압축 바이너리 튜플에서 25.55 ns/op (0 B/op, 0 allocs/op)의 속도로 개별 컬럼 직접 추출 (전체 디코딩 대비 9배 가속).
- **소정수 키 인터닝**: `0..100000` 정수에 대한 사전 계산 문자열 캐시 구축으로 기본키 해석을 나노초 단위 무할당으로 단축.
- **비트마스크 샤드 락 순서화 (`tx.go`)**: 256비트 비트마스크(`[4]uint64`)를 적용하여 힙 할당 없이 카노니컬 오름차순 락 획득 강제.

### 2.2. Windows 플랫폼 동등성 및 네이티브 데몬
- 백그라운드 프로세스 분리를 위한 `CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS` 적용.
- Windows 프로세스 검사(`OpenProcess`, `GetExitCodeProcess`) 및 안전 종료 구현.
- AMD64 및 ARM64 듀얼 아키텍처 지원.

### 2.3. 웹 스튜디오 다중 탭 동시성
- 탭별 독립 세션으로 복수 개발자 창 간 예기치 않은 커밋/롤백 방지.
- `/api/overview`에 1초 TTL `memStatsCache`를 도입하여 실시간 대시보드 폴링 중 `runtime.ReadMemStats` 일시 중단 제거.

### 2.4. 안전성 및 가상 카탈로그 하드닝
- **벤치마크 기본 디렉터리 보호 (`BENCH-DIR-SAFETY-1`)**: 파괴적인 `os.RemoveAll` 제거, 독립 임시 하위 디렉터리(`os.MkdirTemp`)로 정리 범위 격리.
- **클라이언트 GUI 도구 감사 (`TOOL-AUDIT-1`)**: DbGate, DBeaver, SSMS 호환성을 위한 스네이크/카멜케이스 듀얼 메타데이터 정규화.

---

## 3. 검증 지표 및 벤치마크 스코어카드

| 검증 영역 | 벤치마크 / 대상 | 주요 실측 지표 | 비고 |
| :--- | :--- | :--- | :--- |
| **커널 OLTP** | In-Memory OCC 커널 | **1,046,121 tpmC (39,020 TPS)** | 256 샤드 네이티브 맵 복원 (VPERF-22) |
| **VDBE OLTP** | TPC-C VDBE 런타임 | **603,241 tpmC (22,294 TPS)** | 16 Wh / 32 워커, 중단율 0.08% |
| **계좌 이체 스트레스** | TransferFunds | **210,934 TPS** | 5만 건 트랜잭션 100% ACID 검증 |
| **Windows 데몬** | `lionsql.exe` | 1ms 고해상도 타이머 | 순수 Go CGo 제로 정적 빌드 |
| **스모크 테스트** | 13대 아키텍처 도메인 | ~3초 만에 100% PASS | 완전 자동화 스크립트 |
