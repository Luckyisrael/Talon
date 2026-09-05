package runtime

import (
	"strings"
	"testing"
	"time"
)

const fixture = `09-05 14:22:30.100   500   500 I boot_progress: boot complete
09-05 14:22:31.123  1234  1234 I com.test.app: Application onCreate
09-05 14:22:31.400  1234  1234 D ReactNativeJS: Running "SeekerApp"
09-05 14:22:33.000  1234  1234 E AndroidRuntime: FATAL EXCEPTION: main
09-05 14:22:33.001  1234  1234 E AndroidRuntime: java.lang.NullPointerException
not a timestamped line
09-05 14:22:34.000   600   600 I other.tag: unrelated`

func TestParseLine(t *testing.T) {
	l, ok := ParseLine("09-05 14:22:31.123  1234  1234 I com.test.app: Application onCreate")
	if !ok {
		t.Fatal("should parse")
	}
	if l.Tag != "com.test.app" || l.Level != "I" || l.Message != "Application onCreate" {
		t.Errorf("bad parse: %+v", l)
	}
	if _, ok := ParseLine("not a timestamped line"); ok {
		t.Error("brief-format line should not parse")
	}
}

func TestHeadTailAfterContains(t *testing.T) {
	if got := Head(fixture, 2); strings.Count(got, "\n") != 1 {
		t.Errorf("head(2) should be 2 lines: %q", got)
	}
	if got := Tail(fixture, 1); !strings.Contains(got, "unrelated") {
		t.Errorf("tail(1) should be the last line: %q", got)
	}
	after := After(fixture, "onCreate")
	if strings.Contains(after, "onCreate") || !strings.Contains(after, "ReactNativeJS") {
		t.Errorf("after() should exclude the marker line: %q", after)
	}
	if After(fixture, "never-appears") != "" {
		t.Error("after() with missing marker should be empty")
	}
	got := Contains(fixture, "androidruntime")
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, "FATAL") {
		t.Errorf("contains() should find both fatal lines: %q", got)
	}
}

func TestBootGap(t *testing.T) {
	launch := time.Date(2026, 9, 5, 14, 22, 30, 0, time.Local)
	gap, first, ok := BootGap(fixture, "com.test.app", launch)
	if !ok {
		t.Fatal("app logged after launch — should find first line")
	}
	if first.Tag != "com.test.app" {
		t.Errorf("first app line: %+v", first)
	}
	if gap < time.Second || gap > 2*time.Second {
		t.Errorf("gap should be ~1.1s, got %v", gap)
	}
	if _, _, ok := BootGap(fixture, "com.ghost.app", launch); ok {
		t.Error("silent app should report ok=false")
	}
}

func TestCrashExcerpt(t *testing.T) {
	ex := CrashExcerpt(fixture, 2)
	if !strings.Contains(ex, "FATAL EXCEPTION") || !strings.Contains(ex, "--- fatal ---") {
		t.Errorf("excerpt should carry the fatal block: %q", ex)
	}
}
