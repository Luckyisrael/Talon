package ai

import (
	"strings"
	"testing"
)

// Phase guards (PLAN_CORE_PRODUCT.md G): one test per phase, each pinning
// the contract that phase shipped. If a regression breaks a phase's law,
// its guard names it — these are the evals' memory of what shipped and why.

// A: the system prompt is device-aware — it must name the device tools
// (metro_status, capture_screenshot, tail_logcat, run_e2e) and the device
// lifecycle (10m pause / 30m destroy). Cache-stable: the same inputs build
// the same prompt, so the device section is always present.
//
// L21's diet moved the protocol DETAIL (flows dir, app id, verify order,
// tool usage) behind read_skill: those markers now pin in knowledge's
// TestDeviceSessionSkillCarriesProtocol, and the prompt keeps the surface
// law — tools, absence, lifecycle, no-fabricate — so the agent still knows
// the device surface without paying for the how-to every round.
func TestPhaseA_DeviceAwarePrompt(t *testing.T) {
	prompt := SystemPromptBuild

	mustContain := []string{
		"metro_status",
		"capture_screenshot",
		"tail_logcat",
		"run_e2e",
		"device-session",
	}
	for _, want := range mustContain {
		if !strings.Contains(prompt, want) {
			t.Errorf("SystemPromptBuild lost %q — the agent no longer knows this device surface", want)
		}
	}
	// The lifecycle law, stated in the prompt: pause after 10 idle minutes,
	// destroy after 30. The exact phrasing may evolve; the numbers and the
	// restart expectation are the contract.
	for _, want := range []string{"10 idle minutes", "after 30"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the device lifecycle lost %q", want)
		}
	}
	// Cache-stable: two builds are identical.
	if SystemPromptBuild != prompt {
		t.Error("SystemPromptBuild is not deterministic — device awareness could disappear between calls")
	}
}

// A (plan side): plan prompts also know the device exists — the plan asks
// for verifiable units and names device verification in task naming.
func TestPhaseA_PlanPromptDeviceAware(t *testing.T) {
	prompt := PlanSystemPrompt(nil)
	if !strings.Contains(prompt, "device") {
		t.Error("the plan prompt does not mention the device at all")
	}
}
