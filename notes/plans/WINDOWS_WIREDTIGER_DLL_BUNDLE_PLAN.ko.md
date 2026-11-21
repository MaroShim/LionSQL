# 윈도우 WiredTiger MSVC 네이티브 DLL 빌드 및 자가 추출(Self-Extracting) 번들링 설계서
(`notes/plans/WINDOWS_WIREDTIGER_DLL_BUNDLE_PLAN.ko.md`)

> - **최초 작성일**: 2026-11-21
> - **마일스톤**: `v1.1.0` (Priority 1)
> - **태스크 ID**: `FEAT-WINDOWS-WIREDTIGER-DLL-BUNDLE-1`
> - **관련 규칙**: `[RULE:StaticBinary]`, `[RULE:HermeticParity]`, `[RULE:RadicalKISS]`, `[RULE:SafeDefaults]`, `[RULE:PlanPersistence]`

---

## 1. 배경 및 문제 정의 (Background & Problem Analysis)

### 1.1 MinGW 정적 라이브러리(`.a`) 크로스 컴파일의 한계
1. **MSVC 전용 소스 구조**: WiredTiger의 윈도우 OS 계층(`src/include/os_windows.h`)은 MSVC(`_MSC_VER`)를 전제로 작성되어 있어, MinGW GCC로 컴파일 시 수많은 문법 오류, 타입 충돌(`timespec`, `ssize_t`), 인트린식(`_Interlocked*`) 부재가 발생합니다.
2. **C 런타임(CRT) 심볼 불일치**: 정적 아카이브(`libwiredtiger.a`)를 Go Cgo로 링크할 때, Go의 윈도우 링커(`cmd/link`)가 MSVC CRT 심볼(`__chkstk`, `_iob`, `_vsnprintf`)을 해결하지 못해 수백 개의 Undefined reference 에러가 발생합니다.
3. **호스트 OS 파편화**: macOS 호스트에는 기본적으로 윈도우 C 크로스 컴파일러(MinGW, Zig)가 설치되어 있지 않아 `CGO_ENABLED=1 GOOS=windows` 빌드가 원천 차단됩니다.

### 1.2 방안 A의 핵심 해결 원리: MSVC DLL + Self-Extracting 패턴
WiredTiger의 소스코드 내부에서 외부로 호출되는 공개 C 함수는 사실상 **`wiredtiger_open`**과 **`wiredtiger_strerror`** 2개뿐이며, 나머지 모든 커서 및 트랜잭션 연산은 C 구조체 함수 포인터(`conn->open_session`, `session->open_cursor`, `cursor->next`)를 통해 메모리 상에서 직접 호출됩니다.

따라서 WiredTiger를 **공식 MSVC 컴파일러 기반의 DLL(`wiredtiger.dll`)**로 빌드하면:
1. **내부 CRT 종속성 완전 캡슐화**: 파일 I/O, 메모리 락, CRT 라이브러리 호출이 DLL 내부에 완벽히 봉인되어 Go Cgo 링크 시 심볼 충돌이 100% 해소됩니다.
2. **`/MT` (Static CRT) 링킹**: DLL을 `/MT`로 빌드하여 사용자의 윈도우 PC에 `vcruntime140.dll` 등 추가 런타임이 없어도 단독 동작합니다.
3. **자가 추출(Self-Extracting) 번들링**: LionSQL 바이너리 내부에 DLL을 `//go:embed`로 내장하고, 기동 시 로컬 디렉토리로 자동 전개하여 **단일 실행 파일(`.exe`) 배포(`[RULE:StaticBinary]`)**를 완벽하게 유지합니다.

---

## 2. 세부 구현 아키텍처

```
[ GitHub Actions CI: windows-latest ]
   ├── Visual Studio 2022 (MSVC cl.exe)
   ├── CMake + Ninja
   │      │ (WiredTiger 11.3.1 소스 빌드 /MT)
   │      ▼
   ├── wiredtiger.dll (모든 CRT 종속성이 캡슐화된 순수 PE 바이너리)
   └── libwiredtiger.dll.a (MinGW Cgo 호환 임포트 라이브러리)
          │
          ▼
[ LionSQL 소스 저장소: pkg/storage/wiredtiger/lib/windows_amd64/ ]
   ├── include/wiredtiger.h
   ├── libwiredtiger.dll.a
   └── wiredtiger.dll
          │
          ▼
[ Go 빌드: go build -tags wiredtiger ./cmd/lionsql ]
   ├── wt_engine.go (#cgo windows,amd64 지시어 바인딩)
   └── dll_loader_windows.go (기동 시 DLL 존재 확인 및 자가 추출/로딩)
          │
          ▼
[ 최종 배포 산출물: bin/lionsql.exe ]
   (사용자는 별도 DLL이나 Visual Studio 설치 없이 .exe 하나만으로 WiredTiger B+Tree 엔진 즉시 실행)
```

---

## 3. 구현 단계별 태스크 (Implementation Steps)

### 3.1 GitHub Actions 워크플로우 (`.github/workflows/build-windows-wiredtiger.yml`)
- `windows-latest` 러너 기반으로 공식 MSVC 빌드 파이프라인 구축.
- CMake 플래그:
  - `-DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreaded` (`/MT` 정적 CRT)
  - `-DENABLE_SHARED=1 -DENABLE_STATIC=0`
  - `-DENABLE_PYTHON=0 -DENABLE_SNAPPY=0 -DENABLE_LZ4=0 -DENABLE_ZLIB=0 -DENABLE_ZSTD=0`
- `gendef` 및 `dlltool`을 사용해 MinGW 임포트 라이브러리 `libwiredtiger.dll.a` 자동 생성.
- Go Cgo 컴파일 및 `go test -tags wiredtiger ./pkg/storage/wiredtiger` 자동 검증.
- 산출물 아티팩트(`wiredtiger.dll`, `libwiredtiger.dll.a`, `lionsql.exe`) 자동 업로드.

### 3.2 윈도우 Cgo 지시어 및 헤더 정비 (`pkg/storage/wiredtiger/wt_engine.go`)
- `wt_engine.go`에 `#cgo windows,amd64` 지시어 추가:
  ```go
  #cgo windows,amd64 CFLAGS: -I${SRCDIR}/lib/windows_amd64/include
  #cgo windows,amd64 LDFLAGS: -L${SRCDIR}/lib/windows_amd64 -lwiredtiger
  ```

### 3.3 윈도우 DLL 자가 추출기 (`pkg/storage/wiredtiger/dll_loader_windows.go`)
- 윈도우 실행 파일이 실행될 때 `wiredtiger.dll`의 경로를 감지하고, 필요 시 `%LOCALAPPDATA%\LionSQL\bin\`으로 원자적 추출.
- Windows `SetDllDirectoryW` API를 호출하여 OS DLL 로더 경로에 안전하게 등록.

---

## 4. 기대 효과 (Impact)

1. **윈도우 WiredTiger 완전 정상화**: 윈도우 개발자/사용자도 macOS/Linux와 100% 동일한 B+Tree MVCC 스토리지 엔진을 사용할 수 있게 됨 (`[RULE:HermeticParity]`).
2. **단일 바이너리 철학 유지 (`[RULE:StaticBinary]`)**: 별도의 설치 프로그램이나 외부 DLL 배포 없이 단일 `.exe` 실행 파일 경험 보장.
3. **유지보수 자동화**: WiredTiger 버전 업그레이드 시 GitHub Actions 워크플로우를 원클릭 실행하는 것만으로 최신 윈도우 DLL과 임포트 라이브러리가 자동 갱신됨.
