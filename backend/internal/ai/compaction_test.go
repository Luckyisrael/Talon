package ai

import (
	"context"
	"strings"
	"testing"
)

// The compaction plan must drop the oldest span and land on a boundary that
// does not split a tool_use from its result — and it must agree with Compact
// about exactly which turns go.
func TestPlanCompactionKeepsCleanBoundary(t *testing.T) {
	msgs := make([]Message, 0, 30)
	msgs = append(msgs, UserText("build the wallet screen"))
	pair := strings.Repeat("the deposit flow needs a signed challenge before it commits ", 8)
	for i := 0; i < 14; i++ {
		msgs = append(msgs,
			AssistantText(pair+" step "+itoa(i)),
			ToolResults([]Block{{Type: "tool_result", Text: pair + " ok " + itoa(i)}}))
		if i%4 == 3 {
			// A plain user turn — a steer or added context. isCleanBoundary only
			// accepts these, so a real conversation needs them or nothing can be
			// cut cleanly.
			msgs = append(msgs, UserText("keep the wallet screen on the first tab"))
		}
	}

	// A budget that keeps roughly the second half: enough headroom that the
	// boundary snapping is exercised, small enough that a real span is dropped.
	keepFrom, dropped := PlanCompaction(msgs, 1800)
	if keepFrom == 0 || len(dropped) == 0 {
		t.Fatalf("want a real plan, got keepFrom=%d dropped=%d", keepFrom, len(dropped))
	}
	if keepFrom >= len(msgs) {
		t.Fatalf("keepFrom %d must be inside the conversation of %d", keepFrom, len(msgs))
	}
	// Whatever survived must not start with a tool_result — an orphaned answer
	// has no call, and both providers reject it.
	if msg := msgs[keepFrom]; msg.Role == "user" && len(msg.Blocks) > 0 && msg.Blocks[0].Type == "tool_result" {
		t.Errorf("the keep boundary lands on an orphaned tool_result")
	}
	// The dropped span is exactly what precedes the keep boundary, first turn
	// (the original request) excepted.
	if got, want := len(dropped), keepFrom-1; got != want {
		t.Errorf("dropped span length: got %d, want %d", got, want)
	}
}

// A conversation that already fits must not be dropped at all.
func TestPlanCompactionSkipsFittingConversation(t *testing.T) {
	msgs := []Message{UserText("hi"), AssistantText("hello")}
	if keepFrom, dropped := PlanCompaction(msgs, 100000); keepFrom != 0 || len(dropped) != 0 {
		t.Errorf("a fitting conversation must not be planned for compaction: keepFrom=%d dropped=%d", keepFrom, len(dropped))
	}
}

// The shape gate rejects a rambling summary and keeps the static fallback — the
// next request can only read a checkpoint that kept the sections.
func TestLooksLikeCheckpoint(t *testing.T) {
	good := "## Primary Request and Intent\n- build a wallet\n\n## Key Technical Concepts\n- go"
	if !looksLikeCheckpoint(good) {
		t.Errorf("a summary with the sections must be accepted")
	}
	ramble := strings.Repeat("the user wanted many things and also ", 20)
	if looksLikeCheckpoint(ramble) {
		t.Errorf("a summary that lost the sections must be rejected")
	}
}

// A span shorter than the floor is not worth a model call, and a span with
// nothing readable has nothing to summarize — both must report no summary and
// send the caller to the static fallback.
func TestSummarizeSkipsUnusableSpans(t *testing.T) {
	c := &Client{protocol: "openai"}

	if got, err := c.SummarizeForCompaction(context.Background(), "", []Message{
		AssistantText("one"), AssistantText("two"), // below minDroppedToSummarize
	}); err != nil || got != "" {
		t.Errorf("a short span must not be summarized: got %q err %v", got, err)
	}

	if got, err := c.SummarizeForCompaction(context.Background(), "", []Message{
		{Role: "user", Blocks: []Block{{Type: "tool_result", Text: ""}}},
		{Role: "user", Blocks: []Block{{Type: "tool_result", Text: "  "}}},
		{Role: "user", Blocks: []Block{{Type: "tool_result", Text: ""}}},
		{Role: "user", Blocks: []Block{{Type: "tool_result", Text: ""}}},
	}); err != nil || got != "" {
		t.Errorf("an unreadable span must not be summarized: got %q err %v", got, err)
	}
}

// The flattening keeps every readable turn, substitutes a tool turn's results
// for its (absent) text, and never emits an empty message.
func TestFlatPromptMessages(t *testing.T) {
	got, ok := flatPromptMessages([]Message{
		AssistantText("first"),
		{Role: "user", Blocks: []Block{{Type: "tool_result", Text: "result body"}}},
		{Role: "user", Blocks: []Block{{Type: "tool_result", Text: ""}}}, // unreadable
		AssistantText("last"),
	})
	if !ok {
		t.Fatal("a span with readable turns must report ok")
	}
	want := []PromptMessage{
		{Role: "assistant", Content: "first"},
		{Role: "user", Content: "result body"},
		{Role: "assistant", Content: "last"},
	}
	if len(got) != len(want) {
		t.Fatalf("flatten: got %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("flatten[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
