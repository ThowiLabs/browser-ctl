// Package engine: sessions, tab state, and the command engine. A session
// is long-lived in the daemon; the engine navigates the session's current
// tab and only creates one when the session has none — it never searches
// by URL (that was the v1 duplicate-tab bug).
package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/thovilabs/browser-ctl/internal/backend"
	"github.com/thovilabs/browser-ctl/internal/cdp"
	"github.com/thovilabs/browser-ctl/internal/protocol"
)

// Timeouts, matching the JS engine.
const (
	DefaultStepTimeout = 30 * time.Second
	StepHeadroom       = 5 * time.Second
	GotoStepTimeout    = 45 * time.Second
	PDFDownloadTimeout = 60 * time.Second
	TimeoutMax         = 60 * time.Second
	IdleRelease        = 10 * time.Minute
)

// Output caps, matching the JS engine.
const (
	ReadCap     = 10_000
	ReadCapMax  = 75_000
	ResponseCap = 80_000
	MaxImages   = 3
)

// AppInfo identifies the client app for tab grouping.
type AppInfo struct {
	ID   string
	Name string
}

// Session is one client's long-lived state in the daemon.
type Session struct {
	ID           string
	App          AppInfo
	Capabilities map[string]bool

	mu       sync.Mutex
	runMu    sync.Mutex   // serializes Run calls on this session
	Current  int          // current tab (public ID), 0 = none
	Tabs     map[int]bool // tabs this session uses
	Opened   map[int]bool // tabs this session opened (only these may close)
	Pending  int          // tab dragged into the app's group, taken next call
	Browser  string       // chosen browser profile, "" = default
	LastCall time.Time
	// LastRefs maps snapshot refs (e12) to a JS expression yielding the
	// element, from the session's last snapshot.
	LastRefs map[string]string
	// LastRefNodes maps snapshot refs to backend DOM node ids (from the
	// accessibility tree), for precise CDP resolution.
	LastRefNodes map[string]int
}

// AddCapabilities enables capabilities (e.g. "cdp") on the session.
func (s *Session) AddCapabilities(caps ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range caps {
		s.Capabilities[c] = true
	}
}

// TabState is per-tab capture state (console/requests/dialog buffers).
type TabState struct {
	TabID         int
	Console       []ConsoleEntry
	Uncaught      []ConsoleEntry
	Requests      []RequestEntry
	Dialog        *Dialog
	Abandoned     chan struct{} // closed when an interrupted step finishes
	ErrorCount    int
	LastError     string
	LastUsed      time.Time
	InternalCDP   *cdp.Client // engine's own CDP session
	AgentCDP      *cdp.Client // agent's --caps cdp session
	FocusEmulated bool
	CDPEvents     []string // event names the agent subscribed to (cdp on)
	CDPEventLog   []string // buffered CDP events matching subscriptions
	Seq           int      // shared sequence for console/request ordering
}

// ConsoleEntry is one console/pageerror line.
type ConsoleEntry struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Location string `json:"location,omitempty"`
	Seq      int    `json:"seq"`
}

// RequestEntry is one network request.
type RequestEntry struct {
	ID      int    `json:"id"`
	Method  string `json:"method"`
	URL     string `json:"url"`
	Status  int    `json:"status,omitempty"`
	Failure string `json:"failure,omitempty"`
}

// Dialog is an open JS dialog.
type Dialog struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Engine runs commands for sessions against a backend.
type Engine struct {
	backend *backend.Backend
	app     protocol.AppInfo

	mu       sync.Mutex
	sessions map[string]*Session
	tabState map[int]*TabState
	seq      int
	busy     int
}

// New builds an engine over a backend.
func New(b *backend.Backend, app protocol.AppInfo) *Engine {
	return &Engine{
		backend:  b,
		app:      app,
		sessions: map[string]*Session{},
		tabState: map[int]*TabState{},
	}
}

// OpenSession creates (or returns) a session for a client.
func (e *Engine) OpenSession(id string, app AppInfo, caps map[string]bool) *Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.sessions[id]; ok {
		return s
	}
	s := &Session{
		ID:           id,
		App:          app,
		Capabilities: caps,
		Tabs:         map[int]bool{},
		Opened:       map[int]bool{},
		LastCall:     time.Now(),
	}
	e.sessions[id] = s
	return s
}

// CloseSession releases a session's tabs (they stay open in Chrome).
func (e *Engine) CloseSession(id string) {
	e.mu.Lock()
	s, ok := e.sessions[id]
	delete(e.sessions, id)
	e.mu.Unlock()
	if !ok {
		return
	}
	s.mu.Lock()
	tabs := make([]int, 0, len(s.Tabs))
	for t := range s.Tabs {
		tabs = append(tabs, t)
	}
	s.mu.Unlock()
	for _, t := range tabs {
		e.releaseTab(t)
	}
}

// Busy reports whether a call is in flight.
func (e *Engine) Busy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.busy > 0
}

// ReleaseIdle releases tabs idle longer than IdleRelease.
func (e *Engine) ReleaseIdle() {
	e.mu.Lock()
	var stale []int
	now := time.Now()
	for id, ts := range e.tabState {
		if now.Sub(ts.LastUsed) > IdleRelease {
			stale = append(stale, id)
		}
	}
	e.mu.Unlock()
	for _, id := range stale {
		e.releaseTab(id)
	}
}

func (e *Engine) releaseTab(tabID int) {
	e.mu.Lock()
	ts, ok := e.tabState[tabID]
	delete(e.tabState, tabID)
	e.mu.Unlock()
	if !ok {
		return
	}
	// Turn off focus emulation and detach the engine's CDP session.
	if ts.InternalCDP != nil {
		_, _ = ts.InternalCDP.Call("Emulation.setFocusEmulationEnabled", map[string]any{"enabled": false})
		_ = ts.InternalCDP.Close()
	}
	if ts.AgentCDP != nil {
		_ = ts.AgentCDP.Close()
	}
}

// tabStateFor returns (creating) the TabState for a tab, claiming it via
// the backend and enabling focus emulation so Chrome renders it.
func (e *Engine) tabStateFor(tabID int) (*TabState, error) {
	e.mu.Lock()
	if ts, ok := e.tabState[tabID]; ok {
		ts.LastUsed = time.Now()
		e.mu.Unlock()
		return ts, nil
	}
	e.mu.Unlock()
	client, err := e.backend.Claim(tabID)
	if err != nil {
		return nil, err
	}
	ts := &TabState{TabID: tabID, LastUsed: time.Now(), InternalCDP: client}
	// Hidden-tab fix: background tabs don't render without focus emulation.
	if _, err := client.Call("Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}); err == nil {
		ts.FocusEmulated = true
	}
	// Feed console/request buffers from CDP events (cap 500 each, like JS).
	client.OnEvent(func(method string, params json.RawMessage) {
		e.captureEvent(ts, method, params)
	})
	e.mu.Lock()
	e.tabState[tabID] = ts
	e.mu.Unlock()
	return ts, nil
}

// Run executes a batch of commands for a session. Every line is
// pre-validated before anything runs; execution stops at the first error.
func (e *Engine) Run(sess *Session, input string) (string, error) {
	// Two CLI invocations can share one session ("cli"); run them in
	// order so their commands never interleave on the same tab.
	sess.runMu.Lock()
	defer sess.runMu.Unlock()
	lines, err := ParseCommands(input)
	if err != nil {
		return "", err
	}
	// Pre-check every line: unknown command, arg count, flag errors.
	helpOnly := len(lines) > 0
	for _, ln := range lines {
		if err := CheckLine(ln); err != nil {
			return "", fmt.Errorf("nothing ran: step %d: %w", ln.Step, err)
		}
		if ln.Name != "help" {
			helpOnly = false
		}
	}
	e.mu.Lock()
	e.busy++
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.busy--
		e.mu.Unlock()
	}()

	sess.mu.Lock()
	sess.LastCall = time.Now()
	// Take a dragged-in tab at the start of the call.
	if sess.Pending != 0 {
		sess.Current = sess.Pending
		sess.Tabs[sess.Pending] = true
		sess.Pending = 0
	}
	sess.mu.Unlock()

	// Help-only batches don't need a browser (JS: whenReady is skipped).
	if !helpOnly {
		if err := e.backend.WhenReady(); err != nil {
			return "", err
		}
	}

	var out []string
	var pageNote string
	for i, ln := range lines {
		stepOut, note, err := e.runStep(sess, ln)
		if err != nil {
			out = append(out, fmt.Sprintf("✗ step %d: %s — %s", ln.Step, echoLine(ln), err.Error()))
			if i+1 < len(lines) {
				out = append(out, fmt.Sprintf("Steps %d–%d not run", lines[i+1].Step, lines[len(lines)-1].Step))
			}
			if pageNote != "" {
				out = append(out, pageNote)
			}
			return strings.Join(out, "\n"), err
		}
		if stepOut != "" {
			out = append(out, stepOut)
		} else {
			line := "✓ " + echoLine(ln)
			if note != "" {
				line += " — " + note
			}
			out = append(out, line)
		}
		if note != "" {
			pageNote = note
		}
	}
	if pageNote != "" {
		out = append(out, pageNote)
	}
	joined := strings.Join(out, "\n")
	if len(joined) > ResponseCap {
		joined = joined[:ResponseCap] + "\n… cut at " + fmt.Sprint(ResponseCap) + " characters"
	}
	return joined, nil
}

// echoLine is the command line as echoed in output, with typed text redacted.
func echoLine(ln CommandLine) string {
	switch ln.Name {
	case "fill", "type":
		return ln.Name + " <text>"
	}
	return ln.Source
}

// runStep runs one validated line.
func (e *Engine) runStep(sess *Session, ln CommandLine) (output, note string, err error) {
	def := CommandsByName[ln.Name]
	args, flags, err := ParseArgs(ln.Args, def.Flags)
	if err != nil {
		return "", "", err
	}
	if def.MaxArgs >= 0 && len(args) > def.MaxArgs {
		return "", "", fmt.Errorf("%s takes at most %d argument(s)", ln.Name, def.MaxArgs)
	}
	ctx := &Context{Engine: e, Session: sess, Args: args, Flags: flags, Line: ln}
	return def.Run(ctx)
}

// Context is what a command implementation receives.
type Context struct {
	Engine  *Engine
	Session *Session
	Args    []string
	Flags   map[string]string
	Line    CommandLine
}

// CurrentTab returns the session's current tab, forgetting it if closed.
func (e *Engine) CurrentTab(sess *Session) int {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Current != 0 && e.backend.TabClosed(sess.Current) {
		sess.Current = 0
	}
	return sess.Current
}

// ensureClient returns a CDP client for the session's current tab,
// creating a tab only when the session has none.
func (e *Engine) ensureClient(sess *Session) (*cdp.Client, *TabState, error) {
	tabID := e.CurrentTab(sess)
	if tabID == 0 {
		id, err := e.backend.CreateTab("about:blank", e.app)
		if err != nil {
			return nil, nil, err
		}
		sess.mu.Lock()
		sess.Current = id
		sess.Tabs[id] = true
		sess.Opened[id] = true
		sess.mu.Unlock()
		tabID = id
	}
	ts, err := e.tabStateFor(tabID)
	if err != nil {
		return nil, nil, err
	}
	return ts.InternalCDP, ts, nil
}

// eval runs JS in the page and returns the raw CDP result value.
func (e *Engine) eval(client *cdp.Client, expr string) (json.RawMessage, error) {
	raw, err := client.Call("Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	})
	if err != nil {
		return nil, err
	}
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		desc := res.ExceptionDetails.Text
		if res.ExceptionDetails.Exception != nil {
			desc = res.ExceptionDetails.Exception.Description
		}
		return nil, fmt.Errorf("%s", firstLine(desc))
	}
	return res.Result.Value, nil
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// readCap implements --max: default 10k, "all" or N capped at 75k.
func readCap(flag string, present bool) int {
	if !present {
		return ReadCap
	}
	if flag == "" || flag == "true" {
		return ReadCap
	}
	if flag == "all" {
		return ReadCapMax
	}
	var n int
	if _, err := fmt.Sscanf(flag, "%d", &n); err == nil && n > 0 {
		if n > ReadCapMax {
			return ReadCapMax
		}
		return n
	}
	return ReadCap
}

// cap truncates text at a nearby line break with a hint.
func cap(text string, max int) string {
	if len(text) <= max {
		return text
	}
	cut := max
	if i := strings.LastIndex(text[:max], "\n"); i >= max*4/5 {
		cut = i
	}
	return text[:cut] + fmt.Sprintf("\n… cut at %d of %d characters: --max all reads up to %d", cut, len(text), ReadCapMax)
}
