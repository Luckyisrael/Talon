package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/seekersstudio/backend/internal/identity"
)

// protectedFiles are template-managed configuration files. Overwriting
// package.json loses 40+ pinned dependencies and breaks every downstream step,
// so writes to them are refused with an explanation the agent can act on.
var protectedFiles = map[string]bool{
	"package.json":     true,
	"tsconfig.json":    true,
	"app.json":         true,
	"metro.config.js":  true,
	"index.js":         true,
	"eslint.config.js": true,
	".gitignore":       true,
	"expo-env.d.ts":    true,
}

// isProtected matches on the full relative path, not the basename: a component
// legitimately named index.js deep in the tree is not the entrypoint.
func isProtected(rel string) bool {
	clean := filepath.ToSlash(filepath.Clean(rel))
	return protectedFiles[clean]
}

func protectedMessage(rel string) string {
	return fmt.Sprintf(
		"BLOCKED: %s is a platform file managed by the template. It pins the dependency set, "+
			"TypeScript config and Expo config that the build depends on.\n\n"+
			"To add a dependency use install_package. To change app config, tell the user what "+
			"needs changing. Write application code under app/, components/, lib/ or hooks/.", rel)
}

// ── write_file ────────────────────────────────────────

type WriteFile struct{ FS *FS }

func (t *WriteFile) Name() string { return "write_file" }

func (t *WriteFile) Description() string {
	return "Create a new file, or completely replace an existing one, with the content you supply. " +
		"Use this for new files. To change part of an existing file, prefer edit_file — it is cheaper " +
		"and cannot accidentally drop code you did not intend to remove."
}

func (t *WriteFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path relative to the project root, e.g. app/(tabs)/index.tsx"},
			"content": {"type": "string", "description": "The complete file content"}
		},
		"required": ["path", "content"]
	}`)
}

func (t *WriteFile) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if a.Path == "" {
		return errResult("path is required"), nil
	}
	if isProtected(a.Path) {
		return errResult("%s", protectedMessage(a.Path)), nil
	}
	if err := identity.BlockIdentityFields(a.Path, a.Content); err != nil {
		return errResult("%s", err.Error()), nil
	}

	existed := t.FS.Exists(a.Path)
	if err := t.FS.Write(a.Path, a.Content); err != nil {
		return errResult("could not write %s: %v", a.Path, err), nil
	}

	verb := "Created"
	if existed {
		verb = "Replaced"
	}
	lines := strings.Count(a.Content, "\n") + 1
	return ToolResult{Content: fmt.Sprintf("%s %s (%d lines)", verb, a.Path, lines)}, nil
}

// ── edit_file ─────────────────────────────────────────

type EditFile struct{ FS *FS }

func (t *EditFile) Name() string { return "edit_file" }

func (t *EditFile) Description() string {
	return "Replace an exact string in a file with another. The preferred way to change existing code: " +
		"you send only the lines that change, so nothing else can be lost. old_string must appear exactly " +
		"once in the file (include surrounding lines to make it unique) unless replace_all is true."
}

func (t *EditFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path relative to the project root"},
			"old_string": {"type": "string", "description": "Exact text to find, including indentation"},
			"new_string": {"type": "string", "description": "Text to replace it with"},
			"replace_all": {"type": "boolean", "description": "Replace every occurrence instead of requiring exactly one"}
		},
		"required": ["path", "old_string", "new_string"]
	}`)
}

func (t *EditFile) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if isProtected(a.Path) {
		return errResult("%s", protectedMessage(a.Path)), nil
	}

	updated, msg, ok := applyEdit(t.FS, a.Path, a.OldString, a.NewString, a.ReplaceAll)
	if !ok {
		return errResult("%s", msg), nil
	}
	if err := identity.BlockIdentityFields(a.Path, updated); err != nil {
		return errResult("%s", err.Error()), nil
	}
	if err := t.FS.Write(a.Path, updated); err != nil {
		return errResult("could not write %s: %v", a.Path, err), nil
	}
	return ToolResult{Content: msg}, nil
}

// applyEdit performs one string replacement in memory and returns the result
// plus a message describing the outcome. Shared by edit_file and multi_edit.
func applyEdit(fs *FS, path, oldStr, newStr string, replaceAll bool) (string, string, bool) {
	content, truncated, err := fs.Read(path)
	if err != nil {
		return "", fmt.Sprintf("could not read %s: %v — use write_file to create it", path, err), false
	}
	if truncated {
		return "", fmt.Sprintf("%s is too large to edit safely; rewrite it with write_file", path), false
	}
	if oldStr == "" {
		return "", "old_string cannot be empty; use write_file to create a file", false
	}
	if oldStr == newStr {
		return "", "old_string and new_string are identical — nothing to do", false
	}

	count := strings.Count(content, oldStr)
	switch {
	case count == 0:
		hint := ""
		if trimmed := strings.TrimSpace(oldStr); trimmed != "" && strings.Contains(content, trimmed) {
			hint = " The text is present but the surrounding whitespace differs — read the file and copy the indentation exactly."
		}
		return "", fmt.Sprintf("old_string not found in %s.%s", path, hint), false
	case count > 1 && !replaceAll:
		return "", fmt.Sprintf(
			"old_string appears %d times in %s. Include more surrounding lines to identify a single "+
				"occurrence, or set replace_all to change all of them.", count, path), false
	}

	if replaceAll {
		return strings.ReplaceAll(content, oldStr, newStr),
			fmt.Sprintf("Edited %s (%d occurrences replaced)", path, count), true
	}
	return strings.Replace(content, oldStr, newStr, 1),
		fmt.Sprintf("Edited %s", path), true
}

// ── multi_edit ────────────────────────────────────────

type MultiEdit struct{ FS *FS }

func (t *MultiEdit) Name() string { return "multi_edit" }

func (t *MultiEdit) Description() string {
	return "Apply several edits to one file in a single call. Edits are applied in order, each to the " +
		"result of the last. Either all succeed or the file is left untouched — use this when a change " +
		"spans multiple places in the same file."
}

func (t *MultiEdit) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path relative to the project root"},
			"edits": {
				"type": "array",
				"description": "Edits to apply in order",
				"items": {
					"type": "object",
					"properties": {
						"old_string": {"type": "string"},
						"new_string": {"type": "string"},
						"replace_all": {"type": "boolean"}
					},
					"required": ["old_string", "new_string"]
				}
			}
		},
		"required": ["path", "edits"]
	}`)
}

func (t *MultiEdit) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path  string `json:"path"`
		Edits []struct {
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if isProtected(a.Path) {
		return errResult("%s", protectedMessage(a.Path)), nil
	}
	if len(a.Edits) == 0 {
		return errResult("edits cannot be empty"), nil
	}

	content, truncated, err := t.FS.Read(a.Path)
	if err != nil {
		return errResult("could not read %s: %v — use write_file to create it", a.Path, err), nil
	}
	if truncated {
		return errResult("%s is too large to edit safely; rewrite it with write_file", a.Path), nil
	}

	// Apply everything in memory first so a failure on edit 3 of 4 does not
	// leave the file half-changed.
	for i, e := range a.Edits {
		count := strings.Count(content, e.OldString)
		if e.OldString == "" {
			return errResult("edit %d: old_string cannot be empty", i+1), nil
		}
		if count == 0 {
			return errResult("edit %d: old_string not found in %s. No edits were applied.", i+1, a.Path), nil
		}
		if count > 1 && !e.ReplaceAll {
			return errResult(
				"edit %d: old_string appears %d times in %s. Add surrounding context or set "+
					"replace_all. No edits were applied.", i+1, count, a.Path), nil
		}
		if e.ReplaceAll {
			content = strings.ReplaceAll(content, e.OldString, e.NewString)
		} else {
			content = strings.Replace(content, e.OldString, e.NewString, 1)
		}
	}

	if err := identity.BlockIdentityFields(a.Path, content); err != nil {
		return errResult("%s", err.Error()), nil
	}
	if err := t.FS.Write(a.Path, content); err != nil {
		return errResult("could not write %s: %v", a.Path, err), nil
	}
	return ToolResult{Content: fmt.Sprintf("Edited %s (%d edits applied)", a.Path, len(a.Edits))}, nil
}

// ── delete_file ───────────────────────────────────────

type DeleteFile struct{ FS *FS }

func (t *DeleteFile) Name() string { return "delete_file" }

func (t *DeleteFile) Description() string {
	return "Delete a file from the project."
}

func (t *DeleteFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path relative to the project root"}
		},
		"required": ["path"]
	}`)
}

func (t *DeleteFile) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if isProtected(a.Path) {
		return errResult("%s", protectedMessage(a.Path)), nil
	}
	if !t.FS.Exists(a.Path) {
		return ToolResult{Content: fmt.Sprintf("%s does not exist — nothing to delete", a.Path)}, nil
	}
	if err := t.FS.Delete(a.Path); err != nil {
		return errResult("could not delete %s: %v", a.Path, err), nil
	}
	return ToolResult{Content: fmt.Sprintf("Deleted %s", a.Path)}, nil
}

func errResult(format string, args ...any) ToolResult {
	return ToolResult{Content: fmt.Sprintf(format, args...), IsError: true}
}
