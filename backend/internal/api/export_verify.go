package api

import (
	"log"
	"net/http"

	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// enforceVerifyPolicy is W6's fail-closed shipping rule (laws 6/15):
// export/release refuses when the project's recorded verification state is
// refused, best-effort, or absent — an absent record is an unproven build,
// not a passing one. override:true is the named human key: it lets the
// release ship anyway and lands one verify_override row in audit_log naming
// the actor, the project, and the reason. Returns false after writing the
// error response; true means ship.
func (h *ExportHandler) enforceVerifyPolicy(w http.ResponseWriter, r *http.Request, project *store.Project, override bool) bool {
	found, st, err := h.store.GetProjectVerifyState(project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read this project's verification state")
		return false
	}
	reason := orchestrator.VerifyReleaseRefusal(found, st)
	if reason == "" {
		return true
	}
	if !override {
		writeError(w, http.StatusForbidden,
			"Release refused by verification policy: "+reason+
				". Re-run verification, or send override:true to ship anyway — the override is recorded in the audit log.")
		return false
	}
	if err := h.store.AuditLog(GetUserID(r.Context()), project.ID, "verify_override", "export", reason); err != nil {
		log.Printf("project %d: verify override audit failed: %v", project.ID, err)
	}
	return true
}
