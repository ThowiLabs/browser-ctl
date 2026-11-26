// Package client is the thin CLI client: find the daemon via daemon.json,
// start it if missing, and call tools/call over its /client WebSocket.
package client

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/thovilabs/browser-ctl/internal/daemon"
	"github.com/thovilabs/browser-ctl/internal/mcp"
)

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// FindOrStart returns a connected daemon client, starting the daemon
// (detached) if none is running.
func FindOrStart(port int, caps []string) (*mcp.Client, *daemon.DaemonInfo, error) {
	home := daemon.HomeDir()
	if info := daemon.FindDaemon(home); info != nil && (port == 0 || info.Port == port) {
		c, err := mcp.DialSession(info.Port, info.Token, "browser-ctl-cli", "cli")
		if err == nil {
			return c, info, nil
		}
		// daemon.json's daemon answers status but won't take a client
		// session: it is wedged, or something else owns the port.
		// Starting a second daemon would bind the next port and never
		// see the browser, so name it instead of hanging.
		return nil, nil, fmt.Errorf("the daemon on 127.0.0.1:%d (pid %d, from %s) answers status but the client session could not be opened — stop it with `browser-ctl shutdown` and retry", info.Port, info.Pid, filepath.Join(home, "daemon.json"))
	}
	// No usable daemon.json. A browsermcp server may still answer on a
	// relay port — a legacy `serve` relay, or a daemon from another
	// home: the extension connects to the first answering port, so a
	// new daemon on the next port would never get the browser and every
	// command would fail as \"no browser connected\". Refuse and name
	// the port. An explicit --port narrows the probe to that port.
	if p, st := daemon.ProbePorts(port); st != nil {
		return nil, nil, fmt.Errorf("a browsermcp server (pid %d) is already listening on 127.0.0.1:%d but %s does not describe a usable daemon (legacy `serve` relay, or another home's daemon) — stop that server first, or pass --port for a different port", st.Pid, p, filepath.Join(home, "daemon.json"))
	}
	// Start the daemon detached.
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	args := []string{"daemon"}
	if port != 0 {
		args = append(args, "--port", fmt.Sprint(port))
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Detached, so killing the parent never takes the daemon down.
	// Platform-specific: Setsid on Unix, DETACHED_PROCESS on Windows.
	cmd.SysProcAttr = detachedAttr()
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	// Poll for daemon.json, up to 15 s.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if info := daemon.FindDaemon(home); info != nil {
			c, err := mcp.DialSession(info.Port, info.Token, "browser-ctl-cli", "cli")
			if err == nil {
				return c, info, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, nil, fmt.Errorf("could not start the daemon")
}

// RunCommands runs one batch through the daemon and returns the output.
func RunCommands(port int, commands string, caps []string) (string, error) {
	c, _, err := FindOrStart(port, caps)
	if err != nil {
		return "", err
	}
	defer c.Close()
	raw, err := c.Call("tools/call", map[string]any{
		"name":         "browser",
		"arguments":    map[string]any{"commands": commands},
		"capabilities": caps,
	})
	if err != nil {
		return "", err
	}
	// Unwrap the MCP result envelope: the CLI prints plain text, not JSON.
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := jsonUnmarshal(raw, &res); err != nil {
		return string(raw), nil
	}
	var out string
	for _, c := range res.Content {
		out += c.Text
	}
	if res.IsError {
		return out, fmt.Errorf("%s", out)
	}
	return out, nil
}

// Shutdown asks the daemon to stop.
func Shutdown(port int) error {
	home := daemon.HomeDir()
	info := daemon.FindDaemon(home)
	if info == nil {
		return fmt.Errorf("no daemon is running")
	}
	if port != 0 {
		info.Port = port
	}
	return daemon.Shutdown(info.Port, info.Token)
}
