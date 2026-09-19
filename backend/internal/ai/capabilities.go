package ai

import (
	"os"
	"strings"
)

// This file is the model-agnostic lever. Talon aims to produce good apps
// regardless of which model drives the build, and the knob for that is how much
// working discipline the prompt spells out. Every tier gets the same core workflow —
// plan, read before you write, load the skill for an unfamiliar area, work in small
// steps, typecheck as you go — because the deterministic layers depend on it and it
// is not tied to any one model. What the tier changes is how much of it is spelled
// out: a strong model gets it compressed to a few sentences, a weak model gets each
// habit stated in full. The one thing no model can know from training is that this
// stack is newer than that training, so every tier's block leads with it.
//
// The interaction cadence the product wants — small verifiable steps, surfacing
// decisions, ask_user at a real fork — lives in SystemPromptBuild instead of here,
// because it applies to every tier. This file only adds the tier-specific scaffold
// on top of it.
//
// The tier is explicit operator config (AI_MODEL_TIER) — no model-name heuristics.
// Harness owns discipline; models follow it.

// Tier is a coarse capability class for the configured model.
type Tier string

const (
	TierStrong Tier = "strong"
	TierMedium Tier = "medium"
	TierWeak   Tier = "weak"
)

// Capabilities describes the model driving a build well enough to adapt the prompt
// to it. It is derived once from the model id (or an explicit override) and is
// otherwise constant for a deployment.
type Capabilities struct {
	Model string
	Tier  Tier
}

// Capabilities reports the descriptor for the configured model. A nil client
// (as in some tests) reports the neutral middle tier.
func (c *Client) Capabilities() Capabilities {
	if c == nil {
		return Capabilities{Tier: TierMedium}
	}
	return Capabilities{
		Model: c.model,
		Tier:  classifyTier(c.model, os.Getenv("AI_MODEL_TIER")),
	}
}

// classifyTier maps the operator's explicit tier to a Tier. The model id is
// accepted but unused — the harness owns discipline, not the model name. An
// unset or unknown override degrades to the neutral middle tier so a new or
// renamed model is never silently strong or weak.
func classifyTier(_ string, override string) Tier {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "strong":
		return TierStrong
	case "medium":
		return TierMedium
	case "weak":
		return TierWeak
	}
	return TierMedium
}

// WorkingDiscipline returns the tier-appropriate discipline block to append to the
// build system prompt: every tier gets the same core workflow, and the tier decides
// how much of it is spelled out. Even the strong tier gets the full workflow, just
// compressed, because the deterministic layers depend on the model reading before it
// writes, loading the current skill before it uses an unfamiliar API, and
// typechecking as it goes — and a short reminder that this stack is newer than any
// training data steers it to comply before the guardrails have to refuse it and the
// verification ladder has to send it back.
func (caps Capabilities) WorkingDiscipline() string {
	switch caps.Tier {
	case TierWeak:
		return weakDiscipline
	case TierStrong:
		return strongDiscipline
	default:
		return mediumDiscipline
	}
}

const weakDiscipline = "## Working discipline\n\n" +
	"Work in small, checkable steps — one change at a time, not a dozen at once.\n\n" +
	"Before you act, say in a sentence what you are about to change. If it touches an area a skill covers — wallet, navigation, animation, screen design, native UI, performance — call " + "`read_skill`" + " and follow it before writing, because the stack here is newer than your training data and a guessed API will not compile. Before you call a package's API and are not sure of it, run " + "`inspect_package`" + " to read its real signatures from node_modules first. Read a file with " + "`read_file`" + " before you edit it; never edit a file you have not read. When you need several files, read them in the same step rather than one per round — reads run in parallel.\n\n" +
	"After you act, call " + "`run_typecheck`" + " — after each change, not only at the very end, so an error is caught while the code that caused it is still fresh. When it reports an error, read the file it names, fix that one error, and check again; do not pile new changes on top of broken code.\n\n" +
	"Verify on the device per unit: " + "`metro_status`" + " after the device bundles your edits (a bundler error names the file and line), a screenshot when the change is visual, and when the feature is a user flow, a short Maestro flow under " + "`.maestro/flows/`" + " run with " + "`run_e2e`" + ". The dev-client is preinstalled at provision — iterate via Metro, not " + "`build_apk`" + "; only call " + "`build_apk`" + " when the user asks for an APK or you added native code not in the allowlist.\n\n" +
	"Do not stop while the project does not compile, and do not say something works that you have not verified."

const mediumDiscipline = "## Working discipline\n\n" +
	"Plan a change before you make it, and read a file before you edit it. When you need several files, read them in the same step rather than one per round — reads run in parallel. When you work in an area a skill covers, load it with " + "`read_skill`" + " first — the stack here is newer than most training data, so verify an uncertain package API with " + "`inspect_package`" + " rather than guessing. Run " + "`run_typecheck`" + " as you go rather than only at the end, and fix what it reports before moving on.\n\n" +
	"Verify on the device per unit: " + "`metro_status`" + " after the device bundles your edits, a screenshot when the change is visual, and when the feature is a user flow, a short Maestro flow under " + "`.maestro/flows/`" + " run with " + "`run_e2e`" + ". The dev-client is preinstalled at provision — iterate via Metro, not " + "`build_apk`" + "; only call " + "`build_apk`" + " when the user asks for an APK or you added native code not in the allowlist."

const strongDiscipline = "## Working discipline\n\n" +
	"The stack here is newer than your training data — Expo, expo-router, and the Solana Mobile libraries have all moved since. When you work in an area a skill covers — wallet, navigation, animation, screen design, native UI, performance — load it with " + "`read_skill`" + " before you write: a confidently guessed API is the single thing here most likely not to compile — run " + "`inspect_package`" + " to read a package's real signatures when you are unsure.\n\n" +
	"Plan the change, then read a file with " + "`read_file`" + " before you edit it — never edit a file you have not read this turn. When you need several files, read them in the same step — reads run in parallel. Work in small, verifiable steps and run " + "`run_typecheck`" + " as you go, not only at the end, so an error surfaces while the change that caused it is still fresh; fix what it reports before moving on.\n\n" +
	"Verify on the device per unit: " + "`metro_status`" + " after the device bundles your edits, a screenshot when the change is visual, and when the feature is a user flow, a short Maestro flow under " + "`.maestro/flows/`" + " run with " + "`run_e2e`" + ". The dev-client is preinstalled at provision — iterate via Metro, not " + "`build_apk`" + "; only call " + "`build_apk`" + " when the user asks for an APK or you added native code not in the allowlist."
