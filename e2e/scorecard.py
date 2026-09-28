#!/usr/bin/env python3
"""Behavioral scorecard (ENGINEERING_STANDARDS.md §5).

Turns one run's artifacts into the SLO table from §2:

  --stream    SSE capture. Raw (`data: {...}`) lines score counts; lines
              prefixed with `HH:MM:SS ` (e2e/timestamp-stream.py) score time.
  --db        backend SQLite DB — prompt row, transcript, plan task status.
  --prompt / --project   the ids under test.
  --serverlog optional server log — ChatWithTools lines give the system= and
              tools= timeline (S9/S10 evidence).
  --out       write markdown here instead of stdout.

An unmeasurable metric scores '?' — this tool never guesses a pass, and a
required row that is '?' fails the run until it is measured.

Usage:
  python3 e2e/scorecard.py --db talon.db --project 19 --prompt 58 \
      --stream /tmp/run2-stream.log --serverlog /tmp/run2-server.log \
      --out e2e/reports/behavior-latest.md
"""
import argparse
import json
import re
import sqlite3
import sys
from collections import Counter
from datetime import datetime
from pathlib import Path

READ_TOOLS = {
    "read_file", "list_files", "grep_codebase", "search_files", "find_files",
    "explore", "codebase_search", "codebase_graph", "inspect_package",
    "read_skill",
    "lsp_diagnostics", "lsp_references", "lsp_definition", "lsp_symbol",
    "metro_status", "capture_screenshot", "tail_logcat",
    # context_build is deliberately absent: it is the system's turn-start
    # scan, not a model choice, and charging it to S4 measures us, not it.
}
WRITE_TOOLS = {"write_file", "edit_file", "multi_edit", "delete_file"}
CHECK_TOOLS = {"run_typecheck", "run_lint", "run_checks", "run_prettier"}
SCREEN_RE = re.compile(r"(^|/)app/[^ ]*\.tsx$")


def parse_stream(path):
    """Returns (raw_events, calls, timestamped, timeline).

    The emitter writes `running` then a terminal event per call — naive
    counting doubles every tool (Run 1's "46 writes" were 23). Calls pair the
    two: `running` opens a slot keyed by (tool, path), the terminal closes it;
    a terminal with no open slot is a standalone call (read_skill emits only
    `completed`). Each call keeps its start and end timestamps.

    timeline is every timestamped frame as (ts, event-type): S12 measures
    gaps over it, never over tool events alone — the tool-only reading
    reported 514 s max for Run 2 while the all-frames truth is 191 s
    (thinking/text were flowing inside that "gap"). `heartbeat` frames are
    excluded at measurement time (FM-L8a): a liveness ping must never hide
    a stalled turn.
    """
    events = []
    timeline = []
    pending_type, pending_ts = None, None
    for raw in Path(path).read_text(errors="replace").splitlines():
        ts, line = None, raw
        m = re.match(r"^(\d{2}:\d{2}:\d{2}(?:\.\d+)?) (.*)$", raw)
        if m:
            ts, line = m.group(1), m.group(2)
        if line.startswith("event: "):
            pending_type, pending_ts = line[7:].strip(), ts
            continue
        if not line.startswith("data: "):
            continue
        try:
            d = json.loads(line[6:])
        except (ValueError, TypeError):
            continue
        if not isinstance(d, dict):
            continue
        frame_ts = ts or pending_ts
        if frame_ts:
            timeline.append((frame_ts, pending_type or "?"))
        pending_type, pending_ts = None, None
        tool = d.get("tool")
        if tool and d.get("status"):
            events.append({
                "ts": ts, "tool": tool, "path": d.get("path") or "",
                "status": d.get("status"), "name": d.get("name") or "",
            })

    calls, open_slots = [], {}
    for e in events:
        key = (e["tool"], e["path"])
        if e["status"] == "running":
            open_slots[key] = open_slots.get(key, 0) + 1
            calls.append({**e, "t_end": e["ts"], "terminal": None})
        else:  # completed | error | warning | rejected
            if open_slots.get(key, 0) > 0:
                open_slots[key] -= 1
                for c in reversed(calls):
                    if c["tool"] == e["tool"] and c["path"] == e["path"] and c["terminal"] is None:
                        c["terminal"] = e["status"]
                        c["t_end"] = e["ts"] or c["t_end"]
                        break
            else:
                calls.append({**e, "t_end": e["ts"], "terminal": e["status"]})
    return events, calls, any(e["ts"] for e in events), timeline


def parse_serverlog(path):
    """(first_system, max_system, max_tools, max_body, rebuilds, max_ledger)
    from ChatWithTools + the L13 request-ledger lines.

    max_tools, not first: the plan turn runs first against the read-only
    registry (smaller defs), and S9's contract is about the build registry.
    max_ledger is the request-ledger line of the largest body — the named
    layer split of the number S10 reports. Runs predating L13 have none.
    """
    first_sys = max_sys = max_tools = max_body = None
    rebuilds = []
    max_ledger = None
    ledger_keys = ("body", "system", "tools", "payloads", "results", "history", "envelope")
    for raw in Path(path).read_text(errors="replace").splitlines():
        m = re.search(r"est_input_tok=(\d+) \(system=(\d+) hist_msgs=\d+ tools=(\d+) body_bytes=(\d+)", raw)
        if m:
            est, sys_t, tools, body = int(m.group(1)), int(m.group(2)), int(m.group(3)), int(m.group(4))
            if first_sys is None:
                first_sys = sys_t
            max_tools = max(max_tools or 0, tools)
            max_sys = max(max_sys or 0, sys_t)
            max_body = max(max_body or 0, body)
        m2 = re.search(r"request-ledger: body=(\d+) system=(\d+) tools=(\d+) payloads=(\d+) "
                       r"results=(\d+) history=(\d+) envelope=(\d+)", raw)
        if m2:
            led = dict(zip(ledger_keys, (int(g) for g in m2.groups())))
            if max_ledger is None or led["body"] > max_ledger["body"]:
                max_ledger = led
        if "system prompt rebuilt in round" in raw:
            rebuilds.append(raw.split("orchestrator: ", 1)[-1])
    return first_sys, max_sys, max_tools, max_body, rebuilds, max_ledger


def transcript(db, project_id, prompt_id):
    """Walk persisted messages: first write-class tool_use (name, seq, ts),
    read-class-before-first-write counts, run_checks result verdicts,
    route (app/**.tsx) writes, round count."""
    con = sqlite3.connect(db)
    rows = con.execute(
        "SELECT seq, role, blocks, created_at FROM conversation_messages "
        "WHERE project_id=? AND prompt_id=? ORDER BY seq",
        (project_id, prompt_id),
    ).fetchall()
    con.close()

    stats = {
        "reads_before_write": 0, "first_write_seq": None, "first_write_ts": None,
        "route_writes": 0, "route_paths": set(), "check_results": [],
        "rounds": 0, "tools": Counter(),
    }
    pending = {}  # tool_use id -> (name, path, ts)
    for seq, role, blocks_json, created_at in rows:
        try:
            blocks = json.loads(blocks_json)
        except (ValueError, TypeError):
            continue
        if role == "assistant":
            stats["rounds"] += 1
            for b in blocks if isinstance(blocks, list) else []:
                if not isinstance(b, dict):
                    continue
                if b.get("type") == "tool_use":
                    name = b.get("name", "")
                    stats["tools"][name] += 1
                    path = (b.get("input") or {}).get("path", "") if isinstance(b.get("input"), dict) else ""
                    pending[b.get("id", "")] = (name, path, created_at)
        elif role == "user":
            for b in blocks if isinstance(blocks, list) else []:
                if not isinstance(b, dict) or b.get("type") != "tool_result":
                    continue
                name, path, ts = pending.pop(b.get("tool_use_id", ""), (None, None, None))
                if name is None:
                    continue
                content = b.get("content") or ""
                if name == "run_checks":
                    stats["check_results"].append(content)
                if name in WRITE_TOOLS and stats["first_write_seq"] is None:
                    stats["first_write_seq"], stats["first_write_ts"] = seq, ts
                if stats["first_write_seq"] is None and name in READ_TOOLS:
                    stats["reads_before_write"] += 1
                if name in WRITE_TOOLS and SCREEN_RE.search(path):
                    stats["route_writes"] += 1
                    stats["route_paths"].add(path)
    return stats


def plan_tasks(db, project_id):
    con = sqlite3.connect(db)
    try:
        rows = con.execute(
            "SELECT status FROM plan_task_status WHERE project_id=?", (project_id,)
        ).fetchall()
    except sqlite3.Error:
        rows = []
    con.close()
    total = len(rows)
    done = sum(1 for (s,) in rows if s in ("done", "completed"))
    return done, total


def plan_screens(db, project_id):
    """Screens from the project's newest settled plan — S3/S5 measure
    shipped PLAN routes, not raw writes (Run 1 wrote 6 app files that were 2
    of 6 planned screens plus system files). Each screen carries the route
    paths its plan text declares (e.g. "app/(tabs)/index.tsx" for "Home") —
    name matching alone false-negatives when the file is index.tsx."""
    con = sqlite3.connect(db)
    try:
        rows = con.execute(
            "SELECT response FROM prompts WHERE project_id=? AND mode='plan' "
            "AND response IS NOT NULL AND response != '' ORDER BY id DESC LIMIT 3",
            (project_id,),
        ).fetchall()
    except sqlite3.Error:
        rows = []
    con.close()
    path_re = re.compile(r"app/[\w./()-]+\.tsx")
    for (resp,) in rows:
        try:
            data = json.loads(resp)
        except (ValueError, TypeError):
            continue
        plan = data.get("plan") if isinstance(data, dict) else None
        if isinstance(plan, dict) and isinstance(plan.get("screens"), list):
            out = []
            for s in plan["screens"]:
                if not isinstance(s, dict) or not s.get("name"):
                    continue
                blob = " ".join(str(s.get(k, "")) for k in ("route", "path", "file", "description"))
                out.append({"name": s["name"], "paths": path_re.findall(blob)})
            return out
    return []


def fmt_t(delta):
    if delta is None:
        return None
    m, s = divmod(int(delta), 60)
    return f"{m}m{s:02d}s" if m else f"{s}s"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", required=True)
    ap.add_argument("--project", type=int, required=True)
    ap.add_argument("--prompt", type=int, required=True)
    ap.add_argument("--stream")
    ap.add_argument("--serverlog")
    ap.add_argument("--fresh", action="store_true",
                    help="fresh project (score S1/S2 against fresh thresholds)")
    ap.add_argument("--out")
    args = ap.parse_args()

    con = sqlite3.connect(args.db)
    row = con.execute(
        "SELECT status, mode, created_at, updated_at FROM prompts WHERE id=?",
        (args.prompt,),
    ).fetchone()
    con.close()
    if not row:
        sys.exit(f"prompt {args.prompt} not found in {args.db}")
    status, mode, created_at, updated_at = row

    events, calls, ts_ok, timeline = (parse_stream(args.stream) if args.stream else ([], [], False, []))
    srv = parse_serverlog(args.serverlog) if args.serverlog else None
    tr = transcript(args.db, args.project, args.prompt)
    done, total = plan_tasks(args.db, args.project)
    screens = plan_screens(args.db, args.project)

    # --- timing (stream preferred, transcript created_at fallback) ----------
    # Everything is seconds-since-midnight: the stream prefixes are time-only
    # and the DB stamps carry a date, and comparing the two raw types silently
    # scores every row against 1900.
    def secs(s):
        if not s:
            return None
        for fmt in ("%H:%M:%S.%f", "%H:%M:%S", "%Y-%m-%d %H:%M:%S"):
            try:
                dt = datetime.strptime(s, fmt)
                return dt.hour * 3600 + dt.minute * 60 + dt.second + dt.microsecond / 1e6
            except (ValueError, TypeError):
                continue
        return None

    start = secs(created_at)

    def span(a, b):
        """b - a in seconds. The turn is same-day, so a small negative is
        capture jitter — a second boundary falling between the writer's
        strftime and time.time() calls (Run 2's log has 6) — and only a
        large one (past ±12 h) is a midnight wrap."""
        if a is None or b is None:
            return None
        d = b - a
        if d >= 0:
            return d
        return d + 86400 if d < -43200 else 0.0

    stream_t0 = next((secs(e["ts"]) for e in events if e["ts"] and secs(e["ts"]) is not None), None) or start

    first_write_delta = None
    for c in calls:
        if c["tool"] in WRITE_TOOLS:
            first_write_delta = span(stream_t0, secs(c.get("t_end") or c.get("ts")))
            break
    if first_write_delta is None and tr["first_write_ts"]:
        first_write_delta = span(start, secs(tr["first_write_ts"]))

    reads_before_write = tr["reads_before_write"]
    if calls:
        sw = 0
        for c in calls:
            if c["tool"] in WRITE_TOOLS:
                break
            if c["tool"] in READ_TOOLS:
                sw += 1
        reads_before_write = sw  # stream wins, even when the true count is 0

    wall = span(start, secs(updated_at))

    # Plan-route coverage: a planned screen ships when a written route file
    # carries its declared path (or, failing a declared path, its name).
    # Run-1's raw 6 app-file writes were 2 of 6 plan screens; Run-2's Home
    # tab is app/(tabs)/index.tsx — name matching alone would miss it.
    # Union of both sources: a cancelled capture loses the stream tail and the
    # transcript loses tool bodies the compaction dropped — take what survived.
    routes = sorted({c["path"] for c in calls
                     if c["tool"] in WRITE_TOOLS and SCREEN_RE.search(c["path"])}
                    | set(tr["route_paths"]))
    shipped = []
    for s in screens:
        by_path = any(r.endswith(p) for p in s["paths"] for r in routes)
        by_name = any(s["name"].lower() in r.lower() for r in routes)
        if by_path or by_name:
            shipped.append(s["name"])

    # --- SLO rows ----------------------------------------------------------
    rows = []

    def add(metric, standard, measured, verdict):
        rows.append((metric, standard, measured, verdict))

    fw = fmt_t(first_write_delta)
    if args.fresh:
        add("S1 first write (fresh)", "≤ 3 min", fw or "?",
            "PASS" if first_write_delta is not None and first_write_delta <= 180 else "FAIL")
    else:
        add("S1b first write (existing)", "≤ 60 s", fw or "?",
            "PASS" if first_write_delta is not None and first_write_delta <= 60 else "FAIL")

    route_first = None
    for c in calls:
        if c["tool"] in WRITE_TOOLS and SCREEN_RE.search(c["path"]):
            route_first = span(stream_t0, secs(c.get("t_end") or c.get("ts")))
            break
    add("S2 first route file", "≤ 5 min", fmt_t(route_first) or "?",
        "PASS" if route_first is not None and route_first <= 300 else "FAIL")

    # A check proves the state only if it ran AFTER the last write (S6): a
    # stale green is not evidence, and neither is a check cut off mid-build.
    last_check = tr["check_results"][-1] if tr["check_results"] else None
    check_green = None if last_check is None else ("error TS" not in last_check)
    last_write_idx = next((i for i in range(len(calls) - 1, -1, -1)
                           if calls[i]["tool"] in WRITE_TOOLS), None)
    last_check_idx = next((i for i in range(len(calls) - 1, -1, -1)
                           if calls[i]["tool"] == "run_checks"), None)
    covers_end = (last_check_idx is not None and last_write_idx is not None
                  and last_check_idx > last_write_idx)

    if screens:
        scaffold_ok = len(shipped) == len(screens)
        add("S3 scaffold complete", f"all {len(screens)} plan routes + typecheck",
            f"{len(shipped)}/{len(screens)} plan routes"
            f" ({', '.join(shipped) or 'none'}); last check "
            f"{'green' if check_green else 'dirty' if check_green is False else '?'}",
            "PASS" if scaffold_ok and check_green and covers_end else "FAIL")
    else:
        add("S3 scaffold complete", "all plan routes + typecheck",
            f"{len(routes)} route files written; last check "
            f"{'green' if check_green else 'dirty' if check_green is False else '?'}",
            "?" if check_green is None else ("PASS" if check_green else "FAIL"))

    add("S4 reads before first write", "≤ 12", str(reads_before_write),
        "PASS" if reads_before_write <= 12 else "FAIL")

    if wall and screens:
        screens_h = len(shipped) / (wall / 3600)
        add("S5 throughput", "≥ 4 screens/h",
            f"{len(shipped)}/{len(screens)} plan screens in {fmt_t(wall)} = {screens_h:.1f}/h",
            "PASS" if screens_h >= 4 else "FAIL")
    elif wall:
        add("S5 throughput", "≥ 4 screens/h",
            f"{len(routes)} route files in {fmt_t(wall)} (no plan to score against)", "?")
    else:
        add("S5 throughput", "≥ 4 screens/h", "?", "?")

    checks = tr["check_results"]
    clean = sum(1 for c in checks if "error TS" not in c)
    s6 = "?" if (check_green is None or not covers_end) else ("PASS" if check_green else "FAIL")
    add("S6 runnable while building", "last check green, after last write",
        f"last {'green' if check_green else 'dirty' if check_green is False else '?'} "
        f"({'covers' if covers_end else 'STALE vs last write'}; {clean}/{len(checks)} clean)",
        s6)

    add("S7 checklist truth", "all shipped marked", f"{done}/{total} marked",
        "PASS" if total and done == total else "FAIL")

    check_calls = [c for c in calls if c["tool"] == "run_checks"]
    if not check_calls and tr["check_results"]:
        # No stream: the transcript proves the runs even without durations.
        check_count = len(tr["check_results"])
    else:
        check_count = len(check_calls)
    if ts_ok and wall and check_calls:
        check_secs = sum(span(secs(c.get("ts")), secs(c.get("t_end"))) or 0
                         for c in check_calls)
        pct = check_secs / wall * 100
        add("S8 verify economy", "≤ 15% of wall",
            f"{len(check_calls)} run_checks ≈ {fmt_t(check_secs)} = {pct:.0f}% of wall",
            "PASS" if pct <= 15 else "FAIL")
    else:
        add("S8 verify economy", "≤ 15% of wall",
            f"{check_count} run_checks (wall share needs a timestamped stream)", "?")

    # S9: Run-1 advertised 18 tools with 8 prompt-promised ones stripped.
    # Post-fix the strip valve only fires above 100K tokens (never on this
    # route), and coreToolSet carries every promised name for the small-
    # context case the unit test guards — so advertised must be ≥ 26.
    if srv and srv[2] is not None:
        add("S9 tool contract", "0 promised missing (≥ 26 advertised)",
            f"tools advertised per call: {srv[2]} (Run-1: 18 with 8 stripped)",
            "PASS" if srv[2] >= 26 else ("FAIL" if srv[2] <= 18 else "?"))
    else:
        add("S9 tool contract", "0 promised missing (≥ 26 advertised)",
            "? (needs --serverlog)", "?")

    if srv and srv[0] is not None:
        add("S10 input economy", "prefix stable; tail bounded",
            f"system {srv[0]}→{srv[1]} tok, body→{srv[3]} B, rebuilds: {len(srv[4])}",
            "PASS" if srv[1] - srv[0] <= 4096 else "FAIL")
        if srv[4]:
            rows.append(("  S10 rebuild log", "attributed", srv[4][-1].strip(), ""))
        if len(srv) > 5 and srv[5]:
            led = srv[5]
            share = 100.0 * led["payloads"] / led["body"] if led["body"] else 0.0
            rows.append(("  S10 attribution (max body)", "layers sum to body",
                         "system={system} tools={tools} payloads={payloads} "
                         "results={results} history={history} envelope={envelope} "
                         "(payloads {share:.0f}%)".format(share=share, **led), ""))
    else:
        add("S10 input economy", "prefix stable; tail bounded", "? (needs --serverlog)", "?")

    add("S11 cost accounting", "every run priced",
        "? (pricing unknown — L7 open)", "?")

    # S12 measures gaps over every real frame (text/thinking/tool/phase/…),
    # never tool events alone — that reading gave Run 2 a false 514 s while
    # the all-frames truth was 191 s — and never over heartbeat frames
    # (FM-L8a: liveness is not progress).
    real_ts = [t for t, k in timeline if k != "heartbeat"]
    if real_ts and ts_ok:
        gaps, last = [], None
        for t in real_ts:
            cur = secs(t)
            if cur is None:
                continue
            if last is not None:
                gaps.append(span(last, cur) or 0)
            last = cur
        if gaps:
            gaps.sort(reverse=True)
            mx = gaps[0]
            top = " ".join(f"{g:.0f}" for g in gaps[:5])
            add("S12 progress visibility", "gap ≤ 90 s",
                f"max {mx:.0f}s between real events (top5: {top}; heartbeats excluded)",
                "PASS" if mx <= 90 else "FAIL")
        else:
            add("S12 progress visibility", "gap ≤ 90 s", "? (single frame)", "?")
    else:
        add("S12 progress visibility", "gap ≤ 90 s", "? (needs timestamped stream)", "?")

    # --- report ------------------------------------------------------------
    counts = Counter(c["tool"] for c in calls) or tr["tools"]
    out = []
    out.append(f"# Behavioral scorecard — project {args.project}, prompt {args.prompt}\n")
    out.append(f"- status: `{status}` · mode: `{mode}` · wall: {fmt_t(wall) or '?'} · rounds: {tr['rounds']}")
    out.append(f"- stream: {'timestamped' if ts_ok else 'counts only' if events else 'none'}"
               f" · serverlog: {'yes' if srv else 'no'}"
               f" · plan screens: "
               f"{', '.join(s['name'] for s in screens) or 'none found'}\n")
    out.append("| Metric | Standard | Measured | Verdict |")
    out.append("|---|---|---|---|")
    fails = 0
    for metric, standard, measured, verdict in rows:
        mark = {"PASS": "✅", "FAIL": "❌", "?": "❓"}.get(verdict, "")
        if verdict == "FAIL":
            fails += 1
        out.append(f"| {metric} | {standard} | {measured} | {mark or verdict} |")
    out.append(f"\n**Required-row failures: {fails}** "
               f"(unmeasured `?` rows also block a pass until measured — §5).\n")
    out.append("## Tool calls\n")
    out.append("| tool | calls |")
    out.append("|---|---|")
    for name, n in counts.most_common():
        out.append(f"| {name} | {n} |")
    report = "\n".join(out) + "\n"

    if args.out:
        Path(args.out).parent.mkdir(parents=True, exist_ok=True)
        Path(args.out).write_text(report)
        print(f"wrote {args.out}")
    else:
        print(report)


if __name__ == "__main__":
    main()
