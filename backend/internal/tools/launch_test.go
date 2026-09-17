package tools

import (
	"reflect"
	"testing"
)

// The deep-link shape is the whole fix: a dev build launched without the
// bundle URL idles on the dev launcher and every unattended screenshot is
// black. Both shapes asserted exactly — argument order matters to adb.
func TestLaunchArgs(t *testing.T) {
	got := launchArgs("localhost:15555", "com.talon.app", 8081)
	want := []string{"-s", "localhost:15555", "shell", "am", "start",
		"-a", "android.intent.action.VIEW",
		"-d", "exp://127.0.0.1:8081",
		"com.talon.app/.MainActivity"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deep-link launch:\n got %+v\nwant %+v", got, want)
	}

	got = launchArgs("localhost:15555", "com.talon.app", 0)
	want = []string{"-s", "localhost:15555", "shell", "am", "start",
		"-n", "com.talon.app/.MainActivity"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plain launch (no metro):\n got %+v\nwant %+v", got, want)
	}
}
