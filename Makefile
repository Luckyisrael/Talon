# SeekerStudio merge gates. `make evals` is the only merge path: the full
# unit suite plus the phase-guard eval set (one case per PLAN.md phase,
# healthy and fault-injected polarities). `make ci` mirrors the CI workflow.

.PHONY: evals ci vet test

evals: vet test

vet:
	cd backend && go vet ./...

test:
	cd backend && go test -count=1 ./...

ci: evals
