package main

// browser-ctl fill-login <domain>
//
// Fills the login form of the active page with a credential that arrives
// ONLY on stdin, as a JSON payload {"username","password"} attached by
// the host (Lilith's experimental web vault: the agent runs
// `browser-ctl fill-login <domain>` and Lilith's shell tool feeds the
// payload through the process's stdin — never argv, never environment,
// never the tool result). The values are used to fill the form and are
// never printed: stdout/stderr only carry the domain and status.
//
// Safety: the fill is refused when the active page's host does not match
// the requested domain (exact host or a subdomain of it), so a credential
// cannot be sprayed onto the wrong site by a confused caller.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/thovilabs/browser-ctl/internal/client"
)

type vaultPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// parseVaultPayload validates the stdin payload. Errors never echo the
// payload's contents.
func parseVaultPayload(data []byte) (vaultPayload, error) {
	var p vaultPayload
	if len(strings.TrimSpace(string(data))) == 0 {
		return p, errors.New("fill-login: no vault payload on stdin (expected JSON {\"username\",\"password\"} attached by the host)")
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, errors.New("fill-login: invalid vault payload on stdin")
	}
	if p.Username == "" || p.Password == "" {
		return p, errors.New("fill-login: vault payload on stdin is missing username or password")
	}
	return p, nil
}

// hostMatchesDomain reports whether a credential for domain may be filled
// on host: exact match or a subdomain of the credential's domain.
func hostMatchesDomain(host, domain string) bool {
	host = normalizeHost(host)
	domain = normalizeHost(domain)
	if host == "" || domain == "" {
		return false
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func normalizeHost(raw string) string {
	d := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.Index(d, "://"); i >= 0 {
		d = d[i+3:]
	}
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	if i := strings.LastIndex(d, ":"); i >= 0 {
		d = d[:i]
	}
	return strings.TrimSuffix(d, ".")
}

// locateLoginScript runs in the active page and returns a JSON string
// {host, user, pass} with the selectors of the login form (or {error}).
// It never reads field values.
const locateLoginScript = `function(){
  var visible = function(el){ var s = getComputedStyle(el); var r = el.getBoundingClientRect(); return s.visibility !== 'hidden' && s.display !== 'none' && Number(s.opacity || 1) > 0 && r.width > 0 && r.height > 0; };
  var esc = function(v){ return window.CSS && CSS.escape ? CSS.escape(v) : String(v).replace(/[^a-zA-Z0-9_-]/g, '\\$&'); };
  var selector = function(el){
    if (el.id) return '#' + esc(el.id);
    var attrs = ['data-testid','data-test','data-qa'];
    for (var a = 0; a < attrs.length; a++) { var v = el.getAttribute(attrs[a]); if (v) return '[' + attrs[a] + '="' + String(v).replace(/"/g, '\\"') + '"]'; }
    var name = el.getAttribute('name');
    if (name) { var cand = el.tagName.toLowerCase() + '[name="' + String(name).replace(/"/g, '\\"') + '"]'; if (document.querySelectorAll(cand).length === 1) return cand; }
    var parts = []; var node = el;
    while (node && node.nodeType === 1 && node !== document.documentElement) {
      var part = node.tagName.toLowerCase(); var parent = node.parentElement;
      if (parent) { var same = Array.prototype.filter.call(parent.children, function(x){ return x.tagName === node.tagName; }); if (same.length > 1) part += ':nth-of-type(' + (Array.prototype.indexOf.call(same, node) + 1) + ')'; }
      parts.unshift(part); if (parts.length >= 7) break; node = parent;
    }
    return parts.join(' > ');
  };
  var forms = Array.prototype.slice.call(document.querySelectorAll('form'));
  var types = ['password','text','email','tel','search','url','number'];
  var fields = [];
  var nodes = Array.prototype.slice.call(document.querySelectorAll('input'));
  for (var i = 0; i < nodes.length; i++) {
    var el = nodes[i];
    var type = (el.getAttribute('type') || 'text').toLowerCase();
    if (types.indexOf(type) < 0 || el.disabled || !visible(el)) continue;
    fields.push({selector: selector(el), type: type, form: el.form ? forms.indexOf(el.form) : -1, order: fields.length});
  }
  var isUser = function(t){ return ['','text','email','tel','search','url','number'].indexOf(t) >= 0; };
  var passes = fields.filter(function(f){ return f.type === 'password'; });
  if (!passes.length) return JSON.stringify({error: 'no visible password field on the active page'});
  for (var j = 0; j < passes.length; j++) {
    var p = passes[j]; var best = null;
    for (var k = 0; k < fields.length; k++) {
      var f = fields[k];
      if (f.order >= p.order || !isUser(f.type)) continue;
      if (p.form >= 0 && f.form !== p.form) continue;
      if (!best || f.order > best.order) best = f;
    }
    if (best) return JSON.stringify({host: location.hostname, user: best.selector, pass: p.selector});
  }
  return JSON.stringify({host: location.hostname, user: '', pass: passes[0].selector});
}`

type locateResult struct {
	Host  string `json:"host"`
	User  string `json:"user"`
	Pass  string `json:"pass"`
	Error string `json:"error"`
}

// quoteCommandArg renders a string as a double-quoted command argument
// using ONLY the escapes the engine's parser decodes (\" \\ \n \t \r);
// every other character travels literally as UTF-8. (json.Marshal would
// also emit \uXXXX escapes, which the parser does not decode.)
func quoteCommandArg(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// buildLocateCommand renders the eval command line for the engine: the
// script travels as one quoted argument.
func buildLocateCommand() string {
	return "eval " + quoteCommandArg(locateLoginScript)
}

// buildFillCommand renders one engine fill line with both the target and
// the text as quoted arguments, so any credential characters (quotes,
// backslashes, unicode) survive the command parser intact.
func buildFillCommand(selector, text string) string {
	return "fill " + quoteCommandArg(selector) + " " + quoteCommandArg(text)
}

// fillLoginUsage is the subcommand's one-line usage.
const fillLoginUsage = "usage: browser-ctl fill-login [--port N] <domain>"

// usageError marks a command-line usage problem; main exits 2 for it
// (like an unknown subcommand), not 1 like a runtime failure.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// parseFillLoginArgs parses the flags BEFORE anything reads stdin:
// --help/-h is reported as help (the caller prints usage and exits 0)
// and a missing or malformed domain is a usage error. Neither path may
// ever reach the vault payload on stdin — `fill-login --help` once fell
// through to the payload read and surfaced as a vault failure.
func parseFillLoginArgs(args []string) (port int, domain string, help bool, err error) {
	fs := flag.NewFlagSet("fill-login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	p := fs.Int("port", 0, "daemon port (0 = find or start)")
	if perr := fs.Parse(args); perr != nil {
		if errors.Is(perr, flag.ErrHelp) {
			return 0, "", true, nil
		}
		return 0, "", false, &usageError{msg: fillLoginUsage}
	}
	if fs.NArg() != 1 {
		return 0, "", false, &usageError{msg: fillLoginUsage}
	}
	domain = normalizeHost(fs.Arg(0))
	if domain == "" {
		return 0, "", false, &usageError{msg: "fill-login: empty domain — " + fillLoginUsage}
	}
	return *p, domain, false, nil
}

func runFillLogin(args []string) error {
	port, domain, help, err := parseFillLoginArgs(args)
	if err != nil {
		return err
	}
	if help {
		fmt.Println(fillLoginUsage + "\n\nFill the active page's login form with the vault payload (JSON {\"username\",\"password\"}) read from stdin, attached by the host. The active page's host must match the domain.")
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 64*1024))
	if err != nil {
		return fmt.Errorf("fill-login: reading stdin: %w", err)
	}
	payload, err := parseVaultPayload(data)
	if err != nil {
		return err
	}
	out, err := client.RunCommands(port, buildLocateCommand(), nil)
	if err != nil {
		return err
	}
	var loc locateResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &loc); err != nil {
		return errors.New("fill-login: could not read the active page's form")
	}
	if loc.Error != "" {
		return fmt.Errorf("fill-login: %s", loc.Error)
	}
	if loc.Pass == "" {
		return errors.New("fill-login: no login form found on the active page")
	}
	if !hostMatchesDomain(loc.Host, domain) {
		return fmt.Errorf("fill-login: the active page is %s, not %s; refusing to fill", loc.Host, domain)
	}
	var cmds []string
	if loc.User != "" {
		cmds = append(cmds, buildFillCommand(loc.User, payload.Username))
	}
	cmds = append(cmds, buildFillCommand(loc.Pass, payload.Password))
	if _, err := client.RunCommands(port, strings.Join(cmds, "\n"), nil); err != nil {
		return err
	}
	fmt.Printf("login filled for %s\n", domain)
	return nil
}
