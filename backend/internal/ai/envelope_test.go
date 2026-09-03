package ai

import (
	"testing"
)

func TestEnvelopeErrorExtraction(t *testing.T) {
	cases := []struct {
		body    string
		msg     string
		code    int
		present bool
	}{
		{`{"error":{"message":"Provider returned error","code":529}}`, "Provider returned error", 529, true},
		{`{"error":{"message":"Rate limited","code":"429"}}`, "Rate limited", 429, true},
		{`{"error":{"message":"bad key"}}`, "bad key", 0, true},
		{`{"choices":[{"message":{"content":"hi"}}]}`, "", 0, false},
		{`not json`, "", 0, false},
	}
	for _, c := range cases {
		msg, code, ok := envelopeError([]byte(c.body))
		if ok != c.present || msg != c.msg || code != c.code {
			t.Errorf("envelopeError(%q) = (%q,%d,%v), want (%q,%d,%v)",
				c.body, msg, code, ok, c.msg, c.code, c.present)
		}
	}
}

func TestTransientEnvelopeMatching(t *testing.T) {
	transient := []string{
		"Provider returned error",
		"Upstream provider overloaded, try again",
		"Request timed out",
		"No endpoints found for model",
		"rate limit exceeded",
	}
	for _, m := range transient {
		if !transientEnvelopeRe.MatchString(m) {
			t.Errorf("expected transient match for %q", m)
		}
	}
	permanent := []string{
		"Invalid API key",
		"Model not found",
		"Unexpected token in JSON",
	}
	for _, m := range permanent {
		if transientEnvelopeRe.MatchString(m) {
			t.Errorf("expected no transient match for %q", m)
		}
	}
}
