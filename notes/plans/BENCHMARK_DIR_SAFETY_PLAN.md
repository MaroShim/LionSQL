# 벤치마크 디렉토리 안전 라이프사이클 및 데이터 보호 계획 (BENCH-DIR-SAFETY-1)

## 1. 배경 및 문제 정의 (Context & Problem Statement)

### 1.1. 지적된 핵심 취약점
기존 LionSQL의 내장 벤치마크 드라이버(`cmd/bench`, `cmd/tpcc_runtime`, `cmd/tpcc_kernel`)는 기동 시점과 종료 시점(`defer`)에 대상 디렉토리에 대해 무조건적으로 `os.RemoveAll(*dataDir)`을 호출하고 있었습니다:

```go
// 기존 위험 코드 (cmd/bench, cmd/tpcc_runtime, cmd/tpcc_kernel 공통)
case "wiredtiger":
    _ = os.RemoveAll(*dataDir)  // [위험 1] 기존에 데이터가 있던 디렉토리면 시작하자마자 영구 삭제!
    eng = wiredtiger.New()
    ...
defer func() {
    eng.Close()
    if *engineType == "wiredtiger" {
        _ = os.RemoveAll(*dataDir)  // [위험 2] 벤치마크 종료 시 무조건 디렉토리 전체 삭제!
    }
}()
```

### 1.2. 잠재적 치명 위험 (Catastrophic Data Loss Risks)
1. **운영 데이터 디렉토리 오지정 시 즉각 영구 삭제**:
   - 사용자가 `-dir=data` (실제 프로덕션 데이터베이스 디렉토리) 또는 `-dir=.`, `-dir=/var/lib/data`를 지정하고 벤치마크를 실행하면, **기존의 모든 실데이터 파일이 경고나 확인 없이 즉각 영구 삭제**됩니다.
2. **사일런트 실패 안티패턴**:
   - `_ = os.RemoveAll(...)`로 에러를 완전히 삼켜버려 디렉토리 삭제 실패나 권한 부족을 감지할 수 없습니다 (골든 룰 #7 위반).
3. **비파괴 안전성 원칙 위반**:
   - 골든 룰 #1 (Non-Destructive File Safety) 및 #5 (Defensive Path Security): 데이터베이스 소프트웨어는 자신이 배타적으로 생성하지 않은 기존 디렉토리를 절대 임의로 삭제해서는 안 됩니다.

---

## 2. 핵심 안전 계약 (Core Architectural Safety Invariants)

사용자 피드백에 따라 **"사용자가 전달한 경로를 부모(Base) 디렉토리로 이용하고, 항상 그 안에 고유한 신규 하위 디렉토리를 생성하여 실행"**하는 격리 모델을 강제합니다:

```
                  [ 벤치마크 기동 (-dir=<parentPath>) ]
                                │
                                ▼
                   [ 1. 부모 경로 정규화 & 유효성 검사 ]
                     (filepath.Clean, 루트/현재폴더 가드)
                                │
                                ▼
                   [ 2. 부모 디렉토리 준비 (os.MkdirAll) ]
                     (기존 부모 디렉토리는 절대 삭제 안 함)
                                │
                                ▼
                   [ 3. 배타적 신규 하위 디렉토리 생성 ]
                     (os.MkdirTemp: <parent>/<prefix>_*)
                                │
                                ▼
                   [ 4. 벤치마크 실행 (신규 하위 디렉토리 사용) ]
                                │
                                ▼
                   [ 5. 사후 안전 정리 (cleanup) ]
                   - 자신이 생성한 하위 디렉토리만 삭제
                   - 부모 디렉토리 및 기존 파일 100% 보존
                   - (-keep-dir 지정 시 하위 디렉토리 보존)
                   - 삭제 에러 시 표준 에러 가시적 보고
```

1. **부모 디렉토리 보존 불변식 (Parent Directory Preservation)**:
   - 사용자가 지정한 부모 디렉토리(`cleanParent`)와 그 안에 이미 존재하는 다른 파일/폴더는 **절대로 삭제하거나 건드리지 않습니다.**
2. **배타적 신규 하위 디렉토리 격리 (Isolated Subdirectory Precondition)**:
   - `os.MkdirTemp`를 통해 매 벤치마크 실행마다 충돌 없는 독립적 신규 디렉토리를 생성합니다.
3. **소유권 기반 한정 삭제 (Scoped Ownership Teardown)**:
   - 프로세스 종료 시 `defer` 클린업은 **해당 벤치마크 프로세스가 직접 새로 생성한 하위 디렉토리에 대해서만** `os.RemoveAll`을 수행합니다.
4. **에러 가시성 보장 (No Silent Failures)**:
   - `_ = os.RemoveAll`을 전면 금지하며, 삭제 실패 시 사용자에게 경고 메시지를 명확히 출력합니다.
5. **사후 분석 보존 옵션 (`-keep-dir`)**:
   - 벤치마크 결과 데이터베이스를 디버깅하거나 검증하고 싶은 사용자를 위해 `-keep-dir` 플래그를 추가하여 자동 삭제를 방지할 수 있도록 지원합니다.

---

## 3. 상세 구현 설계 (Implementation Specification)

### 3.1. 공통 안전 헬퍼: `pkg/storage/benchdir.go`
`pkg/storage` 패키지에 공통 함수를 구현합니다:

```go
package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// PrepareBenchmarkDir creates an isolated, freshly created unique benchmark directory
// inside parentDir using the specified prefix.
//
// The parentDir is treated as the base directory (created via os.MkdirAll if it does not exist).
// A brand-new unique subdirectory is created inside it via os.MkdirTemp.
//
// The returned cleanup closure removes ONLY the newly created unique child directory upon completion.
// The parent directory and any pre-existing files within it are strictly preserved.
func PrepareBenchmarkDir(parentDir, prefix string, keepDir bool) (dir string, cleanup func(), err error) {
	cleanParent := filepath.Clean(parentDir)
	if cleanParent == "" || cleanParent == "." || cleanParent == "/" || cleanParent == filepath.VolumeName(cleanParent)+"\\" {
		cleanParent = filepath.Join(".", "data")
	}

	// 1. Ensure parent base directory exists (never delete it!)
	if err := os.MkdirAll(cleanParent, 0755); err != nil {
		return "", nil, fmt.Errorf("failed to prepare parent benchmark directory %q: %w", cleanParent, err)
	}

	// 2. Create an exclusively fresh, isolated child directory
	pattern := prefix + "_*"
	subDir, err := os.MkdirTemp(cleanParent, pattern)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create isolated benchmark directory in %q: %w", cleanParent, err)
	}

	// 3. Cleanup closure scoped strictly to the newly created subdirectory
	cleanupFunc := func() {
		if keepDir {
			fmt.Printf("[LionSQL Benchmark] Data directory preserved: %s\n", subDir)
			return
		}
		if rmErr := os.RemoveAll(subDir); rmErr != nil {
			fmt.Fprintf(os.Stderr, "[LionSQL Benchmark] Warning: failed to clean up benchmark directory %q: %v\n", subDir, rmErr)
		}
	}

	return subDir, cleanupFunc, nil
}
```

### 3.2. 3개 벤치마크 바이너리 리팩토링

#### 1) `cmd/bench/main.go`
- `-keep-dir` 플래그 신설 (`flag.Bool("keep-dir", false, "Preserve benchmark data directory after completion")`).
- 기동 시점의 `_ = os.RemoveAll(*dataDir)` 제거.
- WiredTiger 백엔드 준비 단계에서 `storage.PrepareBenchmarkDir(*dataDir, "bench_wt", *keepDir)` 호출:
  ```go
  case "wiredtiger":
      subDir, cleanup, err := storage.PrepareBenchmarkDir(*dataDir, "bench_wt", *keepDir)
      if err != nil {
          fmt.Fprintf(os.Stderr, "Benchmark directory error: %v\n", err)
          os.Exit(1)
      }
      defer cleanup()
      eng = wiredtiger.New()
      if err := eng.Open(subDir); err != nil { ... }
  ```

#### 2) `cmd/tpcc_runtime/main.go`
- `-keep-dir` 플래그 신설.
- WiredTiger 및 In-Memory WAL 모드에서 디스크 디렉토리가 필요한 경우 `storage.PrepareBenchmarkDir`을 통해 신규 하위 디렉토리를 생성하여 엔진에 전달 및 `defer cleanup()` 등록.
- 기동 시점의 `_ = os.RemoveAll(*dataDir)` 2개소 완전 제거.

#### 3) `cmd/tpcc_kernel/main.go`
- `-keep-dir` 플래그 신설.
- WiredTiger 백엔드에 `storage.PrepareBenchmarkDir` 적용 및 기동/종료 시점의 무조건적 `os.RemoveAll` 제거.

### 3.3. 테스트 및 검증 스크립트 정합성
- `scripts/verify_report.sh` (Phase 3 TPC-C):
  - `"${BIN_DIR}/tpcc-runtime" ... -dir="$test_dir"` 실행 시, `test_dir` 내부에 안전한 임시 하위 디렉토리가 생성되어 실행 후 자체 정리됨.

---

## 4. 단위 테스트 설계 (`pkg/storage/benchdir_test.go`)

다음 5대 핵심 테스트 케이스를 구축하여 무결성을 검증합니다:

1. **`TestPrepareBenchmarkDir_PreservesParentAndExistingFiles`**:
   - 기존 파일들이 들어있는 부모 디렉토리를 전달했을 때, 부모 디렉토리와 기존 파일이 100% 보존되고, 신규 하위 디렉토리만 깨끗하게 정리되는지 검증.
2. **`TestPrepareBenchmarkDir_IsolatedConcurrentRuns`**:
   - 동일한 부모 디렉토리를 가리키는 두 벤치마크 작업이 각각 서로 다른 격리된 하위 디렉토리를 생성하고 간섭 없이 정리되는지 검증.
3. **`TestPrepareBenchmarkDir_KeepDir`**:
   - `keepDir = true`일 때 `cleanup()`을 호출해도 하위 디렉토리가 보존되는지 검증.
4. **`TestPrepareBenchmarkDir_AutomaticParentCreation`**:
   - 아직 존재하지 않는 깊은 부모 경로(`data/test/sub/bench`) 전달 시 부모까지 정상 생성 후 하위 디렉토리 작업 완료 검증.
5. **`TestPrepareBenchmarkDir_FallbackForCwdOrEmpty`**:
   - `""` 또는 `"."` 전달 시 안전한 기본 폴더(`data`)를 부모로 자동 지정하여 루트/CWD 파일이 훼손되지 않도록 보호.
