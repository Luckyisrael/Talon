# Behavioral scorecard — project 23, prompt 66

- status: `completed` · mode: `build` · wall: 42m32s · rounds: 60
- stream: timestamped · serverlog: yes · plan screens: Onboarding, Home, Habits, Focus, Insights, Settings

| Metric | Standard | Measured | Verdict |
|---|---|---|---|
| S1 first write (fresh) | ≤ 3 min | 8m35s | ❌ |
| S2 first route file | ≤ 5 min | 14m54s | ❌ |
| S3 scaffold complete | all 6 plan routes + typecheck | 4/6 plan routes (Habits, Focus, Insights, Settings); last check green | ❌ |
| S4 reads before first write | ≤ 12 | 28 | ❌ |
| S5 throughput | ≥ 4 screens/h | 4/6 plan screens in 42m32s = 5.6/h | ✅ |
| S6 runnable while building | last check green, after last write | last green (covers; 6/10 clean) | ✅ |
| S7 checklist truth | all shipped marked | 4/13 marked | ❌ |
| S8 verify economy | ≤ 15% of wall | 10 run_checks ≈ 3m31s = 8% of wall | ✅ |
| S9 tool contract | 0 promised missing (≥ 26 advertised) | tools advertised per call: 41 (Run-1: 18 with 8 stripped) | ✅ |
| S10 input economy | prefix stable; tail bounded | system 10987→11900 tok, body→252751 B, rebuilds: 29 | ✅ |
|   S10 rebuild log | attributed | system prompt rebuilt in round 56: project-map Δ-893 bytes (44089 → 43196) |  |
|   S10 attribution (max body) | layers sum to body | system=48224 tools=20539 payloads=44112 results=65026 history=57364 envelope=17486 (payloads 17%) |  |
| S11 cost accounting | every run priced | ? (pricing unknown — L7 open) | ❓ |
| S12 progress visibility | gap ≤ 90 s | max 31s between real events (top5: 31 29 27 25 25; heartbeats excluded) | ✅ |

**Required-row failures: 5** (unmeasured `?` rows also block a pass until measured — §5).

## Tool calls

| tool | calls |
|---|---|
| write_file | 29 |
| read_file | 24 |
| edit_file | 14 |
| delete_file | 13 |
| inspect_package | 11 |
| run_checks | 10 |
| read_skill | 8 |
| list_files | 3 |
| context_build | 1 |
| exploration_budget | 1 |
| multi_edit | 1 |
| already-preinstalled | 1 |
| grep_codebase | 1 |
| mark_task_done | 1 |
