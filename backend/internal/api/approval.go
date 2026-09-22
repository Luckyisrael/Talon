package api

import (
	"log"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/harness/approval"
	"github.com/luckyisrael/talon/backend/internal/harness/events"
	"github.com/luckyisrael/talon/backend/internal/harness/sessionlog"
	"github.com/luckyisrael/talon/backend/internal/harness/skills"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// The H4 approval seam, wired to Talon's existing bones: the ask_user
// answer flow is the answerer, the turn_approvals table is the binding
// record, the session log (H1) is the audit. This file names the seam those
// bones hang on so every sensitive path can route through it uniformly.

// approvalAudit appends one ApprovalDecided event to the session log — one
// per decision, grants and refusals alike.
func approvalAudit(lg *sessionlog.Log, promptID int64) func(approval.AuditRecord) {
	if lg == nil {
		return nil
	}
	return func(r approval.AuditRecord) {
		// The action text rides Reason when the outcome is a refusal —
		// the payload is compact by design (scope, allowed, why).
		reason := r.Reason
		if r.Outcome == approval.AllowedOnce {
			reason = r.Action
		}
		if _, err := lg.Append(events.ApprovalDecided, events.ApprovalPayload{
			Scope:    r.Scope,
			Allowed:  r.Outcome == approval.AllowedOnce,
			Reason:   reason,
			PromptID: promptID,
		}); err != nil {
			log.Printf("approval audit: append: %v", err)
		}
	}
}

// turnAnswerer is the H4 answerer for turn-scoped requests: it resolves
// against the turn_approvals table, which the ask_user answer flow
// (prompts.go) grants. Scope decides which grant is read — escalation
// requests ("sandbox-escalation:*") read the tier2_shell consent, everything
// else (device build) reads device_build — so a build yes stays a build
// unlock and a command yes unlocks exactly the surfaced command's escalation.
// A grant recorded there is a real user yes; absence is not a no, it is
// not-yet-answered — the answerer reports delivery failure so the seam does
// not convert silence into rejection.
func turnAnswerer(st *store.Store, projectID int64) approval.Answerer {
	return func(req approval.Request) (bool, error) {
		if st == nil {
			return false, errNoStoreAvailable{}
		}
		scope := store.ScopeDeviceBuild
		if strings.HasPrefix(req.Scope, "sandbox-escalation:") {
			scope = store.ScopeTier2Shell
		}
		if st.HasApproval(req.PromptID, scope) {
			// The user's yes is on record: allowed, once. The grant is not
			// consumed — the next sensitive action asks again by contract —
			// but the table is the honesty surface for what was allowed.
			return true, nil
		}
		// Not answered yet: the question must reach the user. Delivery
		// failure (not rejection) — the ask_user tool is the delivery.
		return false, errAwaitingUserAnswer{}
	}
}

type errNoStoreAvailable struct{}

func (errNoStoreAvailable) Error() string { return "no store available to check approvals" }

type errAwaitingUserAnswer struct{}

func (errAwaitingUserAnswer) Error() string { return "the user has not answered yet" }

// buildApprovalSeam assembles the turn seam for one prompt: ask policy, the
// turn-approvals answerer, and the session-log audit. Tier-2 shell
// escalations and the device-build gate both ask through this seam.
func (h *PromptHandler) buildApprovalSeam(lg *sessionlog.Log, projectID, promptID int64) *approval.Seam {
	seam := approval.New(approval.PolicyAsk, approvalAudit(lg, promptID))
	seam.Register(turnAnswerer(h.store, projectID))
	return seam
}

// projectLog resolves the project's session log for seam construction, the
// one call site outside the prompt's hot path (newBuildSession). The cache
// wins when present so builds share the handler's open log; a nil slog (bare
// handler literals in tests) falls back to a direct open. A failed open
// returns nil — audit degrades to a no-op, log loss never fails a run.
func (h *PromptHandler) projectLog(projectID int64) *sessionlog.Log {
	if h.slog != nil {
		return h.slog.get(h.store, projectID)
	}
	return openSessionLog(h.store, projectID)
}

// openSessionLog opens a project's session log directly (no cache), for
// paths that do not carry the prompt handler's cache: session builds under a
// bare handler and the user-initiated HTTP actions below.
func openSessionLog(st *store.Store, projectID int64) *sessionlog.Log {
	if st == nil {
		return nil
	}
	lg, err := st.SessionLog(sessionLogKey(projectID))
	if err != nil {
		log.Printf("approval: opening session log for project %d: %v", projectID, err)
		return nil
	}
	return lg
}

// Scopes for the user-initiated sensitive HTTP actions that route through
// the seam alongside the turn-scoped ones (device_build lives in store).
const (
	// scopeGitPush audits a push to GitHub.
	scopeGitPush = "git-push"
	// scopeReleaseExport audits a release export, which touches the
	// project's signing keystores.
	scopeReleaseExport = "release-export"
)

// userInitiatedAnswerer answers for actions the user themselves initiated
// over authenticated HTTP (export, git push): the request is the yes — the
// authenticated session and the UI's confirmation are the consent. The seam
// still asks, so the decision is audited to the session log and a
// policy-never composition fails closed instead of silently acting.
func userInitiatedAnswerer() approval.Answerer {
	return func(approval.Request) (bool, error) { return true, nil }
}

// approveUserAction routes one user-initiated sensitive HTTP action through
// the H4 seam: one Request, one audit event, AllowedOnce when the answerer
// grants. Handlers refuse when the outcome is anything else — fail closed,
// never unseamed.
func approveUserAction(st *store.Store, projectID int64, scope, action string) approval.Outcome {
	seam := approval.New(approval.PolicyAsk, approvalAudit(openSessionLog(st, projectID), 0))
	seam.Register(userInitiatedAnswerer())
	return seam.Ask(approval.Request{Scope: scope, Action: action, PromptID: 0})
}

// WantsDeviceBuild re-exports the orchestrator's scanners for the answer
// handler so this file is the single place the H4 vocabulary is defined.
var (
	WantsDeviceBuild = orchestrator.WantsDeviceBuild
	WantsApproval    = orchestrator.WantsApproval
)

// catalogLibrary adapts the H6 skills catalog to the tools.SkillLibrary
// interface read_skill consumes.
type catalogLibrary struct{ c *skills.Catalog }

func (l catalogLibrary) SkillBody(name string) (string, bool) {
	body, _, ok := l.c.Body(name)
	return body, ok
}

func (l catalogLibrary) SkillNames() []string { return l.c.Names() }

// orchSkillCatalog extracts the catalog an orchestrator was attached, if any.
func orchSkillCatalog(o *orchestrator.Orchestrator) *skills.Catalog {
	if o == nil {
		return nil
	}
	return o.SkillCatalog()
}
