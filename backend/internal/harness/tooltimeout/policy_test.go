package tooltimeout

import (
	"context"
	"testing"
	"time"
)

// An unregistered tool must be left unbounded — the whole point of the policy
// is that no registry-wide default is applied, because one number is wrong for
// every tool class at once.
func TestUnregisteredToolStaysUnbounded(t *testing.T) {
	p := New()
	if _, ok := p.BudgetFor("some_tool"); ok {
		t.Error("an unregistered tool must report not-bounded")
	}
	ctx, cancel, msg := Derive(context.Background(), "some_tool", 0, false)
	defer cancel()
	if msg != "" {
		t.Errorf("an unbounded tool must not get a timeout message, got %q", msg)
	}
	if _, has := ctx.Deadline(); has {
		t.Error("an unbounded tool must not receive a deadline")
	}
}

// A registered tool runs under its own budget.
func TestRegisteredToolGetsItsBudget(t *testing.T) {
	p := New()
	p.Register("read_file", 5*time.Second)
	budget, ok := p.BudgetFor("read_file")
	if !ok {
		t.Fatal("a registered tool must be bounded")
	}
	if budget != 5*time.Second {
		t.Errorf("budget: got %s, want 5s", budget)
	}
	ctx, cancel, msg := Derive(context.Background(), "read_file", budget, ok)
	defer cancel()
	dl, has := ctx.Deadline()
	if !has {
		t.Fatal("a bounded tool must receive a deadline")
	}
	if remaining := time.Until(dl); remaining > 5*time.Second || remaining < 3*time.Second {
		t.Errorf("deadline should be ~5s out, got %s", remaining)
	}
	if msg == "" {
		t.Error("a bounded tool must get a timeout message for the result")
	}
}

// An earlier parent deadline wins: the tool reads it as an upstream cancel,
// never as its own timeout, so the result names the real cause.
func TestParentDeadlineWins(t *testing.T) {
	p := New()
	p.Register("read_file", time.Hour)
	ctx, cancelParent := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	defer cancelParent()

	tctx, cancel, msg := Derive(ctx, "read_file", time.Hour, true)
	defer cancel()
	if tctx != ctx {
		t.Error("an earlier parent deadline must pass the parent context through")
	}
	if msg != "" {
		t.Errorf("an upstream cancel must not be reported as this tool's timeout, got %q", msg)
	}
	if tctx.Err() == nil {
		_ = tctx
	}
}

// The default policy bounds the shipped tools and leaves the rest alone.
func TestDefaultPolicyCoversShippedTools(t *testing.T) {
	p := DefaultPolicy()
	if _, ok := p.BudgetFor("read_file"); !ok {
		t.Error("read_file must be bounded")
	}
	if _, ok := p.BudgetFor("build_android"); !ok {
		t.Error("build_android must be bounded with the long budget")
	}
	if b, _ := p.BudgetFor("build_android"); b <= p.budgets["read_file"] {
		t.Errorf("a build must get a longer budget than a read, got build=%s read=%s",
			b, p.budgets["read_file"])
	}
	// An unknown tool stays unbounded.
	if _, ok := p.BudgetFor("future_tool"); ok {
		t.Error("the default policy must not bound tools it does not name")
	}
}

// Register replaces an existing budget — the map is the policy.
func TestRegisterReplaces(t *testing.T) {
	p := New()
	p.Register("read_file", time.Second)
	p.Register("read_file", time.Minute)
	if b, _ := p.BudgetFor("read_file"); b != time.Minute {
		t.Errorf("last registration must win, got %s", b)
	}
}
