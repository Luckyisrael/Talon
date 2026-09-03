package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// seedPkg writes a fake installed package under node_modules so the inspector has
// something real to read.
func seedPkg(t *testing.T, fs *FS, pkg string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		seed(t, fs, "node_modules/"+pkg+"/"+rel, content)
	}
}

func TestInspectPackage(t *testing.T) {
	t.Run("not installed points at install_package", func(t *testing.T) {
		fs := newTestFS(t)
		ip := &InspectPackage{WorkspacePath: fs.Root()}
		res, _ := ip.Execute(context.Background(), rawArgs(t, map[string]any{"name": "expo-router"}))
		if !res.IsError || !strings.Contains(res.Content, "not installed") {
			t.Errorf("want a not-installed error, got is_error=%v: %s", res.IsError, res.Content)
		}
	})

	t.Run("types field: returns the declared surface", func(t *testing.T) {
		fs := newTestFS(t)
		seedPkg(t, fs, "expo-router", map[string]string{
			"package.json": `{"version":"3.0.0","types":"index.d.ts"}`,
			"index.d.ts":   "export declare function useRouter(): Router;\nexport interface Router { push(href: string): void }\n",
		})
		ip := &InspectPackage{WorkspacePath: fs.Root()}
		res, _ := ip.Execute(context.Background(), rawArgs(t, map[string]any{"name": "expo-router"}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		for _, want := range []string{"expo-router@3.0.0 — types from index.d.ts", "useRouter", "Router"} {
			if !strings.Contains(res.Content, want) {
				t.Errorf("want %q in output, got:\n%s", want, res.Content)
			}
		}
	})

	t.Run("subpath name is reduced to the package root", func(t *testing.T) {
		fs := newTestFS(t)
		seedPkg(t, fs, "expo-router", map[string]string{
			"package.json": `{"version":"3.0.0","types":"index.d.ts"}`,
			"index.d.ts":   "export declare const ok: true;\n",
		})
		ip := &InspectPackage{WorkspacePath: fs.Root()}
		// The model often passes an import subpath rather than the bare package.
		res, _ := ip.Execute(context.Background(), rawArgs(t, map[string]any{"name": "expo-router/build/hooks"}))
		if res.IsError || !strings.Contains(res.Content, "expo-router@3.0.0") {
			t.Errorf("subpath not reduced to root, got is_error=%v: %s", res.IsError, res.Content)
		}
	})

	t.Run("exports map: types resolved through a condition", func(t *testing.T) {
		fs := newTestFS(t)
		seedPkg(t, fs, "pkg", map[string]string{
			"package.json":    `{"version":"2.1.0","exports":{".":{"types":"./dist/index.d.ts","import":"./dist/index.js"}}}`,
			"dist/index.d.ts": "export declare const VERSION: string;\n",
		})
		ip := &InspectPackage{WorkspacePath: fs.Root()}
		res, _ := ip.Execute(context.Background(), rawArgs(t, map[string]any{"name": "pkg"}))
		if res.IsError || !strings.Contains(res.Content, "types from dist/index.d.ts") || !strings.Contains(res.Content, "VERSION") {
			t.Errorf("exports-map types not resolved, got is_error=%v:\n%s", res.IsError, res.Content)
		}
	})

	t.Run("barrel entry is followed into sibling declarations", func(t *testing.T) {
		fs := newTestFS(t)
		seedPkg(t, fs, "barrel", map[string]string{
			"package.json": `{"version":"1.0.0","types":"index.d.ts"}`,
			"index.d.ts":   "export * from './hooks';\nexport * from './types';\n",
			"hooks.d.ts":   "export declare function useThing(): void;\n",
			"types.d.ts":   "export interface Thing { id: string }\n",
		})
		ip := &InspectPackage{WorkspacePath: fs.Root()}
		res, _ := ip.Execute(context.Background(), rawArgs(t, map[string]any{"name": "barrel"}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		// The whole point of following a barrel: the real signatures, not a wall of
		// `export * from`, and a marker showing where each came from.
		for _, want := range []string{"useThing", "Thing", "./hooks"} {
			if !strings.Contains(res.Content, want) {
				t.Errorf("barrel not followed: missing %q in:\n%s", want, res.Content)
			}
		}
	})

	t.Run("installed but shipping no declarations suggests @types", func(t *testing.T) {
		fs := newTestFS(t)
		seedPkg(t, fs, "plainjs", map[string]string{
			"package.json": `{"version":"0.2.0","main":"index.js"}`,
			"index.js":     "module.exports = {}\n",
		})
		ip := &InspectPackage{WorkspacePath: fs.Root()}
		res, _ := ip.Execute(context.Background(), rawArgs(t, map[string]any{"name": "plainjs"}))
		// Not an error — the package is installed, it just carries no types.
		if res.IsError {
			t.Fatalf("no-types is not a tool error, got: %s", res.Content)
		}
		for _, want := range []string{"ships no TypeScript declarations", "@types/plainjs"} {
			if !strings.Contains(res.Content, want) {
				t.Errorf("want %q, got:\n%s", want, res.Content)
			}
		}
	})

	t.Run("blank name is rejected", func(t *testing.T) {
		fs := newTestFS(t)
		ip := &InspectPackage{WorkspacePath: fs.Root()}
		res, _ := ip.Execute(context.Background(), rawArgs(t, map[string]any{"name": "   "}))
		if !res.IsError {
			t.Errorf("want an error for a blank name, got: %s", res.Content)
		}
	})

	t.Run("inspect_package is read-only", func(t *testing.T) {
		if !(&InspectPackage{}).ReadOnly() {
			t.Error("inspect_package must be read-only")
		}
	})
}

func TestPackageRoot(t *testing.T) {
	cases := map[string]string{
		"expo-router":                   "expo-router",
		"expo-router/build/hooks":       "expo-router",
		"@solana-mobile/adapter":        "@solana-mobile/adapter",
		"@solana-mobile/adapter/web3js": "@solana-mobile/adapter",
		"@scope":                        "", // a scope with no package name is not a package
		"../evil":                       "", // traversal is refused before any path is built
		"/abs":                          "",
		"":                              "",
	}
	for in, want := range cases {
		if got := packageRoot(in); got != want {
			t.Errorf("packageRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindTypesEntry(t *testing.T) {
	parse := func(s string) any {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatalf("bad json %q: %v", s, err)
		}
		return v
	}
	cases := []struct {
		name    string
		exports string
		want    string
	}{
		{"nested types condition", `{".":{"types":"./dist/index.d.ts","import":"./dist/index.js"}}`, "./dist/index.d.ts"},
		{"top-level typings key", `{"typings":"./main.d.ts"}`, "./main.d.ts"},
		{"fallback to any d.ts string", `{".":{"import":"./y.d.ts"}}`, "./y.d.ts"},
		{"bare string entry", `"./index.d.ts"`, "./index.d.ts"},
		{"no declarations at all", `{".":{"import":"./y.js"}}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := findTypesEntry(parse(c.exports)); got != c.want {
				t.Errorf("findTypesEntry = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveDTS(t *testing.T) {
	fs := newTestFS(t)
	seedPkg(t, fs, "p", map[string]string{
		"index.d.ts":     "x",
		"hooks.d.ts":     "x",
		"sub/index.d.ts": "x",
	})
	ip := &InspectPackage{WorkspacePath: fs.Root()}

	cases := []struct {
		rel  string
		want string
	}{
		{"./hooks", "node_modules/p/hooks.d.ts"},   // sibling .d.ts
		{"./sub", "node_modules/p/sub/index.d.ts"}, // directory → its index.d.ts
		{"./ghost", ""},             // nothing there
		{"../../../etc/passwd", ""}, // escape attempt is refused
	}
	for _, c := range cases {
		if got := ip.resolveDTS("node_modules/p", "index.d.ts", c.rel); got != c.want {
			t.Errorf("resolveDTS(%q) = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestLocalReexports(t *testing.T) {
	src := "export * from './a';\n" +
		"import './b';\n" +
		"export { x } from 'external-pkg';\n" +
		"import y from './c';\n"
	got := localReexports(src)
	want := map[string]bool{"./a": true, "./b": true, "./c": true}
	if len(got) != len(want) {
		t.Fatalf("localReexports = %v, want the 3 local specifiers only", got)
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("unexpected specifier %q (an external package should be dropped): %v", s, got)
		}
	}
}
