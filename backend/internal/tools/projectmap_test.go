package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestProjectMapText(t *testing.T) {
	t.Run("empty project yields no map", func(t *testing.T) {
		fs := newTestFS(t)
		// Non-source files must not count: a README and a type declaration are not
		// the project's own code, so a project holding only those is still "fresh".
		seed(t, fs, "README.md", "# hi")
		seed(t, fs, "types/global.d.ts", "declare const x: number;")
		if got := ProjectMapText(fs.Root()); got != "" {
			t.Errorf("want empty map for a project with no source, got:\n%s", got)
		}
	})

	t.Run("extracts exports and imports, grouped by directory", func(t *testing.T) {
		fs := newTestFS(t)
		seed(t, fs, "app/index.tsx", "import { View } from 'react-native'\n"+
			"import { useWallet } from '../lib/wallet'\n"+
			"export function Home() { return null }\n"+
			"export const TITLE = 'x'\n"+
			"export default Home\n")
		seed(t, fs, "lib/wallet.ts", "export const useWallet = () => ({})\n")

		got := ProjectMapText(fs.Root())

		for _, want := range []string{
			"## Current project structure",
			"2 source file(s)",
			"app/",
			"lib/",
			// Exports render in declaration order with a trailing "default"; imports
			// are sorted, so the whole clause is deterministic.
			"index.tsx — exports Home, TITLE, default · imports ../lib/wallet, react-native",
			"wallet.ts — exports useWallet",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("map missing %q, got:\n%s", want, got)
			}
		}
	})

	t.Run("skips vendor dirs, dotfiles, and declaration files", func(t *testing.T) {
		fs := newTestFS(t)
		seed(t, fs, "index.js", "export const root = 1\n")
		seed(t, fs, "node_modules/react/index.js", "module.exports = {}\n")
		seed(t, fs, ".eslintrc.js", "module.exports = {}\n")
		seed(t, fs, "app/types.d.ts", "export declare const y: number;\n")

		got := ProjectMapText(fs.Root())

		if !strings.Contains(got, "(project root)") || !strings.Contains(got, "index.js") {
			t.Errorf("want the root source file mapped, got:\n%s", got)
		}
		// Only index.js is real source; the count proves the others were skipped.
		if !strings.Contains(got, "1 source file(s)") {
			t.Errorf("vendor/dotfile/declaration files were not all skipped, got:\n%s", got)
		}
		for _, absent := range []string{"node_modules", "eslintrc", "types.d.ts"} {
			if strings.Contains(got, absent) {
				t.Errorf("map should not mention %q, got:\n%s", absent, got)
			}
		}
	})

	t.Run("truncates a huge map with a pointer to the file tools", func(t *testing.T) {
		fs := newTestFS(t)
		// Enough files that the rendered lines blow past maxMapBytes (12KB).
		for i := 0; i < 300; i++ {
			seed(t, fs, fmt.Sprintf("app/comp%03d.tsx", i),
				fmt.Sprintf("export const Component%03dExportedName = 1\n", i))
		}
		got := ProjectMapText(fs.Root())
		if len(got) > maxMapBytes+256 {
			t.Errorf("map exceeded its cap: %d bytes", len(got))
		}
		if !strings.Contains(got, "[map truncated") {
			t.Errorf("truncation not announced, tail:\n%s", got[max(0, len(got)-120):])
		}
	})
}

func TestProjectMapTool(t *testing.T) {
	t.Run("fresh project explains itself instead of returning blank", func(t *testing.T) {
		fs := newTestFS(t)
		res, _ := (&ProjectMap{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{}))
		if res.IsError || !strings.Contains(res.Content, "fresh project") {
			t.Errorf("want a fresh-project message, got is_error=%v: %s", res.IsError, res.Content)
		}
	})

	t.Run("project_map is read-only", func(t *testing.T) {
		if !(&ProjectMap{}).ReadOnly() {
			t.Error("project_map must be read-only so it can run alongside other reads")
		}
	})
}
