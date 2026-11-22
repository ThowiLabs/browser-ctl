// Package cdp provides a thin CDP client that talks to the browser through the
// extension's relay: attach to a tab, then send CDP commands and receive CDP events.
package cdp

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/thovilabs/browser-ctl/internal/protocol"
	"github.com/thovilabs/browser-ctl/internal/relay"
)

// Target is a CDP target (the attached tab).
type Target struct {
	ID         string `json:"targetId"`
	Type       string `json:"type"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	Attached   bool   `json:"attached"`
	BrowserID  string `json:"browserId"`
	WindowID   int    `json:"windowId"`
}

// Client sends CDP commands to one tab and receives its events.
type Client struct {
	conn    *relay.Connection
	tabID   int
	target  Target

	mu        sync.Mutex
	nextID    int
	listeners []func(string, json.RawMessage)
}

// Attach connects to a tab and enables the domains we need.
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
	c := &Client{conn: conn, tabID: tabID, target: t, nextID: 1}
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

// Call sends a CDP command and waits for its result. The extension relays the CDP result
// object as-is (its errors arrive as the response frame's error field).
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	rawParams, _ := json.Marshal(params)
	return c.conn.Request("cdp", protocol.CDPParams{
		TabID:  c.tabID,
		Method: method,
		Params: rawParams,
	})
}

// OnEvent registers a listener for CDP events (method, params).
func (c *Client) OnEvent(fn func(method string, params json.RawMessage)) {
	c.mu.Lock()
	c.listeners = append(c.listeners, fn)
	c.mu.Unlock()
}

// DispatchEvent routes an extension cdpEvent to the registered listeners.
func (c *Client) DispatchEvent(method string, params json.RawMessage) {
	c.mu.Lock()
	listeners := make([]func(string, json.RawMessage), len(c.listeners))
	copy(listeners, c.listeners)
	c.mu.Unlock()
	for _, fn := range listeners {
		fn(method, params)
	}
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
