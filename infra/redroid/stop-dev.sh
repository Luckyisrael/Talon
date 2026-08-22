#!/usr/bin/env bash
set -euo pipefail

echo "=== SeekerStudio: Stopping Redroid Dev Environment ==="

# 1. Disconnect ADB targets safely
adb disconnect localhost:5555 2>/dev/null || true
adb disconnect localhost:6555 2>/dev/null || true
adb disconnect emulator-5554 2>/dev/null || true

# 2. Kill the container
docker rm -f seeker-dev 2>/dev/null || true

# 3. Refresh ADB server state
adb kill-server
adb start-server

echo "Done! Cleaned up containers and ADB server resets."

