FROM redroid/redroid:14.0.0_64only-latest

LABEL description="SeekerStudio Redroid image with Solana dev stack"

# Solana CLI version
ARG SOLANA_VERSION=1.18.26

# Install basic tools available in the Android container's Linux userspace
USER root

# Copy entrypoint and setup scripts
COPY entrypoint.sh /entrypoint.sh
COPY setup-solana.sh /setup-solana.sh
RUN chmod +x /entrypoint.sh /setup-solana.sh

# The actual Solana tools installation happens at first boot via ADB,
# because Redroid's userspace is Android (not standard Linux).
# The entrypoint handles:
#   1. Waiting for Android boot + ADB connectivity
#   2. Setting device PIN (required for MWA)
#   3. Installing Solana CLI + test-validator
#   4. Installing mock MWA wallet APK (if present at /tmp/mock-mwa-wallet.apk)
#   5. Starting solana-test-validator in background
#   6. Airdropping SOL to the wallet

ENTRYPOINT ["/entrypoint.sh"]
