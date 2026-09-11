#!/usr/bin/env bash
# run-live.sh — the live E2E of the entire core product. Real backend, real
# model gateway, real docker redroid device, real template project. No
# smoke, no dummies, no mocks.
#
# Preflight → stack up → driver run → per-stage report → teardown. Every
# failure is a real product bug; every skip names its reason.
#
# Exit codes: 0 all passed (skips allowed), 1 any stage failed or the
# preflight itself failed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND="$ROOT/backend"
WORKSPACES="$BACKEND/workspaces-e2e"
DB="$BACKEND/seekersstudio-e2e.db"
PORT="${E2E_PORT:-8099}"
BASE="http://127.0.0.1:$PORT"
MAESTRO_HOME="$(mktemp -d /tmp/maestro-home.XXXXXX)"
SRV_PID=""
cleanup() {
  if [ -n "$SRV_PID" ]; then
    kill "$SRV_PID" 2>/dev/null || true
    wait "$SRV_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "── preflight"
fail=0
need() { # name, found?
  if [ "$2" = "1" ]; then echo "  ✓ $1"; else echo "  ✗ $1 — MISSING"; fail=1; fi
}
docker info >/dev/null 2>&1 && need "docker daemon" 1 || need "docker daemon" 0
docker image inspect seeker-redroid:latest >/dev/null 2>&1 && need "seeker-redroid image" 1 || need "seeker-redroid image" 0
[ -x "$ROOT/.tools/maestro/bin/maestro" ] && need "maestro (vendored 2.10.0)" 1 || need "maestro" 0
[ -f "$BACKEND/scrcpy-server.jar" ] && need "scrcpy-server.jar (v2.1.1)" 1 || need "scrcpy-server.jar" 0
command -v adb >/dev/null && need "adb" 1 || need "adb" 0
grep -q '^AI_API_KEY=' "$BACKEND/.env" && need "AI gateway key" 1 || need "AI gateway key" 0
[ "$fail" = "0" ] || { echo "preflight failed — fix the missing pieces above"; exit 1; }

echo "── stack"
mkdir -p "$WORKSPACES"
# Resume mode keeps the prior run's scratch DB + workspace (the whole point:
# the project the live model built). Fresh mode wipes both.
if [ "${E2E_RESUME:-0}" != "1" ]; then rm -f "$DB"; fi

# The scratch env: every real dependency, zero shared state. The pacer
# keeps the gateway under its requests/minute budget across the whole run.
set -a
# shellcheck disable=SC1090
source <(grep -E '^(AI_PROTOCOL|AI_BASE_URL|AI_API_KEY|AI_MODEL|AI_MODEL_TIER|JWT_SECRET|PUBLIC_HOST)=' "$BACKEND/.env")
set +a
export PORT="$PORT" DB_PATH="$DB" WORKSPACE_DIR="$WORKSPACES" \
  AI_MIN_REQUEST_INTERVAL="${E2E_AI_INTERVAL:-8s}" \
  SCRCPY_SERVER="$BACKEND/scrcpy-server.jar" \
  MAESTRO_USER_HOME="$MAESTRO_HOME" \
  PATH="$ROOT/.tools/maestro/bin:$PATH" \
  AUTH_GITHUB_CLIENT_ID="${AUTH_GITHUB_CLIENT_ID:-e2e-stub}" \
  AUTH_GITHUB_CLIENT_SECRET="${AUTH_GITHUB_CLIENT_SECRET:-e2e-stub-secret}" \
  AUTH_GITHUB_AUTHORIZE_ENDPOINT="http://127.0.0.1:8098/authorize" \
  AUTH_GITHUB_TOKEN_ENDPOINT="http://127.0.0.1:8098/token" \
  AUTH_GITHUB_PROFILE_ENDPOINT="http://127.0.0.1:8098/profile" \
  AUTH_CALLBACK_BASE="$BASE"
(cd "$BACKEND" && GOCACHE=/tmp/gocache GOPATH=/tmp/gopath go run ./cmd/server) &
SRV_PID=$!

# Wait for readiness — up to 90s (go run compiles first).
for _ in $(seq 1 90); do
  if curl -sf "$BASE/healthz" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -sf "$BASE/healthz" >/dev/null || { echo "backend did not become healthy on :$PORT"; exit 1; }
echo "  ✓ backend on :$PORT (scratch DB, scratch workspaces)"

echo "── drive"
cd "$BACKEND"
if [ "${E2E_RESUME:-0}" = "1" ]; then
  GOCACHE=/tmp/gocache GOPATH=/tmp/gopath go run ./cmd/e2e -base "$BASE" -v -resume
else
  GOCACHE=/tmp/gocache GOPATH=/tmp/gopath go run ./cmd/e2e -base "$BASE" -v
fi
rc=$?

echo "── done (rc=$rc)"
exit $rc
