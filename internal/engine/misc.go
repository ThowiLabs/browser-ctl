package engine

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/thovilabs/browser-ctl/internal/cdp"
)

// doDialog answers an open dialog, then resumes the interrupted step
// (JS dialog command: the step finishes within this call, which holds
// the tab).
func doDialog(ctx *Context) (string, string, error) {
	client, ts, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	if len(ctx.Args) == 0 || (ctx.Args[0] != "accept" && ctx.Args[0] != "dismiss") {
		return "", "", fmt.Errorf("dialog takes accept or dismiss")
	}
	captureMu.Lock()
	dialog := ts.Dialog
	captureMu.Unlock()
	if dialog == nil {
		return "", "", fmt.Errorf("No dialog is open")
	}
	accept := ctx.Args[0] == "accept"
	params := map[string]any{"accept": accept}
	if len(ctx.Args) > 1 {
		params["promptText"] = ctx.Args[1]
	}
	if _, err := client.Call("Page.handleJavaScriptDialog", params); err != nil {
		return "", "", err
	}
	// The Closed event clears ts.Dialog too; clear it here so a missed
	// event can't leave a stale dialog behind.
	captureMu.Lock()
	ts.Dialog = nil
	captureMu.Unlock()
	// The step the dialog interrupted goes on now: it finishes within
	// this call, which holds the tab (JS).
	ctx.Engine.finishAbandoned(ts)
	return "", "", nil
}

// doUpload uploads files into a file input via DOM.setFileInputFiles.
// Files are read locally first (same semantics as the JS thin-process
// readFile), then set on the input's DOM node.
func doUpload(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	if len(ctx.Args) < 2 {
		return "", "", fmt.Errorf("upload needs <target> <path>...")
	}
	target := ctx.Args[0]
	var files []string
	for _, p := range ctx.Args[1:] {
		abs := expandPath(p)
		if _, err := os.Stat(abs); err != nil {
			return "", "", fmt.Errorf("upload: cannot read %s: %w", p, err)
		}
		files = append(files, abs)
	}
	nodeID, err := nodeIDForTarget(ctx.Engine, client, ctx.Session, target)
	if err != nil {
		return "", "", err
	}
	// Verify it is a file input.
	raw, err := client.Call("DOM.describeNode", map[string]any{"nodeId": nodeID})
	if err != nil {
		return "", "", err
	}
	var desc struct {
		Node struct {
			NodeName   string   `json:"nodeName"`
			Attributes []string `json:"attributes"`
		} `json:"node"`
	}
	_ = json.Unmarshal(raw, &desc)
	if desc.Node.NodeName != "INPUT" {
		return "", "", fmt.Errorf("upload target is not an input element")
	}
	isFile := false
	for i := 0; i+1 < len(desc.Node.Attributes); i += 2 {
		if desc.Node.Attributes[i] == "type" && desc.Node.Attributes[i+1] == "file" {
			isFile = true
		}
	}
	if !isFile {
		// Not a direct file input: click it and let the page open a file
		// chooser is not possible over CDP here; report clearly.
		return "", "", fmt.Errorf("upload target is not a file input (type=file)")
	}
	if _, err := client.Call("DOM.setFileInputFiles", map[string]any{"files": files, "nodeId": nodeID}); err != nil {
		return "", "", err
	}
	return fmt.Sprintf("uploaded %d file(s)", len(files)), "", nil
}

// nodeIDForTarget resolves a target to a DOM nodeId for DOM.* commands.
// Locators resolve through the AX snapshot to a ref first.
func nodeIDForTarget(e *Engine, client *cdp.Client, sess *Session, target string) (int, error) {
	if ref, ok := locatorAXRef(sess, target); ok {
		target = ref
	}
	if refRe.MatchString(target) {
		sess.mu.Lock()
		backendID, ok := sess.LastRefNodes[target]
		sess.mu.Unlock()
		if !ok || backendID == 0 {
			return 0, fmt.Errorf("ref %s has no DOM node id. Run `snapshot` again", target)
		}
		raw, err := client.Call("DOM.resolveNode", map[string]any{"backendNodeId": backendID})
		if err != nil {
			return 0, err
		}
		var res struct {
			Object struct {
				ObjectID string `json:"objectId"`
			} `json:"object"`
		}
		_ = json.Unmarshal(raw, &res)
		raw, err = client.Call("DOM.describeNode", map[string]any{"objectId": res.Object.ObjectID})
		if err != nil {
			return 0, err
		}
		var desc struct {
			Node struct {
				NodeID int `json:"nodeId"`
			} `json:"node"`
		}
		_ = json.Unmarshal(raw, &desc)
		return desc.Node.NodeID, nil
	}
	t, err := ParseTarget(target)
	if err != nil {
		return 0, err
	}
	if t.Kind != TargetCSS {
		return 0, fmt.Errorf("upload needs a CSS selector or snapshot ref target")
	}
	docRaw, err := client.Call("DOM.getDocument", map[string]any{"depth": -1, "pierce": true})
	if err != nil {
		return 0, err
	}
	var doc struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	_ = json.Unmarshal(docRaw, &doc)
	raw, err := client.Call("DOM.querySelector", map[string]any{"nodeId": doc.Root.NodeID, "selector": t.Selector})
	if err != nil {
		return 0, err
	}
	var res struct {
		NodeID int `json:"nodeId"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.NodeID == 0 {
		return 0, fmt.Errorf("no element matched %q", target)
	}
	return res.NodeID, nil
}

// doPDF saves the page as PDF via Page.printToPDF.
func doPDF(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	path := ctx.Flags["path"]
	if path == "" && len(ctx.Args) > 0 {
		path = ctx.Args[0]
	}
	_, landscape := ctx.Flags["landscape"]
	params := map[string]any{"printBackground": true, "preferCSSPageSize": true, "landscape": landscape}
	raw, err := client.Call("Page.printToPDF", params)
	if err != nil {
		return "", "", err
	}
	var data struct {
		Data string `json:"data"`
	}
	_ = json.Unmarshal(raw, &data)
	pdf, err := base64Decode(data.Data)
	if err != nil {
		return "", "", err
	}
	if path == "" {
		titleRaw, _ := ctx.Engine.eval(client, "document.title")
		var title string
		_ = json.Unmarshal(titleRaw, &title)
		if title == "" {
			title = "page"
		}
		path = safeFileName(title) + ".pdf"
	}
	path, err = saveExclusive(path, pdf)
	if err != nil {
		return "", "", err
	}
	pages := strings.Count(string(pdf), "/Type /Page")
	return fmt.Sprintf("saved %s (%d pages, %s)", path, pages, formatBytes(len(pdf))), "", nil
}

// doDownload fetches a file inside the page (page cookies/CORS) and saves it.
func doDownload(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	arg := ctx.Args[0]
	url := arg
	if !isURLArgument(arg) {
		// A target: read its href/src.
		tExpr, err := ResolveTargetSess(ctx.Engine, client, ctx.Session, arg)
		if err != nil {
			return "", "", err
		}
		raw, err := ctx.Engine.eval(client, fmt.Sprintf("(function(){var el=%s; if(!el) return ''; return el.href||el.src||el.currentSrc||'';})()", tExpr))
		if err != nil {
			return "", "", err
		}
		_ = json.Unmarshal(raw, &url)
		if url == "" {
			return "", "", fmt.Errorf("target has no href/src to download")
		}
	}
	// Fetch inside the page so it gets the page's cookies.
	raw, err := ctx.Engine.eval(client, fmt.Sprintf("(async function(){var r=await fetch(%s); if(!r.ok) throw new Error('HTTP '+r.status); var b=await r.arrayBuffer(); var u8=new Uint8Array(b); var s=''; for(var i=0;i<u8.length;i+=8192){ s+=String.fromCharCode.apply(null,u8.subarray(i,i+8192)); } return btoa(s);})()", jsonString(url)))
	if err != nil {
		return "", "", err
	}
	var b64 string
	_ = json.Unmarshal(raw, &b64)
	data, err := base64Decode(b64)
	if err != nil {
		return "", "", err
	}
	if len(data) > 25*1024*1024 {
		return "", "", fmt.Errorf("download exceeds the 25 MB limit")
	}
	name := filepath.Base(url)
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	if name == "" || name == "/" {
		name = "download"
	}
	path := ctx.Flags["path"]
	if path == "" {
		path = safeFileName(name)
	}
	path, err = saveExclusive(path, data)
	if err != nil {
		return "", "", err
	}
	return fmt.Sprintf("saved %s (%s)", path, formatBytes(len(data))), "", nil
}

// doCDP is raw CDP access (needs --caps cdp).
func doCDP(ctx *Context) (string, string, error) {
	if !ctx.Session.Capabilities["cdp"] {
		return "", "", fmt.Errorf("cdp needs --caps cdp")
	}
	client, ts, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	if len(ctx.Args) == 0 {
		return "", "", fmt.Errorf("cdp needs a method or subcommand")
	}
	switch ctx.Args[0] {
	case "on":
		// cdp on <event>... — subscribe; captureEvent buffers matching
		// events into ts.CDPEventLog.
		for _, ev := range ctx.Args[1:] {
			found := false
			for _, existing := range ts.CDPEvents {
				if existing == ev {
					found = true
				}
			}
			if !found {
				ts.CDPEvents = append(ts.CDPEvents, ev)
			}
		}
		return "", "", nil
	case "events":
		// Print + clear the buffered events.
		out := strings.Join(ts.CDPEventLog, "\n")
		ts.CDPEventLog = nil
		if out == "" {
			return "(no events)", "", nil
		}
		return out, "", nil
	case "wait":
		// cdp wait <event> [--timeout ms] — poll the buffered log.
		if len(ctx.Args) < 2 {
			return "", "", fmt.Errorf("cdp wait needs an event name")
		}
		want := ctx.Args[1]
		timeout := 10 * time.Second
		if t, ok := ctx.Flags["timeout"]; ok {
			if ms, err := strconv.Atoi(t); err == nil {
				timeout = time.Duration(ms) * time.Millisecond
			}
		}
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			captureMu.Lock()
			for _, line := range ts.CDPEventLog {
				if strings.HasPrefix(line, want+" ") || line == want {
					captureMu.Unlock()
					return line, "", nil
				}
			}
			captureMu.Unlock()
			time.Sleep(100 * time.Millisecond)
		}
		return "", "", fmt.Errorf("timed out waiting for CDP event %s", want)
	}
	method := ctx.Args[0]
	if !cdpMethodRe.MatchString(method) {
		return "", "", fmt.Errorf("invalid CDP method %q", method)
	}
	if deniedCDPMethod(method) {
		return "", "", fmt.Errorf("browser-ctl doesn't allow %s: it reaches other sites or tabs", method)
	}
	var params any
	if len(ctx.Args) > 1 {
		if err := json.Unmarshal([]byte(ctx.Args[1]), &params); err != nil {
			return "", "", fmt.Errorf("bad CDP params JSON: %w", err)
		}
	}
	raw, err := client.Call(method, params)
	if err != nil {
		return "", "", err
	}
	return string(raw), "", nil
}

// --- helpers (shared) ---

func base64Decode(s string) ([]byte, error) {
	return base64StdDecode(s)
}

func isURLArgument(text string) bool {
	return strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") ||
		strings.HasPrefix(text, "blob:") || strings.HasPrefix(text, "data:") ||
		strings.HasPrefix(text, "/") || strings.HasPrefix(text, "./") || strings.HasPrefix(text, "../")
}

// safeFileName strips directories/control chars and caps length, like JS.
func safeFileName(name string) string {
	name = filepath.Base(name)
	var sb strings.Builder
	for _, r := range name {
		if r < 32 || strings.ContainsRune(`<>:"|?*`, r) {
			continue
		}
		sb.WriteRune(r)
	}
	name = strings.Trim(sb.String(), " .")
	if len(name) > 120 {
		name = name[:120]
	}
	if name == "" {
		name = "download"
	}
	// Windows-reserved device names.
	lower := strings.ToLower(name)
	for _, reserved := range []string{"con", "prn", "aux", "nul"} {
		if lower == reserved || strings.HasPrefix(lower, reserved+".") {
			name = "_" + name
		}
	}
	return name
}

// saveExclusive writes data, adding (1), (2)... like a browser download,
// using exclusive create so two sessions can't overwrite each other.
func saveExclusive(path string, data []byte) (string, error) {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", err
		}
		return path, nil
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; i <= 100; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		f, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, werr := f.Write(data)
			f.Close()
			if werr != nil {
				return "", werr
			}
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find a free filename for %s", path)
}

func formatBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// --- small helpers used by misc.go ---

func timeAfter(d time.Duration) <-chan time.Time { return time.After(d) }

func base64StdDecode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

var cdpMethodRe = regexp.MustCompile(`^[A-Z]\w*\.\w+$`)

// deniedCDPMethod mirrors the JS cdp-policy: cookies, Target.*, storage
// domains, Network.loadNetworkResource.
func deniedCDPMethod(method string) bool {
	if strings.HasPrefix(method, "Storage.") || strings.HasPrefix(method, "IndexedDB.") ||
		strings.HasPrefix(method, "CacheStorage.") || strings.HasPrefix(method, "DOMStorage.") {
		return true
	}
	denied := map[string]bool{
		"Network.getAllCookies": true, "Network.getCookies": true,
		"Network.setCookie": true, "Network.setCookies": true,
		"Network.deleteCookies": true, "Network.clearBrowserCookies": true,
		"Network.clearBrowserCache": true, "Network.loadNetworkResource": true,
		"Target.attachToTarget": true, "Target.attachToBrowserTarget": true,
		"Target.activateTarget": true, "Target.closeTarget": true,
		"Target.createTarget": true, "Target.createBrowserContext": true,
		"Target.disposeBrowserContext": true, "Target.exposeDevToolsProtocol": true,
		"Target.getTargets": true, "Target.sendMessageToTarget": true,
		"Target.setDiscoverTargets": true, "Target.setRemoteLocations": true,
	}
	return denied[method]
}
