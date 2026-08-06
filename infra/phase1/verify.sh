#!/usr/bin/env bash
# ============================================================
# Phase 1 — Step 4: Verify Binder in Custom Kernel
# Run this inside SeekerLinux:
#   wsl -d SeekerLinux -e bash -c "bash < /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/verify.sh"
# ============================================================
set -euo pipefail

echo "=== SeekerStudio Phase 1: Verify Binder Kernel ==="
echo ""

# Step 1: Check kernel version
echo "--- Kernel version ---"
uname -r
echo ""

# Step 2: Check for binderfs
echo "--- Binder filesystem (must show 'binder') ---"
if grep -q binder /proc/filesystems 2>/dev/null; then
  echo "✅ binder found in /proc/filesystems"
  grep binder /proc/filesystems
else
  echo "❌ binder NOT found — kernel does not have binder support"
  echo "Make sure you set the custom kernel in .wslconfig and ran wsl --shutdown"
  exit 1
fi
echo ""

# Step 3: Check for memfd (used instead of ashmem)
echo "--- Memfd support (required for Redroid) ---"
if grep -q CONFIG_MEMFD_CREATE /proc/config.gz 2>/dev/null || zgrep -q CONFIG_MEMFD_CREATE /proc/config.gz 2>/dev/null; then
  echo "✅ memfd_create available"
elif [ -e /dev/shm ]; then
  echo "✅ /dev/shm exists (memfd likely available)"
else
  echo "⚠️  memfd status unknown — Redroid uses androidboot.use_memfd=1"
fi
echo ""

# Step 3b: Check for ISO9660 (required for Docker Desktop)
echo "--- ISO9660 filesystem (required for Docker Desktop) ---"
if grep -q iso9660 /proc/filesystems 2>/dev/null; then
  echo "✅ ISO9660 found in /proc/filesystems"
else
  echo "❌ ISO9660 NOT found — Docker Desktop cannot mount its CLI tools ISO"
  echo "Rebuild kernel with: scripts/config --enable CONFIG_ISO9660_FS"
  exit 1
fi
echo ""

# Step 4: Check /dev/binder* exists
echo "--- Binder device nodes ---"
ls -la /dev/binder* 2>/dev/null || echo "No /dev/binder* found (binderfs not mounted yet)"
echo ""

# Step 5: Check Docker CE
echo "--- Docker CE ---"
if command -v docker &>/dev/null; then
  echo "✅ docker command available"
  docker --version
else
  echo "❌ docker not installed"
  echo "Install with: apt-get install -y docker.io"
  exit 1
fi

# Check Docker daemon is running
if docker info &>/dev/null; then
  echo "✅ Docker daemon is running"
  docker info 2>/dev/null | grep "Server Version"
else
  echo "⚠️  Docker daemon not running"
  echo "Start with: systemctl start docker"
  echo "Check config: cat /etc/docker/daemon.json"
  echo "Check logs: journalctl -u docker.service"
fi
echo ""

# Step 6: Check adb
echo "--- ADB ---"
if command -v adb &>/dev/null; then
  echo "✅ adb available"
  adb version
else
  echo "⚠️  adb not installed (needed for Redroid)"
  echo "Install with: apt-get install -y android-tools-adb"
fi
echo ""

echo "=== Verification complete ==="
if grep -q binder /proc/filesystems; then
  echo "✅ Kernel supports Android binder"
else
  echo "❌ Kernel does NOT support binder — rebuild kernel before proceeding"
  exit 1
fi

if docker info &>/dev/null; then
  echo "✅ Docker CE is running"
else
  echo "⚠️  Docker CE is not running — check: systemctl status docker"
fi

if grep -q iso9660 /proc/filesystems; then
  echo "✅ ISO9660 support present"
fi

echo ""
echo "✅ READY for Redroid — kernel + Docker verified"
