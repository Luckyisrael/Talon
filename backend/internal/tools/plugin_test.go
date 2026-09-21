package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubFetcher struct {
	data map[string][]byte
	err  map[string]error
}

func (s stubFetcher) FetchPinnedFile(pins map[string]string, plugin, rel string) ([]byte, error) {
	if err, ok := s.err[plugin+"/"+rel]; ok {
		return nil, err
	}
	if d, ok := s.data[plugin+"/"+rel]; ok {
		return d, nil
	}
	return nil, errPluginTest("not found")
}

type errPluginTest string

func (e errPluginTest) Error() string { return string(e) }

type stubPins struct {
	pins map[string]string
	err  error
}

func (s stubPins) ReadPins(root string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.pins, nil
}

func execPlugin(t *testing.T, tool *ReadPluginFile, plugin, path string) ToolResult {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"plugin": plugin, "path": path})
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A pinned reference returns its content.
func TestReadPluginFileSuccess(t *testing.T) {
	tool := &ReadPluginFile{
		WorkspaceRoot: "/ws",
		Fetcher:       stubFetcher{data: map[string][]byte{"talon-mobile-ui/references/connect-button.tsx": []byte("export {}")}},
		Pins:          stubPins{pins: map[string]string{"talon-mobile-ui": "1.0.0"}},
	}
	res := execPlugin(t, tool, "talon-mobile-ui", "references/connect-button.tsx")
	if res.Content != "export {}" {
		t.Errorf("content = %q", res.Content)
	}
	if res.IsError {
		t.Error("success must not be an error")
	}
}

// Missing plugin/path, unconfigured fetching, and fetcher errors all fail
// closed with guidance, never empty output.
func TestReadPluginFileGuardrails(t *testing.T) {
	good := &ReadPluginFile{
		WorkspaceRoot: "/ws",
		Fetcher:       stubFetcher{},
		Pins:          stubPins{pins: map[string]string{}},
	}
	for _, tc := range []struct {
		name, plugin, path string
		tool               *ReadPluginFile
	}{
		{"empty plugin", "", "references/a.tsx", good},
		{"empty path", "talon-mobile-ui", "", good},
		{"no fetcher", "talon-mobile-ui", "references/a.tsx", &ReadPluginFile{Pins: stubPins{}}},
	} {
		res := execPlugin(t, tc.tool, tc.plugin, tc.path)
		if !res.IsError {
			t.Errorf("%s: want error result", tc.name)
		}
	}
	res := execPlugin(t, good, "talon-mobile-ui", "references/missing.tsx")
	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Errorf("fetcher error must surface, got %q", res.Content)
	}
}

// Oversized files truncate with a note rather than blowing context.
func TestReadPluginFileTruncates(t *testing.T) {
	big := make([]byte, 30_000)
	for i := range big {
		big[i] = 'x'
	}
	tool := &ReadPluginFile{
		WorkspaceRoot: "/ws",
		Fetcher:       stubFetcher{data: map[string][]byte{"p/references/big.tsx": big}},
		Pins:          stubPins{pins: map[string]string{"p": "1.0.0"}},
	}
	res := execPlugin(t, tool, "p", "references/big.tsx")
	if !strings.Contains(res.Content, "truncated") {
		t.Error("oversized file must note truncation")
	}
	if len(res.Content) > 25_000 {
		t.Errorf("truncated content too long: %d", len(res.Content))
	}
}
