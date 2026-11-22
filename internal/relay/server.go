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

	// Events is an opaque hook the engine uses to receive cdpEvent / detached
	// events for this browser (set to a *cdp.Registry by the engine).
	Events any
}

// Server is the HTTP + WebSocket server the extension connects to.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
	port       int
	key        *PairingKey
	version    string
	logf       func(string, ...any)

	mu        sync.Mutex
	browsers  map[string]*Browser
	byProfile map[string]string

	// Optional handlers set by the daemon (client WS + shutdown).
	onClient   func(w http.ResponseWriter, r *http.Request)
	onShutdown func(w http.ResponseWriter, r *http.Request)
}

// SetClientHandler registers the /client WebSocket handler (daemon mode).
func (s *Server) SetClientHandler(fn func(w http.ResponseWriter, r *http.Request)) {
	s.mu.Lock()
	s.onClient = fn
	s.mu.Unlock()
}

// SetShutdownHandler registers the POST /shutdown handler (daemon mode).
func (s *Server) SetShutdownHandler(fn func(w http.ResponseWriter, r *http.Request)) {
	s.mu.Lock()
	s.onShutdown = fn
	s.mu.Unlock()
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
		mux.HandleFunc("/client", func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			fn := s.onClient
			s.mu.Unlock()
			if fn == nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			fn(w, r)
		})
		mux.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			fn := s.onShutdown
			s.mu.Unlock()
			if fn == nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			fn(w, r)
		})
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
	// Enforce the browser cap before the handshake so an 8th browser is refused
	// with the proper close code.
	s.mu.Lock()
	if len(s.browsers) >= protocol.MaxBrowsers {
		s.mu.Unlock()
		closeCode(ws, protocol.CloseAnotherBrowser, "another browser")
		s.logf("refused a %dth browser (max %d)", protocol.MaxBrowsers+1, protocol.MaxBrowsers)
		return
	}
	s.mu.Unlock()

	var registered *Browser
	conn, err := Accept(ws, s.port, s.key, s.version,
		func(state protocol.ExtensionState) {
			if state.Paused {
				s.logf("browser paused (Stop pressed)")
			}
		},
		func(code int, reason string) {
			if registered != nil {
				s.mu.Lock()
				delete(s.browsers, registered.Profile)
				delete(s.byProfile, registered.Profile)
				s.mu.Unlock()
				s.logf("extension disconnected: %d %s", code, reason)
			}
		},
		func(tabID int, sessionID, method string, params json.RawMessage) {
			if registered != nil {
				if r, ok := registered.Events.(interface {
					Dispatch(int, string, string, json.RawMessage)
				}); ok {
					r.Dispatch(tabID, sessionID, method, params)
				}
			}
		},
		func(tabID int, reason string) {
			if registered != nil {
				if r, ok := registered.Events.(interface {
					DispatchDetached(int, string)
				}); ok {
					r.DispatchDetached(tabID, reason)
				}
			}
		},
	)
	if err != nil {
		s.logf("extension handshake failed: %v", err)
		return
	}
	hello := conn.Hello()
	profile := hello.BrowserID
	if profile == "" || !validBrowserID(profile) {
		profile = hello.Browser.UserAgent
	}
	b := &Browser{Conn: conn, BrowserID: hello.BrowserID, Profile: profile}
	registered = b
	s.mu.Lock()
	s.browsers[profile] = b
	s.byProfile[profile] = profile
	s.mu.Unlock()
	s.logf("extension connected: %s (profile %s, pairing %s)", hello.Browser.Name, profile, hello.State.Pairing)
}

// validBrowserID mirrors the extension's check (22 chars of [\w-]).
var browserIDRe = regexp.MustCompile(`^[\w-]{22}$`)

func validBrowserID(id string) bool { return browserIDRe.MatchString(id) }

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
