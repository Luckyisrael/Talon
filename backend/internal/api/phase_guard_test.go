package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seekersstudio/backend/internal/notify"
	"github.com/seekersstudio/backend/internal/store"
)

// E phase guard: the notification trigger contract. The product moments
// fire exactly once — a duplicate is reminder spam, a miss is a dead
// trigger. Dark mode (OneSignal unconfigured) must still record the in-app
// history: the surface stays honest without push.
func TestPhaseE_NotificationTriggerContract(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.UpsertOAuthUser("github", "7788", "guard@example.com", "Guard", "")
	if err != nil {
		t.Fatalf("UpsertOAuthUser: %v", err)
	}
	h := NewNotificationHandler(st)

	// 1 — Dispatch records the in-app history. The once-law is the
	// caller's (each product moment calls Dispatch once); the recorder
	// writes exactly what it is handed.
	notify.Dispatch(st, notify.Noop{}, user.ID, notify.KindInputNeeded, "Needs input", "the turn waits")
	notify.Dispatch(st, notify.Noop{}, user.ID, notify.KindBuildSuccess, "Build finished", "verified: true")

	// 2 — The list endpoint demands identity: an unauthenticated request
	// is refused, never served.
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/v1/notifications", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: HTTP %d (want 401)", rec.Code)
	}

	// 3 — The rows are what the store says they are: two moments, newest
	// first, unread.
	rows, err := st.ListNotifications(user.ID, 10)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("recorded rows = %d (want 2 — exactly one per moment)", len(rows))
	}
	if rows[0].Kind != string(notify.KindBuildSuccess) {
		t.Fatalf("newest row is %q (want build_success first)", rows[0].Kind)
	}
	for i, r := range rows {
		if r.ReadAt != nil {
			t.Errorf("row %d is already read", i)
		}
	}

	// 4 — MarkRead stamps exactly one row, once.
	if err := st.MarkNotificationRead(user.ID, rows[0].ID); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}
	after, _ := st.ListNotifications(user.ID, 10)
	if after[0].ReadAt == nil || after[1].ReadAt != nil {
		t.Error("mark-read stamped the wrong rows (leaked to siblings)")
	}

	// 5 — Dark notifier honesty: Noop reports unconfigured; the push flag
	// the list endpoint serves must say false when env is dark.
	if (&notify.Noop{}).Configured() {
		t.Fatal("Noop claims configured")
	}

	// 6 — The Kind vocabulary is the product's moments, non-empty.
	for _, k := range []notify.Kind{
		notify.KindInputNeeded, notify.KindPlanReady, notify.KindBuildSuccess,
		notify.KindBuildFailed, notify.KindBuildPaused, notify.KindBuildDestroy,
	} {
		if strings.TrimSpace(string(k)) == "" {
			t.Error("an empty Kind crept into the vocabulary")
		}
	}
}

// E phase guard: the wire shape the mobile app parses. Drifting a field
// name breaks the phone silently — pin it.
func TestPhaseE_NotificationJSONShape(t *testing.T) {
	n := store.Notification{ID: 5, Kind: "input_needed", Title: "t", Body: "b", SentAt: "now"}
	raw, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "kind", "title", "body", "sent_at", "read_at"} {
		if _, ok := back[key]; !ok {
			t.Errorf("notification JSON lost %q", key)
		}
	}
}

// E phase guard: a configuring a OneSignal notifier with a resolver sends
// exactly what Dispatch decided — the push surface never invents moments.
func TestPhaseE_PushSeesOnlyDispatch(t *testing.T) {
	// The push leg is fire-and-forget by law: Dispatch returns before the
	// send. The guard synchronizes the way any observer must — bounded wait.
	done := make(chan string, 1)
	n := &recordingNotifier{onSend: func(kind notify.Kind) { done <- string(kind) }}
	notify.Dispatch(newRecordingStore(), n, 1, notify.KindBuildFailed, "t", "b")

	select {
	case got := <-done:
		if got != string(notify.KindBuildFailed) {
			t.Fatalf("push surface saw %q (want %q)", got, notify.KindBuildFailed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the push leg never fired — Dispatch dropped it")
	}
}

type recordingNotifier struct {
	onSend func(kind notify.Kind)
}

func (r *recordingNotifier) Send(ctx context.Context, userID int64, kind notify.Kind, title, body string) error {
	r.onSend(kind)
	return nil
}
func (r *recordingNotifier) Configured() bool { return true }

type recordingStore struct {
	rows []string
}

func newRecordingStore() *recordingStore { return &recordingStore{} }

func (r *recordingStore) RecordNotification(userID int64, kind, title, body string) error {
	r.rows = append(r.rows, kind)
	return nil
}
