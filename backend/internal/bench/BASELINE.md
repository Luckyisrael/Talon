# Baseline benchmark — perf plan Phase 1

Captured 2026-09-05 via `make bench` (`-benchtime=1x`, single samples).
Workspace: `backend/workspaces/27` (SolMate, 13 screens+lib files).
Machine: 8-CPU Linux host. Re-run after each optimization; every item in the
plan must move one of these numbers.

| Benchmark | Baseline | Notes |
|---|---|---|
| Typecheck (`tsc --noEmit`, ws27) | **2.29 s/op** (hot cache; ~9 s cold) | Full project, parse-bound on the Expo SDK graph. `tsc --build` measured **no faster** (9.0 s vs 9.2 s warm) — buildinfo doesn't skip on this workload, so verify stays on `--noEmit`. Template keeps `incremental`+`noEmit` for future adoption. Baseline is cache-sensitive: compare runs back-to-back. |

## Kit template re-baseline (Phase 3 migration, SEEKER_BENCH_WS=template)

New template (kit v6, 600 packages) vs old (web3.js, ws27 workspaces):

| Benchmark | Old template | Kit template | Delta |
|---|---|---|---|
| Typecheck | 2.29 s hot / ~9 s cold | **36.5 s** | **16x regression** — kit's declaration graph dominates despite `skipLibCheck:true`. Every verify turn pays this. Biggest perf cost of the migration; no mitigation yet (program is parse-bound, `--build` won't help — same finding as above, larger). |
| LintWalk | 0.30 ms | 8.9 ms | Bigger tree, still negligible. #11 stays deprioritized. |
| CopyTree | 3.7 ms | 7.0 ms | Fine. |
| Analyze | 115 ms | 196 ms | Machine variance + larger binary; same code. |
| LintWalk (`walkSource`, ws27) | **0.30 ms/op** | File walk itself is negligible — if lint is slow, the cost is eslint, not the walk. Deprioritizes plan #11. |
| CopyTree (template `app/`) | **3.7 ms/op** | Small tree; hardlinking (#12) only matters for large workspaces — measure on ws27+node_modules before doing it. |
| Analyze (1080×2400 PNG) | **115 ms/op** | Slower than the "low ms" comment claims (`img.At` dispatch per sample). Runs before every vision call; direct-`Pix` access is the fix if it ever matters. |

Not yet measured (need live infra): session-provision wall time (~3.2 min
observed once: ~40s boot + serial installs), `assembleDebug` cold/warm,
Metro bundle cold/warm. Those require server + Redroid up; capture when the
device lane reopens.

## Measured live 2026-09-06 (session 17, ws27 SolMate)

- **Provision: 3.5 min** fire→ready (container ~7s, boot ~2m13s, installs
  overlapping 12–47s, PIN+verify+smoke ~25s). P4 parallel installs verified
  overlapping (demodapp 12s ∥ wallet 47s ∥ dev-client, saving ~25s vs
  serial). **Boot is ~65% of the pipeline** — the warm-pool case.
- **assembleDebug flagged: 12m53s**, BUILD SUCCESSFUL, 783/828 tasks
  up-to-date (daemon+cache engaged). Prior unflagged run was killed at
  ~10 min without finishing — not a clean A/B, but caching demonstrably
  works now.
- **Boot verified on device**: SolMate (com.seekerstudio.e2echeck)
  installed, `am start` → PID alive, app-code log lines present
  (G5 seen-boot + first-launch proof). Note: `monkey -p` exited -5 without
  launching despite a valid LAUNCHER activity — prefer `am start` in
  run_boot evidence paths.
- **Sessions boot-wait (90s) expired** while actual boot took ~2m13s;
  wallet's internal wait covered it. Consider unifying/raising the timeout.
