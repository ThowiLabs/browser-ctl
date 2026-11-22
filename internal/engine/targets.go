package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/thovilabs/browser-ctl/internal/cdp"
)

// Target resolution, mirroring the JS targets.ts whitelist. Locator
// expressions are parsed, never evaluated as code.

var refRe = regexp.MustCompile(`^(f\d+)?e\d+$`)

// Target kinds.
const (
	TargetRef     = "ref"
	TargetLocator = "locator"
	TargetCSS     = "css"
)

// Target is a parsed element target.
type Target struct {
	Kind string
	Ref  string
	// Locator: ordered method calls, each with literal args.
	Calls []LocatorCall
	// CSS selector.
	Selector string
}

// LocatorCall is one whitelisted Playwright locator method call.
type LocatorCall struct {
	Method string
	Args   []any
}

var locatorRe = regexp.MustCompile(`^(page\.)?[a-zA-Z]+\(`)

// whitelisted locator methods and their max args.
var locatorMethods = map[string]int{
	"getByRole": 2, "getByText": 2, "getByLabel": 2, "getByPlaceholder": 2,
	"getByAltText": 2, "getByTitle": 2, "getByTestId": 1, "locator": 2,
	"frameLocator": 1, "contentFrame": 0, "filter": 1,
	"first": 0, "last": 0, "nth": 1,
}

// ParseTarget parses a target string.
func ParseTarget(s string) (*Target, error) {
	s = strings.TrimSpace(s)
	if refRe.MatchString(s) {
		return &Target{Kind: TargetRef, Ref: s}, nil
	}
	if locatorRe.MatchString(s) {
		calls, err := parseLocatorChain(s)
		if err != nil {
			return nil, err
		}
		return &Target{Kind: TargetLocator, Calls: calls}, nil
	}
	return &Target{Kind: TargetCSS, Selector: s}, nil
}

// parseLocatorChain parses e.g. getByRole('button', {name: 'Go'}).first()
// Only literals are allowed: strings, numbers, booleans, regexes, flat
// objects. No nested objects, no template ${}.
func parseLocatorChain(s string) ([]LocatorCall, error) {
	var calls []LocatorCall
	i := 0
	for i < len(s) {
		// method name
		j := i
		for j < len(s) && (isIdentChar(s[j])) {
			j++
		}
		if j == i {
			return nil, &ParseError{Column: i + 1, Msg: "expected locator method"}
		}
		method := s[i:j]
		// optional "page." prefix already consumed by caller? handle here:
		if method == "page" && j < len(s) && s[j] == '.' {
			i = j + 1
			continue
		}
		max, ok := locatorMethods[method]
		if !ok {
			return nil, &ParseError{Column: i + 1, Msg: fmt.Sprintf("locator method %q is not allowed", method)}
		}
		if j >= len(s) || s[j] != '(' {
			return nil, &ParseError{Column: j + 1, Msg: "expected ( after locator method"}
		}
		// find matching close paren
		depth := 0
		k := j
		var inStr byte
		for k < len(s) {
			ch := s[k]
			if inStr != 0 {
				if ch == '\\' {
					k += 2
					continue
				}
				if ch == inStr {
					inStr = 0
				}
				k++
				continue
			}
			if ch == '"' || ch == '\'' || ch == '/' && k+1 < len(s) && s[k+1] != '/' {
				// '/' as regex start is ambiguous; only treat as string
				// delimiter for quotes.
				if ch != '/' {
					inStr = ch
				}
			}
			if ch == '(' {
				depth++
			}
			if ch == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
			k++
		}
		if k >= len(s) {
			return nil, &ParseError{Column: j + 1, Msg: "unbalanced ( in locator"}
		}
		inner := s[j+1 : k]
		args, err := parseLiteralArgs(inner)
		if err != nil {
			return nil, err
		}
		if len(args) > max {
			return nil, &ParseError{Msg: fmt.Sprintf("%s takes at most %d argument(s)", method, max)}
		}
		calls = append(calls, LocatorCall{Method: method, Args: args})
		i = k + 1
		if i < len(s) && s[i] == '.' {
			i++
			continue
		}
		break
	}
	if len(calls) == 0 {
		return nil, &ParseError{Msg: "empty locator"}
	}
	return calls, nil
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// parseLiteralArgs splits top-level comma args and parses each as a
// literal: string, number, boolean, regex /.../, or flat object.
func parseLiteralArgs(s string) ([]any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts, err := splitTopLevel(s)
	if err != nil {
		return nil, err
	}
	var out []any
	for _, p := range parts {
		v, err := parseLiteral(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func splitTopLevel(s string) ([]string, error) {
	var parts []string
	depth := 0
	var inStr byte
	start := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inStr != 0 {
			if ch == '\\' {
				i++
				continue
			}
			if ch == inStr {
				inStr = 0
			}
			continue
		}
		switch ch {
		case '"', '\'':
			inStr = ch
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if inStr != 0 || depth != 0 {
		return nil, &ParseError{Msg: "unbalanced brackets in locator args"}
	}
	parts = append(parts, s[start:])
	return parts, nil
}

func parseLiteral(s string) (any, error) {
	if s == "" {
		return nil, &ParseError{Msg: "empty locator argument"}
	}
	if (s[0] == '"' || s[0] == '\'') && len(s) >= 2 && s[len(s)-1] == s[0] {
		// string literal (JSON-style for ", simple for ')
		if s[0] == '"' {
			var v string
			if err := json.Unmarshal([]byte(s), &v); err == nil {
				return v, nil
			}
		}
		return s[1 : len(s)-1], nil
	}
	if strings.HasPrefix(s, "/") {
		// regex literal /pattern/flags
		end := strings.LastIndex(s, "/")
		if end > 0 {
			return map[string]string{"regex": s[1:end], "flags": s[end+1:]}, nil
		}
	}
	if s == "true" {
		return true, nil
	}
	if s == "false" {
		return false, nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return n, nil
	}
	if strings.HasPrefix(s, "{") {
		// flat object only: no nested objects, no ${}
		if strings.Contains(s, "${") {
			return nil, &ParseError{Msg: "can't run code in the daemon"}
		}
		inner := strings.TrimSpace(s[1 : len(s)-1])
		obj := map[string]any{}
		if inner == "" {
			return obj, nil
		}
		for _, part := range strings.Split(inner, ",") {
			kv := strings.SplitN(strings.TrimSpace(part), ":", 2)
			if len(kv) != 2 {
				return nil, &ParseError{Msg: "bad object literal in locator"}
			}
			key := strings.Trim(kv[0], `"' `)
			val, err := parseLiteral(strings.TrimSpace(kv[1]))
			if err != nil {
				return nil, err
			}
			if _, isMap := val.(map[string]any); isMap {
				return nil, &ParseError{Msg: "nested objects are not allowed in locators"}
			}
			obj[key] = val
		}
		return obj, nil
	}
	return nil, &ParseError{Msg: fmt.Sprintf("can't parse locator argument %q", s)}
}

// ResolveTarget resolves a target to a DOM node index (as used by the
// snapshot ref table) or a CSS selector for direct queries.
// Returns a JS expression that yields the element, or an error.
func ResolveTarget(e *Engine, client *cdp.Client, target string) (string, error) {
	t, err := ParseTarget(target)
	if err != nil {
		return "", err
	}
	switch t.Kind {
	case TargetRef:
		// Refs are resolved by the snapshot ref table in the engine.
		return "", fmt.Errorf("ref targets need a snapshot first (run `snapshot`)")
	case TargetCSS:
		return fmt.Sprintf("document.querySelector(%s)", jsonString(t.Selector)), nil
	case TargetLocator:
		return locatorToJS(t.Calls)
	}
	return "", fmt.Errorf("unknown target kind")
}

// locatorToJS translates whitelisted locator calls into a JS query.
// getByRole/getByText/etc. are approximated with DOM queries; exact
// Playwright semantics live in the snapshot's ARIA tree (milestone 4).
func locatorToJS(calls []LocatorCall) (string, error) {
	expr := "document"
	for _, c := range calls {
		switch c.Method {
		case "getByRole":
			role, _ := c.Args[0].(string)
			name := ""
			if len(c.Args) > 1 {
				if obj, ok := c.Args[1].(map[string]any); ok {
					name, _ = obj["name"].(string)
				}
			}
			expr = fmt.Sprintf("(function(){var els=Array.from(%s.querySelectorAll('[role=\"%s\"],%s'));%s return els[0]||null;})()", expr, role, roleTag(role), nameFilter(name))
		case "getByText":
			text, _ := c.Args[0].(string)
			expr = fmt.Sprintf("(function(){var els=Array.from(%s.querySelectorAll('*')).filter(function(el){return el.children.length===0 && (el.textContent||'').trim().indexOf(%s)>=0;}); return els[0]||null;})()", expr, jsonString(text))
		case "getByLabel":
			text, _ := c.Args[0].(string)
			expr = fmt.Sprintf("(function(){var els=Array.from(%s.querySelectorAll('label'));var el=els.find(function(l){return (l.textContent||'').trim().indexOf(%s)>=0;}); if(!el) return null; var id=el.getAttribute('for'); return id?%s.getElementById(id):el.querySelector('input,select,textarea');})()", expr, jsonString(text), expr)
		case "getByPlaceholder":
			text, _ := c.Args[0].(string)
			expr = fmt.Sprintf("%s.querySelector('[placeholder*='+%s+']')", expr, jsonString(text))
		case "getByTestId":
			id, _ := c.Args[0].(string)
			expr = fmt.Sprintf("%s.querySelector('[data-testid='+%s+']')", expr, jsonString(id))
		case "locator":
			sel, _ := c.Args[0].(string)
			expr = fmt.Sprintf("%s.querySelector(%s)", expr, jsonString(sel))
		case "first":
			// first element already returned by the helpers above
		case "nth":
			// nth is applied by the caller via index; not supported in JS expr
			return "", fmt.Errorf(".nth() needs snapshot refs; use a more specific target")
		case "filter", "frameLocator", "contentFrame", "last":
			return "", fmt.Errorf("%s is not supported yet in the Go engine", c.Method)
		default:
			return "", fmt.Errorf("locator method %q is not allowed", c.Method)
		}
	}
	return expr, nil
}

func roleTag(role string) string {
	switch role {
	case "button":
		return "button"
	case "link":
		return "a"
	case "textbox":
		return "input,textarea"
	case "checkbox":
		return "input[type=checkbox]"
	case "radio":
		return "input[type=radio]"
	case "combobox":
		return "select,input"
	case "heading":
		return "h1,h2,h3,h4,h5,h6"
	default:
		return "*"
	}
}

func nameFilter(name string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf("els=els.filter(function(el){return ((el.innerText||el.getAttribute('aria-label')||'')+'').indexOf(%s)>=0;});", jsonString(name))
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// countForTarget counts elements matching a target: refs via their stored
// expression, CSS via querySelectorAll, locators via their JS translation
// (counting all matches of the base query).
func countForTarget(e *Engine, client *cdp.Client, sess *Session, target string) (int, error) {
	if refRe.MatchString(target) {
		sess.mu.Lock()
		expr, ok := sess.LastRefs[target]
		sess.mu.Unlock()
		if !ok {
			return 0, fmt.Errorf("ref %s isn't on the page anymore. Run `snapshot -i` and use a fresh ref", target)
		}
		raw, err := e.eval(client, fmt.Sprintf("(function(){var el=%s; return el?1:0;})()", expr))
		if err != nil {
			return 0, err
		}
		var n int
		_ = json.Unmarshal(raw, &n)
		return n, nil
	}
	t, err := ParseTarget(target)
	if err != nil {
		return 0, err
	}
	if t.Kind == TargetCSS {
		raw, err := e.eval(client, fmt.Sprintf("document.querySelectorAll(%s).length", jsonString(t.Selector)))
		if err != nil {
			return 0, err
		}
		var n int
		_ = json.Unmarshal(raw, &n)
		return n, nil
	}
	// Locator: count via the translated query (first match -> 1/0).
	js, err := locatorToJS(t.Calls)
	if err != nil {
		return 0, err
	}
	raw, err := e.eval(client, fmt.Sprintf("(function(){var el=%s; return el?1:0;})()", js))
	if err != nil {
		return 0, err
	}
	var n int
	_ = json.Unmarshal(raw, &n)
	return n, nil
}
