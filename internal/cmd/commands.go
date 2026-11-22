package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/thovilabs/browser-ctl/internal/protocol"
	"github.com/thovilabs/browser-ctl/internal/relay"
)

const version = "0.1.0"

// HomeDir is where browser-ctl keeps its identity (~/.browser-ctl).
func HomeDir() string {
	if h := os.Getenv("BROWSER_CTL_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".browser-ctl")
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// RunServe starts the relay. If wait is true it waits for a browser to connect and
// returns (closing the server); if false it runs as a long-lived daemon until a
// termination signal arrives.
func RunServe(port int, wait bool) error {
	key, err := relay.LoadIdentity(HomeDir())
	if err != nil {
		return err
	}
	server, boundPort, err := relay.StartServer(port, key, version, logf)
	if err != nil {
		return err
	}
	logf("browser-ctl %s relay listening on 127.0.0.1:%d", version, boundPort)

	if !wait {
		logf("daemon running (Ctrl+C to stop)")
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		server.Close()
		return nil
	}

	defer server.Close()
	logf("waiting for the Browser MCP extension to connect...")
	for {
		time.Sleep(500 * time.Millisecond)
		if b := server.FirstBrowser(); b != nil {
			state := b.Conn.State()
			logf("browser connected: %s (pairing %s)", state.Pairing, firstTab(state))
			return nil
		}
	}
}

func firstTab(state protocol.ExtensionState) string {
	if len(state.Tabs) == 0 {
		return "no tabs"
	}
	return fmt.Sprintf("%d tab(s)", len(state.Tabs))
}

// RunExec runs a batch of commands (one per line) and prints the output.
func RunExec(commands string, port int) error {
	key, err := relay.LoadIdentity(HomeDir())
	if err != nil {
		return err
	}
	server, boundPort, err := relay.StartServer(port, key, version, logf)
	if err != nil {
		return err
	}
	defer server.Close()
	logf("relay on 127.0.0.1:%d", boundPort)

	var browser *relay.Browser
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if browser = server.FirstBrowser(); browser != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if browser == nil {
		return fmt.Errorf("%s\nIs the Browser MCP extension installed and enabled? Start it or run: browser-ctl serve", protocol.NoExtensionMessage)
	}
	state := browser.Conn.State()
	if state.Paused {
		return fmt.Errorf("the browser is paused (press Resume in the extension's popup)")
	}
	if state.Pairing != "paired" {
		return fmt.Errorf("not paired: click Allow in the extension's popup (the relay is on 127.0.0.1:%d)", boundPort)
	}

	engine := NewEngine(browser, logf)
	out, err := engine.Run(commands)
	if out != "" {
		fmt.Println(out)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		os.Exit(1)
	}
	return nil
}

// RunStatus prints the relay identity and the ports it will try.
func RunStatus() {
	key, err := relay.LoadIdentity(HomeDir())
	if err != nil {
		logf("error: %v", err)
		return
	}
	logf("home: %s", HomeDir())
	logf("public key: %s", key.PublicKey)
	logf("relay ports: %v (the first free one is used)", protocol.RelayPorts)
}

// RunTabs starts the relay, waits for a browser and lists its tabs.
func RunTabs(port int) error {
	key, err := relay.LoadIdentity(HomeDir())
	if err != nil {
		return err
	}
	server, _, err := relay.StartServer(port, key, version, logf)
	if err != nil {
		return err
	}
	defer server.Close()
	var browser *relay.Browser
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if browser = server.FirstBrowser(); browser != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if browser == nil {
		return fmt.Errorf("%s", protocol.NoExtensionMessage)
	}
	state := browser.Conn.State()
	if len(state.Tabs) == 0 {
		fmt.Println("(no tabs)")
		return nil
	}
	for _, t := range state.Tabs {
		fmt.Printf("%d %s %s\n", t.TabID, truncate(t.Title, 50), t.URL)
	}
	return nil
}
