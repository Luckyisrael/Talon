#!/system/bin/sh
# ============================================================
# SeekerStudio Redroid Setup
# Run via ADB after container boot:
#   adb -s localhost:5555 shell "sh /tmp/entrypoint.sh"
# ============================================================

set -e

SOLANA_VERSION="${SOLANA_VERSION:-1.18.26}"
SOLANA_DIR="/opt/solana"
VALIDATOR_LEDGER="/tmp/validator-ledger"
WALLET_KEYPAIR="/tmp/solana-keypair.json"
DEVICE_PIN="${DEVICE_PIN:-1234}"

log() {
  echo "[seeker] $*"
}

# --- Step 1: Set device PIN (required for MWA) ---
log "Setting device PIN to ${DEVICE_PIN}..."
locksettings set-pin "${DEVICE_PIN}" 2>/dev/null || log "PIN may already be set"

# --- Step 2: Install Solana CLI tools ---
if [ ! -f "${SOLANA_DIR}/bin/solana" ]; then
  log "Installing Solana CLI v${SOLANA_VERSION}..."
  mkdir -p "${SOLANA_DIR}"
  
  SOLANA_URL="https://github.com/solana-labs/solana/releases/download/v${SOLANA_VERSION}/solana-release-x86_64-unknown-linux-gnu.tar.bz2"
  
  # Try to download
  if wget -q "${SOLANA_URL}" -O /tmp/solana.tar.bz2 2>/dev/null; then
    tar -xjf /tmp/solana.tar.bz2 -C /tmp/
    cp -r /tmp/solana-release/* "${SOLANA_DIR}/"
    rm -rf /tmp/solana.tar.bz2 /tmp/solana-release
    log "Solana CLI installed to ${SOLANA_DIR}"
  else
    log "WARN: Could not download Solana CLI. Run setup-solana.sh manually or push files via ADB."
    log "  adb push solana-release-x86_64-unknown-linux-gnu.tar.bz2 /tmp/"
    log "  adb shell sh /tmp/setup-solana.sh"
  fi
fi

# --- Step 3: Generate wallet keypair ---
if [ ! -f "${WALLET_KEYPAIR}" ] && [ -f "${SOLANA_DIR}/bin/solana-keygen" ]; then
  log "Generating wallet keypair..."
  "${SOLANA_DIR}/bin/solana-keygen" new --no-bip39-passphrase -o "${WALLET_KEYPAIR}" --force 2>/dev/null || true
fi

# --- Step 4: Start solana-test-validator ---
if [ -f "${SOLANA_DIR}/bin/solana-test-validator" ]; then
  log "Starting solana-test-validator..."
  mkdir -p "${VALIDATOR_LEDGER}"
  
  # Kill any existing validator
  kill $(pgrep -f solana-test-validator) 2>/dev/null || true
  
  # Start validator in background
  "${SOLANA_DIR}/bin/solana-test-validator" \
    --ledger "${VALIDATOR_LEDGER}" \
    --reset \
    --quiet &
  
  log "Validator starting... waiting 10s"
  sleep 10
  
  # Configure CLI
  "${SOLANA_DIR}/bin/solana" config set --url http://127.0.0.1:8899 2>/dev/null || true
  
  # Airdrop
  if [ -f "${WALLET_KEYPAIR}" ]; then
    log "Airdropping 100 SOL..."
    "${SOLANA_DIR}/bin/solana" airdrop 100 --keypair "${WALLET_KEYPAIR}" 2>/dev/null || true
    BALANCE=$("${SOLANA_DIR}/bin/solana" balance --keypair "${WALLET_KEYPAIR}" 2>/dev/null || echo "check manually")
    log "Wallet balance: ${BALANCE}"
  fi
else
  log "WARN: solana-test-validator not found. Skipping."
fi

log "=== SeekerStudio setup complete ==="
log "Solana CLI: ${SOLANA_DIR}/bin/solana"
log "Validator:  http://127.0.0.1:8899"
log "Keypair:    ${WALLET_KEYPAIR}"
log "PIN:        ${DEVICE_PIN}"
