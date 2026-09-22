package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/events"
	hevents "github.com/luckyisrael/talon/backend/internal/harness/events"
	"github.com/luckyisrael/talon/backend/internal/harness/subagent"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// W1.5 (H9 eval gate): "fork child's first request contains the parent prefix
// and cites no tools the parent lacks." The runner is driven with the loop
// seam so the exact first request is observable, and the session-log fork is
// asserted end to end: the child log is a byte-identical copy of the parent
// prefix plus the lineage event plus the child's own turn, lineage lands in
// BOTH logs, and the child's own messages stay derivation-visible under an
// inherited compaction shadow (session seqs are global, so they always land
// above the boundary).
func TestDelegateForkSeedsChildFromParentPrefix(t *testing.T) {
	// Derive dark: the parent prefix resolves through the legacy table — the
	// same history the parent's next turn would replay.
	t.Setenv("TALONSTUDIO_SESSIONLOG", "")
	st, err := store.New(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("fork@local", "x")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(user.ID, "Fork", "v1", "fork", "com.talon.fork", "com.talon.fork", "forkapp")
	if err != nil {
		t.Fatal(err)
	}
	h := NewPromptHandler(st, nil, t.TempDir(), "", orchestrator.New(nil, st, nil), nil, nil, nil)

	// Parent conversation: four turns dual-written to table and log, with a
	// compaction shadow after turn two — the fork must inherit it.
	parentLog, err := st.SessionLog(fmt.Sprintf("project-%d", proj.ID))
	if err != nil {
		t.Fatalf("parent log: %v", err)
	}
	for i, turn := range []struct{ role, text string }{
		{"user", "analyze the liquidity pools"},
		{"assistant", "pools analyzed: three venues"},
		{"user", "now compare the swap fees"},
		{"assistant", "fees compared: route X wins"},
	} {
		if err := st.AppendMessage(proj.ID, 1, turn.role,
			`[{"type":"text","text":"`+turn.text+`"}]`, 0, 0); err != nil {
			t.Fatalf("append: %v", err)
		}
		kind := hevents.UserMessage
		if turn.role == "assistant" {
			kind = hevents.AssistantMessage
		}
		if _, err := parentLog.Append(kind, hevents.MessagePayload{
			Role: turn.role, Blocks: []hevents.BlockPayload{{Type: "text", Text: turn.text}},
		}); err != nil {
			t.Fatalf("log append: %v", err)
		}
		if i == 1 {
			if last, ok := parentLog.EventAt(int(parentLog.Seq()) - 1); ok {
				if err := parentLog.Shadow(last.Seq, "Earlier turns summarized.", "compaction"); err != nil {
					t.Fatalf("shadow: %v", err)
				}
			}
		}
	}

	var gotHistory []ai.Message
	var gotDefs []ai.ToolDefinition
	r := &childRunner{
		build: func() *buildSession {
			return &buildSession{defs: []ai.ToolDefinition{
				{Name: "read_file"}, {Name: "shell"}, {Name: "list_children"},
			}}
		},
		newSession: func() *orchestrator.SessionState {
			return orchestrator.NewSession(0, proj.ID, t.TempDir(), events.NewSessionEmitter(0, 64))
		},
		systemPrompt: "child system",
		parentTools:  map[string]bool{"read_file": true, "list_children": true},
		forkHistory:  func() []ai.Message { return h.conversation(proj.ID) },
		log:          parentLog,
		st:           st,
		runLoop: func(_ context.Context, _ *orchestrator.SessionState, _ *buildSession,
			defs []ai.ToolDefinition, history []ai.Message, _ string) (*orchestrator.BuildOutcome, error) {
			gotHistory, gotDefs = history, defs
			return &orchestrator.BuildOutcome{FinalText: "fee report: route X is cheapest"}, nil
		},
	}
	svc := subagent.NewService(r, 2)

	pre := parentLog.Replay()
	res, err := svc.Spawn(context.Background(), subagent.Spec{
		Fork:     true,
		Prompt:   "compare against last week's fees",
		Label:    "fee-continuation",
		ParentID: fmt.Sprintf("project-%d", proj.ID),
	})
	if err != nil || res.Errored {
		t.Fatalf("fork spawn: err=%v result=%+v", err, res)
	}

	// Gate 1 — the first request contains the parent prefix, then the task.
	want := h.conversation(proj.ID)
	if len(gotHistory) != len(want)+1 {
		t.Fatalf("first request has %d messages, want %d (parent prefix + task)", len(gotHistory), len(want)+1)
	}
	for i := range want {
		if !reflect.DeepEqual(gotHistory[i], want[i]) {
			t.Fatalf("prefix message %d differs:\n got  %+v\n want %+v", i, gotHistory[i], want[i])
		}
	}
	task := gotHistory[len(gotHistory)-1]
	if task.Role != "user" || len(task.Blocks) == 0 ||
		!strings.Contains(task.Blocks[0].Text, "compare against last week") {
		t.Errorf("final message is not the fork task: %+v", task)
	}

	// Gate 2 — the child cites no tools the parent lacks (shell filtered).
	var names []string
	for _, d := range gotDefs {
		names = append(names, d.Name)
	}
	if !reflect.DeepEqual(names, []string{"read_file", "list_children"}) {
		t.Errorf("child tool set = %v, want a subset of the parent's (no shell)", names)
	}

	// Gate 3 — the parent log gains exactly the lineage event.
	after := parentLog.Replay()
	if len(after) != len(pre)+1 {
		t.Fatalf("parent log has %d events after fork, want %d (prefix + lineage only)", len(after), len(pre)+1)
	}
	if after[len(after)-1].Kind != hevents.SessionForked {
		t.Fatalf("parent's new event = %s, want session/forked", after[len(after)-1].Kind)
	}
	var lin hevents.ForkPayload
	if err := json.Unmarshal(after[len(after)-1].Payload, &lin); err != nil {
		t.Fatalf("lineage payload: %v", err)
	}
	if lin.ParentKey != fmt.Sprintf("project-%d", proj.ID) || lin.ChildKey == "" || lin.Label != "fee-continuation" {
		t.Errorf("lineage payload: %+v", lin)
	}
	if lin.FromSeq != pre[len(pre)-1].Seq {
		t.Errorf("FromSeq = %d, want the parent's last pre-fork seq %d", lin.FromSeq, pre[len(pre)-1].Seq)
	}

	// Gate 4 — child log = byte-identical prefix + lineage + task + report.
	childLog, err := st.SessionLog(lin.ChildKey)
	if err != nil {
		t.Fatalf("open child log: %v", err)
	}
	creplay := childLog.Replay()
	if len(creplay) != len(pre)+3 {
		t.Fatalf("child log has %d events, want %d (prefix + lineage + task + report)", len(creplay), len(pre)+3)
	}
	for i, e := range pre {
		if creplay[i].Kind != e.Kind || !bytes.Equal(creplay[i].Payload, e.Payload) {
			t.Fatalf("prefix event %d not byte-identical: got %s %s, want %s %s",
				i, creplay[i].Kind, creplay[i].Payload, e.Kind, e.Payload)
		}
	}
	if creplay[len(pre)].Kind != hevents.SessionForked {
		t.Errorf("child event after prefix = %s, want session/forked", creplay[len(pre)].Kind)
	}
	if taskRow := creplay[len(pre)+1]; taskRow.Kind != hevents.UserMessage ||
		!strings.Contains(string(taskRow.Payload), "compare against last week") {
		t.Errorf("child task row: %s %s", taskRow.Kind, taskRow.Payload)
	}
	if reportRow := creplay[len(pre)+2]; reportRow.Kind != hevents.AssistantMessage ||
		!strings.Contains(string(reportRow.Payload), "route X is cheapest") {
		t.Errorf("child report row: %s %s", reportRow.Kind, reportRow.Payload)
	}

	// Gate 5 — the child's own turn is derivation-visible even under the
	// inherited shadow: new rows carry global seqs above the boundary.
	foundTask := false
	for _, m := range childLog.DeriveMessages() {
		for _, b := range m.Blocks {
			if b.Text == "compare against last week's fees" {
				foundTask = true
			}
		}
	}
	if !foundTask {
		t.Error("child task is not derivation-visible under the inherited shadow")
	}

	// Regression — a plain (non-fork) spawn seeds nothing and logs nothing.
	pre2 := parentLog.Replay()
	res2, err2 := svc.Spawn(context.Background(), subagent.Spec{Prompt: "self-contained task", Label: "plain"})
	if err2 != nil || res2.Errored {
		t.Fatalf("plain spawn: err=%v result=%+v", err2, res2)
	}
	if len(gotHistory) != 1 || len(gotHistory[0].Blocks) == 0 ||
		gotHistory[0].Blocks[0].Text != "self-contained task" {
		t.Errorf("non-fork history = %d messages, want exactly the task", len(gotHistory))
	}
	if len(parentLog.Replay()) != len(pre2) {
		t.Errorf("non-fork spawn appended to the parent log")
	}
}

// delegate_fork must reach the model-visible catalog when delegation is
// opted in, and stay absent when it is not — the same TALONSTUDIO_SUBAGENTS
// opt-in that gates delegate (a pure addition rides the existing flag).
func TestDelegateForkRegistered(t *testing.T) {
	st, err := store.New(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("forkreg@local", "x")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(user.ID, "ForkReg", "v1", "forkreg", "com.talon.forkreg", "com.talon.forkreg", "forkapp")
	if err != nil {
		t.Fatal(err)
	}
	h := NewPromptHandler(st, nil, t.TempDir(), "", orchestrator.New(nil, st, nil), nil, nil, nil)

	t.Setenv("TALONSTUDIO_SUBAGENTS", "1")
	bs := h.newBuildSession(t.TempDir(), proj.ID, 0, "", nil)
	if _, ok := bs.reg.Get("delegate_fork"); !ok {
		t.Fatal("delegate_fork not registered — the model cannot seed a child from this conversation")
	}
	found := false
	for _, d := range bs.defs {
		if d.Name == "delegate_fork" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("delegate_fork missing from the model-visible tool definitions")
	}

	t.Setenv("TALONSTUDIO_SUBAGENTS", "")
	bs2 := h.newBuildSession(t.TempDir(), proj.ID, 0, "", nil)
	if _, ok := bs2.reg.Get("delegate_fork"); ok {
		t.Error("delegate_fork registered while delegation ships dark")
	}
}
