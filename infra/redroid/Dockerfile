FROM redroid/redroid:14.0.0_64only-latest

LABEL description="SeekerStudio Redroid image with Solana dev stack"

# Redroid runs Android's /init — no shell, no chmod, no RUN commands.
# Setup happens at runtime via ADB after container boot.
# Copy setup scripts into the image for access via docker exec / ADB.
COPY entrypoint.sh /tmp/entrypoint.sh
COPY setup-solana.sh /tmp/setup-solana.sh

# Default: boot Android normally. Setup runs externally.
ENTRYPOINT ["/init"]
