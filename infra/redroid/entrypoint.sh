#!/system/bin/sh
# ============================================================
# SeekerStudio Redroid Entrypoint
# Runs inside the Redroid container (Android userspace)
# ============================================================

set -e

SOLANA_VERSION="${SOLANA_VERSION:-1.18.26}"
SOLANA_DIR="/opt/solana"
VALIDATOR_LEDGER="/tmp/validator-ledger"
WALLET_KEYPAIR="/tmp/solana-keypair.json"
DEVICE_PIN="${DEVICE_PIN:-1234}"

log() {
  echo "[seeker] $(date '+%H:%M:%S') $*"
}

# --- Step 1: Wait for Android boot ---
log "Waiting for Android to boot..."
while [ "$(getprop sys.boot_completed 2>/dev/null)" != "1" ]; do
  sleep 2
done
log "Android boot completed."

# --- Step 2: Wait for ADB ---
log "Waiting for ADB..."
while ! adb get-state >/dev/null 2>&1; do
  sleep 1
done
log "ADB ready."

# --- Step 3: Set device PIN (required for MWA) ---
log "Setting device PIN to ${DEVICE_PIN}..."
adb shell locksettings set-pin "${DEVICE_PIN}" 2>/dev/null || true

# --- Step 4: Install Solana CLI tools ---
if [ ! -f "${SOLANA_DIR}/bin/solana" ]; then
  log "Installing Solana CLI v${SOLANA_VERSION}..."
  mkdir -p "${SOLANA_DIR}"
  
  # Download Solana release tarball
  SOLANA_URL="https://github.com/solana-labs/solana/releases/download/v${SOLANA_VERSION}/solana-release-x86_64-unknown-linux-gnu.tar.bz2"
  
  # Try to download - if network is unavailable, skip and log
  if wget -q "${SOLANA_URL}" -O /tmp/solana.tar.bz2 2>/dev/null; then
    tar -xjf /tmp/solana.tar.bz2 -C /tmp/
    mv /tmp/solana-release/* "${SOLANA_DIR}/"
    rm -rf /tmp/solana.tar.bz2 /tmp/solana-release
    log "Solana CLI installed to ${SOLANA_DIR}"
  else
    log "WARNING: Could not download Solana CLI. Will try again later or install manually."
    log "Manual install: adb push solana-release-x86_64-unknown-linux-gnu.tar.bz2 /tmp/ && run setup-solana.sh"
  fi
fi

# Add Solana to PATH
export PATH="${SOLANA_DIR}/bin:${PATH}"

# --- Step 5: Generate wallet keypair if not exists ---
if [ ! -f "${WALLET_KEYPAIR}" ]; then
  log "Generating wallet keypair..."
  if command -v solana-keygen >/dev/null 2>&1; then
    solana-keygen new --no-bip39-passphrase -o "${WALLET_KEYPAIR}" --force
    log "Keypair created at ${WALLET_KEYPAIR}"
  fi
fi

# --- Step 6: Start solana-test-validator ---
if command -v solana-test-validator >/dev/null 2>&1; then
  log "Starting solana-test-validator..."
  mkdir -p "${VALIDATOR_LEDGER}"
  
  # Kill any existing validator
  pkill -f solana-test-validator 2>/dev/null || true
  sleep 1
  
  # Start validator in background
  solana-test-validator \
    --ledger "${VALIDATOR_LEDGER}" \
    --reset \
    --quiet &
  
  VALIDATOR_PID=$!
  log "Validator started (PID: ${VALIDATOR_PID}). Waiting for it to be ready..."
  
  # Wait for validator to be ready
  for i in $(seq 1 30); do
    if solana cluster-version >/dev/null 2>&1; then
      log "Validator is ready!"
      break
    fi
    sleep 2
  done
  
  # Configure Solana CLI to use local validator
  solana config set --url http://127.0.0.1:8899
  
  # Airdrop SOL to the wallet
  if [ -f "${WALLET_KEYPAIR}" ]; then
    log "Airdropping 100 SOL..."
    solana airdrop 100 --keypair "${WALLET_KEYPAIR}" 2>/dev/null || true
    
    BALANCE=$(solana balance --keypair "${WALLET_KEYPAIR}" 2>/dev/null || echo "unknown")
    log "Wallet balance: ${BALANCE}"
  fi
else
  log "WARNING: solana-test-validator not found. Skipping validator setup."
fi

log "=== SeekerStudio Redroid setup complete ==="
log "Solana CLI: ${SOLANA_DIR}/bin/solana"
log "Validator: http://127.0.0.1:8899"
log "Wallet keypair: ${WALLET_KEYPAIR}"
log "Device PIN: ${DEVICE_PIN}"

# Keep container running
log "Container ready. Sleeping..."
exec tail -f /dev/null
