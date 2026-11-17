# LionSQL 스토리지 엔진 `-disk=compact` 표준화 및 플랫폼별 디폴트 정립 계획서

> **작성 일자**: 2026-11-12  
> **상태**: 적용 완료 (Completed)  
> **관련 규칙**: `[RULE:PlanPersistence]`, `[RULE:HermeticParity]`, `[RULE:StaticBinary]`, `[RULE:RadicalKISS]`, `[RULE:PipelineVerification]`

---

## 1. 배경 및 아키텍처 원칙

LionSQL은 외부 의존성 없는 단일 바이너리 고성능 엔터프라이즈 OLTP 데이터베이스 엔진입니다.
기존 스토리지 옵션 중 In-Memory OCC 엔진을 지칭하던 `-disk=none` 명칭은 다음과 같은 한계와 오해를 가지고 있었습니다:
1. **`-disk=none`의 어폐**: 디스크 영속성(`wal.aof`, `tables.mpk`)이 완벽히 존재하여 ACID 내구성을 보장함에도 `none`이라는 명칭으로 인해 "디스크 저장을 안 하는 휘발성 캐시"로 오해됨.
2. **동의어(Synonyms) 남발 위험**: `-disk=none`, `-disk=compact`, `-engine=memory` 등 불필요한 별칭이 늘어나면 CLI 인터페이스의 일관성이 저해됨.
3. **엔터프라이즈 DBMS 정체성 유지**:
   - **macOS / Linux**: 서버 환경에서 테라바이트급 디스크 확장이 가능한 진지한 디스크 기반 DBMS 엔진임을 증명하기 위해 **`wiredtiger` (Disk B+Tree MVCC)**를 확고한 기본값으로 유지.
   - **Windows**: C 컴파일러/CGo가 부재한 클라이언트 및 개발 환경에서 100% 무설치 단일 정적 실행을 보장하기 위해 **`compact` (In-Memory OCC + FlatRecord + AOF)**를 기본값으로 채택.

### 핵심 결정 사항
- **동의어 남발 제거 및 단일화**: `-disk=none`을 완전히 폐지하고 **`-disk=compact`**로 표준화. (스토리지 엔진 선택지는 오직 **`wiredtiger`**와 **`compact`** 2가지만 공식 인정).
- **플랫폼별 디폴트 확립**:
  - macOS / Linux: `wiredtiger` (기본값)
  - Windows: `compact` (기본값)

---

## 2. 변경 대상 상세 명세

### 2.1. 설정 모듈 (`pkg/config/`)
1. **`DefaultDisk()`**:
   - macOS / Linux: `"wiredtiger"`
   - Windows: `"compact"`
2. **`DefaultEngine()` (Deprecated)**:
   - macOS / Linux: `"wiredtiger"`
   - Windows: `"compact"`
3. **`ServerConfig.GetDisk()`**:
   - `wiredtiger`, `wt` ➔ `"wiredtiger"`
   - `compact`, `none`, `memory`, `mem`, `off` ➔ `"compact"` (레거시 플래그는 안전하게 `compact`로 자동 매핑)
4. **`pkg/config/config_test.go`**:
   - OS별 `DefaultDisk()` / `DefaultEngine()` 반환값 검증.
   - `sLegacy.GetDisk()` (`Disk: "none"`)가 `"compact"`로 수렴함을 검증.

### 2.2. CLI 및 서버 런타임 (`cmd/lionsql/`)
1. **`cmd/lionsql/main.go`**:
   - CLI 도움말 텍스트 갱신: `-disk <type>  Disk persistence backend: wiredtiger (default) or compact`
2. **`cmd/lionsql/server.go`**:
   - `switch resolvedDisk`:
     - `case "compact":` (In-Memory OCC 플랫 버퍼 엔진 구동)
     - `case "wiredtiger":` (WiredTiger 디스크 B+Tree 엔진 구동)
     - `default: log.Fatalf("[LionSQL] Unknown storage engine: %s (supported: compact, wiredtiger)", resolvedDisk)`
   - Ephemeral 검사 메시지: `-ephemeral`은 `-disk=compact`에서만 지원됨을 명시.
3. **`cmd/lionsql/sql.go`**:
   - `fs.String("disk", config.DefaultDisk(), "Disk persistence backend (compact, wiredtiger)")`
   - `openCoordinatorWithOptions` 스위치 문: `case "compact", "none", "memory", "mem":` 처리 및 에러 메시지 갱신.
4. **`cmd/lionsql/migrate.go`**:
   - `fs.String("disk", config.DefaultDisk(), "Disk persistence backend (compact, wiredtiger)")`
   - 레거시 `-engine memory` 입력 시 `resolvedDisk = "compact"`로 설정.

### 2.3. 매뉴얼 및 문서
- `doc/USER_MANUAL.ko.md`, `doc/USER_MANUAL.md`
- `doc/TRANSACTION_PERFORMANCE_GUIDE.ko.md`, `doc/TRANSACTION_PERFORMANCE_GUIDE.md`
