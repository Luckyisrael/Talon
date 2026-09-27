# Behavioral scorecard — project 18, prompt 56

- status: `cancelled` · mode: `build` · wall: 60m00s · rounds: 38
- stream: none · serverlog: no · plan screens: Onboarding, Home, Habits, Focus, Insights, Settings

| Metric | Standard | Measured | Verdict |
|---|---|---|---|
| S1 first write (fresh) | ≤ 3 min | 28m34s | ❌ |
| S2 first route file | ≤ 5 min | ? | ❌ |
| S3 scaffold complete | all 6 plan routes + typecheck | 2/6 plan routes (Home, Habits); last check green | ❌ |
| S4 reads before first write | ≤ 12 | 22 | ❌ |
| S5 throughput | ≥ 4 screens/h | 2/6 plan screens in 60m00s = 2.0/h | ❌ |
| S6 runnable while building | last check green, after last write | last green (STALE vs last write; 4/7 clean) | ❓ |
| S7 checklist truth | all shipped marked | 0/18 marked | ❌ |
| S8 verify economy | ≤ 15% of wall | 7 run_checks (wall share needs a timestamped stream) | ❓ |
| S9 tool contract | 0 promised missing (≥ 26 advertised) | ? (needs --serverlog) | ❓ |
| S10 input economy | prefix stable; tail bounded | ? (needs --serverlog) | ❓ |
| S11 cost accounting | every run priced | ? (pricing unknown — L7 open) | ❓ |
| S12 progress visibility | gap ≤ 90 s | ? (needs timestamped stream) | ❓ |

**Required-row failures: 6** (unmeasured `?` rows also block a pass until measured — §5).

## Tool calls

| tool | calls |
|---|---|
| write_file | 24 |
| edit_file | 16 |
| read_file | 12 |
| inspect_package | 10 |
| run_checks | 7 |
| grep_codebase | 3 |
| list_files | 1 |
| find_files | 1 |
| multi_edit | 1 |
