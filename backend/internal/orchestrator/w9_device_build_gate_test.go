package orchestrator

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/harness/approval"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// W9.2: the device-build gate is one approval.Request when a seam is
// composed — one audit event per decision, turn_approvals still the binding
// record (the seam's answerer reads it), the session's mode composed on
// top. A nil seam keeps the bare table read (the pre-W9 path pinned by
// TestBuildApkGuardrailApprovalBinding).

type gateAudit struct {
	mu sync.Mutex
	rs []approval.AuditRecord
}

func (g *gateAudit) sink(r approval.AuditRecord) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rs = append(g.rs, r)
}

func (g *gateAudit) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.rs)
}

func (g *gateAudit) last() approval.AuditRecord {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.rs) == 0 {
		return approval.AuditRecord{}
	}
	return g.rs[len(g.rs)-1]
}

// deviceGateFixture is a store + project + prompt-scoped session whose
// WantsBuild is off, so the gate alone decides.
func deviceGateFixture(t *testing.T) (*Orchestrator, *SessionState, *store.Store, int64) {
	t.Helper()
	st, err := store.New(t.TempDir() + "/gate.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, err := st.CreateUser("gate@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(u.ID, "gate", "1.0.0", "gate-slug", "com.test.gate", "", "")
	if err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{store: st}
	sess := NewSession(77, p.ID, t.TempDir(), events.NewSessionEmitter(77, 128))
	sess.SetBuildRequested(false)
	return o, sess, st, p.ID
}

// gateSeam mirrors the api composition: PolicyAsk, audit captured, and an
// answerer that resolves turn_approvals — the same binding table the nil
// seam path reads.
func gateSeam(audit *gateAudit, st *store.Store, promptID int64) *approval.Seam {
	seam := approval.New(approval.PolicyAsk, audit.sink)
	// Mirrors api's turnAnswerer: granted → allow; not granted → a
	// delivery-failure error, which the seam reads as Unavailable (nobody
	// could answer yet) rather than a declined answer (Rejected).
	seam.Register(func(approval.Request) (bool, error) {
		if !st.HasApproval(promptID, store.ScopeDeviceBuild) {
			return false, errors.New("no device_build approval recorded for this turn")
		}
		return true, nil
	})
	return seam
}

func TestDeviceBuildGateAuditsThroughSeam(t *testing.T) {
	o, sess, st, projID := deviceGateFixture(t)
	audit := &gateAudit{}
	seam := gateSeam(audit, st, 77)
	opts := RunOptions{Approval: seam, PermissionMode: approval.ModeAsk}

	// No grant: the answerer refuses via the seam — one Unavailable audit
	// event, gate refuses (same verdict as the table read).
	if d := o.guardrail(sess, "build_apk", json.RawMessage(`{}`), opts); d.Allowed {
		t.Fatal("no grant must refuse even through the seam")
	}
	if audit.count() != 1 || audit.last().Outcome != approval.Unavailable {
		t.Fatalf("want exactly one Unavailable audit event, got %d (%+v)", audit.count(), audit.last())
	}

	// Granted in turn_approvals: seam allows, exactly one AllowedOnce event.
	if err := st.GrantApproval(projID, 77, store.ScopeDeviceBuild); err != nil {
		t.Fatal(err)
	}
	if d := o.guardrail(sess, "build_apk", json.RawMessage(`{}`), opts); !d.Allowed {
		t.Fatalf("granted approval must unlock through the seam, got %+v", d)
	}
	if audit.count() != 2 || audit.last().Outcome != approval.AllowedOnce {
		t.Fatalf("want one more AllowedOnce audit event, got %d (%+v)", audit.count(), audit.last())
	}
}

func TestDeviceBuildGateComposesMode(t *testing.T) {
	t.Run("read-only refuses even a granted approval", func(t *testing.T) {
		o, sess, st, projID := deviceGateFixture(t)
		if err := st.GrantApproval(projID, 77, store.ScopeDeviceBuild); err != nil {
			t.Fatal(err)
		}
		audit := &gateAudit{}
		opts := RunOptions{Approval: gateSeam(audit, st, 77), PermissionMode: approval.ModeReadOnly}
		if d := o.guardrail(sess, "build_apk", json.RawMessage(`{}`), opts); d.Allowed {
			t.Fatal("read-only posture must refuse the device build")
		}
		if audit.count() != 1 || !strings.Contains(audit.last().Reason, "read-only") {
			t.Fatalf("want one structured read-only refusal, got %d (%+v)", audit.count(), audit.last())
		}
	})

	t.Run("auto still requires the per-turn consent record", func(t *testing.T) {
		o, sess, st, _ := deviceGateFixture(t)
		audit := &gateAudit{}
		opts := RunOptions{Approval: gateSeam(audit, st, 77), PermissionMode: approval.ModeAuto}
		if d := o.guardrail(sess, "build_apk", json.RawMessage(`{}`), opts); d.Allowed {
			t.Fatal("auto must not build an APK the user never green-lit")
		}
	})

	t.Run("nil seam falls back to the bare table read", func(t *testing.T) {
		o, sess, st, projID := deviceGateFixture(t)
		if err := st.GrantApproval(projID, 77, store.ScopeDeviceBuild); err != nil {
			t.Fatal(err)
		}
		if d := o.guardrail(sess, "build_apk", json.RawMessage(`{}`), RunOptions{}); !d.Allowed {
			t.Fatal("nil-seam path must keep the pre-W9 table read")
		}
	})
}
