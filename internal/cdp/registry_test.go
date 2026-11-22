package cdp

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRegistryDispatch(t *testing.T) {
	reg := NewRegistry()
	c := &Client{tabID: 7, eventCh: make(chan event, 64), done: make(chan struct{})}
	got := make(chan string, 1)
	c.OnEvent(func(method string, params json.RawMessage) {
		got <- method + " " + string(params)
	})
	reg.Register(c)
	reg.Dispatch(7, "", "Page.javascriptDialogOpening", json.RawMessage(`{"type":"alert"}`))
	select {
	case m := <-got:
		if m != `Page.javascriptDialogOpening {"type":"alert"}` {
			t.Fatalf("got %q", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event not delivered")
	}
	// Events for other tabs are dropped.
	reg.Dispatch(8, "", "Page.javascriptDialogOpening", json.RawMessage(`{}`))
	select {
	case m := <-got:
		t.Fatalf("unexpected event for another tab: %q", m)
	case <-time.After(100 * time.Millisecond):
	}
	// After unregister, nothing arrives.
	reg.Unregister(7)
	reg.Dispatch(7, "", "Page.javascriptDialogOpening", json.RawMessage(`{}`))
	select {
	case m := <-got:
		t.Fatalf("event after unregister: %q", m)
	case <-time.After(100 * time.Millisecond):
	}
	// DispatchDetached unregisters too.
	reg.Register(c)
	reg.DispatchDetached(7, "closed")
	reg.Dispatch(7, "", "Page.javascriptDialogOpening", json.RawMessage(`{}`))
	select {
	case m := <-got:
		t.Fatalf("event after detach: %q", m)
	case <-time.After(100 * time.Millisecond):
	}
}
