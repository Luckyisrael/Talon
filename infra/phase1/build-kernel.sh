#!/usr/bin/env bash
# ============================================================
# Phase 1 — Step 2: Build Custom WSL2 Kernel with Binder
# Run this INSIDE the SeekerLinux WSL distro:
#   wsl -d SeekerLinux -e bash -c "bash < /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/build-kernel.sh"
# ============================================================
set -euo pipefail

KERNEL_TAG="linux-msft-wsl-6.6.87.2"
KERNEL_REPO="https://github.com/microsoft/WSL2-Linux-Kernel.git"
BUILD_DIR="/tmp/wsl-kernel-build"
NPROC=$(nproc)

echo "=== SeekerStudio Phase 1: Build Custom WSL2 Kernel ==="
echo "Target tag: $KERNEL_TAG"
echo "CPUs available: $NPROC"
echo ""

# Step 1: Install build dependencies
echo "=== Installing kernel build dependencies ==="
apt-get update -qq
apt-get install -y -qq \
  build-essential flex bison bc libssl-dev libelf-dev \
  dwarves pahole cpio kmod libncurses-dev \
  git ca-certificates 2>/dev/null

# Step 2: Clone WSL2-Linux-Kernel
echo ""
echo "=== Cloning WSL2-Linux-Kernel (tag: $KERNEL_TAG) ==="
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"
git clone --depth 1 --branch "$KERNEL_TAG" "$KERNEL_REPO" .
echo "Clone complete."

# Step 3: Start from Microsoft's WSL config
echo ""
echo "=== Configuring kernel with binder + ashmem ==="
cp Microsoft/config-wsl .config

# Step 4: Enable Android Binder IPC and Ashmem
# These are the key changes that enable Redroid
scripts/config --enable CONFIG_ANDROID
scripts/config --enable CONFIG_ANDROID_BINDER_IPC
scripts/config --enable CONFIG_ANDROID_BINDERFS
scripts/config --enable CONFIG_ASHMEM

# Step 4b: Enable ISO9660 (required for Docker Desktop CLI tools ISO mount)
scripts/config --enable CONFIG_ISO9660_FS

# Step 5: Rebuild config (resolve dependencies)
echo ""
echo "=== Running olddefconfig ==="
make olddefconfig

# Step 6: Verify our options are set
echo ""
echo "=== Verifying binder + ashmem in .config ==="
grep -E 'CONFIG_ANDROID[= ]' .config
grep -E 'CONFIG_ASHMEM[= ]' .config
echo ""

# Step 7: Build
echo "=== Building kernel (this takes 10-20 min) ==="
echo "Start time: $(date)"
make -j"$NPROC" bzImage
echo "Build complete: $(date)"

# Step 8: Copy bzImage to Windows filesystem
echo ""
echo "=== Copying bzImage to Windows ==="
WIN_OUT="/mnt/c/Users/Lucky/seekersstudio-kernel"
mkdir -p "$WIN_OUT"
cp arch/x86/boot/bzImage "$WIN_OUT/bzImage"
echo "Copied to $WIN_OUT/bzImage"

# Step 9: Cleanup (save ~3GB)
echo ""
echo "=== Cleaning build artifacts ==="
make clean
echo "Clean complete. Freed disk space."

# Step 10: Remove build source to reclaim disk space inside VHDX
echo ""
echo "=== Removing kernel source tree to reclaim disk space ==="
rm -rf "$BUILD_DIR"
echo "Source tree removed. Freed ~5 GB inside VHDX."

echo ""
echo "=== DONE ==="
echo "bzImage is at: C:\\Users\\Lucky\\seekersstudio-kernel\\bzImage"
echo ""
echo "Next step in PowerShell:"
echo "  1. Create C:\\Users\\Lucky\\.wslconfig with kernel=path"
echo "     (use infra/phase1/wslconfig.txt as template)"
echo "  2. Run: wsl --shutdown"
echo "  3. Run: wsl -d SeekerLinux -e uname -r  (should show custom kernel)"
echo "  4. Run: wsl -d SeekerLinux -e grep binder /proc/filesystems"
echo "  5. Run: wsl -d SeekerLinux -e grep iso9660 /proc/filesystems"
