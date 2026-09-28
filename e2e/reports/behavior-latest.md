# Behavioral scorecard — project 21, prompt 62

- status: `paused` · mode: `build` · wall: 65m53s · rounds: 60
- stream: timestamped · serverlog: yes · plan screens: Onboarding, Home, Habit Library, Focus Timer, Insights, Settings

| Metric | Standard | Measured | Verdict |
|---|---|---|---|
| S1 first write (fresh) | ≤ 3 min | 8m21s | ❌ |
| S2 first route file | ≤ 5 min | 18m08s | ❌ |
| S3 scaffold complete | all 6 plan routes + typecheck | 2/6 plan routes (Insights, Settings); last check dirty | ❌ |
| S4 reads before first write | ≤ 12 | 17 | ❌ |
| S5 throughput | ≥ 4 screens/h | 2/6 plan screens in 65m53s = 1.8/h | ❌ |
| S6 runnable while building | last check green, after last write | last dirty (covers; 4/12 clean) | ❌ |
| S7 checklist truth | all shipped marked | 0/16 marked | ❌ |
| S8 verify economy | ≤ 15% of wall | 12 run_checks ≈ 4m39s = 7% of wall | ✅ |
| S9 tool contract | 0 promised missing (≥ 26 advertised) | tools advertised per call: 41 (Run-1: 18 with 8 stripped) | ✅ |
| S10 input economy | prefix stable; tail bounded | system 10803→11773 tok, body→310816 B, rebuilds: 31 | ✅ |
|   S10 rebuild log | attributed | system prompt rebuilt in round 58: project-map Δ0 bytes (47095 → 47095) |  |
| S11 cost accounting | every run priced | ? (pricing unknown — L7 open) | ❓ |
| S12 progress visibility | gap ≤ 90 s | max 136s between real events (top5: 136 80 77 36 35; heartbeats excluded) | ❌ |

**Required-row failures: 8** (unmeasured `?` rows also block a pass until measured — §5).

## Tool calls

| tool | calls |
|---|---|
| edit_file | 42 |
| read_file | 24 |
| write_file | 22 |
| run_checks | 12 |
| batch_conflict | 9 |
| read_skill | 7 |
| grep_codebase | 7 |
| context_build | 1 |
| explore | 1 |
| route_scaffold | 1 |
| inspect_package | 1 |
| compact | 1 |
