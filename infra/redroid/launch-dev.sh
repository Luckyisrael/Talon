#!/usr/bin/env bash
# ============================================================
# SeekerStudio — Launch Redroid Dev Container
#
# Redroid is a pure Android device — runs APKs, streams via
# screenshot-based streaming (v1). No Solana tooling inside.
# Host handles all build/compile work.
#
# Usage: bash infra/redroid/launch-dev.sh
# ============================================================

set -euo pipefail

CONTAINER_NAME="redroid-seeker-dev"
IMAGE_NAME="redroid/redroid:14.0.0_64only-latest"
DEVICE_PIN="1234"
ADB_PORT=5555
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== SeekerStudio: Launch Redroid Dev Container ==="

# Step 1: Stop existing containers (clean up orphans)
echo ""
echo "--- Cleaning up existing containers ---"
docker ps -a | grep redroid | awk '{print $1}' | xargs -r docker rm -f 2>/dev/null || true

# Step 2: Launch container with memfd (no ashmem needed)
echo "--- Launching Redroid container (memfd mode) ---"
docker run -d \
  --name "$CONTAINER_NAME" \
  --privileged \
  -p "$ADB_PORT:5555" \
  "$IMAGE_NAME" \
  androidboot.use_memfd=1 \
  androidboot.redroid_width=1080 \
  androidboot.redroid_height=2400 \
  androidboot.redroid_dpi=420

echo "Container started. Waiting for Android boot (30s)..."
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

# Step 5: Verify connection
if adb devices | grep -q "localhost:${ADB_PORT}.*device"; then
    echo "ADB connected!"
else
    echo "ADB connection failed. Check container logs."
    exit 1
fi

# Step 6: Set device PIN (required for MWA)
echo "--- Setting device PIN ---"
adb -s "localhost:$ADB_PORT" push "$SCRIPT_DIR/entrypoint.sh" /data/local/tmp/device-setup.sh 2>&1
adb -s "localhost:$ADB_PORT" shell "sh /data/local/tmp/device-setup.sh" 2>&1

# Step 7: Keep display awake (infinite timeout + wake screen)
echo "--- Keeping display awake ---"
adb -s "localhost:$ADB_PORT" shell "settings put system screen_off_timeout 2147483647" 2>&1
sleep 1
adb -s "localhost:$ADB_PORT" shell "input keyevent KEYCODE_WAKEUP" 2>&1
sleep 1
adb -s "localhost:$ADB_PORT" shell "input keyevent 82" 2>&1
sleep 2

echo ""
echo "=== Container Ready ==="
echo "Container:   $CONTAINER_NAME"
echo "ADB:         adb -s localhost:$ADB_PORT shell"
echo "Resolution:  1080x2400 @ 420 DPI"
echo "Device PIN:  $DEVICE_PIN"
