package proc

import (
	"fmt"
	"strings"
	"sync"

	"sqlion/pkg/storage"
)

// OpCode represents an operation code for the SQLion T-SQL Bytecode Virtual Machine.
type OpCode uint8

const (
	// Control & Stack
	OpHalt OpCode = iota
	OpReturn
	OpLoadConst
	OpLoadVar
	OpStoreVar
	OpCopy
	OpJump
	OpJumpIfFalse
	OpJumpIfTrue
	OpPushCatch
	OpPopCatch
	OpThrow
	OpRaiserror
	OpPrint

	// Arithmetic & Logic
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpNeg
	OpNot
	OpEq
	OpNeq
	OpLt
	OpLte
	OpGt
	OpGte
	OpLike
	OpBetween
	OpIn
	OpIsNull
	OpIsNotNull

	// Transaction Management
	OpTxBegin
	OpTxCommit
	OpTxRollback
	OpTxSavepoint
	OpTxRollbackTo

	// Storage & DML Operations
	OpGet
	OpScan
	OpInsert
	OpUpdate
	OpDelete
	OpUpdateWhere
	OpDeleteWhere
	OpSelectInto

	// Cursor Operations
	OpCursorOpen
	OpCursorFetch
	OpCursorClose
	OpCursorDealloc

	// Calls & Built-in Functions
	OpCallBuiltin
	OpCallProc
	OpCallUDF
	OpExecDynamic

	// Transient / Trigger tables
	OpSetTransientTable
	OpClearTransientTables

	// Relational Query Execution (Volcano Engine Integration)
	OpRelQuery

	// Secondary Index Range & Point Scan
	OpIndexScan

	// Scalar / Point Query Direct Result Generation
	OpResultRow
	OpPointSelect
)

func (op OpCode) String() string {
	switch op {
	case OpResultRow:
		return "RESULT_ROW"
	case OpPointSelect:
		return "POINT_SELECT"
	case OpHalt:
		return "HALT"
	case OpReturn:
		return "RETURN"
	case OpLoadConst:
		return "LOAD_CONST"
	case OpLoadVar:
		return "LOAD_VAR"
	case OpStoreVar:
		return "STORE_VAR"
	case OpCopy:
		return "COPY"
	case OpJump:
		return "JUMP"
	case OpJumpIfFalse:
		return "JUMP_IF_FALSE"
	case OpJumpIfTrue:
		return "JUMP_IF_TRUE"
	case OpPushCatch:
		return "PUSH_CATCH"
	case OpPopCatch:
		return "POP_CATCH"
	case OpThrow:
		return "THROW"
	case OpRaiserror:
		return "RAISERROR"
	case OpPrint:
		return "PRINT"
	case OpAdd:
		return "ADD"
	case OpSub:
		return "SUB"
	case OpMul:
		return "MUL"
	case OpDiv:
		return "DIV"
	case OpNeg:
		return "NEG"
	case OpNot:
		return "NOT"
	case OpEq:
		return "EQ"
	case OpNeq:
		return "NEQ"
	case OpLt:
		return "LT"
	case OpLte:
		return "LTE"
	case OpGt:
		return "GT"
	case OpGte:
		return "GTE"
	case OpLike:
		return "LIKE"
	case OpBetween:
		return "BETWEEN"
	case OpIn:
		return "IN"
	case OpIsNull:
		return "IS_NULL"
	case OpIsNotNull:
		return "IS_NOT_NULL"
	case OpTxBegin:
		return "TX_BEGIN"
	case OpTxCommit:
		return "TX_COMMIT"
	case OpTxRollback:
		return "TX_ROLLBACK"
	case OpTxSavepoint:
		return "TX_SAVEPOINT"
	case OpTxRollbackTo:
		return "TX_ROLLBACK_TO"
	case OpGet:
		return "GET"
	case OpScan:
		return "SCAN"
	case OpInsert:
		return "INSERT"
	case OpUpdate:
		return "UPDATE"
	case OpDelete:
		return "DELETE"
	case OpUpdateWhere:
		return "UPDATE_WHERE"
	case OpDeleteWhere:
		return "DELETE_WHERE"
	case OpSelectInto:
		return "SELECT_INTO"
	case OpCursorOpen:
		return "CURSOR_OPEN"
	case OpCursorFetch:
		return "CURSOR_FETCH"
	case OpCursorClose:
		return "CURSOR_CLOSE"
	case OpCursorDealloc:
		return "CURSOR_DEALLOC"
	case OpCallBuiltin:
		return "CALL_BUILTIN"
	case OpCallProc:
		return "CALL_PROC"
	case OpCallUDF:
		return "CALL_UDF"
	case OpExecDynamic:
		return "EXEC_DYNAMIC"
	case OpSetTransientTable:
		return "SET_TRANSIENT"
	case OpClearTransientTables:
		return "CLEAR_TRANSIENTS"
	case OpRelQuery:
		return "REL_QUERY"
	case OpIndexScan:
		return "INDEX_SCAN"
	default:
		return fmt.Sprintf("OP_%d", op)
	}
}

// Instruction represents a single VM instruction.
type Instruction struct {
	Op   OpCode
	A    int32
	B    int32
	C    int32
	Data any
}

// CallData holds metadata for function and procedure invocations.
type CallData struct {
	Name    string
	ArgRegs []int32
}

// DMLData holds metadata for DML instructions.
type DMLData struct {
	Table       string
	Columns     []string
	ValExprs    []Expr
	Assignments []UpdateAssignment
	WhereCol    string
	WhereVal    Expr
	WhereExpr   Expr
}

// SelectIntoData holds metadata for SELECT INTO operations.
type SelectIntoData struct {
	Column   string
	IntoVar  string
	Table    string
	WhereCol string
	WhereVal Expr
}

// CursorData holds metadata for CURSOR declarations and fetches.
type CursorData struct {
	Name      string
	Table     string
	Columns   []string
	WhereExpr Expr
	IntoVars  []string
}

// RelQueryData holds metadata for relational query execution via Volcano engine.
type RelQueryData struct {
	mu              sync.RWMutex
	SQL             string
	IntoVar         string
	DestReg         int32
	Projections     []string
	CachedQueryInfo any // *planner.QueryInfo cached for zero-reparse execution
	CachedPlan      any // *planner.PlanNode cached for zero-replan execution
}

func (d *RelQueryData) GetCachedQueryInfo() any {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.CachedQueryInfo
}

func (d *RelQueryData) SetCachedQueryInfo(v any) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.CachedQueryInfo = v
}

func (d *RelQueryData) GetCachedPlan() any {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.CachedPlan
}

func (d *RelQueryData) SetCachedPlan(v any) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.CachedPlan = v
}

func (d *RelQueryData) String() string {
	if d == nil {
		return ""
	}
	if d.IntoVar != "" {
		return fmt.Sprintf("SQL:%q INTO @%s", d.SQL, d.IntoVar)
	}
	return fmt.Sprintf("SQL:%q", d.SQL)
}

// IndexScanData holds metadata for secondary index scan operations.
type IndexScanData struct {
	Table       string
	IndexName   string
	LowVal      Expr
	HighVal     Expr
	WhereExpr   Expr
	Projections []string
	IntoVar     string
	IndexOnly   bool
}

func (d *IndexScanData) String() string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("Table:%s Index:%s", d.Table, d.IndexName)
}

// ResultRowData holds metadata for emitting a scalar / calculated row directly from registers.
type ResultRowData struct {
	Columns  []string
	StartReg int32
	Count    int32
	Regs     []int32
}

func (d *ResultRowData) String() string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("Cols:%v Regs:[%d..%d]", d.Columns, d.StartReg, d.StartReg+d.Count-1)
}

// PointSelectData holds metadata for single-table primary key or point lookups.
type PointSelectData struct {
	Table       string
	WhereCol    string
	WhereVal    Expr
	Projections []Projection
	Limit       int64
}

func (d *PointSelectData) String() string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("Table:%s Where:%s=%v Projections:%d", d.Table, d.WhereCol, d.WhereVal, len(d.Projections))
}

// Program represents a compiled, executable T-SQL bytecode program.
type Program struct {
	Name         string
	Type         string // "PROCEDURE", "FUNCTION", "TRIGGER", "QUERY"
	Params       []ParamDef
	ReturnType   storage.DataType
	Table        string   // For triggers
	Events       []string // For triggers
	Instructions []Instruction
	Constants    []storage.Value
	VarNames     []string
	NumRegisters int32
	QuerySQL     string // Optional raw query SQL for ad-hoc / relational query programs
}

// Disassemble produces a human-readable assembly listing of the bytecode program.
func (p *Program) Disassemble() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== Program: %s (%s) ===\n", p.Name, p.Type))
	b.WriteString(fmt.Sprintf("Registers: %d, Constants: %d, Instructions: %d\n", p.NumRegisters, len(p.Constants), len(p.Instructions)))

	if len(p.Constants) > 0 {
		b.WriteString("--- Constants ---\n")
		for i, c := range p.Constants {
			b.WriteString(fmt.Sprintf("  [%02d] %s\n", i, c.String()))
		}
	}

	b.WriteString("--- Instructions ---\n")
	for pc, inst := range p.Instructions {
		b.WriteString(fmt.Sprintf("  %04d: %-16s A:%-4d B:%-4d C:%-4d", pc, inst.Op.String(), inst.A, inst.B, inst.C))
		if inst.Data != nil {
			b.WriteString(fmt.Sprintf(" ; %v", inst.Data))
		}
		b.WriteString("\n")
	}
	return b.String()
}
