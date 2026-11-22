package engine

import "testing"

func TestParseCommands(t *testing.T) {
	lines, err := ParseCommands("goto https://example.com\n# comment\n\nclick e12\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0].Name != "goto" || lines[1].Name != "click" {
		t.Fatalf("names: %q %q", lines[0].Name, lines[1].Name)
	}
}

func TestTokenizeQuoted(t *testing.T) {
	toks, err := Tokenize(`e12 "hello world" --submit`)
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 3 || toks[1] != "hello world" {
		t.Fatalf("toks=%v", toks)
	}
}

func TestTokenizeBrackets(t *testing.T) {
	toks, err := Tokenize(`getByRole('button', {name: 'Go now'})`)
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 1 {
		t.Fatalf("locator with spaces must be one token, got %v", toks)
	}
}

func TestTokenizeUnterminated(t *testing.T) {
	if _, err := Tokenize(`e12 "oops`); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseArgsUnknownFlag(t *testing.T) {
	_, _, err := ParseArgs([]string{"--nope"}, FlagSpec{"max": true})
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
}

func TestParseArgsValueFlag(t *testing.T) {
	args, flags, err := ParseArgs([]string{"e12", "--max", "all"}, FlagSpec{"max": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || flags["max"] != "all" {
		t.Fatalf("args=%v flags=%v", args, flags)
	}
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"example.com":         "https://example.com",
		"localhost:3000":      "http://localhost:3000",
		"127.0.0.1:8080":      "http://127.0.0.1:8080",
		"https://example.com": "https://example.com",
	}
	for in, want := range cases {
		if got := NormalizeURL(in); got != want {
			t.Errorf("NormalizeURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCheckLine(t *testing.T) {
	ln := CommandLine{Name: "goto", Args: []string{"https://example.com"}}
	if err := CheckLine(ln); err != nil {
		t.Fatal(err)
	}
	bad := CommandLine{Name: "nope", Args: nil}
	if err := CheckLine(bad); err == nil {
		t.Fatal("expected unknown command error")
	}
	tooMany := CommandLine{Name: "goto", Args: []string{"a", "b"}}
	if err := CheckLine(tooMany); err == nil {
		t.Fatal("expected arg count error")
	}
}

func TestParseTarget(t *testing.T) {
	if tg, err := ParseTarget("e12"); err != nil || tg.Kind != TargetRef {
		t.Fatalf("ref: %v %v", tg, err)
	}
	if tg, err := ParseTarget("f2e12"); err != nil || tg.Kind != TargetRef {
		t.Fatalf("frame ref: %v %v", tg, err)
	}
	if tg, err := ParseTarget("getByRole('button', {name: 'Go'})"); err != nil || tg.Kind != TargetLocator {
		t.Fatalf("locator: %v %v", tg, err)
	}
	if tg, err := ParseTarget(".btn-primary"); err != nil || tg.Kind != TargetCSS {
		t.Fatalf("css: %v %v", tg, err)
	}
	if _, err := ParseTarget("eval('1+1')"); err == nil {
		t.Fatal("eval() as a locator must be rejected")
	}
}

func TestURLMatch(t *testing.T) {
	if !urlMatch("https://example.com/*", "https://example.com/page") {
		t.Error("single * should match one segment")
	}
	if urlMatch("https://example.com/*", "https://example.com/a/b") {
		t.Error("single * should not match a slash")
	}
	if !urlMatch("https://example.com/**", "https://example.com/a/b") {
		t.Error("** should match nested paths")
	}
	if !urlMatch("example.com", "https://example.com/page") {
		t.Error("plain pattern is a substring match")
	}
}

func TestSafeFileName(t *testing.T) {
	if got := safeFileName("../../etc/passwd"); got != "passwd" {
		t.Errorf("got %q", got)
	}
	if got := safeFileName("con.txt"); got != "_con.txt" {
		t.Errorf("reserved name: got %q", got)
	}
}
