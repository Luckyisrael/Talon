#!/bin/bash
set -euo pipefail

# build-mock-wallet.sh — Builds the Solana Mobile mock wallet APK.
# This script runs ONCE offline, not per-container.
# Output: template/mock-wallet/v{version}/fakewallet-debug.apk

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

# Clone the mock wallet if not already present
BUILD_DIR="/tmp/mock-wallet-build"
if [ -d "$BUILD_DIR" ]; then
    rm -rf "$BUILD_DIR"
fi

echo ""
echo "=== Step 1: Clone fakewallet ==="
git clone --depth 1 https://github.com/solana-mobile/fakewallet.git "$BUILD_DIR"

cd "$BUILD_DIR"

# Step 2: Build the APK
echo ""
echo "=== Step 2: Build APK ==="
./gradlew assembleDebug

# Step 3: Copy APK to output
echo ""
echo "=== Step 3: Copy APK ==="
APK_PATH="$BUILD_DIR/app/build/outputs/apk/debug/app-debug.apk"
if [ ! -f "$APK_PATH" ]; then
    echo "ERROR: APK not found at $APK_PATH"
    exit 1
fi

cp "$APK_PATH" "$OUTPUT_DIR/fakewallet-debug.apk"
echo "Mock wallet APK copied to: $OUTPUT_DIR/fakewallet-debug.apk"

# Step 4: Record version info
echo ""
echo "=== Step 4: Record version ==="
cat > "$OUTPUT_DIR/manifest.json" << EOF
{
  "templateVersion": "$VERSION",
  "walletType": "fakewallet",
  "sourceRepo": "https://github.com/solana-mobile/fakewallet",
  "builtAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "apkFile": "fakewallet-debug.apk"
}
EOF

# Cleanup
echo ""
echo "=== Cleanup ==="
rm -rf "$BUILD_DIR"

echo ""
echo "=== Done ==="
echo "Mock wallet built successfully:"
echo "  APK:      $OUTPUT_DIR/fakewallet-debug.apk"
echo "  Manifest: $OUTPUT_DIR/manifest.json"
