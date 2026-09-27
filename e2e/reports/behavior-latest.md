# Behavioral scorecard — project 20, prompt 60

- status: `paused` · mode: `build` · wall: 48m03s · rounds: 60
- stream: timestamped · serverlog: yes · plan screens: Onboarding, Home, Habits, Focus, Insights, Settings

| Metric | Standard | Measured | Verdict |
|---|---|---|---|
| S1 first write (fresh) | ≤ 3 min | 11m49s | ❌ |
| S2 first route file | ≤ 5 min | 13m59s | ❌ |
| S3 scaffold complete | all 6 plan routes + typecheck | 6/6 plan routes (Onboarding, Home, Habits, Focus, Insights, Settings); last check dirty | ❌ |
| S4 reads before first write | ≤ 12 | 19 | ❌ |
| S5 throughput | ≥ 4 screens/h | 6/6 plan screens in 48m03s = 7.5/h | ✅ |
| S6 runnable while building | last check green, after last write | last dirty (covers; 3/11 clean) | ❌ |
| S7 checklist truth | all shipped marked | 0/17 marked | ❌ |
| S8 verify economy | ≤ 15% of wall | 11 run_checks ≈ 4m26s = 9% of wall | ✅ |
| S9 tool contract | 0 promised missing (≥ 26 advertised) | tools advertised per call: 41 (Run-1: 18 with 8 stripped) | ✅ |
| S10 input economy | prefix stable; tail bounded | system 10607→11603 tok, body→254524 B, rebuilds: 20 | ✅ |
|   S10 rebuild log | attributed | system prompt rebuilt in round 59: project-map Δ0 bytes (46415 → 46415) |  |
| S11 cost accounting | every run priced | ? (pricing unknown — L7 open) | ❓ |
| S12 progress visibility | gap ≤ 90 s | max 183s between real events (top5: 183 48 43 36 24; heartbeats excluded) | ❌ |

**Required-row failures: 7** (unmeasured `?` rows also block a pass until measured — §5).

## Tool calls

| tool | calls |
|---|---|
| read_file | 29 |
| write_file | 24 |
| run_checks | 11 |
| run_shell | 10 |
| read_skill | 7 |
| edit_file | 7 |
| multi_edit | 6 |
| grep_codebase | 4 |
| install_package | 3 |
| inspect_package | 2 |
| node-modules-survey | 2 |
| context_build | 1 |
| explore | 1 |
| route_scaffold | 1 |
| batch_conflict | 1 |
| compact | 1 |
