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

# Set default user and enable systemd for our scripts
echo ""
echo "=== Setting default user to root + enabling systemd ==="
wsl -d "$DISTRO_NAME" -e bash -c "
cat > /etc/wsl.conf << 'WSLCONF'
[boot]
systemd=true

[user]
default=root
WSLCONF
"
echo "Done. Default user set to root, systemd enabled."

# Restart distro for systemd to take effect
echo "Restarting distro for systemd..."
wsl --terminate "$DISTRO_NAME"
sleep 3

# Install Docker CE
echo ""
echo "=== Installing Docker CE ==="
wsl -d "$DISTRO_NAME" -e bash -c "apt-get update -qq && apt-get install -y -qq docker.io 2>/dev/null"
echo "Docker CE installed."

# Configure Docker daemon (no iptables, no bridge — WSL2 kernel modules not available)
echo ""
echo "=== Configuring Docker daemon ==="
wsl -d "$DISTRO_NAME" -e bash -c "
mkdir -p /etc/docker
cat > /etc/docker/daemon.json << 'DOCKERJSON'
{
  \"iptables\": false,
  \"bridge\": \"none\",
  \"ip-forward\": true,
  \"storage-driver\": \"overlay2\"
}
DOCKERJSON
"
echo "Docker daemon configured."

# Enable and start Docker
echo ""
echo "=== Starting Docker service ==="
wsl -d "$DISTRO_NAME" -e systemctl enable docker
wsl -d "$DISTRO_NAME" -e systemctl start docker
sleep 2

# Verify Docker
echo ""
echo "=== Verifying Docker ==="
wsl -d "$DISTRO_NAME" -e docker run --rm hello-world 2>&1 | head -5

echo ""
echo "Next step: Run infra/phase1/build-kernel.sh inside the SeekerLinux distro."
echo "Use: wsl -d SeekerLinux -e bash -c 'bash < /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/build-kernel.sh'"
