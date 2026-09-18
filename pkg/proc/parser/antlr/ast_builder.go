package parser

import (
	"strings"

	"github.com/antlr4-go/antlr/v4"
	"sqlion/pkg/proc"
	"sqlion/pkg/storage"
)

// ASTBuilder traverses an ANTLR T-SQL CST and builds SQLion's executable AST.
type ASTBuilder struct {
	BaseTSqlParserVisitor
}

// NewASTBuilder creates a new ASTBuilder.
func NewASTBuilder() *ASTBuilder {
	return &ASTBuilder{}
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
	if err != nil {
		// If native parser fails, fallback to visitor builder
		builder := NewASTBuilder()
		if node, ok := builder.Visit(res.Tree).(*proc.CreateProcStmt); ok && node != nil {
			return node, nil
		}
		return nil, err
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

	return &proc.CreateProcStmt{
		Name:   procName,
		Params: params,
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
	default:
		return storage.TypeVarChar
	}
}
