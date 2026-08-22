package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// TestWalletRoundtrip tests the MWA wallet round-trip on a device.
// It launches fakedapp, initiates an MWA connection, and checks logcat for success.
type TestWalletRoundtrip struct {
	ADBTarget string // optional — if empty, auto-detects from connected devices
}

func (t *TestWalletRoundtrip) Name() string { return "test_wallet_roundtrip" }

func (t *TestWalletRoundtrip) Description() string {
	return "Test the MWA wallet round-trip on a connected Android device. Launches fakedapp, initiates connection to fakewallet, and verifies the MWA protocol works. Use this after generating wallet code to verify the wallet integration is functional."
}

func (t *TestWalletRoundtrip) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"timeout_seconds": {
				"type": "integer",
				"description": "Timeout in seconds for the test (default: 30)",
				"default": 30
			}
		},
		"required": []
	}`)
}

type WalletTestArgs struct {
	TimeoutSeconds int `json:"timeout_seconds"`
}

type WalletTestResult struct {
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
	Logs    string `json:"logs,omitempty"`
}

func (t *TestWalletRoundtrip) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a WalletTestArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = 30
	}

	// Auto-detect ADB target if not provided
	adbTarget := t.ADBTarget
	if adbTarget == "" {
		adbTarget = t.detectADBTarget()
		if adbTarget == "" {
			return ToolResult{
				Content: mustJSON(WalletTestResult{
					Passed:  false,
					Message: "no Android device found — ensure a device is connected via ADB",
				}),
				IsError: true,
			}, nil
		}
	}
	t.ADBTarget = adbTarget

	timeout := time.Duration(a.TimeoutSeconds) * time.Second
	_ = timeout // used for context deadline if needed

	// Step 1: Check if both apps are installed
	walletInstalled := t.isPackageInstalled("com.solana.mobilewalletadapter.fakewallet")
	dappInstalled := t.isPackageInstalled("com.solana.mobilewalletadapter.fakedapp")

	if !walletInstalled {
		return ToolResult{
			Content: mustJSON(WalletTestResult{
				Passed:  false,
				Message: "fakewallet not installed on device — cannot test round-trip",
			}),
			IsError: true,
		}, nil
	}

	if !dappInstalled {
		return ToolResult{
			Content: mustJSON(WalletTestResult{
				Passed:  false,
				Message: "fakedapp not installed on device — cannot test round-trip",
			}),
			IsError: true,
		}, nil
	}

	// Step 2: Clear logcat to get clean logs
	t.adb("logcat", "-c")
	time.Sleep(500 * time.Millisecond)

	// Step 3: Force-stop any running fakedapp/fakewallet instances
	t.adb("shell", "am", "force-stop", "com.solana.mobilewalletadapter.fakedapp")
	t.adb("shell", "am", "force-stop", "com.solana.mobilewalletadapter.fakewallet")
	time.Sleep(500 * time.Millisecond)

	// Step 4: Launch fakedapp
	output, err := t.adb("shell", "am", "start", "-n",
		"com.solana.mobilewalletadapter.fakedapp/.MainActivity")
	if err != nil {
		return ToolResult{
			Content: mustJSON(WalletTestResult{
				Passed:  false,
				Message: fmt.Sprintf("failed to launch fakedapp: %v — %s", err, output),
			}),
			IsError: true,
		}, nil
	}
	time.Sleep(3 * time.Second)

	// Step 5: Tap "Connect" button using UI Automator to find it dynamically
	// instead of hardcoding screen coordinates that break across device sizes.
	tapResult := t.findAndTapConnectButton()
	if tapResult != "" {
		// Fallback: try common coordinates if UI Automator failed
		t.adb("shell", "input", "tap", "540", "800")
	}
	time.Sleep(2 * time.Second)

	// Step 6: Wait for fakewallet to appear and auto-approve
	// The mock wallet auto-approves all requests
	time.Sleep(3 * time.Second)

	// Step 7: Check logcat for MWA connection evidence
	logOutput, _ := t.adb("logcat", "-d", "-s",
		"MWAProtocol:*", "MobileWalletAdapter:*", "SolanaMobileWalletAdapter:*")

	success := false
	var messages []string

	// Check for connection-related logs
	connectionKeywords := []string{
		"authorize",
		"connected",
		"session",
		"association",
		"handshake",
		"sign",
		"signTransaction",
		"signMessage",
	}

	for _, keyword := range connectionKeywords {
		if strings.Contains(strings.ToLower(logOutput), strings.ToLower(keyword)) {
			success = true
			messages = append(messages, fmt.Sprintf("found '%s' in logs", keyword))
		}
	}

	// Also check if fakedapp is still running (indicates successful connection)
	psOutput, _ := t.adb("shell", "ps", "-A")
	if strings.Contains(psOutput, "fakedapp") {
		messages = append(messages, "fakedapp is running")
	}

	result := WalletTestResult{
		Passed:  success,
		Message: strings.Join(messages, "; "),
		Logs:    logOutput,
	}

	if !success {
		result.Message = "no MWA connection evidence found in logs — connection may not have been initiated"
	}

	return ToolResult{Content: mustJSON(result)}, nil
}

func (t *TestWalletRoundtrip) isPackageInstalled(packageName string) bool {
	output, err := t.adb("shell", "pm", "list", "packages", packageName)
	return err == nil && strings.Contains(output, packageName)
}

func (t *TestWalletRoundtrip) detectADBTarget() string {
	// Run adb devices to find connected devices
	output, err := exec.Command("adb", "devices").CombinedOutput()
	if err != nil {
		return ""
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "device") && strings.Contains(line, ":") {
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				return parts[0]
			}
		}
	}
	return ""
}

func (t *TestWalletRoundtrip) adb(args ...string) (string, error) {
	allArgs := append([]string{"-s", t.ADBTarget}, args...)
	cmd := exec.Command("adb", allArgs...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// findAndTapConnectButton uses UI Automator to find and tap the "Connect" button
// dynamically instead of relying on hardcoded screen coordinates.
func (t *TestWalletRoundtrip) findAndTapConnectButton() string {
	// Dump the UI hierarchy
	dump, err := t.adb("shell", "uiautomator", "dump", "/dev/tty")
	if err != nil {
		return ""
	}

	// Look for nodes with text containing "connect" (case-insensitive)
	lower := strings.ToLower(dump)
	idx := strings.Index(lower, "connect")
	if idx == -1 {
		return "no connect button found"
	}

	// Extract bounds from the node containing "connect"
	// Format: bounds="[x1,y1][x2,y2]"
	boundsStart := strings.LastIndex(dump[:idx+200], "bounds=\"")
	if boundsStart == -1 {
		return "no bounds found"
	}
	boundsEnd := strings.Index(dump[boundsStart:], "\"")
	if boundsEnd == -1 {
		return "no bounds end found"
	}
	boundsStr := dump[boundsStart+8 : boundsStart+8+boundsEnd]

	// Parse [x1,y1][x2,y2]
	var x1, y1, x2, y2 int
	fmt.Sscanf(boundsStr, "[%d,%d][%d,%d]", &x1, &y1, &x2, &y2)
	if x2 <= x1 || y2 <= y1 {
		return "invalid bounds"
	}

	// Tap the center
	cx, cy := (x1+x2)/2, (y1+y2)/2
	t.adb("shell", "input", "tap", fmt.Sprintf("%d", cx), fmt.Sprintf("%d", cy))
	return ""
}

func mustJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}
