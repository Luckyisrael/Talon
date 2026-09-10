package tools

import (
	"context"
	"strings"
	"testing"
)

func TestFindFiles(t *testing.T) {
	fs := newTestFS(t)
	seed(t, fs, "app/index.tsx", "export default function Index() { return null }")
	seed(t, fs, "app/Button.tsx", "export const Button = () => null")
	seed(t, fs, "app/Card.tsx", "export const Card = () => null")
	seed(t, fs, "app/_layout.tsx", "export default () => null")
	seed(t, fs, "app/api/route.ts", "export const GET = () => null")
	seed(t, fs, "lib/state.ts", "export const state = {}")
	seed(t, fs, "node_modules/x.tsx", "ignored")
	seed(t, fs, ".git/HEAD", "ignored")

	t.Run("matches by extension", func(t *testing.T) {
		res, _ := (&FindFiles{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "*.tsx",
		}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		for _, want := range []string{"app/index.tsx", "app/Button.tsx", "app/Card.tsx", "app/_layout.tsx"} {
			if !strings.Contains(res.Content, want) {
				t.Errorf("expected %q in result, got:\n%s", want, res.Content)
			}
		}
		if strings.Contains(res.Content, "node_modules") {
			t.Errorf("did not expect node_modules in result:\n%s", res.Content)
		}
		if strings.Contains(res.Content, ".git") {
			t.Errorf("did not expect .git in result:\n%s", res.Content)
		}
	})

	t.Run("substring glob", func(t *testing.T) {
		res, _ := (&FindFiles{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "*utton*",
		}))
		if !strings.Contains(res.Content, "app/Button.tsx") {
			t.Errorf("expected Button.tsx in result:\n%s", res.Content)
		}
		if strings.Contains(res.Content, "Card.tsx") {
			t.Errorf("did not expect Card.tsx in result:\n%s", res.Content)
		}
	})

	t.Run("scoped to subdir", func(t *testing.T) {
		res, _ := (&FindFiles{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "*.ts", "path": "app",
		}))
		if !strings.Contains(res.Content, "app/api/route.ts") {
			t.Errorf("expected scoped match:\n%s", res.Content)
		}
		if strings.Contains(res.Content, "lib/state.ts") {
			t.Errorf("expected lib/state.ts to be out of scope:\n%s", res.Content)
		}
	})

	t.Run("rejects path escape", func(t *testing.T) {
		res, _ := (&FindFiles{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "*.tsx", "path": "../etc",
		}))
		if !res.IsError {
			t.Fatalf("expected path-escape error, got: %s", res.Content)
		}
	})

	t.Run("rejects empty pattern", func(t *testing.T) {
		res, _ := (&FindFiles{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "   ",
		}))
		if !res.IsError {
			t.Fatalf("expected error for empty pattern, got: %s", res.Content)
		}
	})

	t.Run("sorts results", func(t *testing.T) {
		res, _ := (&FindFiles{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "*.tsx",
		}))
		lines := strings.Split(res.Content, "\n")
		for i := 1; i < len(lines); i++ {
			if lines[i-1] > lines[i] {
				t.Fatalf("results not sorted at line %d: %q > %q", i, lines[i-1], lines[i])
			}
		}
	})

	t.Run("truncation marker when over cap", func(t *testing.T) {
		for i := 0; i < maxFindMatches+5; i++ {
			seed(t, fs, "bulk/"+padInt(i)+".md", "x")
		}
		res, _ := (&FindFiles{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "*.md", "path": "bulk",
		}))
		if !strings.Contains(res.Content, "narrow the pattern") {
			t.Errorf("expected truncation marker, got:\n%s", res.Content)
		}
	})

	t.Run("is read-only", func(t *testing.T) {
		if !(&FindFiles{FS: fs}).ReadOnly() {
			t.Error("find_files must be read-only")
		}
	})
}

func padInt(n int) string {
	const pad = "00000"
	s := pad + itoa(n)
	return s[len(s)-5:]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
