package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/thovilabs/browser-ctl/internal/mcp"
)

// Issue-round-2 tests (report points 8, 9, 10, 12), driven through a
// real daemon + MCP session against the scripted fake extension.

func startIssueDaemon(t *testing.T, tabs []map[string]any) (*mcp.Client, *fakeExtension) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BROWSER_CTL_HOME", home)
	d, err := Start(0, func(string, ...any) {})
	if err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() { d.Stop("test") })
	deadline := time.Now().Add(5 * time.Second)
	for FetchStatus(d.Port) == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	fake := newFakeExtensionWith(t, d.Port, "AAAAAAAAAAAAAAAAAAAAAA", tabs)
	t.Cleanup(func() { fake.ws.Close() })
	// Give the backend time to notice the browser.
	time.Sleep(600 * time.Millisecond)
	c, err := mcp.Dial(d.Port, d.Token)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, fake
}

func oneTab() []map[string]any {
	return []map[string]any{
		{"tabId": 101, "windowId": 1, "url": "https://example.com/", "title": "Example", "active": true, "pinned": false},
	}
}

func syntaxErrorPayload() map[string]any {
	return map[string]any{
		"result": map[string]any{},
		"exceptionDetails": map[string]any{
			"text":      "Uncaught",
			"exception": map[string]any{"description": "SyntaxError: Unexpected token ';'"},
		},
	}
}

// Point 8: fill on a textbox whose accessible name comes from a
// <label> (a 2FA verification-code input). The snapshot ref resolves
// by backend node id; the stored role+name re-query cannot find the
// element (no innerText, no aria-label) and used to throw
// "no element", while click on the same ref worked.
func TestFillLabelWrappedInput(t *testing.T) {
	c, fake := startIssueDaemon(t, oneTab())
	pg := &fakePage{
		axByFrame: map[string][]any{"": {
			axNode("1", "RootWebArea", "", 0, "2"),
			axNode("2", "textbox", "Verification code", 55),
		}},
		evalFn: func(expr string) (map[string]any, bool) {
			if strings.Contains(expr, "querySelectorAll") {
				// The re-query path cannot see this input.
				return map[string]any{
					"result": map[string]any{},
					"exceptionDetails": map[string]any{
						"text":      "Uncaught",
						"exception": map[string]any{"description": "Error: no element"},
					},
				}, true
			}
			return nil, false
		},
	}
	fake.setPage(101, pg)

	out, isErr := callBrowser(t, c, "tab select 101\nsnapshot\nfill e2 \"7225714\"")
	if isErr {
		t.Fatalf("fill failed: %s", out)
	}
	if strings.Contains(out, "no element") {
		t.Fatalf("fill hit the re-query path: %s", out)
	}
	if got := pg.resolvedNodes(); len(got) != 1 || got[0] != 55 {
		t.Fatalf("resolved nodes = %v, want [55] (precise backend-node path)", got)
	}
	calls := pg.calls()
	if len(calls) != 1 || !strings.Contains(calls[0], "7225714") {
		t.Fatalf("callFunctionOn calls = %v, want one setting 7225714", calls)
	}
}

// Point 9: eval must accept multi-statement programs (a; b) and
// return the completion value, without breaking expressions whose
// string literals contain ';'.
func TestEvalMultiStatementProgram(t *testing.T) {
	c, fake := startIssueDaemon(t, oneTab())
	pg := &fakePage{
		evalFn: func(expr string) (map[string]any, bool) {
			switch {
			case strings.Contains(expr, "value='x';"):
				if strings.HasPrefix(expr, "(function(){var f=(") {
					// Wrapped as an expression: a real JS engine
					// rejects the top-level ';' here.
					return syntaxErrorPayload(), true
				}
				// The raw program: completion value is 'done'.
				return map[string]any{"result": map[string]any{"type": "string", "value": "done"}}, true
			case strings.Contains(expr, "'a;b'"):
				return map[string]any{"result": map[string]any{"type": "string", "value": "a;b"}}, true
			}
			return nil, false
		},
	}
	fake.setPage(101, pg)

	out, isErr := callBrowser(t, c, "tab select 101\neval \"document.querySelector('input').value='x'; 'done'\"\neval \"'a;b'\"")
	if isErr {
		t.Fatalf("eval failed: %s", out)
	}
	// Exact result lines (the echoed command lines contain the
	// quoted code, so a bare "done"/"a;b" line is the eval result).
	lines := map[string]bool{}
	for _, ln := range strings.Split(out, "\n") {
		lines[strings.TrimSpace(ln)] = true
	}
	if !lines["done"] {
		t.Fatalf("multi-statement eval did not return its completion value: %s", out)
	}
	if !lines["a;b"] {
		t.Fatalf("quoted-semicolon eval broke: %s", out)
	}
	n := 0
	for _, e := range pg.evals() {
		if strings.Contains(e, "value='x'") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("program evaluated %d times, want 2 (wrapped attempt + raw retry): %v", n, pg.evals())
	}
}

// Point 10: find must reach text that only exists whole in a child
// frame's DOM (an email body iframe), and say which frame matched —
// via the AX tree (tagged with the frame index) and via the deep
// text search over frames/shadow roots.
func TestFindAcrossFrames(t *testing.T) {
	c, fake := startIssueDaemon(t, oneTab())
	pg := &fakePage{
		frameTree: map[string]any{"frameTree": map[string]any{
			"frame":       map[string]any{"id": "F1"},
			"childFrames": []any{map[string]any{"frame": map[string]any{"id": "F2"}}},
		}},
		axByFrame: map[string][]any{
			"": {
				axNode("1", "RootWebArea", "", 0, "2", "3"),
				axNode("2", "StaticText", "Welcome home", 0),
				axNode("3", "Iframe", "mail body", 0),
			},
			"F2": {
				axNode("1", "RootWebArea", "", 0, "2"),
				axNode("2", "StaticText", "Your code is 7225714 today", 0),
			},
		},
		evalFn: func(expr string) (map[string]any, bool) {
			if strings.Contains(expr, "var needle=") {
				return map[string]any{"result": map[string]any{"type": "object", "value": []any{
					map[string]any{"where": "frame 1: https://mail.google.com/body", "snippet": "...the code is 7225714 and it expires soon..."},
				}}}, true
			}
			return nil, false
		},
	}
	fake.setPage(101, pg)

	out, isErr := callBrowser(t, c, "tab select 101\nfind \"7225714\"")
	if isErr {
		t.Fatalf("find failed: %s", out)
	}
	if strings.Contains(out, "no matches") {
		t.Fatalf("find missed the code: %s", out)
	}
	if !strings.Contains(out, "[frame 1]") {
		t.Fatalf("AX match not tagged with its frame: %s", out)
	}
	if !strings.Contains(out, "frame 1: https://mail.google.com/body") {
		t.Fatalf("deep text match missing or untagged: %s", out)
	}
}

// Point 12: snapshot refs belong to the tab that produced them. A
// ref captured on Gmail u/0 must not resolve on u/1 (same origin,
// same roles/names), each tab keeps its own ref space, and switching
// back restores the first tab's refs.
func TestRefsArePerTab(t *testing.T) {
	tabs := []map[string]any{
		{"tabId": 101, "windowId": 1, "url": "https://mail.google.com/mail/u/0", "title": "Inbox Zero", "active": true, "pinned": false},
		{"tabId": 103, "windowId": 1, "url": "https://mail.google.com/mail/u/1", "title": "Inbox One", "active": false, "pinned": false},
	}
	c, fake := startIssueDaemon(t, tabs)
	pg101 := &fakePage{axByFrame: map[string][]any{"": {
		axNode("1", "RootWebArea", "", 0, "2"),
		axNode("2", "button", "Zero Inbox", 11),
	}}}
	pg103 := &fakePage{axByFrame: map[string][]any{"": {
		axNode("1", "RootWebArea", "", 0, "2"),
		axNode("2", "button", "One Inbox", 22),
	}}}
	fake.setPage(101, pg101)
	fake.setPage(103, pg103)

	// Snapshot on u/0, then try u/0's button ref on u/1: it must
	// fail as an unknown ref, and nothing may be resolved on u/1.
	out, isErr := callBrowser(t, c, "tab select 101\nsnapshot\ntab select 103\nclick e2")
	if !isErr {
		t.Fatalf("click with another tab's ref succeeded: %s", out)
	}
	if !strings.Contains(out, "isn't on the page anymore") {
		t.Fatalf("unexpected error for foreign ref: %s", out)
	}
	if got := pg103.resolvedNodes(); len(got) != 0 {
		t.Fatalf("nodes resolved on the wrong tab: %v", got)
	}
	if got := pg101.resolvedNodes(); len(got) != 0 {
		t.Fatalf("nodes resolved during foreign-ref click: %v", got)
	}

	// u/1's own snapshot gives it its own working refs.
	out, isErr = callBrowser(t, c, "snapshot\nclick e2")
	if isErr {
		t.Fatalf("click with u/1's own ref failed: %s", out)
	}
	found := false
	for _, id := range pg103.resolvedNodes() {
		if id == 22 {
			found = true
		}
	}
	if !found {
		t.Fatalf("u/1 click did not resolve its backend node 22: %v", pg103.resolvedNodes())
	}

	// Switching back to u/0: its refs still work there.
	out, isErr = callBrowser(t, c, "tab select 101\nclick e2")
	if isErr {
		t.Fatalf("click back on u/0 failed: %s", out)
	}
	found = false
	for _, id := range pg101.resolvedNodes() {
		if id == 11 {
			found = true
		}
	}
	if !found {
		t.Fatalf("u/0 click did not resolve its backend node 11: %v", pg101.resolvedNodes())
	}
}
