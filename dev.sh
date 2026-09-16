#!/usr/bin/env bash
# Talon dev launcher — one command, fully env-driven.
#
#   ./dev.sh            # backend + frontend, from .env
#   ./dev.sh backend    # backend only
#   ./dev.sh frontend   # frontend only
#   ./dev.sh stop       # stop both
#
# Everything is read from backend/.env — the same file the Go server loads.
# The frontend's NEXT_PUBLIC_API_URL is DERIVED from the backend's PUBLIC_HOST
# (or PORT), so there is no second file to keep in sync. Update backend/.env,
# run ./dev.sh, and the whole stack comes up on the addresses you configured.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND="$ROOT/backend"
FRONTEND="$ROOT/frontend"
LOGS="$ROOT/.dev-logs"

mkdir -p "$LOGS"

# ── Load the backend's env ────────────────────────────────────────────────
# The Go server loads the same file via godotenv, so this is one source of
# truth for the whole stack.
if [[ -f "$BACKEND/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$BACKEND/.env"
  set +a
fi

PORT="${PORT:-8080}"
PUBLIC_HOST="${PUBLIC_HOST:-localhost}"
FRONTEND_PORT="${FRONTEND_PORT:-3000}"
BACKEND_PORT="$PORT"

# The API origin the browser uses to reach the backend: the backend's own
# PUBLIC_HOST:PORT. This is the exact same knob the frontend reads
# (NEXT_PUBLIC_API_URL), so a value here covers both.
#
# NOTE: WEB_ORIGIN is NOT the API — it is the web app's own address, used
# by the OAuth hand-off and the CORS allowlist. Pointing the API at it makes
# every request hit the frontend and come back empty, which is why it is
# deliberately not used here.
API_URL="${NEXT_PUBLIC_API_URL:-http://$PUBLIC_HOST:$PORT}"

# ── Helpers ───────────────────────────────────────────────────────────────
is_up() { curl -s -o /dev/null -w "%{http_code}" --max-time 2 "http://localhost:$1" 2>/dev/null || true; }

# Stop everything holding a port. `go run ./cmd/server` spawns a CHILD
# server process that a plain kill of the launcher misses — the orphan
# keeps the port bound, and the next start dies with EADDRINUSE. Kill the
# whole process group instead, then sweep the port for survivors.
stop_port() {
  local p="$1"
  local pids
  pids="$(lsof -ti :"$p" 2>/dev/null || true)"
  if [[ -z "$pids" ]]; then return 0; fi
  echo "$pids" | while read -r pid; do
    kill -TERM "-$(ps -o pgid= "$pid" 2>/dev/null | tr -d ' ')" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
  done
  for _ in $(seq 1 20); do
    [[ -z "$(lsof -ti :"$p" 2>/dev/null || true)" ]] && return 0
    sleep 0.5
  done
  # Still alive: force.
  lsof -ti :"$p" 2>/dev/null | while read -r pid; do kill -9 "$pid" 2>/dev/null || true; done
  sleep 1
}

# The pidfile's process group, for a targeted stop of a server we started.
kill_pidfile() {
  local f="$1" pgid
  [[ -f "$f" ]] || return 0
  local pid; pid="$(cat "$f" 2>/dev/null || true)"
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    pgid="$(ps -o pgid= "$pid" 2>/dev/null | tr -d ' ' || true)"
    if [[ -n "$pgid" ]]; then
      kill -TERM "-$pgid" 2>/dev/null || true
    else
      kill -TERM "$pid" 2>/dev/null || true
    fi
    for _ in $(seq 1 15); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.5
    done
    kill -9 "$pid" 2>/dev/null || true
  fi
  rm -f "$f"
}

start_backend() {
  echo "▸ backend  :$BACKEND_PORT  (model: ${AI_MODEL:-unset})"
  cd "$BACKEND"
  # setsid gives the server its own process group so stop can kill the
  # whole tree — `go run` would otherwise leave a compiled child holding
  # the port after a plain kill of the launcher.
  setsid nohup go run ./cmd/server > "$LOGS/backend.log" 2>&1 &
  echo $! > "$LOGS/backend.pid"
  cd "$ROOT"
  for _ in $(seq 1 90); do
    local code; code="$(is_up "$BACKEND_PORT")"
    if [[ "$code" != "000" && -n "$code" ]]; then
      echo "  backend up (HTTP $code)"
      return 0
    fi
    sleep 1
  done
  echo "  ✗ backend did not answer — tail of the log:"
  tail -12 "$LOGS/backend.log" || true
  return 1
}

start_frontend() {
  echo "▸ frontend :$FRONTEND_PORT  → API at $API_URL"
  cd "$FRONTEND"
  setsid env NEXT_PUBLIC_API_URL="$API_URL" PORT="$FRONTEND_PORT" \
    nohup npx next dev -p "$FRONTEND_PORT" > "$LOGS/frontend.log" 2>&1 &
  echo $! > "$LOGS/frontend.pid"
  cd "$ROOT"
  for _ in $(seq 1 120); do
    local code; code="$(is_up "$FRONTEND_PORT")"
    if [[ "$code" == "200" ]]; then
      echo "  frontend up — http://localhost:$FRONTEND_PORT"
      echo "                 http://$PUBLIC_HOST:$FRONTEND_PORT (LAN)"
      return 0
    fi
    sleep 1
  done
  echo "  ✗ frontend did not answer — tail of the log:"
  tail -12 "$LOGS/frontend.log" || true
  return 1
}

# ── Commands ──────────────────────────────────────────────────────────────
case "${1:-all}" in
  stop)
    kill_pidfile "$LOGS/frontend.pid"
    kill_pidfile "$LOGS/backend.pid"
    # Sweep for anything the pidfiles missed (a crash, a manual start).
    stop_port "$FRONTEND_PORT"
    stop_port "$BACKEND_PORT"
    echo "stopped both"
    ;;

  status)
    local_b="$(is_up "$BACKEND_PORT")"
    local_f="$(is_up "$FRONTEND_PORT")"
    echo "backend  :$BACKEND_PORT  ${local_b:-down}"
    echo "frontend :$FRONTEND_PORT ${local_f:-down}"
    ;;

  backend)
    kill_pidfile "$LOGS/backend.pid"; stop_port "$BACKEND_PORT"
    start_backend
    ;;

  frontend)
    kill_pidfile "$LOGS/frontend.pid"; stop_port "$FRONTEND_PORT"
    start_frontend
    ;;

  all|"")
    kill_pidfile "$LOGS/frontend.pid"
    kill_pidfile "$LOGS/backend.pid"
    stop_port "$FRONTEND_PORT"
    stop_port "$BACKEND_PORT"
    start_backend
    start_frontend
    echo ""
    echo "Talon is up:"
    echo "  web     http://localhost:$FRONTEND_PORT"
    echo "  web LAN http://$PUBLIC_HOST:$FRONTEND_PORT"
    echo "  api     http://localhost:$BACKEND_PORT"
    echo "  model   ${AI_MODEL:-unset} via ${AI_BASE_URL:-unset}"
    echo "  logs    $LOGS/{backend,frontend}.log"
    ;;

  *)
    echo "usage: ./dev.sh [all|backend|frontend|stop|status]"
    exit 1
    ;;
esac
