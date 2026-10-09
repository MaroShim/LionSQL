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
			VarNames:     make([]string, 0),
		},
		constMap:        make(map[string]int32),
		varNames:        make(map[string]bool),
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
	c.emit(OpReturn, -1, 0, 0, nil)
	c.prog.NumRegisters = c.maxRegisters
	return c.prog, nil
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

	c.emit(OpReturn, -1, 0, 0, nil)
	c.prog.NumRegisters = c.maxRegisters
	return c.prog, nil
}

// CompileProcedure compiles a CreateProcStmt into a Program.
func CompileProcedure(stmt *CreateProcStmt) (*Program, error) {
	c := NewCompiler()
	c.prog.Name = stmt.Name
	c.prog.Type = "PROCEDURE"
	c.prog.Params = stmt.Params

	for _, p := range stmt.Params {
		c.varNames[cleanVarName(p.Name)] = true
	}

	if err := c.compileStatement(stmt.Body); err != nil {
		return nil, err
	}
	if err := c.resolveGotos(); err != nil {
		return nil, err
	}

	c.emit(OpReturn, -1, 0, 0, nil)
	c.prog.NumRegisters = c.maxRegisters
	return c.prog, nil
}

// CompileFunction compiles a CreateFunctionStmt into a Program.
func CompileFunction(stmt *CreateFunctionStmt) (*Program, error) {
	c := NewCompiler()
	c.prog.Name = stmt.Name
	c.prog.Type = "FUNCTION"
	c.prog.Params = stmt.Params
	c.prog.ReturnType = stmt.ReturnType

	for _, p := range stmt.Params {
		c.varNames[cleanVarName(p.Name)] = true
	}

	if err := c.compileStatement(stmt.Body); err != nil {
		return nil, err
	}
	if err := c.resolveGotos(); err != nil {
		return nil, err
	}

	c.emit(OpReturn, -1, 0, 0, nil)
	c.prog.NumRegisters = c.maxRegisters
	return c.prog, nil
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

	c.emit(OpReturn, -1, 0, 0, nil)
	c.prog.NumRegisters = c.maxRegisters
	return c.prog, nil
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
	pc := len(c.prog.Instructions)
	c.prog.Instructions = append(c.prog.Instructions, Instruction{
		Op:   op,
		A:    a,
		B:    b,
		C:    cReg,
		Data: data,
	})
	return pc
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
		c.varNames[varName] = true
		c.prog.VarNames = append(c.prog.VarNames, varName)
		return nil

	case *SetStmt:
		valReg, err := c.compileExpr(s.Value)
		if err != nil {
			return err
		}
		varName := cleanVarName(s.Name)
		c.emit(OpStoreVar, valReg, 0, 0, varName)
		c.freeReg(1)
		return nil

	case *SetOptionStmt:
		for k, v := range s.Options {
			var bVal bool = true
			if strings.EqualFold(v, "OFF") || v == "0" {
				bVal = false
			}
			reg := c.allocReg()
			c.emit(OpLoadConst, reg, 0, 0, storage.BoolValue(bVal))
			c.emit(OpStoreVar, reg, 0, 0, strings.ToLower(k))
			c.freeReg(1)
		}
		return nil

	case *IfStmt:
		condReg, err := c.compileExpr(s.Condition)
		if err != nil {
			return err
		}
		jmpFalse := c.emit(OpJumpIfFalse, condReg, 0, 0, nil)
		c.freeReg(1)

		if err := c.compileStatement(s.Then); err != nil {
			return err
		}

		if s.Else != nil {
			jmpEnd := c.emit(OpJump, 0, 0, 0, nil)
			c.prog.Instructions[jmpFalse].B = int32(c.currentPC())
			if err := c.compileStatement(s.Else); err != nil {
				return err
			}
			c.prog.Instructions[jmpEnd].B = int32(c.currentPC())
		} else {
			c.prog.Instructions[jmpFalse].B = int32(c.currentPC())
		}
		return nil

	case *WhileStmt:
		loopStart := c.currentPC()
		c.loopBreaks = append(c.loopBreaks, []int{})
		c.loopContinues = append(c.loopContinues, []int{})

		condReg, err := c.compileExpr(s.Condition)
		if err != nil {
			return err
		}
		jmpEnd := c.emit(OpJumpIfFalse, condReg, 0, 0, nil)
		c.freeReg(1)

		if err := c.compileStatement(s.Body); err != nil {
			return err
		}

		c.emit(OpJump, 0, int32(loopStart), 0, nil)
		endPC := c.currentPC()
		c.prog.Instructions[jmpEnd].B = int32(endPC)

		breaks := c.loopBreaks[len(c.loopBreaks)-1]
		c.loopBreaks = c.loopBreaks[:len(c.loopBreaks)-1]
		for _, bPC := range breaks {
			c.prog.Instructions[bPC].B = int32(endPC)
		}

		continues := c.loopContinues[len(c.loopContinues)-1]
		c.loopContinues = c.loopContinues[:len(c.loopContinues)-1]
		for _, cPC := range continues {
			c.prog.Instructions[cPC].B = int32(loopStart)
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
		c.emit(OpSelectInto, 0, 0, 0, &SelectIntoData{
			Column:   s.Column,
			IntoVar:  cleanVarName(s.IntoVar),
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
			vars = append(vars, cleanVarName(v))
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
		c.emit(OpLoadVar, r, 0, 0, varName)
		return r, nil

	case *ColumnRefExpr:
		r := c.allocReg()
		c.emit(OpLoadVar, r, 0, 0, cleanVarName(e.Name))
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
		switch e.Op {
		case TokenPlus:
			c.emit(OpAdd, leftReg, leftReg, rightReg, nil)
		case TokenMinus:
			c.emit(OpSub, leftReg, leftReg, rightReg, nil)
		case TokenAsterisk:
			c.emit(OpMul, leftReg, leftReg, rightReg, nil)
		case TokenSlash:
			c.emit(OpDiv, leftReg, leftReg, rightReg, nil)
		case TokenEq:
			c.emit(OpEq, leftReg, leftReg, rightReg, nil)
		case TokenNeq:
			c.emit(OpNeq, leftReg, leftReg, rightReg, nil)
		case TokenLt:
			c.emit(OpLt, leftReg, leftReg, rightReg, nil)
		case TokenLte:
			c.emit(OpLte, leftReg, leftReg, rightReg, nil)
		case TokenGt:
			c.emit(OpGt, leftReg, leftReg, rightReg, nil)
		case TokenGte:
			c.emit(OpGte, leftReg, leftReg, rightReg, nil)
		case TokenAnd:
			c.emit(OpEq, leftReg, leftReg, rightReg, "AND")
		case TokenOr:
			c.emit(OpEq, leftReg, leftReg, rightReg, "OR")
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
		c.emit(OpBetween, valReg, valReg, lowReg, struct {
			HighReg int32
			Not     bool
		}{HighReg: highReg, Not: e.Not})
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
		c.emit(OpLike, valReg, valReg, patReg, e.Not)
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
