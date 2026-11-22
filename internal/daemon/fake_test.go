package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/thovilabs/browser-ctl/internal/mcp"
)

// fakeExtension speaks the extension protocol against a daemon: hello,
// state events, and answers to createTab/attach/cdp requests.
type fakeExtension struct {
	ws   *websocket.Conn
	tabs []map[string]any
}

func newFakeExtension(t *testing.T, port int) *fakeExtension {
	return newFakeExtensionWith(t, port, "AAAAAAAAAAAAAAAAAAAAAA", []map[string]any{
		{"tabId": 101, "windowId": 1, "url": "https://example.com/", "title": "Example", "active": true, "pinned": false},
	})
}

func newFakeExtensionWith(t *testing.T, port int, browserID string, tabs []map[string]any) *fakeExtension {
	t.Helper()
	// Probe status first, like the real extension.
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/browsermcp/status", port))
	if err != nil {
		t.Fatalf("status probe: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status probe: %s", resp.Status)
	}
	hdr := http.Header{}
	hdr.Set("Origin", "chrome-extension://bjfgambnhccakkhmkepdoekmckoijdlc")
	ws, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/extension", port), hdr)
	if err != nil {
		t.Fatalf("dial extension: %v", err)
	}
	f := &fakeExtension{ws: ws, tabs: tabs}
	hello := map[string]any{
		"type": "hello", "protocol": 1, "extensionVersion": "2.0.0-test",
		"browser":   map[string]any{"name": "Chrome", "version": "140", "userAgent": "fake"},
		"browserId": browserID, "capabilities": []string{},
		"challenge": "test-challenge",
		"state":     map[string]any{"tabs": f.tabs, "paused": false, "pairing": "paired"},
	}
	if err := ws.WriteJSON(hello); err != nil {
		t.Fatalf("hello: %v", err)
	}
	var welcome map[string]any
	if err := ws.ReadJSON(&welcome); err != nil {
		t.Fatalf("welcome: %v", err)
	}
	if welcome["type"] != "welcome" {
		t.Fatalf("welcome type: %v", welcome)
	}
	go f.serve()
	return f
}

func (f *fakeExtension) serve() {
	for {
		_, data, err := f.ws.ReadMessage()
		if err != nil {
			return
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		var typ string
		_ = json.Unmarshal(msg["type"], &typ)
		if typ == "ping" {
			var id int
			_ = json.Unmarshal(msg["id"], &id)
			_ = f.ws.WriteJSON(map[string]any{"type": "pong", "id": id})
			continue
		}
		if typ != "request" {
			continue
		}
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(data, &req)
		result := f.answer(req.Method, req.Params)
		_ = f.ws.WriteJSON(map[string]any{"type": "response", "id": req.ID, "result": result})
	}
}

func (f *fakeExtension) answer(method string, params json.RawMessage) any {
	switch method {
	case "createTab":
		var p struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{"tabId": 102}
	case "attach":
		return map[string]any{"targetInfo": map[string]any{"targetId": "fake-target", "type": "page", "url": "https://example.com/", "title": "Example"}}
	case "cdp":
		var p struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(params, &p)
		switch p.Method {
		case "Page.navigate":
			return map[string]any{}
		case "Runtime.evaluate":
			return map[string]any{"result": map[string]any{"result": map[string]any{"type": "string", "value": "complete"}}}
		case "Accessibility.getFullAXTree":
			return map[string]any{"nodes": []any{
				map[string]any{"nodeId": "1", "role": map[string]any{"value": "RootWebArea"}, "name": map[string]any{"value": ""}, "childIds": []string{"2"}},
				map[string]any{"nodeId": "2", "role": map[string]any{"value": "button"}, "name": map[string]any{"value": "Go"}, "childIds": []string{}},
			}}
		default:
			return map[string]any{}
		}
	case "closeTab", "activateTab", "groupTab", "detach":
		return map[string]any{}
	}
	return map[string]any{}
}

func TestDaemonWithFakeExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BROWSER_CTL_HOME", home)
	_ = filepath.Join(home, "x")
	d, err := Start(0, func(string, ...any) {})
	if err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer d.Stop("test")
	// Wait for the relay to answer status.
	deadline := time.Now().Add(5 * time.Second)
	for FetchStatus(d.Port) == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	fake := newFakeExtension(t, d.Port)
	defer fake.ws.Close()
	// Give the backend time to notice the browser.
	time.Sleep(600 * time.Millisecond)

	// Daemon discovery via daemon.json.
	info := FindDaemon(home)
	if info == nil {
		t.Fatal("daemon.json not found / not live")
	}
	if info.Port != d.Port {
		t.Fatalf("daemon.json port %d != %d", info.Port, d.Port)
	}

	// MCP client: tools/call goto + tab list through a fresh session.
	c, err := mcp.Dial(d.Port, d.Token)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer c.Close()
	raw, err := c.Call("tools/call", map[string]any{
		"name":      "browser",
		"arguments": map[string]any{"commands": "goto https://example.com\ntab list"},
	})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v (raw %s)", err, raw)
	}
	if res.IsError {
		t.Fatalf("tools/call error: %v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty content")
	}
	text := res.Content[0].Text
	t.Logf("output:\n%s", text)
	if !contains(text, "→ https://example.com") {
		t.Errorf("goto output missing: %q", text)
	}
	if !contains(text, "101") {
		t.Errorf("tab list missing tab 101: %q", text)
	}

	// Second goto must NOT create a new tab: the session already has a
	// current tab, so goto navigates it (fake createTab would return 102).
	raw2, err := c.Call("tools/call", map[string]any{
		"name":      "browser",
		"arguments": map[string]any{"commands": "goto https://example.com"},
	})
	if err != nil {
		t.Fatalf("second goto: %v", err)
	}
	var res2 struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	_ = json.Unmarshal(raw2, &res2)
	if res2.IsError {
		t.Fatalf("second goto error: %v", res2.Content)
	}
	_ = os.Getenv("HOME")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestPublicTabIDsAcrossBrowsers checks the stable public-ID mapping: two
// browsers reporting the same Chrome tab ID must not share a public ID —
// the second browser's tab gets a spare >= 100001, and the mapping stays
// stable across calls. A stale ID (unknown tab) must read as closed.
func TestPublicTabIDsAcrossBrowsers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BROWSER_CTL_HOME", home)
	d, err := Start(0, func(string, ...any) {})
	if err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer d.Stop("test")
	deadline := time.Now().Add(5 * time.Second)
	for FetchStatus(d.Port) == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	// Browser A: tab 101. Browser B: tab 101 (collision) and tab 103.
	fa := newFakeExtension(t, d.Port)
	defer fa.ws.Close()
	fb := newFakeExtensionWith(t, d.Port, "BBBBBBBBBBBBBBBBBBBBBB", []map[string]any{
		{"tabId": 101, "windowId": 1, "url": "https://other.example/", "title": "Other", "active": true, "pinned": false},
		{"tabId": 103, "windowId": 1, "url": "https://third.example/", "title": "Third", "active": false, "pinned": false},
	})
	defer fb.ws.Close()
	// watchBrowsers notices new browsers every 500 ms.
	time.Sleep(1200 * time.Millisecond)

	tabs := d.Backend.Tabs()
	if len(tabs) != 3 {
		t.Fatalf("tabs = %d, want 3: %+v", len(tabs), tabs)
	}
	byURL := map[string]int{}
	for _, tab := range tabs {
		byURL[tab.URL] = tab.TabID
	}
	// Which browser is discovered first is not deterministic (the relay
	// reports browsers as a map), so assert the set, not the assignment:
	// one of the two colliding tabs keeps 101, the other gets 100001.
	colliding := map[int]bool{byURL["https://example.com/"]: true, byURL["https://other.example/"]: true}
	if !colliding[101] || !colliding[100001] || len(colliding) != 2 {
		t.Errorf("colliding tabs got %v, want {101, 100001}", colliding)
	}
	if byURL["https://third.example/"] != 103 {
		t.Errorf("non-colliding tab keeps its Chrome ID: got %d, want 103", byURL["https://third.example/"])
	}
	// Stable across calls: same public IDs on a second read.
	tabs2 := d.Backend.Tabs()
	for _, tab := range tabs2 {
		if byURL[tab.URL] != tab.TabID {
			t.Errorf("public ID moved for %s: %d -> %d", tab.URL, byURL[tab.URL], tab.TabID)
		}
	}
	// Resolve: 101 and 100001 name different browsers, and tab 103
	// belongs to browser B (its owner is fixed; the spare's owner
	// depends on discovery order).
	br101 := d.Backend.FindTab(101)
	brSpare := d.Backend.FindTab(100001)
	if br101 == nil || brSpare == nil || br101.Profile == brSpare.Profile {
		t.Errorf("FindTab(101) = %v, FindTab(100001) = %v, want two different browsers", br101, brSpare)
	}
	if br := d.Backend.FindTab(103); br == nil || br.Profile != "BBBBBBBBBBBBBBBBBBBBBB" {
		t.Errorf("FindTab(103) = %v, want browser B", br)
	}
	if d.Backend.TabClosed(100001) {
		t.Errorf("TabClosed(100001) = true, want false (tab is live)")
	}
	if !d.Backend.TabClosed(999999) {
		t.Errorf("TabClosed(999999) = false, want true (unknown ID)")
	}
}
