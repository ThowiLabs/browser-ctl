package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/thovilabs/browser-ctl/internal/engine"
)

func TestParseVaultPayload(t *testing.T) {
	p, err := parseVaultPayload([]byte(`{"username":"u@example.com","password":"s3cret"}`))
	if err != nil || p.Username != "u@example.com" || p.Password != "s3cret" {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	for _, bad := range []string{"", "  ", "not json", `{"username":"u"}`, `{"password":"p"}`, `{"username":"","password":"p"}`} {
		if _, err := parseVaultPayload([]byte(bad)); err == nil {
			t.Fatalf("payload %q debió fallar", bad)
		} else if strings.Contains(err.Error(), "s3cret") {
			t.Fatalf("el error no debe ecoar el payload: %v", err)
		}
	}
}

// TestParseFillLoginArgs: los flags se parsean antes de leer stdin —
// --help/-h es ayuda (exit 0) y la falta de dominio es usageError
// (main lo mapea a exit 2); ninguno de los dos caminos toca el payload.
func TestParseFillLoginArgs(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}} {
		_, _, help, err := parseFillLoginArgs(args)
		if err != nil || !help {
			t.Fatalf("args %v: help=%v err=%v", args, help, err)
		}
	}
	for _, args := range [][]string{{}, {"--bogus"}, {"a.com", "b.com"}, {"--port"}} {
		_, _, help, err := parseFillLoginArgs(args)
		var ue *usageError
		if help || !errors.As(err, &ue) {
			t.Fatalf("args %v: help=%v err=%v, want usageError", args, help, err)
		}
	}
	port, domain, help, err := parseFillLoginArgs([]string{"--port", "9011", "https://www.YouTube.com/login"})
	if err != nil || help || port != 9011 || domain != "www.youtube.com" {
		t.Fatalf("port=%d domain=%q help=%v err=%v", port, domain, help, err)
	}
}

func TestHostMatchesDomain(t *testing.T) {
	cases := []struct {
		host, domain string
		want         bool
	}{
		{"youtube.com", "youtube.com", true},
		{"www.youtube.com", "youtube.com", true},
		{"accounts.google.com", "google.com", true},
		{"notyoutube.com", "youtube.com", false},
		{"youtube.com.evil.com", "youtube.com", false},
		{"", "youtube.com", false},
		{"youtube.com", "", false},
	}
	for _, tc := range cases {
		if got := hostMatchesDomain(tc.host, tc.domain); got != tc.want {
			t.Errorf("hostMatchesDomain(%q, %q)=%v, want %v", tc.host, tc.domain, got, tc.want)
		}
	}
}

// TestBuildFillCommandRoundTrip: the credential text must survive the
// engine's command parser byte-for-byte, however hostile its characters.
func TestBuildFillCommandRoundTrip(t *testing.T) {
	text := `p"ass\word with spaces & 'quotes'`
	line := buildFillCommand("#login-pass", text)
	cmds, err := engine.ParseCommands(line)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Name != "fill" || len(cmds[0].Args) != 2 {
		t.Fatalf("cmds=%+v", cmds)
	}
	if cmds[0].Args[0] != "#login-pass" || cmds[0].Args[1] != text {
		t.Fatalf("args=%q", cmds[0].Args)
	}
}

func TestBuildLocateCommandParses(t *testing.T) {
	cmds, err := engine.ParseCommands(buildLocateCommand())
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Name != "eval" || len(cmds[0].Args) != 1 {
		t.Fatalf("cmds=%+v", cmds)
	}
	if !strings.Contains(cmds[0].Args[0], "password") || !strings.Contains(cmds[0].Args[0], "location.hostname") {
		t.Fatalf("el script de localización perdió contenido: %.80q", cmds[0].Args[0])
	}
}
