package orchestrator

// W6 — verification becomes policy: "verified" must mean something you can
// build a release on. This file holds the gate policy matrix and the
// classification that turns a ladder sweep into a policy verdict.
//
// The ladder itself stays fail-open (missing infra never loops the model
// trying to fix a checker), but the OUTCOME is now honest about what ran:
// a sweep where a required check never ran is not a plain "verified", and
// export/release/APK-ship read that verdict before shipping (fail-closed
// with a named human override in audit_log — laws 6/15).
//
// Policy resolution order (project-level flag per the plan):
//  1. .talon/config.json "verify_policy" — the project's own choice;
//  2. TALONSTUDIO_VERIFY_POLICY — the deployment default;
//  3. strict — the default the plan pins "for new projects after evals".
//
// The same config file carries an optional per-stage matrix under
// "verify_gates" ({"vision":"off"} and so on). Both keys share the file
// lsp.Config reads; each struct decodes only its own keys, so unknown
// fields are ignored on both sides.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/store"
)

// GateClass is one ladder stage's policy class.
type GateClass string

const (
	// GateRequired: the stage must have RUN for the outcome to count as
	// verified. It may still fail the ladder the ordinary way (repair
	// loop); a skip of a required stage refuses verification outright.
	GateRequired GateClass = "required"
	// GateBestEffort: the stage is allowed to be missing (no device, no
	// LSP server) — the outcome stays verified but is marked best-effort,
	// and release requires an explicit override for it.
	GateBestEffort GateClass = "best-effort"
	// GateOff: the stage is not part of this project's ladder at all —
	// never run, never recorded as skipped (an explicit project choice,
	// visible in the committed config, not an infra gap).
	GateOff GateClass = "off"
)

// Verification statuses — the three states a finished sweep can record.
const (
	// VerifyStatusVerified: every gate that ran passed and no gate was
	// skipped. This is the only status release ships without an override.
	VerifyStatusVerified = "verified"
	// VerifyStatusBestEffort: the ladder passed but skipped best-effort
	// gates (device-less runs, absent language server). Honest "everything
	// that could run, ran" — never a silent release.
	VerifyStatusBestEffort = "best-effort"
	// VerifyStatusRefused: a required gate skipped, or the ladder failed.
	// Export/APK-ship refuse; a named human override is the only way past.
	VerifyStatusRefused = "refused"
)

// Verification policy values.
const (
	VerifyPolicyStrict     = "strict"
	VerifyPolicyBestEffort = "besteffort"
)

// verifyConfigKeys is the W6 view of .talon/config.json (see file header).
type verifyConfigKeys struct {
	VerifyPolicy string            `json:"verify_policy"`
	VerifyGates  map[string]string `json:"verify_gates"`
}

func loadVerifyConfig(workspacePath string) verifyConfigKeys {
	var cfg verifyConfigKeys
	if workspacePath == "" {
		return cfg
	}
	raw, err := os.ReadFile(filepath.Join(workspacePath, ".talon", "config.json"))
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	return cfg
}

// resolveVerifyPolicy picks the policy for one sweep: project config, then
// the env default, then strict. An unrecognized value is strict — a typo in
// a safety flag must not silently mean permissive.
func resolveVerifyPolicy(workspacePath string) string {
	if p := normalizePolicy(loadVerifyConfig(workspacePath).VerifyPolicy); p != "" {
		return p
	}
	if p := normalizePolicy(os.Getenv("TALONSTUDIO_VERIFY_POLICY")); p != "" {
		return p
	}
	return VerifyPolicyStrict
}

func normalizePolicy(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case VerifyPolicyStrict:
		return VerifyPolicyStrict
	case VerifyPolicyBestEffort, "best-effort":
		return VerifyPolicyBestEffort
	case "":
		return ""
	default:
		log.Printf("orchestrator: unrecognized verify policy %q — using strict", v)
		return VerifyPolicyStrict
	}
}

// defaultGateClass is the plan's default matrix: static checks required;
// boot/vision/interact required only when a device is attached (device-less
// CI cannot satisfy them, so they are best-effort there — the risk note's
// policy classes, not a global switch); the LSP rung is best-effort because
// W5's law is that an absent server never blocks (a project that wants it
// binding promotes it through verify_gates); anything new fails closed to
// required.
func defaultGateClass(stage string, deviceAttached bool) GateClass {
	switch stage {
	case "typecheck", "lint", "bundle", "gradle":
		return GateRequired
	case "lsp":
		return GateBestEffort
	case "boot", "vision", "interact":
		if deviceAttached {
			return GateRequired
		}
		return GateBestEffort
	default:
		return GateRequired
	}
}

// gatePolicyClass resolves a stage's class: the project's verify_gates
// entry when it names a valid class, else the default. An invalid entry
// logs and falls back — config typos degrade to the default, never to off.
func gatePolicyClass(workspacePath, stage string, deviceAttached bool) GateClass {
	if entry, ok := loadVerifyConfig(workspacePath).VerifyGates[stage]; ok {
		switch GateClass(strings.ToLower(strings.TrimSpace(entry))) {
		case GateRequired:
			return GateRequired
		case GateBestEffort:
			return GateBestEffort
		case GateOff:
			return GateOff
		default:
			log.Printf("orchestrator: unrecognized class %q for gate %q — using the default", entry, stage)
		}
	}
	return defaultGateClass(stage, deviceAttached)
}

// classifyVerification turns one finished sweep (ladder ok, these stages
// skipped) into a status. policy is the already-resolved policy.
//
// refused  — a required stage was skipped under strict (fail-closed truth).
// best-effort — skips existed but every one of them was best-effort class
//
//	(or required skips under the besteffort policy, which downgrades
//	the claim instead of voiding it).
//
// verified — nothing was skipped.
func classifyVerification(policy, workspacePath string, skips []string, deviceAttached bool) (status, reason string) {
	if len(skips) == 0 {
		return VerifyStatusVerified, ""
	}
	var required []string
	for _, stage := range skips {
		if gatePolicyClass(workspacePath, stage, deviceAttached) == GateRequired {
			required = append(required, stage)
		}
	}
	if len(required) == 0 || policy == VerifyPolicyBestEffort {
		return VerifyStatusBestEffort, ""
	}
	return VerifyStatusRefused, "required gate(s) never ran: " + strings.Join(required, ", ")
}

// VerifyReleaseRefusal is the one rule export/release and APK-ship share:
// "" means the recorded state allows shipping. found=false (the project
// has never completed a verified sweep) refuses too — fail-closed means an
// absence of evidence is not evidence of a passing build.
func VerifyReleaseRefusal(found bool, st store.ProjectVerifyState) string {
	if !found {
		return "this project has no verification record yet"
	}
	switch st.Status {
	case VerifyStatusVerified:
		return ""
	case VerifyStatusBestEffort:
		msg := "verification was best-effort — skipped gate(s): " + strings.Join(st.Skipped, ", ")
		if len(st.Remaining) > 0 {
			msg += "; pending plan task(s): " + strings.Join(st.Remaining, ", ")
		}
		return msg
	default:
		return "verification was refused: " + st.Reason
	}
}

// recordVerifyState persists the sweep's verdict so a later export or
// build_apk call can enforce it. Nil-store harnesses record nothing; a
// write error logs and never fails the turn (the turn's outcome is the
// primary truth — the row is its durable shadow).
func (o *Orchestrator) recordVerifyState(projectID int64, policy, status, reason string, skipped, remaining []string) {
	if o.store == nil {
		return
	}
	err := o.store.SetProjectVerifyState(store.ProjectVerifyState{
		ProjectID: projectID,
		Status:    status,
		Policy:    policy,
		Skipped:   skipped,
		Remaining: remaining,
		Reason:    reason,
	})
	if err != nil {
		log.Printf("orchestrator: could not record verify state for project %d: %v", projectID, err)
	}
}

// verifyStateReason builds the refusal reason recorded for a sweep the
// ladder itself refused (as opposed to a skip classification).
func verifyStateReason(maxAttempts int) string {
	return fmt.Sprintf("code did not pass verification after %d repair attempts", maxAttempts)
}
