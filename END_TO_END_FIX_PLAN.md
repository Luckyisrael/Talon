# End-to-End Fix & Test Plan

## Executive Summary

Four critical issues prevent SeekerStudio from being production-ready. This plan fixes each one and validates the full user journey: register → create project → type prompt → AI generates code → code compiles → live preview on device → wallet connects → transaction signs.

---

## Issue 1: Typecheck/Lint Errors During Build

### Root Cause Analysis

The AI agent **overwrites `package.json` and `tsconfig.json`** during code generation. Evidence from workspaces 17-30:

| Workspace | What AI Overwrote | Result |
|-----------|-------------------|--------|
| 17 | package.json → only zustand | "Cannot find module" for 11 packages |
| 19 | package.json → no react | "Cannot find module react" |
| 24 | tsconfig.json + package.json | Lost `jsx: "react-native"`, lost all deps |
| 25 | package.json → only typescript | "expo/tsconfig.base not found" |

The workspace template correctly copies these files to the project root (workspace.go `fallbackCopy`), but the AI's `write_file` tool overwrites them with minimal versions. The healing nudge then wastes rounds trying to fix cascading errors instead of restoring the original files.

### Fix Strategy: Protected Files + Recovery

#### Step 1: Add "protected files" guard to write_file tool

**File:** `backend/internal/tools/write_file.go`

Add a list of files that `WriteFileStaged` refuses to overwrite:

```go
var protectedFiles = map[string]bool{
    "package.json":    true,
    "tsconfig.json":   true,
    "app.json":        true,
    "babel.config.js": true,
    "metro.config.js": true,
    "index.js":        true,
}

func (t *WriteFile) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
    var input WriteFileInput
    if err := json.Unmarshal(args, &input); err != nil {
        return ToolResult{}, err
    }

    // Check protected files
    base := filepath.Base(input.Path)
    if protectedFiles[base] {
        return ToolResult{
            Content: fmt.Sprintf("BLOCKED: %s is a protected configuration file. Do not overwrite it. Use write_file for source code files only (e.g., src/**/*.tsx, app/**/*.tsx).", base),
            IsError: true,
        }, nil
    }

    // ... rest of existing logic
}
```

#### Step 2: Add explicit system prompt instruction

**File:** `backend/internal/api/prompts.go` (line 605, after the CRITICAL EXECUTION RULE)

Add to `systemPromptBuild`:

```
=== PROTECTED FILES (NEVER OVERWRITE) ===
The following files are auto-managed by the platform. NEVER call write_file on them:
- package.json (contains all 40+ dependencies — your code needs them)
- tsconfig.json (configured for Expo with path aliases)
- app.json (Expo configuration)
- babel.config.js, metro.config.js (build toolchain)
- index.js (app entry point)

If you need a package not in the template, use install_package instead of overwriting package.json.
```

#### Step 3: Improve healing nudge with specific error file extraction

**File:** `backend/internal/orchestrator/orchestrator.go` (lines 227-243)

Extract the filename from the tsc error and tell the AI exactly which file to fix:

```go
if tcErr != nil || typecheckResult != "" {
    sess.Emitter.EmitToolCall("run_typecheck", "Checking types…", "", "error")
    errorMsg := "TypeScript typecheck failed:\n" + typecheckResult
    if tcErr != nil {
        errorMsg = tcErr.Error()
    }

    // Extract the specific file from the first error
    targetFile := extractErrorFile(typecheckResult)
    healNudge := "STOP EXPLORING. Your code has type errors.\n\n" + errorMsg + "\n\n"
    if targetFile != "" {
        healNudge += "THE ERROR IS IN: " + targetFile + "\n"
        healNudge += "Fix ONLY this file by calling write_file. Do not touch any other files.\n"
    } else {
        healNudge += "Fix the errors by calling write_file on the affected files.\n"
    }
    healNudge += "CRITICAL: Do NOT overwrite package.json, tsconfig.json, or app.json. These are protected.\n"
    healNudge += "After fixing, call run_typecheck to verify."

    history = append(history, ai.PromptMessage{
        Role:    "user",
        Content: healNudge,
    })
    continue
}
```

Add helper:

```go
func extractErrorFile(tscOutput string) string {
    // Parse lines like: app/index.tsx(5,12): error TS2307: ...
    lines := strings.Split(tscOutput, "\n")
    for _, line := range lines {
        if idx := strings.Index(line, "("); idx > 0 {
            if idx2 := strings.Index(line[:idx], "/"); idx2 < 0 {
                // No path separator — this is a filename like "app/index.tsx"
                return line[:idx]
            }
        }
    }
    return ""
}
```

#### Step 4: Add lint config fallback

**File:** `backend/internal/tools/verify.go` (line 102)

The lint command uses `--ext` which is deprecated in ESLint 9+. Fix to use flat config:

```go
// If eslint.config.js exists, use flat config (ESLint 9+)
eslintConfig := filepath.Join(t.WorkspacePath, "eslint.config.js")
if _, err := os.Stat(eslintConfig); err == nil {
    cmd = exec.CommandContext(ctx, "npx", "eslint", ".")
} else {
    cmd = exec.CommandContext(ctx, "npx", "eslint", "--ext", ".ts,.tsx,.js,.jsx", ".")
}
```

### Verification

1. Create a fresh project, run build with prompt "Create a counter app"
2. Confirm AI does NOT overwrite package.json/tsconfig.json
3. Confirm typecheck passes after build
4. Confirm lint passes after build

---

## Issue 2: Wallet Round-Trip (Connect → Sign → Verify)

### What Exists

- `fakewallet-debug.apk` (13MB) — pre-built mock wallet that auto-approves signing
- `fakedapp-debug.apk` (11MB) — reference dapp for testing MWA
- `seeker-studio-example/` — full example app using `@wallet-ui/react-native-web3js`
- Template `package.json` already has `@solana-mobile/mobile-wallet-adapter-protocol-web3js`

### What's Missing

1. No automated test that actually initiates an MWA connection
2. No AI tool to verify wallet functionality
3. No generated app with MWA integration (the AI creates generic apps, not wallet apps)

### Fix Strategy: Test Harness + AI Wallet Tool

#### Step 1: Create an automated wallet round-trip test

**File:** `backend/internal/devclient/wallet_roundtrip_test.go`

Create a script that:
1. Installs fakedapp on the device
2. Launches fakedapp via ADB
3. Uses ADB shell commands to simulate taps (fakedapp has known button coordinates)
4. Checks logcat for MWA connection success
5. Verifies a signed transaction appears in the logs

The fakedapp has a "Connect" button at known coordinates. We can:
- `adb shell input tap <x> <y>` to press Connect
- `adb shell input tap <x> <y>` to approve in fakewallet
- `adb logcat -d | grep "MWA\|sign\|connect"` to verify

**Implementation approach:**

```go
func (wm *WalletManager) RunRoundTripTest(ctx context.Context, adbTarget string) error {
    // 1. Clear logcat
    exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell", "logcat", "-c").Run()

    // 2. Launch fakedapp
    exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
        "am", "start", "-n",
        "com.solana.mobilewalletadapter.fakedapp/.MainActivity").Run()
    time.Sleep(3 * time.Second)

    // 3. Tap "Connect" button (known coordinates from fakedapp layout)
    exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
        "input", "tap", "540", "1200").Run()
    time.Sleep(2 * time.Second)

    // 4. Check logcat for MWA connection attempt
    logOutput, _ := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell",
        "logcat", "-d", "-s", "MWAProtocol:F", "MobileWalletAdapter:F").CombinedOutput()

    if strings.Contains(string(logOutput), "connected") ||
       strings.Contains(string(logOutput), "authorize") {
        return nil // round trip succeeded
    }

    return fmt.Errorf("round trip test failed — logcat output:\n%s", logOutput)
}
```

#### Step 2: Add wallet test tool for AI

**File:** `backend/internal/tools/wallet_test.go`

Create a new tool `test_wallet_roundtrip` that the AI can call:

```go
type TestWalletRoundtrip struct {
    ADBTarget string
}

func (t *TestWalletRoundtrip) Name() string { return "test_wallet_roundtrip" }

func (t *TestWalletRoundtrip) Description() string {
    return "Test the MWA wallet round-trip: launches fakedapp, initiates connection, verifies signing works. Use this to verify wallet functionality after generating wallet code."
}

func (t *TestWalletRoundtrip) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
    // ... call WalletManager.RunRoundTripTest
}
```

#### Step 3: Update system prompt to teach MWA patterns

**File:** `backend/internal/api/prompts.go` (in systemPromptBuild)

Add a wallet integration guide:

```
=== MWA WALLET INTEGRATION GUIDE ===
When the user asks for a wallet/Solana app, use these patterns:

1. Import: `import { transact } from '@solana-mobile/mobile-wallet-adapter-protocol-web3js'`
2. Connect: `const session = await transact(async (wallet) => { return await wallet.authorize(...) })`
3. Sign: `const signed = await transact(async (wallet) => { return await wallet.signTransaction(tx) })`

The mock wallet on the device auto-approves all requests. After generating wallet code,
call test_wallet_roundtrip to verify the connection works.

DO NOT use @wallet-ui/* packages — use the raw @solana-mobile/* packages.
```

### Verification

1. Create a project with prompt "Create a Solana wallet app with connect and sign transaction"
2. Verify AI generates MWA code with correct imports
3. Verify AI calls `test_wallet_roundtrip` after generating code
4. Verify the round-trip test passes on the device

---

## Issue 3: Frontend IDE Not Tested

### What Exists

The frontend is a complete 3-panel IDE:
- **Left:** FileTree + AIChat (Plan/Build modes, streaming)
- **Center:** CodeEditor (Monaco) + Terminal (xterm.js)
- **Right:** PhonePreview (canvas + WebSocket video/touch)

### What Needs Testing

1. **Auth flow:** Register → Login → Token stored in localStorage
2. **Project flow:** Create project → Select template → IDE opens
3. **AI chat:** Type prompt → Streaming response → Tool calls visible
4. **Build flow:** AI writes files → FileTree updates → Code appears in editor
5. **Phone preview:** WebSocket connects → JPEG frames render → Touch input works
6. **Terminal:** WebSocket shell connects → Commands execute → Output visible

### Test Plan

#### Step 1: Start frontend dev server

```bash
cd frontend
npm run dev  # Starts on port 3000
```

#### Step 2: Manual E2E test sequence

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open http://localhost:3000 | Landing page loads with hero, features, CTA |
| 2 | Click "Get Started Free" | Navigates to /register |
| 3 | Enter email + password, submit | Account created, redirected to /dashboard |
| 4 | Click "New Project" | Modal with template selection |
| 5 | Select "Expo", enter name | Project created, IDE opens at /project/{id} |
| 6 | Type "Create a counter app" in AI chat | Streaming response begins, tool calls appear |
| 7 | Wait for build to complete | Files appear in FileTree, code shows in editor |
| 8 | Click "Start Session" | PhonePreview connects, shows Redroid screen |
| 9 | Touch the phone screen | ADB input sent to device |
| 10 | Open Terminal tab | WebSocket shell connects, commands work |

#### Step 3: Fix any connection issues

Common issues to watch for:
- CORS errors (backend needs `http://localhost:3000` in allowed origins)
- WebSocket auth (token passed as query param)
- Missing env vars (NEXT_PUBLIC_API_URL must be set)
- State synchronization (Zustand store not updating from SSE events)

#### Step 4: Automated E2E test (optional)

**File:** `frontend/e2e/ide-flow.spec.ts`

Using Playwright:

```typescript
test('full IDE flow', async ({ page }) => {
  await page.goto('http://localhost:3000');
  await page.click('text=Get Started Free');
  await page.fill('input[type="email"]', 'e2e@test.com');
  await page.fill('input[type="password"]', 'testpass123');
  await page.click('button[type="submit"]');
  // ... continue with project creation, build, preview
});
```

### Verification

- [ ] All 10 manual test steps pass
- [ ] No console errors in browser
- [ ] WebSocket connections establish successfully
- [ ] File tree updates in real-time as AI writes files
- [ ] Phone preview shows live device screen

---

## Issue 4: Dev-Client APK Install Failure

### Root Cause

The dev-client APK (271MB) fails to install because:
1. **Race condition:** Install runs before device finishes booting (same issue as wallet)
2. **Storage:** 271MB APK may exceed available storage in the container
3. **Service not ready:** `Can't find service: package` error

### Fix Strategy

#### Step 1: Add boot-wait to dev-client install (same pattern as wallet)

**File:** `backend/internal/devclient/manager.go` (line 72-89)

```go
func (m *Manager) InstallDevClient(ctx context.Context, adbTarget string) error {
    // Wait for boot
    if err := m.waitForBoot(ctx, adbTarget); err != nil {
        return fmt.Errorf("device boot wait failed: %w", err)
    }

    apkPath := m.DevClientAPKPath()
    if _, err := os.Stat(apkPath); os.IsNotExist(err) {
        return fmt.Errorf("dev-client APK not found at %s", apkPath)
    }

    // Retry install (same pattern as wallet)
    for attempt := 0; attempt < 5; attempt++ {
        if attempt > 0 {
            select {
            case <-ctx.Done():
                return ctx.Err()
            case <-time.After(5 * time.Second):
            }
        }

        cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "install", "-r", apkPath)
        output, err := cmd.CombinedOutput()
        if err != nil {
            if strings.Contains(string(output), "Can't find service: package") {
                continue
            }
            // If storage issue, try with -g (grant permissions) flag
            if strings.Contains(string(output), "INSTALL_FAILED_INSUFFICIENT_STORAGE") {
                return fmt.Errorf("insufficient storage for dev-client APK (271MB)")
            }
            return fmt.Errorf("adb install failed: %s — %w", string(output), err)
        }

        log.Printf("dev-client installed on %s from %s", adbTarget, apkPath)
        return nil
    }

    return fmt.Errorf("dev-client install failed after 5 attempts")
}
```

#### Step 2: Make dev-client install share the wallet's boot-wait

Move `waitForBoot` to a shared utility or have the session handler call it once before both wallet and dev-client setup:

**File:** `backend/internal/api/sessions.go` (lines 84-104)

```go
// Wait for device boot ONCE before all ADB operations
adbTarget := "localhost:" + strconv.Itoa(containerInfo.ADBPort)
if err := waitForDeviceBoot(adbTarget, 90*time.Second); err != nil {
    log.Printf("WARN: device boot wait failed: %v", err)
}

// Install dev-client (soft failure)
if h.devClient != nil {
    if err := h.devClient.InstallDevClient(context.Background(), adbTarget); err != nil {
        log.Printf("WARN: dev-client install failed for session %d: %v", session.ID, err)
    }
}

// Setup wallet (hard failure)
if h.wallet != nil && h.wallet.IsInstalled() {
    if err := h.wallet.SetupSession(context.Background(), adbTarget); err != nil {
        // ...
    }
}
```

### Verification

1. Start fresh Redroid container
2. Create session — dev-client installs successfully
3. Check `adb shell pm list packages | grep devclient` — package present
4. Dev-client launches without crash

---

## Issue 5: Pre-Bake Wallet + Dev-Client into Docker Image

### Why This Matters

PHASE_8 spec requirement 2.2 states: "Baked into container image, not installed post-boot."

Currently, both wallet (13MB) and dev-client (271MB) are installed via ADB at session creation. This causes:
- 5-10s added to session creation time
- Race conditions with device boot (the bugs we've been fixing)
- Retry logic complexity

Pre-baking eliminates all of these issues.

### Fix Strategy

#### Step 1: Create a custom Dockerfile

**File:** `infra/redroid/Dockerfile.seeker`

```dockerfile
FROM redroid/redroid:14.0.0_64only-latest

# Copy APKs into the image
COPY backend/template/mock-wallet/v1.0.0/fakewallet-debug.apk /tmp/fakewallet.apk
COPY backend/template/mock-wallet/v1.0.0/fakedapp-debug.apk /tmp/fakedapp.apk
COPY backend/template/dev-client/v1.0.0/app-debug.apk /tmp/devclient.apk

# Copy install script
COPY infra/redroid/install-apps.sh /tmp/install-apps.sh
RUN chmod +x /tmp/install-apps.sh

# The install script runs at container start (not build time)
# because ADB needs the device to be booted
ENTRYPOINT ["/tmp/install-apps.sh"]
```

#### Step 2: Create install script that runs at boot

**File:** `infra/redroid/install-apps.sh`

```bash
#!/bin/bash
# Start Redroid in background
/original-entrypoint &

# Wait for boot
echo "Waiting for device boot..."
while [ "$(getprop sys.boot_completed)" != "1" ]; do
    sleep 1
done
sleep 5  # Extra time for services

# Wait for package service
echo "Waiting for package service..."
while ! adb shell service check package 2>/dev/null | grep -q "not found"; do
    sleep 1
done

# Install apps
echo "Installing fakewallet..."
adb install -r /tmp/fakewallet.apk
echo "Installing fakedapp..."
adb install -r /tmp/fakedapp.apk
echo "Installing devclient..."
adb install -r /tmp/devclient.apk

# Set PIN
adb shell locksettings set-pin 1234

echo "All apps installed. Device ready."

# Keep container running
wait
```

#### Step 3: Update Go backend to detect pre-installed apps

**File:** `backend/internal/api/sessions.go` (lines 84-104)

```go
adbTarget := "localhost:" + strconv.Itoa(containerInfo.ADBPort)

// Check if wallet is already installed (pre-baked image)
walletAlreadyInstalled := h.wallet.IsInstalledOnDevice(adbTarget)

if walletAlreadyInstalled {
    log.Printf("wallet already installed on %s (pre-baked image)", adbTarget)
    // Skip install, just verify
    if err := h.wallet.VerifyWallet(context.Background(), adbTarget); err != nil {
        log.Printf("WARN: wallet verification failed: %v", err)
    }
} else {
    // Legacy path: install at session creation
    if h.wallet != nil && h.wallet.IsInstalled() {
        if err := h.wallet.SetupSession(context.Background(), adbTarget); err != nil {
            // ...
        }
    }
}
```

Add helper:

```go
func (wm *WalletManager) IsInstalledOnDevice(adbTarget string) bool {
    cmd := exec.Command("adb", "-s", adbTarget, "shell",
        "pm", "list", "packages", "com.solana.mobilewalletadapter.fakewallet")
    output, err := cmd.CombinedOutput()
    return err == nil && len(output) > 0
}
```

#### Step 4: Update docker-compose to use custom image

**File:** `docker-compose.yml` (or wherever Redroid is configured)

```yaml
services:
  redroid:
    build:
      context: .
      dockerfile: infra/redroid/Dockerfile.seeker
    ports:
      - "5555:5555"
    privileged: true
```

### Verification

1. Build custom image: `docker build -f infra/redroid/Dockerfile.seeker -t seeker-redroid .`
2. Start container from custom image
3. Verify apps are pre-installed: `adb shell pm list packages | grep -E "fake|devclient"`
4. Create session — should be instant (no install step)
5. Session creation log should show "wallet already installed (pre-baked image)"

---

## Execution Order

| Phase | Task | Priority | Est. Time |
|-------|------|----------|-----------|
| **1** | Protected files in write_file | HIGH | 30 min |
| **2** | System prompt update (no overwrite + MWA guide) | HIGH | 15 min |
| **3** | Healing nudge with file extraction | HIGH | 30 min |
| **4** | Dev-client boot-wait fix (quick fix for now) | HIGH | 20 min |
| **5** | Lint config fix (ESLint 9+) | MEDIUM | 10 min |
| **6** | Build test: "Create counter app" → typecheck passes | HIGH | 15 min |
| **7** | Frontend E2E manual test | HIGH | 30 min |
| **8** | Pre-bake Docker image (wallet + dev-client) | HIGH | 45 min |
| **9** | Wallet round-trip test tool | MEDIUM | 45 min |
| **10** | Wallet round-trip test: "Create wallet app" → sign works | MEDIUM | 30 min |
| **11** | Full regression: clean build → all stages pass | HIGH | 30 min |

**Total estimated time: ~5 hours**

---

## Success Criteria

| Gate | Requirement | How to Verify |
|------|-------------|---------------|
| G1 | Generated code compiles | `npx tsc --noEmit` returns 0 |
| G2 | Generated code lints clean | `npx eslint .` returns 0 |
| G3 | AI never overwrites protected files | Check build output for "BLOCKED" messages |
| G4 | Frontend IDE loads and connects | Login → Create project → IDE opens |
| G5 | AI chat streams correctly | Type prompt → see tool calls → files appear |
| G6 | Phone preview shows device | WebSocket connects → JPEG frames render |
| G7 | Pre-baked image has all apps | `adb shell pm list packages` shows fakewallet + fakedapp + devclient |
| G8 | Session creation is instant | No ADB install step, log shows "pre-baked image" |
| G9 | Wallet round-trip works | `test_wallet_roundtrip` returns success |
| G10 | Full E2E: prompt → working app on device | User can interact with generated app on Redroid |
| G11 | Two consecutive clean builds | No failures in 2 sequential build tests |
