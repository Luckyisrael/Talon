# ============================================================
# SeekerStudio — Launch Redroid Dev Container
# Run from PowerShell (not as admin needed):
#   .\launch-dev.ps1
# ============================================================

$ContainerName = "redroid-seeker-dev"
$ImageName = "redroid-seeker-dev"
$DevicePin = "1234"

Write-Host "=== SeekerStudio: Launch Redroid Dev Container ===" -ForegroundColor Cyan

# Step 1: Stop existing container
Write-Host "`n--- Stopping existing container ---" -ForegroundColor Yellow
wsl -d SeekerLinux -e docker stop $ContainerName 2>$null
wsl -d SeekerLinux -e docker rm $ContainerName 2>$null

# Step 2: Launch container
Write-Host "--- Launching Redroid container ---" -ForegroundColor Yellow
wsl -d SeekerLinux -e docker run -d `
  --name $ContainerName `
  --privileged `
  --network host `
  $ImageName

Write-Host "Container started. Waiting for Android boot (30s)..." -ForegroundColor Yellow
Start-Sleep -Seconds 30

# Step 3: Wait for ADB
Write-Host "--- Waiting for ADB ---" -ForegroundColor Yellow
$retries = 0
while ($retries -lt 15) {
  $devices = wsl -d SeekerLinux -e adb devices 2>&1
  if ($devices -match "emulator-5554\s+device") {
    Write-Host "ADB ready!" -ForegroundColor Green
    break
  }
  Start-Sleep -Seconds 3
  $retries++
}

if ($retries -ge 15) {
  Write-Host "ADB not ready after 45s. Check container logs." -ForegroundColor Red
  exit 1
}

# Step 4: Run setup via ADB
Write-Host "--- Running setup (PIN + Solana + Validator) ---" -ForegroundColor Yellow
wsl -d SeekerLinux -e adb -s emulator-5554 push /mnt/c/Users/Lucky/Desktop/SeekerStudio/infra/redroid/entrypoint.sh /tmp/entrypoint.sh 2>&1
wsl -d SeekerLinux -e adb -s emulator-5554 shell "chmod 755 /tmp/entrypoint.sh" 2>&1
wsl -d SeekerLinux -e adb -s emulator-5554 shell "sh /tmp/entrypoint.sh" 2>&1

Write-Host "`n=== Container Ready ===" -ForegroundColor Green
Write-Host "ADB:         adb -s emulator-5554 shell"
Write-Host "Solana:      adb -s emulator-5554 shell /opt/solana/bin/solana --version"
Write-Host "Validator:   adb -s emulator-5554 shell /opt/solana/bin/solana cluster-version"
Write-Host "Wallet:      /tmp/solana-keypair.json"
