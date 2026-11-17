# 데모 시드 스크립트(demo_seed.sql) 단일 바이너리 내장 및 Web Studio 일치화 아키텍처 플랜 (`EMBEDDED_DEMO_SEED_UNIFICATION_PLAN.ko.md`)

## 1. 배경 및 문제 정의

LionSQL은 외부 의존성이 전혀 없는 단일 정적 실행 파일(Single Standalone Executable)을 지향하지만, 데모 시드 데이터와 관련하여 다음과 같은 두 가지 결함과 불일치가 존재했습니다:

1. **외부 파일시스템 경로 의존성 결함**:
   - `cmd/lionsql/server.go`에서 서버 기동 시 `-seed` 플래그를 처리할 때 `os.ReadFile("scripts/demo_seed.sql")`을 직접 호출합니다.
   - 사용자가 컴파일된 단일 바이너리(`bin/lionsql` 또는 `bin/lionsql.exe`)를 Git 저장소 외부의 임의 디렉터리나 프로덕션 서버로 이동하여 실행하면, `scripts/demo_seed.sql`을 찾지 못해 시딩이 실패합니다 (`[RULE:StaticBinary]` 위배).

2. **시드 데이터 이원화 및 사용자 경험 파편화**:
   - CLI의 `-seed` 플래그는 `scripts/demo_seed.sql` (전자상거래 및 은행 도메인: `customers`, `accounts`, `products`, `sales`, `sales_details` 5개 테이블, 34건 레코드, `TransferFunds`, `ProcessOrder` 2개 T-SQL 저장 프로시저)을 로드합니다.
   - 반면 Web Studio의 **Seed Sample Data** 버튼(`POST /api/seed`)은 `pkg/api/handlers_records.go` 내부에 Go 코드로 하드코딩된 축소판 데이터(`accounts`, `customers` 2개 테이블, 7건 레코드)를 별도로 생성합니다.
   - 이로 인해 공식 가이드 문서(`doc/WEB_STUDIO_GUIDE.ko.md`)에 기술된 카탈로그 구성(`products`, `sales` 등)과 Web Studio 실제 시딩 결과가 불일치하는 문제가 발생합니다.

---

## 2. 목표 아키텍처 (Target Architecture)

```
[scripts/demo_seed.sql] <== (동일성 검증 테스트) ==> [pkg/seed/demo_seed.sql]
                                                               ||
                                                        //go:embed demo_seed.sql
                                                               ||
                                                      [pkg/seed/seed.go]
                                                      (seed.DemoSeedSQL)
                                                               ||
                                      +------------------------+------------------------+
                                      |                                                 |
                                      v                                                 v
                       [cmd/lionsql/server.go]                               [pkg/api/handlers_records.go]
                     (CLI: ./lionsql serve -seed)                               (Web Studio: POST /api/seed)
                                      |                                                 |
                                      +------------------------+------------------------+
                                                               |
                                                               v
                                                 [engine.Coordinator.ExecuteSQL]
                                                               |
                                                               v
                                                    [Single Binary Kernel]
                                             5 Tables, 34 Records, 2 Procedures
```

1. **`pkg/seed` 패키지 구축 및 `//go:embed` 바인딩**:
   - `pkg/seed/demo_seed.sql` 파일을 배치하고, `pkg/seed/seed.go`에서 `//go:embed demo_seed.sql`을 통해 `seed.DemoSeedSQL` 변수로 컴파일 시점에 바이너리에 정적 포함합니다.
   - 메타데이터 상수 정의: `DemoTables`, `DemoProcedures`, `DemoRecordCount (34)`.

2. **단일 진실 공급원(SSOT) 통합**:
   - `cmd/lionsql/server.go`: `os.ReadFile`을 제거하고 `server.ExecuteSQL(seed.DemoSeedSQL)`로 직결.
   - `pkg/api/handlers_records.go`: 하드코딩된 Go 테이블 생성을 제거하고, 빈 데이터베이스 검증(Empty DB Guard) 후 `s.coordinator.ExecuteSQL(seed.DemoSeedSQL)`을 호출하도록 통합.

3. **Web Studio UI 및 클라이언트 피드백 갱신**:
   - `pkg/ui/dashboard.html`의 툴팁, 토스트 메시지 및 테이블 로드 대상을 통합 스키마에 맞게 갱신.

4. **무결성 회귀 테스트 체계**:
   - `scripts/demo_seed.sql`과 `pkg/seed/demo_seed.sql`의 바이트 단위 100% 동일성을 보장하는 유닛 테스트 추가.
   - `pkg/api/`의 기존 테스트 스위트가 통합 시드 스키마와 완벽히 정합하도록 조정.

---

## 3. 세부 변경 계획

| 변경 파일 | 변경 내용 |
| :--- | :--- |
| `pkg/seed/demo_seed.sql` | `scripts/demo_seed.sql`의 마스터 복사본 배치 |
| `pkg/seed/seed.go` | `//go:embed demo_seed.sql` 및 스키마 메타데이터 정의 |
| `pkg/seed/seed_test.go` | `scripts/demo_seed.sql`과의 바이트 무손실 동일성 검증 테스트 |
| `cmd/lionsql/server.go` | `os.ReadFile` 제거, `seed.DemoSeedSQL` 직결 호출 |
| `pkg/api/handlers_records.go` | `handleSeed`에서 `s.coordinator.ExecuteSQL(seed.DemoSeedSQL)` 실행 및 응답 표준화 |
| `pkg/ui/dashboard.html` | Seed Sample Data 카드 텍스트 및 토스트 알림 메시지 최신화 |
| `pkg/api/server_test.go` | `/api/seed` 응답 및 레코드 검증 테스트를 통합 스키마 기준으로 동기화 |
| `pkg/api/realtime_test.go` | 시드 후 계좌 레코드 추가 테스트의 컬럼 정합성 동기화 |

---

## 4. 3단계 검증 파이프라인

1. **Build**:
   - macOS 호스트: `go build -buildvcs=false -o bin/lionsql ./cmd/lionsql`
   - Windows 64-bit: `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o bin/lionsql.exe ./cmd/lionsql`
2. **Test**:
   - `go test -v -race ./pkg/seed/... ./pkg/api/... ./pkg/engine/...` (100% PASS)
3. **Commit**:
   - Conventional Commits 형식 준수 및 장식용 이모지 배제 (`[RULE:StyleIntegrity]`)
