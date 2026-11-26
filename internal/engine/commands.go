package engine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thovilabs/browser-ctl/internal/cdp"
)

// CommandDef describes one command, mirroring the JS CommandDef.
type CommandDef struct {
	Name        string
	Usage       string
	Help        string
	Flags       FlagSpec
	MaxArgs     int // -1 = unlimited
	Navigates   bool
	Reads       bool
	NoPage      bool
	WhileDialog bool
	Timeout     time.Duration // 0 = default
	Capability  string        // e.g. "cdp"
	Run         func(ctx *Context) (output, note string, err error)
}

// CommandsByName is the registry.
var CommandsByName = map[string]*CommandDef{}

func reg(def *CommandDef) *CommandDef {
	CommandsByName[def.Name] = def
	return def
}

// CheckLine pre-validates one parsed line (unknown command, capability,
// arg count, flag errors) without running anything.
func CheckLine(ln CommandLine) error {
	def, ok := CommandsByName[ln.Name]
	if !ok {
		return fmt.Errorf("unknown command %q (run `help`)", ln.Name)
	}
	args, _, err := ParseArgs(ln.Args, def.Flags)
	if err != nil {
		return err
	}
	args = mergeTargetArgs(ln.Name, args)
	if def.MaxArgs >= 0 && len(args) > def.MaxArgs {
		return fmt.Errorf("%s takes at most %d argument(s)", ln.Name, def.MaxArgs)
	}
	return nil
}

// mergeTargetArgs junta un target locator de varias palabras en un solo
// argumento: el parser divide por espacios, pero `click getByRole button
// "Nombre"` es un único target. Solo se junta cuando el primer token es
// un método locator (así `fill e12 hello world` sigue fallando por
// MaxArgs como antes, en vez de rellenar mal). drag y upload quedan
// fuera: dos targets o rutas no se pueden separar sin ambigüedad.
// deuda-tecnica: los targets en flags (--gone, --in) tampoco se juntan.
func mergeTargetArgs(name string, args []string) []string {
	// get lleva el target al final (get count <target>, get attr <name>
	// <target>): el locator empieza en args[1:] o args[2:].
	if name == "get" {
		if len(args) < 2 {
			return args
		}
		switch args[0] {
		case "count", "value", "text":
			if len(args) > 2 && startsLocator(args[1:]) {
				return append([]string{args[0]}, strings.Join(args[1:], " "))
			}
		case "attr":
			if len(args) > 3 && startsLocator(args[2:]) {
				return append([]string{args[0], args[1]}, strings.Join(args[2:], " "))
			}
		}
		return args
	}
	if !startsLocator(args) {
		return args
	}
	switch name {
	case "click", "hover", "check", "uncheck", "screenshot", "snapshot":
		if len(args) > 1 {
			return []string{strings.Join(args, " ")}
		}
	case "fill", "select":
		if len(args) > 2 {
			return append([]string{strings.Join(args[:len(args)-1], " ")}, args[len(args)-1])
		}
	}
	return args
}

// startsLocator dice si los tokens empiezan por un método locator
// (forma con espacios: `getByRole button "Nombre"`).
func startsLocator(args []string) bool {
	if len(args) == 0 {
		return false
	}
	first := args[0]
	first = strings.TrimPrefix(first, "page.")
	_, ok := locatorMethods[first]
	return ok
}

// HelpText lists all commands, like JS `help`.
func HelpText(caps map[string]bool) string {
	var lines []string
	for _, name := range commandOrder {
		def := CommandsByName[name]
		if def.Capability != "" && !caps[def.Capability] {
			continue
		}
		lines = append(lines, def.Usage)
	}
	return strings.Join(lines, "\n") + "\n\n" + TargetsHelp
}

const TargetsHelp = `Targets are snapshot refs (e12, f1e3 in iframes), Playwright locators
(getByRole, getByLabel, getByText, getByPlaceholder, getByTestId, .first(),
.nth(i), .filter({hasText}), locator('css')), or CSS selectors (quote if spaces).
Text with spaces must be quoted.`

var commandOrder []string

func init() {
	registerCommands()
}

func registerCommands() {
	reg(&CommandDef{Name: "goto", Usage: "goto <url>", Help: "Navigate the current tab to a URL.", MaxArgs: 1, Navigates: true, NoPage: true, Timeout: GotoStepTimeout, Run: doGoto})
	reg(&CommandDef{Name: "back", Usage: "back", Help: "Go back in history.", MaxArgs: 0, Run: doBack})
	reg(&CommandDef{Name: "forward", Usage: "forward", Help: "Go forward in history.", MaxArgs: 0, Run: doForward})
	reg(&CommandDef{Name: "reload", Usage: "reload", Help: "Reload the page.", MaxArgs: 0, Navigates: true, Run: doReload})
	reg(&CommandDef{Name: "click", Usage: "click <target> [--double] [--button left|right|middle] [--modifiers Shift,Control,...]", Help: "Click an element.", Flags: FlagSpec{"double": false, "button": true, "modifiers": true}, MaxArgs: 1, Run: doClick})
	reg(&CommandDef{Name: "hover", Usage: "hover <target>", Help: "Hover over an element.", MaxArgs: 1, Run: doHover})
	reg(&CommandDef{Name: "drag", Usage: "drag <source> <target>", Help: "Drag from one element to another.", MaxArgs: 2, Run: doDrag})
	reg(&CommandDef{Name: "scroll", Usage: "scroll down|up [<px>] | top|bottom | <target> [--in <target>]", Help: "Scroll the page or an area.", Flags: FlagSpec{"in": true}, MaxArgs: 2, Run: doScroll})
	reg(&CommandDef{Name: "fill", Usage: "fill <target> \"<text>\" [--submit]", Help: "Fill a field.", Flags: FlagSpec{"submit": false}, MaxArgs: 2, Run: doFill})
	reg(&CommandDef{Name: "type", Usage: "type \"<text>\" [--submit] [--slowly] [--paste]", Help: "Type into the focused element.", Flags: FlagSpec{"submit": false, "slowly": false, "paste": false}, MaxArgs: 1, Run: doType})
	reg(&CommandDef{Name: "press", Usage: "press <key>", Help: "Press a key (e.g. Control+a).", MaxArgs: 1, Run: doPress})
	reg(&CommandDef{Name: "select", Usage: "select <target> \"<value>\"...", Help: "Select an option.", MaxArgs: -1, Run: doSelect})
	reg(&CommandDef{Name: "check", Usage: "check <target>", Help: "Check a checkbox.", MaxArgs: 1, Run: func(ctx *Context) (string, string, error) { return doCheck(ctx, false) }})
	reg(&CommandDef{Name: "uncheck", Usage: "uncheck <target>", Help: "Uncheck a checkbox.", MaxArgs: 1, Run: func(ctx *Context) (string, string, error) { return doCheck(ctx, true) }})
	reg(&CommandDef{Name: "wait", Usage: "wait <ms> | --text \"<t>\" | --gone <target> | --url <glob> [--timeout <ms>]", Help: "Wait for time, text, or a URL.", Flags: FlagSpec{"text": true, "gone": true, "url": true, "timeout": true}, MaxArgs: 1, NoPage: true, Run: doWait})
	reg(&CommandDef{Name: "tab", Usage: "tab list | new [<url>] | select <tabId> | close [<tabId>]", Help: "List, open, select, or close tabs.", Flags: FlagSpec{"browser": true}, MaxArgs: 2, NoPage: true, Navigates: true, Timeout: GotoStepTimeout, WhileDialog: true, Run: doTab})
	reg(&CommandDef{Name: "browser", Usage: "browser list | use \"<name>\"|<id>", Help: "List browsers or pin the new-tab browser.", MaxArgs: 2, NoPage: true, WhileDialog: true, Run: doBrowser})
	reg(&CommandDef{Name: "snapshot", Usage: "snapshot [<target>] [-i] [--depth N] [--max N|all]", Help: "Accessibility snapshot of the page.", Flags: FlagSpec{"i": false, "depth": true, "max": true}, MaxArgs: 1, Reads: true, Run: doSnapshot})
	reg(&CommandDef{Name: "find", Usage: "find \"<text>\" [--max N|all]", Help: "Find text in the snapshot.", Flags: FlagSpec{"max": true}, MaxArgs: 1, Reads: true, Run: doFind})
	reg(&CommandDef{Name: "get", Usage: "get text|value|count [<target>] | get attr <name> <target> | get url | get title", Help: "Read text, value, count, attribute, URL or title.", Flags: FlagSpec{"max": true}, MaxArgs: 3, Reads: true, Run: doGet})
	reg(&CommandDef{Name: "screenshot", Usage: "screenshot [<target>] [--full] [--path <file>]", Help: "Screenshot the page or an element.", Flags: FlagSpec{"full": false, "path": true}, MaxArgs: 1, Reads: true, Run: doScreenshot})
	reg(&CommandDef{Name: "eval", Usage: "eval \"<function>\" [<target>]", Help: "Run JS in the page.", Flags: FlagSpec{"max": true}, MaxArgs: 2, Run: doEval})
	reg(&CommandDef{Name: "console", Usage: "console [--errors] [--max N|all]", Help: "Console and uncaught errors.", Flags: FlagSpec{"errors": false, "max": true}, MaxArgs: 0, Reads: true, Run: doConsole})
	reg(&CommandDef{Name: "requests", Usage: "requests [--static] [--filter \"<text>\"] [--max N|all]", Help: "Recent network requests.", Flags: FlagSpec{"static": false, "filter": true, "max": true}, MaxArgs: 0, Reads: true, Run: doRequests})
	reg(&CommandDef{Name: "request", Usage: "request <id> [--body] [--max N|all]", Help: "One request's headers and body.", Flags: FlagSpec{"body": false, "max": true}, MaxArgs: 1, Reads: true, Run: doRequest})
	reg(&CommandDef{Name: "dialog", Usage: "dialog accept [\"<text>\"] | dismiss   # a dialog ends the batch it opens in", Help: "Answers the open alert, confirm, prompt or beforeunload dialog. A dialog ends the batch it opens in. accept \"<text>\" fills a prompt.", MaxArgs: 2, WhileDialog: true, Run: doDialog})
	reg(&CommandDef{Name: "upload", Usage: "upload <target> <path>...", Help: "Upload files into a file input.", MaxArgs: -1, Run: doUpload})
	reg(&CommandDef{Name: "pdf", Usage: "pdf [<path>] [--landscape] [--path <file>]", Help: "Save the page as PDF.", Flags: FlagSpec{"landscape": false, "path": true}, MaxArgs: 1, Timeout: PDFDownloadTimeout, Run: doPDF})
	reg(&CommandDef{Name: "download", Usage: "download <url>|<target> [--path <file>]", Help: "Download a file from the page.", Flags: FlagSpec{"path": true}, MaxArgs: 1, Timeout: PDFDownloadTimeout, Run: doDownload})
	reg(&CommandDef{Name: "cdp", Usage: "cdp <Domain.method> [<json>] | cdp on <event>... | cdp events [<event>] | cdp wait <event>", Help: "Raw CDP access (needs --caps cdp).", Flags: FlagSpec{"max": true, "timeout": true}, MaxArgs: -1, Capability: "cdp", Run: doCDP})
	reg(&CommandDef{Name: "help", Usage: "help [<command>]", Help: "List commands or show one command's help.", MaxArgs: 1, NoPage: true, WhileDialog: true, Run: doHelp})

	commandOrder = []string{"goto", "back", "forward", "reload", "click", "hover", "drag", "scroll", "fill", "type", "press", "select", "check", "uncheck", "upload", "wait", "dialog", "tab", "browser", "snapshot", "find", "get", "screenshot", "pdf", "download", "eval", "console", "requests", "request", "cdp", "help"}
}

// --- Navigation ---

func doGoto(ctx *Context) (string, string, error) {
	url := NormalizeURL(ctx.Args[0])
	if err := RequireExposedURL(url); err != nil {
		return "", "", err
	}
	e := ctx.Engine
	sess := ctx.Session
	tabID := e.CurrentTab(sess)
	if tabID == 0 {
		// Create the tab blank, claim it first (so console/request/CDP
		// event listeners are in place), then navigate: creating it
		// already at the URL loads the page before anyone listens and
		// the load requests never reach the buffers.
		id, err := e.backend.CreateTab("about:blank", e.app)
		if err != nil {
			return "", "", err
		}
		sess.mu.Lock()
		sess.Current = id
		sess.Tabs[id] = true
		sess.Opened[id] = true
		sess.mu.Unlock()
		client, _, err := e.ensureClient(sess)
		if err != nil {
			return "", "", err
		}
		if _, err := client.Call("Page.navigate", map[string]any{"url": url}); err != nil {
			return "", "", err
		}
		if err := waitForLoad(client, 10*time.Second); err != nil {
			return "", "", err
		}
		return "→ " + url, "", nil
	}
	client, _, err := e.ensureClient(sess)
	if err != nil {
		return "", "", err
	}
	if _, err := client.Call("Page.navigate", map[string]any{"url": url}); err != nil {
		return "", "", err
	}
	if err := waitForLoad(client, 10*time.Second); err != nil {
		return "", "", err
	}
	return "→ " + url, "", nil
}

func doBack(ctx *Context) (string, string, error) {
	return goHistory(ctx, -1)
}
func doForward(ctx *Context) (string, string, error) {
	return goHistory(ctx, 1)
}

func goHistory(ctx *Context, delta int) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	if _, err := client.Call("Page.navigateHistory", map[string]any{"delta": delta}); err != nil {
		return "", "", err
	}
	if err := waitForLoad(client, 10*time.Second); err != nil {
		return "", "", err
	}
	return "", "", nil
}

func doReload(ctx *Context) (string, string, error) {
	client, _, err := ctx.Engine.ensureClient(ctx.Session)
	if err != nil {
		return "", "", err
	}
	if _, err := client.Call("Page.reload", map[string]any{}); err != nil {
		return "", "", err
	}
	if err := waitForLoad(client, 10*time.Second); err != nil {
		return "", "", err
	}
	return "", "", nil
}

// waitForLoad polls document.readyState until complete (bfcache restores
// don't fire load) or the timeout.
func waitForLoad(client *cdp.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := client.Call("Runtime.evaluate", map[string]any{"expression": "document.readyState", "returnByValue": true})
		if err == nil {
			var res struct {
				Result struct {
					Value string `json:"value"`
				} `json:"result"`
			}
			_ = jsonUnmarshal(raw, &res)
			if res.Result.Value == "complete" {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil // load wait is best-effort, like JS (it notes, doesn't fail)
}

// --- Tabs / browsers ---

func doTab(ctx *Context) (string, string, error) {
	e := ctx.Engine
	sess := ctx.Session
	if len(ctx.Args) == 0 || ctx.Args[0] == "list" {
		tabs := e.backend.Tabs()
		var lines []string
		cur := e.CurrentTab(sess)
		for _, t := range tabs {
			marker := " "
			if t.TabID == cur {
				marker = "*"
			}
			lines = append(lines, fmt.Sprintf("%s %d %s %s", marker, t.TabID, truncate(t.Title, 40), t.URL))
		}
		if len(lines) == 0 {
			return "(no tabs)", "", nil
		}
		return strings.Join(lines, "\n"), "", nil
	}
	switch ctx.Args[0] {
	case "new":
		url := "about:blank"
		if len(ctx.Args) > 1 {
			url = NormalizeURL(ctx.Args[1])
			if err := RequireExposedURL(url); err != nil {
				return "", "", err
			}
		}
		id, err := e.backend.CreateTab(url, e.app)
		if err != nil {
			return "", "", err
		}
		sess.mu.Lock()
		sess.Current = id
		sess.Tabs[id] = true
		sess.Opened[id] = true
		sess.mu.Unlock()
		return fmt.Sprintf("tab %d", id), "", nil
	case "select":
		if len(ctx.Args) < 2 {
			return "", "", fmt.Errorf("tab select needs a tab id")
		}
		id, err := strconv.Atoi(ctx.Args[1])
		if err != nil {
			return "", "", err
		}
		if e.backend.FindTab(id) == nil {
			return "", "", fmt.Errorf("tab %d is not open", id)
		}
		sess.mu.Lock()
		sess.Current = id
		sess.Tabs[id] = true
		// Adoptar la pestaña también le da propiedad para cerrarla:
		// sin esto una pestaña adoptada no se puede cerrar nunca.
		sess.Opened[id] = true
		sess.mu.Unlock()
		// Selecting a tab must leave it attached: claim it now, so a
		// debugger that detached while the tab sat idle fails HERE —
		// with the tab named — instead of surfacing as a surprise in
		// the next command. (tabStateFor is a no-op when the tab is
		// already claimed and live.)
		if _, err := e.tabStateFor(id); err != nil {
			return "", "", err
		}
		// Adopting a user's tab groups it (unless pinned — the extension
		// handles that).
		_ = e.backend.GroupTab(id, &e.app)
		return "", "", nil
	case "focus":
		if len(ctx.Args) < 2 {
			return "", "", fmt.Errorf("tab focus needs a tab id")
		}
		id, err := strconv.Atoi(ctx.Args[1])
		if err != nil {
			return "", "", err
		}
		if err := e.backend.ActivateTab(id); err != nil {
			return "", "", err
		}
		sess.mu.Lock()
		sess.Current = id
		sess.Tabs[id] = true
		sess.mu.Unlock()
		return fmt.Sprintf("tab %d focused", id), "", nil
	case "close":
		id := e.CurrentTab(sess)
		if len(ctx.Args) > 1 {
			id, _ = strconv.Atoi(ctx.Args[1])
		}
		// Only tabs the session opened may be closed (JS rule).
		sess.mu.Lock()
		own := sess.Opened[id]
		sess.mu.Unlock()
		if !own {
			return "", "", fmt.Errorf("tab %d was not opened by this session", id)
		}
		if err := e.backend.CloseTab(id); err != nil {
			return "", "", err
		}
		sess.mu.Lock()
		delete(sess.Tabs, id)
		delete(sess.Opened, id)
		if sess.Current == id {
			sess.Current = 0
		}
		sess.mu.Unlock()
		e.releaseTab(id)
		return "", "", nil
	default:
		return "", "", fmt.Errorf("tab: unknown subcommand %q", ctx.Args[0])
	}
}

func doBrowser(ctx *Context) (string, string, error) {
	e := ctx.Engine
	if len(ctx.Args) == 0 || ctx.Args[0] == "list" {
		summaries := e.backend.Browsers()
		var lines []string
		for _, b := range summaries {
			marker := " "
			if b.Default {
				marker = "*"
			}
			status := "connected"
			if b.Paused {
				status = "paused"
			} else if b.Pairing != "paired" {
				status = "waiting for Allow in its popup"
			}
			lines = append(lines, fmt.Sprintf("%s %s %q — %s, %d tabs", marker, b.ID, b.Label, status, b.Tabs))
		}
		if len(lines) == 0 {
			return "(no browsers)", "", nil
		}
		return strings.Join(lines, "\n"), "", nil
	}
	if ctx.Args[0] == "use" && len(ctx.Args) > 1 {
		name := ctx.Args[1]
		for _, b := range e.backend.Browsers() {
			if b.ID == name || strings.EqualFold(b.Label, name) {
				ctx.Session.mu.Lock()
				ctx.Session.Browser = b.ID
				ctx.Session.mu.Unlock()
				return "", "", nil
			}
		}
		return "", "", fmt.Errorf("no browser named %q", name)
	}
	return "", "", fmt.Errorf("browser: unknown subcommand %q", ctx.Args[0])
}

func doHelp(ctx *Context) (string, string, error) {
	if len(ctx.Args) == 0 {
		return HelpText(ctx.Session.Capabilities), "", nil
	}
	def, ok := CommandsByName[ctx.Args[0]]
	if !ok {
		return "", "", fmt.Errorf("unknown command %q", ctx.Args[0])
	}
	out := def.Usage + "\n" + def.Help
	if def.Capability != "" && !ctx.Session.Capabilities[def.Capability] {
		out += "\nNeeds --caps " + def.Capability
	}
	return out, "", nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
