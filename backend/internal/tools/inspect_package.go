package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── inspect_package ───────────────────────────────────
//
// A read-only tool that reads an installed package's TypeScript declarations
// straight from node_modules and returns its public type surface. The whole stack
// here (Expo, expo-router, the Solana Mobile libraries) is newer than any model's
// training data, so the single most common failure is a confidently-guessed API
// that does not exist. This lets the model check the real signatures instead.

type InspectPackage struct{ WorkspacePath string }

func (t *InspectPackage) Name() string { return "inspect_package" }

func (t *InspectPackage) Description() string {
	return "Read an installed package's TypeScript type declarations from node_modules and return its public " +
		"API surface — exported functions, types, and their signatures. Use this before calling into a library " +
		"whose exact API you are unsure of, rather than guessing: this stack is newer than your training data."
}

func (t *InspectPackage) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "Installed package name, e.g. expo-router or @solana-mobile/mobile-wallet-adapter-protocol-web3js"}
		},
		"required": ["name"]
	}`)
}

// maxTypeBytes bounds the declarations returned across the entry file and any
// barrel re-exports followed, keeping a large library's types under the tool
// output cap.
const maxTypeBytes = 16 * 1024

// maxBarrelFollow bounds how many local re-exported declaration files a barrel
// entry point is chased into, so a deeply split package cannot fan out unbounded.
const maxBarrelFollow = 8

func (t *InspectPackage) Execute(_ context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	pkg := packageRoot(strings.TrimSpace(a.Name))
	if pkg == "" {
		return errResult("provide a package name, e.g. expo-router"), nil
	}

	pkgDir := filepath.Join("node_modules", pkg)
	metaPath := filepath.Join(pkgDir, "package.json")
	meta, err := t.readInside(metaPath)
	if err != nil {
		return errResult("%s is not installed (no node_modules/%s). Install it with install_package first, "+
			"or check the spelling.", pkg, pkg), nil
	}

	var pj struct {
		Version string          `json:"version"`
		Types   string          `json:"types"`
		Typings string          `json:"typings"`
		Main    string          `json:"main"`
		Module  string          `json:"module"`
		Exports json.RawMessage `json:"exports"`
	}
	_ = json.Unmarshal([]byte(meta), &pj)

	// Locate the entry declaration file: an explicit types/typings field, then a
	// .d.ts reachable through the exports map, then the conventional index.d.ts.
	entry := pj.Types
	if entry == "" {
		entry = pj.Typings
	}
	if entry == "" && len(pj.Exports) > 0 {
		var ex any
		if json.Unmarshal(pj.Exports, &ex) == nil {
			entry = findTypesEntry(ex)
		}
	}
	if entry == "" {
		if _, e := t.readInside(filepath.Join(pkgDir, "index.d.ts")); e == nil {
			entry = "index.d.ts"
		}
	}

	header := pkg
	if pj.Version != "" {
		header += "@" + pj.Version
	}

	if entry == "" {
		main := pj.Main
		if main == "" {
			main = pj.Module
		}
		msg := fmt.Sprintf("%s ships no TypeScript declarations", header)
		if main != "" {
			msg += fmt.Sprintf(" (main entry: %s)", main)
		}
		msg += ".\nThere may be community types under @types/" + strings.TrimPrefix(pkg, "@") +
			"; otherwise treat its API as untyped and verify calls carefully."
		return ToolResult{Content: msg}, nil
	}

	entry = filepath.ToSlash(filepath.Clean(strings.TrimPrefix(entry, "./")))
	body := t.collectDeclarations(pkgDir, entry)
	if body == "" {
		return errResult("%s declares types at %s but it could not be read.", header, entry), nil
	}

	return ToolResult{Content: fmt.Sprintf("%s — types from %s\n\n%s", header, entry, body)}, nil
}

// collectDeclarations reads the entry .d.ts and, when it is a barrel that mostly
// re-exports sibling declaration files, follows those one level so the real
// signatures are included rather than a wall of `export * from`. Everything is
// bounded by maxTypeBytes and maxBarrelFollow.
func (t *InspectPackage) collectDeclarations(pkgDir, entry string) string {
	content, err := t.readInside(filepath.Join(pkgDir, entry))
	if err != nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(content)

	followed := 0
	for _, rel := range localReexports(content) {
		if followed >= maxBarrelFollow || b.Len() >= maxTypeBytes {
			break
		}
		target := t.resolveDTS(pkgDir, entry, rel)
		if target == "" {
			continue
		}
		sub, err := t.readInside(target)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "\n\n// ── %s ──\n%s", rel, sub)
		followed++
	}

	out := b.String()
	if len(out) > maxTypeBytes {
		out = out[:maxTypeBytes] + "\n\n[declarations truncated — read the .d.ts directly for the rest]"
	}
	return out
}

// readInside reads a file that must resolve within the workspace root. The path is
// sandbox-validated so a crafted package name cannot escape node_modules.
func (t *InspectPackage) readInside(rel string) (string, error) {
	abs, err := ValidateSandboxPath(t.WorkspacePath, rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// packageRoot reduces a possibly-subpath specifier to the package root directory:
// "@scope/name/sub" → "@scope/name", "name/sub" → "name". A traversal attempt
// returns "" so the caller rejects it before any path is built.
func packageRoot(name string) string {
	if name == "" || strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
		return ""
	}
	parts := strings.Split(name, "/")
	if strings.HasPrefix(name, "@") {
		if len(parts) < 2 {
			return ""
		}
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// findTypesEntry walks a package.json "exports" value and returns the first
// declaration file it can find, preferring one reached through a "types"
// condition. It handles the string, map, and nested-condition forms.
func findTypesEntry(v any) string {
	if fallback := searchTypes(v, false); fallback != "" {
		return fallback
	}
	return searchTypes(v, true)
}

// searchTypes recurses through an exports subtree. When preferAny is false it only
// accepts a .d.ts reached via a "types" key (the correct, highest-priority
// signal); when true it accepts any .d.ts string it finds.
func searchTypes(v any, preferAny bool) string {
	switch t := v.(type) {
	case string:
		if preferAny && strings.HasSuffix(t, ".d.ts") {
			return t
		}
	case map[string]any:
		// A "types"/"typings" condition is the authoritative pointer; take it first.
		for _, key := range []string{"types", "typings"} {
			if s, ok := t[key].(string); ok && strings.HasSuffix(s, ".d.ts") {
				return s
			}
		}
		// Prefer the "." (root) subtree, then any remaining conditions.
		order := make([]string, 0, len(t))
		if _, ok := t["."]; ok {
			order = append(order, ".")
		}
		for k := range t {
			if k != "." {
				order = append(order, k)
			}
		}
		for _, k := range order {
			if got := searchTypes(t[k], preferAny); got != "" {
				return got
			}
		}
	}
	return ""
}

// localReexports pulls the relative module specifiers a declaration file
// re-exports or imports (`export * from './x'`, `export { a } from './y'`,
// `import './z'`), which is what a barrel entry point is made of. External
// specifiers are ignored — only local siblings are followed.
func localReexports(src string) []string {
	seen := map[string]bool{}
	var out []string
	// fromRe/bareRe are compiled once in projectmap.go (same package); call their
	// methods directly rather than naming the regexp type, so this file needs no
	// regexp import of its own.
	collect := func(matches [][]string) {
		for _, m := range matches {
			s := strings.TrimSpace(m[1])
			if !strings.HasPrefix(s, ".") || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	collect(fromRe.FindAllStringSubmatch(src, -1))
	collect(bareRe.FindAllStringSubmatch(src, -1))
	return out
}

// resolveDTS turns a relative specifier from within entry into a workspace-relative
// declaration path (so readInside can consume it directly), trying the specifier
// itself, a .d.ts suffix, and an index.d.ts. It returns "" when none exists inside
// the workspace. pkgDir is workspace-relative (node_modules/<pkg>); entry is
// package-relative; rel is relative to entry's directory.
func (t *InspectPackage) resolveDTS(pkgDir, entry, rel string) string {
	base := filepath.Dir(entry)
	joined := filepath.ToSlash(filepath.Clean(filepath.Join(base, rel)))
	candidates := []string{joined}
	if !strings.HasSuffix(joined, ".d.ts") {
		candidates = append(candidates, joined+".d.ts", filepath.ToSlash(filepath.Join(joined, "index.d.ts")))
	}
	for _, c := range candidates {
		wsRel := filepath.ToSlash(filepath.Join(pkgDir, c))
		abs, err := ValidateSandboxPath(t.WorkspacePath, wsRel)
		if err != nil {
			continue
		}
		if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
			return wsRel
		}
	}
	return ""
}
