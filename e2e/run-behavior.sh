#!/usr/bin/env bash
# run-behavior.sh — the behavior-gauntlet recipe: real backend on :8097
# against the OAuth stub on :8098, one plan + one build turn driven by
# e2e/drive.py, scored into e2e/reports/behavior-latest.md. Runs 1–3
# (ENGINEERING_STANDARDS Appendix B) ran exactly this.
#
#   e2e/run-behavior.sh <run-label> <plan-file> <build-file>
#
# Example (the Run-2/3 task, committed under e2e/prompts/):
#   e2e/run-behavior.sh run3 e2e/prompts/momentum-plan.txt \
#                            e2e/prompts/momentum-build.txt
#
# Logs land in ${BEHAVIOR_LOGS:-/tmp/opencode}/<label>-*.log. Exits with
# the scorecard's code. The stack is torn down on exit (trap); the DB
# (backend/talon.db) keeps every run, so any run can be re-scored later
# with e2e/scorecard.py alone.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LABEL="${1:?usage: run-behavior.sh <run-label> <plan-file> <build-file>}"
PLAN="${2:?plan prompt file missing}"
BUILD="${3:?build prompt file missing}"
LOGS="${BEHAVIOR_LOGS:-/tmp/opencode}"
OUT="$LOGS/$LABEL"
cd "$ROOT" || exit 1

STUB_PID=""
SRV_PID=""
cleanup() {
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null
  [ -n "$STUB_PID" ] && kill "$STUB_PID" 2>/dev/null
  pkill -f "stub-oauth[.]py" 2>/dev/null
  pkill -f "talon-server" 2>/dev/null
  wait 2>/dev/null
}
trap cleanup EXIT

mkdir -p "$LOGS"
rm -f "$OUT"-*.log "$LOGS/talon-server"

# 1. OAuth stub — the consent screen approves instantly, so the run
#    measures the studio's machinery, not GitHub's.
python3 e2e/stub-oauth.py > "$OUT-stub.log" 2>&1 &
STUB_PID=$!

# 2. backend — built first so SRV_PID is the real process (go run would
#    orphan the child on kill).
cd backend || exit 1
if ! go build -o "$LOGS/talon-server" ./cmd/server; then
  echo "SERVER BUILD FAILED"
  exit 1
fi
PORT=8097 \
AUTH_GITHUB_CLIENT_ID=e2e-stub \
AUTH_GITHUB_CLIENT_SECRET=e2e-stub-secret \
AUTH_GITHUB_AUTHORIZE_ENDPOINT=http://127.0.0.1:8098/authorize \
AUTH_GITHUB_TOKEN_ENDPOINT=http://127.0.0.1:8098/token \
AUTH_GITHUB_PROFILE_ENDPOINT=http://127.0.0.1:8098/profile \
AUTH_CALLBACK_BASE=http://127.0.0.1:8097 \
  "$LOGS/talon-server" > "$OUT-server.log" 2>&1 &
SRV_PID=$!
cd "$ROOT" || exit 1

up=0
for _ in $(seq 1 90); do
  if curl -sf http://127.0.0.1:8097/healthz > /dev/null 2>&1; then up=1; break; fi
  sleep 2
done
if [ "$up" != "1" ]; then
  echo "SERVER NOT UP"
  tail -30 "$OUT-server.log"
  exit 1
fi
echo "[$LABEL] stack up (stub pid $STUB_PID, server pid $SRV_PID)"

# 3. drive the turns
python3 e2e/drive.py --plan "@$PLAN" --build "@$BUILD" \
  --out "$OUT" 2>&1 | tee "$OUT-drive.log"
DRIVE_RC=${PIPESTATUS[0]}
echo "[$LABEL] drive rc=$DRIVE_RC"

# 4. extract ids (RESULT always prints on the success path; tolerate a
#    partial run so a failed turn still gets scored honestly)
PROJ="" PLANID="" BPID=""
eval "$(python3 - "$OUT-drive.log" <<'PY'
import json, sys
try:
    txt = open(sys.argv[1], errors='replace').read()
except OSError:
    txt = ""
i = txt.rfind('[drive] RESULT ')
if i < 0:
    print('PROJ=""'); print('PLANID=""'); print('BPID=""')
else:
    blob = txt[i + len('[drive] RESULT '):]
    j = json.loads(blob[blob.find('{'):])
    print(f'PROJ={j.get("project", "")}')
    print(f'PLANID={j.get("plan", {}).get("prompt_id", "")}')
    print(f'BPID={j.get("build", {}).get("prompt_id", "")}')
PY
)"
echo "[$LABEL] project=$PROJ plan=$PLANID build=$BPID"

if [ -z "$PROJ" ] || [ -z "$BPID" ]; then
  echo "[$LABEL] no build prompt to score — drive failed before the build turn"
  exit "${DRIVE_RC:-1}"
fi

# 5. score — unmeasured rows score '?' and block the run by design.
python3 e2e/scorecard.py --db backend/talon.db --project "$PROJ" --prompt "$BPID" \
  --stream "$OUT-build.log" --serverlog "$OUT-server.log" \
  --fresh --out e2e/reports/behavior-latest.md
RC=$?
echo "[$LABEL] scorecard rc=$RC"
echo "── report ──"
cat e2e/reports/behavior-latest.md
exit $RC
