// Package cmd implements the browser-ctl command engine: it parses the one-line-per-command
// syntax (goto, click, fill, snapshot, ...) and executes it against the browser through CDP.
package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/thovilabs/browser-ctl/internal/cdp"
	"github.com/thovilabs/browser-ctl/internal/protocol"
	"github.com/thovilabs/browser-ctl/internal/relay"
)

const appID = "browser-ctl"
const appName = "browser-ctl"

// Engine executes commands against a browser.
type Engine struct {
	conn     *relay.Connection
	registry *eventRegistry
	app      protocol.AppInfo
	tabs     map[int]bool
	current  int
	logf     func(string, ...any)

	// refs maps e# refs (from the last snapshot) to their DOM index.
	refs map[string]int
}

// NewEngine builds an engine over a connected browser and wires its CDP-event registry
// into the browser so extension cdpEvent / detached events reach the clients.
func NewEngine(browser *relay.Browser, logf func(string, ...any)) *Engine {
	reg := newEventRegistry()
	browser.Events = reg
	return &Engine{
		conn:     browser.Conn,
		registry: reg,
		app:      protocol.AppInfo{ID: appID, Name: appName},
		tabs:     map[int]bool{},
		logf:     logf,
		refs:     map[string]int{},
	}
}

type commandResult struct {
	Line   string
	Output string
}

// Run parses and executes a batch of commands (one per line), stopping at the first error.
func (e *Engine) Run(commands string) (string, error) {
	lines := strings.Split(commands, "\n")
	var out []string
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		res, err := e.exec(line)
		if err != nil {
			return strings.Join(out, "\n"), fmt.Errorf("step %d: %v", i+1, err)
		}
		if res.Output != "" {
			out = append(out, res.Output)
		} else {
			out = append(out, "✓ "+line)
		}
	}
	return strings.Join(out, "\n"), nil
}

func (e *Engine) exec(line string) (commandResult, error) {
	parts := strings.SplitN(line, " ", 2)
	cmd := parts[0]
	var args string
	if len(parts) > 1 {
		args = parts[1]
	}
	res := commandResult{}
	var err error
	switch cmd {
	case "goto":
		err = e.doGoto(args, &res)
	case "back":
		err = e.goHistory(-10, &res)
	case "forward":
		err = e.goHistory(10, &res)
	case "reload":
		_, err = e.cdpCall("Page.reload", map[string]any{})
		res.Output = "reloaded"
	case "click":
		err = e.doClick(args, &res)
	case "hover":
		err = e.doHover(args, &res)
	case "fill":
		err = e.doFill(args, &res)
	case "type":
		err = e.doType(args, &res)
	case "press":
		err = e.doPress(args, &res)
	case "select":
		err = e.doSelect(args, &res)
	case "check":
		err = e.doCheck(args, false, &res)
	case "uncheck":
		err = e.doCheck(args, true, &res)
	case "wait":
		err = e.doWait(args, &res)
	case "snapshot":
		err = e.doSnapshot(args, &res)
	case "find":
		err = e.doFind(args, &res)
	case "get":
		err = e.doGet(args, &res)
	case "screenshot":
		err = e.doScreenshot(args, &res)
	case "eval":
		err = e.doEval(args, &res)
	case "tab":
		err = e.doTab(args, &res)
	case "scroll":
		err = e.doScroll(args, &res)
	case "drag":
		err = e.doDrag(args, &res)
	case "browser":
		res.Output = "1 browser connected"
	case "help":
		res.Output = helpText
	default:
		err = fmt.Errorf("unknown command %q (try help)", cmd)
	}
	if err != nil {
		return res, err
	}
	return res, nil
}

// --- Navigation ---

func (e *Engine) doGoto(args string, res *commandResult) error {
	url := normalizeURL(args)
	if err := requireExposedURL(url); err != nil {
		return err
	}
	if e.current == 0 {
		tabID, err := e.createTab(url)
		if err != nil {
			return err
		}
		e.current = tabID
		e.tabs[tabID] = true
	}
	client, err := e.attach(e.current)
	if err != nil {
		return err
	}
	client.Call("Page.enable", map[string]any{})
	if _, err := client.Call("Page.navigate", map[string]any{"url": url}); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	res.Output = "→ " + url
	return nil
}

func (e *Engine) goHistory(delta int, res *commandResult) error {
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	_, err = client.Call("Page.navigateHistory", map[string]any{"delta": delta})
	return err
}

func (e *Engine) createTab(url string) (int, error) {
	raw, err := e.conn.Request("createTab", protocol.CreateTabParams{URL: url, App: e.app})
	if err != nil {
		return 0, err
	}
	var r protocol.CreateTabResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return 0, err
	}
	return r.TabID, nil
}

func (e *Engine) attach(tabID int) (*cdp.Client, error) {
	client, err := cdp.Attach(e.conn, tabID)
	if err != nil {
		return nil, err
	}
	if e.registry != nil {
		e.registry.Register(client)
	}
	return client, nil
}

// ensureClient returns a client for the current tab, creating the tab if needed.
func (e *Engine) ensureClient() (*cdp.Client, error) {
	state := e.conn.State()
	if state.Paused {
		return nil, fmt.Errorf("the browser is paused (press Resume in the extension's popup)")
	}
	if state.Pairing != "paired" {
		return nil, fmt.Errorf("not paired: click Allow in the extension's popup")
	}
	if e.current == 0 {
		tabID, err := e.createTab("about:blank")
		if err != nil {
			return nil, err
		}
		e.current = tabID
		e.tabs[tabID] = true
	}
	return e.attach(e.current)
}

// --- Actions ---

func (e *Engine) doClick(args string, res *commandResult) error {
	flags := parseClickFlags(args)
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	idx, err := e.resolveTarget(client, flags.target)
	if err != nil {
		return err
	}
	return e.clickNode(client, idx, flags)
}

func (e *Engine) clickNode(client *cdp.Client, idx int, f clickFlagsParsed) error {
	box, err := e.nodeBox(client, idx)
	if err != nil {
		return err
	}
	x := box.X + box.Width/2
	y := box.Y + box.Height/2
	button := f.button
	if button == "" {
		button = "left"
	}
	counts := []int{1}
	if f.double {
		counts = []int{1, 2}
	}
	for _, count := range counts {
		if _, err := client.Call("Input.dispatchMouseEvent", map[string]any{
			"type": "mousePressed", "x": x, "y": y, "button": button, "clickCount": count, "modifiers": f.mod,
		}); err != nil {
			return err
		}
		if _, err := client.Call("Input.dispatchMouseEvent", map[string]any{
			"type": "mouseReleased", "x": x, "y": y, "button": button, "clickCount": count, "modifiers": f.mod,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) doHover(args string, res *commandResult) error {
	target := strings.TrimSpace(args)
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	idx, err := e.resolveTarget(client, target)
	if err != nil {
		return err
	}
	box, err := e.nodeBox(client, idx)
	if err != nil {
		return err
	}
	_, err = client.Call("Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved", "x": box.X + box.Width/2, "y": box.Y + box.Height/2,
	})
	return err
}

func (e *Engine) doFill(args string, res *commandResult) error {
	target, text, submit, err := parseFill(args)
	if err != nil {
		return err
	}
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	idx, err := e.resolveTarget(client, target)
	if err != nil {
		return err
	}
	if err := e.clickNode(client, idx, clickFlagsParsed{button: "left"}); err != nil {
		return err
	}
	if _, err := client.Call("Input.insertText", map[string]any{"text": text}); err != nil {
		return err
	}
	if submit {
		for _, typ := range []string{"keyDown", "keyUp"} {
			if _, err := client.Call("Input.dispatchKeyEvent", map[string]any{
				"type": typ, "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) doType(args string, res *commandResult) error {
	text, _, err := parseType(args)
	if err != nil {
		return err
	}
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	_, err = client.Call("Input.insertText", map[string]any{"text": text})
	return err
}

func (e *Engine) doPress(args string, res *commandResult) error {
	key := strings.TrimSpace(args)
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	info := keyInfo(key)
	for _, typ := range []string{"keyDown", "keyUp"} {
		if _, err := client.Call("Input.dispatchKeyEvent", map[string]any{
			"type": typ, "key": info.key, "code": info.code, "windowsVirtualKeyCode": info.vk,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) doSelect(args string, res *commandResult) error {
	target, values, err := parseSelect(args)
	if err != nil {
		return err
	}
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	idx, err := e.resolveTarget(client, target)
	if err != nil {
		return err
	}
	expr := fmt.Sprintf(`(function(n){
		if (!n || n.tagName !== 'SELECT') return 'not a select';
		var vals = %s;
		var found = 0;
		for (var i = 0; i < n.options.length; i++) {
			if (vals.indexOf(n.options[i].value) >= 0 || vals.indexOf(n.options[i].text) >= 0) {
				n.options[i].selected = true; found++;
			}
		}
		n.dispatchEvent(new Event('change', { bubbles: true }));
		return found + ' selected';
	})(document.querySelectorAll('*')[%d])`, jsonStringList(values), idx)
	raw, err := e.evalExpr(client, expr)
	if err != nil {
		return err
	}
	var s string
	json.Unmarshal(raw, &s)
	res.Output = s
	return nil
}

func (e *Engine) doCheck(args string, uncheck bool, res *commandResult) error {
	target := strings.TrimSpace(args)
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	idx, err := e.resolveTarget(client, target)
	if err != nil {
		return err
	}
	expr := fmt.Sprintf(`(function(n){ return n ? n.checked : undefined; })(document.querySelectorAll('*')[%d])`, idx)
	raw, err := e.evalExpr(client, expr)
	if err != nil {
		return err
	}
	var checked bool
	json.Unmarshal(raw, &checked)
	if checked != !uncheck {
		return e.clickNode(client, idx, clickFlagsParsed{button: "left"})
	}
	return nil
}

func (e *Engine) doScroll(args string, res *commandResult) error {
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	args = strings.TrimSpace(args)
	switch {
	case args == "top":
		_, err = e.evalExpr(client, "window.scrollTo(0, 0)")
		res.Output = "at the top"
	case args == "bottom":
		_, err = e.evalExpr(client, "window.scrollTo(0, document.body.scrollHeight)")
		res.Output = "at the bottom"
	default:
		dir := 1
		px := 500
		fields := strings.Fields(args)
		if len(fields) > 0 && fields[0] == "up" {
			dir = -1
		}
		if len(fields) > 1 {
			if p, err := strconv.Atoi(fields[1]); err == nil {
				px = p
			}
		}
		_, err = e.evalExpr(client, fmt.Sprintf("window.scrollBy(0, %d)", dir*px))
		res.Output = fmt.Sprintf("scrolled %dpx", dir*px)
	}
	return err
}

// doDrag drags from one point to another. Each point is "x,y" or a snapshot ref (e12)
// (centered). Example: drag e3 100,200 or drag 10,10 200,300.
func (e *Engine) doDrag(args string, res *commandResult) error {
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	fields := strings.Fields(args)
	if len(fields) < 2 {
		return fmt.Errorf("drag needs two points: <x,y|ref> <x,y|ref>")
	}
	start, err := e.dragPoint(client, fields[0])
	if err != nil {
		return err
	}
	end, err := e.dragPoint(client, fields[1])
	if err != nil {
		return err
	}
	steps := 20
	for i := 0; i <= steps; i++ {
		x := start.x + (end.x-start.x)*float64(i)/float64(steps)
		y := start.y + (end.y-start.y)*float64(i)/float64(steps)
		typ := "mouseMoved"
		if i == 0 {
			typ = "mousePressed"
		} else if i == steps {
			typ = "mouseReleased"
		}
		if _, err := client.Call("Input.dispatchMouseEvent", map[string]any{
			"type": typ, "x": x, "y": y, "button": "left", "clickCount": 1,
		}); err != nil {
			return err
		}
	}
	res.Output = fmt.Sprintf("dragged %g,%g → %g,%g", start.x, start.y, end.x, end.y)
	return nil
}

type dragPoint struct{ x, y float64 }

func (e *Engine) dragPoint(client *cdp.Client, s string) (dragPoint, error) {
	var p dragPoint
	if ref := refRe.FindString(s); ref != "" && strings.TrimSpace(s) == ref {
		idx, err := e.resolveTarget(client, s)
		if err != nil {
			return p, err
		}
		box, err := e.nodeBox(client, idx)
		if err != nil {
			return p, err
		}
		return dragPoint{x: box.X + box.Width/2, y: box.Y + box.Height/2}, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return p, fmt.Errorf("drag point %q must be x,y or a ref", s)
	}
	x, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	y, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return p, fmt.Errorf("drag point %q must be x,y or a ref", s)
	}
	return dragPoint{x: x, y: y}, nil
}

// --- Reading ---

func (e *Engine) doWait(args string, res *commandResult) error {
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	args = strings.TrimSpace(args)
	if ms, err := strconv.Atoi(args); err == nil && ms > 0 {
		if ms > 60000 {
			ms = 60000
		}
		_, err = client.Call("Runtime.evaluate", map[string]any{
			"expression":   fmt.Sprintf("new Promise(r => setTimeout(r, %d))", ms),
			"awaitPromise": true,
		})
		return err
	}
	if strings.HasPrefix(args, "--text") {
		text, _ := extractQuoted(args)
		return e.waitForCondition(client, fmt.Sprintf("document.body.innerText.includes(%s)", jsonString(text)))
	}
	if strings.HasPrefix(args, "--url") {
		url, _ := extractQuoted(args)
		if url == "" {
			url = strings.TrimSpace(strings.TrimPrefix(args, "--url"))
		}
		return e.waitForCondition(client, fmt.Sprintf("location.href.indexOf(%s) >= 0", jsonString(url)))
	}
	if strings.HasPrefix(args, "--selector") {
		sel, _ := extractQuoted(args)
		if sel == "" {
			sel = strings.TrimSpace(strings.TrimPrefix(args, "--selector"))
		}
		return e.waitForCondition(client, fmt.Sprintf("document.querySelector(%s) !== null", jsonString(sel)))
	}
	return fmt.Errorf("wait needs <ms>, --text \"...\", --url <url> or --selector \"<css>\"")
}

func (e *Engine) waitForCondition(client *cdp.Client, condition string) error {
	expr := fmt.Sprintf(`new Promise(function(resolve){
		var t0 = Date.now();
		function check(){
			try { if (%s) { resolve('ok'); return; } } catch(e) {}
			if (Date.now() - t0 > 10000) { resolve('timeout'); return; }
			setTimeout(check, 100);
		}
		check();
	})`, condition)
	raw, err := client.Call("Runtime.evaluate", map[string]any{
		"expression": expr, "awaitPromise": true, "returnByValue": true,
	})
	if err != nil {
		return err
	}
	var ev struct {
		Result struct {
			Value string
		}
	}
	json.Unmarshal(raw, &ev)
	if ev.Result.Value == "timeout" {
		return fmt.Errorf("wait timed out")
	}
	return nil
}

func (e *Engine) doSnapshot(args string, res *commandResult) error {
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	res.Output = e.domSnapshot(client)
	return nil
}

// domSnapshot builds a ref table from the DOM's interactive elements.
func (e *Engine) domSnapshot(client *cdp.Client) string {
	raw, err := e.evalExpr(client, `JSON.stringify(Array.from(document.querySelectorAll('a,button,input,select,textarea,summary,[role],[onclick],h1,h2,h3,label')).slice(0,200).map(function(el, i){
		return {
			"i": i,
			"tag": el.tagName.toLowerCase(),
			"text": (el.innerText || el.value || el.getAttribute('aria-label') || el.placeholder || '').trim().slice(0,80),
			"id": el.id || '',
			"cls": (el.className && typeof el.className === 'string') ? el.className.slice(0,60) : '',
			"role": el.getAttribute('role') || '',
			"href": el.href || '',
			"all": Array.prototype.indexOf.call(document.querySelectorAll('*'), el)
		};
	}))`)
	if err != nil {
		return "snapshot: " + err.Error()
	}
	// raw is the JSON array string (JSON.stringify'd); decode it
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return "snapshot: " + err.Error()
	}
	var items []struct {
		I    int    `json:"i"`
		Tag  string `json:"tag"`
		Text string `json:"text"`
		ID   string `json:"id"`
		Cls  string `json:"cls"`
		Role string `json:"role"`
		Href string `json:"href"`
		All  int    `json:"all"`
	}
	if err := json.Unmarshal([]byte(encoded), &items); err != nil {
		return "snapshot: " + err.Error()
	}
	e.refs = map[string]int{}
	var lines []string
	nextRef := 1
	for _, it := range items {
		role := it.Role
		if role == "" {
			role = it.Tag
		}
		ref := fmt.Sprintf("e%d", nextRef)
		nextRef++
		e.refs[ref] = it.All
		line := role
		if it.Text != "" {
			line += " " + it.Text
		}
		if it.Href != "" {
			line += " (" + truncate(it.Href, 50) + ")"
		}
		lines = append(lines, line+"  ["+ref+"]")
	}
	if len(lines) == 0 {
		return "(empty page)"
	}
	return strings.Join(lines, "\n")
}

func (e *Engine) doFind(args string, res *commandResult) error {
	text, _ := extractQuoted(args)
	if text == "" {
		text = strings.TrimSpace(args)
	}
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	snap := e.domSnapshot(client)
	var matches []string
	for _, line := range strings.Split(snap, "\n") {
		if strings.Contains(strings.ToLower(line), strings.ToLower(text)) {
			matches = append(matches, line)
		}
	}
	if len(matches) == 0 {
		res.Output = "no matches for " + text
		return nil
	}
	res.Output = strings.Join(matches, "\n")
	return nil
}

func (e *Engine) doGet(args string, res *commandResult) error {
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return fmt.Errorf("get needs text|value|count|url|title")
	}
	switch fields[0] {
	case "url":
		raw, err := e.evalExpr(client, "location.href")
		if err != nil {
			return err
		}
		var s string
		json.Unmarshal(raw, &s)
		res.Output = s
	case "title":
		raw, err := e.evalExpr(client, "document.title")
		if err != nil {
			return err
		}
		var s string
		json.Unmarshal(raw, &s)
		res.Output = s
	case "text":
		target := ""
		if len(fields) > 1 {
			target = strings.Join(fields[1:], " ")
		}
		expr := "document.body.innerText"
		if target != "" {
			expr = fmt.Sprintf("(function(){ var el = document.querySelector(%s); return el ? el.innerText : ''; })()", jsonString(e.cssFor(target)))
		}
		raw, err := e.evalExpr(client, expr)
		if err != nil {
			return err
		}
		var s string
		json.Unmarshal(raw, &s)
		res.Output = s
	case "value":
		if len(fields) < 2 {
			return fmt.Errorf("get value needs a target")
		}
		target := strings.Join(fields[1:], " ")
		expr := fmt.Sprintf("(function(){ var el = document.querySelector(%s); return el ? el.value : ''; })()", jsonString(e.cssFor(target)))
		raw, err := e.evalExpr(client, expr)
		if err != nil {
			return err
		}
		var s string
		json.Unmarshal(raw, &s)
		res.Output = s
	case "count":
		if len(fields) < 2 {
			return fmt.Errorf("get count needs a target")
		}
		target := strings.Join(fields[1:], " ")
		expr := fmt.Sprintf("document.querySelectorAll(%s).length", jsonString(e.cssFor(target)))
		raw, err := e.evalExpr(client, expr)
		if err != nil {
			return err
		}
		var n int
		json.Unmarshal(raw, &n)
		res.Output = strconv.Itoa(n)
	default:
		return fmt.Errorf("get: unknown subcommand %q", fields[0])
	}
	return nil
}

func (e *Engine) doScreenshot(args string, res *commandResult) error {
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	path := ""
	if p, ok := extractFlag(args, "--path"); ok {
		path = p
	}
	raw, err := client.Call("Page.captureScreenshot", map[string]any{"format": "jpeg", "quality": 80})
	if err != nil {
		return err
	}
	var data struct {
		Data string
	}
	json.Unmarshal(raw, &data)
	imgBytes, err := base64.StdEncoding.DecodeString(data.Data)
	if err != nil {
		return err
	}
	if path != "" {
		if err := os.WriteFile(path, imgBytes, 0o644); err != nil {
			return err
		}
		res.Output = "screenshot saved to " + path
		return nil
	}
	res.Output = asciiPreview(imgBytes, 64, 18)
	return nil
}

func (e *Engine) doEval(args string, res *commandResult) error {
	expr, _ := extractQuoted(args)
	if expr == "" {
		expr = strings.TrimSpace(args)
	}
	client, err := e.ensureClient()
	if err != nil {
		return err
	}
	raw, err := e.evalExpr(client, expr)
	if err != nil {
		return err
	}
	res.Output = string(raw)
	return nil
}

func (e *Engine) doTab(args string, res *commandResult) error {
	fields := strings.Fields(args)
	if len(fields) == 0 || fields[0] == "list" {
		state := e.conn.State()
		var lines []string
		for _, t := range state.Tabs {
			marker := " "
			if e.tabs[t.TabID] || t.TabID == e.current {
				marker = "*"
			}
			lines = append(lines, fmt.Sprintf("%s %d %s %s", marker, t.TabID, truncate(t.Title, 40), t.URL))
		}
		if len(lines) == 0 {
			lines = append(lines, "(no tabs)")
		}
		res.Output = strings.Join(lines, "\n")
		return nil
	}
	switch fields[0] {
	case "new":
		url := "about:blank"
		if len(fields) > 1 {
			url = normalizeURL(fields[1])
		}
		tabID, err := e.createTab(url)
		if err != nil {
			return err
		}
		e.current = tabID
		e.tabs[tabID] = true
		res.Output = fmt.Sprintf("tab %d", tabID)
	case "select":
		if len(fields) < 2 {
			return fmt.Errorf("tab select needs a tab id")
		}
		id, err := strconv.Atoi(fields[1])
		if err != nil {
			return err
		}
		e.current = id
		e.tabs[id] = true
	case "focus":
		if len(fields) < 2 {
			return fmt.Errorf("tab focus needs a tab id")
		}
		id, err := strconv.Atoi(fields[1])
		if err != nil {
			return err
		}
		if _, err := e.conn.Request("activateTab", protocol.ActivateTabParams{TabID: id}); err != nil {
			return err
		}
		e.current = id
		e.tabs[id] = true
		res.Output = fmt.Sprintf("tab %d focused", id)
	case "close":
		id := e.current
		if len(fields) > 1 {
			id, _ = strconv.Atoi(fields[1])
		}
		if _, err := e.conn.Request("closeTab", protocol.CloseTabParams{TabID: id}); err != nil {
			return err
		}
		delete(e.tabs, id)
		if e.current == id {
			e.current = 0
		}
	default:
		return fmt.Errorf("tab: unknown subcommand %q", fields[0])
	}
	return nil
}

// --- CDP helpers ---

func (e *Engine) cdpCall(method string, params any) (json.RawMessage, error) {
	client, err := e.ensureClient()
	if err != nil {
		return nil, err
	}
	return client.Call(method, params)
}

func (e *Engine) evalExpr(client *cdp.Client, expr string) (json.RawMessage, error) {
	raw, err := client.Call("Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
	})
	if err != nil {
		return nil, err
	}
	// Unwrap the CDP envelope: { result: { type, value } } -> the value itself
	var ev struct {
		Result struct {
			Value json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &ev); err == nil && len(ev.Result.Value) > 0 {
		return ev.Result.Value, nil
	}
	return raw, nil
}

// --- Target resolution ---

var refRe = regexp.MustCompile(`e\d+`)
var getByRoleRe = regexp.MustCompile(`getByRole\(\s*['"]([a-z-]+)['"]\s*(?:,\s*\{[^}]*name:\s*['"]([^'"]*)['"])?`)
var getByTextRe = regexp.MustCompile(`getByText\(\s*['"]([^'"]*)['"]`)

// resolveTarget turns a target (e# ref, getByRole/getByText, or CSS) into a DOM index.
func (e *Engine) resolveTarget(client *cdp.Client, target string) (int, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return 0, fmt.Errorf("empty target")
	}
	if ref := refRe.FindString(target); ref != "" {
		if idx, ok := e.refs[ref]; ok {
			return idx, nil
		}
		return 0, fmt.Errorf("unknown ref %s (run snapshot first)", ref)
	}
	if m := getByRoleRe.FindStringSubmatch(target); m != nil {
		return e.findByRole(client, m[1], m[2])
	}
	if m := getByTextRe.FindStringSubmatch(target); m != nil {
		return e.findByText(client, m[1])
	}
	return e.queryNode(client, target)
}

func (e *Engine) queryNode(client *cdp.Client, selector string) (int, error) {
	raw, err := e.evalExpr(client, fmt.Sprintf(`(function(){
		var el = document.querySelector(%s);
		if (!el) return null;
		return Array.prototype.indexOf.call(document.querySelectorAll('*'), el);
	})()`, jsonString(selector)))
	if err != nil {
		return 0, err
	}
	var idx *int
	json.Unmarshal(raw, &idx)
	if idx == nil {
		return 0, fmt.Errorf("element not found: %s", selector)
	}
	return *idx, nil
}

func (e *Engine) findByRole(client *cdp.Client, role, name string) (int, error) {
	expr := fmt.Sprintf(`(function(){
		var all = Array.from(document.querySelectorAll('[role],a,button,input,select,textarea,summary,h1,h2,h3,h4'));
		var el = all.find(function(n){
			var r = (n.getAttribute('role') || n.tagName.toLowerCase()).toLowerCase();
			if (r !== %s) return false;
			if (%s === 'true') {
				var t = (n.innerText || n.value || n.getAttribute('aria-label') || '').trim().toLowerCase();
				return t.includes(%s);
			}
			return true;
		});
		if (!el) return null;
		return Array.prototype.indexOf.call(document.querySelectorAll('*'), el);
	})()`, jsonString(role), fmt.Sprintf("%t", name != ""), jsonString(strings.ToLower(name)))
	raw, err := e.evalExpr(client, expr)
	if err != nil {
		return 0, err
	}
	var idx *int
	json.Unmarshal(raw, &idx)
	if idx == nil {
		return 0, fmt.Errorf("no element with role %s%s", role, nonEmptyName(name))
	}
	return *idx, nil
}

func (e *Engine) findByText(client *cdp.Client, text string) (int, error) {
	expr := fmt.Sprintf(`(function(){
		var needle = %s.toLowerCase();
		var all = Array.from(document.querySelectorAll('a,button,span,div,p,h1,h2,h3,h4,label,li,td,th,summary'));
		var el = all.find(function(n){
			var t = (n.innerText || '').trim().toLowerCase();
			return t.length > 0 && t.length < 200 && t.includes(needle);
		});
		if (!el) return null;
		return Array.prototype.indexOf.call(document.querySelectorAll('*'), el);
	})()`, jsonString(text))
	raw, err := e.evalExpr(client, expr)
	if err != nil {
		return 0, err
	}
	var idx *int
	json.Unmarshal(raw, &idx)
	if idx == nil {
		return 0, fmt.Errorf("no element with text %q", text)
	}
	return *idx, nil
}

// cssFor turns a target into a CSS selector for querySelector.
func (e *Engine) cssFor(target string) string {
	if ref := refRe.FindString(target); ref != "" {
		if idx, ok := e.refs[ref]; ok {
			return fmt.Sprintf("*:nth-child(%d)", idx+1)
		}
	}
	return target
}

func (e *Engine) nodeBox(client *cdp.Client, idx int) (box struct {
	X, Y, Width, Height float64
}, err error) {
	raw, err := e.evalExpr(client, fmt.Sprintf(`(function(){
		var el = document.querySelectorAll('*')[%d];
		if (!el) return null;
		var r = el.getBoundingClientRect();
		return { x: r.x, y: r.y, w: r.width, h: r.height };
	})()`, idx))
	if err != nil {
		return box, err
	}
	var b struct {
		X, Y, W, H float64
	}
	if json.Unmarshal(raw, &b) != nil {
		return box, fmt.Errorf("no box for element")
	}
	box.X, box.Y, box.Width, box.Height = b.X, b.Y, b.W, b.H
	return box, nil
}

// --- Parsing helpers ---

type clickFlagsParsed struct {
	target    string
	double    bool
	button    string
	modifiers []string
	mod       int
}

func parseClickFlags(args string) clickFlagsParsed {
	fields := strings.Fields(args)
	out := clickFlagsParsed{}
	var targetParts []string
	for _, f := range fields {
		switch {
		case f == "--double":
			out.double = true
		case strings.HasPrefix(f, "--button"):
			out.button = strings.TrimPrefix(f, "--button")
		case strings.HasPrefix(f, "--modifiers"):
			out.modifiers = strings.Split(strings.TrimPrefix(f, "--modifiers"), ",")
		default:
			targetParts = append(targetParts, f)
		}
	}
	out.target = strings.Join(targetParts, " ")
	for _, m := range out.modifiers {
		switch strings.ToLower(m) {
		case "shift":
			out.mod |= 1
		case "control":
			out.mod |= 2
		case "alt":
			out.mod |= 4
		case "meta":
			out.mod |= 8
		}
	}
	return out
}

func parseFill(args string) (target, text string, submit bool, err error) {
	submit = strings.Contains(args, "--submit")
	text, rest := extractQuoted(args)
	if text == "" {
		return "", "", false, fmt.Errorf("fill needs a quoted text")
	}
	target = strings.TrimSpace(strings.Replace(rest, "--submit", "", -1))
	if target == "" {
		return "", "", false, fmt.Errorf("fill needs a target")
	}
	return target, text, submit, nil
}

func parseType(args string) (text string, submit bool, err error) {
	submit = strings.Contains(args, "--submit")
	text, _ = extractQuoted(args)
	if text == "" {
		return "", false, fmt.Errorf("type needs a quoted text")
	}
	return text, submit, nil
}

func parseSelect(args string) (target string, values []string, err error) {
	var quoted []string
	rest := args
	for {
		q, r := extractQuoted(rest)
		if q == "" {
			break
		}
		quoted = append(quoted, q)
		rest = r
	}
	if len(quoted) == 0 {
		return "", nil, fmt.Errorf("select needs at least one quoted value")
	}
	values = quoted[1:]
	target = strings.TrimSpace(strings.TrimSuffix(args, quoted[0]))
	if target == "" {
		return "", nil, fmt.Errorf("select needs a target")
	}
	return target, values, nil
}

func extractQuoted(s string) (string, string) {
	start := strings.Index(s, "\"")
	if start < 0 {
		return "", s
	}
	end := strings.Index(s[start+1:], "\"")
	if end < 0 {
		return "", s
	}
	quoted := s[start+1 : start+1+end]
	return quoted, s[:start] + s[start+end+2:]
}

func extractFlag(s string, flag string) (string, bool) {
	if i := strings.Index(s, flag+"="); i >= 0 {
		rest := s[i+len(flag)+1:]
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			return fields[0], true
		}
	}
	if i := strings.Index(s, flag+" "); i >= 0 {
		fields := strings.Fields(s[i+len(flag):])
		if len(fields) > 0 {
			return fields[0], true
		}
	}
	return "", false
}

func normalizeURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "about:blank"
	}
	// Keep URLs that already carry a scheme (https://, http://, chrome://,
	// edge://, about:, file:, ...) untouched; only bare hostnames get a prefix.
	if i := strings.Index(s, ":"); i > 0 && strings.HasSuffix(s[:i+1], ":") {
		return s
	}
	if strings.HasPrefix(s, "localhost") || strings.HasPrefix(s, "127.") {
		return "http://" + s
	}
	return "https://" + s
}

func keyInfo(key string) (info struct {
	key, code string
	vk        int
}) {
	upper := strings.ToUpper(key)
	m := map[string]struct {
		code string
		vk   int
	}{
		"ENTER": {"Enter", 13}, "TAB": {"Tab", 9}, "ESCAPE": {"Escape", 27},
		"BACKSPACE": {"Backspace", 8}, "DELETE": {"Delete", 46},
		"ARROWUP": {"ArrowUp", 38}, "ARROWDOWN": {"ArrowDown", 40},
		"ARROWLEFT": {"ArrowLeft", 37}, "ARROWRIGHT": {"ArrowRight", 39},
		"HOME": {"Home", 36}, "END": {"End", 35}, "SPACE": {" ", 32},
	}
	if v, ok := m[upper]; ok {
		info = struct {
			key, code string
			vk        int
		}{key: strings.ToLower(key), code: v.code, vk: v.vk}
		return info
	}
	info = struct {
		key, code string
		vk        int
	}{key: key, code: upper, vk: 0}
	return info
}

func valuesJSON(values []string) string { return jsonStringList(values) }

func jsonStringList(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = jsonString(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func nonEmptyName(s string) string {
	if s == "" {
		return ""
	}
	return " named " + s
}

// --- Misc helpers ---

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// asciiPreview renders a JPEG to a small ASCII block so the agent gets a visual cue.
func asciiPreview(jpegData []byte, width, height int) string {
	img, err := jpeg.Decode(bytes.NewReader(jpegData))
	if err != nil {
		return "(screenshot captured)"
	}
	b := img.Bounds()
	const chars = " .:-=+*#%@"
	var lines []string
	for y := 0; y < height; y++ {
		var line strings.Builder
		for x := 0; x < width; x++ {
			pixelX := b.Min.X + x*(b.Max.X-b.Min.X)/width
			pixelY := b.Min.Y + y*(b.Max.Y-b.Min.Y)/height
			r, g, bl, _ := img.At(pixelX, pixelY).RGBA()
			lum := (int(r/257)*30 + int(g/257)*59 + int(bl/257)*11) / 100
			idx := (255 - lum) * len(chars) / 256
			if idx >= len(chars) {
				idx = len(chars) - 1
			}
			line.WriteByte(chars[idx])
		}
		lines = append(lines, line.String())
	}
	return "screenshot:\n" + strings.Join(lines, "\n")
}

var _ = image.Black

var helpText = `commands (one per line):
  goto <url> | back | forward | reload
  click <target> [--double] [--button left|right] [--modifiers Shift,Control]
  hover <target> | scroll top|bottom|<dir> [<px>] | drag <x,y|ref> <x,y|ref>
  fill <target> "<text>" [--submit] | type "<text>" | press <key>
  select <target> "<value>"... | check|uncheck <target>
  wait <ms> | wait --text "<t>" | wait --url <url> | wait --selector "<css>"
  snapshot | find "<text>"
  get text|value|count <target> | get url | get title
  screenshot [--path <file>] | eval "<js>"
  tab list|new [url]|select <id>|focus <id>|close [id]
  help [command]
targets: e12 (snapshot refs), getByRole('button', {name:'Go'}), getByText('...'), or CSS`
