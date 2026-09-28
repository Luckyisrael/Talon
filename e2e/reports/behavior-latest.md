# Behavioral scorecard — project 24, prompt 68

- status: `paused` · mode: `build` · wall: 14m37s · rounds: 60
- stream: timestamped · serverlog: yes · plan screens: none found

| Metric | Standard | Measured | Verdict |
|---|---|---|---|
| S1 first write (fresh) | ≤ 3 min | 2m21s | ✅ |
| S2 first route file | ≤ 5 min | 3m52s | ✅ |
| S3 scaffold complete | all plan routes + typecheck | 9 route files written; last check green | ✅ |
| S4 reads before first write | ≤ 12 | 14 | ❌ |
| S5 throughput | ≥ 4 screens/h | 9 route files in 14m37s (no plan to score against) | ❓ |
| S6 runnable while building | last check green, after last write | last green (covers; 8/11 clean) | ✅ |
| S7 checklist truth | all shipped marked | 0/0 marked | ❌ |
| S8 verify economy | ≤ 15% of wall | 11 run_checks ≈ 4m37s = 32% of wall | ❌ |
| S9 tool contract | 0 promised missing (≥ 26 advertised) | ? (needs --serverlog) | ❓ |
| S10 input economy | prefix stable; tail bounded | ? (needs --serverlog) | ❓ |
| S11 cost accounting | every run priced | ? (pricing unknown — L7 open) | ❓ |
| S12 progress visibility | gap ≤ 90 s | max 64s between real events (top5: 64 59 54 33 31; heartbeats excluded) | ✅ |

**Required-row failures: 3** (unmeasured `?` rows also block a pass until measured — §5).

## Tool calls

| tool | calls |
|---|---|
| read_file | 34 |
| write_file | 33 |
| edit_file | 21 |
| delete_file | 21 |
| run_checks | 11 |
| run_command | 7 |
| read_skill | 6 |
| run_shell | 4 |
| grep_codebase | 4 |
| git_status | 3 |
| git_commit | 3 |
| inspect_package | 2 |
| batch_conflict | 2 |
| context_build | 1 |
| node-modules-survey | 1 |
| multi_edit | 1 |
| compact | 1 |
| run_prettier | 1 |
| run_bundle | 1 |
| git_log | 1 |
| mark_task_done | 1 |
