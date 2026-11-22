package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/thovilabs/browser-ctl/internal/protocol"
)

var allowedExtensionIDs = map[string]bool{
	protocol.ChromeWebStoreExtensionID: true,
	protocol.SelfBuiltExtensionID:      true,
}

var extensionOriginRe = regexp.MustCompile(`^chrome-extension://([a-p]{32})$`)

var allowedHostnames = map[string]bool{"127.0.0.1": true, "localhost": true, "[::1]": true}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// Browser is a connected extension (one browser profile).
type Browser struct {
	Conn      *Connection
	BrowserID string
	Profile   string
}

// Server is the HTTP + WebSocket server the extension connects to.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
	port       int
	key        *PairingKey
	version    string
	logf       func(string, ...any)

	mu          sync.Mutex
	browsers    map[string]*Browser
	byProfile   map[string]string
}

// StartServer listens on the first free port of RelayPorts (or a fixed one if port != 0).
func StartServer(port int, key *PairingKey, version string, logf func(string, ...any)) (*Server, int, error) {
	ports := protocol.RelayPorts
	if port != 0 {
		ports = []int{port}
	}
	var lastErr error
	for _, p := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			lastErr = err
			continue
		}
		s := &Server{
			key:       key,
			version:   version,
			logf:      logf,
			port:      p,
			browsers:  map[string]*Browser{},
			byProfile: map[string]string{},
			listener:  ln,
		}
		mux := http.NewServeMux()
		mux.HandleFunc(protocol.PathStatus, s.handleStatus)
		mux.HandleFunc(protocol.PathExtension, s.handleExtensionWS)
		s.httpServer = &http.Server{Handler: mux}
		go func() {
			if err := s.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logf("relay server error: %v", err)
			}
		}()
		return s, p, nil
	}
	return nil, 0, fmt.Errorf("no free relay port: %w", lastErr)
}

// Port returns the port the server is listening on.
func (s *Server) Port() int { return s.port }

// Close shuts the server down, closing every extension connection.
func (s *Server) Close() {
	s.mu.Lock()
	browsers := make([]*Browser, 0, len(s.browsers))
	for _, b := range s.browsers {
		browsers = append(browsers, b)
	}
	s.browsers = map[string]*Browser{}
	s.mu.Unlock()
	for _, b := range browsers {
		b.Conn.Close(protocol.CloseShuttingDown, "shutting down")
	}
	s.httpServer.Close()
}

// Browsers returns the connected browsers.
func (s *Server) Browsers() map[string]*Browser {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*Browser, len(s.browsers))
	for k, v := range s.browsers {
		out[k] = v
	}
	return out
}

// FirstBrowser returns any connected browser, for single-profile setups.
func (s *Server) FirstBrowser() *Browser {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.browsers {
		return b
	}
	return nil
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !isAllowedHost(r.Host) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "chrome-extension://") {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	status := map[string]any{
		"product":     protocol.Product,
		"version":     s.version,
		"protocol":    protocol.ProtocolVersion,
		"minProtocol": protocol.MinProtocolVersion,
		"pid":         syscall.Getpid(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleExtensionWS(w http.ResponseWriter, r *http.Request) {
	if !isAllowedHost(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	origin := r.Header.Get("Origin")
	m := extensionOriginRe.FindStringSubmatch(origin)
	if m == nil || !allowedExtensionIDs[m[1]] {
		s.logf("rejected extension connection from %q", origin)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.handleExtensionConn(ws)
}

func (s *Server) handleExtensionConn(ws *websocket.Conn) {
	var registered string
	conn, err := Accept(ws, s.port, s.key, s.version,
		func(state protocol.ExtensionState) {
			if state.Paused {
				s.logf("browser paused (Stop pressed)")
			}
		},
		func(code int, reason string) {
			if registered != "" {
				s.mu.Lock()
				delete(s.browsers, registered)
				delete(s.byProfile, registered)
				s.mu.Unlock()
				s.logf("extension disconnected: %d %s", code, reason)
			}
		},
	)
	if err != nil {
		s.logf("extension handshake failed: %v", err)
		return
	}
	hello := conn.Hello()
	profile := hello.BrowserID
	if profile == "" {
		profile = hello.Browser.UserAgent
	}
	registered = profile
	s.mu.Lock()
	s.browsers[profile] = &Browser{Conn: conn, BrowserID: hello.BrowserID, Profile: profile}
	s.byProfile[profile] = profile
	s.mu.Unlock()
	s.logf("extension connected: %s (profile %s, pairing %s)", hello.Browser.Name, profile, hello.State.Pairing)
}

// isAllowedHost rejects other Host headers against DNS rebinding.
func isAllowedHost(host string) bool {
	if host == "" {
		return false
	}
	hostname := strings.ToLower(host)
	if i := strings.LastIndex(hostname, ":"); i > 0 {
		hostname = hostname[:i]
	}
	return allowedHostnames[hostname]
}
