package cmd

import "testing"

func TestNormalizeURLForCompare(t *testing.T) {
	cases := []struct{ a, b string }{
		{"https://example.com", "https://example.com/"},
		{"https://example.com/", "https://example.com"},
		{"https://EXAMPLE.com", "https://example.com/"},
		{"https://example.com:443", "https://example.com/"},
		{"https://example.com/page", "https://example.com/page"},
		{"https://example.com/page#frag", "https://example.com/page"},
	}
	for _, c := range cases {
		if got, want := normalizeURLForCompare(c.a), normalizeURLForCompare(c.b); got != want {
			t.Errorf("normalizeURLForCompare(%q) = %q, want %q", c.a, got, want)
		}
	}
	if normalizeURLForCompare("https://example.com/a") == normalizeURLForCompare("https://example.com/b") {
		t.Errorf("different paths must not match")
	}
	if normalizeURLForCompare("https://example.com/?x=1") == normalizeURLForCompare("https://example.com/") {
		t.Errorf("different queries must not match")
	}
}
