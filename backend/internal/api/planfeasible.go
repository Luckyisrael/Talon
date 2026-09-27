package api

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// L4 — plan feasibility gate (ENGINEERING_STANDARDS §6).
//
// Failure modes, written before the code (TEST_RULES §3):
//
//  1. FM-L4a — the plan prompt's prose ("only list packages not in this
//     set") is advisory; Run-1 listed 6 packages — 4 absent from the
//     template — anyway and the plan turn closed `completed`, deferring
//     discovery to the build (10 errored inspect_package probes, features
//     promised at plan time and still absent at hour 58). Advisory prose
//     never fails a turn; the gate is code at the plan doors.
//  2. FM-L4b — an existence probe (npm view) is the wrong bar: all six of
//     Run-1's packages exist; they failed as installations against that
//     workspace. Feasibility is "resolvable in THIS workspace" — npm's own
//     resolver, dry.
//  3. FM-L4c — probe flakiness must not fail healthy plans: timeout or
//     registry outage reports unknown and passes; only a definitive
//     resolution failure (404, ERESOLVE) fails the turn. Availability ≠
//     feasibility.
//  4. FM-L4d — the probe must not mutate: plan mode is read-only by tool
//     absence (W3, law 6), so the check is `npm install --dry-run` — no
//     package.json edit, no node_modules write.
//  5. FM-L4e — the dry-run resolves the whole workspace, so a template
//     whose own deps already fail would make every plan look infeasible;
//     a definitive batch failure first proves the baseline (the workspace
//     without the plan's packages) resolves. A broken baseline reports
//     unknown, not infeasible.
//
// The gate runs at both plan doors (Send + stream) after the envelope
// parses and before savePlan: an infeasible package fails the plan turn
// with the package named — the contract §6 asks for — instead of failing
// the build turn hours later.

// planPackageProbe reports which of the given packages are infeasible in
// workspacePath. definitive=false means the answer is unknown (the probe
// flaked) and the turn must pass regardless of infeasible's contents.
type planPackageProbe func(ctx context.Context, workspacePath string, packages []string) (infeasible []string, reason string, definitive bool)

// npmProbeTimeout bounds one dry-run. It is a ceiling, not an expectation:
// a dead network burns it once on the batch, the failure classifies as
// transient, and the turn passes — isolation runs only after a
// definitive batch failure, where npm answers in seconds.
const npmProbeTimeout = 45 * time.Second

// checkPlanFeasibility is the L4 gate. It returns the user-facing
// rejection message and whether to reject. Skips that answer "pass": no
// plan, no packages, no probe wired, no workspace to resolve against, or
// an unknown answer (FM-L4c).
func (h *PromptHandler) checkPlanFeasibility(ctx context.Context, projectID int64, plan *ai.AppPlan) (string, bool) {
	if plan == nil || len(plan.Packages) == 0 || h.packageProbe == nil {
		return "", false
	}
	ws, err := h.workspacePath(projectID)
	if err != nil {
		return "", false // nothing to resolve against — unknown, never a guess
	}
	pre := map[string]bool{}
	if h.knowledge != nil {
		for _, p := range h.knowledge.PreinstalledList() {
			pre[strings.TrimSpace(p)] = true
		}
	}
	var fresh []string
	for _, p := range plan.Packages {
		name := strings.TrimSpace(p)
		if name == "" || pre[name] {
			continue
		}
		fresh = append(fresh, name)
	}
	if len(fresh) == 0 {
		return "", false
	}
	infeasible, reason, definitive := h.packageProbe(ctx, ws, fresh)
	if !definitive || len(infeasible) == 0 {
		return "", false
	}
	return infeasibleMessage(infeasible, reason), true
}

// npmDryRunProbe resolves the packages with npm's resolver in dry mode
// (FM-L4d: nothing written). The batch runs first — packages resolve
// together or not at all; a definitive batch failure is then proven
// against the workspace baseline (FM-L4e) and isolated per package, so
// the turn names exactly the package that cannot install. Transient or
// unrecognized failures report unknown (FM-L4c).
func npmDryRunProbe(ctx context.Context, workspacePath string, packages []string) ([]string, string, bool) {
	out, err := runNPMDryRun(ctx, workspacePath, packages)
	if err == nil {
		return nil, "", true // resolved: a definitive pass
	}
	if classifyNPMFailure(out) != npmFailureDefinitive {
		return nil, "", false // transient or unrecognized — unknown
	}
	// FM-L4e: prove the baseline (the workspace without the plan's
	// packages) resolves before trusting isolation against it. If the
	// template itself cannot resolve, plans cannot be judged here.
	if _, berr := runNPMDryRun(ctx, workspacePath, nil); berr != nil {
		return nil, "", false
	}
	var bad []string
	for _, p := range packages {
		pout, perr := runNPMDryRun(ctx, workspacePath, []string{p})
		if perr != nil && classifyNPMFailure(pout) == npmFailureDefinitive {
			bad = append(bad, p)
		}
	}
	if len(bad) > 0 {
		return bad, npmFailureLine(out), true
	}
	// Definitive batch failure with no single culprit: a pairwise or
	// workspace-level resolve conflict — the batch is the bar (FM-L4b),
	// so the whole set is implicated.
	return packages, npmFailureLine(out), true
}

// npmFailure classifies one dry-run failure.
type npmFailure int

const (
	// npmFailureUnknown — unrecognized or empty output: never proof
	// (FM-L4c's conservative direction).
	npmFailureUnknown npmFailure = iota
	// npmFailureTransient — network-level: availability, not feasibility.
	npmFailureTransient
	// npmFailureDefinitive — the registry or workspace said no (404,
	// ERESOLVE): the only class that may fail a turn.
	npmFailureDefinitive
)

// classifyNPMFailure reads npm's output for the failure class.
// Definitive markers come first: an ERESOLVE trace that mentions the
// network is still a resolve verdict, not an outage.
func classifyNPMFailure(output string) npmFailure {
	if strings.TrimSpace(output) == "" {
		return npmFailureUnknown
	}
	for _, m := range []string{
		"npm ERR! code E404", "npm ERR! 404", "404 Not Found",
		"npm ERR! code ERESOLVE", "ERESOLVE",
	} {
		if strings.Contains(output, m) {
			return npmFailureDefinitive
		}
	}
	for _, m := range []string{
		"ENOTFOUND", "EAI_AGAIN", "ETIMEDOUT", "ECONNREFUSED", "ECONNRESET",
		"EHOSTUNREACH", "ENETUNREACH", "ECONNABORTED",
		"npm ERR! network", "npm ERR! 408", "npm ERR! 429",
		"npm ERR! 500", "npm ERR! 502", "npm ERR! 503", "npm ERR! 504",
	} {
		if strings.Contains(output, m) {
			return npmFailureTransient
		}
	}
	return npmFailureUnknown
}

// npmFailureLine pulls npm's own verdict out of the output for the
// rejection message: the first npm ERR! line, else the first non-empty
// line, trimmed to a readable length.
func npmFailureLine(output string) string {
	line := ""
	for _, l := range strings.Split(output, "\n") {
		if strings.Contains(l, "npm ERR!") {
			line = l
			break
		}
		if line == "" && strings.TrimSpace(l) != "" {
			line = l
		}
	}
	line = strings.TrimSpace(line)
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	return line
}

// runNPMDryRun executes `npm install --dry-run …` in dir and returns the
// combined output.
func runNPMDryRun(ctx context.Context, dir string, packages []string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, npmProbeTimeout)
	defer cancel()
	args := append([]string{"install", "--dry-run", "--no-audit", "--no-fund"}, packages...)
	cmd := exec.CommandContext(cctx, "npm", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// infeasibleMessage renders the plan-turn rejection.
func infeasibleMessage(names []string, reason string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, "`"+n+"`")
	}
	verb := "is"
	if len(names) > 1 {
		verb = "are"
	}
	if reason == "" {
		reason = "npm could not resolve it"
	}
	return fmt.Sprintf("Plan rejected: %s %s not installable in this workspace (%s). "+
		"Remove it from \"packages\" or replace it with one that resolves against the template's dependencies, then plan again — "+
		"a package that cannot install here would fail the build turn instead, hours later.",
		strings.Join(quoted, ", "), verb, reason)
}
