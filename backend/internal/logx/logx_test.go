package logx

import (
	"strings"
	"testing"
)

func TestRenderOmitsZeroFields(t *testing.T) {
	line := render("INFO", Fields{}, "hello", "a", 1)
	if strings.Contains(line, "req=") || strings.Contains(line, "user=") {
		t.Errorf("zero fields should be omitted: %q", line)
	}
	if !strings.Contains(line, `msg=hello`) {
		t.Errorf("message missing: %q", line)
	}
}

func TestRenderFullIdentity(t *testing.T) {
	line := render("ERROR", Fields{ReqID: "abc", UserID: 7, ProjectID: 24, SessionID: 3},
		"build failed", "rounds", 39)
	for _, want := range []string{"level=ERROR", "req=abc", "user=7", "project=24", "session=3", "rounds=39"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %q", want, line)
		}
	}
}

func TestRenderQuotesSpaces(t *testing.T) {
	line := render("INFO", Fields{}, "x", "q", "a b")
	if !strings.Contains(line, `q="a b"`) {
		t.Errorf("value with space must be quoted: %q", line)
	}
}

func TestRenderDropsOddTail(t *testing.T) {
	line := render("INFO", Fields{}, "x", "lonely")
	if strings.Contains(line, "lonely") {
		t.Errorf("odd tail should be dropped: %q", line)
	}
}
