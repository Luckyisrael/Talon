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
4. Enables four critical options:
   - `CONFIG_ANDROID=y` — enables the Android subsystem
   - `CONFIG_ANDROID_BINDER_IPC=y` — the Binder IPC driver (built-in, not module)
   - `CONFIG_ANDROID_BINDERFS=y` — modern Binder filesystem (replaces legacy /dev/binder)
   - `CONFIG_ASHMEM=y` — shared memory for legacy compatibility
   - `CONFIG_ISO9660_FS=y` — ISO9660 filesystem (required for Docker Desktop to mount its CLI tools ISO)
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
✅ ISO9660 found in /proc/filesystems
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

## Docker: Docker CE (not Docker Desktop)

We use Docker CE installed directly inside SeekerLinux, **not Docker Desktop**.

**Why not Docker Desktop?**
Docker Desktop's WSL2 backend ships precompiled LinuxKit binaries (`initd`) that are
compiled against specific kernel netlink headers. Our custom kernel (6.6.87.2+) has
slightly different netlink attribute validation, causing `initd` to crash silently.
This is a known issue ([WSL#40573](https://github.com/microsoft/WSL/issues/40573))
that cannot be fixed by kernel config — it requires Docker to recompile their binaries.

**How Docker CE works instead:**
- `docker.io` package from Ubuntu repos installs Docker CE 29.x directly into SeekerLinux
- Docker daemon runs as a systemd service inside SeekerLinux (systemd enabled via `/etc/wsl.conf`)
- Containers use `--network host` mode (bridge/iptables not available due to missing kernel modules)
- Access Docker from Windows via: `wsl -d SeekerLinux -e docker ...`
- Also works for your other projects (postgres, etc.) via the same pattern

**Docker daemon configuration** (`/etc/docker/daemon.json`):
```json
{
  "iptables": false,
  "bridge": "none",
  "ip-forward": true,
  "storage-driver": "overlay2"
}
```
- `iptables: false` — `xt_addrtype` module missing from kernel (compiled as `=m`, not loadable)
- `bridge: none` — bridge netfilter module missing, same reason
- Containers use `--network host` instead for networking
- For port-mapped containers, access via `localhost:<port>` from Windows

## Disk Space Awareness

The kernel build consumes ~5-7 GB temporarily:
- Kernel source: ~2 GB
- Build artifacts (.o files): ~3-4 GB
- bzImage output: ~15 MB

The `build-kernel.sh` script runs `make clean` AND removes the source tree after
copying the bzImage, freeing ~5 GB. Docker CE adds ~300 MB. Net disk impact is minimal.

After setup, compact the SeekerLinux VHDX to reclaim deleted space on Windows:
```powershell
wsl --manage SeekerLinux --set-compact true
```

## How to Run (Manual Steps)

If you prefer running commands manually instead of the master script:

```powershell
# 1. Import Ubuntu WSL distro
wsl --import SeekerLinux C:\Users\Lucky\wsl\SeekerLinux C:\Users\Lucky\AppData\Local\Temp\opencode\noble-wsl-amd64.wsl

# 2. Set default user to root + enable systemd
wsl -d SeekerLinux -e bash -c 'printf "[boot]\nsystemd=true\n\n[user]\ndefault=root\n" > /etc/wsl.conf'
wsl --terminate SeekerLinux
Start-Sleep -Seconds 3

# 3. Install Docker CE
wsl -d SeekerLinux -e bash -c "apt-get update && apt-get install -y docker.io"

# 4. Configure Docker daemon
wsl -d SeekerLinux -e bash -c 'mkdir -p /etc/docker && printf "{\"iptables\":false,\"bridge\":\"none\",\"ip-forward\":true,\"storage-driver\":\"overlay2\"}\n" > /etc/docker/daemon.json'
wsl -d SeekerLinux -e systemctl restart docker

# 5. Verify Docker
wsl -d SeekerLinux -e docker run --rm hello-world

# 6. Build kernel (10-20 min)
wsl -d SeekerLinux -e bash -c "bash < /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/build-kernel.sh"

# 7. Write .wslconfig
@'
[wsl2]
kernel=C:\\Users\\Lucky\\seekersstudio-kernel\\bzImage
'@ | Out-File -FilePath "$env:USERPROFILE\.wslconfig" -Encoding ascii -Force

# 8. Restart WSL
wsl --shutdown
Start-Sleep -Seconds 3

# 9. Verify binder + Docker
wsl -d SeekerLinux -e bash -c "bash < /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/verify.sh"

# 10. Compact VHDX (reclaim disk space)
wsl --manage SeekerLinux --set-compact true

# 11. Boot Redroid
wsl -d SeekerLinux -e bash -c "bash < /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/phase1/boot-redroid.sh"
```

**Using Docker from Windows:**
```powershell
# Run any docker command via WSL
wsl -d SeekerLinux -e docker ps
wsl -d SeekerLinux -e docker run -d --name mydb --network host -e POSTGRES_PASSWORD=secret postgres:16
wsl -d SeekerLinux -e docker exec -it mydb psql -U postgres

# Or enter SeekerLinux shell and use docker directly
wsl -d SeekerLinux
docker ps
```

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `grep binder` returns nothing | Custom kernel not loaded | Verify `.wslconfig` path, run `wsl --shutdown`, restart |
| `grep iso9660` returns nothing | ISO9660 not built into kernel | Rebuild kernel with `scripts/config --enable CONFIG_ISO9660_FS` |
| Docker fails to start | Kernel modules missing | Check `/etc/docker/daemon.json` has `iptables:false`, `bridge:none` |
| `docker pull` times out | DNS not resolving inside WSL | Check `/etc/resolv.conf` in SeekerLinux, restart WSL |
| `adb devices` shows `offline` | Container not fully booted | Wait 10s, retry `adb connect localhost:5555` |
| Container exits immediately | Kernel lacks binder | Rebuild kernel with binder enabled |
| `getprop` shows wrong resolution | Redroid boot params wrong | Recreate container with correct `androidboot.redroid_*` flags |
| "address already in use" | Port conflict with host services | Use `--network host` and different ports |

## What This Enables

Once Phase 1 is complete:
- **Phase 2** can build the Solana dev stack image (mock wallet + validator)
- **Phase 3** can start the Go backend (container management)
- **Phase 4** can wire streaming (ws-scrcpy to browser)
- **Phase 5** can build the AI orchestrator (typecheck gate + heal loop)

The Redroid container is the foundation everything else depends on.
