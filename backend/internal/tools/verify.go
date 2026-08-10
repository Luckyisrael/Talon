package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type RunTypecheck struct {
	WorkspacePath string
}

func (t *RunTypecheck) Name() string { return "run_typecheck" }

func (t *RunTypecheck) Description() string {
	return "Run TypeScript type checking (tsc --noEmit). Returns type errors if any."
}

func (t *RunTypecheck) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
}

type TypecheckResult struct {
	Passed bool   `json:"passed"`
	Errors string `json:"errors,omitempty"`
}

func (t *RunTypecheck) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	cmd := exec.CommandContext(ctx, "npx", "tsc", "--noEmit")
	cmd.Dir = t.WorkspacePath

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	result := TypecheckResult{
		Passed: err == nil,
		Errors: outputStr,
	}

	data, _ := json.Marshal(result)

	if err != nil {
		return ToolResult{
			Content: string(data),
			IsError: true,
		}, err
	}

	return ToolResult{Content: string(data)}, nil
}

type RunLint struct {
	WorkspacePath string
}

func (t *RunLint) Name() string { return "run_lint" }

func (t *RunLint) Description() string {
	return "Run ESLint on the project. Returns lint errors if any."
}

func (t *RunLint) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
}

type LintResult struct {
	Passed bool   `json:"passed"`
	Errors string `json:"errors,omitempty"`
}

func (t *RunLint) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	tsFiles := []string{}
	err := filepath.Walk(t.WorkspacePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if name == "node_modules" || name == ".expo" || name == ".staging" || name == ".git" {
			return filepath.SkipDir
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
			tsFiles = append(tsFiles, path)
		}
		return nil
	})
	if err != nil || len(tsFiles) == 0 {
		return ToolResult{Content: `{"passed": true, "errors": ""}`}, nil
	}

	// ESLint 9+ uses flat config (eslint.config.js) — no --ext flag needed
	// If eslint.config.js exists, use flat config mode
	var cmd *exec.Cmd
	eslintConfig := filepath.Join(t.WorkspacePath, "eslint.config.js")
	if _, err := os.Stat(eslintConfig); err == nil {
		cmd = exec.CommandContext(ctx, "npx", "eslint", ".")
	} else {
		// Fallback for older ESLint configs
		cmd = exec.CommandContext(ctx, "npx", "eslint", "--ext", ".ts,.tsx,.js,.jsx", ".")
	}
	cmd.Dir = t.WorkspacePath

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	result := LintResult{
		Passed: err == nil,
		Errors: outputStr,
	}

	data, _ := json.Marshal(result)

	if err != nil {
		return ToolResult{
			Content: string(data),
			IsError: true,
		}, err
	}

	return ToolResult{Content: string(data)}, nil
}
