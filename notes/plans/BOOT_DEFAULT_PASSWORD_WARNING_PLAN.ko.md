# 초기 슈퍼유저 패스워드 미변경 시 최후행 대문자 보안 경고 아키텍처 계획서 (BOOT_DEFAULT_PASSWORD_WARNING_PLAN)

> - **최초 작성일**: 2026-11-07
> - **상태**: Draft / Review
> - **연관 태스크**: `SEC-BOOT-DEFAULT-PASS-WARN` (P0 긴급 보안 가드)
> - **불변식 원칙**: `[RULE:ExplicitError]`, `[RULE:RadicalKISS]`, `[RULE:DeterministicTesting]`

---

## 1. 개요 및 배경

### 1.1 현행 구현의 문제점 및 한계
LionSQL은 관리자 계정(`admin@lionsql.local`)의 초기 비밀번호(`1234567890`)에 대해 두 가지 검사를 수행하고 있었습니다:
1. `cfg.ValidateSecurityGuard()` (`cmd/lionsql/server.go:336-357`): 외부 IP(`0.0.0.0` 등) 바인딩 시 기동 즉시 중단(`os.Exit(1)`).
2. 배너 안내문 (`cmd/lionsql/server.go:863-868`):
   ```go
   if cfg.Auth.AdminPass == "1234567890" {
       fmt.Println("--------------------------------------------------------------------------------")
       fmt.Println(" [SECURITY NOTICE] Default administrator credentials active on localhost (127.0.0.1).")
       fmt.Println("                   (admin@lionsql.local / 1234567890)")
       fmt.Println("                   Set -admin-pass or LIONSQL_ADMIN_PASS before exposing to external networks.")
   }
   fmt.Println("--------------------------------------------------------------------------------")
   fmt.Println("Press Ctrl+C to stop server")
   ```

**현행 한계**:
- **가시성 상실**: 안내문 뒤에 구분선(`---`)과 `Press Ctrl+C to stop server`가 추가로 출력되어 안내문이 중간으로 밀립니다.
- **비동기 출력 레이스**: 배너가 `go func() { ... }()` 내부에서 비동기로 출력되어 네트워크 리스너의 기동 로그(`log.Printf`)와 뒤섞이며 화면 상단으로 스크롤되어 사라집니다.
- **어조의 긴급성 부족**: 소문자/혼합 표기의 안내(`[SECURITY NOTICE]`) 수준에 머물러 있어 운영자에게 보안 조치 긴급성을 각인시키지 못합니다.
- **로그 및 데몬 누락**: 파일 로깅이나 데몬 실행 시 로그 파일 최후행에 해당 경고가 확정적으로 기록되지 않습니다.

---

## 2. 핵심 설계 요구사항 및 불변식

1. **상시 발동 불변식 (Universal Trigger)**:
   - 포트 옵션(`-http`, `-pg`, `-tds` 온/오프)이나 실행 방식(포그라운드, 백그라운드 데몬, Windows 서비스 등)과 무관하게,
   - 슈퍼유저 패스워드가 기본값(`1234567890`)인 경우 반드시 발동.
2. **최후행의 절대 보장 (The Very Last Line Invariant)**:
   - 부팅 배너 및 기동 완료 로그의 **가장 마지막 줄(The Very Last Line)**에 출력.
   - `Press Ctrl+C to stop server`나 구분선(`---`)보다도 아래, 즉 터미널과 로그의 절대적 종결 행에 위치.
3. **100% 대문자 강조 (ALL CAPS Command Notice)**:
   - 소문자 없이 시각적 충격을 주는 대문자 단일 라인 포맷.
   - 비밀번호 리터럴(값) 자체는 로그 유출(CWE-532) 방지를 위해 절대 포함하지 않음.
   - 표준 상수:
     ```text
     WARNING: DEFAULT SUPERUSER PASSWORD HAS NOT BEEN CHANGED! CHANGE PASSWORD IMMEDIATELY TO PREVENT UNAUTHORIZED ACCESS!
     ```
4. **동기화 출력 보장 (Synchronized Flush)**:
   - 리스너 기동 완료 후 최종 기동 상태 배너의 최하단 및 로거에 플러시하여 레이스 컨디션 방지.

---

## 3. 구현 세부 계획

### 3.1 상수 정의 (`pkg/config/auth.go` 또는 `cmd/lionsql/server.go`)
```go
const DefaultAdminPass = "1234567890"

const DefaultAdminPassWarning = "WARNING: DEFAULT SUPERUSER PASSWORD HAS NOT BEEN CHANGED! CHANGE PASSWORD IMMEDIATELY TO PREVENT UNAUTHORIZED ACCESS!"
```

### 3.2 배너 및 로그 출력 리팩토링 (`cmd/lionsql/server.go`)
- 기존 863-870행의 조건부 블록 교체:
```go
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("Press Ctrl+C to stop server")
	if cfg.Auth.AdminPass == DefaultAdminPass {
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Println(DefaultAdminPassWarning)
		log.Println(DefaultAdminPassWarning)
	}
```
- 화면(stdout)과 로거(log) 모두에 최후행으로 출력되도록 구성.

### 3.3 데몬 프로세스 기동 메시지 연계 (`cmd/lionsql/daemon.go`)
- 데몬 실행 성공 안내 콘솔 출력 시에도 기본 패스워드 상태인 경우 해당 대문자 경고를 최후행에 병기:
```go
	if adminPass == DefaultAdminPass {
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Println(DefaultAdminPassWarning)
	}
```

---

## 4. TDD 회귀 검증 계획 (3-Stage Verification)

1. **Red 테스트 작성 (`cmd/lionsql/server_banner_test.go`)**:
   - `runServer` 또는 배너 렌더러 함수에 버퍼를 주입하여 출력 캡처.
   - 케이스 1: 기본 비밀번호(`1234567890`) 기동 시, 캡처된 출력의 **가장 마지막 비공백 라인**이 정확히 `DefaultAdminPassWarning`과 일치하는지 검증 (`strings.TrimSpace(lines[len(lines)-1]) == DefaultAdminPassWarning`).
   - 케이스 2: 커스텀 비밀번호(`-admin-pass="MySecretP@ss!"`) 기동 시, 해당 경고가 출력되지 않고 정상 종료 문구가 마지막인지 검증.
2. **Green 구현**:
   - `cmd/lionsql/server.go` 및 `cmd/lionsql/daemon.go`에 최후행 출력 로직 반영.
3. **3-Stage Pipeline**:
   - `go build -tags wiredtiger -buildvcs=false -o bin/lionsql ./cmd/lionsql`
   - `go test -tags wiredtiger -v -race ./cmd/lionsql/...`
   - Git Commit (Conventional Commits, No Emojis).
