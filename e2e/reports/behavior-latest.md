# Behavioral scorecard — project 19, prompt 58

- status: `cancelled` · mode: `build` · wall: 57m06s · rounds: 47
- stream: timestamped · serverlog: yes · plan screens: Onboarding, Home, Habit Library, Focus, Insights, Settings

| Metric | Standard | Measured | Verdict |
|---|---|---|---|
| S1 first write (fresh) | ≤ 3 min | 4m18s | ❌ |
| S2 first route file | ≤ 5 min | 46m05s | ❌ |
| S3 scaffold complete | all 6 plan routes + typecheck | 2/6 plan routes (Onboarding, Home); last check green | ❌ |
| S4 reads before first write | ≤ 12 | 14 | ❌ |
| S5 throughput | ≥ 4 screens/h | 2/6 plan screens in 57m06s = 2.1/h | ❌ |
| S6 runnable while building | last check green, after last write | last green (STALE vs last write; 2/5 clean) | ❓ |
| S7 checklist truth | all shipped marked | 0/18 marked | ❌ |
| S8 verify economy | ≤ 15% of wall | 5 run_checks ≈ 1m35s = 3% of wall | ✅ |
| S9 tool contract | 0 promised missing (≥ 26 advertised) | tools advertised per call: 41 (Run-1: 18 with 8 stripped) | ✅ |
| S10 input economy | prefix stable; tail bounded | system 10399→10399 tok, body→293899 B, rebuilds: 24 | ✅ |
|   S10 rebuild log | attributed | system prompt rebuilt in round 47: project-map Δ250 bytes (41206 → 41456) |  |
| S11 cost accounting | every run priced | ? (pricing unknown — L7 open) | ❓ |
| S12 progress visibility | gap ≤ 90 s | max 514s between events | ❌ |

**Required-row failures: 7** (unmeasured `?` rows also block a pass until measured — §5).

## Tool calls

| tool | calls |
|---|---|
| delete_file | 17 |
| read_file | 16 |
| write_file | 16 |
| edit_file | 15 |
| read_skill | 8 |
| inspect_package | 5 |
| run_checks | 5 |
| grep_codebase | 4 |
| install_package | 3 |
| run_command | 3 |
| run_shell | 2 |
| batch_conflict | 2 |
| context_build | 1 |
| list_files | 1 |
| multi_edit | 1 |
| find_files | 1 |
