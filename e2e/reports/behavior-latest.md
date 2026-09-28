# Behavioral scorecard — project 22, prompt 64

- status: `failed` · mode: `build` · wall: 10m28s · rounds: 8
- stream: timestamped · serverlog: yes · plan screens: Onboarding, Home, Habit Library, Focus, Insights, Settings

| Metric | Standard | Measured | Verdict |
|---|---|---|---|
| S1 first write (fresh) | ≤ 3 min | 5m40s | ❌ |
| S2 first route file | ≤ 5 min | 7m20s | ❌ |
| S3 scaffold complete | all 6 plan routes + typecheck | 1/6 plan routes (Onboarding); last check ? | ❌ |
| S4 reads before first write | ≤ 12 | 13 | ❌ |
| S5 throughput | ≥ 4 screens/h | 1/6 plan screens in 10m28s = 5.7/h | ✅ |
| S6 runnable while building | last check green, after last write | last ? (STALE vs last write; 0/0 clean) | ❓ |
| S7 checklist truth | all shipped marked | 0/14 marked | ❌ |
| S8 verify economy | ≤ 15% of wall | 0 run_checks (wall share needs a timestamped stream) | ❓ |
| S9 tool contract | 0 promised missing (≥ 26 advertised) | tools advertised per call: 41 (Run-1: 18 with 8 stripped) | ✅ |
| S10 input economy | prefix stable; tail bounded | system 10057→10389 tok, body→148495 B, rebuilds: 5 | ✅ |
|   S10 rebuild log | attributed | system prompt rebuilt in round 8: project-map Δ146 bytes (41412 → 41558) |  |
|   S10 attribution (max body) | layers sum to body | system=42007 tools=20539 payloads=24229 results=26135 history=31968 envelope=3617 (payloads 16%) |  |
| S11 cost accounting | every run priced | ? (pricing unknown — L7 open) | ❓ |
| S12 progress visibility | gap ≤ 90 s | max 21s between real events (top5: 21 9 7 5 5; heartbeats excluded) | ✅ |

**Required-row failures: 5** (unmeasured `?` rows also block a pass until measured — §5).

## Tool calls

| tool | calls |
|---|---|
| read_file | 10 |
| write_file | 9 |
| read_skill | 4 |
| context_build | 1 |
