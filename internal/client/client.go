// Package client is the thin CLI client: find the daemon via daemon.json,
// start it if missing, and call tools/call over its /client WebSocket.
package client

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
