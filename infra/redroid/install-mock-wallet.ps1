# ============================================================
# SeekerStudio — Install Mock MWA Wallet on Redroid
#
# The Solana Mobile Mock Wallet lets generated apps test:
#   - Wallet connection (MWA protocol)
#   - Transaction signing
#   - Message signing
#   - All standard wallet operations
#
# Prerequisites (on Windows):
#   - JDK 17+ (https://adoptium.net/)
#   - Android SDK (via Android Studio or cmdline-tools)
#   - Set ANDROID_HOME environment variable
#
# Run from PowerShell:
#   powershell C:\Users\Lucky\Desktop\SeekerStudio\infra\redroid\install-mock-wallet.ps1
# ============================================================

$RepoUrl = "https://github.com/solana-mobile/mock-mwa-wallet.git"
$CloneDir = "$env:TEMP\mock-mwa-wallet"
$AdbTarget = "localhost:5555"

Write-Host "=== SeekerStudio: Install Mock MWA Wallet ===" -ForegroundColor Cyan

# Step 1: Check prerequisites
Write-Host "`n--- Checking prerequisites ---" -ForegroundColor Yellow

if (-not $env:ANDROID_HOME) {
    Write-Host "ANDROID_HOME not set. Install Android SDK and set ANDROID_HOME." -ForegroundColor Red
    Write-Host "  Option A: Install Android Studio (includes SDK)"
    Write-Host "  Option B: Download cmdline-tools from https://developer.android.com/studio#command-line-tools-only"
    exit 1
}
Write-Host "ANDROID_HOME: $env:ANDROID_HOME" -ForegroundColor Green

# Check Java
try {
    $javaVersion = java -version 2>&1 | Select-String "version"
    Write-Host "Java: $javaVersion" -ForegroundColor Green
} catch {
    Write-Host "Java not found. Install JDK 17+: https://adoptium.net/" -ForegroundColor Red
    exit 1
}

# Check ADB
$devices = wsl -d SeekerLinux -e adb devices 2>&1
if ($devices -notmatch "localhost:5555\s+device") {
    Write-Host "Redroid not connected on localhost:5555. Run launch-dev.ps1 first." -ForegroundColor Red
    exit 1
}
Write-Host "Redroid connected" -ForegroundColor Green

# Step 2: Clone mock wallet repo
Write-Host "`n--- Cloning mock-mwa-wallet ---" -ForegroundColor Yellow
if (Test-Path $CloneDir) {
    Remove-Item -Recurse -Force $CloneDir
}
git clone --depth 1 $RepoUrl $CloneDir 2>&1 | Select-Object -Last 3

# Step 3: Build APK
Write-Host "`n--- Building APK ---" -ForegroundColor Yellow
Push-Location $CloneDir

# Find gradlew
$gradlew = if (Test-Path "gradlew.bat") { "gradlew.bat" } else { ".\gradlew" }

& $gradlew assembleDebug 2>&1 | Select-Object -Last 10
if ($LASTEXITCODE -ne 0) {
    Write-Host "Build failed. Check output above." -ForegroundColor Red
    Pop-Location
    exit 1
}
Pop-Location

# Step 4: Find the built APK
$apkPath = Get-ChildItem -Path $CloneDir -Recurse -Filter "*.apk" | Where-Object { $_.FullName -match "debug" } | Select-Object -First 1
if (-not $apkPath) {
    Write-Host "APK not found after build." -ForegroundColor Red
    exit 1
}
Write-Host "APK found: $($apkPath.FullName)" -ForegroundColor Green

# Step 5: Install on Redroid
Write-Host "`n--- Installing on Redroid ---" -ForegroundColor Yellow
wsl -d SeekerLinux -e adb -s $AdbTarget install "$($apkPath.FullName -replace '\\','/')" 2>&1

# Step 6: Verify installation
Write-Host "`n--- Verifying installation ---" -ForegroundColor Yellow
$packages = wsl -d SeekerLinux -e adb -s $AdbTarget shell "pm list packages" 2>&1
if ($packages -match "mock") {
    Write-Host "Mock MWA Wallet installed!" -ForegroundColor Green
} else {
    Write-Host "Package not found. Check install output." -ForegroundColor Red
}

# Cleanup
Write-Host "`n--- Cleaning up ---" -ForegroundColor Yellow
Remove-Item -Recurse -Force $CloneDir 2>$null

Write-Host "`n=== Mock Wallet Ready ===" -ForegroundColor Green
Write-Host "Wallet: Solana Mobile Mock MWA Wallet"
Write-Host "Package: com.solana.mobile.mockwallet (approximate)"
Write-Host "Test: Open a generated app → Connect Wallet → should find Mock Wallet"
