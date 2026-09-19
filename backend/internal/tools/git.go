package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ── git_status ────────────────────────────────────────

type GitStatus struct{ WorkspacePath string }

func (t *GitStatus) Name() string { return "git_status" }
func (t *GitStatus) Description() string {
	return "Show the workspace git status — staged, unstaged, and untracked files. Use before committing to see what changed."
}
func (t *GitStatus) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}
func (t *GitStatus) ReadOnly() bool { return true }
func (t *GitStatus) Execute(ctx context.Context, _ json.RawMessage) (ToolResult, error) {
	out, err := gitRun(ctx, t.WorkspacePath, "status", "--porcelain=v1", "--branch")
	if err != nil && isNotRepo(out) {
		return ToolResult{Content: "not a git repository — workspace has no git baseline"}, nil
	}
	if err != nil {
		return errResult("git status failed: %v (%s)", err, strings.TrimSpace(out)), nil
	}
	if strings.TrimSpace(out) == "" {
		return ToolResult{Content: "clean — no changes"}, nil
	}
	return ToolResult{Content: strings.TrimSpace(out)}, nil
}

// ── git_diff ──────────────────────────────────────────

type GitDiff struct{ WorkspacePath string }

func (t *GitDiff) Name() string { return "git_diff" }
func (t *GitDiff) Description() string {
	return "Show the unified diff against HEAD for the whole workspace. Use to see exactly what the build changed before committing or pushing. Returns empty when there is nothing to diff."
}
func (t *GitDiff) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"staged":{"type":"boolean","description":"Diff staged changes only (--cached) when true"}},"required":[]}`)
}
func (t *GitDiff) ReadOnly() bool { return true }
func (t *GitDiff) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Staged bool `json:"staged"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	cmdArgs := []string{"diff", "HEAD", "--"}
	if a.Staged {
		cmdArgs = []string{"diff", "--cached", "--"}
	}
	out, err := gitRun(ctx, t.WorkspacePath, cmdArgs...)
	if err != nil && isNotRepo(out) {
		return ToolResult{Content: "not a git repository"}, nil
	}
	if err != nil {
		return errResult("git diff failed: %v (%s)", err, strings.TrimSpace(out)), nil
	}
	if strings.TrimSpace(out) == "" {
		return ToolResult{Content: "no diff — working tree matches HEAD"}, nil
	}
	// Cap: full diff of a large template copy could be huge
	if len(out) > 48*1024 {
		out = out[:48*1024] + fmt.Sprintf("\n… [%d bytes truncated]", len(out)-48*1024)
	}
	return ToolResult{Content: out}, nil
}

// ── git_log ───────────────────────────────────────────

type GitLog struct{ WorkspacePath string }

func (t *GitLog) Name() string { return "git_log" }
func (t *GitLog) Description() string {
	return "Show recent commits (oneline, decorated). Use to see history before branching or to verify a commit landed."
}
func (t *GitLog) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","description":"Number of commits to show (default 12, max 50)"}},"required":[]}`)
}
func (t *GitLog) ReadOnly() bool { return true }
func (t *GitLog) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Count int `json:"count"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	n := a.Count
	if n <= 0 {
		n = 12
	}
	if n > 50 {
		n = 50
	}
	out, err := gitRun(ctx, t.WorkspacePath, "log", "--oneline", "--decorate", fmt.Sprintf("-n%d", n))
	if err != nil && isNotRepo(out) {
		return ToolResult{Content: "not a git repository"}, nil
	}
	if err != nil {
		return errResult("git log failed: %v (%s)", err, strings.TrimSpace(out)), nil
	}
	if strings.TrimSpace(out) == "" {
		return ToolResult{Content: "no commits yet"}, nil
	}
	return ToolResult{Content: strings.TrimSpace(out)}, nil
}

// ── git_commit ────────────────────────────────────────

type GitCommit struct{ WorkspacePath string }

func (t *GitCommit) Name() string { return "git_commit" }
func (t *GitCommit) Description() string {
	return "Stage all changes and commit with a message. Use after a verified unit of work — one commit per feature/fix, not per file."
}
func (t *GitCommit) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","description":"Commit message — imperative, under 72 chars first line"}},"required":["message"]}`)
}
func (t *GitCommit) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	msg := strings.TrimSpace(a.Message)
	if len(msg) < 10 {
		return errResult("commit message too short — describe what changed (≥10 chars)"), nil
	}
	if len(msg) > 500 {
		return errResult("commit message too long — keep it under 500 chars"), nil
	}
	// Stage
	if out, err := gitRun(ctx, t.WorkspacePath, "add", "-A"); err != nil {
		return errResult("git add failed: %v (%s)", err, strings.TrimSpace(out)), nil
	}
	// Check diff
	diffOut, _ := gitRun(ctx, t.WorkspacePath, "diff", "--cached", "--stat")
	if strings.TrimSpace(diffOut) == "" {
		return ToolResult{Content: "nothing to commit — working tree clean"}, nil
	}
	out, err := gitRun(ctx, t.WorkspacePath, "commit", "-m", msg)
	if err != nil {
		if strings.Contains(out, "nothing to commit") {
			return ToolResult{Content: "nothing to commit"}, nil
		}
		return errResult("git commit failed: %v (%s)", err, strings.TrimSpace(out)), nil
	}
	return ToolResult{Content: strings.TrimSpace(out) + "\n" + strings.TrimSpace(diffOut)}, nil
}

// ── git_branch ────────────────────────────────────────

type GitBranch struct{ WorkspacePath string }

func (t *GitBranch) Name() string { return "git_branch" }
func (t *GitBranch) Description() string {
	return "List local and remote branches, or create/switch a branch. Use to isolate a feature or to see what exists."
}
func (t *GitBranch) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"create":{"type":"string","description":"Branch name to create and switch to"},"checkout":{"type":"string","description":"Branch name to switch to"}},"required":[]}`)
}
func (t *GitBranch) ReadOnly() bool {
	// Listing is read-only; create/checkout mutates, but the tool itself is not marked
	// read-only so the orchestrator serializes it — which is correct for branch switches.
	return false
}
func (t *GitBranch) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Create   string `json:"create"`
		Checkout string `json:"checkout"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	if c := strings.TrimSpace(a.Create); c != "" {
		if err := validateBranchName(c); err != nil {
			return errResult("%v", err), nil
		}
		out, err := gitRun(ctx, t.WorkspacePath, "checkout", "-b", c)
		if err != nil {
			return errResult("git branch create failed: %v (%s)", err, strings.TrimSpace(out)), nil
		}
		return ToolResult{Content: strings.TrimSpace(out)}, nil
	}
	if c := strings.TrimSpace(a.Checkout); c != "" {
		if err := validateBranchName(c); err != nil {
			return errResult("%v", err), nil
		}
		out, err := gitRun(ctx, t.WorkspacePath, "checkout", c)
		if err != nil {
			return errResult("git checkout failed: %v (%s)", err, strings.TrimSpace(out)), nil
		}
		return ToolResult{Content: strings.TrimSpace(out)}, nil
	}
	out, err := gitRun(ctx, t.WorkspacePath, "branch", "-a", "--verbose")
	if err != nil && isNotRepo(out) {
		return ToolResult{Content: "not a git repository"}, nil
	}
	if err != nil {
		return errResult("git branch failed: %v (%s)", err, strings.TrimSpace(out)), nil
	}
	if strings.TrimSpace(out) == "" {
		return ToolResult{Content: "no branches yet"}, nil
	}
	return ToolResult{Content: strings.TrimSpace(out)}, nil
}

func validateBranchName(name string) error {
	if len(name) < 1 || len(name) > 80 {
		return fmt.Errorf("branch name must be 1-80 chars")
	}
	if strings.Contains(name, "..") || strings.Contains(name, " ") || strings.HasPrefix(name, "-") || strings.HasSuffix(name, ".lock") {
		return fmt.Errorf("invalid branch name %q", name)
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '/' || c == '.') {
			return fmt.Errorf("invalid character %q in branch name %q", string(c), name)
		}
	}
	return nil
}

func gitRun(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func isNotRepo(out string) bool {
	low := strings.ToLower(out)
	return strings.Contains(low, "not a git repository") || strings.Contains(low, "not git repository")
}
