#!/usr/bin/env bash
# e2e/run-breaktest.sh — the stack wrapper for the backend break-test.
#
# Boots the backend against a scratch DB and scratch workspaces, points it at
# two local stubs (OAuth provider + model gateway, both started inside the
# driver), runs backend/cmd/breaktest, and leaves the scorecard at
# e2e/reports/breaktest-latest.md (override with BREAKTEST_REPORT).
#
#   ./e2e/run-breaktest.sh          # full run, machine-readable scorecard
#   ./e2e/run-breaktest.sh -v       # also print every SSE event
#
# Failure modes are enumerated in e2e/BREAKTEST.md (before the harness).
# No paid gateway is touched: AI_BASE_URL points at the in-process stub.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND="$ROOT/backend"
DB="$BACKEND/talon-breaktest.db"
WORKSPACES="$BACKEND/workspaces-breaktest"
BASE="http://127.0.0.1:8096"
REPORT="${BREAKTEST_REPORT:-$ROOT/e2e/reports/breaktest-latest.md}"
SRV_BIN="${TMPDIR:-/tmp}/talon-breaktest-server"
SRV_LOG="${TMPDIR:-/tmp}/talon-breaktest-server.log"

die() { echo "breaktest: $*" >&2; exit 2; }

# ── preflight ────────────────────────────────────────────────────────────
command -v go >/dev/null || die "go not on PATH"
command -v curl >/dev/null || die "curl not on PATH"
[ -f "$BACKEND/go.mod" ] || die "no backend/go.mod at $BACKEND"
[ -d "$BACKEND/template" ] || die "template dir missing ($BACKEND/template)"

for port in 8094 8095 8096; do
  if curl -s -m 1 -o /dev/null "http://127.0.0.1:$port/healthz" 2>/dev/null || \
     curl -s -m 1 -o /dev/null "http://127.0.0.1:$port/" 2>/dev/null; then
    die "port $port is already in use — stop the process first (this run never kills unknown servers)"
  fi
done

# ── scratch state: a clean slate every run ───────────────────────────────
rm -f "$DB" "$DB-wal" "$DB-shm" "$BACKEND/talon-breaktest.db" \
      "$BACKEND/talon-breaktest.db-wal" "$BACKEND/talon-breaktest.db-shm"
rm -rf "$WORKSPACES"
mkdir -p "$WORKSPACES" "$(dirname "$REPORT")"

# ── environment ──────────────────────────────────────────────────────────
# godotenv.Load() in main.go never overrides exported keys, so everything
# set here wins over backend/.env (which still fills JWT_SECRET etc.).
export PORT=8096
export DB_PATH="$DB"
export WORKSPACE_DIR="$WORKSPACES"

# Model gateway → the in-process stub (started by the driver).
export AI_PROTOCOL=openai
export AI_BASE_URL="http://127.0.0.1:8094"
export AI_API_KEY="breaktest-key"
export AI_MODEL="breaktest-stub"
export AI_MODEL_CHEAP="breaktest-stub"
export AI_MIN_REQUEST_INTERVAL="0s"
export AI_MODEL_TIER=""
export AI_STREAM_RESTARTS=0

# OAuth → the driver's stub; callback base is the backend itself.
export AUTH_CALLBACK_BASE="$BASE"
export AUTH_GITHUB_CLIENT_ID="talon-breaktest"
export AUTH_GITHUB_CLIENT_SECRET="breaktest-secret"
export AUTH_GITHUB_AUTHORIZE_ENDPOINT="http://127.0.0.1:8095/authorize"
export AUTH_GITHUB_TOKEN_ENDPOINT="http://127.0.0.1:8095/token"
export AUTH_GITHUB_PROFILE_ENDPOINT="http://127.0.0.1:8095/profile"

# ── build + boot the server ──────────────────────────────────────────────
echo "breaktest: building server + driver..."
(cd "$BACKEND" && go build -o "$SRV_BIN" ./cmd/server) || die "server build failed"
(cd "$BACKEND" && go build -o "${SRV_BIN}-driver" ./cmd/breaktest) || die "driver build failed"

cleanup() {
  if [ -n "${SERVER_PID:-}" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null
    echo "breaktest: server stopped"
  fi
}
trap cleanup EXIT INT TERM

echo "breaktest: starting server (log: $SRV_LOG)"
(cd "$BACKEND" && exec "$SRV_BIN") >"$SRV_LOG" 2>&1 &
SERVER_PID=$!

up=""
for _ in $(seq 1 120); do
  if curl -sf -m 2 "$BASE/healthz" >/dev/null 2>&1; then up=1; break; fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "── server log ───────────────────────────────" >&2
    tail -n 40 "$SRV_LOG" >&2
    die "server exited during boot"
  fi
  sleep 0.5
done
[ -n "$up" ] || { tail -n 40 "$SRV_LOG" >&2; die "server not healthy after 60s"; }
echo "breaktest: server up at $BASE"

# ── run the driver ───────────────────────────────────────────────────────
export BREAKTEST_REPORT="$REPORT"
set +e
(cd "$BACKEND" && "${SRV_BIN}-driver" -base "$BASE" -stub-port 8095 -model-port 8094 "${1:-}")
rc=$?
set -e

echo "breaktest: scorecard at $REPORT"
echo "breaktest: server log at $SRV_LOG"
exit "$rc"
