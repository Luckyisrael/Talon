package api

import (
	"log"

	"github.com/luckyisrael/talon/backend/internal/harness/events"
	"github.com/luckyisrael/talon/backend/internal/harness/sessionlog"
	"github.com/luckyisrael/talon/backend/internal/hooks"
)

// appendHookRecord appends one W10 hook execution to the project's session
// log (hook/exec): the session-log half of the always-on server-log line
// the hooks package prints for the same execution. Same open-append
// discipline as approvalAudit — api-layer events (SessionStart,
// UserPromptSubmit, SubagentStart/Stop) fire once per request or turn, so
// opening per record costs nothing. A nil log (no store, open failure) is
// quiet, mirroring approvalAudit: audit trouble never breaks the caller.
func appendHookRecord(h *PromptHandler, projectID int64, rec hooks.ExecRecord) {
	appendHookExecToLog(openSessionLog(h.store, projectID), projectID, rec)
}

// appendHookExecToLog writes the row to an already-open session log.
func appendHookExecToLog(lg *sessionlog.Log, projectID int64, rec hooks.ExecRecord) {
	if lg == nil {
		return
	}
	hp := events.HookPayload{
		Event:      string(rec.Event),
		Hook:       rec.Hook,
		Decision:   rec.Decision.Decision,
		Reason:     rec.Decision.Reason,
		DurationMS: rec.Took.Milliseconds(),
	}
	if rec.Err != nil {
		hp.Error = rec.Err.Error()
	}
	if _, err := lg.Append(events.HookExec, hp); err != nil {
		log.Printf("hook audit (project %d): append: %v", projectID, err)
	}
}
