package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestArch_NoManualSQLParsingInDDL (ARCH-AST-GUARD-1) enforces that sqlexec_ddl.go
// never reintroduces manual SQL string slicing helpers (parse*, split*) or forbidden
// strings.* slicing functions (Index, LastIndex, Split, SplitN, Fields) or regexp imports.
func TestArch_NoManualSQLParsingInDDL(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sqlexec_ddl.go", nil, 0)
	if err != nil {
		t.Fatalf("failed to parse sqlexec_ddl.go: %v", err)
	}

	// 1. Forbid regexp imports (Invariant 7)
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == "regexp" {
			t.Errorf("[ARCH-GUARD] sqlexec_ddl.go imports forbidden package %q (Invariant 7)", path)
		}
	}

	// 2. Forbid local manual parser/splitter function declarations in sqlexec_ddl.go
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		name := fn.Name.Name
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "parse") || strings.HasPrefix(lower, "split") || strings.HasPrefix(lower, "cleandefault") {
			pos := fset.Position(fn.Pos())
			t.Errorf("[ARCH-GUARD] sqlexec_ddl.go:%d defines manual parsing helper %q; all DDL parsing must reside in pkg/proc",
				pos.Line, name)
		}
	}

	// 3. Forbid raw strings.Index, strings.LastIndex, strings.Split, strings.SplitN, strings.Fields in sqlexec_ddl.go (Invariant 12)
	forbiddenStringsFuncs := map[string]bool{
		"Index":     true,
		"LastIndex": true,
		"Split":     true,
		"SplitN":    true,
		"Fields":    true,
	}

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok || pkgIdent.Name != "strings" {
			return true
		}
		if forbiddenStringsFuncs[sel.Sel.Name] {
			pos := fset.Position(call.Pos())
			t.Errorf("[ARCH-GUARD] sqlexec_ddl.go:%d:%d calls forbidden strings.%s; use pkg/proc AST or pkg/sqlutil (Invariant 12)",
				pos.Line, pos.Column, sel.Sel.Name)
		}
		return true
	})
}
