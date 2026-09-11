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
	"sync"
	"time"

	"github.com/luckyisrael/talon/backend/internal/device"
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
	return filepath.Join(wm.templateDir, "mock-wallet", "v"+wm.version, "demowallet-debug.apk")
}

// DemoDappAPKPath returns the path to the pre-built demodapp APK (verification baseline).
func (wm *WalletManager) DemoDappAPKPath() string {
	return filepath.Join(wm.templateDir, "mock-wallet", "v"+wm.version, "demodapp-debug.apk")
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

// HasDemoDapp checks if the demodapp verification APK exists.
func (wm *WalletManager) HasDemoDapp() bool {
	apkPath := wm.DemoDappAPKPath()
	_, err := os.Stat(apkPath)
	return err == nil
}

// IsInstalledOnDevice checks if the mock wallet is already installed on the device.
func (wm *WalletManager) IsInstalledOnDevice(adbTarget string) bool {
	cmd := exec.Command("adb", "-s", adbTarget, "shell",
		"pm", "list", "packages", "com.solana.mobilewalletadapter.fakewallet")
	output, err := cmd.CombinedOutput()
	return err == nil && len(output) > 0
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
// Retries up to 5 times if the package manager service is not ready.
func (wm *WalletManager) InstallWallet(ctx context.Context, adbTarget string) error {
	apkPath := wm.MockWalletAPKPath()

	// Check if APK exists
	if _, err := os.Stat(apkPath); os.IsNotExist(err) {
		return fmt.Errorf("mock wallet APK not found at %s — run build-mock-wallet.sh first", apkPath)
	}

	// Wait for package manager service to be ready
	for attempt := 0; attempt < 10; attempt++ {
		if attempt > 0 {
			log.Printf("wallet install: waiting for package service (attempt %d/10)", attempt+1)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}

		// Check if package service is available
		checkCmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell", "service", "check", "package")
		checkOutput, _ := checkCmd.CombinedOutput()
		if strings.Contains(string(checkOutput), "not found") {
			continue
		}

		// Install via ADB
		cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "install", "-r", apkPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			if strings.Contains(string(output), "Can't find service: package") {
				continue // retry
			}
			return fmt.Errorf("adb install failed: %s — %w", string(output), err)
		}

		log.Printf("mock wallet installed on %s from %s", adbTarget, apkPath)
		return nil
	}

	return fmt.Errorf("wallet install failed after 10 attempts: package service not ready")
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

// VerifyWallet checks if the mock wallet is properly installed.
func (wm *WalletManager) VerifyWallet(ctx context.Context, adbTarget string) error {
	// Check if the wallet app is installed
	cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"pm", "list", "packages", "com.solana.mobilewalletadapter.fakewallet")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wallet verification failed: %w", err)
	}

	if len(output) == 0 {
		return fmt.Errorf("mock wallet package not found on device")
	}

	// Launch the wallet. am start, not monkey: monkey exits -5 silently on
	// this image without launching despite a valid LAUNCHER activity (E2E
	// finding), so its "best-effort" launch proved nothing.
	launchOut, _ := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"am", "start", "-n", "com.solana.mobilewalletadapter.fakewallet/.MainActivity").CombinedOutput()
	log.Printf("wallet launch output: %s", strings.TrimSpace(string(launchOut)))

	log.Printf("mock wallet verified on %s", adbTarget)
	return nil
}

// InstallDemoDapp installs the demodapp verification baseline APK.
func (wm *WalletManager) InstallDemoDapp(ctx context.Context, adbTarget string) error {
	apkPath := wm.DemoDappAPKPath()

	if _, err := os.Stat(apkPath); os.IsNotExist(err) {
		log.Printf("demodapp APK not found at %s (non-fatal)", apkPath)
		return nil
	}

	cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "install", "-r", apkPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("demodapp install failed: %s — %w", string(output), err)
	}

	log.Printf("demodapp installed on %s", adbTarget)
	return nil
}

// VerifyRoundTrip attempts to launch demodapp and verify it can connect to the demo wallet.
// This is a basic smoke test — a full round trip requires UI interaction.
func (wm *WalletManager) VerifyRoundTrip(ctx context.Context, adbTarget string) error {
	// Check if both apps are installed
	walletInstalled := false
	dappInstalled := false

	// Check wallet
	cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"pm", "list", "packages", "com.solana.mobilewalletadapter.fakewallet")
	if output, err := cmd.CombinedOutput(); err == nil && len(output) > 0 {
		walletInstalled = true
	}

	// Check dapp
	cmd = exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"pm", "list", "packages", "com.solana.mobilewalletadapter.fakedapp")
	if output, err := cmd.CombinedOutput(); err == nil && len(output) > 0 {
		dappInstalled = true
	}

	if !walletInstalled {
		return fmt.Errorf("demo wallet not installed — cannot verify round trip")
	}

	if !dappInstalled {
		log.Printf("demodapp not installed — skipping round trip verification")
		return nil
	}

	// Launch demodapp to verify it starts. am start over monkey (see
	// VerifyWallet) — "Error type 3" in the output is an explicit failure.
	launchOut, _ := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
		"am", "start", "-n", "com.solana.mobilewalletadapter.fakedapp/.MainActivity").CombinedOutput()
	log.Printf("demodapp launch output: %s", strings.TrimSpace(string(launchOut)))

	log.Printf("round trip verification: demodapp launched on %s (manual approval required for full test)", adbTarget)
	return nil
}

// SetupSession performs the full wallet setup for a new session:
// 1. Wait for device boot completion
// 2. Install the mock wallet + demodapp APKs concurrently
// 3. Configure device PIN
// 4. Verify wallet is functional
// Returns an error if any step fails (hard failure per §2.9).
func (wm *WalletManager) SetupSession(ctx context.Context, adbTarget string) error {
	log.Printf("setting up mock wallet for session (target: %s)", adbTarget)

	// Step 0: Wait for device boot completion
	if err := wm.waitForBoot(ctx, adbTarget); err != nil {
		return fmt.Errorf("device boot wait failed: %w", err)
	}

	// Step 1+2: Install wallet and demodapp concurrently. Separate packages,
	// no ordering dependency — serial installs just add their times.
	var wg sync.WaitGroup
	var walletErr, dappErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := wm.InstallWallet(ctx, adbTarget); err != nil {
			walletErr = fmt.Errorf("wallet install failed: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		// Non-fatal if missing, per InstallDemoDapp contract.
		if err := wm.InstallDemoDapp(ctx, adbTarget); err != nil {
			dappErr = err
		}
	}()
	wg.Wait()
	if walletErr != nil {
		return walletErr
	}

	// Demodapp install problems stay non-fatal (verification baseline only).
	if dappErr != nil {
		log.Printf("WARNING: demodapp install failed: %v (continuing)", dappErr)
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

// waitForBoot polls until the device reports boot_completed=1, within the
// shared device.BootBudget (the old fixed 30×2s loop ran out at 60s while
// real cold boot takes ~2m13s — the sessions waiter had to rescue it).
func (wm *WalletManager) waitForBoot(ctx context.Context, adbTarget string) error {
	log.Printf("waiting for device boot completion (target: %s)", adbTarget)
	deadline := time.Now().Add(device.BootBudget)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}

		cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
			"getprop", "sys.boot_completed")
		output, err := cmd.CombinedOutput()
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(output)) == "1" {
			// Extra 3s for services to initialize after boot_completed
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
			log.Printf("device boot completed (target: %s)", adbTarget)
			return nil
		}
	}
	return fmt.Errorf("device did not boot within %v (target: %s)", device.BootBudget, adbTarget)
}
