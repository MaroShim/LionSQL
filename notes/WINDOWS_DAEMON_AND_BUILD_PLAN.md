# Windows 플랫폼 지원 및 데몬 분리 구현 계획서 (`WINDOWS_DAEMON_AND_BUILD_PLAN.md`)

> **상태 (Status)**: 완료됨 (Completed) `[x]`  
> **적용 일자**: 2026-10-25  
> **구현 대상**: `cmd/lionsql/daemon.go`, `cmd/lionsql/daemon_unix.go`, `cmd/lionsql/daemon_windows.go`, `bin/lionsql.exe`, `bin/lionsql-arm64.exe`

## 1. 개요 및 추진 배경 (Overview & Problem Statement)

LionSQL은 데이터베이스 커널, 웹 관리 스튜디오, 다중 와이어 프로토콜(PGWire, TDS)이 단 하나의 독립 실행 파일(`bin/lionsql`)에 내장된 고동시성 OLTP 엔진입니다. 

순수 Go 기반의 스토리지 엔진(`memengine`), VDBE 가상머신(`pkg/proc`), 쿼리 플래너/실행기(`pkg/engine`, `pkg/execution`), 네트워크 프로토콜(`pkg/wire`)은 이미 OS 독립적으로 설계되어 있어 Windows 컴파일(`GOOS=windows GOARCH=amd64 go build ./pkg/...`)이 100% 통과합니다.

그러나 CLI 통합 실행 파일인 `cmd/lionsql/daemon.go`에 **POSIX 전용 시스템 콜(`syscall.Kill`, `syscall.SIGTERM`, `syscall.SysProcAttr.Setsid`)이 하드코딩**되어 있어, Windows 타깃 크로스 컴파일 시 즉각 컴파일 에러가 발생합니다:

```
# lionsql/cmd/lionsql (Windows 타깃 컴파일 에러)
cmd/lionsql/daemon.go:83:17: undefined: syscall.Kill
cmd/lionsql/daemon.go:135:14: undefined: syscall.Kill
cmd/lionsql/daemon.go:151:15: undefined: syscall.Kill
cmd/lionsql/daemon.go:206:3: unknown field Setsid in struct literal of type syscall.SysProcAttr
```

본 계획서는 `daemon.go`를 **OS 중립적 공통 오케스트레이터**와 **플랫폼별 빌드 태그 파일(`daemon_unix.go`, `daemon_windows.go`)**로 분리하여, macOS/Linux 및 Windows 환경 모두에서 단일 바이너리(`bin/lionsql`, `bin/lionsql.exe`)가 완벽히 빌드되고 기동되도록 아키텍처를 확장합니다.

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
- 생명주기 오케스트레이션
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
  	// stillActive: Windows 커널의 STILL_ACTIVE 상태 코드 (259)
  	stillActive = 259
  	// detachedProcess: 콘솔 창을 생성하지 않고 부모 콘솔에서 독립
  	detachedProcess = 0x00000008
  	// createNewProcessGroup: 독립 프로세스 그룹 생성
  	createNewProcessGroup = 0x00000200
  )

  func isProcessAlive(pid int) bool {
  	if pid <= 0 {
  		return false
  	}
  	p, err := os.FindProcess(pid)
  	if err != nil {
  		return false
  	}
  	defer p.Release()

  	var exitCode uint32
  	err = syscall.GetExitCodeProcess(syscall.Handle(p.Handle), &exitCode)
  	if err != nil {
  		return false
  	}
  	return exitCode == stillActive
  }

  func killProcessGraceful(pid int) error {
  	p, err := os.FindProcess(pid)
  	if err != nil {
  		return err
  	}
  	defer p.Release()
  	return p.Kill() // Windows 커널 TerminateProcess 호출
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
