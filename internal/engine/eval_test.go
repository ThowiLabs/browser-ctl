package engine

import (
	"encoding/json"
	"testing"
)

func TestWrapEvalExpr(t *testing.T) {
	cases := []struct{ expr, target, want string }{
		{"() => document.title", "undefined",
			"(function(){var f=(() => document.title); if(typeof f==='function'){return f(undefined);} return f;})()"},
		{"() => 1 + 1", "undefined",
			"(function(){var f=(() => 1 + 1); if(typeof f==='function'){return f(undefined);} return f;})()"},
		{"document.title", "undefined",
			"(function(){var f=(document.title); if(typeof f==='function'){return f(undefined);} return f;})()"},
		{"function(el){ return el.textContent; }", "document.body",
			"(function(){var f=(function(el){ return el.textContent; }); if(typeof f==='function'){return f(document.body);} return f;})()"},
		{"() => 1;", "undefined",
			"(function(){var f=(() => 1); if(typeof f==='function'){return f(undefined);} return f;})()"},
	}
	for _, c := range cases {
		if got := wrapEvalExpr(c.expr, c.target); got != c.want {
			t.Errorf("wrapEvalExpr(%q, %q) = %q, want %q", c.expr, c.target, got, c.want)
		}
	}
}

func TestFormatEvalValue(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`"Example Domain"`, "Example Domain"},
		{`2`, "2"},
		{`true`, "true"},
		{`null`, "null"},
		{`{"a":1}`, `{"a":1}`},
		{``, ""},
	}
	for _, c := range cases {
		if got := formatEvalValue(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("formatEvalValue(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}
