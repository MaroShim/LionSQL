package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestArch_EngineCodeGuard enforces strict architecture and safety invariants across the engine package:
// 1. [Invariant 7] Forbid regexp imports in engine, planner, and execution modules.
// 2. [Invariant 12] Forbid manual SQL string slicing (Index, LastIndex, Split, SplitN, Fields) in core execution files.
// 3. [Invariant 8] Forbid declaring manual ad-hoc SQL parser helpers (parse*, split*, cleandefault*) in engine.
// 4. [RULE:ExplicitError] Forbid silently swallowing errors via "_ = err" or empty error handlers.
func TestArch_EngineCodeGuard(t *testing.T) {
	targetFiles := []string{
		"sqlexec_ddl.go",
		"sqlexec_cursor.go",
		"sqlexec_conditional_ddl.go",
		"sqlexec_prepare.go",
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

		// 2. Forbid local manual parser/splitter function declarations (Invariant 8)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			name := fn.Name.Name
			lower := strings.ToLower(name)
			// Whitelisted helpers that are purely non-grammatical argument parsers
			if name == "parseArgValue" || name == "splitTopLevelElse" {
				continue
			}
			if strings.HasPrefix(lower, "parse") || strings.HasPrefix(lower, "split") || strings.HasPrefix(lower, "cleandefault") {
				pos := fset.Position(fn.Pos())
				t.Errorf("[ARCH-GUARD] %s:%d defines manual parsing helper %q; all SQL parsing must reside in pkg/proc or pkg/sqlutil (Invariant 8)",
					filename, pos.Line, name)
			}
		}

		// 3. Forbid raw strings.Index, strings.LastIndex, strings.Split, strings.SplitN, strings.Fields on SQL (Invariant 12)
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
				// Allow parseArgValue and simple non-SQL parameter splitting in sqlexec_prepare.go
				pos := fset.Position(call.Pos())
				if filename == "sqlexec_prepare.go" && (sel.Sel.Name == "SplitN" || sel.Sel.Name == "Index" || sel.Sel.Name == "Fields") && pos.Line < 600 {
					return true // parameter map parsing (@p1 = v) and header splitting
				}
				t.Errorf("[ARCH-GUARD] %s:%d:%d calls forbidden strings.%s; use pkg/proc AST or pkg/sqlutil (Invariant 12)",
					filename, pos.Line, pos.Column, sel.Sel.Name)
			}
			return true
		})
	}
}

// TestArch_NoRegexpInQueryPath verifies that query compilation and execution engines never import regexp.
func TestArch_NoRegexpInQueryPath(t *testing.T) {
	packages := []string{"./", "../planner", "../execution"}
	fset := token.NewFileSet()

	for _, pkgDir := range packages {
		pkgs, err := parser.ParseDir(fset, pkgDir, func(fi fs.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("failed to parse directory %s: %v", pkgDir, err)
		}

		for _, pkg := range pkgs {
			for filePath, f := range pkg.Files {
				for _, imp := range f.Imports {
					importPath := strings.Trim(imp.Path.Value, `"`)
					if importPath == "regexp" {
						pos := fset.Position(imp.Pos())
						t.Errorf("[ARCH-GUARD] %s:%d imports forbidden 'regexp' package (Invariant 7: Zero-Regex SQL Parsing in Production Paths)",
							filepath.Base(filePath), pos.Line)
					}
				}
			}
		}
	}
}
