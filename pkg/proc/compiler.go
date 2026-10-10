package proc

import (
	"fmt"
	"strings"
	"sync"

	"sqlion/pkg/storage"
)

// ProcedureFallbackParser represents a pluggable parser invoked when the primary Pratt parser fails.
type ProcedureFallbackParser func(script string) (*CreateProcStmt, error)

var (
	procFallbackMu     sync.RWMutex
	procFallbackParser ProcedureFallbackParser
)

// RegisterProcedureFallback registers a fallback procedure parser (such as ANTLR).
func RegisterProcedureFallback(p ProcedureFallbackParser) {
	procFallbackMu.Lock()
	defer procFallbackMu.Unlock()
	procFallbackParser = p
}

// GetProcedureFallback returns the registered fallback parser, if any.
func GetProcedureFallback() ProcedureFallbackParser {
	procFallbackMu.RLock()
	defer procFallbackMu.RUnlock()
	return procFallbackParser
}

type gotoFixup struct {
	pc    int
	label string
}

// Compiler compiles AST nodes into executable bytecode Programs.
type Compiler struct {
	prog            *Program
	regCounter      int32
	maxRegisters    int32
	constMap        map[string]int32
	varNames        map[string]bool
	varSlots        map[string]int32
	varTypes        map[string]storage.DataType
	slotNames       []string
	loopBreaks      [][]int
	loopContinues   [][]int
	labels          map[string]int
	unresolvedGotos []gotoFixup
}

// NewCompiler creates a new Compiler instance.
func NewCompiler() *Compiler {
	return &Compiler{
		prog: &Program{
			Instructions: make([]Instruction, 0),
			Constants:    make([]storage.Value, 0),
			AuxData:      make([]any, 0),
			VarNames:     make([]string, 0),
			VarSlots:     make(map[string]int32),
		},
		constMap:        make(map[string]int32),
		varNames:        make(map[string]bool),
		varSlots:        make(map[string]int32),
		varTypes:        make(map[string]storage.DataType),
		slotNames:       make([]string, 0),
		loopBreaks:      make([][]int, 0),
		loopContinues:   make([][]int, 0),
		labels:          make(map[string]int),
		unresolvedGotos: make([]gotoFixup, 0),
	}
}

// Compile parses and compiles a T-SQL script into a bytecode Program.
func Compile(sql string) (*Program, error) {
	trimmed := strings.TrimSpace(sql)
	upper := strings.ToUpper(trimmed)

	if strings.HasPrefix(upper, "CREATE PROCEDURE") || strings.HasPrefix(upper, "CREATE PROC") {
		lexer := NewLexer(sql)
		parser := NewParser(lexer)
		stmt, err := parser.ParseProcedure()
		if err != nil {
			if fb := GetProcedureFallback(); fb != nil {
				fbStmt, fbErr := fb(sql)
				if fbErr == nil && fbStmt != nil {
					return CompileProcedure(fbStmt)
				}
				if fbErr != nil {
					return nil, fmt.Errorf("fallback error: %w", fbErr)
				}
			}
			return nil, err
		}
		return CompileProcedure(stmt)
	}

	if strings.HasPrefix(upper, "CREATE FUNCTION") {
		stmt, err := ParseFunction(sql)
		if err != nil {
			return nil, err
		}
		return CompileFunction(stmt)
	}

	if strings.HasPrefix(upper, "CREATE TRIGGER") {
		stmt, err := ParseTrigger(sql)
		if err != nil {
			return nil, err
		}
		return CompileTrigger(stmt)
	}

	if strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH") {
		return CompileQuery(sql)
	}

	if strings.HasPrefix(upper, "INSERT") || strings.HasPrefix(upper, "UPDATE") || strings.HasPrefix(upper, "DELETE") {
		return CompileStatement(sql)
	}

	// Fallback to procedure parser
	lexer := NewLexer(sql)
	parser := NewParser(lexer)
	stmt, err := parser.ParseProcedure()
	if err != nil {
		return nil, err
	}
	return CompileProcedure(stmt)
}

// CompileStatement compiles an interactive / standalone DML or SQL statement into a VDBE Program.
func CompileStatement(sql string) (*Program, error) {
	trimmed := strings.TrimSpace(sql)
	c := NewCompiler()
	c.prog.Name = "INTERACTIVE_DML"
	c.prog.Type = "DML"
	c.prog.QuerySQL = trimmed

	p := NewParser(NewLexer(trimmed))
	stmt, err := p.ParseSQLStatement()
	if err != nil {
		return nil, err
	}
	if err := c.compileStatement(stmt); err != nil {
		return nil, err
	}
	if err := c.resolveGotos(); err != nil {
		return nil, err
	}
	return c.finalizeProgram(), nil
}

// CompileQuery compiles an interactive / standalone SELECT or relational query into a VDBE Program containing OpRelQuery.
func CompileQuery(sql string) (*Program, error) {
	trimmed := strings.TrimSpace(sql)
	c := NewCompiler()
	c.prog.Name = "INTERACTIVE_QUERY"
	c.prog.Type = "QUERY"
	c.prog.QuerySQL = trimmed

	// Attempt to parse via native parser if possible
	p := NewParser(NewLexer(trimmed))
	var stmt *GeneralSelectStmt
	var err error
	if p.curTokenIs(TokenSelect) {
		p.nextToken() // eat SELECT
		stmt, err = p.parseGeneralSelect()
	} else {
		var s Statement
		s, err = p.ParseSQLStatement()
		if sel, ok := s.(*GeneralSelectStmt); ok {
			stmt = sel
		}
	}

	if err == nil && stmt != nil {
		if stmt.RawSQL == "" {
			stmt.RawSQL = trimmed
		}
		if err := c.compileStatement(stmt); err != nil {
			return nil, err
		}
	} else {
		// Emit OpRelQuery with raw SQL for unparsed or complex relational statements
		c.emit(OpRelQuery, 0, 0, 0, &RelQueryData{
			SQL: trimmed,
		})
	}

	return c.finalizeProgram(), nil
}

// CompileProcedure compiles a CreateProcStmt into a Program.
func CompileProcedure(stmt *CreateProcStmt) (*Program, error) {
	c := NewCompiler()
	c.prog.Name = stmt.Name
	c.prog.Type = "PROCEDURE"
	c.prog.Params = stmt.Params

	for _, p := range stmt.Params {
		varName := cleanVarName(p.Name)
		c.getOrAllocSlot(varName)
		if p.Type == storage.TypeInt {
			c.varTypes[varName] = storage.TypeInt
		} else {
			c.varTypes[varName] = p.Type
		}
	}

	if err := c.compileStatement(stmt.Body); err != nil {
		return nil, err
	}
	if err := c.resolveGotos(); err != nil {
		return nil, err
	}

	return c.finalizeProgram(), nil
}

// CompileFunction compiles a CreateFunctionStmt into a Program.
func CompileFunction(stmt *CreateFunctionStmt) (*Program, error) {
	c := NewCompiler()
	c.prog.Name = stmt.Name
	c.prog.Type = "FUNCTION"
	c.prog.Params = stmt.Params
	c.prog.ReturnType = stmt.ReturnType

	for _, p := range stmt.Params {
		varName := cleanVarName(p.Name)
		c.getOrAllocSlot(varName)
		if p.Type == storage.TypeInt {
			c.varTypes[varName] = storage.TypeInt
		} else {
			c.varTypes[varName] = p.Type
		}
	}

	if err := c.compileStatement(stmt.Body); err != nil {
		return nil, err
	}
	if err := c.resolveGotos(); err != nil {
		return nil, err
	}

	return c.finalizeProgram(), nil
}

// CompileTrigger compiles a CreateTriggerStmt into a Program.
func CompileTrigger(stmt *CreateTriggerStmt) (*Program, error) {
	c := NewCompiler()
	c.prog.Name = stmt.Name
	c.prog.Type = "TRIGGER"
	c.prog.Table = stmt.Table
	c.prog.Events = stmt.Events

	if err := c.compileStatement(stmt.Body); err != nil {
		return nil, err
	}
	if err := c.resolveGotos(); err != nil {
		return nil, err
	}

	return c.finalizeProgram(), nil
}

func (c *Compiler) finalizeProgram() *Program {
	c.emit(OpReturn, -1, 0, 0, nil)
	c.optimizePeephole()
	c.prog.NumRegisters = c.maxRegisters
	c.prog.VarSlots = c.varSlots
	c.prog.SlotCount = int32(len(c.slotNames))
	c.prog.VarNames = c.slotNames
	return c.prog
}

// optimizePeephole applies static peep-hole optimizations to bytecode instructions:
// 1. Fall-through jump elimination (converting OpJump i+1 to OpNop)
// 2. Jump threading / chaining (trampoline jump elimination)
func (c *Compiler) optimizePeephole() {
	instructions := c.prog.Instructions
	n := len(instructions)

	for i := 0; i < n; i++ {
		inst := &instructions[i]

		// 1. Fall-through jump optimization
		// If an unconditional OpJump jumps to the immediately following instruction (i + 1),
		// it is a no-op fallthrough. Convert it to OpNop.
		if inst.Op == OpJump && int(inst.B) == i+1 {
			inst.Op = OpNop
			continue
		}

		// 2. Jump threading / chaining
		// If a jump instruction targets an instruction that itself is an unconditional OpJump,
		// shortcut directly to the final destination.
		target := -1
		isFused := IsFusedJump(inst.Op)
		if isFused {
			target = int(inst.C)
		} else if inst.Op == OpJump || inst.Op == OpJumpIfFalse || inst.Op == OpJumpIfTrue {
			target = int(inst.B)
		}

		if target >= 0 && target < n {
			visited := 0
			curr := target
			for visited < 32 && curr >= 0 && curr < n && instructions[curr].Op == OpJump {
				next := int(instructions[curr].B)
				if next == curr {
					break
				}
				curr = next
				visited++
			}
			if curr != target && curr >= 0 && curr < n {
				if isFused {
					inst.C = int32(curr)
				} else {
					inst.B = int32(curr)
				}
			}
		}
	}
}

func (c *Compiler) patchJumpTarget(pc int, targetPC int) {
	if pc < 0 || pc >= len(c.prog.Instructions) {
		return
	}
	inst := &c.prog.Instructions[pc]
	if IsFusedJump(inst.Op) {
		inst.C = int32(targetPC)
	} else {
		inst.B = int32(targetPC)
	}
}

func (c *Compiler) compileConditionJump(cond Expr, invert bool) (int, error) {
	if be, ok := cond.(*BinaryExpr); ok {
		var fusedOp OpCode
		isInt := c.isIntegerExpr(be.Left) && c.isIntegerExpr(be.Right)

		switch be.Op {
		case TokenEq:
			if invert {
				if isInt {
					fusedOp = OpJumpIfNeqInt
				} else {
					fusedOp = OpJumpIfNeq
				}
			} else {
				if isInt {
					fusedOp = OpJumpIfEqInt
				} else {
					fusedOp = OpJumpIfEq
				}
			}
		case TokenNeq:
			if invert {
				if isInt {
					fusedOp = OpJumpIfEqInt
				} else {
					fusedOp = OpJumpIfEq
				}
			} else {
				if isInt {
					fusedOp = OpJumpIfNeqInt
				} else {
					fusedOp = OpJumpIfNeq
				}
			}
		case TokenLt:
			if invert {
				if isInt {
					fusedOp = OpJumpIfGteInt
				} else {
					fusedOp = OpJumpIfGte
				}
			} else {
				if isInt {
					fusedOp = OpJumpIfLtInt
				} else {
					fusedOp = OpJumpIfLt
				}
			}
		case TokenLte:
			if invert {
				if isInt {
					fusedOp = OpJumpIfGtInt
				} else {
					fusedOp = OpJumpIfGt
				}
			} else {
				if isInt {
					fusedOp = OpJumpIfLteInt
				} else {
					fusedOp = OpJumpIfLte
				}
			}
		case TokenGt:
			if invert {
				if isInt {
					fusedOp = OpJumpIfLteInt
				} else {
					fusedOp = OpJumpIfLte
				}
			} else {
				if isInt {
					fusedOp = OpJumpIfGtInt
				} else {
					fusedOp = OpJumpIfGt
				}
			}
		case TokenGte:
			if invert {
				if isInt {
					fusedOp = OpJumpIfLtInt
				} else {
					fusedOp = OpJumpIfLt
				}
			} else {
				if isInt {
					fusedOp = OpJumpIfGteInt
				} else {
					fusedOp = OpJumpIfGte
				}
			}
		}

		if fusedOp != 0 {
			leftReg, err := c.compileExpr(be.Left)
			if err != nil {
				return 0, err
			}
			rightReg, err := c.compileExpr(be.Right)
			if err != nil {
				return 0, err
			}
			jmpPC := c.emit(fusedOp, leftReg, rightReg, 0, nil)
			c.freeReg(2)
			return jmpPC, nil
		}
	}

	// General condition fallback
	condReg, err := c.compileExpr(cond)
	if err != nil {
		return 0, err
	}
	op := OpJumpIfFalse
	if !invert {
		op = OpJumpIfTrue
	}
	jmpPC := c.emit(op, condReg, 0, 0, nil)
	c.freeReg(1)
	return jmpPC, nil
}

func isSystemVar(name string) bool {
	if strings.HasPrefix(name, "@@") {
		return true
	}
	clean := cleanVarName(name)
	switch strings.ToUpper(clean) {
	case "ROWCOUNT", "FETCH_STATUS", "TRANCOUNT", "IDENTITY", "ERROR", "VERSION", "SPID":
		return true
	default:
		return false
	}
}

func (c *Compiler) getOrAllocSlot(varName string) int32 {
	if isSystemVar(varName) {
		return -1
	}
	clean := cleanVarName(varName)
	if isSystemVar(clean) {
		return -1
	}
	lower := strings.ToLower(clean)
	if slot, exists := c.varSlots[lower]; exists {
		return slot
	}
	slot := int32(len(c.slotNames))
	c.varSlots[lower] = slot
	c.varSlots[clean] = slot
	c.slotNames = append(c.slotNames, clean)
	c.varNames[clean] = true
	return slot
}

func (c *Compiler) getSlot(varName string) int32 {
	if isSystemVar(varName) {
		return -1
	}
	clean := cleanVarName(varName)
	if isSystemVar(clean) {
		return -1
	}
	lower := strings.ToLower(clean)
	if slot, exists := c.varSlots[lower]; exists {
		return slot
	}
	if slot, exists := c.varSlots[clean]; exists {
		return slot
	}
	return -1
}

func (c *Compiler) allocReg() int32 {
	r := c.regCounter
	c.regCounter++
	if c.regCounter > c.maxRegisters {
		c.maxRegisters = c.regCounter
	}
	return r
}

func (c *Compiler) freeReg(count int32) {
	c.regCounter -= count
	if c.regCounter < 0 {
		c.regCounter = 0
	}
}

func (c *Compiler) emit(op OpCode, a, b, cReg int32, data any) int {
	return c.emitWithFlags(op, 0, a, b, cReg, data)
}

func (c *Compiler) emitWithFlags(op OpCode, flags uint8, a, b, cReg int32, data any) int {
	var aux uint16
	if data != nil {
		aux = c.prog.AddAux(data)
	}
	pc := len(c.prog.Instructions)
	c.prog.Instructions = append(c.prog.Instructions, Instruction{
		Op:    op,
		Flags: flags,
		Aux:   aux,
		A:     a,
		B:     b,
		C:     cReg,
	})
	return pc
}

// EmitForTest is a test helper for building manual bytecode programs.
func (c *Compiler) EmitForTest(op OpCode, a, b, cReg int32, data any) int {
	return c.emit(op, a, b, cReg, data)
}

// FinalizeProgramForTest is a test helper for finalizing manually built programs.
func (c *Compiler) FinalizeProgramForTest() *Program {
	return c.finalizeProgram()
}

func (c *Compiler) currentPC() int {
	return len(c.prog.Instructions)
}

func (c *Compiler) internConst(val storage.Value) int32 {
	key := fmt.Sprintf("%v:%s", val.Type, val.String())
	if idx, ok := c.constMap[key]; ok {
		return idx
	}
	idx := int32(len(c.prog.Constants))
	c.prog.Constants = append(c.prog.Constants, val)
	c.constMap[key] = idx
	return idx
}

func (c *Compiler) compileStatement(stmt Statement) error {
	if stmt == nil {
		return nil
	}

	switch s := stmt.(type) {
	case *BlockStmt:
		for _, child := range s.Statements {
			if err := c.compileStatement(child); err != nil {
				return err
			}
		}
		return nil

	case *DeclareStmt:
		varName := cleanVarName(s.Name)
		c.getOrAllocSlot(varName)
		if s.Type == storage.TypeInt {
			c.varTypes[varName] = storage.TypeInt
		} else {
			c.varTypes[varName] = s.Type
		}
		return nil

	case *SetStmt:
		valReg, err := c.compileExpr(s.Value)
		if err != nil {
			return err
		}
		varName := cleanVarName(s.Name)
		slot := c.getOrAllocSlot(varName)
		if c.isIntegerExpr(s.Value) {
			c.varTypes[varName] = storage.TypeInt
		}
		c.emit(OpStoreVar, valReg, slot, 0, varName)
		c.freeReg(1)
		return nil

	case *SetOptionStmt:
		for k, v := range s.Options {
			var bVal bool = true
			if strings.EqualFold(v, "OFF") || v == "0" {
				bVal = false
			}
			reg := c.allocReg()
			cIdx := c.internConst(storage.BoolValue(bVal))
			c.emit(OpLoadConst, reg, cIdx, 0, nil)
			slot := c.getOrAllocSlot(k)
			c.emit(OpStoreVar, reg, slot, 0, strings.ToLower(k))
			c.freeReg(1)
		}
		return nil

	case *IfStmt:
		jmpFalse, err := c.compileConditionJump(s.Condition, true)
		if err != nil {
			return err
		}

		if err := c.compileStatement(s.Then); err != nil {
			return err
		}

		if s.Else != nil {
			jmpEnd := c.emit(OpJump, 0, 0, 0, nil)
			c.patchJumpTarget(jmpFalse, c.currentPC())
			if err := c.compileStatement(s.Else); err != nil {
				return err
			}
			c.patchJumpTarget(jmpEnd, c.currentPC())
		} else {
			c.patchJumpTarget(jmpFalse, c.currentPC())
		}
		return nil

	case *WhileStmt:
		loopStart := c.currentPC()
		c.loopBreaks = append(c.loopBreaks, []int{})
		c.loopContinues = append(c.loopContinues, []int{})

		jmpEnd, err := c.compileConditionJump(s.Condition, true)
		if err != nil {
			return err
		}

		if err := c.compileStatement(s.Body); err != nil {
			return err
		}

		c.emit(OpJump, 0, int32(loopStart), 0, nil)
		endPC := c.currentPC()
		c.patchJumpTarget(jmpEnd, endPC)

		breaks := c.loopBreaks[len(c.loopBreaks)-1]
		c.loopBreaks = c.loopBreaks[:len(c.loopBreaks)-1]
		for _, bPC := range breaks {
			c.patchJumpTarget(bPC, endPC)
		}

		continues := c.loopContinues[len(c.loopContinues)-1]
		c.loopContinues = c.loopContinues[:len(c.loopContinues)-1]
		for _, cPC := range continues {
			c.patchJumpTarget(cPC, loopStart)
		}
		return nil

	case *BreakStmt:
		if len(c.loopBreaks) == 0 {
			return fmt.Errorf("BREAK statement outside of loop")
		}
		bPC := c.emit(OpJump, 0, 0, 0, nil)
		c.loopBreaks[len(c.loopBreaks)-1] = append(c.loopBreaks[len(c.loopBreaks)-1], bPC)
		return nil

	case *ContinueStmt:
		if len(c.loopContinues) == 0 {
			return fmt.Errorf("CONTINUE statement outside of loop")
		}
		cPC := c.emit(OpJump, 0, 0, 0, nil)
		c.loopContinues[len(c.loopContinues)-1] = append(c.loopContinues[len(c.loopContinues)-1], cPC)
		return nil

	case *ReturnStmt:
		if s.Value != nil {
			valReg, err := c.compileExpr(s.Value)
			if err != nil {
				return err
			}
			c.emit(OpReturn, valReg, 0, 0, nil)
			c.freeReg(1)
		} else {
			c.emit(OpReturn, -1, 0, 0, nil)
		}
		return nil

	case *PrintStmt:
		valReg, err := c.compileExpr(s.Expr)
		if err != nil {
			return err
		}
		c.emit(OpPrint, valReg, 0, 0, nil)
		c.freeReg(1)
		return nil

	case *RaiseErrorStmt:
		c.emit(OpRaiserror, 0, 0, 0, s.Message)
		return nil

	case *ThrowStmt:
		c.emit(OpThrow, 0, 0, 0, s.Message)
		return nil

	case *TryCatchStmt:
		catchJump := c.emit(OpPushCatch, 0, 0, 0, nil)
		if err := c.compileStatement(s.TryBody); err != nil {
			return err
		}
		c.emit(OpPopCatch, 0, 0, 0, nil)
		jmpEnd := c.emit(OpJump, 0, 0, 0, nil)

		catchPC := c.currentPC()
		c.prog.Instructions[catchJump].B = int32(catchPC)

		if err := c.compileStatement(s.CatchBody); err != nil {
			return err
		}
		c.prog.Instructions[jmpEnd].B = int32(c.currentPC())
		return nil

	case *TxStmt:
		switch s.Action {
		case TxBegin:
			c.emit(OpTxBegin, 0, 0, 0, nil)
		case TxCommit:
			c.emit(OpTxCommit, 0, 0, 0, nil)
		case TxRollback:
			c.emit(OpTxRollback, 0, 0, 0, nil)
		case TxSavepoint:
			c.emit(OpTxSavepoint, 0, 0, 0, s.Name)
		case TxRollbackTo:
			c.emit(OpTxRollbackTo, 0, 0, 0, s.Name)
		}
		return nil

	case *InsertStmt:
		c.emit(OpInsert, 0, 0, 0, &DMLData{
			Table:    s.Table,
			Columns:  s.Columns,
			ValExprs: s.Values,
		})
		return nil

	case *GeneralInsertStmt:
		for _, tuple := range s.Tuples {
			c.emit(OpInsert, 0, 0, 0, &DMLData{
				Table:    s.Table,
				Columns:  s.Columns,
				ValExprs: tuple,
			})
		}
		return nil

	case *UpdateStmt:
		c.emit(OpUpdate, 0, 0, 0, &DMLData{
			Table:       s.Table,
			Assignments: s.Assignments,
			WhereCol:    s.WhereCol,
			WhereVal:    s.WhereVal,
		})
		return nil

	case *UpdateWhereStmt:
		c.emit(OpUpdateWhere, 0, 0, 0, &DMLData{
			Table:       s.Table,
			Assignments: s.Assignments,
			WhereExpr:   s.Where,
		})
		return nil

	case *DeleteStmt:
		c.emit(OpDelete, 0, 0, 0, &DMLData{
			Table:    s.Table,
			WhereCol: s.WhereCol,
			WhereVal: s.WhereVal,
		})
		return nil

	case *DeleteWhereStmt:
		c.emit(OpDeleteWhere, 0, 0, 0, &DMLData{
			Table:     s.Table,
			WhereExpr: s.Where,
		})
		return nil

	case *SelectIntoStmt:
		intoVar := cleanVarName(s.IntoVar)
		c.getOrAllocSlot(intoVar)
		c.emit(OpSelectInto, 0, 0, 0, &SelectIntoData{
			Column:   s.Column,
			IntoVar:  intoVar,
			Table:    s.Table,
			WhereCol: s.WhereCol,
			WhereVal: s.WhereVal,
		})
		return nil

	case *DeclareCursorStmt:
		c.emit(OpCursorOpen, 0, 0, 0, &CursorData{
			Name:      cleanVarName(s.Name),
			Table:     s.Table,
			Columns:   s.Columns,
			WhereExpr: s.Where,
		})
		return nil

	case *OpenCursorStmt:
		c.emit(OpCursorOpen, 0, 0, 0, cleanVarName(s.Name))
		return nil

	case *FetchNextStmt:
		var vars []string
		for _, v := range s.IntoVars {
			clean := cleanVarName(v)
			c.getOrAllocSlot(clean)
			vars = append(vars, clean)
		}
		c.emit(OpCursorFetch, 0, 0, 0, &CursorData{
			Name:     cleanVarName(s.CursorName),
			IntoVars: vars,
		})
		return nil

	case *CloseCursorStmt:
		c.emit(OpCursorClose, 0, 0, 0, cleanVarName(s.Name))
		return nil

	case *DeallocateCursorStmt:
		c.emit(OpCursorDealloc, 0, 0, 0, cleanVarName(s.Name))
		return nil

	case *ExecStmt:
		var argRegs []int32
		for _, a := range s.Args {
			r, err := c.compileExpr(a)
			if err != nil {
				return err
			}
			argRegs = append(argRegs, r)
		}
		c.emit(OpCallProc, 0, 0, 0, &CallData{
			Name:    s.ProcName,
			ArgRegs: argRegs,
		})
		c.freeReg(int32(len(argRegs)))
		return nil

	case *DynamicExecStmt:
		reg, err := c.compileExpr(s.Expr)
		if err != nil {
			return err
		}
		c.emit(OpExecDynamic, reg, 0, 0, nil)
		c.freeReg(1)
		return nil

	case *LabelStmt:
		labelKey := strings.ToLower(s.Label)
		if _, exists := c.labels[labelKey]; exists {
			return fmt.Errorf("duplicate label %q", s.Label)
		}
		c.labels[labelKey] = c.currentPC()
		return nil

	case *GotoStmt:
		labelKey := strings.ToLower(s.Label)
		if targetPC, exists := c.labels[labelKey]; exists {
			c.emit(OpJump, 0, int32(targetPC), 0, nil)
		} else {
			jmpIdx := c.emit(OpJump, 0, 0, 0, nil)
			c.unresolvedGotos = append(c.unresolvedGotos, gotoFixup{
				pc:    jmpIdx,
				label: s.Label,
			})
		}
		return nil

	case *GeneralSelectStmt:
		// 1. Scalar SELECT without FROM (e.g. SELECT 1 + 1 AS res, 'hello' AS msg, GETDATE())
		if s.From.Name == "" && len(s.CTEs) == 0 && len(s.Joins) == 0 && len(s.GroupBy) == 0 && s.Having == nil && !s.IsExplain {
			var colNames []string
			var projRegs []int32

			for _, p := range s.Projections {
				reg, err := c.compileExpr(p.Expr)
				if err != nil {
					return err
				}
				projRegs = append(projRegs, reg)
				name := p.Alias
				if name == "" {
					if colRef, ok := p.Expr.(*ColumnRefExpr); ok {
						name = colRef.Name
					} else {
						name = p.Expr.String()
					}
				}
				colNames = append(colNames, name)
			}

			startReg := int32(0)
			if len(projRegs) > 0 {
				startReg = projRegs[0]
			}
			count := int32(len(projRegs))
			c.emit(OpResultRow, startReg, count, 0, &ResultRowData{
				Columns:  colNames,
				StartReg: startReg,
				Count:    count,
				Regs:     projRegs,
			})
			return nil
		}

		// 2. Single-table Point Query (WHERE col = val without joins/groups/aggregates/distinct)
		if s.From.Name != "" && len(s.CTEs) == 0 && len(s.Joins) == 0 && len(s.GroupBy) == 0 && s.Having == nil && !hasAggregates(s.Projections) && s.Where != nil && !s.Distinct && !s.IsExplain {
			if whereCol, whereVal, ok := ExtractSimpleEq(s.Where); ok {
				c.emit(OpPointSelect, 0, 0, 0, &PointSelectData{
					Table:       s.From.Name,
					WhereCol:    whereCol,
					WhereVal:    whereVal,
					Projections: s.Projections,
					Limit:       s.Limit,
				})
				return nil
			}
		}

		// 3. Fallback to Volcano relational execution engine
		sql := s.RawSQL
		if sql == "" {
			sql = s.String()
		}
		if c.prog.QuerySQL == "" {
			c.prog.QuerySQL = sql
		}
		var projNames []string
		for _, p := range s.Projections {
			if p.Alias != "" {
				projNames = append(projNames, p.Alias)
			}
		}
		c.emit(OpRelQuery, 0, 0, 0, &RelQueryData{
			SQL:         sql,
			Projections: projNames,
		})
		return nil

	default:
		return fmt.Errorf("unsupported statement type: %T", stmt)
	}
}

// ExtractSimpleEq extracts a single column equality predicate (e.g. col = val or val = col).
func ExtractSimpleEq(expr Expr) (string, Expr, bool) {
	if expr == nil {
		return "", nil, false
	}
	if bin, ok := expr.(*BinaryExpr); ok && bin.Op == TokenEq {
		if colRef, ok := bin.Left.(*ColumnRefExpr); ok {
			return cleanVarName(colRef.Name), bin.Right, true
		} else if colRef, ok := bin.Right.(*ColumnRefExpr); ok {
			return cleanVarName(colRef.Name), bin.Left, true
		}
	}
	return "", nil, false
}

func hasAggregates(projections []Projection) bool {
	for _, p := range projections {
		pUpper := strings.ToUpper(p.Expr.String())
		if strings.Contains(pUpper, "COUNT(") || strings.Contains(pUpper, "SUM(") ||
			strings.Contains(pUpper, "AVG(") || strings.Contains(pUpper, "MIN(") ||
			strings.Contains(pUpper, "MAX(") || strings.Contains(pUpper, "PERCENTILE_CONT(") ||
			strings.Contains(pUpper, "PERCENTILE_DISC(") || strings.Contains(pUpper, "APPROX_PERCENTILE(") ||
			strings.Contains(pUpper, "MEDIAN(") {
			return true
		}
	}
	return false
}

func (c *Compiler) resolveGotos() error {
	for _, fix := range c.unresolvedGotos {
		labelKey := strings.ToLower(fix.label)
		targetPC, ok := c.labels[labelKey]
		if !ok {
			return fmt.Errorf("label %q is not defined", fix.label)
		}
		c.prog.Instructions[fix.pc].B = int32(targetPC)
	}
	return nil
}

func (c *Compiler) isIntegerExpr(e Expr) bool {
	if e == nil {
		return false
	}
	switch expr := e.(type) {
	case *LiteralExpr:
		return expr.Val.Type == storage.TypeInt && !expr.Val.IsNull
	case *VariableExpr:
		t, ok := c.varTypes[cleanVarName(expr.Name)]
		return ok && t == storage.TypeInt
	case *BinaryExpr:
		switch expr.Op {
		case TokenPlus, TokenMinus, TokenAsterisk:
			return c.isIntegerExpr(expr.Left) && c.isIntegerExpr(expr.Right)
		}
	case *UnaryExpr:
		if expr.Op == TokenMinus {
			return c.isIntegerExpr(expr.Expr)
		}
	}
	return false
}

func (c *Compiler) compileExpr(expr Expr) (int32, error) {
	if expr == nil {
		r := c.allocReg()
		cIdx := c.internConst(storage.NullValue(storage.TypeUnknown))
		c.emit(OpLoadConst, r, cIdx, 0, nil)
		return r, nil
	}

	switch e := expr.(type) {
	case *LiteralExpr:
		r := c.allocReg()
		cIdx := c.internConst(e.Val)
		c.emit(OpLoadConst, r, cIdx, 0, nil)
		return r, nil

	case *VariableExpr:
		r := c.allocReg()
		varName := cleanVarName(e.Name)
		slot := c.getOrAllocSlot(varName)
		c.emit(OpLoadVar, r, slot, 0, varName)
		return r, nil

	case *ColumnRefExpr:
		r := c.allocReg()
		varName := cleanVarName(e.Name)
		slot := c.getSlot(varName)
		c.emit(OpLoadVar, r, slot, 0, varName)
		return r, nil

	case *UnaryExpr:
		valReg, err := c.compileExpr(e.Expr)
		if err != nil {
			return 0, err
		}
		switch e.Op {
		case TokenMinus:
			c.emit(OpNeg, valReg, valReg, 0, nil)
		case TokenNot:
			c.emit(OpNot, valReg, valReg, 0, nil)
		default:
			return 0, fmt.Errorf("unsupported unary operator: %v", e.Op)
		}
		return valReg, nil

	case *BinaryExpr:
		leftReg, err := c.compileExpr(e.Left)
		if err != nil {
			return 0, err
		}
		rightReg, err := c.compileExpr(e.Right)
		if err != nil {
			return 0, err
		}
		isInt := c.isIntegerExpr(e.Left) && c.isIntegerExpr(e.Right)
		switch e.Op {
		case TokenPlus:
			if isInt {
				c.emit(OpAddInt, leftReg, leftReg, rightReg, nil)
			} else {
				c.emit(OpAdd, leftReg, leftReg, rightReg, nil)
			}
		case TokenMinus:
			if isInt {
				c.emit(OpSubInt, leftReg, leftReg, rightReg, nil)
			} else {
				c.emit(OpSub, leftReg, leftReg, rightReg, nil)
			}
		case TokenAsterisk:
			if isInt {
				c.emit(OpMulInt, leftReg, leftReg, rightReg, nil)
			} else {
				c.emit(OpMul, leftReg, leftReg, rightReg, nil)
			}
		case TokenSlash:
			c.emit(OpDiv, leftReg, leftReg, rightReg, nil)
		case TokenEq:
			if isInt {
				c.emit(OpEqInt, leftReg, leftReg, rightReg, nil)
			} else {
				c.emit(OpEq, leftReg, leftReg, rightReg, nil)
			}
		case TokenNeq:
			if isInt {
				c.emit(OpNeqInt, leftReg, leftReg, rightReg, nil)
			} else {
				c.emit(OpNeq, leftReg, leftReg, rightReg, nil)
			}
		case TokenLt:
			if isInt {
				c.emit(OpLtInt, leftReg, leftReg, rightReg, nil)
			} else {
				c.emit(OpLt, leftReg, leftReg, rightReg, nil)
			}
		case TokenLte:
			if isInt {
				c.emit(OpLteInt, leftReg, leftReg, rightReg, nil)
			} else {
				c.emit(OpLte, leftReg, leftReg, rightReg, nil)
			}
		case TokenGt:
			if isInt {
				c.emit(OpGtInt, leftReg, leftReg, rightReg, nil)
			} else {
				c.emit(OpGt, leftReg, leftReg, rightReg, nil)
			}
		case TokenGte:
			if isInt {
				c.emit(OpGteInt, leftReg, leftReg, rightReg, nil)
			} else {
				c.emit(OpGte, leftReg, leftReg, rightReg, nil)
			}
		case TokenAnd:
			c.emit(OpAnd, leftReg, leftReg, rightReg, nil)
		case TokenOr:
			c.emit(OpOr, leftReg, leftReg, rightReg, nil)
		default:
			return 0, fmt.Errorf("unsupported binary operator: %v", e.Op)
		}
		c.freeReg(1)
		return leftReg, nil

	case *InExpr:
		valReg, err := c.compileExpr(e.Expr)
		if err != nil {
			return 0, err
		}
		var itemRegs []int32
		for _, item := range e.List {
			ir, err := c.compileExpr(item)
			if err != nil {
				return 0, err
			}
			itemRegs = append(itemRegs, ir)
		}
		c.emit(OpIn, valReg, valReg, int32(len(itemRegs)), &CallData{
			ArgRegs: itemRegs,
		})
		c.freeReg(int32(len(itemRegs)))
		if e.Not {
			c.emit(OpNot, valReg, valReg, 0, nil)
		}
		return valReg, nil

	case *BetweenExpr:
		valReg, err := c.compileExpr(e.Expr)
		if err != nil {
			return 0, err
		}
		lowReg, err := c.compileExpr(e.Low)
		if err != nil {
			return 0, err
		}
		highReg, err := c.compileExpr(e.High)
		if err != nil {
			return 0, err
		}
		var flags uint8
		if e.Not {
			flags = 1
		}
		c.emitWithFlags(OpBetween, flags, valReg, lowReg, highReg, nil)
		c.freeReg(2)
		return valReg, nil

	case *LikeExpr:
		valReg, err := c.compileExpr(e.Expr)
		if err != nil {
			return 0, err
		}
		patReg, err := c.compileExpr(e.Pattern)
		if err != nil {
			return 0, err
		}
		var flags uint8
		if e.Not {
			flags = 1
		}
		c.emitWithFlags(OpLike, flags, valReg, valReg, patReg, nil)
		c.freeReg(1)
		return valReg, nil

	case *IsNullExpr:
		valReg, err := c.compileExpr(e.Expr)
		if err != nil {
			return 0, err
		}
		if e.Not {
			c.emit(OpIsNotNull, valReg, valReg, 0, nil)
		} else {
			c.emit(OpIsNull, valReg, valReg, 0, nil)
		}
		return valReg, nil

	case *CaseExpr:
		r := c.allocReg()
		var endJumps []int

		for _, w := range e.Whens {
			condReg, err := c.compileExpr(w.Condition)
			if err != nil {
				return 0, err
			}
			jmpNext := c.emit(OpJumpIfFalse, condReg, 0, 0, nil)
			c.freeReg(1)

			resReg, err := c.compileExpr(w.Result)
			if err != nil {
				return 0, err
			}
			c.emit(OpCopy, r, resReg, 0, nil)
			c.freeReg(1)

			endJmp := c.emit(OpJump, 0, 0, 0, nil)
			endJumps = append(endJumps, endJmp)
			c.prog.Instructions[jmpNext].B = int32(c.currentPC())
		}

		if e.Else != nil {
			elseReg, err := c.compileExpr(e.Else)
			if err != nil {
				return 0, err
			}
			c.emit(OpCopy, r, elseReg, 0, nil)
			c.freeReg(1)
		} else {
			cIdx := c.internConst(storage.NullValue(storage.TypeUnknown))
			c.emit(OpLoadConst, r, cIdx, 0, nil)
		}

		endPC := c.currentPC()
		for _, jmp := range endJumps {
			c.prog.Instructions[jmp].B = int32(endPC)
		}
		return r, nil

	case *FunctionCallExpr:
		var argRegs []int32
		for _, a := range e.Args {
			ar, err := c.compileExpr(a)
			if err != nil {
				return 0, err
			}
			argRegs = append(argRegs, ar)
		}
		r := c.allocReg()
		fnName := strings.ToUpper(e.Name)
		if isBuiltinFunction(fnName) {
			c.emit(OpCallBuiltin, r, int32(len(argRegs)), 0, &CallData{
				Name:    fnName,
				ArgRegs: argRegs,
			})
		} else {
			c.emit(OpCallUDF, r, int32(len(argRegs)), 0, &CallData{
				Name:    e.Name,
				ArgRegs: argRegs,
			})
		}
		c.freeReg(int32(len(argRegs)))
		return r, nil

	case *ScalarSubqueryExpr:
		col := e.Column
		tbl := e.Table
		whereVal := e.Where
		if e.Query != nil {
			if len(e.Query.Projections) > 0 {
				col = e.Query.Projections[0].String()
			}
			tbl = e.Query.From.Name
			whereVal = e.Query.Where
		}
		r := c.allocReg()
		c.emit(OpSelectInto, r, 0, 0, &SelectIntoData{
			Column:   col,
			Table:    tbl,
			WhereVal: whereVal,
		})
		return r, nil

	default:
		return 0, fmt.Errorf("unsupported expression: %T", expr)
	}
}

func isBuiltinFunction(name string) bool {
	return IsBuiltinFunction(name)
}

func cleanVarName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "@@") {
		return name
	}
	name = strings.TrimPrefix(name, "@")
	name = strings.Trim(name, "[]\"'`")
	if strings.HasPrefix(strings.ToLower(name), "dbo.") {
		name = name[4:]
	}
	return strings.Trim(name, "[]\"'`")
}
