# ============================================================
# SeekerStudio — Launch Redroid Dev Container
# Run from PowerShell:
#   powershell C:\Users\Lucky\Desktop\SeekerStudio\infra\redroid\launch-dev.ps1
#
# Redroid is a pure Android device — runs APKs, streams via ws-scrcpy.
# No Solana tooling inside. Host handles all build/compile work.
# ============================================================

$ContainerName = "redroid-seeker-dev"
$ImageName = "redroid/redroid:14.0.0_64only-latest"
$DevicePin = "1234"
$AdbPort = 5555

Write-Host "=== SeekerStudio: Launch Redroid Dev Container ===" -ForegroundColor Cyan

# Step 1: Stop existing container
Write-Host "`n--- Stopping existing container ---" -ForegroundColor Yellow
wsl -d SeekerLinux -e docker stop $ContainerName 2>$null
wsl -d SeekerLinux -e docker rm $ContainerName 2>$null

# Step 2: Launch container with memfd (no ashmem needed)
Write-Host "--- Launching Redroid container (memfd mode) ---" -ForegroundColor Yellow
wsl -d SeekerLinux -e docker run -d `
  --name $ContainerName `
  --privileged `
  --network host `
  $ImageName `
  androidboot.use_memfd=1 `
  androidboot.redroid_width=1080 `
  androidboot.redroid_height=2400 `
  androidboot.redroid_dpi=420

Write-Host "Container started. Waiting for Android boot (30s)..." -ForegroundColor Yellow
Start-Sleep -Seconds 30

# Step 3: Enable ADB TCP mode
Write-Host "--- Enabling ADB TCP mode ---" -ForegroundColor Yellow
wsl -d SeekerLinux -e docker exec $ContainerName setprop service.adb.tcp.port $AdbPort 2>$null
Start-Sleep -Seconds 2
wsl -d SeekerLinux -e docker exec $ContainerName stop adbd 2>$null
Start-Sleep -Seconds 2
wsl -d SeekerLinux -e docker exec $ContainerName start adbd 2>$null
Start-Sleep -Seconds 5

# Step 4: Connect ADB
Write-Host "--- Connecting ADB ---" -ForegroundColor Yellow
wsl -d SeekerLinux -e adb connect localhost:$AdbPort
Start-Sleep -Seconds 2

# Step 5: Verify connection
$devices = wsl -d SeekerLinux -e adb devices 2>&1
if ($devices -match "localhost:$AdbPort\s+device") {
    Write-Host "ADB connected!" -ForegroundColor Green
} else {
    Write-Host "ADB connection failed. Check container logs." -ForegroundColor Red
    exit 1
}

# Step 6: Set device PIN (required for MWA)
Write-Host "--- Setting device PIN ---" -ForegroundColor Yellow
wsl -d SeekerLinux -e adb -s localhost:$AdbPort push /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/redroid/entrypoint.sh /tmp/device-setup.sh 2>&1
wsl -d SeekerLinux -e adb -s localhost:$AdbPort shell "sh /tmp/device-setup.sh" 2>&1

Write-Host "`n=== Container Ready ===" -ForegroundColor Green
Write-Host "Container:   $ContainerName"
Write-Host "ADB:         adb -s localhost:$AdbPort shell"
Write-Host "Resolution:  1080x2400 @ 420 DPI"
Write-Host "Device PIN:  $DevicePin"
