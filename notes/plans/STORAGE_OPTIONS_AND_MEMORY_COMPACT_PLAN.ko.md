# LionSQL 스토리지 옵션 계층 분리(--disk / --memory) 및 인메모리 컴팩트 기본값 전환 계획서 (`notes/plans/STORAGE_OPTIONS_AND_MEMORY_COMPACT_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-10
> - **문서 성격**: 스토리지 엔진 옵션 직교화 및 1.0 인메모리 Flat 컴팩트 레코드 기본값 승격 아키텍처 사양서
> - **과제 식별 코드**: `CFG-STORAGE-OPT-1` (Stage 1: 옵션 계층 분리), `CFG-MEMORY-COMPACT-DEFAULT-1` (Stage 2: 기본값 컴팩트 전환)
> - **적용 원칙**: `[RULE:RadicalKISS]`, `[RULE:PlanPersistence]`, `[RULE:SafeDefaults]`, `[RULE:ContinuousBacklog]`, Invariant 3 (Non-Blocking OCC/MVCC), Invariant 4 (Standalone Single Binary)

---

## 1. 추진 배경 및 아키텍처 문제의식

### 1.1 기존 단일 `--engine` 옵션의 구조적 모호성
LionSQL은 초기 설계에서 단일 플래그인 `--engine=<wiredtiger|memory>`를 사용하여 스토리지 백엔드를 지정했습니다. 그러나 엔진이 고도화됨에 따라 다음과 같은 구조적 모순이 대두되었습니다:

1. **디스크 영속 계층과 인메모리 표현 계층의 혼재**:
   - `wiredtiger`: 디스크 기반 Lock-Free B+Tree MVCC 영속 스토리지 엔진.
   - `memory`: 인메모리 OCC(Optimistic Concurrency Control) 기반 고속 트랜잭션 엔진.
   - WiredTiger 모드를 활성화하더라도 세션 로컬 임시 테이블(`#temp`), T-SQL 테이블 변수(`@tbl`), 서브쿼리 구체화 파이프라인은 CGo 디스크 오버헤드를 우회하기 위해 순수 Go 인메모리 엔진(`memengine`)을 하이브리드로 사용함 (`PERF-HYBRID-TEMPMEM-1`).
   - 따라서 "WiredTiger 모드일 때 인메모리 레이아웃은 무엇을 사용하는가?"라는 질문에 기존 `--engine` 체계로는 직교적(Orthogonal)으로 답할 수 없음.

2. **`OCC` 용어의 의미적 왜곡 (Concurrency Protocol vs. Memory Layout)**:
   - OCC는 **낙관적 동시성 제어(Optimistic Concurrency Control)**라는 트랜잭션 충돌 검증 프로토콜의 명칭입니다.
   - 반면 사용자가 선택하고자 하는 것은 데이터의 **인메모리 튜플 저장 포맷(Tuple Layout Representation)**입니다:
     - Go 표준 슬라이스/포인터 구조체 (Raw): CPU 캐시 지향 극강의 단일 스레드 처리량 (104만 tpmC), 그러나 Go 런타임 GC 포인터 추적 오버헤드 존재.
     - 단일 바이트 버퍼 포인터리스 구조 (Flat): 83.2% 메모리 압축(5.95배), 0개의 GC 추적 힙 포인터(GC Pause 0ms), 극도로 압축된 풋프린트.
   - 이를 `--occ-mode=raw|flat`과 같은 임시 플래그로 노출하는 것은 사용자 관점에서 개념적 혼란을 야기함.

3. **향후 스토리지 엔진 확장성(Extensibility) 한계**:
   - 추후 CGo를 배제한 순수 Go 디스크 B+Tree 엔진 또는 LSM-Tree 엔진이 도입될 때, 단일 `--engine` 문자열 방식은 디스크 옵션과 인메모리 옵션 간의 조합 폭발(Combinatorial Explosion)을 유발함.

---

## 2. 핵심 설계 원칙 및 명명 체계 (Radical KISS Primacy)

### 2.1 2계층 직교 스토리지 사양 (`--disk` & `--memory`)
불필요한 동의어나 임시 플래그를 일절 배제하고(`[RULE:RadicalKISS]`), 가장 직관적인 두 개의 축으로 엔진 구성을 명확히 분리합니다:

```
+-------------------------------------------------------------------------+
|                              LionSQL Server                             |
+------------------------------------+------------------------------------+
| 1. 디스크 영속 스토리지 (--disk)    | 2. 인메모리 튜플 레이아웃 (--memory)|
+------------------------------------+------------------------------------+
| • wiredtiger                       | • compact (기본 권장, 83% 메모리 절감)
|   - Lock-Free B+Tree MVCC          |   - Pointer-Free Flat Byte Buffer   |
|   - Unix (macOS/Linux) 기본값      |   - Zero GC Pause (포인터 0개)       |
| • none                             | • performance                      |
|   - 디스크 영속 비활성화 (순수 메모리) |   - Direct Go Pointer Slices        |
|   - Windows 기본값                 |   - 104만 tpmC 극강의 단일 쿼리 속도  |
+------------------------------------+------------------------------------+
```

### 2.2 CLI 플래그 및 환경 설정 명세
1. **`--disk=<wiredtiger|none>`**:
   - `wiredtiger`: WiredTiger 디스크 B+Tree MVCC 영속화 엔진 사용 (Unix 기본값).
   - `none`: 디스크 B+Tree를 사용하지 않고 순수 인메모리 모드로 실행 (Windows 기본값, 독립형 제로 CGo).
2. **`--memory=<compact|performance>`**:
   - `compact`: 단일 바이트 슬라이스 기반 포인터리스 Flat 레코드. 83% 메모리 절감, GC 추적 포인터 0개 보장.
   - `performance`: 기존 오리지널 Go 슬라이스 레코드. 104만 tpmC 초고속 처리 성능.
3. **완벽한 하위 호환성 (Zero-Breaking Backward Compatibility)**:
   - 기존 설정 파일(`config.json`) 및 CLI의 `--engine` 옵션을 영구적으로 인식하여 자동 매핑:
     - `--engine=wiredtiger` ➔ `disk="wiredtiger"`
     - `--engine=memory` ➔ `disk="none"`
   - 사용자가 `--disk`를 명시적으로 주면 `--disk`가 우선 적용됨.

---

## 3. 2단계 단계적 롤아웃 계획 (Two-Stage Rollout Plan)

안정적인 프로덕션 운영을 위해 기능 도입과 기본값 변경을 2단계로 분리하여 진행합니다:

### [Stage 1] 스토리지 옵션 계층 분리 구현 (즉시 착수)
- **목표**: `--disk` 및 `--memory` 설정을 `pkg/config`, `pkg/storage/memengine`, `cmd/lionsql`에 전면 적용하고, 기존 동작을 100% 보존하면서 직교적 옵션 제어를 가능하게 함.
- **Stage 1 기본값**:
  - `DefaultDisk()`: Unix는 `"wiredtiger"`, Windows는 `"none"`.
  - `DefaultMemory()`: 임시로 기존 오리지널인 `"performance"` 유지 (`[RULE:SafeDefaults]`).
- **하위 호환성 매핑**:
  - `ServerConfig.GetDisk()`: `Disk`가 지정되어 있으면 사용, 비어있으면 `Engine` 값 해석(`"wiredtiger"` ➔ `"wiredtiger"`, `"memory"` ➔ `"none"`).
  - `ServerConfig.GetMemory()`: `Memory` 값 파싱 (`"compact"` ➔ Flat, `"performance"` ➔ Raw).
- **검증**: `go build -tags wiredtiger` 컴파일 무결성 및 `pkg/config`, `cmd/lionsql` 단위 테스트 100% PASS.

---

### [Stage 2] 인메모리 컴팩트 모드 전수 검증 및 기본값 공식 승격 (오늘 중 완료)
- **목표**: LionSQL 1.0의 메모리 풋프린트 혁신(83% 절감, GC Pause 0ms)을 공식 프로덕션 기본값으로 활성화하기 위해, 4대 필수 검증 게이트를 완벽히 통과한 후 `DefaultMemory()`를 `"compact"`로 전격 승격.
- **4대 검증 게이트 (Mandatory Reliability Gates)**:
  1. **Gate 1 (Race-Free Unit Suite)**:
     - `go test -tags wiredtiger -count=1 -v -race ./...` (컴팩트 모드 환경에서 100% 통과 및 0 Data Race).
  2. **Gate 2 (Full SLT Compliance)**:
     - `./bin/sqllogictest` 10,000+ 쿼리 컴팩트 모드 100% 인증.
  3. **Gate 3 (Crash Recovery & Durability)**:
     - `./bin/crashtest` (Flat 버퍼 AOF/RDB 직렬화 및 예기치 못한 강제 종료 복구 무결성 검증).
  4. **Gate 4 (Performance Gate Test)**:
     - `pkg/storage/wiredtiger/perf_gate_test.go` 및 인메모리 벤치마크 실측치 확인.
- **기본값 전환**:
  - `DefaultMemory()` 반환값을 `"performance"`에서 **`"compact"`**로 공식 변경.
  - 별도 옵션 지정 없이 기동 시 `compact` 모드가 기본 적용되어 최소 메모리/제로 GC로 가동.

---

## 4. 세부 모듈별 코드 변경 사양

### 4.1 `pkg/config/config.go`
```go
type ServerConfig struct {
    DataDir                  string `json:"data_dir"`
    Disk                     string `json:"disk,omitempty"`   // "wiredtiger", "none"
    Memory                   string `json:"memory,omitempty"` // "compact", "performance"
    Engine                   string `json:"engine,omitempty"` // [Legacy] 하위 호환성 지원
    // ... 기존 필드 유지
}

func DefaultDisk() string {
    if runtime.GOOS == "windows" {
        return "none"
    }
    return "wiredtiger"
}

func DefaultMemory() string {
    // Stage 1: "performance"
    // Stage 2 (검증 후): "compact"
    return "compact"
}

func (s *ServerConfig) GetDisk() string {
    d := strings.ToLower(strings.TrimSpace(s.Disk))
    if d != "" {
        switch d {
        case "wiredtiger", "wt":
            return "wiredtiger"
        case "none", "memory", "off":
            return "none"
        default:
            return d
        }
    }
    // 하위 호환성: Engine 필드 해석
    e := strings.ToLower(strings.TrimSpace(s.Engine))
    if e == "memory" {
        return "none"
    }
    return DefaultDisk()
}

func (s *ServerConfig) GetMemory() string {
    m := strings.ToLower(strings.TrimSpace(s.Memory))
    switch m {
    case "compact", "flat":
        return "compact"
    case "performance", "raw", "fast", "speed":
        return "performance"
    default:
        return DefaultMemory()
    }
}
```

### 4.2 `pkg/storage/memengine/memengine.go`
```go
// 기존 OCCMode 열거형에 직관적인 신규 상수 매핑 추가
const (
    OCCModePerformance OCCMode = 0 // alias for OCCModeRaw
    OCCModeCompact     OCCMode = 1 // alias for OCCModeFlat
)

// ParseOCCMode는 문자열("compact", "performance")을 OCCMode로 변환
func ParseOCCMode(s string) (OCCMode, error) {
    switch strings.ToLower(strings.TrimSpace(s)) {
    case "compact", "flat":
        return OCCModeCompact, nil
    case "performance", "raw", "":
        return OCCModePerformance, nil
    default:
        return OCCModePerformance, fmt.Errorf("unknown memory mode: %q (expected 'compact' or 'performance')", s)
    }
}
```

### 4.3 `cmd/lionsql/server.go`, `cmd/lionsql/main.go`, `cmd/lionsql/sql.go`
- CLI 플래그 바인딩:
  - `-disk <type>`: 디스크 스토리지 백엔드 (`wiredtiger`, `none`)
  - `-memory <type>`: 인메모리 튜플 레이아웃 (`compact`, `performance`)
  - `-engine <type>`: 레거시 하위 호환 플래그 (지정 시 `-disk`로 매핑 및 경고 로깅)
- 서버 기동 배너 출력 업데이트:
  ```
  LionSQL Server v0.92.0
  Storage Persistence: Disk [wiredtiger] | Memory Layout [compact (flat, zero-gc)]
  Listeners: HTTP :8090 | PGWire :5433 | TDS :1433
  ```

---

## 5. 지속적 백로그 및 작업 등록 (Dual-Sync)

`[RULE:ContinuousBacklog]`에 따라 SQLite 스프린트 DB(`.sprint.db`), `TASKS.md`, `TODO.md`에 본 계획의 두 핵심 과제를 등록합니다:

1. **`CFG-STORAGE-OPT-1` (Stage 1)**:
   - **제목**: 스토리지 옵션 계층 분리 (`--disk` / `--memory`) 및 하위 호환성 매핑
   - **도메인**: Configuration / CLI
   - **우선순위**: Priority 1
   - **상태**: In Progress
2. **`CFG-MEMORY-COMPACT-DEFAULT-1` (Stage 2)**:
   - **제목**: 인메모리 컴팩트(`compact`) 모드 4대 게이트 검증 및 기본값 공식 승격
   - **도메인**: Storage / Engine
   - **우선순위**: Priority 1
   - **상태**: Pending (Stage 1 완료 후 즉시 착수)

---

## 6. 결론 및 향후 로드맵 연계

본 작업을 통해 LionSQL은 다음과 같은 결실을 얻게 됩니다:
1. **개념적 명확성**: 디스크 영속성(`--disk`)과 인메모리 레이아웃(`--memory`)이 완벽히 직교화되어 사용자와 개발자 모두에게 명쾌한 멘탈 모델을 제공.
2. **프로덕션 풋프린트 극소화**: 1.0 릴리즈에서 `compact` 모드가 기본값으로 정착됨으로써 동일 하드웨어 대비 5.95배의 데이터 수용량과 Zero GC Pause를 달성.
3. **안전한 전환**: 엄격한 3단계 파이프라인(`Build` ➔ `Test` ➔ `Commit`)과 4대 검증 게이트를 거친 후 기본값을 변경하므로 회귀 결함 제로 보장.
