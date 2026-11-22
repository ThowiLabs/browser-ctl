package cmd

import (
	"encoding/json"
	"sync"

	"github.com/thovilabs/browser-ctl/internal/cdp"
)

// eventRegistry routes extension cdpEvent / detached events to the cdp.Client
// attached to the matching tab (or to a child session, e.g. an out-of-process iframe).
type eventRegistry struct {
	mu    sync.Mutex
	byTab map[int]*cdp.Client
}

func newEventRegistry() *eventRegistry {
	return &eventRegistry{byTab: map[int]*cdp.Client{}}
}

// Register tracks a client for its tab.
func (r *eventRegistry) Register(c *cdp.Client) {
	r.mu.Lock()
	r.byTab[c.TabID()] = c
	r.mu.Unlock()
}

// Unregister drops a client for its tab.
func (r *eventRegistry) Unregister(c *cdp.Client) {
	r.mu.Lock()
	if r.byTab[c.TabID()] == c {
		delete(r.byTab, c.TabID())
	}
	r.mu.Unlock()
}

// Dispatch routes a CDP event to the attached client.
func (r *eventRegistry) Dispatch(tabID int, sessionID, method string, params json.RawMessage) {
	r.mu.Lock()
	c := r.byTab[tabID]
	r.mu.Unlock()
	if c == nil {
		return
	}
	c.DispatchEvent(method, params)
	_ = sessionID
}

// DispatchDetached is a hook for the detached event; the engine re-attaches on the next
// use, so no action is required here.
func (r *eventRegistry) DispatchDetached(tabID int, reason string) {}
