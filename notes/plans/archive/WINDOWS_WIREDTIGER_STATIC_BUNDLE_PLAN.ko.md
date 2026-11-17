# 윈도우 WiredTiger 사전 빌드 정적 번들링 및 Cgo 크로스 플랫폼 일원화 설계서
(`notes/plans/archive/WINDOWS_WIREDTIGER_STATIC_BUNDLE_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-16
> - **마일스톤**: `v0.94.0` (Priority 1)
> - **태스크 ID**: `FEAT-WINDOWS-WIREDTIGER-STATIC-BUNDLE-1`
> - **관련 규칙**: `[RULE:StaticBinary]`, `[RULE:HermeticParity]`, `[RULE:RadicalKISS]`, `[RULE:SafeDefaults]`, `[RULE:PlanPersistence]`

---

## 1. 배경 및 문제 정의 (Background & Problem Statement)

### 1.1 윈도우 플랫폼의 WiredTiger 결핍과 아키텍처 파편화
1. **스토리지 엔진 기본값 괴리**:
   - 현재 LionSQL은 macOS 및 Linux에서는 프로덕션 표준 스토리지 엔진으로 `WiredTiger`(B+Tree MVCC)를 기본값으로 사용하고 있습니다.
   - 반면 Windows에서는 Cgo 및 C 라이브러리 빌드 복잡성으로 인해 단독 Pure-Go 바이너리(`CGO_ENABLED=0`)로 빌드되며, 기본 스토리지 엔진이 `compact` (In-Memory OCC)로 분기되어 있었습니다.
2. **윈도우에서 WiredTiger 부착이 어려웠던 기술적 원인**:
   - **패키지 매니저 부재**: macOS(`brew install wiredtiger`)나 Linux(`apt install libwiredtiger-dev`)와 달리 Windows에는 공식 패키지 매니저(vcpkg 포함)에 WiredTiger 포트가 존재하지 않습니다.
   - **MSVC와 Go Cgo 툴체인의 충돌**: WiredTiger의 공식 윈도우 빌드는 MSVC(Visual Studio) 기준이나, Go의 Windows Cgo 링커는 GCC ABI(MinGW-w64)를 기반으로 동작하여 C 런타임(CRT, `msvcrt.dll` vs `ucrt.dll`) 심볼 충돌 및 스택 체크(`__chkstk`) 불일치가 발생합니다.
   - **외부 의존성 및 제로-세팅 철학 위배**: 사용자나 개발자에게 "LionSQL을 쓰려면 CMake, Python, Visual Studio를 깔아서 WiredTiger를 직접 컴파일하세요"라고 요구하는 것은 LionSQL의 핵심 설계 원칙인 **단독 정적 실행 파일(Zero-Dependency Self-Contained Binary, Invariant 4, `[RULE:StaticBinary]`)**에 정면으로 위배됩니다.

### 1.2 목표 (Goal)
- **사용자 제로-설치(Zero-Setup) 보장**: 윈도우 사용자나 개발자가 별도의 C 컴파일러나 라이브러리를 설치할 필요 없이, `git pull` 후 `go build -tags wiredtiger`만으로 즉시 컴파일되는 환경을 구축합니다.
- **맥에서 윈도우 WiredTiger 바이너리 크로스 컴파일 달성**: macOS 호스트 환경에서 `zig cc` 또는 MinGW를 활용하여 `lionsql-windows-amd64.exe`에 WiredTiger가 포함된 완전 정적 바이너리를 한 번에 빌드합니다.
- **OS 간 스토리지 엔진 기본값 100% 일원화**: Windows에서도 `DefaultEngine = "wiredtiger"`를 기본값으로 지정하여 macOS, Linux, Windows 전 플랫폼에서 동일한 MVCC B+Tree 엔진 경험을 제공합니다.

---

## 2. 핵심 설계 아키텍처 (Core Architectural Design)

### 2.1 사전 빌드 정적 라이브러리 번들링 방식 (Vendored Static Library)

오픈소스 Go 생태계에서 검증된 Cgo 모범 사례(예: `mattn/go-sqlite3`, `cockroachdb/pebble`, RocksDB Go 바인딩)를 차용하여, MinGW-w64 ABI로 사전 빌드된 `libwiredtiger.a`를 저장소 내부에 번들링합니다.

```text
pkg/storage/wiredtiger/
├── wt_engine.go               # Cgo 엔진 래퍼
├── wt_stub.go                 # Cgo 비활성화 시 스텁
└── lib/
    └── windows_amd64/
        ├── include/           # WiredTiger C 공개 헤더
        │   ├── wiredtiger.h
        │   └── wiredtiger_ext.h
        └── libwiredtiger.a    # MinGW-w64로 정적 컴파일된 라이브러리 (~12MB)
```

### 2.2 Go Cgo 지시어 설계 (`wt_engine.go` / `wt_engine_windows.go`)

Windows amd64 플랫폼 타깃에서 번들된 정적 라이브러리와 윈도우 시스템 DLL(`ws2_32`, `ntdll`)을 자동으로 링크하도록 조건부 Cgo 플래그를 구성합니다:

```go
//go:build cgo && wiredtiger

package wiredtiger

/*
#cgo darwin,arm64 CFLAGS: -I/opt/homebrew/include
#cgo darwin,arm64 LDFLAGS: -L/opt/homebrew/lib -lwiredtiger
#cgo darwin,amd64 CFLAGS: -I/usr/local/include
#cgo darwin,amd64 LDFLAGS: -L/usr/local/lib -lwiredtiger
#cgo linux CFLAGS: -I/usr/local/include -I/usr/include
#cgo linux LDFLAGS: -L/usr/local/lib -L/usr/lib -lwiredtiger

#cgo windows,amd64 CFLAGS: -I${SRCDIR}/lib/windows_amd64/include
#cgo windows,amd64 LDFLAGS: -L${SRCDIR}/lib/windows_amd64 -lwiredtiger -lws2_32 -lntdll

#include <stdlib.h>
#include <string.h>
#include <wiredtiger.h>
...
*/
import "C"
```

- **`${SRCDIR}` 매크로**: Go Cgo 표준 매크로로서, 패키지 디렉토리 절대 경로로 치환되므로 사용자가 어디에 저장소를 clone 하더라도 상대 경로로 완벽하게 링크됩니다.
- **`-lws2_32 -lntdll`**: WiredTiger의 윈도우 OS 계층(`src/os_win/`)에서 필요로 하는 Winsock 소켓 및 네이티브 NT 커널 동기화 API를 제공합니다.

---

## 3. Docker 기반 정적 라이브러리(`libwiredtiger.a`) 빌드 파이프라인

WiredTiger 공식 소스코드(`github.com/wiredtiger/wiredtiger`)는 이미 윈도우 네이티브 OS 계층(`src/os_win/`)을 내장하고 있습니다. 호스트 OS 환경 오염 없이 100% 격리된 Docker 환경에서 MinGW-w64 툴체인으로 정적 컴파일하는 절차를 정형화합니다.

### 3.1 Docker 기반 Hermetic 정적 빌드 환경
- **Docker 컨테이너**: 표준 `ubuntu:22.04` 기반
- **필수 빌드 패키지**: `gcc-mingw-w64-x86-64`, `g++-mingw-w64-x86-64`, `cmake`, `ninja-build`, `python3`, `git`
- **주요 CMake 정적 플래그**:
  ```bash
  cmake .. \
    -G "Ninja" \
    -DCMAKE_SYSTEM_NAME=Windows \
    -DCMAKE_C_COMPILER=x86_64-w64-mingw32-gcc \
    -DCMAKE_CXX_COMPILER=x86_64-w64-mingw32-g++ \
    -DCMAKE_BUILD_TYPE=Release \
    -DENABLE_STATIC=1 \
    -DENABLE_SHARED=0 \
    -DHAVE_ATTACH=0 \
    -DENABLE_SNAPPY=0 \
    -DENABLE_LZ4=0 \
    -DENABLE_ZLIB=0 \
    -DENABLE_ZSTD=0 \
    -DENABLE_PYTHON=0
  ```
- **외부 압축 라이브러리 정책**:
  - 1단계에서는 외부 C 라이브러리(snappy, zstd 등) 의존성을 배제하고 WiredTiger 자체 B+Tree 및 순수 프리미티브로 빌드하여 링킹 복잡도를 최소화합니다 (`[RULE:RadicalKISS]`).
  - 산출물로 생성된 `libwiredtiger.a`와 `wiredtiger.h`를 추출하여 `pkg/storage/wiredtiger/lib/windows_amd64/`에 안착시킵니다.

### 3.2 원클릭 빌드 스크립트 (`scripts/build_wiredtiger_windows.sh`)
- 호스트 머신의 `scripts/build_wiredtiger_windows.sh`를 실행하면 Docker 컨테이너가 기동되어 소스 다운로드부터 MinGW 컴파일, 정적 라이브러리 추출까지 원클릭으로 완료합니다.
- 추출이 끝나면 생성된 `pkg/storage/wiredtiger/lib/windows_amd64/`를 Git에 커밋하여, 이후 모든 빌드/릴리즈 과정에서 Docker 없이 네이티브 Go 빌드가 가능하도록 합니다.

---

## 4. 맥(macOS)에서의 윈도우 크로스 컴파일 전략

릴리즈 자동화(`scripts/release.sh`)에서 맥 호스트가 윈도우 WiredTiger 바이너리를 빌드하는 핵심 수단은 **`zig cc`**입니다.

### 4.1 Zig CC 기반 제로-도커 크로스 컴파일
- `zig`는 전 세계의 모든 libc와 Windows mingw-w64 헤더/런타임을 자체 내장하고 있어, 별도의 크로스 컴파일러 툴체인(heavy docker 등) 없이도 완전한 Windows Cgo 빌드를 수행할 수 있습니다.
- **컴파일 명령어**:
  ```bash
  CC="zig cc -target x86_64-windows-gnu" \
  CXX="zig c++ -target x86_64-windows-gnu" \
  CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
  go build -tags wiredtiger -ldflags "-s -w ${RELEASE_LDFLAGS}" -buildvcs=false -o bin/lionsql-windows-amd64.exe ./cmd/lionsql
  ```
- 만약 호스트에 `zig`나 `x86_64-w64-mingw32-gcc`가 없는 환경에서는, 기존의 순수 Go in-memory 폴백(`CGO_ENABLED=0`)을 안전하게 타도록 `scripts/release.sh`에 단계적 Fallback을 유지합니다.

---

## 5. 기본값 및 아키텍처 통일 (All-Platform Parity)

Windows용 WiredTiger 정적 번들링이 완료되면, LionSQL의 설정 계층에서 OS별 분기를 전면 제거할 수 있습니다:

### 5.1 `pkg/config/config.go` 통일
```go
// DefaultEngine returns the default storage engine backend ("wiredtiger").
// With Windows static WiredTiger bundling, all supported platforms (macOS, Linux, Windows)
// standardize on WiredTiger (Lock-Free B+Tree MVCC) out-of-the-box.
func DefaultEngine() string {
    return "wiredtiger"
}

// DefaultDisk returns "wiredtiger" across all platforms.
func DefaultDisk() string {
    return "wiredtiger"
}
```
- 모든 OS에서 디폴트 = `wiredtiger`
- 모든 OS에서 `-engine compact` 지정 시 = `compact` (In-Memory OCC)
- 모든 OS에서 `compact`의 디폴트 영속성 = `schema_and_data`
- **결과**: OS에 따른 어떠한 동작/설정 분기도 존재하지 않는 완전한 크로스 플랫폼 대칭성(`[RULE:HermeticParity]`) 달성.

---

## 6. 구현 마일스톤 및 로드맵 (Milestones)

### Phase 1: WiredTiger Windows 정적 빌드 및 벤더링 (`v0.94.0`)
- [ ] WiredTiger 11.x 소스를 MinGW-w64로 정적 컴파일하여 `libwiredtiger.a` 생성.
- [ ] `pkg/storage/wiredtiger/lib/windows_amd64/`에 헤더와 `.a` 파일 배치.
- [ ] `wt_engine.go`에 `#cgo windows,amd64` 지시어 추가.

### Phase 2: 크로스 컴파일 및 로컬 검증 (`v0.94.0`)
- [ ] macOS 호스트에서 `zig cc` 또는 `mingw`를 통한 Windows Cgo 컴파일 검증.
- [ ] 윈도우 환경(또는 Wine)에서 `./bin/lionsql-windows-amd64.exe --doctor` 및 `wtEng.Open(dataDir)` B+Tree 생성/검증.

### Phase 3: 릴리즈 스크립트 및 디폴트 엔진 일원화 (`v0.94.0`)
- [ ] `scripts/release.sh`의 Windows 빌드 타깃에 `-tags wiredtiger` 통합.
- [ ] `pkg/config/config.go`의 `DefaultEngine()`을 전 플랫폼 `"wiredtiger"`로 단일화.
- [ ] 릴리즈 자동 검증 방화벽에 윈도우 바이너리의 WiredTiger 태그 검증 추가.

---

## 7. 기대 효과 (Impact & Benefits)

1. **사용자 경험 극대화**:
   - 윈도우 개발자도 엔터프라이즈급 WiredTiger B+Tree MVCC 스토리지 엔진을 아무런 C 개발도구 설치 없이 단일 실행 파일(`.exe`)로 즉시 활용 가능.
2. **코드베이스 단순화 (`[RULE:RadicalKISS]`)**:
   - `runtime.GOOS == "windows"`로 인한 스토리지 엔진 분기, 매니페스트 불일치, 테스트 예외 처리 코드 대규모 정리.
3. **완전한 3대 OS 대칭성 (`[RULE:HermeticParity]`)**:
   - macOS, Linux, Windows 3개 플랫폼이 100% 동일한 스토리지 엔진 아키텍처 및 벤치마크 특성을 공유.
