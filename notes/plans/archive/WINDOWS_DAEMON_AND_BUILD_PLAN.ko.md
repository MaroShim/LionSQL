# Windows 플랫폼 지원, 데몬 분리 및 고해상도 타이머 구현 계획서 (`WINDOWS_DAEMON_AND_BUILD_PLAN.ko.md`)

> **상태 (Status)**: 완료됨 (Completed) `[x]`  
> **적용 일자**: 2026-10-25  
> **구현 대상**: `cmd/lionsql/daemon.go`, `cmd/lionsql/daemon_unix.go`, `cmd/lionsql/daemon_windows.go`, `bin/lionsql.exe`, `bin/lionsql-arm64.exe`

## 1. 개요 및 추진 배경 (Overview & Problem Statement)

LionSQL은 데이터베이스 커널, 웹 관리 스튜디오, 다중 와이어 프로토콜(PGWire, TDS)이 단 하나의 독립 실행 파일(`bin/lionsql`)에 내장된 고동시성 OLTP 엔진입니다. 

순수 Go 기반의 스토리지 엔진(`memengine`), VDBE 가상머신(`pkg/proc`), 쿼리 플래너/실행기(`pkg/engine`, `pkg/execution`), 네트워크 프로토콜(`pkg/wire`)은 이미 OS 독립적으로 설계되어 있어 Windows 컴파일(`GOOS=windows GOARCH=amd64 go build ./pkg/...`)이 100% 통과합니다.

그러나 Windows 플랫폼 완전 지원을 위해 다음 2가지 OS 고유의 문제가 해결되어야 합니다:

1. **POSIX 전용 시스템 콜 하드코딩 에러**:
   - `cmd/lionsql/daemon.go`에 `syscall.Kill`, `syscall.SIGTERM`, `syscall.SysProcAttr.Setsid`가 직접 호출되어 Windows 타깃 컴파일 에러 발생.
2. **Windows 기본 타이머 해상도 지연 문제 (15.625ms 틱 한계)**:
   - Windows의 기본 OS 스케줄러 인터럽트 주기는 15.625ms(64Hz)로, 짧은 밀리초 단위 `time.Sleep`이나 타임아웃 채널 대기가 다음 인터럽트 틱까지 대기하여 최대 15.6ms까지 스레드가 지연되는 현상 발생.
   - 고성능 OLTP 데이터베이스의 일관된 1ms 미만 Tail Latency 보장을 위해 멀티미디어 고해상도 타이머(`timeBeginPeriod(1)`) 활성화 필요.

본 계획서는 `daemon.go`를 **OS 중립적 공통 오케스트레이터**와 **플랫폼별 빌드 태그 파일(`daemon_unix.go`, `daemon_windows.go`)**로 분리하고, Windows 환경에서 **1ms 고해상도 타이머**를 자동 초기화하여 macOS/Linux 및 Windows 환경 모두에서 최상의 OLTP 반응성으로 빌드 및 기동되도록 아키텍처를 확장합니다.

---

## 2. 파일 분해 및 인터페이스 설계 (Decomposition Architecture)

```
                            [ cmd/lionsql/ ]
                                    │
    ┌───────────────────────────────┼───────────────────────────────┐
    ▼                               ▼                               ▼
[ daemon.go ]             [ daemon_unix.go ]              [ daemon_windows.go ]
(공통 OS 중립 로직)           (//go:build !windows)           (//go:build windows)
- CLI 플래그 핸들링          - syscall.Kill(pid, 0)          - OpenProcess + GetExitCode
- PID 파일 입출력            - SIGTERM ➔ SIGKILL 에스컬레이션 - TerminateProcess (proc.Kill)
- 상태 출력 포맷팅           - Setsid: true (세션 분리)      - DETACHED_PROCESS 플래그
- 생명주기 오케스트레이션                                     - timeBeginPeriod(1) 1ms 타이머
```

### 2.1. 플랫폼 추상화 함수 인터페이스
플랫폼별 차이점은 다음 4대 저수준 기본 연산으로 캡슐화합니다:

1. **`isProcessAlive(pid int) bool`**:
   - 대상 PID 프로세스가 현재 OS 커널 상에 살아있는지 확인.
2. **`killProcessGraceful(pid int) error`**:
   - 대상 프로세스에 부드러운 종료 요청 전달.
3. **`killProcessForce(pid int) error`**:
   - 타임아웃 초과 시 대상 프로세스 강제 종료.
4. **`setDaemonSysProcAttr(cmd *exec.Cmd)`**:
   - 자식 백그라운드 프로세스가 현재 콘솔/터미널 세션으로부터 완전히 분리(Detached)되도록 프로세스 속성 주입.

### 2.2. Windows 고해상도 타이머 인터럽트 아키텍처
* **문제점**: Windows의 기본 시스템 타이머 주기는 15.625ms(64Hz)로, 짧은 대기(Sleep/Select) 시 최대 15.6ms 지연 발생.
* **해결책**: Go 표준 라이브러리 `syscall.NewLazyDLL("winmm.dll")`을 통해 `timeBeginPeriod(1)`을 프로세스 초기화(`init()`) 시점에 호출하여 OS 스케줄러 인터럽트 해상도를 **1ms (1000Hz)**로 설정.
* **현대 Windows 호환성**: Windows 10 (버전 2004+) 및 Windows 11에서는 `timeBeginPeriod`가 전역이 아닌 **해당 프로세스 격리(Per-Process)**로 동작하므로 타 프로세스나 전력 소모에 영향을 주지 않으며, 프로세스 종료 시 OS에 의해 자동 해제됨.

---

## 3. 플랫폼별 상세 구현 명세 (Implementation Details)

### 3.1. Unix 계열 구현 (`cmd/lionsql/daemon_unix.go`)
- **빌드 태그**: `//go:build !windows`
- **구현 내용**:
  ```go
  package main

  import (
  	"os/exec"
  	"syscall"
  )

  func isProcessAlive(pid int) bool {
  	if pid <= 0 {
  		return false
  	}
  	err := syscall.Kill(pid, 0)
  	return err == nil || err == syscall.EPERM
  }

  func killProcessGraceful(pid int) error {
  	return syscall.Kill(pid, syscall.SIGTERM)
  }

  func killProcessForce(pid int) error {
  	return syscall.Kill(pid, syscall.SIGKILL)
  }

  func setDaemonSysProcAttr(cmd *exec.Cmd) {
  	cmd.SysProcAttr = &syscall.SysProcAttr{
  		Setsid: true, // 터미널 세션 리더 분리
  	}
  }
  ```

### 3.2. Windows 계열 구현 (`cmd/lionsql/daemon_windows.go`)
- **빌드 태그**: `//go:build windows`
- **구현 내용**:
  ```go
  package main

  import (
  	"os"
  	"os/exec"
  	"syscall"
  )

  const (
  	stillActive             = 259
  	processQueryLimitedInfo = 0x1000
  	processQueryInfo        = 0x0400
  	createNewProcessGroup   = 0x00000200
  	detachedProcess         = 0x00000008
  )

  var (
  	modwinmm            = syscall.NewLazyDLL("winmm.dll")
  	procTimeBeginPeriod = modwinmm.NewProc("timeBeginPeriod")
  	procTimeEndPeriod   = modwinmm.NewProc("timeEndPeriod")
  )

  func init() {
  	// 1ms 고해상도 타이머 요청 (Windows 15.6ms 스케줄러 틱 지연 방지)
  	_ = timeBeginPeriod(1)
  }

  func timeBeginPeriod(period uint32) error {
  	if err := procTimeBeginPeriod.Find(); err != nil {
  		return err
  	}
  	r, _, _ := procTimeBeginPeriod.Call(uintptr(period))
  	if r != 0 {
  		return syscall.Errno(r)
  	}
  	return nil
  }

  func isProcessAlive(pid int) bool {
  	if pid <= 0 {
  		return false
  	}
  	h, err := syscall.OpenProcess(processQueryLimitedInfo, false, uint32(pid))
  	if err != nil {
  		h, err = syscall.OpenProcess(processQueryInfo, false, uint32(pid))
  		if err != nil {
  			return err == syscall.ERROR_ACCESS_DENIED
  		}
  	}
  	defer syscall.CloseHandle(h)

  	var exitCode uint32
  	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
  		return false
  	}
  	return exitCode == stillActive
  }

  func killProcessGraceful(pid int) error {
  	p, err := os.FindProcess(pid)
  	if err != nil {
  		return err
  	}
  	return p.Kill()
  }

  func killProcessForce(pid int) error {
  	return killProcessGraceful(pid)
  }

  func setDaemonSysProcAttr(cmd *exec.Cmd) {
  	cmd.SysProcAttr = &syscall.SysProcAttr{
  		CreationFlags: createNewProcessGroup | detachedProcess,
  	}
  }
  ```

### 3.3. OS 중립적 오케스트레이터 (`cmd/lionsql/daemon.go`)
- `daemon.go` 상단의 `import "syscall"`을 완전 제거 (플랫폼 중립화).
- `handleDaemonCommands`, `readPidFile`, `checkDaemonStatus`, `startDaemon`, `stopDaemon`을 유지하되, 내부에서 `syscall.Kill` 대신 `isProcessAlive`, `killProcessGraceful`, `killProcessForce`, `setDaemonSysProcAttr`를 호출하도록 리팩토링.
- 윈도우 경로 및 PID 파일 입출력 시 `filepath.Clean`을 통한 경로 정규화 유지.

---

## 4. 빌드 파이프라인 및 아티팩트 (Build Targets)

### 4.1. 타깃 바이너리 빌드 매트릭스
1. **macOS / Linux 프로덕션 바이너리 (기존 유지)**:
   ```bash
   go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql
   ```
2. **Windows x64 정적 단일 바이너리 (신규)**:
   ```bash
   CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o bin/lionsql.exe ./cmd/lionsql
   ```
3. **Windows arm64 정적 단일 바이너리 (신규)**:
   ```bash
   CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -buildvcs=false -o bin/lionsql-arm64.exe ./cmd/lionsql
   ```

*(참고: Windows 환경에서는 Cgo 기반 WiredTiger 의존성이 없는 순수 Go 인메모리 OCC 스토리지 엔진(`-engine=memory`)이 기본 탑재되어 완벽한 제로 외장 의존성으로 구동됩니다.)*

---

## 5. 실행 및 3단계 검증 파이프라인 (Build ➔ Test ➔ Commit)

1. **Stage 1: 교차 플랫폼 컴파일 검증 (Cross-Compilation Check)**:
   - macOS 바이너리 빌드: `bin/lionsql`
   - Windows amd64 바이너리 빌드: `bin/lionsql.exe`
   - Windows arm64 바이너리 빌드: `bin/lionsql-arm64.exe`
2. **Stage 2: 전체 단위 및 회귀 테스트 검증 (Test -race)**:
   - `go test -tags wiredtiger -count=1 -race ./cmd/lionsql/...`
   - `GOOS=windows GOARCH=amd64 go test -exec=none ./cmd/lionsql/...`
3. **Stage 3: 커밋 및 문서 갱신 (Commit)**:
   - `notes/WINDOWS_DAEMON_AND_BUILD_PLAN.md` 영속화 및 `notes/README.md` 색인 반영.
   - `CHANGELOG.md` 및 `README.md` / `README.ko.md` 지원 플랫폼 업데이트.
   - 커밋: `feat(daemon): decouple platform-specific process lifecycle and enable Windows binary build`
