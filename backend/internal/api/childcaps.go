package api

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/subagent"
)

// W12.2 — the fan-out cap knobs. They are read once when the session's
// delegation service is built (one turn's lifetime, which is exactly the
// spend accumulator's window). Unset/invalid falls back to the law-10
// defaults; delegation itself still ships dark behind
// TALONSTUDIO_SUBAGENTS, so these change nothing a deployment did not
// already opt into.

// childIsolationWorktree reads TALONSTUDIO_CHILD_ISOLATION: "worktree"
// opts mutating children into per-child git worktrees with merge-back;
// anything else (default readonly) keeps children on the shared
// read-only path — byte-identical for every existing deployment.
func childIsolationWorktree() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TALONSTUDIO_CHILD_ISOLATION")), "worktree")
}

// childSpendCapFromEnv reads TALONSTUDIO_CHILD_SPEND_CAP — the USD bound
// on one turn's fan-out (priced from ROUTING_PRICING_JSON through each
// child's CostUSD). 0 disables the cap deliberately; a parse failure
// falls back to the default rather than silently unbounding.
func childSpendCapFromEnv() float64 {
	raw := strings.TrimSpace(os.Getenv("TALONSTUDIO_CHILD_SPEND_CAP"))
	if raw == "" {
		return subagent.DefaultSpendCapUSD
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f < 0 {
		log.Printf("api: TALONSTUDIO_CHILD_SPEND_CAP %q invalid, using default $%.2f", raw, subagent.DefaultSpendCapUSD)
		return subagent.DefaultSpendCapUSD
	}
	return f
}

// childWallFromEnv reads TALONSTUDIO_CHILD_WALL_SECS — the per-child
// wall-clock budget. 0 disables it deliberately (unbounded children are
// an explicit choice, never a typo's side effect).
func childWallFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("TALONSTUDIO_CHILD_WALL_SECS"))
	if raw == "" {
		return subagent.DefaultWall
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		log.Printf("api: TALONSTUDIO_CHILD_WALL_SECS %q invalid, using default %s", raw, subagent.DefaultWall)
		return subagent.DefaultWall
	}
	return time.Duration(n) * time.Second
}
