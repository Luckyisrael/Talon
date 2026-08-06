#!/usr/bin/env bash
# ============================================================
# Phase 1 — Step 5: Boot Redroid at Seeker Resolution
# Run this inside SeekerLinux (after binder verified):
#   wsl -d SeekerLinux -e bash -c "bash < /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/boot-redroid.sh"
# ============================================================
set -euo pipefail

CONTAINER_NAME="redroid-seeker-base"
IMAGE="redroid/redroid:14.0.0_64only-latest"

echo "=== SeekerStudio Phase 1: Boot Redroid Container ==="
echo ""

# Pre-checks
echo "--- Pre-checks ---"
if ! grep -q binder /proc/filesystems; then
  echo "❌ binder not available in kernel. Run verify.sh first."
  exit 1
fi
echo "✅ binder available"

if ! docker info &>/dev/null; then
  echo "❌ Docker daemon not running. Start with: systemctl start docker"
  exit 1
fi
echo "✅ Docker accessible"
echo ""

# Stop existing container if running
if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
  echo "--- Removing existing container ---"
  docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
fi

# Pull latest image
echo "--- Pulling Redroid image ---"
docker pull "$IMAGE"

# Launch Redroid at Seeker resolution
echo ""
echo "--- Launching Redroid container ---"
echo "Resolution: 1080x2400 @ 420 DPI (Seeker aspect ratio)"
docker run -d \
  --name "$CONTAINER_NAME" \
  --privileged \
  --network host \
  "$IMAGE" \
  androidboot.redroid_width=1080 \
  androidboot.redroid_height=2400 \
  androidboot.redroid_dpi=420 \
  androidboot.redroid_fps=60

echo "Container started. Waiting 10s for boot..."
sleep 10

# Verify container is running
echo ""
echo "--- Container status ---"
docker ps --filter "name=$CONTAINER_NAME" --format "table {{.ID}}\t{{.Status}}\t{{.Ports}}"

# Connect ADB
echo ""
echo "--- Connecting ADB ---"
apt-get update -qq && apt-get install -y -qq android-tools-adb 2>/dev/null
adb connect localhost:5555
sleep 2
adb devices

# Verify resolution
echo ""
echo "--- Verify Seeker resolution ---"
adb -s localhost:5555 shell getprop ro.boot.redroid_width
adb -s localhost:5555 shell getprop ro.boot.redroid_height
adb -s localhost:5555 shell getprop ro.boot.redroid_dpi

# Take a screenshot
echo ""
echo "--- Taking screenshot ---"
adb -s localhost:5555 exec-out screencap -p > /tmp/seeker-screenshot.png
if [ -f /tmp/seeker-screenshot.png ]; then
  cp /tmp/seeker-screenshot.png /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/screenshot.png
  echo "✅ Screenshot saved to infra/phase1/screenshot.png"
else
  echo "⚠️  Screenshot failed"
fi

echo ""
echo "=== Redroid Boot Complete ==="
echo "Container: $CONTAINER_NAME"
echo "ADB: localhost:5555"
echo "Resolution: 1080x2400 @ 420 DPI"
echo ""
echo "You can now:"
echo "  - Connect: adb connect localhost:5555"
echo "  - Shell: adb shell"
echo "  - Screenshot: adb exec-out screencap -p > screen.png"
