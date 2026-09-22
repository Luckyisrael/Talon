// Package tooltimeout is the cooperative per-tool call-timeout policy.
//
// Before this there was no bound on a tool call at all: a tool that hung — a
// gradle daemon that never answered, a fetch to a dead host, a device command
// parked on a locked boot — held its round forever, and the round budget was
// the only thing that eventually ended the turn. By then the person had been
// staring at a "running" row for minutes with no idea which call is stuck.
//
// The policy is deliberately NOT a registry-wide default. Different tools have
// wildly different honest costs: a read_file answers in milliseconds, a
// device build in minutes. One number would either kill real work or bound
// nothing. The policy maps a tool to its own budget and only the tools it
// names get a deadline — an unnamed tool keeps the old unbounded behaviour
// rather than inheriting a default that is wrong for it.
//
// "Cooperative" means the tool is asked to stop, not killed: the derived
// context's deadline is the signal, and a tool that respects ctx stops itself
// and returns its own partial story. A tool that ignores ctx runs past the
// deadline — the policy then reports the timeout in the result rather than
// pretending the call succeeded, and the round moves on.
package tooltimeout

import (
	"context"
	"fmt"
	"time"
)

// Default budgets per tool class. These are starting points, not commandments:
// a tool that legitimately needs more is named explicitly by the policy, and
// one that turns out to hang is named with a tighter budget.
const (
	readBudget  = 30 * time.Second
	writeBudget = 2 * time.Minute
	shellBudget = 5 * time.Minute
	buildBudget = 10 * time.Minute
)

// Policy maps a tool name to its call budget. The zero policy applies no
// deadlines — callers opt in by registering the tools they run.
type Policy struct {
	budgets map[string]time.Duration
}

// New returns an empty policy: nothing is bounded until Register names it.
func New() *Policy { return &Policy{budgets: map[string]time.Duration{}} }

// Register sets one tool's budget. Registering a tool already registered
// replaces its budget — the map is the policy, last write wins.
func (p *Policy) Register(name string, budget time.Duration) {
	p.budgets[name] = budget
}

// BudgetFor reports the tool's budget and whether the policy bounds it at all.
// An unregistered tool returns ok=false and the caller must leave it
// unbounded: a default here is exactly the registry-wide mistake the policy
// exists to avoid.
func (p *Policy) BudgetFor(name string) (budget time.Duration, ok bool) {
	b, ok := p.budgets[name]
	return b, ok
}

// Derive returns the context the tool should run under. Three cases:
//
//   - the tool is unregistered: the parent context passes through untouched.
//     No deadline is invented for a tool the policy does not name.
//   - the parent already has an earlier deadline: the parent wins. The tool
//     reads that as an upstream cancel — the turn is ending — never as its
//     own timeout, so the result names the real cause.
//   - the policy's deadline is earlier: the derived context carries it.
//
// The returned cancel must be called when the tool returns; the name is for
// the timeout message.
func Derive(parent context.Context, name string, budget time.Duration, ok bool) (context.Context, context.CancelFunc, string) {
	if !ok {
		// Nothing to bound and nothing to leak: return a no-op cancel.
		return parent, func() {}, ""
	}
	if parent == nil {
		parent = context.Background()
	}
	// An earlier parent deadline is an upstream cancel and must not be
	// extended or reinterpreted as this tool's budget.
	if pd, has := parent.Deadline(); has && pd.Before(time.Now().Add(budget)) {
		return parent, func() {}, ""
	}
	ctx, cancel := context.WithDeadline(parent, time.Now().Add(budget))
	return ctx, cancel, fmt.Sprintf("%s exceeded its %s call budget", name, budget)
}

// DefaultPolicy seeds the budgets the shipped tools need. This is the only
// place a registry-wide assumption appears — and it is per-tool, not a blanket.
func DefaultPolicy() *Policy {
	p := New()
	for _, name := range []string{
		"read_file", "list_files", "project_map", "inspect_package",
		"codebase_search", "codebase_graph",
		// W5: the lsp_* tools ask a local server over stdin/stdout — as
		// fast as grep; the fleet is cached, nothing compiles per call.
		"lsp_diagnostics", "lsp_references", "lsp_definition", "lsp_symbol",
	} {
		p.Register(name, readBudget)
	}
	for _, name := range []string{
		"write_file", "try_edit", "keep_edit", "create_component",
		// W5.4: the one lsp write — journaled through FS like every write.
		"lsp_organize_imports",
	} {
		p.Register(name, writeBudget)
	}
	for _, name := range []string{
		"run_typecheck", "run_lint", "run_tests", "install_package",
	} {
		p.Register(name, shellBudget)
	}
	for _, name := range []string{
		"build_android", "run_verify", "device_build",
	} {
		p.Register(name, buildBudget)
	}
	return p
}
