package api

import (
	"log"

	"github.com/seekersstudio/backend/internal/harness/approval"
	"github.com/seekersstudio/backend/internal/harness/events"
	"github.com/seekersstudio/backend/internal/harness/sessionlog"
	"github.com/seekersstudio/backend/internal/orchestrator"
	"github.com/seekersstudio/backend/internal/store"
)

// The H4 approval seam, wired to SeekerStudio's existing bones: the ask_user
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

// deviceBuildAnswerer is the H4 answerer for the device-build scope: it
// resolves against the turn_approvals table, which the ask_user answer flow
// (prompts.go) grants. A grant recorded there is a real user yes; absence is
// not a no, it is not-yet-answered — the answerer reports delivery failure so
// the seam does not convert silence into rejection.
func deviceBuildAnswerer(st *store.Store, projectID int64) approval.Answerer {
	return func(req approval.Request) (bool, error) {
		if st == nil {
			return false, errNoStoreAvailable{}
		}
		if st.HasApproval(req.PromptID, store.ScopeDeviceBuild) {
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

// buildApprovalSeam assembles the device-build seam for one prompt: ask
// policy, the turn-approvals answerer, and the session-log audit.
func (h *PromptHandler) buildApprovalSeam(lg *sessionlog.Log, projectID, promptID int64) *approval.Seam {
	seam := approval.New(approval.PolicyAsk, approvalAudit(lg, promptID))
	seam.Register(deviceBuildAnswerer(h.store, projectID))
	return seam
}

// WantsDeviceBuild re-exports the orchestrator's scanner for the answer
// handler so this file is the single place the H4 vocabulary is defined.
var WantsDeviceBuild = orchestrator.WantsDeviceBuild
