package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	// Detach simulation: while a tab is in detached, its cdp requests
	// fail with the real extension's error until a fresh attach clears
	// the mark — unless the tab is also in cdpStuck, where attach
	// succeeds but cdp keeps failing (a debugger that won't stay on).
	mu          sync.Mutex
	detached    map[int]bool
	cdpStuck    map[int]bool
	attachCount map[int]int
	pages       map[int]*fakePage
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
	f := &fakeExtension{ws: ws, tabs: tabs, detached: map[int]bool{}, cdpStuck: map[int]bool{}, attachCount: map[int]int{}, pages: map[int]*fakePage{}}
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
		if errText := f.requestError(req.Method, req.Params); errText != "" {
			_ = f.ws.WriteJSON(map[string]any{"type": "response", "id": req.ID, "error": errText})
			continue
		}
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
		// Like the real extension, the new tab joins the tab registry
		// and a state event pushes it (CreateTab waits for it).
		f.tabs = append(f.tabs, map[string]any{"tabId": 102, "windowId": 1, "url": p.URL, "title": "", "active": true, "pinned": false})
		_ = f.ws.WriteJSON(map[string]any{"type": "event", "event": map[string]any{"type": "state", "data": map[string]any{"tabs": f.tabs, "paused": false, "pairing": "paired"}}})
		return map[string]any{"tabId": 102}
	case "attach":
		var p struct {
			TabID int `json:"tabId"`
		}
		_ = json.Unmarshal(params, &p)
		f.mu.Lock()
		f.attachCount[p.TabID]++
		if !f.cdpStuck[p.TabID] {
			delete(f.detached, p.TabID)
		}
		f.mu.Unlock()
		return map[string]any{"targetInfo": map[string]any{"targetId": "fake-target", "type": "page", "url": "https://example.com/", "title": "Example"}}
	case "cdp":
		var p struct {
			TabID  int             `json:"tabId"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(params, &p)
		if pg := f.pageFor(p.TabID); pg != nil {
			if res, ok := pg.answer(p.Method, p.Params); ok {
				return res
			}
		}
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

// requestError is the detach simulation: while a tab is marked
// detached, its cdp requests fail with the real extension's exact
// error text. Any other request is answered normally.
func (f *fakeExtension) requestError(method string, params json.RawMessage) string {
	if method != "cdp" {
		return ""
	}
	var p struct {
		TabID int `json:"tabId"`
	}
	_ = json.Unmarshal(params, &p)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.detached[p.TabID] {
		return fmt.Sprintf("The debugger isn't attached to tab %d", p.TabID)
	}
	return ""
}

// dropDebugger simulates Chrome dropping the debugger from a tab the
// daemon still believes it has claimed (daemon/extension restart).
func (f *fakeExtension) dropDebugger(tabID int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detached[tabID] = true
}

// stickDebugger is dropDebugger plus: re-attaching reports success
// but cdp keeps failing — a debugger that won't stay on.
func (f *fakeExtension) stickDebugger(tabID int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detached[tabID] = true
	f.cdpStuck[tabID] = true
}

// attaches counts the attach requests a tab has received.
func (f *fakeExtension) attaches(tabID int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attachCount[tabID]
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

// callBrowser runs one tools/call batch and returns its text + isError.
func callBrowser(t *testing.T, c *mcp.Client, commands string) (string, bool) {
	t.Helper()
	raw, err := c.Call("tools/call", map[string]any{
		"name":      "browser",
		"arguments": map[string]any{"commands": commands},
	})
	if err != nil {
		t.Fatalf("tools/call %q: %v", commands, err)
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
	text := ""
	if len(res.Content) > 0 {
		text = res.Content[0].Text
	}
	return text, res.IsError
}

// startDaemonWithFake boots a daemon plus the fake extension (tab 101)
// and a client, with the backend given a tick to notice the browser.
func startDaemonWithFake(t *testing.T) (*Daemon, *fakeExtension, *mcp.Client) {
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
	fake := newFakeExtension(t, d.Port)
	t.Cleanup(func() { fake.ws.Close() })
	time.Sleep(600 * time.Millisecond) // watchBrowsers tick
	c, err := mcp.Dial(d.Port, d.Token)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return d, fake, c
}

// TestTabSelectClaimsTab: selecting an existing tab attaches its
// debugger right there, instead of leaving the failure for the next
// command.
func TestTabSelectClaimsTab(t *testing.T) {
	_, fake, c := startDaemonWithFake(t)
	if out, isErr := callBrowser(t, c, "tab select 101"); isErr {
		t.Fatalf("tab select: %s", out)
	}
	if got := fake.attaches(101); got != 1 {
		t.Fatalf("attaches(101) = %d, want 1: selecting a tab must claim it", got)
	}
}

// TestDetachedStepReattachesAndRetries: when Chrome drops the debugger
// under the daemon's cached CDP client, the next step re-attaches and
// retries once instead of failing the batch.
func TestDetachedStepReattachesAndRetries(t *testing.T) {
	_, fake, c := startDaemonWithFake(t)
	if out, isErr := callBrowser(t, c, "tab select 101"); isErr {
		t.Fatalf("tab select: %s", out)
	}
	fake.dropDebugger(101)
	out, isErr := callBrowser(t, c, "get title")
	if isErr {
		t.Fatalf("get title after detach should recover, got: %s", out)
	}
	if got := fake.attaches(101); got != 2 {
		t.Fatalf("attaches(101) = %d, want 2 (select + re-attach)", got)
	}
}

// TestDetachedStepStillFailingNamesTabAndRetry: when the retry also
// fails detached, the error that reaches the user names the tab and
// says a re-attach + retry already happened.
func TestDetachedStepStillFailingNamesTabAndRetry(t *testing.T) {
	_, fake, c := startDaemonWithFake(t)
	if out, isErr := callBrowser(t, c, "tab select 101"); isErr {
		t.Fatalf("tab select: %s", out)
	}
	fake.stickDebugger(101)
	out, isErr := callBrowser(t, c, "get title")
	if !isErr {
		t.Fatalf("get title should fail, got %q", out)
	}
	if !contains(out, "tab 101") || !contains(out, "re-attached and retried") {
		t.Fatalf("error should name the tab and the retry: %q", out)
	}
}

// TestProbePorts: a live browsermcp status on a well-known port is
// found even with no daemon.json anywhere.
func TestProbePorts(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:9010")
	if err != nil {
		t.Skipf("port 9010 unavailable: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"product": "browsermcp", "version": "2.0.0", "protocol": 1, "minProtocol": 1, "pid": 4242,
		})
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	port, st := ProbePorts(0)
	if st == nil || port != 9010 || st.Pid != 4242 {
		t.Fatalf("ProbePorts(0) = %d, %+v; want 9010 pid 4242", port, st)
	}
	if _, st := ProbePorts(9010); st == nil {
		t.Fatal("ProbePorts(9010) should find the same server")
	}
}

// fakePage scripts one tab's CDP answers for the issue tests: an AX
// tree per frame, an optional frame tree, and a programmable
// Runtime.evaluate. It records how the engine resolved nodes, so a
// test can assert WHICH resolution path a command took (precise
// backend-node resolution vs the stored querySelector expression).
type fakePage struct {
	axByFrame map[string][]any // frameId -> AX nodes; "" = main frame (no frameId param)
	frameTree map[string]any   // Page.getFrameTree result; nil = single-frame default
	evalFn    func(expr string) (map[string]any, bool)

	mu        sync.Mutex
	resolved  []int    // backendNodeIds passed to DOM.resolveNode
	funcCalls []string // functionDeclarations passed to Runtime.callFunctionOn
	evaluated []string // Runtime.evaluate expressions, in order
}

func (f *fakeExtension) setPage(tabID int, p *fakePage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages[tabID] = p
}

func (f *fakeExtension) pageFor(tabID int) *fakePage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pages[tabID]
}

// answer serves the scripted CDP methods; ok=false means "fall
// through to the fake's generic answers".
func (pg *fakePage) answer(method string, params json.RawMessage) (any, bool) {
	switch method {
	case "Page.getFrameTree":
		if pg.frameTree != nil {
			return pg.frameTree, true
		}
	case "Accessibility.getFullAXTree":
		var p struct {
			FrameID string `json:"frameId"`
		}
		_ = json.Unmarshal(params, &p)
		if nodes, ok := pg.axByFrame[p.FrameID]; ok {
			return map[string]any{"nodes": nodes}, true
		}
	case "Runtime.evaluate":
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		pg.mu.Lock()
		pg.evaluated = append(pg.evaluated, p.Expression)
		pg.mu.Unlock()
		if pg.evalFn != nil {
			return pg.evalFn(p.Expression)
		}
	case "DOM.resolveNode":
		var p struct {
			BackendNodeID int `json:"backendNodeId"`
		}
		_ = json.Unmarshal(params, &p)
		pg.mu.Lock()
		pg.resolved = append(pg.resolved, p.BackendNodeID)
		pg.mu.Unlock()
		return map[string]any{"object": map[string]any{"objectId": fmt.Sprintf("obj-%d", p.BackendNodeID)}}, true
	case "Runtime.callFunctionOn":
		var p struct {
			FunctionDeclaration string `json:"functionDeclaration"`
		}
		_ = json.Unmarshal(params, &p)
		pg.mu.Lock()
		pg.funcCalls = append(pg.funcCalls, p.FunctionDeclaration)
		pg.mu.Unlock()
		if strings.Contains(p.FunctionDeclaration, "elementFromPoint") {
			return map[string]any{"result": map[string]any{"type": "string", "value": "ok"}}, true
		}
		return map[string]any{"result": map[string]any{"type": "boolean", "value": true}}, true
	case "DOM.getBoxModel":
		return map[string]any{"model": map[string]any{"content": []float64{0, 0, 10, 0, 10, 10, 0, 10}}}, true
	}
	return nil, false
}

func (pg *fakePage) resolvedNodes() []int {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return append([]int(nil), pg.resolved...)
}

func (pg *fakePage) calls() []string {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return append([]string(nil), pg.funcCalls...)
}

func (pg *fakePage) evals() []string {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return append([]string(nil), pg.evaluated...)
}

// axNode builds one Accessibility.getFullAXTree node for a fixture.
func axNode(id, role, name string, backend int, children ...string) map[string]any {
	n := map[string]any{
		"nodeId":   id,
		"role":     map[string]any{"value": role},
		"name":     map[string]any{"value": name},
		"childIds": children,
	}
	if backend != 0 {
		n["backendDOMNodeId"] = backend
	}
	return n
}
