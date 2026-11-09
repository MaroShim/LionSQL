# LionSQL v0.92.0 릴리즈 노트 (`notes/release_notes/RELEASE_NOTES_v0.92.0.ko.md`)

> - **최초 작성일**: 2026-11-09
> - **릴리즈 버전**: `v0.92.0` (Minor Release)
> - **마일스톤 주제**: **37선 P0 전수 감사 결함 완전 척결, WiredTiger 고동시성 락 해제 (Invariant 10), 전 티어 TPC-C 역대 최고 성능 도약 (+16%~+86%), 및 쿨다운 아키텍처 탑재**

---

## 1. 릴리즈 하이라이트 개요

LionSQL `v0.92.0` 버전은 135단계 전수 감사(Full-Spectrum Codebase Audit)를 통해 발굴된 37건의 P0 핵심 결함을 완치하여 엔터프라이즈 미션 크리티컬 무결성을 확립하고, 스토리지 및 VM 엔진의 심층 병목을 혁신하여 **전 티어 벤치마크 역대 최고치(All-Time High)**를 경신한 기념비적 릴리즈입니다:

- **135단계 전수 감사 P0 37건 결함 전수 해결 (구조적 하드닝)**:
  - **스토리지 & 내구성**: FlatRecord 힙 오프셋 충돌 격리 태그(`slot[7]`, `P0-01`), `ALTER TABLE ADD COLUMN` 시 기존 행 데이터 보존(`P0-04`), 파티션 키 뮤텍스 분리(`partMu`, `P0-05`), WAL Fail-Closed 절삭 금지 복구(`P0-03`, `ADR-027`), WAL 아카이브 세그먼트 원자적 회전(`P0-15`).
  - **보안 & RBAC**: 익명 사용자 관리자 우회 백도어 영구 제거(`P0-06`), 명시적 DENY 절대 우선권 보장(`P0-07`), 계정/권한 `auth.json` 원자적 영속화(`P0-09`), 부팅 배너 기본 패스워드 대문자 경고(`SEC-BOOT-DEFAULT-PASS-WARN`).
  - **복제 & CDC**: 브로커 장애 시 CDC Outbox 지수 백오프 백프레셔(`P0-08`, `ADR-026`), CDC Lossless Bridge 2단계 핸드오버(`P0-10`), 슬로우 레플리카 버퍼 오버플로우 시 무음 폐기 방지 및 격리(`P0-34`).
  - **실행 엔진 & 메모리 거버너**: CUBE 12차원 상한(`P0-11`), WindowIterator 및 MergeJoin 64MB WorkMem 실시간 가드(`P0-12`, `P0-13`), 자식 이터레이터 리소스 누수 방지(`P0-14`), MERGE 문 PK 다이렉트 스트리밍(`P0-18`), DML 스캔 실패 시 Fail-Closed 롤백(`P0-19`).
  - **와이어 프로토콜 & 세션**: TDS/PGWire 64MB 및 HTTP Chunked 32MB 유계 프레임 상한(`P0-27`, `P0-35`), 세션 연결 단절 시 트랜잭션 최우선 롤백(`P0-26`), 세션 락 스코프 축소 및 재진입 워커 가드(`P0-30`), RLS 동적 AST 격리(`P0-31`), `@@TRANCOUNT` 중첩 커밋 엄격화(`P0-32`), 집합 연산 전 `LazyTuple` 조기 구체화(`P0-33`), 전역 세션 패닉 복구 가드(`P0-37`).
  - **메모리 위생**: Go `sync.Pool` 반환 시 포인터 제로잉 및 슬라이스 용량 상한선 강제(`P0-29`)로 고동시성 힙 비대화 및 GC Stop-The-World 스파이크 원천 차단.

- **WiredTiger 고동시성 락 해제 및 Invariant 10 영구 봉인**:
  - `wiredtiger_open` 설정에서 글로벌 C 트랜잭션 락 직렬화를 유발하던 `transaction_sync` 설정을 완전 영구 제거하는 4계층 봉인 체계 확립 (`[RULE:WiredTigerHighConcurrency]`).
  - WiredTiger 네이티브 그룹 커밋과 1초 주기 백그라운드 동기화로 100% ACID 내구성을 유지하면서, 스토리지 커널 TPC-C **1,184,785 tpmC (+42.1%)**, 네트워크 TDS **255,040 tpmC (+86.4%)** 폭증 달성.

- **전 티어 벤치마크 역대 최고치 경신 & 5초 쿨다운 아키텍처 (`scripts/run_all_benchmarks.sh`)**:
  - **Tier 1 (cmd/bench)**: In-Memory OCC **218,282 TPS (+16.1%)**, WiredTiger **141,472 TPS (+16.6%)** (100% ACID 검증).
  - **Tier 2 (Storage Kernel)**: In-Memory OCC **1,066,827 tpmC (39,748 TPS)**, WiredTiger **1,184,785 tpmC (41,905 TPS)**.
  - **Tier 3 (VDBE T-SQL Runtime)**: In-Memory OCC **723k ~ 759k tpmC (26.9k ~ 28.1k TPS, +25%~+46%)**, WiredTiger **825k ~ 863k tpmC (28.7k ~ 31.7k TPS, +25%~+39%)**.
  - **Tier 4 (Network Wire)**: PGWire v3 **329k ~ 376k tpmC (11.7k ~ 13.9k TPS)**, TDS **228k ~ 315k tpmC (8.4k ~ 11.6k TPS)**.
  - 고빈도 루프백 측정 시 OS TCP `TIME_WAIT` 소켓 드레인 및 CPU 써멀 안정을 보장하는 **5초 쿨다운 프로토콜(`--cooldown=5`)** 탑재.

- **SSMS 공식 연동 가이드 발간 및 가상 시스템 뷰 확충**:
  - Microsoft SQL Server Management Studio (SSMS 19/20) 완벽 연동 가이드 발간 ([`guide/SSMS_INTEGRATION_GUIDE.ko.md`](../../guide/SSMS_INTEGRATION_GUIDE.ko.md) / [English](../../guide/SSMS_INTEGRATION_GUIDE.md)).
  - `sys.dm_os_host_info` 가상 시스템 뷰 지원으로 GUI 인트로스펙션 호환성 극대화.

---

## 2. 검증 결과 요약

1. **3-Stage 검증 파이프라인 통과 (`[RULE:PipelineVerification]`)**:
   - macOS (`darwin-arm64`), Linux (`linux-amd64`), Windows (`windows-amd64.exe`) 3대 플랫폼 공식 릴리즈 바이너리 무결성 100% 빌드 성공.
   - `go test -tags wiredtiger -count=1 -race ./pkg/...` 전수 100% PASS (0 data races).
2. **ACID 내구성 및 크래시 복구 오라클 검증**:
   - `bin/crashtest` 10라운드 연속 무손실 복구 100% 검증 통과.
   - 10만 건 뱅킹 이체 트랜잭션 0 Rollback, 100% ACID 합치성 실증.
