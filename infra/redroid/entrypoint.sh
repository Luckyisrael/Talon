#!/system/bin/sh
# ============================================================
# SeekerStudio Redroid Device Setup
# Run via ADB after container boot:
#   adb -s localhost:5555 shell "sh /data/local/tmp/device-setup.sh"
#
# This script only configures the Android device itself.
# No Solana CLI or validators — that runs on the host.
# ============================================================

set -e

DEVICE_PIN="${DEVICE_PIN:-1234}"

log() {
  echo "[seeker] $*"
}

# --- Set device PIN (required for MWA wallet sessions) ---
log "Setting device PIN to ${DEVICE_PIN}..."
locksettings set-pin "${DEVICE_PIN}" 2>/dev/null || log "PIN may already be set"

# --- Verify display ---
log "Display info:"
wm size 2>/dev/null || log "wm size not available"
wm density 2>/dev/null || log "wm density not available"

log "=== Device setup complete ==="
log "Device PIN: ${DEVICE_PIN}"
log "ADB:       adb shell"
