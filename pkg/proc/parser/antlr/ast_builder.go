package parser

import (
	"fmt"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

func init() {
	proc.RegisterProcedureFallback(BuildProcedure)
}

// ASTBuilder traverses an ANTLR T-SQL CST and builds LionSQL's executable AST.
type ASTBuilder struct {
	BaseTSqlParserVisitor
	script string
}

// NewASTBuilder creates a new ASTBuilder.
func NewASTBuilder(script string) *ASTBuilder {
	return &ASTBuilder{script: script}
}

// BuildProcedure parses a CREATE PROCEDURE T-SQL script using ANTLR and builds a CreateProcStmt AST.
func BuildProcedure(script string) (*proc.CreateProcStmt, error) {
	// 1. Verify and parse syntax using ANTLR
	res, err := ParseTSql(script)
	if err != nil {
		return nil, err
	}

	// 2. Fast native Pratt parser generates the high-performance AST
	lexer := proc.NewLexer(script)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil || plan == nil || plan.Body == nil {
		// If native parser fails, fallback to visitor builder
		builder := NewASTBuilder(script)
		val := builder.Visit(res.Tree)
		if node, ok := val.(*proc.CreateProcStmt); ok && node != nil && node.Body != nil {
			return node, nil
		}
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("ANTLR fallback failed to build procedure AST from script")
	}

	return plan, nil
}

func (b *ASTBuilder) Visit(tree antlr.ParseTree) interface{} {
	if tree == nil {
		return nil
	}
	return tree.Accept(b)
}

func (b *ASTBuilder) VisitTsql_file(ctx *Tsql_fileContext) interface{} {
	for _, batch := range ctx.AllBatch() {
		if res := b.Visit(batch); res != nil {
			return res
		}
	}
	return nil
}

func (b *ASTBuilder) VisitBatch(ctx *BatchContext) interface{} {
	if ctx.Batch_level_statement() != nil {
		return b.Visit(ctx.Batch_level_statement())
	}
	for _, clause := range ctx.AllSql_clauses() {
		if res := b.Visit(clause); res != nil {
			return res
		}
	}
	return nil
}

func (b *ASTBuilder) VisitBatch_level_statement(ctx *Batch_level_statementContext) interface{} {
	if ctx.Create_or_alter_procedure() != nil {
		return b.Visit(ctx.Create_or_alter_procedure())
	}
	return nil
}

func (b *ASTBuilder) VisitCreate_or_alter_procedure(ctx *Create_or_alter_procedureContext) interface{} {
	var procName string
	if ctx.Func_proc_name_schema() != nil {
		procName = ctx.Func_proc_name_schema().GetText()
		if idx := strings.LastIndex(procName, "."); idx >= 0 {
			procName = procName[idx+1:]
		}
		procName = strings.Trim(procName, "[]")
	}

	params := make([]proc.ParamDef, 0)
	for _, p := range ctx.AllProcedure_param() {
		pName := p.LOCAL_ID().GetText()
		pType := storage.TypeVarChar
		if p.Data_type() != nil {
			pType = parseDataTypeString(p.Data_type().GetText())
		}
		params = append(params, proc.ParamDef{Name: pName, Type: pType})
	}

	var stmts []proc.Statement
	clauses := ctx.AllSql_clauses()
	for _, clause := range clauses {
		startTok := clause.GetStart()
		stopTok := clause.GetStop()
		if startTok == nil || stopTok == nil {
			continue
		}
		var clauseText string
		startIdx := startTok.GetStart()
		stopIdx := stopTok.GetStop() + 1
		if b.script != "" && startIdx >= 0 && stopIdx <= len(b.script) && startIdx < stopIdx {
			clauseText = strings.TrimSpace(b.script[startIdx:stopIdx])
		}
		if clauseText == "" || clauseText == ";" {
			continue
		}
		cLexer := proc.NewLexer(clauseText)
		cParser := proc.NewParser(cLexer)
		s, sErr := cParser.ParseStatement()
		if sErr == nil && s != nil {
			stmts = append(stmts, s)
		}
	}

	var body proc.Statement
	if len(stmts) == 1 {
		body = stmts[0]
	} else if len(stmts) > 1 {
		body = &proc.BlockStmt{Statements: stmts}
	}

	return &proc.CreateProcStmt{
		Name:   procName,
		Params: params,
		Body:   body,
	}
}

func parseDataTypeString(s string) storage.DataType {
	switch strings.ToUpper(s) {
	case "INT", "INTEGER", "BIGINT":
		return storage.TypeInt
	case "VARCHAR", "TEXT", "NVARCHAR", "CHAR":
		return storage.TypeVarChar
	case "FLOAT", "DECIMAL", "DOUBLE", "NUMERIC":
		return storage.TypeFloat
	case "BIT", "BOOL", "BOOLEAN":
		return storage.TypeBool
	case "DATETIME", "TIMESTAMP":
		return storage.TypeDateTime
	case "DATE":
		return storage.TypeDate
	case "TIME":
		return storage.TypeTime
	case "UUID", "UNIQUEIDENTIFIER":
		return storage.TypeUUID
	case "VARBINARY", "BINARY", "BYTEA":
		return storage.TypeBinary
	default:
		return storage.TypeVarChar
	}
}
