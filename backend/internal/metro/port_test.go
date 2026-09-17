package metro

import "testing"

// Unknown sessions must report cleanly: callers launch without a bundle URL
// rather than fabricate one from a stale port.
func TestPortForUnknownSession(t *testing.T) {
	if port, ok := PortForSession(-999999); ok || port != 0 {
		t.Errorf("unknown session should report (0, false), got (%d, %v)", port, ok)
	}
}
