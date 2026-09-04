package ws

import "testing"

func TestDecideRotation(t *testing.T) {
	cases := []struct {
		out  string
		want string
	}{
		{"Physical size: 1080x2400\n", "2400x1080"},
		{"Physical size: 1080x2400\nOverride size: 2400x1080\n", "1080x2400"},
		{"Physical size: 1080x2400\nOverride size: 1080x2400\n", "2400x1080"},
		{"garbage", "1080x2400"},
		{"", "1080x2400"},
	}
	for _, c := range cases {
		if got := decideRotation(c.out); got != c.want {
			t.Errorf("decideRotation(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}
