# Behavioral scorecard — project 25, prompt 70

- status: `completed` · mode: `build` · wall: 18m23s · rounds: 58
- stream: timestamped · serverlog: yes · plan screens: Welcome, Home, Habits, Focus, Insights, Settings

| Metric | Standard | Measured | Verdict |
|---|---|---|---|
| S1 first write (fresh) | ≤ 3 min | 2m42s | ✅ |
| S2 first route file | ≤ 5 min | 4m17s | ✅ |
| S3 scaffold complete | all 6 plan routes + typecheck | 5/6 plan routes (Welcome, Habits, Focus, Insights, Settings); last check green | ❌ |
| S4 reads before first write | ≤ 12 | 17 | ❌ |
| S5 throughput | ≥ 4 screens/h | 5/6 plan screens in 18m23s = 16.3/h | ✅ |
| S6 runnable while building | last check green, after last write | last green (covers; 9/13 clean) | ✅ |
| S7 checklist truth | all shipped marked | 15/15 marked | ✅ |
| S8 verify economy | ≤ 15% of wall | 13 run_checks ≈ 6m37s = 36% of wall | ❌ |
| S9 tool contract | 0 promised missing (≥ 26 advertised) | tools advertised per call: 41 (Run-1: 18 with 8 stripped) | ✅ |
| S10 input economy | prefix stable; tail bounded | system 12070→12641 tok, body→234748 B, rebuilds: 28 | ✅ |
|   S10 rebuild log | attributed | system prompt rebuilt in round 54: project-map Δ-48 bytes (41767 → 41719) |  |
|   S10 attribution (max body) | layers sum to body | system=47479 tools=22216 payloads=20608 results=89165 history=36909 envelope=18371 (payloads 9%) |  |
| S11 cost accounting | every run priced | ? (pricing unknown — L7 open) | ❓ |
| S12 progress visibility | gap ≤ 90 s | max 133s between real events (top5: 133 40 39 37 36; heartbeats excluded) | ❌ |

**Required-row failures: 4** (unmeasured `?` rows also block a pass until measured — §5).

## Tool calls

| tool | calls |
|---|---|
| write_file | 37 |
| read_file | 29 |
| delete_file | 18 |
| run_checks | 13 |
| multi_edit | 10 |
| inspect_package | 9 |
| git_commit | 8 |
| read_skill | 7 |
| edit_file | 4 |
| grep_codebase | 4 |
| mark_task_done | 2 |
| compact | 2 |
| context_build | 1 |
| node-modules-survey | 1 |
| read-before-write | 1 |
| batch_conflict | 1 |
| run_prettier | 1 |
| git_status | 1 |
