# Talon merge gates. `make evals` is the only merge path: the full
# unit suite plus the phase-guard eval set (one case per PLAN.md phase,
# healthy and fault-injected polarities). `make ci` mirrors the CI workflow.

.PHONY: evals ci vet test scorecard golden bench nightly e2e-live

evals: vet scorecard golden test

vet:
	cd backend && go vet ./...

# W6.3: the grader's fixture cases gate merges as their own named stage.
# Grade() is pure scoring code — nothing in the suite outside internal/evals
# would catch a broken disqualifier, so a green build here means the
# yardstick still measures.
scorecard:
	cd backend && go test -count=1 -run 'TestFixtureScorecard|TestGrade' ./internal/evals/

# W13: the recorded-provider replay suite is its own named stage — the
# agent-behavior gate. Prompt/tool/guardrail edits red it until the golden
# transcripts are re-recorded (TALONSTUDIO_UPDATE_GOLDEN=1) and the diff is
# reviewed; the rounds budgets in the fixtures are the rounds-to-green
# regression threshold that blocks the offending merge.
golden:
	cd backend && go test -count=1 ./internal/evals/golden/

test:
	cd backend && go test -count=1 ./...

# Baseline benchmarks (perf plan Phase 1). -benchtime=1x: single samples of
# slow gates (typecheck, gradle-adjacent copy) — this is measurement, not
# tuning. TALON_BENCH_WS overrides the workspace (default: workspaces/27).
bench:
	cd backend && go test -run='^$$' -bench=. -benchtime=1x ./internal/tools/ ./internal/release/ ./internal/verify/

ci: evals

# The live E2E of the entire core product (PLAN_CORE_PRODUCT.md phase C):
# real stack, real model gateway, real redroid device, real template
# project — no mocks. e2e/run-live.sh owns preflight, the scratch stack,
# the drive, and teardown.
e2e-live:
	./e2e/run-live.sh

# W13.5: nightly scorecard. The same live run, with the published artifact —
# cmd/e2e's report() writes e2e/reports/nightly-latest.md on every exit path
# when E2E_REPORT is set (run-live.sh sets it by default). CI stays
# secret-free by law (no AI key, no device there), so the nightly runs
# where the stack lives — a cron/systemd timer invoking this target — and
# the report file is the artifact that gets reviewed and published.
nightly: e2e-live
