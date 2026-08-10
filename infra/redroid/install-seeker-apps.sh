#!/bin/bash
# ============================================================
# SeekerStudio Redroid — Boot-time App Installer
#
# This script:
# 1. Starts Redroid (Android) in the background
# 2. Waits for the device to fully boot
# 3. Installs pre-baked APKs (fakewallet, fakedapp, devclient)
# 4. Sets device PIN for MWA testing
# 5. Continues running as PID 1
# ============================================================

set -e

SEEKER_DIR="/opt/seeker"
LOG_PREFIX="[seeker-preboot]"

log() {
    echo "$LOG_PREFIX $*"
}

# Start Redroid's /init in the background
log "Starting Redroid..."
/original-entrypoint &
INIT_PID=$!

# Wait for device boot completion
log "Waiting for device boot..."
BOOT_TIMEOUT=120  # seconds
ELAPSED=0
while [ "$ELAPSED" -lt "$BOOT_TIMEOUT" ]; do
    BOOT_COMPLETED=$(adb shell getprop sys.boot_completed 2>/dev/null || echo "")
    if [ "$BOOT_COMPLETED" = "1" ]; then
        break
    fi
    sleep 2
    ELAPSED=$((ELAPSED + 2))
done

if [ "$ELAPSED" -ge "$BOOT_TIMEOUT" ]; then
    log "WARNING: Device boot timed out after ${BOOT_TIMEOUT}s"
else
    log "Device boot completed in ${ELAPSED}s"
fi

# Extra wait for services to initialize
log "Waiting for services to initialize..."
sleep 5

# Wait for package manager service
log "Waiting for package service..."
PM_TIMEOUT=30
PM_ELAPSED=0
while [ "$PM_ELAPSED" -lt "$PM_TIMEOUT" ]; do
    PM_CHECK=$(adb shell service check package 2>/dev/null || echo "not found")
    if echo "$PM_CHECK" | grep -q "not found"; then
        break
    fi
    sleep 2
    PM_ELAPSED=$((PM_ELAPSED + 2))
done

if [ "$PM_ELAPSED" -ge "$PM_TIMEOUT" ]; then
    log "WARNING: Package service not ready after ${PM_TIMEOUT}s"
else
    log "Package service ready"
fi

# Install pre-baked APKs
install_apk() {
    local apk_path="$1"
    local apk_name=$(basename "$apk_path")
    
    if [ ! -f "$apk_path" ]; then
        log "SKIP: $apk_name not found at $apk_path"
        return
    fi
    
    log "Installing $apk_name..."
    OUTPUT=$(adb install -r "$apk_path" 2>&1)
    if echo "$OUTPUT" | grep -q "Success"; then
        log "  ✓ $apk_name installed successfully"
    else
        log "  ✗ $apk_name install failed: $OUTPUT"
    fi
}

install_apk "$SEEKER_DIR/fakewallet.apk"
install_apk "$SEEKER_DIR/fakedapp.apk"
install_apk "$SEEKER_DIR/devclient.apk"

# Set device PIN (required for MWA wallet signing)
log "Setting device PIN to 1234..."
adb shell locksettings set-pin 1234 2>/dev/null || log "  PIN may already be set"

# Verify installations
log "Verifying installations..."
log "  Installed packages:"
adb shell pm list packages 2>/dev/null | grep -E "fake|seeker" | sed 's/^/    /' || true

log "=== SeekerStudio device ready ==="

# Keep the container running by waiting on the init process
wait $INIT_PID
