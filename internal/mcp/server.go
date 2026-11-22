// Package mcp is the MCP server over stdio: the thin client from the JS
// system, reimplemented in Go with no Node. It speaks JSON-RPC 2.0 over
// stdin/stdout, exposes exactly one tool (`browser`), and forwards
// tools/list + tools/call to the daemon over its /client WebSocket.
// The daemon calls back over the same socket for readFile/writeFile.
package mcp

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Instructions shown to the MCP client (short, like the JS thin server).
const Instructions = `Act in batches with the browser tool. Look with snapshot -i or find "<text>".
Read a whole page with get text --max all. Re-snapshot after navigating — old refs fail.`

// Tool definition for the single browser tool.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func browserTool() Tool {
	return Tool{
		Name:        "browser",
		Description: "Control the user's Chrome: run browser commands, one per line.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"commands": map[string]any{"type": "string", "description": "Command lines, one per line"},
			},
			"required": []string{"commands"},
		},
	}
}

// Client is the daemon connection (a /client WebSocket).
type Client struct {
	ws      *websocket.Conn
	mu      sync.Mutex
	next    int
	pending map[int]chan rpcResult
	// file access handlers (reverse RPC from the daemon)
	roots []string
	home  string
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

// Dial connects to the daemon's /client endpoint with its token.
// The session is fresh for every connection.
func Dial(port int, token string) (*Client, error) {
	return DialSession(port, token, "browser-ctl-mcp", "")
}

// DialSession connects like Dial, but identifies the client and, when
// sessionID is not empty, asks the daemon to reuse that engine session
// across connections. The CLI passes sessionID "cli" so separate `run`
// invocations share one current tab instead of opening a new tab each.
func DialSession(port int, token, client, sessionID string) (*Client, error) {
	url := fmt.Sprintf("ws://127.0.0.1:%d/client?token=%s", port, token)
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	c := &Client{ws: ws, pending: map[int]chan rpcResult{}, home: mustHome()}
	// hello
	hello := map[string]any{
		"type": "hello", "protocol": 1, "version": "2.0.0",
		"client": client, "capabilities": []string{}, "pid": os.Getpid(),
		"sessionId": sessionID,
	}
	if err := ws.WriteJSON(hello); err != nil {
		ws.Close()
		return nil, err
	}
	// first message must be welcome
	ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var welcome map[string]any
	if err := ws.ReadJSON(&welcome); err != nil {
		ws.Close()
		return nil, fmt.Errorf("daemon did not welcome: %w", err)
	}
	ws.SetReadDeadline(time.Time{})
	go c.readLoop()
	return c, nil
}

func mustHome() string {
	home, _ := os.UserHomeDir()
	return home
}

func (c *Client) readLoop() {
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		var method string
		_ = json.Unmarshal(msg["method"], &method)
		if method != "" {
			// Reverse RPC from the daemon (readFile/writeFile).
			c.handleReverse(data)
			continue
		}
		var id int
		_ = json.Unmarshal(msg["id"], &id)
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if errMsg, ok := msg["error"]; ok {
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(errMsg, &e)
			ch <- rpcResult{err: fmt.Errorf("%s", e.Message)}
			continue
		}
		ch <- rpcResult{result: msg["result"]}
	}
}

func (c *Client) handleReverse(data []byte) {
	var req struct {
		ID     int             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return
	}
	var result any
	var errStr string
	switch req.Method {
	case "readFile":
		var p struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(req.Params, &p)
		result, errStr = c.readFile(p.Path)
	case "writeFile":
		var p struct {
			Path      string `json:"path"`
			Base64    string `json:"base64"`
			Exclusive bool   `json:"exclusive"`
		}
		_ = json.Unmarshal(req.Params, &p)
		result, errStr = c.writeFile(p.Path, p.Base64, p.Exclusive)
	default:
		errStr = "unknown method " + req.Method
	}
	resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if errStr != "" {
		resp["error"] = map[string]any{"code": -32603, "message": errStr}
	} else {
		resp["result"] = result
	}
	c.mu.Lock()
	_ = c.ws.WriteJSON(resp)
	c.mu.Unlock()
}

func (c *Client) resolveFile(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(c.home, path[2:])
	}
	if filepath.IsAbs(path) {
		return path
	}
	if len(c.roots) > 0 {
		return filepath.Join(c.roots[0], path)
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, path)
}

func (c *Client) readFile(path string) (any, string) {
	abs := c.resolveFile(path)
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err.Error()
	}
	return map[string]any{
		"name":     filepath.Base(abs),
		"mimeType": mime.TypeByExtension(filepath.Ext(abs)),
		"base64":   base64.StdEncoding.EncodeToString(data),
	}, ""
}

func (c *Client) writeFile(path, b64 string, exclusive bool) (any, string) {
	abs := c.resolveFile(path)
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err.Error()
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err.Error()
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if exclusive {
		flag = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(abs, flag, 0o644)
	if err != nil {
		return nil, err.Error()
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return nil, err.Error()
	}
	return map[string]any{"path": abs}, ""
}

// Call makes a JSON-RPC call to the daemon.
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	id := c.next
	c.next++
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := c.ws.WriteJSON(req); err != nil {
		return nil, err
	}
	select {
	case res := <-ch:
		return res.result, res.err
	case <-time.After(5*time.Minute + 30*time.Second):
		return nil, fmt.Errorf("daemon did not answer %s in time", method)
	}
}

// Close closes the daemon connection.
func (c *Client) Close() { c.ws.Close() }

// SetRoots sets the MCP roots used to resolve relative file paths.
func (c *Client) SetRoots(roots []string) { c.roots = roots }

// Server is the MCP stdio server.
type Server struct {
	client *Client
	caps   map[string]bool
}

// New builds an MCP server over a daemon client.
func New(client *Client, caps map[string]bool) *Server {
	return &Server{client: client, caps: caps}
}

// Serve reads JSON-RPC requests from stdin and writes responses to stdout.
func (s *Server) Serve() error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		resp := s.handle(line)
		if resp == nil {
			continue
		}
		data, _ := json.Marshal(resp)
		writer.Write(data)
		writer.WriteByte('\n')
		writer.Flush()
	}
	return scanner.Err()
}

func (s *Server) handle(line []byte) map[string]any {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      any             `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}}
	}
	switch req.Method {
	case "initialize":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "Browser MCP", "version": "2.0.0"},
			"instructions":    Instructions,
		}}
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "tools/list":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []Tool{browserTool()}}}
	case "tools/call":
		return s.toolsCall(req.ID, req.Params)
	case "ping":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}
	default:
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found: " + req.Method}}
	}
}

func (s *Server) toolsCall(id any, params json.RawMessage) map[string]any {
	var p struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errResult(id, "bad tools/call params")
	}
	if p.Name != "browser" {
		return errResult(id, "unknown tool "+p.Name)
	}
	// Forward to the daemon, with this client's capabilities.
	args := map[string]any{"name": "browser", "arguments": p.Arguments, "capabilities": capsList(s.caps)}
	raw, err := s.client.Call("tools/call", args)
	if err != nil {
		return textResult(id, "Error: "+err.Error(), true)
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return textResult(id, string(raw), false)
	}
	contents := make([]any, 0, len(res.Content))
	for _, c := range res.Content {
		contents = append(contents, map[string]any{"type": c.Type, "text": c.Text})
	}
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"content": contents, "isError": res.IsError}}
}

func capsList(caps map[string]bool) []string {
	var out []string
	for k, v := range caps {
		if v {
			out = append(out, k)
		}
	}
	return out
}

func errResult(id any, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": msg}}
}

func textResult(id any, text string, isError bool) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": isError,
	}}
}
