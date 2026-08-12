package api

import "testing"

func TestResolveInsideRejectsEscapes(t *testing.T) {
	root := "/workspaces/34"

	// The old check was strings.HasPrefix(abs, root), so a sibling directory whose
	// name merely started with the root's name passed containment.
	escapes := []string{
		"../35/app.tsx",
		"../../etc/passwd",
		"..",
		"app/../../35/secret.ts",
		"/etc/passwd",
		"/workspaces/34-other/app.tsx",
		"node_modules/react/index.js",
		".git/config",
		"app/\x00.tsx",
	}
	for _, rel := range escapes {
		if got, err := resolveInside(root, rel); err == nil {
			t.Errorf("resolveInside(%q, %q) = %q, want an error", root, rel, got)
		}
	}
}

func TestResolveInsideAllowsProjectPaths(t *testing.T) {
	root := "/workspaces/34"

	ok := map[string]string{
		"app/index.tsx":            "/workspaces/34/app/index.tsx",
		"./app/index.tsx":          "/workspaces/34/app/index.tsx",
		"app/(tabs)/profile.tsx":   "/workspaces/34/app/(tabs)/profile.tsx",
		"components/ui/Button.tsx": "/workspaces/34/components/ui/Button.tsx",
		"package.json":             "/workspaces/34/package.json",
		"app/nested/../index.tsx":  "/workspaces/34/app/index.tsx",
	}
	for rel, want := range ok {
		got, err := resolveInside(root, rel)
		if err != nil {
			t.Errorf("resolveInside(%q, %q) errored: %v", root, rel, err)
			continue
		}
		if got != want {
			t.Errorf("resolveInside(%q, %q) = %q, want %q", root, rel, got, want)
		}
	}
}
