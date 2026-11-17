package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestArch_NoManualSQLParsingInDDL (ARCH-AST-GUARD-1) enforces that DDL and cursor execution
// modules never reintroduce manual SQL string slicing helpers (parse*, split*) or forbidden
// strings.* slicing functions (Index, LastIndex, Split, SplitN, Fields) or regexp imports.
func TestArch_NoManualSQLParsingInDDL(t *testing.T) {
	targetFiles := []string{
		"sqlexec_ddl.go",
		"sqlexec_cursor.go",
		"sqlexec_conditional_ddl.go",
	}

	forbiddenStringsFuncs := map[string]bool{
		"Index":     true,
		"LastIndex": true,
		"Split":     true,
		"SplitN":    true,
		"Fields":    true,
	}

	fset := token.NewFileSet()
	for _, filename := range targetFiles {
		f, err := parser.ParseFile(fset, filename, nil, 0)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", filename, err)
		}

		// 1. Forbid regexp imports (Invariant 7)
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "regexp" {
				t.Errorf("[ARCH-GUARD] %s imports forbidden package %q (Invariant 7)", filename, path)
			}
		}

		// 2. Forbid local manual parser/splitter function declarations
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			name := fn.Name.Name
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "parse") || (strings.HasPrefix(lower, "split") && name != "splitTopLevelElse") || strings.HasPrefix(lower, "cleandefault") {
				pos := fset.Position(fn.Pos())
				t.Errorf("[ARCH-GUARD] %s:%d defines manual parsing helper %q; all SQL parsing must reside in pkg/proc",
					filename, pos.Line, name)
			}
		}

		// 3. Forbid raw strings.Index, strings.LastIndex, strings.Split, strings.SplitN, strings.Fields (Invariant 12)
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
				t.Errorf("[ARCH-GUARD] %s:%d:%d calls forbidden strings.%s; use pkg/proc AST or pkg/sqlutil (Invariant 12)",
					filename, pos.Line, pos.Column, sel.Sel.Name)
			}
			return true
		})
	}
}
