# Phase 1 — Educational Guide: WSL2 Kernel + Redroid

## What We Built

Phase 1 creates the local Android runtime that powers SeekerStudio's live preview.
Without this, there is no Redroid container, no ADB, no streaming — the core loop doesn't exist.

## The Problem: Why a Custom Kernel?

Redroid (Remote Android in Docker) runs Android as a Linux container. Unlike regular
Docker containers that share the host's Linux kernel, Redroid needs two special kernel
features that Android's runtime depends on:

1. **`binder_linux`** — Android's inter-process communication (IPC) mechanism.
   Every Android app talks to the system server, SurfaceFlinger, and other apps through
   Binder. Without it, the Android runtime simply cannot start.

2. **`ashmem_linux`** — Android's shared memory allocator. Used for zero-copy data
   passing between processes. Some Android components depend on it (though BinderFS is
   the modern replacement).

**The issue:** Microsoft's stock WSL2 kernel (`6.6.87.2-microsoft-standard-WSL2`) does NOT
include these modules. They're disabled in the default kernel config because Microsoft
doesn't ship Android-related features by default.

**The solution:** Build a custom WSL2 kernel from Microsoft's own source code, enabling
`CONFIG_ANDROID_BINDER_IPC`, `CONFIG_ANDROID_BINDERFS`, and `CONFIG_ASHMEM`.

## Architecture of the Solution

```
┌──────────────────────────────────────────────────────────────┐
│                    Windows Host                              │
│  .wslconfig ──► kernel=C:\...\bzImage (custom WSL2 kernel)  │
└───────────────────────┬──────────────────────────────────────┘
                        │ WSL2 loads this kernel for ALL distros
┌───────────────────────▼──────────────────────────────────────┐
│              SeekerLinux (Ubuntu 24.04 WSL2)                │
│  Built the custom kernel from microsoft/WSL2-Linux-Kernel   │
│  with binder + ashmem enabled in .config                    │
└───────────────────────┬──────────────────────────────────────┘
                        │ Docker Desktop uses this same kernel
┌───────────────────────▼──────────────────────────────────────┐
│            Docker Desktop (WSL2 backend)                     │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  redroid-seeker-base container (--privileged)          │  │
│  │  Android 14 @ 1080x2400 @ 420 DPI                     │  │
│  │  - Mounts /dev/binder* from host kernel                │  │
│  │  - ADB on port 5555                                    │  │
│  │  - Mock MWA wallet + solana-test-validator             │  │
│  └────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────┘
```

## Step-by-Step Breakdown

### Step 1: Import Ubuntu WSL2 Distro (`setup-wsl.sh`)

**What it does:**
- Downloads an Ubuntu 24.04 rootfs (`.wsl` format) from Canonical's cloud images
- Imports it as a WSL2 distro named `SeekerLinux` using `wsl --import`
- Sets the default user to `root` (so our scripts run without interactive prompts)

**Why we need it:**
Building a Linux kernel requires the GCC compiler, Make, flex, bison, and other
build tools. These aren't available in the minimal `docker-desktop` WSL2 distro.
Ubuntu provides them via `apt`.

**Key command:**
```powershell
wsl --import SeekerLinux C:\Users\Lucky\wsl\SeekerLinux noble-wsl-amd64.wsl
```

### Step 2: Build Custom Kernel (`build-kernel.sh`)

**What it does:**
1. Installs kernel build dependencies (`build-essential`, `flex`, `bison`, `bc`, `libssl-dev`, etc.)
2. Clones `microsoft/WSL2-Linux-Kernel` at tag `linux-msft-wsl-6.6.87.2` (matching your running kernel exactly)
3. Copies Microsoft's `config-wsl` as the base `.config`
4. Enables three critical options:
   - `CONFIG_ANDROID=y` — enables the Android subsystem
   - `CONFIG_ANDROID_BINDER_IPC=y` — the Binder IPC driver (built-in, not module)
   - `CONFIG_ANDROID_BINDERFS=y` — modern Binder filesystem (replaces legacy /dev/binder)
   - `CONFIG_ASHMEM=y` — shared memory for legacy compatibility
5. Runs `make olddefconfig` to resolve dependencies
6. Builds `bzImage` with all available CPUs (`make -j$(nproc)`)
7. Copies the finished kernel to `C:\Users\Lucky\seekersstudio-kernel\bzImage`

**Why matching the exact kernel tag matters:**
WSL2 kernels have Microsoft-specific patches (9p filesystem, virtio drivers, GPU
paravirtualization). If we built a different version, WSL2 might break. Matching
`6.6.87.2` ensures all existing WSL2 features continue working — we're just adding
the Android modules.

**Build time:** 10-20 minutes depending on CPU cores.

### Step 3: Wire the Custom Kernel (`.wslconfig`)

**What it does:**
Creates `C:\Users\Lucky\.wslconfig` pointing WSL2 to our custom kernel:

```ini
[wsl2]
kernel=C:\\Users\\Lucky\\seekersstudio-kernel\\bzImage
```

Then runs `wsl --shutdown` to force all WSL2 distros to restart with the new kernel.

**Why this works:**
`.wslconfig` is WSL2's global configuration file. The `kernel=` directive tells WSL2
to use a specific kernel binary instead of the bundled one. Since ALL WSL2 distros
(including `docker-desktop`) share this kernel, Redroid containers get binder support
automatically.

### Step 4: Verify Binder (`verify.sh`)

**What it does:**
Runs a battery of checks inside SeekerLinux:
- `uname -r` — confirms the custom kernel is loaded
- `grep binder /proc/filesystems` — confirms BinderFS is available
- `ls /dev/binder*` — confirms device nodes exist
- Docker connectivity check
- ADB availability check

**Expected output:**
```
✅ binder found in /proc/filesystems
binderfs /dev/binderfs
✅ Docker accessible from SeekerLinux
```

### Step 5: Boot Redroid (`boot-redroid.sh`)

**What it does:**
1. Pulls `redroid/redroid:14.0.0_64only-latest`
2. Launches with `--privileged` flag (required for binderfs access)
3. Sets Seeker resolution: `1080x2400 @ 420 DPI @ 60 FPS`
4. Connects via ADB on `localhost:5555`
5. Verifies resolution via `getprop`
6. Takes a screenshot as proof of life

**Why `--privileged`:**
Redroid needs access to the host's binderfs mount. Docker's privilege separation
normally blocks this. The `--privileged` flag grants the container the necessary
kernel capabilities.

## Disk Space Awareness

The kernel build consumes ~5-7 GB temporarily:
- Kernel source: ~2 GB
- Build artifacts (.o files): ~3-4 GB
- bzImage output: ~15 MB

The `build-kernel.sh` script runs `make clean` after copying the bzImage, freeing
~3 GB. The source code (~2 GB) remains for future rebuilds.

## How to Run (Manual Steps)

If you prefer running commands manually instead of the master script:

```powershell
# 1. Import Ubuntu WSL distro
wsl --import SeekerLinux C:\Users\Lucky\wsl\SeekerLinux C:\Users\Lucky\AppData\Local\Temp\opencode\noble-wsl-amd64.wsl

# 2. Set default user to root
wsl -d SeekerLinux -e bash -c 'echo -e "[user]\ndefault=root" > /etc/wsl.conf'

# 3. Build kernel (10-20 min)
wsl -d SeekerLinux -e bash -c "bash < /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/build-kernel.sh"

# 4. Write .wslconfig
@'
[wsl2]
kernel=C:\\Users\\Lucky\\seekersstudio-kernel\\bzImage
'@ | Out-File -FilePath "$env:USERPROFILE\.wslconfig" -Encoding ascii -Force

# 5. Restart WSL
wsl --shutdown
Start-Sleep -Seconds 3

# 6. Verify binder
wsl -d SeekerLinux -e bash -c "bash < /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/verify.sh"

# 7. Boot Redroid
wsl -d SeekerLinux -e bash -c "bash < /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/boot-redroid.sh"
```

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `grep binder` returns nothing | Custom kernel not loaded | Verify `.wslconfig` path, run `wsl --shutdown`, restart |
| Docker says "cannot connect" | Docker Desktop not running | Start Docker Desktop from Start Menu |
| `adb devices` shows `offline` | Container not fully booted | Wait 10s, retry `adb connect localhost:5555` |
| Container exits immediately | Kernel lacks binder | Rebuild kernel with binder enabled |
| `getprop` shows wrong resolution | Redroid boot params wrong | Recreate container with correct `androidboot.redroid_*` flags |

## What This Enables

Once Phase 1 is complete:
- **Phase 2** can build the Solana dev stack image (mock wallet + validator)
- **Phase 3** can start the Go backend (container management)
- **Phase 4** can wire streaming (ws-scrcpy to browser)
- **Phase 5** can build the AI orchestrator (typecheck gate + heal loop)

The Redroid container is the foundation everything else depends on.
