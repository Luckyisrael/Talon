# Baseline benchmark — perf plan Phase 1

Captured 2026-09-05 via `make bench` (`-benchtime=1x`, single samples).
Workspace: `backend/workspaces/27` (SolMate, 13 screens+lib files).
Machine: 8-CPU Linux host. Re-run after each optimization; every item in the
plan must move one of these numbers.

| Benchmark | Baseline | Notes |
|---|---|---|
| Typecheck (`tsc --noEmit`, ws27) | **2.29 s/op** | Full project, no incremental. Target of `incremental: true` + `tsc --build`. |
| LintWalk (`walkSource`, ws27) | **0.30 ms/op** | File walk itself is negligible — if lint is slow, the cost is eslint, not the walk. Deprioritizes plan #11. |
| CopyTree (template `app/`) | **3.7 ms/op** | Small tree; hardlinking (#12) only matters for large workspaces — measure on ws27+node_modules before doing it. |
| Analyze (1080×2400 PNG) | **115 ms/op** | Slower than the "low ms" comment claims (`img.At` dispatch per sample). Runs before every vision call; direct-`Pix` access is the fix if it ever matters. |

Not yet measured (need live infra): session-provision wall time (~3.2 min
observed once: ~40s boot + serial installs), `assembleDebug` cold/warm,
Metro bundle cold/warm. Those require server + Redroid up; capture when the
device lane reopens.
