#!/usr/bin/env python3
"""Drive one plan + build cycle against a live Talon stack, capturing
timestamped SSE streams for e2e/scorecard.py.

  python3 e2e/drive.py --base http://127.0.0.1:8097 --name Momentum \\
      --plan @/tmp/opencode/p55.txt --build @/tmp/opencode/p56.txt \\
      --out /tmp/opencode/run2 --db backend/talon.db

The login mirrors cmd/e2e's account stage (start → stub consent → callback
→ exchange). Each turn's raw SSE goes to <out>-plan.log / <out>-build.log
with HH:MM:SS.mmm prefixes, which is exactly what scorecard.py parses. A
mid-turn question is the product's ask_user loop: we answer it (permissively
— the run already granted workspace and device) and keep reading.
"""
import argparse
import json
import sqlite3
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


OPENER = urllib.request.build_opener(_NoRedirect)
DEFAULT_ANSWER = ("Yes, proceed — this E2E run approves workspace writes "
                  "and device probes.")


def _req(method, url, body=None, headers=None, timeout=30):
    """Returns (status, headers-dict, body-bytes); 3xx comes back as a normal
    return because the opener does not follow redirects."""
    data = json.dumps(body).encode() if body is not None else None
    hdrs = {"Content-Type": "application/json"} if body is not None else {}
    hdrs.update(headers or {})
    req = urllib.request.Request(url, data=data, method=method, headers=hdrs)
    try:
        with OPENER.open(req, timeout=timeout) as r:
            return r.status, dict(r.headers), r.read()
    except urllib.error.HTTPError as e:
        if 300 <= e.code < 400:
            return e.code, dict(e.headers), b""
        raw = e.read()[:400]
        raise RuntimeError(f"{method} {url} -> HTTP {e.code}: {raw!r}") from None


def login(base):
    """OAuth dance against the stub provider → bearer token."""
    _, hdrs, _ = _req("GET", base + "/v1/auth/github/start?redirect=/auth/callback")
    consent = hdrs.get("Location") or hdrs.get("location")
    if not consent:
        raise RuntimeError("start did not redirect — is the stub on :8098 "
                           "and AUTH_GITHUB_* pointed at it?")
    _, hdrs, _ = _req("GET", consent)
    cb = hdrs.get("Location") or hdrs.get("location")
    if not cb:
        raise RuntimeError("stub consent did not redirect to the callback")
    _, hdrs, _ = _req("GET", cb)
    final = hdrs.get("Location") or hdrs.get("location") or ""
    if "code=" not in final:
        raise RuntimeError(f"callback landed on {final[:160]} without a code")
    code = final.split("code=", 1)[1].split("&", 1)[0]
    _, _, raw = _req("POST", base + "/v1/auth/exchange", {"code": code})
    token = json.loads(raw).get("token")
    if not token:
        raise RuntimeError(f"exchange returned no token: {raw[:200]!r}")
    return token


def last_prompt_id(db, project_id, mode):
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    try:
        row = con.execute(
            "SELECT MAX(id) FROM prompts WHERE project_id=? AND mode=?",
            (project_id, mode)).fetchone()
        return int(row[0]) if row and row[0] else None
    finally:
        con.close()


def stream_turn(base, token, path, body, out_f, deadline_at, answer_state):
    """One SSE request appended to out_f. Returns dict done-payload or None
    (stream ended without done), and updates answer_state."""
    data = json.dumps(body).encode()
    req = urllib.request.Request(
        base + path, data=data, method="POST",
        headers={"Content-Type": "application/json",
                 "Accept": "text/event-stream",
                 "Authorization": "Bearer " + token})
    # Socket timeout = remaining deadline + grace: a long idle gap between
    # model rounds must not be mistaken for a dead stream, but a turn that
    # never ends must not hang the driver either.
    resp = OPENER.open(req, timeout=max(60, deadline_at - time.time() + 120))
    done_payload, kind = None, ""
    try:
        for raw in resp:
            line = raw.decode(errors="replace").rstrip("\r\n")
            out_f.write(time.strftime("%H:%M:%S.") +
                        f"{int(time.time() * 1000) % 1000:03d} {line}\n")
            out_f.flush()
            if line.startswith("event:"):
                kind = line[6:].strip()
            elif line.startswith("data:"):
                payload = line[5:].strip()
                try:
                    d = json.loads(payload)
                except ValueError:
                    d = None
                if kind == "done" and isinstance(d, dict):
                    done_payload = d
                if (kind == "question" and isinstance(d, dict)
                        and d.get("prompt_id")):
                    answer_state["question_prompt"] = int(d["prompt_id"])
                kind = ""
            if time.time() > deadline_at:
                break
    finally:
        try:
            resp.close()
        except Exception:
            pass
    return done_payload


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base", default="http://127.0.0.1:8097")
    ap.add_argument("--name", default="Momentum")
    ap.add_argument("--plan", required=True, help="text or @file")
    ap.add_argument("--build", required=True, help="text or @file")
    ap.add_argument("--out", required=True, help="log prefix (writes -plan.log/-build.log)")
    ap.add_argument("--db", default="backend/talon.db")
    ap.add_argument("--plan-timeout", type=int, default=1200)
    ap.add_argument("--build-timeout", type=int, default=4500)
    ap.add_argument("--answer", default=DEFAULT_ANSWER)
    args = ap.parse_args()

    def expand(s):
        return Path(s[1:]).read_text() if s.startswith("@") else s

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)

    print(f"[drive] login {args.base} …", flush=True)
    token = login(args.base)
    print("[drive] login ok", flush=True)

    _, _, raw = _req("POST", args.base + "/v1/projects", {"name": args.name},
                     headers={"Authorization": "Bearer " + token})
    project_id = json.loads(raw)["id"]
    print(f"[drive] project {project_id} ({args.name})", flush=True)

    results = {}
    for mode in ("plan", "build"):
        prompt = expand(args.plan if mode == "plan" else args.build)
        timeout = args.plan_timeout if mode == "plan" else args.build_timeout
        log = open(str(out) + f"-{mode}.log", "w", buffering=1)
        print(f"[drive] {mode} turn start (timeout {timeout}s)", flush=True)
        status, answered = None, 0
        deadline_at = time.time() + timeout
        state = {}
        done = stream_turn(
            args.base, token, "/v1/prompts/stream",
            {"project_id": project_id, "mode": mode, "prompt": prompt},
            log, deadline_at, state)
        # ask_user loop: a waiting turn resumes on the answer endpoint and
        # may pause again — follow until it stops asking (max 4, like
        # cmd/e2e: a model stuck asking is a failure, not a hang).
        while done is not None and done.get("status") == "waiting":
            qid = state.get("question_prompt") or last_prompt_id(
                args.db, project_id, mode)
            answered += 1
            if answered > 4:
                status = "stuck-asking"
                break
            print(f"[drive] {mode}: question on prompt {qid}, answering "
                  f"(round {answered})", flush=True)
            done = stream_turn(
                args.base, token, f"/v1/prompts/{qid}/answer",
                {"answer": args.answer}, log, deadline_at, state)
        if status is None:
            status = (done.get("status") if done else
                      "stream-ended-without-done")
        log.close()
        pid = last_prompt_id(args.db, project_id, mode)
        results[mode] = {"prompt_id": pid, "status": status,
                         "questions_answered": answered}
        print(f"[drive] {mode} turn end: {status} (prompt {pid}, "
              f"{answered} answered)", flush=True)
        if status in ("failed", "stuck-asking", "stream-ended-without-done",
                      "answer-stream-ended-without-done"):
            break

    print("[drive] RESULT " + json.dumps(
        {"project": project_id, **results}, indent=2), flush=True)


if __name__ == "__main__":
    main()
