# ============================================================
# SeekerStudio Phase 1 — Master Orchestration Script
# Run this from PowerShell in the project root:
#   .\infra\phase1\run-phase1.ps1
# ============================================================
param(
  [switch]$SkipKernelBuild,
  [switch]$SkipRedroid
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$WSL_DISTRO = "SeekerLinux"
$WIN_HOME = "C:\Users\Lucky"

Write-Host "=============================================" -ForegroundColor Cyan
Write-Host " SeekerStudio Phase 1: WSL2 + Redroid Setup" -ForegroundColor Cyan
Write-Host "=============================================" -ForegroundColor Cyan
Write-Host ""

# ---- Step 1: Import WSL Distro ----
Write-Host "[1/5] Importing Ubuntu WSL2 Distro..." -ForegroundColor Yellow
$importScript = Join-Path $ScriptDir "setup-wsl.sh"
wsl -d $WSL_DISTRO -e echo "Distro already exists" 2>$null
if ($LASTEXITCODE -ne 0) {
    bash $importScript
} else {
    Write-Host "  Distro $WSL_DISTRO already imported." -ForegroundColor Green
}
Write-Host ""

# ---- Step 2: Build Custom Kernel ----
if (-not $SkipKernelBuild) {
    Write-Host "[2/5] Building Custom WSL2 Kernel (10-20 min)..." -ForegroundColor Yellow
    $buildScript = Join-Path $ScriptDir "build-kernel.sh"
    $buildScriptWsl = "/mnt/c" + $buildScript.Substring(3).Replace("\", "/")
    wsl -d $WSL_DISTRO -e bash -c "bash $buildScriptWsl"
    Write-Host "  Kernel build complete." -ForegroundColor Green
} else {
    Write-Host "[2/5] SKIPPED (use -SkipKernelBuild to skip)" -ForegroundColor DarkGray
}
Write-Host ""

# ---- Step 3: Apply .wslconfig ----
Write-Host "[3/5] Applying .wslconfig..." -ForegroundColor Yellow
$wslconfigSrc = Join-Path $ScriptDir "wslconfig.txt"
$wslconfigDst = "$WIN_HOME\.wslconfig"
$bzImage = "$WIN_HOME\seekersstudio-kernel\bzImage"

if (Test-Path $bzImage) {
    # Write .wslconfig with correct kernel path
    $content = @"
[wsl2]
kernel=C:\\Users\\Lucky\\seekersstudio-kernel\\bzImage
"@
    Set-Content -Path $wslconfigDst -Value $content -Force
    Write-Host "  .wslconfig written to $wslconfigDst" -ForegroundColor Green

    Write-Host "  Shutting down WSL to apply kernel..." -ForegroundColor Yellow
    wsl --shutdown
    Start-Sleep -Seconds 3

    Write-Host "  Verifying kernel..." -ForegroundColor Yellow
    $kernelVersion = wsl -d $WSL_DISTRO -e uname -r 2>&1
    Write-Host "  Kernel: $kernelVersion" -ForegroundColor Cyan
} else {
    Write-Host "  bzImage not found at $bzImage — skipping kernel wiring" -ForegroundColor Red
    Write-Host "  Make sure Step 2 completed successfully" -ForegroundColor Red
}
Write-Host ""

# ---- Step 4: Verify Binder ----
Write-Host "[4/5] Verifying Binder Support..." -ForegroundColor Yellow
$verifyScript = Join-Path $ScriptDir "verify.sh"
$verifyScriptWsl = "/mnt/c" + $verifyScript.Substring(3).Replace("\", "/")
wsl -d $WSL_DISTRO -e bash -c "bash $verifyScriptWsl"
Write-Host ""

# ---- Step 5: Boot Redroid ----
if (-not $SkipRedroid) {
    Write-Host "[5/5] Booting Redroid Container..." -ForegroundColor Yellow
    $redroidScript = Join-Path $ScriptDir "boot-redroid.sh"
    $redroidScriptWsl = "/mnt/c" + $redroidScript.Substring(3).Replace("\", "/")
    wsl -d $WSL_DISTRO -e bash -c "bash $redroidScriptWsl"
} else {
    Write-Host "[5/5] SKIPPED (use -SkipRedroid to skip)" -ForegroundColor DarkGray
}

Write-Host ""
Write-Host "=============================================" -ForegroundColor Green
Write-Host " Phase 1 Complete!" -ForegroundColor Green
Write-Host "=============================================" -ForegroundColor Green
