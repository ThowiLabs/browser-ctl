package engine

import (
	"encoding/json"
	"testing"

	"github.com/thovilabs/browser-ctl/internal/protocol"
)

func TestCaptureConsoleAndRequests(t *testing.T) {
	e := New(nil, protocol.AppInfo{ID: "x", Name: "x"})
	_ = e
	ts := &TabState{}
	consoleParams, _ := json.Marshal(map[string]any{
		"type": "error",
		"args": []any{map[string]any{"value": "boom"}},
	})
	e.captureEvent(ts, "Runtime.consoleAPICalled", consoleParams)
	if len(ts.Console) != 1 || ts.Console[0].Text != "boom" {
		t.Fatalf("console buffer: %+v", ts.Console)
	}
	if ts.ErrorCount != 1 {
		t.Fatalf("errorCount=%d", ts.ErrorCount)
	}
	// Favicon 404s are filtered.
	favParams, _ := json.Marshal(map[string]any{
		"type": "error",
		"args": []any{map[string]any{"value": "Failed to load resource: favicon.ico 404"}},
	})
	e.captureEvent(ts, "Runtime.consoleAPICalled", favParams)
	if len(ts.Console) != 1 {
		t.Fatalf("favicon 404 should be filtered, buffer: %+v", ts.Console)
	}
	reqParams, _ := json.Marshal(map[string]any{
		"requestId": "1",
		"request":   map[string]any{"method": "GET", "url": "https://example.com/"},
	})
	e.captureEvent(ts, "Network.requestWillBeSent", reqParams)
	if len(ts.Requests) != 1 || ts.Requests[0].Method != "GET" {
		t.Fatalf("requests buffer: %+v", ts.Requests)
	}
	respParams, _ := json.Marshal(map[string]any{
		"requestId": "1",
		"response":  map[string]any{"status": 200},
	})
	e.captureEvent(ts, "Network.responseReceived", respParams)
	if ts.Requests[0].Status != 200 {
		t.Fatalf("status=%d", ts.Requests[0].Status)
	}
	// Agent-subscribed CDP events are buffered.
	ts.CDPEvents = []string{"Page.loadEventFired"}
	e.captureEvent(ts, "Page.loadEventFired", json.RawMessage(`{}`))
	if len(ts.CDPEventLog) != 1 {
		t.Fatalf("CDPEventLog: %v", ts.CDPEventLog)
	}
}

func TestAppendCapped(t *testing.T) {
	var buf []int
	for i := 0; i < 600; i++ {
		buf = appendCapped(buf, i)
	}
	if len(buf) != 500 || buf[0] != 100 {
		t.Fatalf("len=%d first=%d", len(buf), buf[0])
	}
}
