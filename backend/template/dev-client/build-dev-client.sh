#!/bin/bash
set -euo pipefail

# build-dev-client.sh — Builds one dev-client APK per template version.
# This script runs ONCE offline, not per-project or per-session.
# Output: template/dev-client/v{version}/app-debug.apk

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE_DIR="$(dirname "$SCRIPT_DIR")"
VERSION_FILE="$TEMPLATE_DIR/.template-version"

if [ ! -f "$VERSION_FILE" ]; then
    echo "ERROR: .template-version not found at $VERSION_FILE"
    exit 1
fi

VERSION=$(cat "$VERSION_FILE" | tr -d '[:space:]')
echo "Building dev-client for template version: $VERSION"

OUTPUT_DIR="$TEMPLATE_DIR/dev-client/v$VERSION"
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

# Step 1: expo prebuild
echo ""
echo "=== Step 1: expo prebuild ==="
cd "$TEMPLATE_DIR"
npx expo prebuild -p android --no-install

# The 2-ABI law: redroid is x86_64, the Seeker phone arm64-v8a. The RN
# gradle plugin reads reactNativeArchitectures and overrides any
# defaultConfig.ndk.abiFilters — this property is the operative knob.
# Prebuild may have regenerated gradle.properties from its own template;
# pin it AFTER prebuild, before gradle.
echo ""
echo "=== Pinning reactNativeArchitectures=x86_64,arm64-v8a ==="
sed -i 's/^reactNativeArchitectures=.*/reactNativeArchitectures=x86_64,arm64-v8a/' "$TEMPLATE_DIR/android/gradle.properties"
grep -q '^reactNativeArchitectures=x86_64,arm64-v8a$' "$TEMPLATE_DIR/android/gradle.properties" \
  || echo 'reactNativeArchitectures=x86_64,arm64-v8a' >> "$TEMPLATE_DIR/android/gradle.properties"

# Step 2: gradle assembleDebug
echo ""
echo "=== Step 2: gradle assembleDebug ==="
cd "$TEMPLATE_DIR/android"
chmod +x gradlew
./gradlew assembleDebug

# Step 3: copy APK to output
echo ""
echo "=== Step 3: copy APK ==="
APK_PATH="$TEMPLATE_DIR/android/app/build/outputs/apk/debug/app-debug.apk"
if [ ! -f "$APK_PATH" ]; then
    echo "ERROR: APK not found at $APK_PATH"
    exit 1
fi

cp "$APK_PATH" "$OUTPUT_DIR/app-debug.apk"
echo "Dev-client APK copied to: $OUTPUT_DIR/app-debug.apk"

# Step 4: generate native-modules.json
echo ""
echo "=== Step 4: generate allowlist ==="
GRADLE_FILE="$TEMPLATE_DIR/android/app/build.gradle"
if [ -f "$GRADLE_FILE" ]; then
    # Simple extraction of native module references
    # This creates a basic allowlist — enhance with actual scanning in production
    cat > "$OUTPUT_DIR/native-modules.json" << 'ALLOWSLIST'
{
  "templateVersion": "PLACEHOLDER_VERSION",
  "sdkVersion": "55.0.0",
  "builtAt": "PLACEHOLDER_DATE",
  "modules": [
    {
      "name": "react-native-quick-crypto",
      "version": "1.0.6",
      "nativeLinking": true,
      "androidPackage": "com.nicktdev.quickcrypto"
    },
    {
      "name": "react-native-gesture-handler",
      "version": "2.30.0",
      "nativeLinking": true,
      "androidPackage": "com.swmansion.gesturehandler"
    },
    {
      "name": "react-native-reanimated",
      "version": "4.2.1",
      "nativeLinking": true,
      "androidPackage": "com.swmansion.reanimated"
    },
    {
      "name": "react-native-screens",
      "version": "4.23.0",
      "nativeLinking": true,
      "androidPackage": "com.swmansion.screens"
    },
    {
      "name": "react-native-safe-area-context",
      "version": "5.6.2",
      "nativeLinking": true,
      "androidPackage": "com.th3rdwave.safeareacontext"
    },
    {
      "name": "react-native-svg",
      "version": "15.15.3",
      "nativeLinking": true,
      "androidPackage": "com.horcrux.svg"
    },
    {
      "name": "react-native-webview",
      "version": "13.16.0",
      "nativeLinking": true,
      "androidPackage": "com.reactnative.webview"
    },
    {
      "name": "react-native-worklets",
      "version": "0.7.4",
      "nativeLinking": true,
      "androidPackage": "com.swmansion.worklets"
    },
    {
      "name": "@react-native-async-storage/async-storage",
      "version": "2.2.0",
      "nativeLinking": true,
      "androidPackage": "com.reactnativecommunity.asyncstorage"
    },
    {
      "name": "expo-dev-client",
      "version": "55.0.18",
      "nativeLinking": true,
      "androidPackage": "expo.modules.devclient"
    },
    {
      "name": "expo-constants",
      "version": "55.0.9",
      "nativeLinking": true,
      "androidPackage": "expo.modules.constants"
    },
    {
      "name": "expo-font",
      "version": "55.0.4",
      "nativeLinking": true,
      "androidPackage": "expo.modules.font"
    },
    {
      "name": "expo-haptics",
      "version": "55.0.9",
      "nativeLinking": true,
      "androidPackage": "expo.modules.haptics"
    },
    {
      "name": "expo-image",
      "version": "55.0.6",
      "nativeLinking": true,
      "androidPackage": "expo.modules.image"
    },
    {
      "name": "expo-linear-gradient",
      "version": "55.0.9",
      "nativeLinking": true,
      "androidPackage": "expo.modules.lineargradient"
    },
    {
      "name": "expo-linking",
      "version": "55.0.8",
      "nativeLinking": true,
      "androidPackage": "expo.modules.linking"
    },
    {
      "name": "expo-router",
      "version": "55.0.7",
      "nativeLinking": true,
      "androidPackage": "expo.modules.router"
    },
    {
      "name": "expo-splash-screen",
      "version": "55.0.12",
      "nativeLinking": true,
      "androidPackage": "expo.modules.splashscreen"
    },
    {
      "name": "expo-status-bar",
      "version": "55.0.4",
      "nativeLinking": true,
      "androidPackage": "expo.modules.statusbar"
    },
    {
      "name": "expo-symbols",
      "version": "55.0.5",
      "nativeLinking": true,
      "androidPackage": "expo.modules.symbols"
    },
    {
      "name": "expo-system-ui",
      "version": "55.0.10",
      "nativeLinking": true,
      "androidPackage": "expo.modules.systemui"
    },
    {
      "name": "expo-web-browser",
      "version": "55.0.10",
      "nativeLinking": true,
      "androidPackage": "expo.modules.webbrowser"
    },
    {
      "name": "expo-blur",
      "version": "55.0.10",
      "nativeLinking": true,
      "androidPackage": "expo.modules.blur"
    },
    {
      "name": "expo-font",
      "version": "55.0.4",
      "nativeLinking": true,
      "androidPackage": "expo.modules.font"
    }
  ]
}
ALLOWSLIST

    # Replace placeholders
    sed -i "s/PLACEHOLDER_VERSION/$VERSION/g" "$OUTPUT_DIR/native-modules.json"
    sed -i "s/PLACEHOLDER_DATE/$(date -u +%Y-%m-%dT%H:%M:%SZ)/g" "$OUTPUT_DIR/native-modules.json"

    echo "Allowlist generated at: $OUTPUT_DIR/native-modules.json"
fi

# Cleanup android build artifacts (not needed after APK is copied)
echo ""
echo "=== Cleanup ==="
rm -rf "$TEMPLATE_DIR/android"

echo ""
echo "=== Done ==="
echo "Dev-client built successfully:"
echo "  APK:    $OUTPUT_DIR/app-debug.apk"
echo "  Allowlist: $OUTPUT_DIR/native-modules.json"
