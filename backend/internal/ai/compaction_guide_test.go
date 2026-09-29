package ai

import (
	"strings"
	"testing"
)

// FM-CMPb — written first (TEST_RULES §3). The failure: the compaction
// checkpoint's section list is the shape the *next* request reads to
// resume cold, and today's eight sections (Primary Request and Intent,
// Key Technical Concepts, Files and Code, Errors and Fixes, Pending
// Jobs, Current Work, Next Step, Critical Context) mix durable state
// with volatile state and have no home for two recovery-critical
// facts: a stated requirement that must not be re-litigated, and a
// blocker with who must act. The shape that resumes cold is the one
// this project's own session checkpoints use: Objective, Requirements,
// Decisions Taken, then the work state (Completed / Active / Blocked /
// Next Move), Relevant Files, Important Context.
//
// The law: the instruction lists exactly those sections, in order;
// the shape gate accepts a guide-shaped reply. The legacy anchor
// stays accepted (pinned by TestLooksLikeCheckpoint, untouched): a
// legacy checkpoint echoing from kept history still beats the static
// fallback line — the gate rejects ramblers, not fashion.
//
// Red state: the instruction still lists the legacy eight sections
// (Objective absent from the section list), and looksLikeCheckpoint
// only knows "Primary Request and Intent", so a guide-shaped reply
// would be rejected into the static fallback.

// guideSections is the contract order: three durable sections, then
// the work-state group (its heading plus four sub-sections), then
// files and context. The instruction must list them in exactly this
// order between its list marker and the Rules block.
var guideSections = []string{
	"Objective",
	"Requirements",
	"Decisions Taken",
	"Current Work State",
	"Completed",
	"Active",
	"Blocked",
	"Next Move",
	"Relevant Files",
	"Important Context",
}

// guideReply is a minimal shape-valid guide checkpoint — what the
// summarizer must be allowed to hand back.
const guideReply = `## Objective
- build the dashboard

## Requirements
- (none)

## Decisions Taken
- (none)

## Current Work State
### Completed
- (none)
### Active
- compaction test fixture
### Blocked
- (none)
### Next Move
- (none)

## Relevant Files
- (none)

## Important Context
- (none)`

func TestCheckpointCarriesTheGuideShape(t *testing.T) {
	// The section list: everything between the list marker and the
	// Rules block, split on newlines. RED today — the list is the
	// legacy eight sections, so the first missing anchor fails.
	listStart := strings.Index(compactionInstruction, "in this order, each as a markdown heading:")
	if listStart == -1 {
		t.Fatal("the instruction has no section-list marker")
	}
	list := compactionInstruction[listStart:]
	if rules := strings.Index(list, "\nRules:"); rules != -1 {
		list = list[:rules]
	}
	var got []string
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "in this order") {
			continue
		}
		got = append(got, line)
	}
	if len(got) != len(guideSections) {
		t.Errorf("section list = %q, want %q", got, guideSections)
	} else {
		for i := range guideSections {
			if got[i] != guideSections[i] {
				t.Errorf("section[%d] = %q, want %q", i, got[i], guideSections[i])
			}
		}
	}

	// The shape gate must accept the reply the new instruction asks
	// for — today it only knows the legacy anchor, so a correct
	// guide-shaped checkpoint would be rejected into the static
	// fallback and its memory lost.
	if !looksLikeCheckpoint(guideReply) {
		t.Error("a guide-shaped checkpoint must be accepted by the shape gate")
	}
}
