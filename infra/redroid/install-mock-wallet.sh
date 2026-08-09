#!/usr/bin/env bash
# ============================================================
# SeekerStudio — Install Mock MWA Wallet on Redroid
#
# The Solana Mobile Mock Wallet lets generated apps test:
#   - Wallet connection (MWA protocol)
#   - Transaction signing
#   - Message signing
#   - All standard wallet operations
#
# Prerequisites:
#   - JDK 17+ (sudo apt install openjdk-17-jdk)
#   - Android SDK (set ANDROID_HOME)
#   - Redroid container running (launch-dev.sh)
#
# Usage: bash infra/redroid/install-mock-wallet.sh
# ============================================================

set -euo pipefail

REPO_URL="https://github.com/solana-mobile/mock-mwa-wallet.git"
CLONE_DIR="/tmp/mock-mwa-wallet"
ADB_TARGET="localhost:5555"

echo "=== SeekerStudio: Install Mock MWA Wallet ==="

# Step 1: Check prerequisites
echo ""
echo "--- Checking prerequisites ---"

if [ -z "${ANDROID_HOME:-}" ]; then
    echo "ANDROID_HOME not set. Install Android SDK and set ANDROID_HOME."
    echo "  Option A: Install Android Studio (includes SDK)"
    echo "  Option B: Download cmdline-tools from https://developer.android.com/studio#command-line-tools-only"
    exit 1
fi
echo "ANDROID_HOME: $ANDROID_HOME"

# Check Java
if ! command -v java &>/dev/null; then
    echo "Java not found. Install JDK 17+: sudo apt install openjdk-17-jdk"
    exit 1
fi
echo "Java: $(java -version 2>&1 | head -1)"

# Check ADB
if ! adb devices | grep -q "5555"; then
    echo "Redroid not connected on localhost:5555. Run launch-dev.sh first."
    echo "ADB output:"
    adb devices
    exit 1
fi
echo "Redroid connected"

# Step 2: Clone mock wallet repo
echo ""
echo "--- Cloning mock-mwa-wallet ---"
rm -rf "$CLONE_DIR"
git clone --depth 1 "$REPO_URL" "$CLONE_DIR"

# Step 3: Build APK
echo ""
echo "--- Building APK ---"
cd "$CLONE_DIR"
chmod +x gradlew
./gradlew assembleDebug

# Step 4: Find the built APK
APK_PATH=$(find "$CLONE_DIR" -name "*.apk" -path "*/debug/*" | head -1)
if [ -z "$APK_PATH" ]; then
    echo "APK not found after build."
    exit 1
fi
echo "APK found: $APK_PATH"

# Step 5: Install on Redroid
echo ""
echo "--- Installing on Redroid ---"
adb -s "$ADB_TARGET" install "$APK_PATH"

# Step 6: Verify installation
echo ""
echo "--- Verifying installation ---"
if adb -s "$ADB_TARGET" shell "pm list packages" | grep -q "mock"; then
    echo "Mock MWA Wallet installed!"
else
    echo "Package not found. Check install output."
fi

# Cleanup
echo ""
echo "--- Cleaning up ---"
rm -rf "$CLONE_DIR"

echo ""
echo "=== Mock Wallet Ready ==="
echo "Wallet: Solana Mobile Mock MWA Wallet"
echo "Package: com.solana.mobile.mockwallet (approximate)"
echo "Test: Open a generated app → Connect Wallet → should find Mock Wallet"
