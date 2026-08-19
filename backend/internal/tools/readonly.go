package tools

// Read-only tools have no side effects on the workspace, so the orchestrator is
// free to run them concurrently inside a single turn. Everything that mutates
// files or shells out runs one at a time.

func (t *ReadFile) ReadOnly() bool     { return true }
func (t *ListFiles) ReadOnly() bool    { return true }
func (t *GrepCodebase) ReadOnly() bool { return true }
func (t *ReadSkill) ReadOnly() bool    { return true }
