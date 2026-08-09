package devclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WalletManager handles mock wallet lifecycle: installation, configuration, verification.
type WalletManager struct {
	templateDir string
	version     string
}

// WalletManifest describes a mock wallet build.
type WalletManifest struct {
	TemplateVersion string `json:"templateVersion"`
	WalletType      string `json:"walletType"`
	SourceRepo      string `json:"sourceRepo"`
	BuiltAt         string `json:"builtAt"`
	APKFile         string `json:"apkFile"`
}

// NewWalletManager creates a wallet manager for the given template directory.
func NewWalletManager(templateDir string) (*WalletManager, error) {
	verFile := filepath.Join(templateDir, ".template-version")
	data, err := os.ReadFile(verFile)
	if err != nil {
		return nil, fmt.Errorf("read template version: %w", err)
	}
	version := strings.TrimRight(string(data), "\r\n\t ")

	return &WalletManager{
		templateDir: templateDir,
		version:     version,
	}, nil
}

// MockWalletAPKPath returns the path to the pre-built mock wallet APK.
func (wm *WalletManager) MockWalletAPKPath() string {
	return filepath.Join(wm.templateDir, "mock-wallet", "v"+wm.version, "fakewallet-debug.apk")
}

// FakeDappAPKPath returns the path to the pre-built fakedapp APK (verification baseline).
func (wm *WalletManager) FakeDappAPKPath() string {
	return filepath.Join(wm.templateDir, "mock-wallet", "v"+wm.version, "fakedapp-debug.apk")
}

// MockWalletManifestPath returns the path to the wallet manifest.
func (wm *WalletManager) MockWalletManifestPath() string {
	return filepath.Join(wm.templateDir, "mock-wallet", "v"+wm.version, "manifest.json")
}

// IsInstalled checks if the mock wallet APK exists.
func (wm *WalletManager) IsInstalled() bool {
	apkPath := wm.MockWalletAPKPath()
	_, err := os.Stat(apkPath)
	return err == nil
}

// HasFakeDapp checks if the fakedapp verification APK exists.
func (wm *WalletManager) HasFakeDapp() bool {
	apkPath := wm.FakeDappAPKPath()
	_, err := os.Stat(apkPath)
	return err == nil
}

// LoadManifest loads the wallet manifest.
func (wm *WalletManager) LoadManifest() (*WalletManifest, error) {
	manifestPath := wm.MockWalletManifestPath()
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}

	var manifest WalletManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// InstallWallet installs the mock wallet APK into a container via ADB.
func (wm *WalletManager) InstallWallet(ctx context.Context, adbTarget string) error {
	apkPath := wm.MockWalletAPKPath()

	// Check if APK exists
	if _, err := os.Stat(apkPath); os.IsNotExist(err) {
		return fmt.Errorf("mock wallet APK not found at %s — run build-mock-wallet.sh first", apkPath)
	}

	// Install via ADB
	cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "install", "-r", apkPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("adb install failed: %s — %w", string(output), err)
	}

	log.Printf("mock wallet installed on %s from %s", adbTarget, apkPath)
	return nil
}

// ConfigureDevice sets up device-level configuration required for MWA sessions.
// This includes setting a device PIN which is required for wallet signing operations.
func (wm *WalletManager) ConfigureDevice(ctx context.Context, adbTarget string) error {
	// Set device PIN (required for MWA signing)
	// The PIN "1234" is used for testing purposes
	pin := "1234"

	// Use locksettings to set PIN (requires root or shell permissions)
	cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"locksettings", "set-pin", pin)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Try alternative method via settings
		log.Printf("locksettings method failed, trying alternative: %v", err)

		// Some devices may need a different approach
		cmd = exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
			"am", "broadcast", "-a", "android.intent.action.SET_SCREEN_LOCK",
			"--ei", "lockscreen.password_type", "1",
			"--es", "lockscreen.password", pin)
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to set device PIN: %s — %w", string(output), err)
		}
	}

	log.Printf("device PIN configured on %s", adbTarget)
	return nil
}

// VerifyWallet checks if the mock wallet is properly installed and responds.
func (wm *WalletManager) VerifyWallet(ctx context.Context, adbTarget string) error {
	// Check if the wallet app is installed
	cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"pm", "list", "packages", "com.solana.mobile.walletadapter.fakewallet")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wallet verification failed: %w", err)
	}

	if len(output) == 0 {
		return fmt.Errorf("mock wallet package not found on device")
	}

	// Launch the wallet to verify it's functional
	cmd = exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"monkey", "-p", "com.solana.mobile.walletadapter.fakewallet",
		"-c", "android.intent.category.LAUNCHER", "1")
	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to launch mock wallet: %w", err)
	}

	log.Printf("mock wallet verified on %s", adbTarget)
	return nil
}

// InstallFakeDapp installs the fakedapp verification baseline APK.
func (wm *WalletManager) InstallFakeDapp(ctx context.Context, adbTarget string) error {
	apkPath := wm.FakeDappAPKPath()

	if _, err := os.Stat(apkPath); os.IsNotExist(err) {
		log.Printf("fakedapp APK not found at %s (non-fatal)", apkPath)
		return nil
	}

	cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "install", "-r", apkPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fakedapp install failed: %s — %w", string(output), err)
	}

	log.Printf("fakedapp installed on %s", adbTarget)
	return nil
}

// VerifyRoundTrip attempts to launch fakedapp and verify it can connect to fakewallet.
// This is a basic smoke test — a full round trip requires UI interaction.
func (wm *WalletManager) VerifyRoundTrip(ctx context.Context, adbTarget string) error {
	// Check if both apps are installed
	walletInstalled := false
	dappInstalled := false

	// Check wallet
	cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"pm", "list", "packages", "com.solana.mobile.walletadapter.fakewallet")
	if output, err := cmd.CombinedOutput(); err == nil && len(output) > 0 {
		walletInstalled = true
	}

	// Check dapp
	cmd = exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"pm", "list", "packages", "com.solana.mobile.walletadapter.fakedapp")
	if output, err := cmd.CombinedOutput(); err == nil && len(output) > 0 {
		dappInstalled = true
	}

	if !walletInstalled {
		return fmt.Errorf("fakewallet not installed — cannot verify round trip")
	}

	if !dappInstalled {
		log.Printf("fakedapp not installed — skipping round trip verification")
		return nil
	}

	// Launch fakedapp to verify it starts
	cmd = exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"monkey", "-p", "com.solana.mobile.walletadapter.fakedapp",
		"-c", "android.intent.category.LAUNCHER", "1")
	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to launch fakedapp: %w", err)
	}

	log.Printf("round trip verification: fakedapp launched on %s (manual approval required for full test)", adbTarget)
	return nil
}

// SetupSession performs the full wallet setup for a new session:
// 1. Install the mock wallet APK
// 2. Install fakedapp APK (verification baseline)
// 3. Configure device PIN
// 4. Verify wallet is functional
// Returns an error if any step fails (hard failure per §2.9).
func (wm *WalletManager) SetupSession(ctx context.Context, adbTarget string) error {
	log.Printf("setting up mock wallet for session (target: %s)", adbTarget)

	// Step 1: Install wallet
	if err := wm.InstallWallet(ctx, adbTarget); err != nil {
		return fmt.Errorf("wallet install failed: %w", err)
	}

	// Step 2: Install fakedapp (non-fatal if missing)
	if err := wm.InstallFakeDapp(ctx, adbTarget); err != nil {
		log.Printf("WARNING: fakedapp install failed: %v (continuing)", err)
	}

	// Step 3: Configure device PIN
	if err := wm.ConfigureDevice(ctx, adbTarget); err != nil {
		return fmt.Errorf("wallet configuration failed: %w", err)
	}

	// Step 4: Verify wallet
	if err := wm.VerifyWallet(ctx, adbTarget); err != nil {
		return fmt.Errorf("wallet verification failed: %w", err)
	}

	// Step 5: Verify round trip capability
	if err := wm.VerifyRoundTrip(ctx, adbTarget); err != nil {
		return fmt.Errorf("round trip verification failed: %w", err)
	}

	log.Printf("mock wallet setup complete for session (target: %s)", adbTarget)
	return nil
}
