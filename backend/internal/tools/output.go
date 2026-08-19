package tools

import (
	"bytes"
	"io"
	"os/exec"
)

// runCaptured runs a command while forwarding its combined stdout/stderr to
// sink as it is produced, and returns the full output. It replaces
// CombinedOutput so the user can watch what a build command is doing instead of
// waiting blind for the tool result. The returned bytes are identical to what
// CombinedOutput would have produced, so tool results are unchanged.
func runCaptured(cmd *exec.Cmd, sink io.Writer) ([]byte, error) {
	var buf bytes.Buffer
	out := io.Writer(&buf)
	if sink != nil {
		out = io.MultiWriter(&buf, sink)
	}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	return buf.Bytes(), err
}
