package engine

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func doGet(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	if len(ctx.Args) == 0 {
		return "", "", fmt.Errorf("get needs text|value|count|url|title")
	}
	max := readCap(ctx.Flags["max"], func() bool { _, ok := ctx.Flags["max"]; return ok }())
	switch ctx.Args[0] {
	case "url":
		raw, err := ctx.Engine.eval(client, "location.href")
		if err != nil {
			return "", "", err
		}
		var s string
		_ = json.Unmarshal(raw, &s)
		return s, "", nil
	case "title":
		raw, err := ctx.Engine.eval(client, "document.title")
		if err != nil {
			return "", "", err
		}
		var s string
		_ = json.Unmarshal(raw, &s)
		return s, "", nil
	case "text":
		target := ""
		if len(ctx.Args) > 1 {
			target = strings.Join(ctx.Args[1:], " ")
		}
		expr := "document.body.innerText"
		if target != "" {
			tExpr, err := ResolveTarget(ctx.Engine, client, target)
			if err != nil {
				return "", "", err
			}
			expr = fmt.Sprintf("(function(){var el=%s; return el?el.innerText:'';})()", tExpr)
		}
		raw, err := ctx.Engine.eval(client, expr)
		if err != nil {
			return "", "", err
		}
		var s string
		_ = json.Unmarshal(raw, &s)
		// Collapse 3+ newlines, like JS.
		for strings.Contains(s, "\n\n\n") {
			s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
		}
		return cap(s, max), "", nil
	case "value":
		if len(ctx.Args) < 2 {
			return "", "", fmt.Errorf("get value needs a target")
		}
		tExpr, err := ResolveTarget(ctx.Engine, client, strings.Join(ctx.Args[1:], " "))
		if err != nil {
			return "", "", err
		}
		raw, err := ctx.Engine.eval(client, fmt.Sprintf("(function(){var el=%s; return el?el.value:'';})()", tExpr))
		if err != nil {
			return "", "", err
		}
		var s string
		_ = json.Unmarshal(raw, &s)
		return s, "", nil
	case "count":
		if len(ctx.Args) < 2 {
			return "", "", fmt.Errorf("get count needs a target")
		}
		target := strings.Join(ctx.Args[1:], " ")
		n, err := countForTarget(ctx.Engine, client, ctx.Session, target)
		if err != nil {
			return "", "", err
		}
		return strconv.Itoa(n), "", nil
	case "attr":
		if len(ctx.Args) < 3 {
			return "", "", fmt.Errorf("get attr needs <name> <target>")
		}
		tExpr, err := ResolveTarget(ctx.Engine, client, strings.Join(ctx.Args[2:], " "))
		if err != nil {
			return "", "", err
		}
		raw, err := ctx.Engine.eval(client, fmt.Sprintf("(function(){var el=%s; if(!el) return null; var v=el.getAttribute(%s); return v===null?'(none)':v;})()", tExpr, jsonString(ctx.Args[1])))
		if err != nil {
			return "", "", err
		}
		var s string
		_ = json.Unmarshal(raw, &s)
		return s, "", nil
	default:
		return "", "", fmt.Errorf("get: unknown subcommand %q", ctx.Args[0])
	}
}

func doEval(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	max := readCap(ctx.Flags["max"], func() bool { _, ok := ctx.Flags["max"]; return ok }())
	raw, err := ctx.Engine.eval(client, ctx.Args[0])
	if err != nil {
		return "", "", err
	}
	return cap(string(raw), max), "", nil
}

func doScreenshot(ctx *Context) (string, string, error) {
	client, ts, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	_ = ts
	path := ctx.Flags["path"]
	params := map[string]any{"format": "jpeg", "quality": 80}
	if _, full := ctx.Flags["full"]; full {
		params["captureBeyondViewport"] = true
	}
	raw, err := client.Call("Page.captureScreenshot", params)
	if err != nil {
		return "", "", err
	}
	var data struct {
		Data string `json:"data"`
	}
	_ = json.Unmarshal(raw, &data)
	img, err := base64.StdEncoding.DecodeString(data.Data)
	if err != nil {
		return "", "", err
	}
	if path != "" {
		if err := os.WriteFile(path, img, 0o644); err != nil {
			return "", "", err
		}
		return "screenshot saved to " + path, "", nil
	}
	// No path: return the image as a base64 data marker the MCP layer
	// turns into an image content block.
	return "data:image/jpeg;base64," + data.Data, "", nil
}

func doWait(ctx *Context) (string, string, error) {
	// wait <ms>
	if len(ctx.Args) > 0 {
		if ms, err := strconv.Atoi(ctx.Args[0]); err == nil {
			if ms > int(TimeoutMax.Milliseconds()) {
				ms = int(TimeoutMax.Milliseconds())
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
			return "", "", nil
		}
	}
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	timeout := 10 * time.Second
	if t, ok := ctx.Flags["timeout"]; ok {
		if ms, err := strconv.Atoi(t); err == nil {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if text, ok := ctx.Flags["text"]; ok {
			raw, err := ctx.Engine.eval(client, "document.body.innerText")
			if err == nil {
				var s string
				_ = json.Unmarshal(raw, &s)
				if strings.Contains(s, text) {
					return "", "", nil
				}
			}
		}
		if url, ok := ctx.Flags["url"]; ok {
			raw, err := ctx.Engine.eval(client, "location.href")
			if err == nil {
				var s string
				_ = json.Unmarshal(raw, &s)
				if urlMatch(url, s) {
					return "", "", nil
				}
			}
		}
		if gone, ok := ctx.Flags["gone"]; ok {
			if _, err := ResolveTarget(ctx.Engine, client, gone); err != nil {
				return "", "", nil
			}
			raw, err := ctx.Engine.eval(client, fmt.Sprintf("(function(){return !!document.querySelector(%s);})()", jsonString(gone)))
			if err == nil {
				var present bool
				_ = json.Unmarshal(raw, &present)
				if !present {
					return "", "", nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", "", fmt.Errorf("wait timed out after %s", timeout)
}

// urlMatch implements the JS urlMatcher glob: * -> [^/]*, ** -> .*, else
// substring.
func urlMatch(pattern, url string) bool {
	if strings.Contains(pattern, "*") {
		re := "^" + strings.ReplaceAll(strings.ReplaceAll(pattern, "**", "\x00"), "*", "[^/]*")
		re = strings.ReplaceAll(re, "\x00", ".*") + "$"
		matched, _ := regexp.MatchString(re, url)
		return matched
	}
	return strings.Contains(url, pattern)
}

func doConsole(ctx *Context) (string, string, error) {
	_, ts, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	_, errorsOnly := ctx.Flags["errors"]
	max := readCap(ctx.Flags["max"], func() bool { _, ok := ctx.Flags["max"]; return ok }())
	var lines []string
	for _, e := range ts.Console {
		if errorsOnly && e.Type != "error" {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", e.Type, e.Text))
	}
	for _, e := range ts.Uncaught {
		lines = append(lines, fmt.Sprintf("[uncaught] %s", e.Text))
	}
	if len(lines) == 0 {
		return "(no console output)", "", nil
	}
	// capTail: keep the newest end.
	joined := strings.Join(lines, "\n")
	if len(joined) > max {
		joined = "…\n" + joined[len(joined)-max:]
	}
	return joined, "", nil
}

func doRequests(ctx *Context) (string, string, error) {
	_, ts, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	max := readCap(ctx.Flags["max"], func() bool { _, ok := ctx.Flags["max"]; return ok }())
	filter := ctx.Flags["filter"]
	var lines []string
	for _, r := range ts.Requests {
		if filter != "" && !strings.Contains(r.URL, filter) {
			continue
		}
		lines = append(lines, fmt.Sprintf("#%d %s %d %s", r.ID, r.Method, r.Status, r.URL))
	}
	if len(lines) == 0 {
		return "(no requests)", "", nil
	}
	return cap(strings.Join(lines, "\n"), max), "", nil
}

func doRequest(ctx *Context) (string, string, error) {
	_, ts, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	id, _ := strconv.Atoi(ctx.Args[0])
	for _, r := range ts.Requests {
		if r.ID == id {
			out := fmt.Sprintf("#%d %s %s\nstatus: %d", r.ID, r.Method, r.URL, r.Status)
			if r.Failure != "" {
				out += "\nfailure: " + r.Failure
			}
			return out, "", nil
		}
	}
	return "", "", fmt.Errorf("no request #%d", id)
}
