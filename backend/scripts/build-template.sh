#!/usr/bin/env bash
# ============================================================
# SeekerStudio — Build Template Artifact
#
# Pre-bakes node_modules into the template directory so that
# project creation is instant (no npm install at request time).
#
# Usage: bash backend/scripts/build-template.sh
# ============================================================

set -euo pipefail

TEMPLATE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../template" && pwd)"
TEMPLATE_VERSION="${1:-1.0.0}"

echo "=== SeekerStudio: Build Template Artifact ==="
echo "Template dir: $TEMPLATE_DIR"
echo "Version: $TEMPLATE_VERSION"

# Step 1: Install dependencies in template directory
echo ""
echo "--- Installing dependencies ---"
cd "$TEMPLATE_DIR"
npm install --production=false 2>&1 | tail -5

# Step 2: Write version file
echo ""
echo "--- Writing version file ---"
echo "$TEMPLATE_VERSION" > "$TEMPLATE_DIR/.template-version"
echo "Template version $TEMPLATE_VERSION written to $TEMPLATE_DIR/.template-version"

# Step 3: Verify
echo ""
echo "--- Verifying ---"
if [ -d "$TEMPLATE_DIR/node_modules" ]; then
    MODULE_COUNT=$(ls -1 "$TEMPLATE_DIR/node_modules" | wc -l)
    echo "node_modules present: $MODULE_COUNT packages"
else
    echo "ERROR: node_modules not found after install"
    exit 1
fi

if [ -f "$TEMPLATE_DIR/.template-version" ]; then
    echo "Version file: $(cat "$TEMPLATE_DIR/.template-version")"
fi

echo ""
echo "=== Template artifact ready ==="
echo "node_modules is now pre-baked. Project creation will use OverlayFS mounts."
