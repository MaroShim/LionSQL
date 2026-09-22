# SQLion Internals: Virtual Database Engine (VDBE) & Bytecode Specification

This document describes the internal architecture of SQLion's native T-SQL Bytecode Virtual Machine, compiler pipeline, register allocation strategy, opcode instruction set architecture (ISA), and procedural runtime environment.

---

## 1. Virtual Machine Architecture Overview

SQLion executes T-SQL stored procedures, triggers, and user-defined functions using a register-based Virtual Database Engine (VDBE) inspired by SQLite. Unlike stack-based virtual machines or JavaScript runtimes (e.g. Goja), SQLion's VDBE minimizes heap allocations through pooled register arrays and direct primitive evaluation.

```
[T-SQL Source Script]
         │
         ▼
[Lexer & Parser (Native Pratt / ANTLR4)]
         │
         ▼
[Unified AST: *proc.CreateProcStmt]
         │
         ▼
[Bytecode Compiler (pkg/proc/compiler.go)]
         │
         ▼
[Bytecode Program: []proc.Instruction]
         │
         ├── Instructions Stream (Linear Opcode Vector)
         ├── String & Constant Literals Pool
         └── Register Allocation Table
         │
         ▼
[Virtual Machine Executor (pkg/proc/runtime/vm.go)]
         ├── Program Counter (IP)
         ├── Register Memory ([32]storage.Value from sync.Pool)
         ├── Ambient ACID Storage Transaction (storage.Transaction)
         ├── Cursor State Table (Active Table Cursors)
         └── Call Stack & Exception Frame (TRY...CATCH Blocks)
```

---

## 2. Register Allocation & Memory Recycling

To achieve zero-allocation hot paths (Golden Rule 30), the VDBE recycles register arrays using Go's `sync.Pool`:

```go
var registerPool = sync.Pool{
    New: func() any {
        regs := make([]storage.Value, 64)
        return &regs
    },
}
```

### Register Conventions
- **`R[0]`**: Reserved for return code / status.
- **`R[1]` - `R[15]`**: Parameter registers mapped to procedure arguments (`@from`, `@to`, `@amount`).
- **`R[16]` - `R[31]`**: Local variable registers declared via `DECLARE @var TYPE`.
- **`R[32]+`**: Ephemeral scratch registers for intermediate arithmetic, string concatenation, and comparison evaluations.

---

## 3. Instruction Set Architecture (ISA) & Opcode Reference

The VDBE instruction format is defined in `pkg/proc/compiler.go`:

```go
type Opcode int

type Instruction struct {
    Op      Opcode
    P1      int    // First integer operand (e.g. source register, jump target, opcode modifier)
    P2      int    // Second integer operand (e.g. target register, parameter index)
    P3      int    // Third integer operand (e.g. scratch register, comparison mode)
    P4      string // String literal, table name, or column name
    Comment string // Diagnostic comment for disassembly
}
```

### 3.1. Initialization & Flow Control Opcodes

| Opcode | Operands | Description |
|---|---|---|
| `OpInit` | `P1=target_ip` | Initializes VM state, sets IP to `P1`, and resets execution registers. |
| `OpHalt` | `P1=exit_code` | Terminates VM execution and returns `P1` as procedure exit code. |
| `OpJump` | `P1=target_ip` | Unconditional jump; sets `IP = P1`. |
| `OpJumpIfTrue` | `P1=reg, P2=target_ip` | Jumps to `P2` if boolean register `R[P1]` is true. |
| `OpJumpIfFalse` | `P1=reg, P2=target_ip` | Jumps to `P2` if boolean register `R[P1]` is false or null. |
| `OpReturn` | `P1=reg` | Returns control from current frame, passing `R[P1]` as return value. |

### 3.2. Register Loading & Constant Opcodes

| Opcode | Operands | Description |
|---|---|---|
| `OpInteger` | `P1=val, P2=target_reg` | Loads 64-bit signed integer `P1` into `R[P2]`. |
| `OpString` | `P2=target_reg, P4=str` | Loads string literal `P4` into `R[P2]`. |
| `OpReal` | `P2=target_reg, P4=float_str` | Parses float literal `P4` and loads into `R[P2]`. |
| `OpNull` | `P1=type, P2=target_reg` | Sets register `R[P2]` to typed `NULL`. |
| `OpCopy` | `P1=src_reg, P2=target_reg`| Copies value from `R[P1]` into `R[P2]`. |

### 3.3. Arithmetic & Comparison Opcodes

| Opcode | Operands | Description |
|---|---|---|
| `OpAdd` | `P1=src1, P2=src2, P3=dst` | `R[dst] = R[src1] + R[src2]` (handles type promotion INT ➔ FLOAT). |
| `OpSubtract` | `P1=src1, P2=src2, P3=dst` | `R[dst] = R[src1] - R[src2]`. |
| `OpMultiply` | `P1=src1, P2=src2, P3=dst` | `R[dst] = R[src1] * R[src2]`. |
| `OpDivide` | `P1=src1, P2=src2, P3=dst` | `R[dst] = R[src1] / R[src2]`. Raises divide-by-zero error if `R[src2] == 0`. |
| `OpEq` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] == R[src2])`. |
| `OpNe` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] != R[src2])`. |
| `OpLt` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] < R[src2])`. |
| `OpLe` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] <= R[src2])`. |
| `OpGt` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] > R[src2])`. |
| `OpGe` | `P1=src1, P2=src2, P3=dst` | `R[dst] = (R[src1] >= R[src2])`. |

### 3.4. Storage & Transactional Opcodes

| Opcode | Operands | Description |
|---|---|---|
| `OpOpenRead` | `P1=cursor_id, P4=table` | Opens a read-only table cursor on table `P4`. |
| `OpOpenWrite` | `P1=cursor_id, P4=table` | Opens a read-write table handle on table `P4`. |
| `OpColumn` | `P1=cursor_id, P2=dst, P4=col`| Extracts column `P4` from current cursor row into `R[P2]`. |
| `OpInsert` | `P1=cursor_id, P2=rec_reg` | Inserts record map in `R[rec_reg]` into table. |
| `OpUpdate` | `P1=cursor_id, P2=pk, P3=rec` | Updates record with primary key `R[pk]` using record in `R[rec]`. |
| `OpDelete` | `P1=cursor_id, P2=pk` | Deletes record with primary key `R[pk]`. |
| `OpCommit` | - | Commits the active ambient transaction. |
| `OpRollback` | - | Rolls back the active ambient transaction. |

### 3.5. Diagnostics & Exception Handling

| Opcode | Operands | Description |
|---|---|---|
| `OpPrint` | `P1=reg` or `P4=msg` | Appends diagnostic message to procedure output buffer. |
| `OpRaiseError` | `P1=severity, P4=msg` | Raises runtime exception; unwinds stack to nearest `CATCH` frame. |
| `OpTry` | `P1=catch_ip` | Pushes exception frame pointing to `P1` onto exception stack. |
| `OpEndTry` | - | Pops top exception frame from exception stack. |

---

## 4. Compilation & Disassembly Example

Given the following T-SQL procedure:

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

The compiler produces the following linear VDBE instruction vector:

```
ADDR  OPCODE          P1    P2    P3    P4                COMMENT
---------------------------------------------------------------------------------
00    OpInit          01    00    00                      ; Start program
01    OpOpenRead      01    00    00    "accounts"        ; Open cursor 1 for read
02    OpGet           01    01    16                      ; Seek PK in R[1] (@acc_id), load into R[16] (@bal)
03    OpLt            16    02    32                      ; R[32] = R[16] < R[2] (@bal < @amount)
04    OpJumpIfFalse   32    08    00                      ; If not less, jump to ADDR 08
05    OpRaiseError    16    00    00    "Insufficient..." ; Raise error
06    OpInteger       -1    00    00                      ; R[0] = -1
07    OpHalt          -1    00    00                      ; Halt with code -1
08    OpSubtract      16    02    33                      ; R[33] = R[16] - R[2]
09    OpOpenWrite     01    00    00    "accounts"        ; Open cursor 1 for write
10    OpUpdateField   01    01    33    "balance"         ; Set accounts.balance = R[33] WHERE id = R[1]
11    OpCommit        00    00    00                      ; Commit ambient transaction
12    OpInteger       00    00    00                      ; R[0] = 0
13    OpHalt          00    00    00                      ; Halt with code 0
```

---

## 5. Ambient Transaction Integration

VDBE execution operates within an ambient transaction context (`storage.Transaction`):
1. **Implicit Transactions**: A procedure execution automatically begins an ambient transaction if one is not already open on the client session.
2. **Atomic Rollback on Uncaught Error**: If `OpRaiseError` executes outside a `TRY...CATCH` block, the VM automatically aborts and issues `tx.Rollback()`.
3. **Trigger Chaining**: When an `OpInsert`, `OpUpdate`, or `OpDelete` fires a trigger, the child trigger's bytecode executes inside the exact same ambient transaction, sharing isolation guarantees.
