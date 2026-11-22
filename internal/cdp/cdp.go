// Package cdp provides a thin CDP client that talks to the browser through the
// extension's relay: attach to a tab, send CDP commands, and receive CDP events.
package cdp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/thovilabs/browser-ctl/internal/protocol"
	"github.com/thovilabs/browser-ctl/internal/relay"
)

// InputTimeout is how long an Input.* command waits for the extension before the
// client gives up (Chrome doesn't take input in a tab it isn't rendering).
const InputTimeout = 5 * time.Second

// Target is a CDP target (the attached tab).
type Target struct {
	ID        string `json:"targetId"`
	Type      string `json:"type"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Attached  bool   `json:"attached"`
	BrowserID string `json:"browserId"`
	WindowID  int    `json:"windowId"`
}

// event is a CDP event delivered to a tab (or one of its child sessions).
type event struct {
	method string
	params json.RawMessage
}

// Client sends CDP commands to one tab and receives its events.
type Client struct {
	conn   *relay.Connection
	tabID  int
	target Target

	mu        sync.Mutex
	inputMu   sync.Mutex
	listeners []func(string, json.RawMessage)
	eventCh   chan event
	done      chan struct{}
	closed    bool
}

// Attach connects to a tab and enables the domains the engine relies on.
func Attach(conn *relay.Connection, tabID int) (*Client, error) {
	raw, err := conn.Request("attach", protocol.AttachParams{TabID: tabID})
	if err != nil {
		return nil, err
	}
	var res protocol.AttachResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("bad attach result: %w", err)
	}
	t := Target{
		ID:       asString(res.TargetInfo["targetId"]),
		Type:     asString(res.TargetInfo["type"]),
		URL:      asString(res.TargetInfo["url"]),
		Title:    asString(res.TargetInfo["title"]),
		Attached: true,
	}
	if t.ID == "" {
		return nil, fmt.Errorf("attach returned no targetId")
	}
	c := &Client{conn: conn, tabID: tabID, target: t, eventCh: make(chan event, 64), done: make(chan struct{})}
	// Best-effort enable of the domains the command engine relies on.
	for _, domain := range []string{"Runtime", "Page", "DOM", "Network", "Log", "Target"} {
		c.Call(domain+".enable", map[string]any{})
	}
	return c, nil
}

// TabID returns the browser tab this client is attached to.
func (c *Client) TabID() int { return c.tabID }

// Target returns the CDP target info.
func (c *Client) Target() Target { return c.target }

// Call sends a CDP command and waits for its result. Input.* commands run one at a
// time per tab (mirroring the extension, which serializes them); everything else runs
// directly.
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	rawParams, _ := json.Marshal(params)
	if strings.HasPrefix(method, "Input.") {
		c.inputMu.Lock()
		raw, err := c.request("cdp", protocol.CDPParams{TabID: c.tabID, Method: method, Params: rawParams}, InputTimeout)
		c.inputMu.Unlock()
		return raw, err
	}
	return c.request("cdp", protocol.CDPParams{TabID: c.tabID, Method: method, Params: rawParams}, 0)
}

// request sends a relay request. The relay enforces its own per-method timeouts
// (RequestTimeoutMs for most, CDPRequestTimeoutMs for cdp); Input.* is bounded by the
// extension's INPUT_TIMEOUT_MS, so a stuck Input command surfaces as the relay timeout.
func (c *Client) request(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	return c.conn.Request(method, params)
}

// OnEvent registers a listener for CDP events (method, params).
func (c *Client) OnEvent(fn func(method string, params json.RawMessage)) {
	c.mu.Lock()
	c.listeners = append(c.listeners, fn)
	c.mu.Unlock()
	go c.pumpEvents()
}

// pumpEvents delivers queued events to the listeners without blocking the read loop.
func (c *Client) pumpEvents() {
	for ev := range c.eventCh {
		c.mu.Lock()
		listeners := make([]func(string, json.RawMessage), len(c.listeners))
		copy(listeners, c.listeners)
		closed := c.closed
		c.mu.Unlock()
		for _, fn := range listeners {
			fn(ev.method, ev.params)
		}
		_ = closed
	}
}

// DispatchEvent routes an extension cdpEvent (tab or session) to this client's listeners.
func (c *Client) DispatchEvent(method string, params json.RawMessage) {
	select {
	case c.eventCh <- event{method: method, params: params}:
	default:
		// Event buffer full; drop rather than block the relay read loop.
	}
}

// Close stops event pumping and detaches from the tab.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.done)
	c.mu.Unlock()
	return c.Detach()
}

// Detach detaches the debugger from the tab.
func (c *Client) Detach() error {
	_, err := c.conn.Request("detach", protocol.DetachParams{TabID: c.tabID})
	return err
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
