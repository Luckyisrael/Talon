package api

import "strings"

// Quota is the per-cycle allowance for a subscription tier. A negative value
// means unlimited.
//
// This is the single source of truth for tier limits. Before it existed the
// numbers were written in three places that had drifted apart: the enforcement
// path (promptLimitFor) allowed Pro 2000 prompts, the reporting path (usage.go)
// showed Pro 500, and the pricing page advertised 500. A Pro user was told they
// had 500 but could actually spend 2000 — the UI lied about the limit it was
// enforcing. Every path now reads these values, so the advertised number, the
// reported number, and the enforced number cannot disagree again.
type Quota struct {
	Prompts       int // prompts per cycle; -1 = unlimited
	DeviceMinutes int // device minutes per cycle; -1 = unlimited
}

// quotaFor returns the allowance for a tier. Tier names are matched
// case-insensitively; anything unrecognised falls back to the free allowance,
// which is the safe default (never grant more than we mean to).
func quotaFor(tier string) Quota {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "pro":
		return Quota{Prompts: 500, DeviceMinutes: 1500}
	case "team", "enterprise", "unlimited":
		return Quota{Prompts: -1, DeviceMinutes: -1}
	default:
		return Quota{Prompts: 20, DeviceMinutes: 60}
	}
}

// isPaidTier reports whether a tier is a paid one. It is derived from the same
// source as the quotas: a paid tier is exactly one whose allowance exceeds the
// free allowance (either a higher prompt count or unlimited). Defining it this
// way rather than as a second hand-written list means a new tier added to
// quotaFor is classified correctly here without a separate edit — the two
// cannot drift apart the way the limits once did.
//
// This gates premium-only features (the GitHub integration in W4). The check is
// always server-side against the user's stored tier; a client claim never
// decides it.
func isPaidTier(tier string) bool {
	q := quotaFor(tier)
	free := quotaFor("free")
	return q.Prompts < 0 || q.Prompts > free.Prompts
}
