// Package engine: the command language. Parser first: quoted args with
// JSON escaping, balanced brackets for locators/CSS/JSON, verbatim eval
// lines, ParseError with column and never the text.
package engine

import (
	"fmt"
	"strings"
)

// ParseError is a user-facing parse failure. It never includes the text
// (it may be typed page text).
type ParseError struct {
	Column int
	Msg    string
}

func (e *ParseError) Error() string {
	if e.Column > 0 {
		return fmt.Sprintf("parse error at column %d: %s", e.Column, e.Msg)
	}
	return fmt.Sprintf("parse error: %s", e.Msg)
}

// CommandLine is one parsed command line.
type CommandLine struct {
	Step   int
	Source string
	Name   string
	Args   []string
	Flags  map[string]string // flag name -> value ("" for switches)
}

// ParseCommands splits input into command lines. Blank lines and #
// comments are skipped.
func ParseCommands(input string) ([]CommandLine, error) {
	var out []CommandLine
	lines := strings.Split(input, "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := CommandName(line)
		if name == "" {
			return nil, &ParseError{Column: 1, Msg: "empty command"}
		}
		// eval lines are verbatim: only the quote's own escape is decoded.
		var args []string
		var err error
		if name == "eval" {
			args, err = tokenizeEval(line[len(name):])
		} else {
			args, err = Tokenize(line[len(name):])
		}
		if err != nil {
			return nil, err
		}
		out = append(out, CommandLine{Step: i + 1, Source: line, Name: name, Args: args, Flags: map[string]string{}})
	}
	return out, nil
}

// CommandName is the first word of a line, without tokenising the rest.
func CommandName(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return line[:i]
	}
	return line
}

// Tokenize splits args, handling double/single-quoted args with JSON
// escaping and balanced brackets so Playwright locators, CSS attribute
// selectors and JSON stay single tokens even with spaces.
func Tokenize(s string) ([]string, error) {
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		tok, n, err := nextToken(s, i)
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
		i = n
	}
	return out, nil
}

func nextToken(s string, start int) (string, int, error) {
	c := s[start]
	if c == '"' || c == '\'' {
		return quotedToken(s, start, c)
	}
	// Unquoted: read until whitespace, tracking bracket depth so
	// locator('a[href="/x y"]') and JSON stay whole.
	depth := 0
	i := start
	for i < len(s) {
		ch := s[i]
		switch ch {
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			depth--
			if depth < 0 {
				return "", 0, &ParseError{Column: i + 1, Msg: "unbalanced bracket"}
			}
		case '"', '\'':
			// quoted section inside an unquoted token
			_, n, err := quotedToken(s, i, ch)
			if err != nil {
				return "", 0, err
			}
			i = n
			continue
		case ' ', '\t':
			if depth == 0 {
				return s[start:i], i, nil
			}
		}
		i++
	}
	if depth != 0 {
		return "", 0, &ParseError{Column: start + 1, Msg: "unbalanced bracket"}
	}
	return s[start:i], i, nil
}

func quotedToken(s string, start int, quote byte) (string, int, error) {
	// Find the matching quote, honouring backslash escapes.
	i := start + 1
	var sb strings.Builder
	for i < len(s) {
		ch := s[i]
		if ch == '\\' && i+1 < len(s) {
			next := s[i+1]
			switch next {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '\\':
				sb.WriteByte('\\')
			case quote:
				sb.WriteByte(quote)
			default:
				sb.WriteByte(next)
			}
			i += 2
			continue
		}
		if ch == quote {
			return sb.String(), i + 1, nil
		}
		sb.WriteByte(ch)
		i++
	}
	return "", 0, &ParseError{Column: start + 1, Msg: "unterminated quote"}
}

// tokenizeEval keeps an eval line verbatim: the first quoted argument is
// the JS expression, decoded only for its own quote escapes.
func tokenizeEval(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if s[0] == '"' || s[0] == '\'' {
		tok, n, err := quotedToken(s, 0, s[0])
		if err != nil {
			return nil, err
		}
		rest := strings.TrimSpace(s[n:])
		out := []string{tok}
		if rest != "" {
			more, err := Tokenize(rest)
			if err != nil {
				return nil, err
			}
			out = append(out, more...)
		}
		return out, nil
	}
	return Tokenize(s)
}

// FlagSpec maps flag names to whether they take a value (true) or are
// switches (false).
type FlagSpec map[string]bool

// ParseArgs splits a token list into positional args and flags per spec.
// Quoted tokens are always positional. Unknown flags are an error.
func ParseArgs(tokens []string, spec FlagSpec) (args []string, flags map[string]string, err error) {
	flags = map[string]string{}
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if strings.HasPrefix(t, "--") && len(t) > 2 {
			name := t[2:]
			// --name=value form
			if eq := strings.Index(name, "="); eq >= 0 {
				val := name[eq+1:]
				name = name[:eq]
				takes, ok := spec[name]
				if !ok {
					return nil, nil, &ParseError{Msg: fmt.Sprintf("unknown flag --%s (run `help`)", name)}
				}
				if !takes {
					return nil, nil, &ParseError{Msg: fmt.Sprintf("flag --%s takes no value", name)}
				}
				flags[name] = val
				continue
			}
			takes, ok := spec[name]
			if !ok {
				return nil, nil, &ParseError{Msg: fmt.Sprintf("unknown flag --%s (run `help`)", name)}
			}
			if takes {
				if i+1 >= len(tokens) {
					return nil, nil, &ParseError{Msg: fmt.Sprintf("flag --%s needs a value", name)}
				}
				i++
				flags[name] = tokens[i]
			} else {
				flags[name] = ""
			}
			continue
		}
		if strings.HasPrefix(t, "-") && len(t) == 2 && t != "-" {
			// short flag, e.g. -i
			name := t[1:]
			takes, ok := spec[name]
			if !ok {
				return nil, nil, &ParseError{Msg: fmt.Sprintf("unknown flag -%s (run `help`)", name)}
			}
			if takes {
				if i+1 >= len(tokens) {
					return nil, nil, &ParseError{Msg: fmt.Sprintf("flag -%s needs a value", name)}
				}
				i++
				flags[name] = tokens[i]
			} else {
				flags[name] = ""
			}
			continue
		}
		args = append(args, t)
	}
	return args, flags, nil
}

// NormalizeURL adds a scheme when missing: http for localhost/loopback,
// https otherwise. Nothing else is normalised.
func NormalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return u
	}
	if strings.Contains(u, "://") {
		return u
	}
	lower := strings.ToLower(u)
	if strings.HasPrefix(lower, "localhost") || strings.HasPrefix(lower, "127.") || strings.HasPrefix(lower, "[::1]") {
		return "http://" + u
	}
	return "https://" + u
}
