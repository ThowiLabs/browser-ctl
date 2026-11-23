package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/thovilabs/browser-ctl/internal/cdp"
)

// Actions: click, hover, drag, scroll, fill, type, press, select,
// check/uncheck. Each resolves its target, checks actionability
// (visible, enabled, not covered), then uses CDP input with force.

func doClick(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	box, err := resolveBoxSess(ctx.Engine, client, ctx.Session, ctx.Args[0])
	if err != nil {
		return "", "", err
	}
	if err := checkActionable(ctx.Engine, client, ctx.Session, ctx.Args[0], box); err != nil {
		return "", "", err
	}
	_, dbl := ctx.Flags["double"]
	button := ctx.Flags["button"]
	if button == "" {
		button = "left"
	}
	clickCount := 1
	if dbl {
		clickCount = 2
	}
	before := currentURL(ctx.Engine, client)
	moveCursor(ctx.Session, client, box.CX, box.CY, true)
	// Brief pause on arrival, like a human hand settling before the press.
	time.Sleep(80 * time.Millisecond)
	if err := mouseClick(client, box.CX, box.CY, button, clickCount); err != nil {
		return "", "", err
	}
	settleAfterAction(client, before)
	return "", "", nil
}

func doHover(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	box, err := resolveBoxSess(ctx.Engine, client, ctx.Session, ctx.Args[0])
	if err != nil {
		return "", "", err
	}
	moveCursor(ctx.Session, client, box.CX, box.CY, true)
	return "", "", nil
}

func doDrag(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	from, err := resolveBoxSess(ctx.Engine, client, ctx.Session, ctx.Args[0])
	if err != nil {
		return "", "", err
	}
	to, err := resolveBoxSess(ctx.Engine, client, ctx.Session, ctx.Args[1])
	if err != nil {
		return "", "", err
	}
	// The moves must carry buttons:1 (left held): without it Chrome
	// sees the mouse moving with no button pressed and never starts
	// the HTML5 drag, so the drop has no effect.
	moveCursor(ctx.Session, client, from.CX, from.CY, true)
	if _, err := client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": from.CX, "y": from.CY, "button": "left", "buttons": 1, "clickCount": 1}); err != nil {
		return "", "", err
	}
	steps := 8
	for i := 1; i <= steps; i++ {
		x := from.CX + (to.CX-from.CX)*float64(i)/float64(steps)
		y := from.CY + (to.CY-from.CY)*float64(i)/float64(steps)
		if _, err := client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y, "button": "left", "buttons": 1}); err != nil {
			return "", "", err
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Glide the overlay to the drop point before releasing, like JS.
	moveCursor(ctx.Session, client, to.CX, to.CY, false)
	_, err = client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": to.CX, "y": to.CY, "button": "left", "buttons": 0, "clickCount": 1})
	return "", "", err
}

func doScroll(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	arg := ""
	if len(ctx.Args) > 0 {
		arg = ctx.Args[0]
	}
	var expr string
	switch arg {
	case "", "down":
		expr = "window.scrollBy(0, window.innerHeight*0.8)"
	case "up":
		expr = "window.scrollBy(0, -window.innerHeight*0.8)"
	case "top":
		expr = "window.scrollTo(0,0)"
	case "bottom":
		expr = "window.scrollTo(0, document.body.scrollHeight)"
	default:
		// <px> number or a target
		if n, err := strconv.ParseFloat(arg, 64); err == nil {
			expr = fmt.Sprintf("window.scrollBy(0, %v)", n)
		} else {
			box, err := resolveBoxSess(ctx.Engine, client, ctx.Session, arg)
			if err != nil {
				return "", "", err
			}
			expr = fmt.Sprintf("window.scrollTo(0, %v)", box.Y)
		}
	}
	if _, err := ctx.Engine.eval(client, expr); err != nil {
		return "", "", err
	}
	return "", "", nil
}

func doFill(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	expr, err := ResolveTargetSess(ctx.Engine, client, ctx.Session, ctx.Args[0])
	if err != nil {
		return "", "", err
	}
	text := ctx.Args[1]
	setExpr := fmt.Sprintf("(function(){var el=%s; if(!el) throw new Error('no element'); el.focus(); el.value=%s; el.dispatchEvent(new Event('input',{bubbles:true})); el.dispatchEvent(new Event('change',{bubbles:true})); return true;})()", expr, jsonString(text))
	if _, err := ctx.Engine.eval(client, setExpr); err != nil {
		return "", "", err
	}
	if _, ok := ctx.Flags["submit"]; ok {
		return doPress(&Context{Engine: ctx.Engine, Session: ctx.Session, Args: []string{"Enter"}, Flags: map[string]string{}})
	}
	return "", "", nil
}

func doType(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	text := ctx.Args[0]
	_, slowly := ctx.Flags["slowly"]
	_, paste := ctx.Flags["paste"]
	if paste || len(text) > 200 {
		_, err := client.Call("Input.insertText", map[string]any{"text": text})
		if err != nil {
			return "", "", err
		}
	} else if slowly {
		for _, r := range text {
			if _, err := client.Call("Input.insertText", map[string]any{"text": string(r)}); err != nil {
				return "", "", err
			}
			time.Sleep(30 * time.Millisecond)
		}
	} else {
		_, err := client.Call("Input.insertText", map[string]any{"text": text})
		if err != nil {
			return "", "", err
		}
	}
	if _, ok := ctx.Flags["submit"]; ok {
		return doPress(&Context{Engine: ctx.Engine, Session: ctx.Session, Args: []string{"Enter"}, Flags: map[string]string{}})
	}
	return "", "", nil
}

func doPress(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	key := ctx.Args[0]
	// Split modifiers: Control+a, Shift+Tab, etc.
	parts := strings.Split(key, "+")
	code := parts[len(parts)-1]
	modifiers := 0
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "control", "ctrl":
			modifiers |= 2
		case "shift":
			modifiers |= 8
		case "alt":
			modifiers |= 1
		case "meta", "command":
			modifiers |= 4
		}
	}
	params := map[string]any{"type": "keyDown", "key": code, "code": code, "modifiers": modifiers}
	if _, err := client.Call("Input.dispatchKeyEvent", params); err != nil {
		return "", "", err
	}
	params["type"] = "keyUp"
	_, err = client.Call("Input.dispatchKeyEvent", params)
	return "", "", err
}

func doSelect(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	expr, err := ResolveTargetSess(ctx.Engine, client, ctx.Session, ctx.Args[0])
	if err != nil {
		return "", "", err
	}
	values := ctx.Args[1:]
	valsJSON, _ := json.Marshal(values)
	setExpr := fmt.Sprintf("(function(){var el=%s; if(!el) throw new Error('no element'); var vals=%s; var found=0; for(var i=0;i<el.options.length;i++){ if(vals.indexOf(el.options[i].value)>=0 || vals.indexOf(el.options[i].text)>=0){ el.options[i].selected=true; found++; } } if(!found) throw new Error('no option matched'); el.dispatchEvent(new Event('change',{bubbles:true})); return found;})()", expr, string(valsJSON))
	_, err = ctx.Engine.eval(client, setExpr)
	return "", "", err
}

func doCheck(ctx *Context, uncheck bool) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	expr, err := ResolveTargetSess(ctx.Engine, client, ctx.Session, ctx.Args[0])
	if err != nil {
		return "", "", err
	}
	want := "true"
	if uncheck {
		want = "false"
	}
	setExpr := fmt.Sprintf("(function(){var el=%s; if(!el) throw new Error('no element'); if(el.checked===%s) return 'already'; el.click(); return el.checked===%s ? 'ok' : 'failed';})()", expr, want, want)
	raw, err := ctx.Engine.eval(client, setExpr)
	if err != nil {
		return "", "", err
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	if s == "already" {
		return "", "already " + map[bool]string{true: "unchecked", false: "checked"}[uncheck], nil
	}
	if s != "ok" {
		return "", "", fmt.Errorf("could not change checkbox state")
	}
	return "", "", nil
}

// --- helpers ---

type box struct {
	X, Y, W, H float64
	CX, CY     float64
}

// resolveBox resolves a target to its on-screen box (centre included).
func resolveBox(e *Engine, client *cdp.Client, target string) (box, error) {
	return resolveBoxSess(e, client, nil, target)
}

func resolveBoxSess(e *Engine, client *cdp.Client, sess *Session, target string) (box, error) {
	// Snapshot refs resolve via the last snapshot's ref table.
	if refRe.MatchString(target) {
		return resolveRefBox(e, client, sess, target)
	}
	// Locators: primero el árbol AX del último snapshot (preciso, con
	// el id de nodo DOM); sin coincidencia, la traducción JS.
	if sess != nil {
		if ref, ok := locatorAXRef(sess, target); ok {
			return resolveRefBox(e, client, sess, ref)
		}
	}
	expr, err := ResolveTarget(e, client, target)
	if err != nil {
		return box{}, err
	}
	return queryBox(e, client, expr)
}

func queryBox(e *Engine, client *cdp.Client, expr string) (box, error) {
	js := fmt.Sprintf("(function(){var el=%s; if(!el) return null; var r=el.getBoundingClientRect(); return {x:r.x,y:r.y,w:r.width,h:r.height};})()", expr)
	raw, err := e.eval(client, js)
	if err != nil {
		return box{}, err
	}
	var b struct {
		X, Y, W, H float64
	}
	if string(raw) == "null" {
		return box{}, fmt.Errorf("no element matched %q", expr)
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return box{}, err
	}
	return box{X: b.X, Y: b.Y, W: b.W, H: b.H, CX: b.X + b.W/2, CY: b.Y + b.H/2}, nil
}

// resolveRefBox looks up a snapshot ref in the session's last snapshot.
// Precise path: backend DOM node id -> DOM.resolveNode -> DOM.getBoxModel.
// Fallback: the stored JS expression.
func resolveRefBox(e *Engine, client *cdp.Client, sess *Session, ref string) (box, error) {
	if sess == nil {
		return box{}, fmt.Errorf("snapshot refs need a session (run `snapshot` first)")
	}
	sess.mu.Lock()
	backendID, hasNode := sess.LastRefNodes[ref]
	expr, ok := sess.LastRefs[ref]
	sess.mu.Unlock()
	if !ok {
		return box{}, fmt.Errorf("ref %s isn't on the page anymore. Run `snapshot -i` and use a fresh ref", ref)
	}
	if hasNode && backendID != 0 {
		if b, err := boxByBackendNode(client, backendID); err == nil {
			return b, nil
		}
	}
	return queryBox(e, client, expr)
}

// boxByBackendNode resolves a backend DOM node id to its on-screen box.
func boxByBackendNode(client *cdp.Client, backendNodeID int) (box, error) {
	raw, err := client.Call("DOM.resolveNode", map[string]any{"backendNodeId": backendNodeID})
	if err != nil {
		return box{}, err
	}
	var res struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Object.ObjectID == "" {
		return box{}, fmt.Errorf("could not resolve node %d", backendNodeID)
	}
	raw, err = client.Call("DOM.getBoxModel", map[string]any{"objectId": res.Object.ObjectID})
	if err != nil {
		return box{}, err
	}
	var model struct {
		Model struct {
			Content []float64 `json:"content"`
		} `json:"model"`
	}
	if err := json.Unmarshal(raw, &model); err != nil {
		return box{}, err
	}
	pts := model.Model.Content
	if len(pts) < 8 {
		return box{}, fmt.Errorf("no box model for node %d", backendNodeID)
	}
	// content quad: x1,y1 x2,y2 x3,y3 x4,y4
	minX, minY := pts[0], pts[1]
	maxX, maxY := pts[0], pts[1]
	for i := 0; i < 8; i += 2 {
		if pts[i] < minX {
			minX = pts[i]
		}
		if pts[i] > maxX {
			maxX = pts[i]
		}
		if pts[i+1] < minY {
			minY = pts[i+1]
		}
		if pts[i+1] > maxY {
			maxY = pts[i+1]
		}
	}
	return box{X: minX, Y: minY, W: maxX - minX, H: maxY - minY, CX: (minX + maxX) / 2, CY: (minY + maxY) / 2}, nil
}

// checkActionable mirrors JS: visible, enabled, not covered by another
// element at its centre (elementFromPoint). The hit-test compares the
// element at the click point with the target itself: if another element
// covers it, the click would land elsewhere, so it fails loudly instead
// of succeeding silently (contexto/04, hallazgo 4).
func checkActionable(e *Engine, client *cdp.Client, sess *Session, target string, b box) error {
	if b.W == 0 || b.H == 0 {
		return fmt.Errorf("element is not visible (zero size)")
	}
	// Frame refs skip the hit-test: the point is in the main viewport
	// but the element lives in the frame's own coordinates.
	// deuda-tecnica: hit-test en iframes; hacerlo requeriría evaluar en
	// el contexto del frame. Disparador: un click en iframe que falle en
	// silencio por estar cubierto.
	if refRe.MatchString(target) && strings.Contains(target, "f") {
		return nil
	}
	// Precise path for snapshot refs: backend DOM node id.
	if refRe.MatchString(target) && sess != nil {
		sess.mu.Lock()
		backendID, ok := sess.LastRefNodes[target]
		sess.mu.Unlock()
		if ok && backendID != 0 {
			return hitTestNode(client, backendID, b)
		}
	}
	expr, err := ResolveTargetSess(e, client, sess, target)
	if err != nil {
		return err
	}
	raw, err := e.eval(client, fmt.Sprintf("(function(){var el=%s; if(!el) return 'noelement'; var at=document.elementFromPoint(%v,%v); if(!at) return 'none'; if(at===el||el.contains(at)) return 'ok'; return 'covered';})()", expr, b.CX, b.CY))
	if err != nil {
		return err
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return hitTestResult(s)
}

// hitTestNode runs the hit-test on a DOM node resolved by backend id
// (Runtime.callFunctionOn with this = the element).
func hitTestNode(client *cdp.Client, backendNodeID int, b box) error {
	raw, err := client.Call("DOM.resolveNode", map[string]any{"backendNodeId": backendNodeID})
	if err != nil {
		return err
	}
	var res struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Object.ObjectID == "" {
		return fmt.Errorf("could not resolve node %d", backendNodeID)
	}
	fn := fmt.Sprintf("function(){var at=document.elementFromPoint(%v,%v); if(!at) return 'none'; if(this===at||this.contains(at)) return 'ok'; return 'covered';}", b.CX, b.CY)
	raw, err = client.Call("Runtime.callFunctionOn", map[string]any{
		"objectId": res.Object.ObjectID, "functionDeclaration": fn, "returnByValue": true,
	})
	if err != nil {
		return err
	}
	var out struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	return hitTestResult(out.Result.Value)
}

func hitTestResult(s string) error {
	switch s {
	case "ok":
		return nil
	case "covered":
		return fmt.Errorf("element is covered by another element at its centre (the click would hit a different element)")
	case "none":
		return fmt.Errorf("no element at the click point")
	case "noelement":
		return fmt.Errorf("no element matched the target")
	default:
		return fmt.Errorf("element is not actionable (%s at its centre)", s)
	}
}

// currentURL reads location.href, best-effort.
func currentURL(e *Engine, client *cdp.Client) string {
	raw, err := e.eval(client, "location.href")
	if err != nil {
		return ""
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// settleAfterAction mirrors JS withSettle: if a navigation started within
// 500 ms of the action, wait for commit + load (up to 10 s).
func settleAfterAction(client *cdp.Client, before string) {
	if before == "" {
		return
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		raw, err := client.Call("Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true})
		if err == nil {
			var res struct {
				Result struct {
					Value string `json:"value"`
				} `json:"result"`
			}
			_ = json.Unmarshal(raw, &res)
			if res.Result.Value != before {
				_ = waitForLoad(client, 10*time.Second)
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// moveCursor glides the on-page cursor overlay to the point the way a
// human hand would: a gently curved path (quadratic bezier, pushed a
// little to one side) with ease-in-out timing — slow start, fast middle,
// slow landing — instead of a straight-line teleport. The real mouse
// follows the same path with mouseMoved events when emit is true, so
// hover effects trigger along the way; drag passes false for the final
// glide because the real mouse already travelled there in steps.
// The overlay is the JS original's: a big blue SVG arrow with glow and
// a pill label, living in a closed shadow root so it never leaks into
// innerText or snapshots. The last position is kept on the session, so
// the next move starts where the hand was left.
func moveCursor(sess *Session, client *cdp.Client, x, y float64, emit bool) {
	sess.mu.Lock()
	sx, sy, known := sess.CursorX, sess.CursorY, sess.CursorKnown
	sess.mu.Unlock()
	setCursorPos(client, x, y)
	if !known {
		// No start point yet, so no glide — but the real mouse must
		// still be told where it is, or :hover never triggers.
		if emit {
			_, _ = client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y, "button": "none"})
		}
		sess.mu.Lock()
		sess.CursorX, sess.CursorY, sess.CursorKnown = x, y, true
		sess.mu.Unlock()
		return
	}
	dx, dy := x-sx, y-sy
	dist := math.Hypot(dx, dy)
	if dist < 2 {
		if emit {
			_, _ = client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y, "button": "none"})
		}
		sess.mu.Lock()
		sess.CursorX, sess.CursorY = x, y
		sess.mu.Unlock()
		return
	}
	// Control point: midpoint pushed sideways 8% of the distance.
	mx, my := (sx+x)/2, (sy+y)/2
	px, py := -dy/dist, dx/dist
	off := 0.08 * dist
	cx, cy := mx+px*off, my+py*off
	steps := int(dist / 40)
	if steps < 8 {
		steps = 8
	}
	if steps > 24 {
		steps = 24
	}
	dur := 200 + dist*0.6
	if dur < 250 {
		dur = 250
	}
	if dur > 700 {
		dur = 700
	}
	pause := time.Duration(dur/float64(steps)) * time.Millisecond
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		e := easeInOutCubic(t)
		bx := (1-e)*(1-e)*sx + 2*(1-e)*e*cx + e*e*x
		by := (1-e)*(1-e)*sy + 2*(1-e)*e*cy + e*e*y
		setCursorPos(client, bx, by)
		if emit {
			_, _ = client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": bx, "y": by, "button": "none"})
		}
		time.Sleep(pause)
	}
	setCursorPos(client, x, y)
	if emit {
		_, _ = client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y, "button": "none"})
	}
	sess.mu.Lock()
	sess.CursorX, sess.CursorY = x, y
	sess.mu.Unlock()
}

func easeInOutCubic(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	return 1 - math.Pow(-2*t+2, 3)/2
}

// setCursorPos creates the cursor overlay once and moves it to (x, y).
// Same look as the JS original: blue SVG arrow with glow + pill label,
// in a closed shadow root on <html>, aria-hidden and pointer-events none.
func setCursorPos(client *cdp.Client, x, y float64) {
	js := fmt.Sprintf(`(function(){var id='browser-ctl-cursor';var host=document.getElementById(id);if(!host){host=document.createElement('div');host.id=id;host.setAttribute('aria-hidden','true');host.style.cssText='position:fixed;left:0;top:0;width:0;height:0;z-index:2147483647;pointer-events:none;';host.attachShadow({mode:'closed'}).innerHTML='<style>.arrow{position:absolute;left:-3px;top:-3px;filter:drop-shadow(0 2px 3px rgba(0,0,0,.45)) drop-shadow(0 0 6px rgba(37,99,235,.9));}.label{position:absolute;left:22px;top:26px;padding:3px 9px;border-radius:999px;background:#2563eb;color:#fff;font:600 12px/16px system-ui,-apple-system,sans-serif;white-space:nowrap;border:1.5px solid #fff;box-shadow:0 2px 6px rgba(0,0,0,.35);}</style><svg class="arrow" width="30" height="36" viewBox="0 0 20 24"><path d="M1 1 L1 19 L6 14.5 L9.5 22 L12.5 20.7 L9 13.3 L15.5 13 Z" fill="#2563eb" stroke="#fff" stroke-width="1.6" stroke-linejoin="round"/></svg><div class="label">browser-ctl</div>';document.documentElement.appendChild(host);}host.style.transform='translate(%vpx, %vpx)';})()`, x, y)
	_, _ = client.Call("Runtime.evaluate", map[string]any{"expression": js})
}

func mouseClick(client *cdp.Client, x, y float64, button string, clickCount int) error {
	if _, err := client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": x, "y": y, "button": button, "clickCount": clickCount}); err != nil {
		return err
	}
	_, err := client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": x, "y": y, "button": button, "clickCount": clickCount})
	return err
}
