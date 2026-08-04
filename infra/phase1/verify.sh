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

# Step 3: Check for ashmem (optional, for legacy compatibility)
echo "--- Ashmem (optional) ---"
if grep -q ashmem /proc/misc 2>/dev/null; then
  echo "✅ ashmem found"
  grep ashmem /proc/misc
else
  echo "⚠️  ashmem not found (ok if using binderfs, which is preferred)"
fi
echo ""

# Step 4: Check /dev/binder* exists
echo "--- Binder device nodes ---"
ls -la /dev/binder* 2>/dev/null || echo "No /dev/binder* found (binderfs not mounted yet)"
echo ""

# Step 5: Check Docker access
echo "--- Docker access ---"
if docker info &>/dev/null; then
  echo "✅ Docker accessible from SeekerLinux"
  docker --version
else
  echo "⚠️  Docker not accessible from SeekerLinux"
  echo "If using Docker Desktop, make sure it's running"
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
  echo "✅ READY for Redroid — kernel supports Android binder"
else
  echo "❌ NOT READY — fix kernel before proceeding"
  exit 1
fi
