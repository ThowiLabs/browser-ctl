package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/thovilabs/browser-ctl/internal/backend"
	"github.com/thovilabs/browser-ctl/internal/engine"
	"github.com/thovilabs/browser-ctl/internal/protocol"
	"github.com/thovilabs/browser-ctl/internal/relay"
)

// Daemon is the running long-lived process.
type Daemon struct {
	Port    int
	Token   string
	Backend *backend.Backend
	Engine  *engine.Engine
	server  *relay.Server
	home    string
	logf    func(string, ...any)

	mu       sync.Mutex
	sessions map[string]*clientSession
	stopped  chan string
	idleAt   time.Time
}

// clientSession is one /client connection (one MCP thin process or CLI).
type clientSession struct {
	stable bool // keep the engine session after this connection closes
	id     string
	app    engine.AppInfo
	caps   map[string]bool
	ws     *websocket.Conn
	engine *engine.Engine
	sess   *engine.Session
}

// Start starts the daemon: relay, backend, engine, daemon.json.
// It returns once the daemon is up; Stop shuts it down.
func Start(port int, logf func(string, ...any)) (*Daemon, error) {
	home := HomeDir()
	key, err := relay.LoadIdentity(home)
	if err != nil {
		return nil, err
	}
	token, err := NewToken()
	if err != nil {
		return nil, err
	}
	server, boundPort, err := relay.StartServer(port, key, Version(), logf)
	if err != nil {
		return nil, err
	}
	d := &Daemon{
		Port:     boundPort,
		Token:    token,
		home:     home,
		logf:     logf,
		server:   server,
		sessions: map[string]*clientSession{},
		stopped:  make(chan string, 1),
		idleAt:   time.Now().Add(engine.IdleRelease),
	}
	d.Backend = backend.New(server)
	d.Engine = engine.New(d.Backend, protocol.AppInfo{ID: "browser-ctl", Name: "browser-ctl"})
	// Wire browser connect/disconnect into the backend.
	// (relay.Server reports browsers via FirstBrowser/Browsers; the
	// backend is updated lazily from the server in Backend methods —
	// see NoteBrowser calls in the client handler below.)
	server.SetClientHandler(d.handleClient)
	server.SetShutdownHandler(d.handleShutdown)
	if err := writeInfo(home, &DaemonInfo{Port: boundPort, Pid: Pid(), Version: Version(), Token: token}); err != nil {
		server.Close()
		return nil, err
	}
	go d.idleLoop()
	go d.watchBrowsers(server)
	return d, nil
}

// watchBrowsers keeps the backend's slot list in sync with the relay.
func (d *Daemon) watchBrowsers(server *relay.Server) {
	for {
		time.Sleep(500 * time.Millisecond)
		for _, br := range server.Browsers() {
			d.Backend.NoteBrowser(br)
		}
		select {
		case <-d.stopped:
			return
		default:
		}
	}
}

func (d *Daemon) idleLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		d.mu.Lock()
		idle := len(d.sessions) == 0 && time.Now().After(d.idleAt)
		d.mu.Unlock()
		if idle {
			d.logf("daemon idle for %s, exiting", engine.IdleRelease)
			d.Stop("idle")
			return
		}
		d.Engine.ReleaseIdle()
	}
}

// Stop shuts the daemon down.
func (d *Daemon) Stop(reason string) {
	RemoveInfo(d.home, Pid())
	d.server.Close()
	select {
	case d.stopped <- reason:
	default:
	}
}

// Wait blocks until the daemon stops (signal or idle/shutdown).
func (d *Daemon) Wait() string {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case reason := <-d.stopped:
		return reason
	case s := <-sig:
		d.Stop(s.String())
		return s.String()
	}
}

// handleShutdown is POST /shutdown?token= — waits for the engine to be
// idle (up to 60 s) then stops.
func (d *Daemon) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("token") != d.Token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
	go func() {
		deadline := time.Now().Add(60 * time.Second)
		for d.Engine.Busy() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		d.Stop("shutdown")
	}()
}

var clientUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// handleClient is WS /client?token= — one thin process / CLI per socket.
func (d *Daemon) handleClient(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("token") != d.Token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ws, err := clientUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	// First message must be hello within 10 s.
	ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello struct {
		Type         string   `json:"type"`
		Protocol     int      `json:"protocol"`
		Version      string   `json:"version"`
		Client       string   `json:"client"`
		Capabilities []string `json:"capabilities"`
		Pid          int      `json:"pid"`
		ExtensionIDs []string `json:"extensionIds"`
		SessionID    string   `json:"sessionId"`
	}
	if err := ws.ReadJSON(&hello); err != nil || hello.Type != "hello" {
		ws.Close()
		return
	}
	ws.SetReadDeadline(time.Time{})
	// A client that sends sessionId reuses that engine session across
	// connections (the CLI does this with "cli"). Otherwise each
	// connection gets a fresh session, like the JS daemon.
	sessionID := hello.SessionID
	stable := sessionID != ""
	if !stable {
		sessionID = fmt.Sprintf("s-%d", time.Now().UnixNano())
	}
	caps := map[string]bool{}
	for _, c := range hello.Capabilities {
		caps[c] = true
	}
	app := engine.AppInfo{ID: appIDFor(hello.Client), Name: hello.Client}
	sess := d.Engine.OpenSession(sessionID, app, caps)
	cs := &clientSession{id: sessionID, app: app, caps: caps, ws: ws, engine: d.Engine, sess: sess, stable: stable}
	d.mu.Lock()
	d.sessions[sessionID] = cs
	d.mu.Unlock()
	_ = ws.WriteJSON(map[string]any{
		"type": "welcome", "protocol": 1, "version": Version(), "sessionId": sessionID,
	})
	d.serveClient(cs)
}

func appIDFor(client string) string {
	// Map known MCP client names to AppInfo ids, like JS app-info.ts.
	switch client {
	case "claude-code":
		return "claude-code"
	case "cursor-vscode":
		return "cursor"
	case "codex-mcp-client":
		return "codex"
	case "zed":
		return "zed"
	}
	if client == "" {
		return "browser-ctl"
	}
	return client
}

func (d *Daemon) serveClient(cs *clientSession) {
	defer func() {
		d.mu.Lock()
		delete(d.sessions, cs.id)
		if len(d.sessions) == 0 {
			d.idleAt = time.Now().Add(engine.IdleRelease)
		}
		d.mu.Unlock()
		// A stable session (CLI) survives disconnect: its current tab
		// is the next invocation's tab. It dies with the daemon.
		if !cs.stable {
			d.Engine.CloseSession(cs.id)
		}
		cs.ws.Close()
	}()
	for {
		_, data, err := cs.ws.ReadMessage()
		if err != nil {
			return
		}
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			continue
		}
		switch req.Method {
		case "tools/list":
			_ = cs.ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"tools": []any{map[string]any{
					"name": "browser", "description": "Control the user's Chrome: run browser commands, one per line.",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
						"commands": map[string]any{"type": "string"},
					}, "required": []string{"commands"}},
				}},
			}})
		case "tools/call":
			d.toolsCall(cs, req.ID, req.Params)
		default:
			_ = cs.ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found: " + req.Method}})
		}
	}
}

func (d *Daemon) toolsCall(cs *clientSession, id any, params json.RawMessage) {
	var p struct {
		Name         string   `json:"name"`
		Arguments    any      `json:"arguments"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		_ = cs.ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": "bad params"}})
		return
	}
	if p.Name != "browser" {
		_ = cs.ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": "unknown tool " + p.Name}})
		return
	}
	commands := ""
	if args, ok := p.Arguments.(map[string]any); ok {
		commands, _ = args["commands"].(string)
	}
	// Capabilities arrive in the call params (from `mcp --caps` or
	// `run --caps`); they update the session, e.g. "cdp".
	if len(p.Capabilities) > 0 {
		cs.sess.AddCapabilities(p.Capabilities...)
	}
	out, err := cs.engine.Run(cs.sess, commands)
	content := []any{map[string]any{"type": "text", "text": out}}
	if err != nil {
		// Run returns the partial output plus the error; surface both.
		if out == "" {
			content = []any{map[string]any{"type": "text", "text": "Error: " + err.Error()}}
		}
		_ = cs.ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"content": content, "isError": true}})
		return
	}
	_ = cs.ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"content": content, "isError": false}})
}
