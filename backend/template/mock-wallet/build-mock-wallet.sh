#!/bin/bash
set -euo pipefail

# build-mock-wallet.sh — Builds the Solana Mobile mock wallet + fakedapp.
# Source: github.com/solana-mobile/mobile-wallet-adapter (android/fakewallet, android/fakedapp)
# This script runs ONCE offline, not per-container.
# Output: template/mock-wallet/v{version}/

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE_DIR="$(dirname "$SCRIPT_DIR")"
VERSION_FILE="$TEMPLATE_DIR/.template-version"

if [ ! -f "$VERSION_FILE" ]; then
    echo "ERROR: .template-version not found at $VERSION_FILE"
    exit 1
fi

VERSION=$(cat "$VERSION_FILE" | tr -d '[:space:]')
echo "Building mock wallet for template version: $VERSION"

OUTPUT_DIR="$SCRIPT_DIR/v$VERSION"
mkdir -p "$OUTPUT_DIR"

# Find Java 17
if [ -z "${JAVA_HOME:-}" ]; then
    for candidate in /usr/lib/jvm/java-17-openjdk-amd64 /usr/lib/jvm/java-17-openjdk /usr/lib/jvm/java-17; do
        if [ -f "$candidate/bin/java" ]; then
            export JAVA_HOME="$candidate"
            break
        fi
    done
fi

if [ -z "${JAVA_HOME:-}" ]; then
    echo "ERROR: Java JDK 17 not found. Set JAVA_HOME or install JDK 17."
    exit 1
fi

# Find Android SDK
if [ -z "${ANDROID_HOME:-}" ]; then
    for candidate in "$HOME/Android/Sdk" /opt/android-sdk /usr/local/android-sdk "$HOME/android-sdk"; do
        if [ -f "$candidate/platform-tools/adb" ]; then
            export ANDROID_HOME="$candidate"
            break
        fi
    done
fi

if [ -z "${ANDROID_HOME:-}" ]; then
    echo "ERROR: Android SDK not found. Set ANDROID_HOME or install Android SDK."
    exit 1
fi

echo "JAVA_HOME=$JAVA_HOME"
echo "ANDROID_HOME=$ANDROID_HOME"

# Clone the mobile-wallet-adapter monorepo
BUILD_DIR="/tmp/mwa-build"
if [ -d "$BUILD_DIR" ]; then
    rm -rf "$BUILD_DIR"
fi

echo ""
echo "=== Step 1: Clone mobile-wallet-adapter monorepo ==="
git clone --depth 1 https://github.com/solana-mobile/mobile-wallet-adapter.git "$BUILD_DIR"

cd "$BUILD_DIR"

# Step 2: Build fakewallet APK
echo ""
echo "=== Step 2: Build fakewallet ==="
./gradlew :fakewallet:assembleDebug

# Step 3: Build fakedapp APK (verification baseline)
echo ""
echo "=== Step 3: Build fakedapp ==="
./gradlew :fakedapp:assembleDebug

# Step 4: Copy APKs to output
echo ""
echo "=== Step 4: Copy APKs ==="

FAKEWALLET_APK="$BUILD_DIR/android/fakewallet/build/outputs/apk/debug/fakewallet-debug.apk"
if [ ! -f "$FAKEWALLET_APK" ]; then
    # Try alternative path
    FAKEWALLET_APK=$(find "$BUILD_DIR/android/fakewallet" -name "*.apk" -type f | head -1)
fi

if [ -z "$FAKEWALLET_APK" ] || [ ! -f "$FAKEWALLET_APK" ]; then
    echo "ERROR: fakewallet APK not found"
    exit 1
fi

cp "$FAKEWALLET_APK" "$OUTPUT_DIR/fakewallet-debug.apk"
echo "fakewallet APK copied to: $OUTPUT_DIR/fakewallet-debug.apk"

FAKEDAPP_APK="$BUILD_DIR/android/fakedapp/build/outputs/apk/debug/fakedapp-debug.apk"
if [ ! -f "$FAKEDAPP_APK" ]; then
    # Try alternative path
    FAKEDAPP_APK=$(find "$BUILD_DIR/android/fakedapp" -name "*.apk" -type f | head -1)
fi

if [ -n "$FAKEDAPP_APK" ] && [ -f "$FAKEDAPP_APK" ]; then
    cp "$FAKEDAPP_APK" "$OUTPUT_DIR/fakedapp-debug.apk"
    echo "fakedapp APK copied to: $OUTPUT_DIR/fakedapp-debug.apk"
else
    echo "WARNING: fakedapp APK not found (non-fatal)"
fi

# Step 5: Record version info
echo ""
echo "=== Step 5: Record version ==="
cat > "$OUTPUT_DIR/manifest.json" << EOF
{
  "templateVersion": "$VERSION",
  "walletType": "fakewallet",
  "sourceRepo": "https://github.com/solana-mobile/mobile-wallet-adapter",
  "sourceModule": "android/fakewallet",
  "verificationDapp": "android/fakedapp",
  "builtAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "apkFile": "fakewallet-debug.apk",
  "verificationApk": "fakedapp-debug.apk"
}
EOF

# Cleanup
echo ""
echo "=== Cleanup ==="
rm -rf "$BUILD_DIR"

echo ""
echo "=== Done ==="
echo "Mock wallet built successfully:"
echo "  fakewallet: $OUTPUT_DIR/fakewallet-debug.apk"
echo "  fakedapp:   $OUTPUT_DIR/fakedapp-debug.apk (verification baseline)"
echo "  Manifest:   $OUTPUT_DIR/manifest.json"
