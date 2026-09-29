# Behavioral scorecard — project 27, prompt 74

- status: `paused` · mode: `build` · wall: 13m03s · rounds: 60
- stream: timestamped · serverlog: yes · plan screens: Onboarding, Home Dashboard, Habit Library, Focus Timer, Insights, Settings

| Metric | Standard | Measured | Verdict |
|---|---|---|---|
| S1 first write (fresh) | ≤ 3 min | 5s | ✅ |
| S2 first route file | ≤ 5 min | 7m18s | ❌ |
| S3 scaffold complete | all 6 plan routes + typecheck | 3/6 plan routes (Onboarding, Insights, Settings); last check dirty | ❌ |
| S4 reads before first write | ≤ 12 | 0 | ✅ |
| S5 throughput | ≥ 4 screens/h | 3/6 plan screens in 13m03s = 13.8/h | ✅ |
| S6 runnable while building | last check green, after last write | last dirty (covers; 1/6 clean) | ❌ |
| S7 checklist truth | all shipped marked | 6/13 marked | ❌ |
| S8 verify economy | ≤ 15% of wall | 6 run_checks ≈ 2m30s = 19% of wall | ❌ |
| S9 tool contract | 0 promised missing (≥ 26 advertised) | tools advertised per call: 41 (Run-1: 18 with 8 stripped) | ✅ |
| S10 input economy | prefix stable; tail bounded | system 11046→11761 tok, body→249411 B, rebuilds: 21 | ✅ |
|   S10 rebuild log | attributed | system prompt rebuilt in round 58: project-map Δ237 bytes (45736 → 45973) |  |
|   S10 attribution (max body) | layers sum to body | system=46591 tools=23405 payloads=25906 results=60490 history=67879 envelope=25140 (payloads 10%) |  |
| S11 cost accounting | every run priced | ? (pricing unknown — L7 open) | ❓ |
| S12 progress visibility | gap ≤ 90 s | max 46s between real events (top5: 46 22 22 21 20; heartbeats excluded) | ✅ |

**Required-row failures: 5** (unmeasured `?` rows also block a pass until measured — §5).

## Tool calls

| tool | calls |
|---|---|
| write_file | 31 |
| read_file | 26 |
| edit_file | 16 |
| read_skill | 6 |
| run_checks | 6 |
| run_command | 5 |
| node-modules-survey | 3 |
| run_shell | 3 |
| batch_conflict | 3 |
| grep_codebase | 2 |
| inspect_package | 2 |
| run_typecheck | 2 |
| context_build | 1 |
| list_files | 1 |
| shell | 1 |
| run_typecheck 2>&1; echo "EXIT:$?"</arg_value> | 1 |
| run_typecheck 2>&1 | 1 |
| run_typecheck 2>&1 || true | 1 |
