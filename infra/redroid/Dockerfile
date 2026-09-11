FROM redroid/redroid:14.0.0_64only-latest

LABEL description="Talon Redroid — Android device emulator for Talon app preview"

# Redroid runs Android's /init — no shell, no chmod, no RUN commands.
# All setup happens at runtime via ADB (device PIN, APK installs, etc.)
# No Solana CLI or smart contract tooling — this is a pure Android device.

ENTRYPOINT ["/init"]
