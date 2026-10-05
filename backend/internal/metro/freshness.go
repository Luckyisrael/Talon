package metro

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// G-10 — the file map can lie.
//
// Metro's watcher can miss a file the agent just wrote. The map then stays
// wrong until something forces a rescan, and the device's next bundle
// request comes back HTTP 500 — which the dev client surfaces as a native
// crash dialog, not as an error in the product. Every gate can read green
// (tsc, eslint, expo export, gradle) while the live bundler disagrees,
// because they are not the process that will serve the launch. The observed
// incident: a file created at 18:01 sat invisible for 14 minutes, the phone
// crashed three times, and an mtime-only touch brought it back to 200
// instantly.
//
// So the freshness of the map is checked at the source — a real bundle
// request, the same one the dev client makes at launch — and healed where
// the miss is still cheap to absorb: right after a turn writes files, in the
// background, while the user is reading the agent's reply. Never on the
// launch path, where the only honest answer to a stale map is a crash.
//
// The ladder is probe → nudge → restart:
//
//	probe   the entry bundle. Healthy is the common case and costs one
//	        cached request — and it warms the bundle, so the phone's first
//	        launch after a turn is already built.
//	nudge   bump mtimes across the workspace's own sources. A watcher that
//	        dropped a create event sees the tree again; content is untouched
//	        so nothing rebuilds that was not going to anyway.
//	restart the bundler, on the same port (the adb reverse and the relay
//	        captured it once) with its emitter carried over (subscribers
//	        stay attached). Last resort, because it throws away cache.
//
// The steps are injected so the ladder itself is unit-testable without a
// process: probe, touch, restart.

// probeTimeout bounds one bundle request. A cold build after a large turn has
// been measured at ~140s; this runs in the background, so the generous bound
// buys a warm bundle rather than a false "bundler is stale".
const probeTimeout = 180 * time.Second

// freshMu serializes heal ladders. Two turns finishing together must not race
// a restart; the second simply finds the first's result.
var freshMu sync.Mutex

// EnsureFresh verifies (and, when needed, heals) the bundler for a session.
// Safe to call from any goroutine: no-op when the session has no Metro.
// Returns once the map is known good or the last resort has been tried.
func EnsureFresh(sessionID int64) {
	freshMu.Lock()
	defer freshMu.Unlock()

	sessionsMu.Lock()
	s := sessions[sessionID]
	sessionsMu.Unlock()
	if s == nil {
		return // nothing bundling for this session — nothing can be stale
	}

	client := &http.Client{Timeout: probeTimeout}
	if err := ensureFresh(
		func() error { return s.probeBundle(client) },
		func() { s.touchSources() },
		func() bool { return s.restart(sessionID) },
	); err != nil {
		log.Printf("metro %d: bundle still unbuildable after heal ladder: %v", sessionID, err)
	}
}

// ensureFresh runs the ladder. It returns nil as soon as a probe passes and
// the last probe error otherwise, so the caller can log a defeat honestly
// rather than claim freshness it never observed.
func ensureFresh(probe func() error, touch func(), restart func() bool) error {
	err := probe()
	if err == nil {
		return nil
	}
	log.Printf("metro: bundle probe failed (%v) — nudging the file map", err)

	touch()
	if err = probe(); err == nil {
		log.Printf("metro: bundle healthy after rescan nudge")
		return nil
	}
	log.Printf("metro: still failing after nudge (%v) — restarting the bundler", err)

	if !restart() {
		return err
	}
	return probe()
}

// bundleURL is the request the dev client makes at launch — the exact
// surface where a stale map becomes a native crash dialog. Probing any
// other surface would prove something the phone never asks for.
func (s *Server) bundleURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/index.bundle?platform=android&dev=true&hot=false&lazy=true"+
		"&transform.engine=hermes&transform.bytecode=1&transform.routerRoot=app", s.port)
}

// probeBundle asks Metro to build the entry bundle. Non-200 is the failure
// that would have reached the phone; the small body tail rides along because
// Metro's 500 body names the module it could not resolve — the one detail
// that says whether the map is stale or the code is actually broken.
func (s *Server) probeBundle(client *http.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.bundleURL(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("bundler unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil
	}
	tail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("bundle HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(tail)))
}

// skipDirs are trees Metro never bundles and a nudge has no business
// rewriting: dependencies, VCS state, build output, and this server's own
// metadata. node_modules alone is ~100k files — walking it would turn a
// background nudge into a stall.
var skipDirs = map[string]bool{
	"node_modules":  true,
	".git":          true,
	".expo":         true,
	".expo-bundle":  true,
	".gradle":       true,
	".talon":        true,
	"android":       true,
	"ios":           true,
	"dist":          true,
	"build":         true,
	"coverage":      true,
}

// touchSources bumps mtimes across the workspace's own sources so a watcher
// that missed a create sees the tree again. Content is never modified — git
// does not track mtimes and Metro keys its transform cache on content, so
// the nudge re-discovers files without rebuilding the ones that did not
// change. Failures are ignored on purpose: the probe after this decides
// whether it worked.
func (s *Server) touchSources() {
	now := time.Now()
	touched := 0
	_ = filepath.WalkDir(s.workspaceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip it, keep walking
		}
		name := d.Name()
		if d.IsDir() {
			if skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if os.Chtimes(path, now, now) == nil {
				touched++
			}
		}
		return nil
	})
	log.Printf("metro: nudged %d source files under %s", touched, s.workspaceDir)
}

// restart kills this bundler and starts one on the same port, carrying the
// emitter over. It reports whether a Metro now exists for the session: false
// means the session went away underneath us (or a replacement already
// started), in which case there is nothing to heal and no process to leave
// orphaned.
func (s *Server) restart(sessionID int64) bool {
	sessionsMu.Lock()
	if sessions[sessionID] != s {
		// Torn down — or replaced by a fresh Start — while we probed.
		sessionsMu.Unlock()
		return false
	}
	delete(sessions, sessionID)
	sessionsMu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
	exec.Command("adb", "-s", s.target, "reverse", "--remove", fmt.Sprintf("tcp:%d", s.port)).Run()

	if _, err := startAt(sessionID, s.workspaceDir, s.target, s.port, s.events); err != nil {
		log.Printf("metro %d: restart failed: %v", sessionID, err)
		return false
	}
	log.Printf("metro %d: bundler restarted on port %d", sessionID, s.port)
	return true
}
