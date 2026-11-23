package engine

import (
	"encoding/json"
	"strings"
	"sync"
)

// captureEvent feeds a tab's console/request/CDP-event buffers from one
// CDP event. Buffers are capped at 500 entries (oldest dropped), like JS.
// Favicon 404 console errors are filtered out.

var captureMu sync.Mutex

func (e *Engine) captureEvent(ts *TabState, method string, params json.RawMessage) {
	captureMu.Lock()
	defer captureMu.Unlock()
	ts.Seq++
	switch method {
	case "Runtime.consoleAPICalled":
		var ev struct {
			Type string `json:"type"`
			Args []struct {
				Value       json.RawMessage `json:"value"`
				Description string          `json:"description"`
			} `json:"args"`
			StackTrace *struct {
				CallFrames []struct {
					URL          string `json:"url"`
					LineNumber   int    `json:"lineNumber"`
					ColumnNumber int    `json:"columnNumber"`
				} `json:"callFrames"`
			} `json:"stackTrace"`
		}
		if json.Unmarshal(params, &ev) != nil {
			return
		}
		var parts []string
		for _, a := range ev.Args {
			if len(a.Value) > 0 {
				var s string
				if json.Unmarshal(a.Value, &s) == nil {
					parts = append(parts, s)
				} else {
					parts = append(parts, string(a.Value))
				}
			} else if a.Description != "" {
				parts = append(parts, a.Description)
			}
		}
		text := strings.Join(parts, " ")
		loc := ""
		if ev.StackTrace != nil && len(ev.StackTrace.CallFrames) > 0 {
			f := ev.StackTrace.CallFrames[0]
			loc = f.URL
		}
		if ev.Type == "error" && strings.Contains(text, "favicon.ico") && strings.Contains(text, "404") {
			return
		}
		ts.Console = appendCapped(ts.Console, ConsoleEntry{Type: ev.Type, Text: text, Location: loc, Seq: ts.Seq})
		if ev.Type == "error" {
			ts.ErrorCount++
			if ts.LastError == "" {
				ts.LastError = firstLine(text)
			}
		}
	case "Runtime.exceptionThrown":
		var ev struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				Exception *struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(params, &ev) != nil {
			return
		}
		text := ev.ExceptionDetails.Text
		if ev.ExceptionDetails.Exception != nil {
			text = ev.ExceptionDetails.Exception.Description
		}
		ts.Uncaught = appendCapped(ts.Uncaught, ConsoleEntry{Type: "uncaught", Text: text, Seq: ts.Seq})
		ts.ErrorCount++
		if ts.LastError == "" {
			ts.LastError = firstLine(text)
		}
	case "Log.entryAdded":
		var ev struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
				URL   string `json:"url"`
			} `json:"entry"`
		}
		if json.Unmarshal(params, &ev) != nil {
			return
		}
		if ev.Entry.Level == "error" {
			ts.Console = appendCapped(ts.Console, ConsoleEntry{Type: "error", Text: ev.Entry.Text, Location: ev.Entry.URL, Seq: ts.Seq})
		}
	case "Network.requestWillBeSent":
		var ev struct {
			RequestID string `json:"requestId"`
			Request   struct {
				Method string `json:"method"`
				URL    string `json:"url"`
			} `json:"request"`
		}
		if json.Unmarshal(params, &ev) != nil {
			return
		}
		id := len(ts.Requests) + 1
		ts.Requests = appendCapped(ts.Requests, RequestEntry{ID: id, Method: ev.Request.Method, URL: ev.Request.URL, CDPID: ev.RequestID})
	case "Network.responseReceived":
		var ev struct {
			RequestID string `json:"requestId"`
			Response  struct {
				Status int `json:"status"`
			} `json:"response"`
		}
		if json.Unmarshal(params, &ev) != nil {
			return
		}
		// Match by CDP request id; fall back to order (last without status).
		matched := false
		for i := range ts.Requests {
			if ts.Requests[i].CDPID == ev.RequestID && ts.Requests[i].Status == 0 {
				ts.Requests[i].Status = ev.Response.Status
				matched = true
				break
			}
		}
		if !matched {
			for i := len(ts.Requests) - 1; i >= 0; i-- {
				if ts.Requests[i].Status == 0 {
					ts.Requests[i].Status = ev.Response.Status
					break
				}
			}
		}
	case "Network.loadingFailed":
		var ev struct {
			RequestID string `json:"requestId"`
			ErrorText string `json:"errorText"`
			Canceled  bool   `json:"canceled"`
		}
		if json.Unmarshal(params, &ev) != nil {
			return
		}
		matched := false
		for i := range ts.Requests {
			if ts.Requests[i].CDPID == ev.RequestID && ts.Requests[i].Failure == "" {
				ts.Requests[i].Failure = ev.ErrorText
				matched = true
				break
			}
		}
		if !matched {
			for i := len(ts.Requests) - 1; i >= 0; i-- {
				if ts.Requests[i].Failure == "" {
					ts.Requests[i].Failure = ev.ErrorText
					break
				}
			}
		}
	case "Page.javascriptDialogOpening":
		// A JS dialog opened: remember it so the engine can block other
		// steps and `dialog` can answer it (JS TabState.dialog, set by
		// page.on("dialog")).
		var ev struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &ev) != nil {
			return
		}
		ts.Dialog = &Dialog{Type: ev.Type, Message: ev.Message}
	case "Page.javascriptDialogClosed":
		// Answered in Chrome (or by `dialog`): the dialog is gone.
		ts.Dialog = nil
	}
	// Agent-subscribed CDP events (cdp on <event>).
	for _, sub := range ts.CDPEvents {
		if sub == method {
			ts.CDPEventLog = appendCapped(ts.CDPEventLog, method+" "+string(params))
			break
		}
	}
}

func appendCapped[T any](buf []T, v T) []T {
	buf = append(buf, v)
	if len(buf) > 500 {
		buf = buf[len(buf)-500:]
	}
	return buf
}
