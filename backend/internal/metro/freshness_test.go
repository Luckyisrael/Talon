package metro

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// G-10's ladder is the contract: probe first (healthy costs no nudge and no
// restart), nudge only after a failed probe, restart only after a nudge that
// did not help — and a defeat is returned rather than swallowed, so a caller
// can log that freshness was never observed instead of claiming it.
func TestEnsureFreshLadder(t *testing.T) {
	boom := errors.New("bundle HTTP 500: Unable to resolve module ./watchlist-row")
	cases := []struct {
		name         string
		probes       []error // in order; the last entry repeats if probed again
		restartOK    bool
		wantTouch    bool
		wantRestart  bool
		wantProbes   int
		wantErrIsNil bool
	}{
		{
			name:         "healthy probe stops at probe one",
			probes:       []error{nil},
			wantTouch:    false,
			wantRestart:  false,
			wantProbes:   1,
			wantErrIsNil: true,
		},
		{
			name:         "nudge heals a stale map",
			probes:       []error{boom, nil},
			wantTouch:    true,
			wantRestart:  false,
			wantProbes:   2,
			wantErrIsNil: true,
		},
		{
			name:         "restart only when the nudge was not enough",
			probes:       []error{boom, boom, nil},
			restartOK:    true,
			wantTouch:    true,
			wantRestart:  true,
			wantProbes:   3,
			wantErrIsNil: true,
		},
		{
			name:         "defeat is reported, not hidden",
			probes:       []error{boom, boom, boom},
			restartOK:    true,
			wantTouch:    true,
			wantRestart:  true,
			wantProbes:   3,
			wantErrIsNil: false,
		},
		{
			name:         "a session torn down mid-ladder ends it honestly",
			probes:       []error{boom, boom},
			restartOK:    false,
			wantTouch:    true,
			wantRestart:  true,
			wantProbes:   2,
			wantErrIsNil: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probes := 0
			touched, restarted := false, false

			probe := func() error {
				i := probes
				probes++
				if i >= len(tc.probes) {
					i = len(tc.probes) - 1
				}
				return tc.probes[i]
			}
			touch := func() { touched = true }
			restart := func() bool { restarted = true; return tc.restartOK }

			err := ensureFresh(probe, touch, restart)

			if probes != tc.wantProbes {
				t.Errorf("probed %d times, want %d", probes, tc.wantProbes)
			}
			if touched != tc.wantTouch {
				t.Errorf("touched=%v, want %v", touched, tc.wantTouch)
			}
			if restarted != tc.wantRestart {
				t.Errorf("restarted=%v, want %v", restarted, tc.wantRestart)
			}
			if (err == nil) != tc.wantErrIsNil {
				t.Errorf("err=%v, want nil=%v", err, tc.wantErrIsNil)
			}
			if err != nil && !strings.Contains(err.Error(), "500") {
				t.Errorf("defeat lost the probe error: %v", err)
			}
		})
	}
}

// The probe must be the request the dev client makes at launch — probing any
// other surface would prove something the phone never asks for.
func TestBundleURLIsTheLaunchRequest(t *testing.T) {
	s := &Server{port: 41234}
	u := s.bundleURL()
	for _, want := range []string{
		"http://127.0.0.1:41234/index.bundle",
		"platform=android",
		"dev=true",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("bundle URL %q missing %q", u, want)
		}
	}
}

// The nudge re-discovers the tree Metro watches and leaves everything it
// does not bundle alone — node_modules is ~100k files, and rewriting them
// would turn a background heal into a stall.
func TestTouchSourcesBumpsSourcesNotDependencies(t *testing.T) {
	ws := t.TempDir()
	src := filepath.Join(ws, "app")
	if err := os.MkdirAll(filepath.Join(src), 0o755); err != nil {
		t.Fatal(err)
	}
	nm := filepath.Join(ws, "node_modules", "dep")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(src, "watchlist.tsx")
	dep := filepath.Join(nm, "index.js")
	for _, f := range []string{source, dep} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	past := time.Now().Add(-time.Hour)
	for _, f := range []string{source, dep} {
		if err := os.Chtimes(f, past, past); err != nil {
			t.Fatal(err)
		}
	}

	(&Server{workspaceDir: ws}).touchSources()

	assertFresh := func(f string) {
		t.Helper()
		fi, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(fi.ModTime()) > time.Minute {
			t.Errorf("%s mtime not bumped: %s", f, fi.ModTime())
		}
	}
	assertFresh(source)

	fi, err := os.Stat(dep)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(fi.ModTime()) < time.Minute {
		t.Errorf("node_modules file was rewritten: %s", fi.ModTime())
	}
}
