package api

import (
	"testing"

	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// Build receipts: record → pending → inject-once. A finished build whose
// turn is gone must surface exactly once in the next turn's context, never
// zero times (lost) and never twice (nagging).
func TestBuildReceiptRoundTrip(t *testing.T) {
	st, err := store.New(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("receipt@local", "x")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(user.ID, "Receipts", "v1", "receipts", "com.talon.receipts", "com.talon.receipts", "receiptapp")
	if err != nil {
		t.Fatal(err)
	}

	bs := &buildSession{projectID: proj.ID, promptID: 7, store: st}
	bs.recordBuildReceipt(tools.ToolResult{Content: "APK built successfully: /tmp/app-debug.apk"}, nil)
	bs.recordBuildReceipt(tools.ToolResult{Content: "gradle blew up", IsError: true}, nil)

	pending, err := st.PendingBuildReceipts(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending receipts, got %d", len(pending))
	}
	if pending[0].Status != "built" || pending[0].ApkPath != "/tmp/app-debug.apk" {
		t.Errorf("built receipt should carry status + apk path: %+v", pending[0])
	}
	if pending[1].Status != "failed" || pending[1].PromptID != 7 {
		t.Errorf("failed receipt should carry status + prompt attribution: %+v", pending[1])
	}

	if err := st.AckBuildReceipts([]int64{pending[0].ID, pending[1].ID}); err != nil {
		t.Fatal(err)
	}
	again, err := st.PendingBuildReceipts(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("acked receipts must not resurface, got %d", len(again))
	}
}
