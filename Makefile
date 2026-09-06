# SeekerStudio merge gates. `make evals` is the only merge path: the full
# unit suite plus the phase-guard eval set (one case per PLAN.md phase,
# healthy and fault-injected polarities). `make ci` mirrors the CI workflow.

.PHONY: evals ci vet test bench

evals: vet test

vet:
	cd backend && go vet ./...

test:
	cd backend && go test -count=1 ./...

# Baseline benchmarks (perf plan Phase 1). -benchtime=1x: single samples of
# slow gates (typecheck, gradle-adjacent copy) — this is measurement, not
# tuning. SEEKER_BENCH_WS overrides the workspace (default: workspaces/27).
bench:
	cd backend && go test -run='^$$' -bench=. -benchtime=1x ./internal/tools/ ./internal/release/ ./internal/verify/

ci: evals
