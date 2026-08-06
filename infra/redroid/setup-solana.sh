#!/system/bin/sh
# ============================================================
# SeekerStudio Solana Setup Script
# Run this manually via ADB if automatic setup fails:
#   adb -s localhost:5555 shell "sh /setup-solana.sh"
# ============================================================

set -e

SOLANA_VERSION="${SOLANA_VERSION:-1.18.26}"
SOLANA_DIR="/opt/solana"

log() {
  echo "[setup] $(date '+%H:%M:%S') $*"
}

log "Setting up Solana v${SOLANA_VERSION}..."

# Check if already installed
if [ -f "${SOLANA_DIR}/bin/solana" ]; then
  log "Solana already installed at ${SOLANA_DIR}"
  "${SOLANA_DIR}/bin/solana" --version
  exit 0
fi

# Download and install
SOLANA_URL="https://github.com/solana-labs/solana/releases/download/v${SOLANA_VERSION}/solana-release-x86_64-unknown-linux-gnu.tar.bz2"

log "Downloading from ${SOLANA_URL}..."
mkdir -p "${SOLANA_DIR}"

if wget -q "${SOLANA_URL}" -O /tmp/solana.tar.bz2; then
  log "Extracting..."
  tar -xjf /tmp/solana.tar.bz2 -C /tmp/
  mv /tmp/solana-release/* "${SOLANA_DIR}/"
  rm -rf /tmp/solana.tar.bz2 /tmp/solana-release
  
  log "Solana installed. Adding to PATH..."
  export PATH="${SOLANA_DIR}/bin:${PATH}"
  
  # Add to system-wide PATH
  echo "export PATH=${SOLANA_DIR}/bin:\$PATH" >> /etc/profile.d/solana.sh
  
  log "Verifying installation..."
  "${SOLANA_DIR}/bin/solana" --version
  "${SOLANA_DIR}/bin/solana-keygen" --version
  
  log "Setup complete!"
else
  log "ERROR: Failed to download Solana. Check network connectivity."
  exit 1
fi
