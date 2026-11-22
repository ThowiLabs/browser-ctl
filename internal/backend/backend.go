// Package backend is the daemon's view of browsers and tabs: the public
// tab-ID registry, multi-browser slots, and the claim/release lifecycle
// for CDP access. It mirrors the JS RelayBackend, minus the Playwright
// bridge (v2 speaks raw CDP through the extension instead).
package backend

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/thovilabs/browser-ctl/internal/cdp"
	"github.com/thovilabs/browser-ctl/internal/protocol"
	"github.com/thovilabs/browser-ctl/internal/relay"
)

// SpareTabIDStart is the first spare public tab ID: when a Chrome tab ID
// collides across browsers, the later tab gets a spare from here up (JS).
const SpareTabIDStart = 100001

// BrowserSummary is what `browser list` reports for one browser.
type BrowserSummary struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Connected   bool   `json:"connected"`
	Pairing     string `json:"pairing"`
	Paused      bool   `json:"paused"`
	Tabs        int    `json:"tabs"`
	LastFocused *int64 `json:"lastFocusedAt,omitempty"`
	Usable      bool   `json:"usable"`
	Default     bool   `json:"default"`
}

// Tab is an exposed tab with its backend public ID and owning browser.
type Tab struct {
	protocol.ExposedTab
	Browser string `json:"browser"`
}

// Backend owns the browsers connected to the relay and the public-ID map.
type Backend struct {
	server *relay.Server

	mu        sync.Mutex
	refs      map[int]*Tab // public ID -> tab (kept forever after close)
	nextSpare int
	slots     map[string]*relay.Browser // profile -> browser
	order     []string                  // connection order (profile keys)
}

// New builds a backend over a running relay server.
func New(server *relay.Server) *Backend {
	return &Backend{
		server:    server,
		refs:      map[int]*Tab{},
		nextSpare: SpareTabIDStart,
		slots:     map[string]*relay.Browser{},
	}
}

// NoteBrowser registers a newly connected browser. Called by the relay
// server's extension handler (wired in daemon startup). It also gives the
// browser its event registry: the relay hands every cdpEvent to
// Browser.Events, so without it no CDP event ever reached the engine.
func (b *Backend) NoteBrowser(br *relay.Browser) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.slots[br.Profile]; !ok {
		b.order = append(b.order, br.Profile)
	}
	if br.Events == nil {
		br.Events = cdp.NewRegistry()
	}
	b.slots[br.Profile] = br
}

// ForgetBrowser removes a disconnected browser (its tabs stay in refs so
// stale IDs fail as closed tabs, never naming another tab).
func (b *Backend) ForgetBrowser(profile string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.slots, profile)
}

// Connected reports whether any usable browser is connected.
func (b *Backend) Connected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, br := range b.slots {
		if usable(br) {
			return true
		}
	}
	return false
}

func usable(br *relay.Browser) bool {
	st := br.Conn.State()
	return st.Pairing == "paired" && !st.Paused
}

// UnavailableReason explains why no browser can be used, or "" if one can.
func (b *Backend) UnavailableReason() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.slots) == 0 {
		return protocol.NoExtensionMessage
	}
	for _, br := range b.slots {
		st := br.Conn.State()
		if st.Paused {
			return protocol.PausedMessage
		}
		if st.Pairing != "paired" {
			return "not paired: click Allow in the extension's popup"
		}
	}
	return ""
}

// DefaultBrowser returns the last-focused usable browser, else the
// last-connected one, else nil.
func (b *Backend) DefaultBrowser() *relay.Browser {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.defaultLocked()
}

// refreshLocked assigns stable public IDs to every live tab (call with
// b.mu held). A tab keeps its Chrome ID as public ID unless that ID is
// already a public ID (taken by another browser's tab, live or closed);
// then it gets a spare from nextSpare up. Public IDs are never reused
// within a daemon run, so a stale ID can never name another tab.
// deuda-tecnica: solo probado en vivo con un navegador; la colision real
// entre dos navegadores solo esta cubierta por el test con extension falsa.
// Mejorar cuando haya multi-navegador real en pruebas.
func (b *Backend) refreshLocked() {
	if b.refs == nil {
		b.refs = map[int]*Tab{}
	}
	if b.nextSpare < SpareTabIDStart {
		b.nextSpare = SpareTabIDStart
	}
	for _, key := range b.order {
		br, ok := b.slots[key]
		if !ok {
			continue
		}
		for _, t := range br.Conn.State().Tabs {
			if _, ok := b.publicIDLocked(key, t.TabID); ok {
				continue // already mapped, keep its public ID
			}
			pub := t.TabID
			if _, taken := b.refs[pub]; taken {
				for {
					pub = b.nextSpare
					b.nextSpare++
					if _, taken := b.refs[pub]; !taken {
						break
					}
				}
			}
			b.refs[pub] = &Tab{ExposedTab: t, Browser: key}
		}
	}
}

// publicIDLocked returns the public ID of a tab, if mapped (call with
// b.mu held).
func (b *Backend) publicIDLocked(profile string, chromeID int) (int, bool) {
	for pub, tab := range b.refs {
		if tab.Browser == profile && tab.TabID == chromeID {
			return pub, true
		}
	}
	return 0, false
}

// resolveLocked translates a public ID to its browser and Chrome tab ID,
// only when the tab is still live in that browser (call with b.mu held).
func (b *Backend) resolveLocked(publicID int) (*relay.Browser, int, bool) {
	tab, ok := b.refs[publicID]
	if !ok {
		return nil, 0, false
	}
	br, ok := b.slots[tab.Browser]
	if !ok {
		return nil, 0, false
	}
	for _, t := range br.Conn.State().Tabs {
		if t.TabID == tab.TabID {
			return br, t.TabID, true
		}
	}
	return nil, 0, false
}

// Tabs returns all exposed tabs across browsers, with public IDs.
func (b *Backend) Tabs() []Tab {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refreshLocked()
	var out []Tab
	for _, key := range b.order {
		br, ok := b.slots[key]
		if !ok {
			continue
		}
		for _, t := range br.Conn.State().Tabs {
			pub, ok := b.publicIDLocked(key, t.TabID)
			if !ok {
				continue // refresh just mapped it; defensive
			}
			shown := t
			shown.TabID = pub
			out = append(out, Tab{ExposedTab: shown, Browser: key})
		}
	}
	return out
}

// Browsers returns a summary per known browser.
func (b *Backend) Browsers() []BrowserSummary {
	b.mu.Lock()
	defer b.mu.Unlock()
	def := b.defaultLocked()
	var out []BrowserSummary
	for i, key := range b.order {
		br, ok := b.slots[key]
		if !ok {
			continue
		}
		st := br.Conn.State()
		out = append(out, BrowserSummary{
			ID:          fmt.Sprintf("b%d", i+1),
			Label:       st.Label,
			Connected:   true,
			Pairing:     st.Pairing,
			Paused:      st.Paused,
			Tabs:        len(st.Tabs),
			LastFocused: st.FocusedAt,
			Usable:      usable(br),
			Default:     def != nil && def.Profile == key,
		})
	}
	return out
}

func (b *Backend) defaultLocked() *relay.Browser {
	var last *relay.Browser
	var lastFocused *relay.Browser
	var lastFocusedAt int64
	for _, key := range b.order {
		br, ok := b.slots[key]
		if !ok || !usable(br) {
			continue
		}
		last = br
		if st := br.Conn.State(); st.FocusedAt != nil && *st.FocusedAt > lastFocusedAt {
			lastFocusedAt = *st.FocusedAt
			lastFocused = br
		}
	}
	if lastFocused != nil {
		return lastFocused
	}
	return last
}

// FindTab returns the browser owning a public tab ID, or nil.
func (b *Backend) FindTab(publicID int) *relay.Browser {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refreshLocked()
	br, _, ok := b.resolveLocked(publicID)
	if !ok {
		return nil
	}
	return br
}

// TabClosed is true only when the owning browser is connected+paired and
// does not report the tab; false while the browser is away or pending.
func (b *Backend) TabClosed(publicID int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refreshLocked()
	if _, _, ok := b.resolveLocked(publicID); ok {
		return false
	}
	for _, br := range b.slots {
		if br.Conn.State().Pairing != "paired" {
			return false
		}
	}
	return len(b.slots) > 0
}

// CreateTab asks the default browser's extension to open a tab.
func (b *Backend) CreateTab(url string, app protocol.AppInfo) (int, error) {
	br := b.DefaultBrowser()
	if br == nil {
		return 0, fmt.Errorf("%s", protocol.NoExtensionMessage)
	}
	raw, err := br.Conn.Request("createTab", protocol.CreateTabParams{URL: url, App: app})
	if err != nil {
		return 0, err
	}
	var r protocol.CreateTabResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return 0, err
	}
	// The extension answers before its tab registry updates; wait
	// briefly so a following Claim finds the tab instead of failing
	// with "tab %d is not open". Returns the tab's public ID, not the
	// raw Chrome ID (they differ when the ID collides across browsers).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, t := range br.Conn.State().Tabs {
			if t.TabID == r.TabID {
				b.mu.Lock()
				b.refreshLocked()
				pub, ok := b.publicIDLocked(br.Profile, r.TabID)
				b.mu.Unlock()
				if ok {
					return pub, nil
				}
				return r.TabID, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return r.TabID, nil
}

// CloseTab asks the owning browser to close a tab.
func (b *Backend) CloseTab(publicID int) error {
	b.mu.Lock()
	b.refreshLocked()
	br, chromeID, ok := b.resolveLocked(publicID)
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("tab %d is not open", publicID)
	}
	_, err := br.Conn.Request("closeTab", protocol.CloseTabParams{TabID: chromeID})
	return err
}

// ActivateTab brings a tab to the front.
func (b *Backend) ActivateTab(publicID int) error {
	b.mu.Lock()
	b.refreshLocked()
	br, chromeID, ok := b.resolveLocked(publicID)
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("tab %d is not open", publicID)
	}
	_, err := br.Conn.Request("activateTab", protocol.ActivateTabParams{TabID: chromeID})
	return err
}

// GroupTab assigns a tab to an app's group (app nil = ungroup).
func (b *Backend) GroupTab(publicID int, app *protocol.AppInfo) error {
	b.mu.Lock()
	b.refreshLocked()
	br, chromeID, ok := b.resolveLocked(publicID)
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("tab %d is not open", publicID)
	}
	_, err := br.Conn.Request("groupTab", protocol.GroupTabParams{TabID: chromeID, App: app})
	return err
}

// Claim attaches the debugger to a tab and returns a CDP client.
// Only claimed tabs are CDP targets. The client is registered in the
// browser's event registry so the tab's CDP events reach the engine.
func (b *Backend) Claim(publicID int) (*cdp.Client, error) {
	b.mu.Lock()
	b.refreshLocked()
	br, chromeID, ok := b.resolveLocked(publicID)
	b.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("tab %d is not open", publicID)
	}
	client, err := cdp.Attach(br.Conn, chromeID)
	if err != nil {
		return nil, err
	}
	if br.Events == nil {
		br.Events = cdp.NewRegistry()
	}
	if reg, ok := br.Events.(*cdp.Registry); ok && reg != nil {
		reg.Register(client)
	}
	return client, nil
}

// WhenReady waits (up to 20 s) for a usable browser.
func (b *Backend) WhenReady() error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b.Connected() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	if reason := b.UnavailableReason(); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	return fmt.Errorf("%s", protocol.NoExtensionMessage)
}
