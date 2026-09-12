#!/usr/bin/env bash
# ============================================================
# Talon — Launch Redroid Dev Container
#
# Runs the stock redroid image — the same one the product's session
# manager uses (manager.go) — and installs the APKs from host paths
# over ADB after boot. The old pre-baked image duplicated exactly this
# install step at build time; one path, maintained once, is the law.
#
# Usage: bash infra/redroid/launch-dev.sh
# ============================================================

set -euo pipefail

CONTAINER_NAME="talon-dev"
IMAGE_NAME="redroid/redroid:14.0.0_64only-latest"
DEVICE_PIN="1234"
ADB_PORT=6555
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "=== Talon: Launch Redroid Dev Container ==="

# Step 1: Stop existing containers
echo ""
echo "--- Cleaning up existing containers ---"
docker ps -a | grep talon | awk '{print $1}' | xargs -r docker rm -f 2>/dev/null || true

# Step 2: Launch container (stock image, pulled on first run)
echo "--- Launching Redroid container ---"
docker run -d \
  --name "$CONTAINER_NAME" \
  --privileged \
  -p "$ADB_PORT:6555" \
  "$IMAGE_NAME" \
  androidboot.use_memfd=1 \
  androidboot.redroid_width=1080 \
  androidboot.redroid_height=2400 \
  androidboot.redroid_dpi=420

echo "Waiting for Android boot (30s)..."
sleep 30

# Step 3: Enable ADB TCP mode
echo "--- Enabling ADB TCP mode ---"
docker exec "$CONTAINER_NAME" setprop service.adb.tcp.port "$ADB_PORT" 2>/dev/null || true
sleep 2
docker exec "$CONTAINER_NAME" stop adbd 2>/dev/null || true
sleep 2
docker exec "$CONTAINER_NAME" start adbd 2>/dev/null || true
sleep 5

# Step 4: Connect ADB
echo "--- Connecting ADB ---"
adb connect "localhost:$ADB_PORT"
sleep 2

# Step 6: Verify connection
if ! adb devices | grep -q "localhost:${ADB_PORT}.*device"; then
    echo "ADB connection failed. Check container logs."
    exit 1
fi
echo "ADB connected!"

# Step 5: Install APKs from host paths
echo "--- Installing pre-baked APKs ---"

install_apk() {
    local src="$1"
    local name=$(basename "$src")
    if [ ! -f "$src" ]; then
        echo "  ✗ $name not found at $src"
        return 1
    fi
    echo "  Installing $name..."
    OUTPUT=$(adb -s "localhost:$ADB_PORT" install -r "$src" 2>&1)
    if echo "$OUTPUT" | grep -q "Success"; then
        echo "  ✓ $name installed"
    else
        echo "  ✗ $name failed: $OUTPUT"
        return 1
    fi
}

install_apk "$REPO_ROOT/backend/template/mock-wallet/v1.0.0/demowallet-debug.apk"
install_apk "$REPO_ROOT/backend/template/mock-wallet/v1.0.0/demodapp-debug.apk"
install_apk "$REPO_ROOT/backend/template/dev-client/v1.0.0/app-debug.apk"

# Step 6: Set device PIN
echo "--- Setting device PIN ---"
adb -s "localhost:$ADB_PORT" shell "locksettings set-pin $DEVICE_PIN" 2>/dev/null || echo "  PIN may already be set"

# Step 7: Keep display awake
echo "--- Keeping display awake ---"
adb -s "localhost:$ADB_PORT" shell "settings put system screen_off_timeout 2147483647" 2>&1
sleep 1
adb -s "localhost:$ADB_PORT" shell "input keyevent KEYCODE_WAKEUP" 2>&1
sleep 1
adb -s "localhost:$ADB_PORT" shell "input keyevent 82" 2>&1

# Step 8: Verify
echo ""
echo "--- Verifying installations ---"
PACKAGES=$(adb -s "localhost:$ADB_PORT" shell "pm list packages" 2>/dev/null | grep -iE "fake|talon" || true)
if [ -n "$PACKAGES" ]; then
    echo "$PACKAGES" | sed 's/^/  /'
else
    echo "  No packages found — APK install may have failed"
fi

echo ""
echo "=== Container Ready ==="
echo "Container:   $CONTAINER_NAME"
echo "Image:       $IMAGE_NAME (stock; APKs installed from host over ADB)"
echo "ADB:         adb -s localhost:$ADB_PORT shell"
echo "Resolution:  1080x2400 @ 420 DPI"
echo "Device PIN:  $DEVICE_PIN"
