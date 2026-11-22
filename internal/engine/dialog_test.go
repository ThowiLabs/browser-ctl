package engine

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCaptureDialogEvents(t *testing.T) {
	e := &Engine{}
	ts := &TabState{}
	openParams, _ := json.Marshal(map[string]any{
		"type": "alert", "message": "I am a JS Alert",
	})
	e.captureEvent(ts, "Page.javascriptDialogOpening", openParams)
	if ts.Dialog == nil || ts.Dialog.Type != "alert" || ts.Dialog.Message != "I am a JS Alert" {
		t.Fatalf("dialog not captured: %+v", ts.Dialog)
	}
	// The agent-subscribed log also records it when subscribed (no filter here).
	e.captureEvent(ts, "Page.javascriptDialogClosed", json.RawMessage(`{}`))
	if ts.Dialog != nil {
		t.Fatalf("dialog not cleared: %+v", ts.Dialog)
	}
}

func TestDialogLabel(t *testing.T) {
	if got := dialogLabel(&Dialog{Type: "confirm", Message: "Are you sure?"}); got != `confirm dialog "Are you sure?"` {
		t.Fatalf("label = %q", got)
	}
}

func TestFinishAbandoned(t *testing.T) {
	ts := &TabState{}
	ch := make(chan struct{})
	ts.Abandoned = ch
	close(ch)
	finishAbandonedTimeout(ts, 50*time.Millisecond)
	if ts.Abandoned != nil {
		t.Fatal("abandoned not cleared")
	}
	// An unfinished step: returns after the timeout, doesn't hang.
	ts.Abandoned = make(chan struct{})
	start := time.Now()
	finishAbandonedTimeout(ts, 50*time.Millisecond)
	if time.Since(start) > 5*time.Second {
		t.Fatal("finishAbandoned hung")
	}
	if ts.Abandoned != nil {
		t.Fatal("abandoned not cleared after timeout")
	}
}

func TestStepTimeout(t *testing.T) {
	if got := stepTimeout(CommandsByName["click"], nil, nil); got != DefaultStepTimeout {
		t.Fatalf("click timeout = %v", got)
	}
	if got := stepTimeout(CommandsByName["goto"], nil, nil); got != GotoStepTimeout+StepHeadroom {
		t.Fatalf("goto timeout = %v", got)
	}
	wait := CommandsByName["wait"]
	if got := stepTimeout(wait, []string{"8000"}, nil); got != DefaultStepTimeout {
		t.Fatalf("wait 8000 timeout = %v", got)
	}
	if got := stepTimeout(wait, []string{"60000"}, nil); got != 65*time.Second {
		t.Fatalf("wait 60000 timeout = %v", got)
	}
	if got := stepTimeout(wait, nil, map[string]string{"timeout": "60000"}); got != 65*time.Second {
		t.Fatalf("wait --timeout timeout = %v", got)
	}
}

func TestStepWhileDialog(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{"dialog accept", true},
		{"dialog dismiss", true},
		{"tab list", true},
		{"browser list", true},
		{"help", true},
		{"wait 5000", true},
		{"wait --text \"hello\"", false},
		{"wait --gone e1", false},
		{"click e1", false},
		{"snapshot", false},
		{"get title", false},
	}
	for _, c := range cases {
		lines, err := ParseCommands(c.src)
		if err != nil || len(lines) != 1 {
			t.Fatalf("ParseCommands(%q): %v", c.src, err)
		}
		if got := stepWhileDialog(lines[0]); got != c.want {
			t.Errorf("stepWhileDialog(%q) = %v, want %v", c.src, got, c.want)
		}
	}
}

func TestFormatSeconds(t *testing.T) {
	if got := formatSeconds(30 * time.Second); got != "30 s" {
		t.Fatalf("formatSeconds = %q", got)
	}
	if got := formatSeconds(65 * time.Second); got != "65 s" {
		t.Fatalf("formatSeconds = %q", got)
	}
}
