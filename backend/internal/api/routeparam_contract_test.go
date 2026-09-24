package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Route/param law: every chi URLParam a handler reads must be declared by the
// route pattern(s) the server mounts that handler under.
//
// Why this exists: manual live-API testing found GET
// /v1/projects/{projectID}/conversation/search always answering 400 — the
// handler read "id" while the route declared "{projectID}". Handler-level
// tests inject RouteContext params by hand, so they mirror the handler's
// assumption and cannot see the mismatch; only a request through the real
// route table does. This test closes the class: it reads the actual route
// registrations out of cmd/server/main.go and cross-checks the param names
// each handler method reads in this package.
//
// How the two sides are joined: main.go mounts `x.Method(...)` with a
// variable, and this package declares `func (h *T) Method(...)`. The variable
// is resolved to T through its constructor call (x := api.NewT(...) against
// NewT's declared return type), so Get on UsageHandler and Get on
// ProjectHandler are checked against their own routes, not pooled.
//
// Honest limits:
//   - A handler variable not built by an api.New* constructor is skipped
//     (reported via t.Log, never silently): no receiver type, no join.
//   - URLParam calls inside package-level helpers (e.g. providerParam) are
//     not attributed to their callers; only method bodies are checked.
//   - Only string-literal param names are collected; a computed name would be
//     skipped rather than guessed.
//   - A method mounted under several patterns must declare every param it
//     reads on every one of them — correct for unconditional reads, too
//     strict for a genuinely optional param, which no current handler has.
func TestRouteParamContract(t *testing.T) {
	ctorRet := constructorReturnTypes(t) // NewT -> "*T" (raw expr text)
	varToType := handlerVars(t, ctorRet)
	routes := routeTable(t, varToType)
	if len(routes) == 0 {
		t.Fatal("no routes parsed from cmd/server/main.go — the parser is broken, not the server")
	}
	params := handlerURLParams(t)

	checked := 0
	var skippedKeys []string
	for key, names := range params {
		patterns, registered := routes[key]
		if !registered {
			skippedKeys = append(skippedKeys, key)
			continue // method not mounted (helper, or an unresolvable var)
		}
		checked++
		for _, pattern := range patterns {
			declared := routeParamNames(pattern)
			for name := range names {
				if !declared[name] {
					t.Errorf("%s reads chi.URLParam(%q) but is mounted under %q, which declares %v",
						key, name, pattern, sortedKeys(declared))
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no handler params cross-checked — parser drifted from the source layout")
	}
	sort.Strings(skippedKeys)
	t.Logf("cross-checked %d handlers against %d route registrations; skipped (no mount found): %v",
		checked, len(routes), skippedKeys)
}

// routeTable maps "Type.Method" -> every route pattern the server mounts it
// under, parsed from the real server source.
func routeTable(t *testing.T, varToType map[string]string) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("..", "..", "cmd", "server", "main.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse server main.go: %v", err)
	}

	mount := map[string]bool{
		"Get": true, "Post": true, "Put": true, "Patch": true, "Delete": true,
	}
	out := map[string][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !mount[sel.Sel.Name] || len(call.Args) != 2 {
			return true
		}
		pattern, ok := call.Args[0].(*ast.BasicLit)
		if !ok || pattern.Kind != token.STRING {
			return true // a computed pattern cannot be checked statically
		}
		path, err := strconv.Unquote(pattern.Value)
		if err != nil {
			return true
		}
		handler, ok := call.Args[1].(*ast.SelectorExpr) // usageHandler.Get
		if !ok {
			return true // func literals and bare funcs: no receiver to join on
		}
		varIdent, ok := handler.X.(*ast.Ident)
		if !ok {
			return true
		}
		typeName, known := varToType[varIdent.Name]
		if !known {
			return true // unresolvable var: skipped, per the law's limits
		}
		key := typeName + "." + handler.Sel.Name
		out[key] = append(out[key], path)
		return true
	})
	return out
}

// constructorReturnTypes maps each api.New* constructor to the type it
// constructs ("NewUsageHandler" -> "UsageHandler").
func constructorReturnTypes(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse api package: %v", err)
	}
	out := map[string]string{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "New") || fn.Type.Results == nil {
					continue
				}
				for _, field := range fn.Type.Results.List {
					if star, ok := field.Type.(*ast.StarExpr); ok {
						if ident, ok := star.X.(*ast.Ident); ok {
							out[fn.Name.Name] = ident.Name
						}
					}
				}
			}
		}
	}
	return out
}

// handlerVars maps handler variable name -> receiver type, from the
// `x := api.NewT(...)` assignments in main.go.
func handlerVars(t *testing.T, ctorRet map[string]string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("..", "..", "cmd", "server", "main.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse server main.go: %v", err)
	}
	out := map[string]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
				return true
			}
			lhs, ok := assign.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			call, ok := assign.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if typeName, known := ctorRet[sel.Sel.Name]; known {
				out[lhs.Name] = typeName
			}
			return true
		})
	}
	return out
}

// handlerURLParams maps "Type.Method" -> the set of chi.URLParam string
// literals read directly in that method's body.
func handlerURLParams(t *testing.T) map[string]map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse api package: %v", err)
	}

	out := map[string]map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || fn.Body == nil {
					continue // helpers and free functions are out of scope
				}
				recvType, ok := receiverTypeName(fn)
				if !ok {
					continue
				}
				key := recvType + "." + fn.Name.Name
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "URLParam" || len(call.Args) < 2 {
						return true
					}
					pkgIdent, ok := sel.X.(*ast.Ident)
					if !ok || pkgIdent.Name != "chi" {
						return true
					}
					lit, ok := call.Args[1].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return true // computed name: skipped, per the law's limits
					}
					name, err := strconv.Unquote(lit.Value)
					if err != nil {
						return true
					}
					if out[key] == nil {
						out[key] = map[string]bool{}
					}
					out[key][name] = true
					return true
				})
			}
		}
	}
	return out
}

// receiverTypeName unwraps (h *PromptHandler) to "PromptHandler".
func receiverTypeName(fn *ast.FuncDecl) (string, bool) {
	if len(fn.Recv.List) == 0 {
		return "", false
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

var routeParamRe = regexp.MustCompile(`\{(\w+)\}`)

func routeParamNames(pattern string) map[string]bool {
	out := map[string]bool{}
	for _, m := range routeParamRe.FindAllStringSubmatch(pattern, -1) {
		out[m[1]] = true
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
