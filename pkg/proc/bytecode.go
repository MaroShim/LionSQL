package proc

import (
	"fmt"
	"strings"
	"sync"

	"lionsql/pkg/storage"
)

// OpCode represents an operation code for the LionSQL T-SQL Bytecode Virtual Machine.
type OpCode uint8

const (
	// Control & Stack
	OpHalt OpCode = iota
	OpNop
	OpReturn
	OpLoadConst
	OpLoadVar
	OpStoreVar
	OpCopy
	OpJump
	OpJumpIfFalse
	OpJumpIfTrue
	OpJumpIfEq
	OpJumpIfNeq
	OpJumpIfLt
	OpJumpIfLte
	OpJumpIfGt
	OpJumpIfGte
	OpJumpIfEqInt
	OpJumpIfNeqInt
	OpJumpIfLtInt
	OpJumpIfLteInt
	OpJumpIfGtInt
	OpJumpIfGteInt
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
	OpAnd
	OpOr
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

	// Specialized Integer Arithmetic & Comparison (Hot-Path Inlining)
	OpAddInt
	OpSubInt
	OpMulInt
	OpEqInt
	OpNeqInt
	OpLtInt
	OpLteInt
	OpGtInt
	OpGteInt

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

	// Type Cast & Conversion
	OpCast
)

func (op OpCode) String() string {
	switch op {
	case OpResultRow:
		return "RESULT_ROW"
	case OpPointSelect:
		return "POINT_SELECT"
	case OpCast:
		return "CAST"
	case OpHalt:
		return "HALT"
	case OpNop:
		return "NOP"
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
	case OpJumpIfEq:
		return "JUMP_IF_EQ"
	case OpJumpIfNeq:
		return "JUMP_IF_NEQ"
	case OpJumpIfLt:
		return "JUMP_IF_LT"
	case OpJumpIfLte:
		return "JUMP_IF_LTE"
	case OpJumpIfGt:
		return "JUMP_IF_GT"
	case OpJumpIfGte:
		return "JUMP_IF_GTE"
	case OpJumpIfEqInt:
		return "JUMP_IF_EQ_INT"
	case OpJumpIfNeqInt:
		return "JUMP_IF_NEQ_INT"
	case OpJumpIfLtInt:
		return "JUMP_IF_LT_INT"
	case OpJumpIfLteInt:
		return "JUMP_IF_LTE_INT"
	case OpJumpIfGtInt:
		return "JUMP_IF_GT_INT"
	case OpJumpIfGteInt:
		return "JUMP_IF_GTE_INT"
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
	case OpAnd:
		return "AND"
	case OpOr:
		return "OR"
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
	case OpAddInt:
		return "ADD_INT"
	case OpSubInt:
		return "SUB_INT"
	case OpMulInt:
		return "MUL_INT"
	case OpEqInt:
		return "EQ_INT"
	case OpNeqInt:
		return "NEQ_INT"
	case OpLtInt:
		return "LT_INT"
	case OpLteInt:
		return "LTE_INT"
	case OpGtInt:
		return "GT_INT"
	case OpGteInt:
		return "GTE_INT"
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

// IsFusedJump returns true if the opcode is a fused compare-and-branch instruction.
func IsFusedJump(op OpCode) bool {
	switch op {
	case OpJumpIfEq, OpJumpIfNeq, OpJumpIfLt, OpJumpIfLte, OpJumpIfGt, OpJumpIfGte,
		OpJumpIfEqInt, OpJumpIfNeqInt, OpJumpIfLtInt, OpJumpIfLteInt, OpJumpIfGtInt, OpJumpIfGteInt:
		return true
	default:
		return false
	}
}

// Instruction represents a single 16-byte VM instruction.
// Packed to exactly 16 bytes (zero padding) to maximize CPU L1 Data Cache locality
// (fits exactly 4 instructions per 64-byte L1 Cache Line).
type Instruction struct {
	Op    OpCode // Operation code (1 byte)
	Flags uint8  // Flags for negation, condition invert, nullability, etc. (1 byte)
	Aux   uint16 // Auxiliary index into Program.AuxData or specialized payload (2 bytes)
	A     int32  // Register A / Jump target PC / Return value register (4 bytes)
	B     int32  // Register B / Slot index / Constant index / Jump PC (4 bytes)
	C     int32  // Register C / Result register / Extra operand (4 bytes)
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

// CastData holds metadata for OpCast type conversion.
type CastData struct {
	TargetType storage.DataType
	Precision  int
	Scale      int
	Style      int
	Try        bool
}

func (d *CastData) String() string {
	if d == nil {
		return ""
	}
	prefix := "CAST"
	if d.Try {
		prefix = "TRY_CAST"
	}
	return fmt.Sprintf("%s AS %v(p:%d,s:%d,style:%d)", prefix, d.TargetType, d.Precision, d.Scale, d.Style)
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
	AuxData      []any            // Out-of-band auxiliary metadata objects indexed by Instruction.Aux
	VarNames     []string
	VarSlots     map[string]int32 // Slot index mapping for fast O(1) indexed variable access
	SlotCount    int32            // Total number of allocated variable slots
	NumRegisters int32
	QuerySQL     string // Optional raw query SQL for ad-hoc / relational query programs
}

// AddAux registers an auxiliary metadata object in Program.AuxData and returns a 1-based index.
// Index 0 is reserved for nil (no aux data).
func (p *Program) AddAux(data any) uint16 {
	if p == nil || data == nil {
		return 0
	}
	if len(p.AuxData) == 0 {
		p.AuxData = append(p.AuxData, nil) // Index 0 reserved for nil
	}
	idx := len(p.AuxData)
	p.AuxData = append(p.AuxData, data)
	return uint16(idx)
}

// GetAux retrieves the auxiliary metadata object at the given 1-based index safely.
func (p *Program) GetAux(aux uint16) any {
	if p == nil || aux == 0 || int(aux) >= len(p.AuxData) {
		return nil
	}
	return p.AuxData[aux]
}

// ResultColumns inspects Program auxiliary data to find output column names if this program emits rows.
func (p *Program) ResultColumns() []string {
	if p == nil {
		return nil
	}
	for _, aux := range p.AuxData {
		if aux == nil {
			continue
		}
		if rowData, ok := aux.(*ResultRowData); ok && len(rowData.Columns) > 0 {
			return rowData.Columns
		}
		if pointData, ok := aux.(*PointSelectData); ok && len(pointData.Projections) > 0 {
			var cols []string
			for _, pr := range pointData.Projections {
				name := pr.Alias
				if name == "" {
					if colRef, ok := pr.Expr.(*ColumnRefExpr); ok {
						name = colRef.Name
					} else {
						name = pr.Expr.String()
					}
				}
				cols = append(cols, name)
			}
			return cols
		}
	}
	return nil
}

// AddInstruction appends a new instruction to the program, automatically interning auxData if provided.
func (p *Program) AddInstruction(op OpCode, a, b, c int32, auxData any) int {
	var aux uint16
	if auxData != nil {
		aux = p.AddAux(auxData)
	}
	pc := len(p.Instructions)
	p.Instructions = append(p.Instructions, Instruction{
		Op:  op,
		Aux: aux,
		A:   a,
		B:   b,
		C:   c,
	})
	return pc
}

// GetData retrieves metadata for this instruction from the program.
func (inst *Instruction) GetData(p *Program) any {
	if p == nil {
		return nil
	}
	return p.GetAux(inst.Aux)
}

// Disassemble produces a human-readable assembly listing of the bytecode program.
func (p *Program) Disassemble() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== Program: %s (%s) ===\n", p.Name, p.Type))
	b.WriteString(fmt.Sprintf("Registers: %d, Slots: %d, Constants: %d, Instructions: %d, AuxData: %d\n", p.NumRegisters, p.SlotCount, len(p.Constants), len(p.Instructions), len(p.AuxData)))

	if len(p.VarNames) > 0 {
		b.WriteString("--- Variables ---\n")
		for i, name := range p.VarNames {
			b.WriteString(fmt.Sprintf("  [slot %02d] @%s\n", i, name))
		}
	}

	if len(p.Constants) > 0 {
		b.WriteString("--- Constants ---\n")
		for i, c := range p.Constants {
			b.WriteString(fmt.Sprintf("  [%02d] %s\n", i, c.String()))
		}
	}

	b.WriteString("--- Instructions ---\n")
	for pc, inst := range p.Instructions {
		b.WriteString(fmt.Sprintf("  %04d: %-16s A:%-4d B:%-4d C:%-4d Flags:%-2d Aux:%-2d", pc, inst.Op.String(), inst.A, inst.B, inst.C, inst.Flags, inst.Aux))
		if auxData := p.GetAux(inst.Aux); auxData != nil {
			b.WriteString(fmt.Sprintf(" ; %v", auxData))
		}
		b.WriteString("\n")
	}
	return b.String()
}
