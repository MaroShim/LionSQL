# LionSQL v0.90.1 릴리즈 노트 (`notes/RELEASE_NOTES_v0.90.1.ko.md`)

> - **최종 갱신일**: 2026-11-04
> - **최초 작성일**: 2026-11-04
> - **릴리즈 버전**: `v0.90.1` (Patch Release)
> - **마일스톤 주제**: **T-SQL Stored Procedure & 재귀 CTE 엔진 무결성 완치, SQLLogicTest 공식 코퍼스 정비, Web Studio 인라인 그리드 혁신**

---

## 1. 릴리즈 하이라이트 개요

LionSQL `v0.90.1`은 `v0.90.0` GA 이후 현업 엔터프라이즈 환경에서 사용되는 대규모 절차형 로직(AWS Babelfish 2,900-Suite)과 SQLite 국제 표준 SQLLogicTest 공식 코퍼스 전수를 엔진에 직접 교차 검증하며 도출된 **핵심 관계형 엔진의 수학적/구조적 난제를 완벽하게 해결**한 패치 릴리즈입니다.

- **표준 SQL 재귀 CTE(`WITH RECURSIVE`) 델타 시맨틱 정복**:
  - 누적 테이블 반복 참조로 인한 무한 행 증폭 현상을 직전 스텝 델타 작업 테이블(`workTempName`) 격리 주입으로 원천 차단 (수천 번의 무한 루프에서 단 4ms 만에 수학적으로 정상 수렴).
- **T-SQL Stored Procedure 호출 규약 및 수명주기 완비**:
  - `EXEC @ret = procName` 리턴 상태 코드 바인딩 지원.
  - `@param AS INT = 100`, `@p VARCHAR(20) NULL` 등 파라미터 관용 문법 지원.
  - `DROP FUNCTION` / `DROP FUNCTION IF EXISTS` 카탈로그 연동 및 UDF 캐시 동적 무효화 파이프라인 안착.
- **AWS Babelfish 2,900-Suite 실전 검증 완주**:
  - 2,900개 파일과 116,884개 배치를 크래시/패닉 없이 단 12초~2분 내에 완주하며, **68,000개에 육박하는 배치 정상 통과(Batch Pass Rate 58.16%)** 달성.
  - 관련 기술 분석 및 디버깅 여정을 [`notes/BABELFISH_INTEGRATION_REPORT.ko.md`](BABELFISH_INTEGRATION_REPORT.ko.md)에 상세 수록.
- **SQLLogicTest (SLT) 107개 공식 코퍼스 및 절차형 스위트 14종 영구 정착**:
  - SQLite 공식 대규모 스위트(`full`, `evidence`, `random`, `index`) 107개 전수 정착.
  - Stored Procedure 회귀 방지를 위한 14개 절차형 스위트(`test/slt_proc/`) 신설 및 **100% PASS (Race 0)** 달성.
- **Web Studio UX 혁신 (엑셀 스타일 인라인 그리드)**:
  - 모달 팝업 대신 테이블 최하단에 스프레드시트 스타일 행 생성(`startInlineNewRow`) 및 키보드 내비게이션(`Enter` 저장, `Esc` 취소, `Tab` 이동) 지원.

---

## 2. 세부 변경 사항

### 2.1. 쿼리 및 절차형 엔진 (Query & Procedural Engine)
- **재귀 CTE 델타 작업 테이블 도입 (`pkg/engine/sqlexec_explain.go`)**:
  - `WITH RECURSIVE` 실행 시 직전 재귀 스텝에서 새로 산출된 델타 행들만 다음 반복의 입력 테이블(`workTempName`)로 주입하도록 SQL 표준 시맨틱 확립 (`BABEL-3392`, `BABEL-RECURSIVE-CTE` 무한루프 영구 해소).
- **VDBE VM 협력적 타임아웃 컨텍스트 전파 (`pkg/engine/sqlexec_select.go`)**:
  - `ExecuteInTxContext(ctx, ...)`를 통해 상위 컨텍스트 데드라인을 VDBE 실행 루프에 연결하여 의도적인 초장기 루프나 무한 쿼리가 발생해도 안전하게 조기 탈출하도록 방어벽 강화 (`[RULE:CooperativeAbort]`).
- **T-SQL 파라미터 선언 유연성 확대 (`pkg/proc/parser_ddl.go`)**:
  - 파라미터 기본값 지정 시 `TokenAssign` 뿐만 아니라 `TokenEq`(`=`) 문법을 동시 허용하고, `AS <type>`, `NULL` 속성 선언을 관용적으로 수용.
- **프로시저 실행 상태 코드 반환 바인딩 (`pkg/proc/parser_stmt.go`)**:
  - `EXEC @ret = procName` 구문 파싱 및 VDBE 런타임 반환 레지스터 할당 로직 완비.
- **UDF 동적 라이프사이클 관리 (`pkg/engine/sqlexec_ddl.go`, `pkg/proc/runtime/vm.go`)**:
  - `DROP FUNCTION` 구문 지원 및 엔진 내 UDF 등록 해제(`executor.UnregisterUDF`), 플랜 캐시 무효화 파이프라인 구현.

### 2.2. 테스트 자동화 및 검증 도구 (Test & Verification)
- **Babelfish 전용 고성능 러너 개발 (`cmd/babelfish_runner`)**:
  - 16 워커 병렬 실행, 개별 3초 타임아웃 안전망 채널 가드, 실시간 프로그레스 모니터링 탑재.
  - 기대 오류 주석(`-- should fail`, `-- expected error` 등) 식별 메커니즘을 적용하여 SQLLogicTest와 일관된 PASS/FAIL 판정 기반 마련.
- **SQLLogicTest 절차형 스위트 신설 (`test/slt_proc/`)**:
  - `10_babelfish_proc_params.slt`: 파라미터 기본값 및 관용 문법 검증
  - `11_babel_iterative_exec_return.slt`: 루프 내 반복 실행 및 리턴 코드 바인딩 검증
  - `12_babel_xp_qv.slt`: xp_qv 확장 및 쿼리 제어 검증
  - `13_recursive_cte_delta.slt`: 정수/문자열 재귀 및 계층 트리 탐색 델타 시맨틱 검증
  - `14_drop_function_and_params.slt`: `DROP FUNCTION` 및 동적 UDF 무효화 검증

### 2.3. 임베디드 웹 스튜디오 (Web Studio UX)
- **인라인 스프레드시트 데이터 입력 모드 (`pkg/ui/dashboard.html`)**:
  - 기존의 화면 가림과 컨텍스트 단절을 유발하던 모달 팝업 입력창을 배제하고, 기존 행들을 보면서 직관적으로 데이터를 추가할 수 있는 엑셀 스타일 인라인 행 생성 인터랙션 도입.
  - 키보드 친화적 단축키 바인딩 및 자동 스크롤/포커스 복원 기능 탑재.

---

## 3. 검증 결과 요약

1. **3-Stage 검증 파이프라인 통과 (`[RULE:PipelineVerification]`)**:
   - macOS(darwin-arm64), Linux(linux-amd64), Windows(windows-amd64) 3대 플랫폼 공식 릴리즈 바이너리 무결성 100% 빌드 성공.
   - `go test -tags wiredtiger -count=1 -race ./pkg/... ./test/slt_proc/...` 100% 통과 (0 Data Races).
2. **Babelfish 2,900-Suite 실측 지표**:
   - 총 배치 수: 116,884개
   - 통과 배치 수: **67,981개** (배치 통과율 58.16%)
   - 소요 시간: 12.12초 ~ 2분 27초 (초당 수천~1만 배치 처리)
   - 크래시/패닉/메모리 누수: **0건**
