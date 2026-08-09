package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type CaptureScreenshot struct {
	ADBPath   string
	ADBTarget string
}

func (t *CaptureScreenshot) Name() string { return "capture_screenshot" }

func (t *CaptureScreenshot) Description() string {
	return "Capture a screenshot from the connected Android device for visual verification."
}

func (t *CaptureScreenshot) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
}

type ScreenshotResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func (t *CaptureScreenshot) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	adbPath := t.ADBPath
	if adbPath == "" {
		adbPath = "adb"
	}

	target := t.ADBTarget
	if target == "" {
		target = "localhost:5555"
	}

	cmd := exec.CommandContext(ctx, adbPath, "-s", target, "exec-out", "screencap", "-p")
	output, err := cmd.CombinedOutput()

	if err != nil {
		result := ScreenshotResult{
			Success: false,
			Error:   fmt.Sprintf("screenshot failed: %v, output: %s", err, string(output)),
		}
		data, _ := json.Marshal(result)
		return ToolResult{Content: string(data), IsError: true}, err
	}

	result := ScreenshotResult{Success: true}
	data, _ := json.Marshal(result)
	return ToolResult{Content: string(data)}, nil
}

type TailLogcat struct {
	ADBPath   string
	ADBTarget string
}

func (t *TailLogcat) Name() string { return "tail_logcat" }

func (t *TailLogcat) Description() string {
	return "Tail the Android logcat for React Native errors and crashes."
}

func (t *TailLogcat) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"lines": {
				"type": "integer",
				"description": "Number of recent lines to retrieve (default: 50)"
			}
		},
		"required": []
	}`)
}

type TailLogcatArgs struct {
	Lines int `json:"lines"`
}

type LogcatEntry struct {
	Level string `json:"level"`
	Tag   string `json:"tag"`
	Msg   string `json:"msg"`
}

func (t *TailLogcat) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a TailLogcatArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	if a.Lines == 0 {
		a.Lines = 50
	}

	adbPath := t.ADBPath
	if adbPath == "" {
		adbPath = "adb"
	}

	target := t.ADBTarget
	if target == "" {
		target = "localhost:5555"
	}

	cmd := exec.CommandContext(ctx, adbPath, "-s", target, "logcat", "-d", "-t", fmt.Sprintf("%d", a.Lines), "ReactNativeJS:E", "AndroidRuntime:E")
	output, err := cmd.CombinedOutput()

	if err != nil {
		return ToolResult{
			Content: fmt.Sprintf("logcat failed: %v", err),
			IsError: true,
		}, err
	}

	lines := strings.Split(string(output), "\n")
	var entries []LogcatEntry
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entries = append(entries, LogcatEntry{
			Level: "E",
			Tag:   "ReactNativeJS",
			Msg:   line,
		})
	}

	data, _ := json.MarshalIndent(entries, "", "  ")
	return ToolResult{Content: string(data)}, nil
}
