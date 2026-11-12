# LionSQL 내부 아키텍처: 가상 데이터베이스 엔진(VDBE) 및 바이트코드 명세서 (`doc/internal/VDBE.ko.md`)

> - **최초 작성일**: 2026-10-15
> - **최종 갱신일**: 2026-11-12

---

이 문서는 LionSQL의 네이티브 T-SQL 바이트코드 가상 머신(VDBE), 컴파일러 파이프라인, 레지스터 할당 전략, Opcode 명령어 셋 아키텍처(ISA), 그리고 절차적 런타임 환경의 내부 구조를 상세히 기술합니다.

---

## 1. 가상 머신 아키텍처 개요

LionSQL은 SQLite의 아키텍처에서 영감을 얻은 레지스터 기반 VDBE(Virtual Database Engine)를 통해 T-SQL 저장 프로시저, 트리거 및 사용자 정의 함수를 고속 실행합니다. 스택 기반 VM이나 무거운 JavaScript 엔진(Goja 등)과 달리, LionSQL VDBE는 풀링된 고정 레지스터 배열과 기본 원시값 직접 연산을 통해 힙 메모리 할당을 극단적으로 최소화합니다.

```
[T-SQL 소스 스크립트]
         │
         ▼
[어휘 및 구문 파서 (Native Pratt / ANTLR4)]
         │
         ▼
[통합 AST: *proc.CreateProcStmt]
         │
         ▼
[바이트코드 컴파일러 (pkg/proc/compiler.go)]
         │
         ▼
[바이트코드 프로그램: []proc.Instruction]
         │
         ├── 명령어 스트림 (선형 Opcode 벡터)
         ├── 문자열 및 상수 리터럴 풀
         └── 레지스터 할당 테이블
         │
         ▼
[가상 머신 실행기 (pkg/proc/runtime/vm.go)]
         ├── 프로그램 카운터 (IP)
         ├── 레지스터 메모리 ([32]storage.Value, sync.Pool 재활용)
         ├── 앰비언트 ACID 스토리지 트랜잭션 (storage.Transaction)
         ├── 커서 상태 테이블 (활성 테이블 커서)
         └── 콜스택 및 예외 프레임 (TRY...CATCH 블록)
```

---

## 2. 레지스터 할당 및 메모리 재활용

핫패스에서의 제로 알로케이션(`[RULE:ZeroAllocation]`)을 달성하기 위해, VDBE는 Go 언어의 `sync.Pool`을 통해 레지스터 배열을 적극 재활용합니다:

```go
var registerPool = sync.Pool{
    New: func() any {
        regs := make([]storage.Value, 64)
        return &regs
    },
}
```

### 레지스터 규칙
- **`R[0]`**: 반환 코드 및 상태 저장용 예약 레지스터.
- **`R[1]` ~ `R[15]`**: 프로시저 인자 매핑 매개변수 레지스터 (`@from`, `@to`, `@amount` 등).
- **`R[16]` ~ `R[31]`**: `DECLARE @var TYPE`으로 선언된 지역 변수 레지스터.
- **`R[32]+`**: 중간 산술 연산, 문자열 연결, 비교 평가를 위한 임시 스크래치 레지스터.

---

## 3. 명령어 셋 아키텍처(ISA) 및 Opcode 레퍼런스

VDBE의 명령어 포맷은 CPU L1 데이터 캐시 지역성을 극대화하기 위해 16바이트 패킹 구조(제로 패딩)로 설계되었습니다 (64바이트 L1 캐시 라인당 정확히 4개 명령어 배치):

```go
type OpCode uint8

type Instruction struct {
    Op    OpCode // 동작 코드 (1 byte)
    Flags uint8  // 부정, 조건 반전, Nullability 플래그 (1 byte)
    Aux   uint16 // Program.AuxData 보조 인덱스 또는 특화 페이로드 (2 bytes)
    A     int32  // 레지스터 A / 점프 대상 PC / 반환값 레지스터 (4 bytes)
    B     int32  // 레지스터 B / 슬롯 인덱스 / 상수 인덱스 / 점프 PC (4 bytes)
    C     int32  // 레지스터 C / 결과 레지스터 / 추가 피연산자 (4 bytes)
} // 정확히 16 바이트 (Zero Padding)
```

> **아키텍처 불변식 (16바이트 고정 레이아웃 vs 8바이트 압축)**:
> `Instruction` 구조체를 8바이트로 축소하는 방안(`int16` 피연산자 또는 `uint64` 비트패킹)은 영구 기각(Spec-Out)되었습니다. 일반적인 OLTP 프로시저(15~100 명령어, 240B~1.6KB)는 이미 32KB~64KB L1 데이터 캐시 내에 100% 상주합니다. 반면 `int16`은 점프 오프셋과 즉시 정수값을 32,767로 제한하여 대형 스크립트를 파괴하며, `uint64` 비트패킹은 매 디스패치마다 시프트/마스크 연산 오버헤드를 유발합니다. 상세 내용은 [`notes/plans/VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md`](../../notes/plans/VDBE_VM_EXTREME_OPTIMIZATION_PLAN.ko.md) 및 [`notes/VDBE_BYTECODE_DIRECTORY.ko.md`](../../notes/VDBE_BYTECODE_DIRECTORY.ko.md)를 참조하십시오.

### 3.1. 초기화 및 제어 흐름 Opcode

| Opcode | 피연산자 | 설명 |
|---|---|---|
| `OpInit` | `P1=target_ip` | VM 상태 초기화, IP를 `P1`로 설정, 실행 레지스터 리셋 |
| `OpHalt` | `P1=exit_code` | VM 실행 종료 및 `P1`을 종료 코드로 반환 |
| `OpJump` | `P1=target_ip` | 무조건 점프; `IP = P1` 설정 |
| `OpJumpIfTrue` | `P1=reg, P2=target_ip` | 불리언 레지스터 `R[P1]`이 true이면 `P2`로 점프 |
| `OpJumpIfFalse` | `P1=reg, P2=target_ip` | 불리언 레지스터 `R[P1]`이 false 또는 null이면 `P2`로 점프 |
| `OpReturn` | `P1=reg` | 현재 프레임에서 복귀하며 `R[P1]`을 반환값으로 전달 |

### 3.2. 레지스터 로딩 및 상수 Opcode

| Opcode | 피연산자 | 설명 |
|---|---|---|
| `OpInteger` | `P1=val, P2=target_reg` | 64비트 정수 `P1`을 `R[P2]`에 로드 |
| `OpString` | `P2=target_reg, P4=str` | 문자열 리터럴 `P4`를 `R[P2]`에 로드 |
| `OpReal` | `P2=target_reg, P4=float_str` | 부동소수점 리터럴 `P4`를 파싱하여 `R[P2]`에 로드 |
| `OpNull` | `P1=type, P2=target_reg` | `R[P2]`를 지정된 타입의 `NULL`로 설정 |
| `OpCopy` | `P1=src_reg, P2=target_reg`| `R[P1]`의 값을 `R[P2]`로 복사 |

### 3.3. 산술 및 비교 Opcode

| Opcode | 피연산자 | 설명 |
|---|---|---|
| `OpAdd` | `P1=src1, P2=src2, P3=dst` | `R[dst] = R[src1] + R[src2]` (정수 ➔ 실수 자동 타입 승격 지원) |
| `OpSubtract` | `P1=src1, P2=src2, P3=dst` | `R[dst] = R[src1] - R[src2]` |
| `OpMultiply` | `P1=src1, P2=src2, P3=dst` | `R[dst] = R[src1] * R[src2]` |
| `OpDivide` | `P1=src1, P2=src2, P3=dst` | `R[dst] = R[src1] / R[src2]`. `R[src2] == 0`인 경우 분모 0 오류 발생 |
| `OpEq` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] == R[src2])` |
| `OpNe` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] != R[src2])` |
| `OpLt` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] < R[src2])` |
| `OpLe` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] <= R[src2])` |
| `OpGt` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] > R[src2])` |
| `OpGe` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] >= R[src2])` |

### 3.4. 스토리지 및 트랜잭션 Opcode

| Opcode | 피연산자 | 설명 |
|---|---|---|
| `OpOpenRead` | `P1=cursor_id, P4=table` | 대상 테이블에 읽기 전용 커서 개방 |
| `OpOpenWrite` | `P1=cursor_id, P4=table` | 대상 테이블에 읽기/쓰기 커서 개방 |
| `OpIndexScan` | `P1=cursor_id, P4=table` | 보조 인덱스를 사용하여 점 또는 범위 조건 레코드 스캔 |
| `OpColumn` | `P1=cursor_id, P2=dst, P4=col`| 현재 커서 행에서 지정 컬럼 값을 추출하여 `R[P2]`에 저장 |
| `OpInsert` | `P1=cursor_id, P2=rec_reg` | 레코드 맵을 테이블에 삽입 (스키마 검증 및 Identity 추적) |
| `OpUpdate` | `P1=cursor_id, P2=pk, P3=rec` | 기본키 `R[pk]` 행을 새 레코드 `R[rec]`로 갱신 |
| `OpDelete` | `P1=cursor_id, P2=pk` | 기본키 `R[pk]` 행 삭제 |
| `OpRelQuery` | `P4=sql` | 매개변수가 바인딩된 관계형 쿼리를 Volcano 파이프라인으로 디스패치 |
| `OpCommit` | - | 활성 앰비언트 트랜잭션 커밋 |
| `OpRollback` | - | 활성 앰비언트 트랜잭션 롤백 |

---

## 4. 컴파일 및 디스어셈블리 예제

다음과 같은 T-SQL 프로시저가 주어졌을 때:

```sql
CREATE PROCEDURE DebitAccount
    @acc_id INT,
    @amount INT
AS
BEGIN
    DECLARE @bal INT;
    SELECT balance INTO @bal FROM accounts WHERE id = @acc_id;

    IF @bal < @amount
    BEGIN
        RAISERROR('Insufficient balance');
        RETURN -1;
    END;

    UPDATE accounts SET balance = balance - @amount WHERE id = @acc_id;
    RETURN 0;
END;
```

컴파일러는 다음과 같은 선형 VDBE 바이트코드 명령어를 생성합니다:

```
ADDR  OPCODE          P1    P2    P3    P4                COMMENT
---------------------------------------------------------------------------------
00    OpInit          01    00    00                      ; 프로그램 시작
01    OpOpenRead      01    00    00    "accounts"        ; 커서 1 읽기 개방
02    OpGet           01    01    16                      ; R[1]의 PK(@acc_id) 탐색, R[16](@bal)에 로드
03    OpLt            16    02    32                      ; R[32] = R[16] < R[2] (@bal < @amount)
04    OpJumpIfFalse   32    08    00                      ; 잔액 충분 시 08번 주소로 점프
05    OpRaiseError    16    00    00    "Insufficient..." ; 에러 발생
06    OpInteger       -1    00    00                      ; R[0] = -1
07    OpHalt          -1    00    00                      ; 종료 코드 -1로 종료
08    OpSubtract      16    02    33                      ; R[33] = R[16] - R[2] (차감액 계산)
09    OpOpenWrite     01    00    00    "accounts"        ; 커서 1 쓰기 개방
10    OpUpdateField   01    01    33    "balance"         ; balance 갱신
11    OpCommit        00    00    00                      ; 트랜잭션 커밋
12    OpInteger       00    00    00                      ; R[0] = 0
13    OpHalt          00    00    00                      ; 정상 종료 코드 0으로 종료
```

---

## 5. 앰비언트 트랜잭션 통합 및 단일 엔진 실행

1. **자동 앰비언트 트랜잭션**: 프로시저 실행 시 활성 트랜잭션이 없으면 자동으로 트랜잭션을 시작합니다.
2. **미처리 에러 시 원자적 롤백**: `TRY...CATCH` 블록 외부에서 `OpRaiseError`가 실행되면 VM이 작업을 즉시 중단하고 `tx.Rollback()`을 호출합니다.
3. **단일 엔진 DQL & DML 통합**: 인터랙티브 쿼리(`SELECT`, `INSERT`, `UPDATE`, `DELETE`)와 프로시저가 모두 동일한 VDBE 가상 머신 상에서 실행됩니다.
4. **와이어 프로토콜 바이트코드 캐싱**: PostgreSQL Wire(`Parse`) 및 TDS(`sp_prepare`)에서 프리페어드 스테이트먼트 생성 시 컴파일된 `*proc.Program` 바이트코드를 세션 캐시에 보관하여, 후속 `Execute` 시 어휘 분석 및 구문 트리 생성을 100% 우회합니다.
