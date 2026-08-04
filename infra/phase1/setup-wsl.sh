#!/usr/bin/env bash
# ============================================================
# Phase 1 — Step 1: Import Ubuntu WSL2 Distro
# Run this in PowerShell (not inside WSL)
# ============================================================
set -euo pipefail

DISTRO_NAME="SeekerLinux"
IMPORT_DIR="C:\Users\Lucky\wsl\${DISTRO_NAME}"
ROOTFS="C:\Users\Lucky\AppData\Local\Temp\opencode\noble-wsl-amd64.wsl"

echo "=== SeekerStudio Phase 1: Import Ubuntu WSL2 Distro ==="

# Pre-checks
if ! command -v wsl &>/dev/null; then
  echo "ERROR: WSL not found. Run 'wsl --install' first."
  exit 1
fi
if [ ! -f "$ROOTFS" ]; then
  echo "ERROR: Rootfs not found at $ROOTFS"
  echo "Download from: https://cdimages.ubuntu.com/ubuntu-wsl/noble/daily-live/current/noble-wsl-amd64.wsl"
  exit 1
fi

# Check if distro already exists
if wsl -l -v 2>/dev/null | grep -q "$DISTRO_NAME"; then
  echo "Distro $DISTRO_NAME already exists. Skipping import."
else
  echo "Importing rootfs as WSL distro '$DISTRO_NAME'..."
  mkdir -p "$IMPORT_DIR"
  wsl --import "$DISTRO_NAME" "$IMPORT_DIR" "$ROOTFS"
  echo "Import complete."
fi

# Verify
echo "=== Distro list ==="
wsl -l -v

# Set default user to root for our scripts
echo ""
echo "=== Setting default user to root ==="
wsl -d "$DISTRO_NAME" -e bash -c "
cat > /etc/wsl.conf << 'WSLCONF'
[user]
default=root
WSLCONF
"
echo "Done. Default user set to root."
echo ""
echo "Next step: Run infra/phase1/build-kernel.sh inside the SeekerLinux distro."
