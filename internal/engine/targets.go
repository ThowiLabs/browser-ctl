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
	if calls, err := parseSpaceLocator(s); err != nil {
		return nil, err
	} else if calls != nil {
		return &Target{Kind: TargetLocator, Calls: calls}, nil
	}
	return &Target{Kind: TargetCSS, Selector: s}, nil
}

// parseSpaceLocator parses the space-separated locator form used on the
// command line: getByRole button "Name", getByText some text, getByLabel
// Email. The name/text is everything after the role (for getByRole) or
// after the method (for the rest); quotes are already stripped by the
// tokenizer. Chaining (.first(), .nth()) is only supported in the
// parenthesised form. Returns (nil, nil) when the string is not a
// space-form locator at all (e.g. a CSS selector with spaces).
func parseSpaceLocator(s string) ([]LocatorCall, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "page.")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		// Un método locator solo, sin argumentos, es un error (no un
		// selector CSS): el usuario claramente quiso un locator.
		if _, ok := locatorMethods[s]; ok {
			return nil, &ParseError{Msg: fmt.Sprintf("%s needs an argument", s)}
		}
		return nil, nil
	}
	method := s[:i]
	max, ok := locatorMethods[method]
	if !ok || max == 0 {
		return nil, nil
	}
	rest := strings.TrimSpace(s[i+1:])
	if rest == "" {
		return nil, &ParseError{Msg: fmt.Sprintf("%s needs an argument", method)}
	}
	switch method {
	case "getByRole":
		j := strings.IndexAny(rest, " \t")
		role := rest
		name := ""
		if j >= 0 {
			role, name = rest[:j], strings.TrimSpace(rest[j+1:])
		}
		if role == "" {
			return nil, &ParseError{Msg: "getByRole needs a role"}
		}
		if name == "" {
			return []LocatorCall{{Method: method, Args: []any{role}}}, nil
		}
		if strings.HasPrefix(name, "{") {
			v, err := parseLiteral(name)
			if err != nil {
				return nil, err
			}
			return []LocatorCall{{Method: method, Args: []any{role, v}}}, nil
		}
		return []LocatorCall{{Method: method, Args: []any{role, unquoteLiteral(name)}}}, nil
	case "getByText", "getByLabel", "getByPlaceholder", "getByAltText", "getByTitle", "getByTestId", "locator":
		return []LocatorCall{{Method: method, Args: []any{unquoteLiteral(rest)}}}, nil
	default:
		return nil, nil
	}
}

// unquoteLiteral quita un nivel de comillas que rodean el texto (la
// línea de comandos ya las quita al tokenizar, pero ParseTarget también
// se llama con strings sin tokenizar).
func unquoteLiteral(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
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

// ResolveTargetSess resuelve un target a una expresión JS usando la tabla
// de refs del snapshot de la sesión para las refs y el árbol AX para los
// locators (locators sin coincidencia en el snapshot y CSS pasan por
// ResolveTarget).
func ResolveTargetSess(e *Engine, client *cdp.Client, sess *Session, target string) (string, error) {
	if refRe.MatchString(target) {
		if sess == nil {
			return "", fmt.Errorf("ref targets need a snapshot first (run `snapshot`)")
		}
		sess.mu.Lock()
		expr, ok := sess.LastRefs[target]
		sess.mu.Unlock()
		if !ok {
			return "", fmt.Errorf("ref %s isn't on the page anymore. Run `snapshot -i` and use a fresh ref", target)
		}
		return expr, nil
	}
	if sess != nil {
		if ref, ok := locatorAXRef(sess, target); ok {
			sess.mu.Lock()
			expr, ok2 := sess.LastRefs[ref]
			sess.mu.Unlock()
			if ok2 {
				return expr, nil
			}
		}
	}
	return ResolveTarget(e, client, target)
}

// locatorAXRef resuelve un target locator a su ref del último snapshot,
// o found=false si no es locator o no hay coincidencia en el árbol AX.
func locatorAXRef(sess *Session, target string) (string, bool) {
	t, err := ParseTarget(target)
	if err != nil || t.Kind != TargetLocator {
		return "", false
	}
	return resolveLocatorAX(sess, t.Calls)
}

// resolveLocatorAX resuelve un locator contra el último snapshot (árbol
// AX): roles y nombres accesibles, en orden de documento. Solo
// getByRole/getByLabel/getByText con .first()/.last()/.nth()/.filter().
// Sin coincidencia devuelve found=false y el llamador usa la traducción
// JS de siempre (cubre elementos fuera del snapshot o snapshots viejos).
func resolveLocatorAX(sess *Session, calls []LocatorCall) (string, bool) {
	matches := resolveLocatorAXMatches(sess, calls)
	if len(matches) == 0 {
		return "", false
	}
	return matches[0].Ref, true
}

// resolveLocatorAXMatches devuelve todos los nodos del snapshot que
// casan con el locator (nil si el método no se resuelve por AX).
func resolveLocatorAXMatches(sess *Session, calls []LocatorCall) []AXEntry {
	if sess == nil || len(calls) == 0 || len(sess.LastAX) == 0 {
		return nil
	}
	sess.mu.Lock()
	ax := append([]AXEntry(nil), sess.LastAX...)
	sess.mu.Unlock()
	var matches []AXEntry
	switch calls[0].Method {
	case "getByRole":
		role, _ := calls[0].Args[0].(string)
		name, exact := locatorNameArg(calls[0].Args)
		for _, n := range ax {
			if !roleMatches(role, n.Role) {
				continue
			}
			if name != "" && !nameMatches(name, n.Name, exact) {
				continue
			}
			matches = append(matches, n)
		}
	case "getByLabel":
		text, _ := calls[0].Args[0].(string)
		for _, n := range ax {
			if nameMatches(text, n.Name, false) {
				matches = append(matches, n)
			}
		}
	case "getByText":
		text, _ := calls[0].Args[0].(string)
		for _, n := range ax {
			if nameMatches(text, n.Name, false) || nameMatches(text, n.Value, false) {
				matches = append(matches, n)
			}
		}
	default:
		return nil
	}
	for _, c := range calls[1:] {
		switch c.Method {
		case "filter":
			var has string
			if len(c.Args) > 0 {
				if obj, ok := c.Args[0].(map[string]any); ok {
					has, _ = obj["hasText"].(string)
				}
			}
			var kept []AXEntry
			for _, n := range matches {
				if has == "" || nameMatches(has, n.Name, false) {
					kept = append(kept, n)
				}
			}
			matches = kept
		case "first":
			if len(matches) > 0 {
				matches = matches[:1]
			}
		case "last":
			if len(matches) > 0 {
				matches = matches[len(matches)-1:]
			}
		case "nth":
			if len(c.Args) > 0 {
				if f, ok := c.Args[0].(float64); ok {
					i := int(f)
					if i < 0 || i >= len(matches) {
						return nil
					}
					matches = matches[i : i+1]
				}
			}
		default:
			return nil
		}
	}
	return matches
}

// locatorNameArg lee el nombre de getByRole: segundo argumento como
// string plano (forma con espacios) o como objeto {name, exact}.
func locatorNameArg(args []any) (name string, exact bool) {
	if len(args) < 2 {
		return "", false
	}
	switch v := args[1].(type) {
	case string:
		return v, false
	case map[string]any:
		name, _ = v["name"].(string)
		exact, _ = v["exact"].(bool)
		return name, exact
	}
	return "", false
}

// roleMatches compara roles AX: getByRole('textbox') casa con textbox y
// searchbox, como en Playwright.
func roleMatches(want, got string) bool {
	if strings.EqualFold(want, got) {
		return true
	}
	return want == "textbox" && got == "searchbox"
}

// nameMatches compara nombres accesibles: normaliza espacios y
// mayúsculas; exact exige igualdad total, si no basta con contener
// (semántica por defecto de Playwright).
func nameMatches(want, got string, exact bool) bool {
	norm := func(s string) string {
		return strings.Join(strings.Fields(strings.ToLower(s)), " ")
	}
	w, g := norm(want), norm(got)
	if w == "" {
		return true
	}
	if exact {
		return w == g
	}
	return strings.Contains(g, w)
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
			name, exact := locatorNameArg(c.Args)
			expr = fmt.Sprintf("(function(){var els=Array.from(%s.querySelectorAll('[role=\"%s\"],%s'));%s return els[0]||null;})()", expr, role, roleTag(role), nameFilter(name, exact))
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

func nameFilter(name string, exact bool) string {
	if name == "" {
		return ""
	}
	if exact {
		return fmt.Sprintf("els=els.filter(function(el){return ((el.innerText||el.getAttribute('aria-label')||'').trim()===%s);});", jsonString(name))
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
	// Locator: count via the AX matches when available (exact count),
	// else via the translated query (first match -> 1/0).
	if sess != nil {
		if matches := resolveLocatorAXMatches(sess, t.Calls); matches != nil {
			return len(matches), nil
		}
	}
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
