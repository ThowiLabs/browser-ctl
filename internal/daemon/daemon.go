// Package daemon owns the long-lived process: the relay the extension
// connects to, the in-memory sessions, and daemon.json discovery so
// clients (CLI, MCP thin) can find and reuse it.
package daemon

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/thovilabs/browser-ctl/internal/protocol"
)

const version = "2.0.0"

// Version is the daemon version reported in /browsermcp/status and welcome.
func Version() string { return version }

// HomeDir is where the daemon keeps identity.json / daemon.json.
// BROWSER_CTL_HOME overrides; default ~/.browser-ctl (v2 home, so v1 and
// the JS daemon can coexist).
func HomeDir() string {
	if h := os.Getenv("BROWSER_CTL_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".browser-ctl")
}

// DaemonInfo is ~/.browser-ctl/daemon.json: how clients find the daemon.
type DaemonInfo struct {
	Port    int    `json:"port"`
	Pid     int    `json:"pid"`
	Version string `json:"version"`
	Token   string `json:"token"`
}

func infoPath(home string) string { return filepath.Join(home, "daemon.json") }

// Status is what GET /browsermcp/status returns.
type Status struct {
	Product     string `json:"product"`
	Version     string `json:"version"`
	Protocol    int    `json:"protocol"`
	MinProtocol int    `json:"minProtocol"`
	Pid         int    `json:"pid"`
}

// FetchStatus probes a port like the extension does. Returns nil if the
// port does not answer with a browsermcp status.
func FetchStatus(port int) *Status {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/browsermcp/status", port))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var st Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil
	}
	if st.Product != "browsermcp" {
		return nil
	}
	return &st
}

// ReadInfo reads daemon.json, validating its shape.
func ReadInfo(home string) (*DaemonInfo, error) {
	data, err := os.ReadFile(infoPath(home))
	if err != nil {
		return nil, err
	}
	var info DaemonInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	if info.Port == 0 || info.Token == "" {
		return nil, fmt.Errorf("daemon.json is incomplete")
	}
	return &info, nil
}

// FindDaemon returns the running daemon's info if daemon.json names a
// live process whose status pid matches, else nil.
func FindDaemon(home string) *DaemonInfo {
	info, err := ReadInfo(home)
	if err != nil {
		return nil
	}
	st := FetchStatus(info.Port)
	if st == nil || st.Pid != info.Pid {
		return nil
	}
	return info
}

// ProbePorts looks for a live browsermcp server on the well-known
// relay ports: only `only` when it is non-zero, else RelayPorts in
// order. It returns the answering port and its status, or (0, nil)
// when nothing answers. An answering port is not by itself a usable
// daemon — a daemon is only drivable through daemon.json, which
// holds its client token — but an answering port with no usable
// daemon.json (a legacy `serve` relay, another home's daemon) means
// a new daemon would bind the NEXT port while the extension stays
// with the first one it found, so callers name the conflict instead
// of starting a second server that can never reach the browser.
func ProbePorts(only int) (int, *Status) {
	ports := protocol.RelayPorts
	if only != 0 {
		ports = []int{only}
	}
	for _, port := range ports {
		if st := FetchStatus(port); st != nil {
			return port, st
		}
	}
	return 0, nil
}

// NewToken returns a fresh per-boot token (base64url of 32 random bytes).
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeInfo(home string, info *DaemonInfo) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", infoPath(home), os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, infoPath(home)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// RemoveInfo deletes daemon.json, but only if it still names this pid.
func RemoveInfo(home string, pid int) {
	info, err := ReadInfo(home)
	if err != nil || info.Pid != pid {
		return
	}
	os.Remove(infoPath(home))
}

// IsPortAnswering reports whether something accepts TCP on a port.
func IsPortAnswering(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Pid returns the current process pid.
func Pid() int { return syscall.Getpid() }

// Shutdown POSTs /shutdown?token= to a daemon.
func Shutdown(port int, token string) error {
	client := &http.Client{Timeout: 70 * time.Second}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/shutdown?token=%s", port, token), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shutdown failed: %s", resp.Status)
	}
	return nil
}
