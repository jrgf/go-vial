package middleware

import (
	"net/url"
	"testing"
)

func FuzzNormalizeOrigin(fuzz *testing.F) {
	fuzz.Add("https://example.com", false)
	fuzz.Add("http://localhost:8080/path", true)
	fuzz.Add("https://user@example.com", false)
	fuzz.Add("not an origin", false)
	fuzz.Add("http://::", true)

	fuzz.Fuzz(func(t *testing.T, value string, allowPath bool) {
		origin, err := normalizeOrigin(value, allowPath)
		if err != nil {
			return
		}
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			t.Fatalf("invalid normalized origin %q", origin)
		}
		second, err := normalizeOrigin(origin, false)
		if err != nil || second != origin {
			t.Fatalf("origin normalization is not idempotent: %q, %v", second, err)
		}
	})
}

func FuzzCSRFTokenParsing(fuzz *testing.F) {
	policy, err := newCSRFPolicy(CSRFConfig{Key: []byte("0123456789abcdef0123456789abcdef")})
	if err != nil {
		fuzz.Fatal(err)
	}
	fuzz.Add("")
	fuzz.Add("invalid.invalid")
	fuzz.Fuzz(func(t *testing.T, token string) {
		if len(token) > 8<<10 {
			t.Skip()
		}
		_ = policy.validToken(token)
	})
}
