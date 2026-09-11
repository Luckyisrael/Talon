package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

func call(name, path string) ai.ToolUseBlock {
	in, _ := json.Marshal(map[string]string{"path": path})
	return ai.ToolUseBlock{Name: name, Input: in}
}

func TestBatchConflicts(t *testing.T) {
	tests := []struct {
		name  string
		calls []ai.ToolUseBlock
		want  map[int]string
	}{
		{
			name: "no writes, no conflicts",
			calls: []ai.ToolUseBlock{
				call("read_file", "app/a.tsx"),
				call("read_file", "app/b.tsx"),
			},
			want: map[int]string{},
		},
		{
			name: "write then write same path flags both",
			calls: []ai.ToolUseBlock{
				call("write_file", "app/a.tsx"),
				call("write_file", "app/a.tsx"),
			},
			want: map[int]string{0: "app/a.tsx", 1: "app/a.tsx"},
		},
		{
			name: "write then delete same path flags both",
			calls: []ai.ToolUseBlock{
				call("write_file", "app/a.tsx"),
				call("delete_file", "app/a.tsx"),
			},
			want: map[int]string{0: "app/a.tsx", 1: "app/a.tsx"},
		},
		{
			name: "read before write is not a conflict",
			calls: []ai.ToolUseBlock{
				call("read_file", "app/a.tsx"),
				call("write_file", "app/a.tsx"),
			},
			want: map[int]string{},
		},
		{
			name: "distinct paths never conflict",
			calls: []ai.ToolUseBlock{
				call("write_file", "app/a.tsx"),
				call("write_file", "app/b.tsx"),
			},
			want: map[int]string{},
		},
		{
			name: "three writes to one path flag all three",
			calls: []ai.ToolUseBlock{
				call("write_file", "app/a.tsx"),
				call("edit_file", "app/a.tsx"),
				call("multi_edit", "app/a.tsx"),
			},
			want: map[int]string{0: "app/a.tsx", 1: "app/a.tsx", 2: "app/a.tsx"},
		},
		{
			name: "malformed input is skipped",
			calls: []ai.ToolUseBlock{
				{Name: "write_file", Input: json.RawMessage(`{"wrong":"field"}`)},
				call("write_file", "app/a.tsx"),
			},
			want: map[int]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := batchConflicts(tt.calls)
			if len(got) != len(tt.want) {
				t.Fatalf("batchConflicts() = %v, want %v", got, tt.want)
			}
			for i, path := range tt.want {
				if got[i] != path {
					t.Errorf("batchConflicts()[%d] = %q, want %q", i, got[i], path)
				}
			}
		})
	}
}

func TestSessionVerifyAttempts(t *testing.T) {
	s := newTestSession(t)
	if got := s.GetVerifyAttempts(); got != 0 {
		t.Fatalf("fresh session verify attempts = %d, want 0", got)
	}
	if got := s.IncrementVerifyAttempt(); got != 1 {
		t.Errorf("IncrementVerifyAttempt() = %d, want 1", got)
	}
	if got := s.IncrementVerifyAttempt(); got != 2 {
		t.Errorf("second IncrementVerifyAttempt() = %d, want 2", got)
	}
	if got := s.GetVerifyAttempts(); got != 2 {
		t.Errorf("GetVerifyAttempts() = %d, want 2", got)
	}
}
