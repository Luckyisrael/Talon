package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// VisionVerdict is a model's read of a screenshot: whether it shows the screen
// the build was meant to render, and whether anything on it looks broken.
type VisionVerdict struct {
	Matches  bool   `json:"matches"`
	Reason   string `json:"reason"`
	HasError bool   `json:"has_error"`
	ErrorMsg string `json:"error_msg,omitempty"`
}

// VisionChecker analyzes a PNG screenshot against a plain-English description of
// the screen the build should be showing. *ai.Client satisfies this through a
// thin adapter in the api package; the tool holds the interface so it stays
// testable without a live model and so this low-level package keeps no
// dependency on the AI client.
type VisionChecker interface {
	VisionCheck(ctx context.Context, screenshot []byte, intendedScreen string) (*VisionVerdict, error)
}

type CaptureScreenshot struct {
	ADBPath   string
	ADBTarget string
	// Vision, when set, turns a raw screencap into a verdict: the model looks at
	// the pixels and reports whether they match what the build intended. Without
	// it the tool still captures, but it reports only that — it never claims a
	// verification it did not perform.
	Vision VisionChecker
	// capture grabs the raw PNG bytes from the device. Defaults to the adb
	// screencap below; the seam lets tests exercise the analysis path without a
	// device attached.
	capture func(ctx context.Context) ([]byte, error)
}

func (t *CaptureScreenshot) Name() string { return "capture_screenshot" }

func (t *CaptureScreenshot) Description() string {
	return "Capture a screenshot from the connected Android device and, given intended_screen, have a vision model verify it shows what the build intended."
}

func (t *CaptureScreenshot) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"intended_screen": {
				"type": "string",
				"description": "Plain-English description of what the screen should be showing right now, e.g. 'the wallet-connect screen with a Connect button'. When provided, the screenshot is analyzed against this and a match/error verdict is returned; omit it to only capture."
			}
		},
		"required": []
	}`)
}

// ScreenshotResult is deliberately explicit about how far the tool got: whether
// it captured, whether a model actually looked at the pixels (Analyzed), and
// only then the verdict. The old version returned a bare {"success":true} even
// though nothing had inspected the screen — the agent read that as "the screen
// is fine" and stopped. Analyzed==false means Matches/HasError carry no meaning.
type ScreenshotResult struct {
	Success  bool   `json:"success"`
	Bytes    int    `json:"bytes,omitempty"`
	Analyzed bool   `json:"analyzed"`
	Matches  bool   `json:"matches"`
	Reason   string `json:"reason,omitempty"`
	HasError bool   `json:"has_error"`
	Error    string `json:"error,omitempty"`
}

type CaptureScreenshotArgs struct {
	IntendedScreen string `json:"intended_screen"`
}

// screencap is the default capture: pull a PNG off the device over adb.
func (t *CaptureScreenshot) screencap(ctx context.Context) ([]byte, error) {
	adbPath := t.ADBPath
	if adbPath == "" {
		adbPath = "adb"
	}
	target := t.ADBTarget
	if target == "" {
		target = "localhost:5555"
	}
	cmd := exec.CommandContext(ctx, adbPath, "-s", target, "exec-out", "screencap", "-p")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("screenshot failed: %v, output: %s", err, string(out))
	}
	return out, nil
}

func (t *CaptureScreenshot) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	// Malformed args must not sink the turn — we can still capture the screen and
	// report it, just without a target to check against.
	var a CaptureScreenshotArgs
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}

	capture := t.capture
	if capture == nil {
		capture = t.screencap
	}

	output, err := capture(ctx)
	if err != nil {
		result := ScreenshotResult{Success: false, Error: err.Error()}
		data, _ := json.Marshal(result)
		return ToolResult{Content: string(data), IsError: true}, err
	}

	result := ScreenshotResult{Success: true, Bytes: len(output)}

	// No checker, or nothing to check against: report the capture and stop there.
	// Reporting a passed verification we never ran is exactly the lie that lets
	// the agent call a broken screen done.
	if t.Vision == nil || a.IntendedScreen == "" {
		if t.Vision == nil {
			result.Reason = "captured; no vision model configured, so the screen was not analyzed"
		} else {
			result.Reason = "captured; pass intended_screen to have the screen analyzed against what it should show"
		}
		data, _ := json.Marshal(result)
		return ToolResult{Content: string(data)}, nil
	}

	verdict, verr := t.Vision.VisionCheck(ctx, output, a.IntendedScreen)
	if verr != nil {
		// The capture worked; only the analysis failed. Say so and let the agent
		// decide whether to retry — a flaky vision call shouldn't read as a bad
		// screen, nor as a good one.
		result.Reason = fmt.Sprintf("captured, but vision analysis failed: %v", verr)
		data, _ := json.Marshal(result)
		return ToolResult{Content: string(data)}, nil
	}

	result.Analyzed = true
	result.Matches = verdict.Matches
	result.Reason = verdict.Reason
	result.HasError = verdict.HasError
	result.Error = verdict.ErrorMsg

	// A mismatch or a visible error is a real problem. Surfacing it as is_error
	// steers the loop into another iteration instead of letting it declare
	// victory on a screen the model just said is wrong.
	isErr := !verdict.Matches || verdict.HasError

	data, _ := json.Marshal(result)
	return ToolResult{Content: string(data), IsError: isErr}, nil
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
