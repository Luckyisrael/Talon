package lsp

import (
	"os"
	"strings"
)

// ── W5: language-server ground truth ────────────────────────────────
//
// An instrument the harness listens to: a framed JSON-RPC client to
// typescript-language-server / gopls, a per-workspace process fleet with
// restart-on-crash, LRU and an idle reaper (the device reaper's pattern),
// and the lsp_* tools + verify gate zero built on top. Everything is
// fail-open-availability: a missing server is structured Unavailable
// ("LSP_UNAVAILABLE") and never blocks a build — the same split sandbox
// uses (fail-open on availability, fail-closed only on truth).

// Enabled reads TALONSTUDIO_LSP with the same truthy set every other flag
// in the codebase uses. Off (the deployment default) means the tools never
// register, the verify ladder never gains a gate, and no process spawns.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_LSP"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}
