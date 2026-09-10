package api

import (
	"testing"
	"time"
)

// The reaper's windows are the phase contract: 10 minutes to pause, 30 to
// destroy. These are money and user-trust numbers — the guard pins them.
// (The clock itself — TouchSession/IdleSessions against aged rows — is
// proven in the store package, next to the SQL it tests.)
func TestReaperWindows(t *testing.T) {
	if ReaperPauseAfter != 10*time.Minute {
		t.Errorf("fault: pause window %s, want 10m", ReaperPauseAfter)
	}
	if ReaperDestroyAfter != 30*time.Minute {
		t.Errorf("fault: destroy window %s, want 30m", ReaperDestroyAfter)
	}
}
