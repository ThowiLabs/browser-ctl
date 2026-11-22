// Package protocol implements the wire protocol between the browser-ctl
// relay (this CLI) and the Browser MCP Chrome extension.
//
// The extension is a WebSocket CLIENT: it probes GET http://127.0.0.1:<port>/browsermcp/status
// on RELAYPorts in order, then opens ws://127.0.0.1:<port>/extension and sends a hello frame
// with a random challenge. We answer with a welcome frame carrying our ECDSA P-256 public key
// and a signature of the challenge. Every frame is one JSON object.
package protocol

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the protocol version we speak. The extension compares it against its own.
const ProtocolVersion = 1

// MinProtocolVersion is the oldest protocol version we accept from the extension.
const MinProtocolVersion = 1

// Product is what /browsermcp/status reports, so the extension recognizes us.
const Product = "browsermcp"

// LegacyPort is the port of the 0.1.x servers; nothing of ours listens there.
const LegacyPort = 9009

// RelayPorts are the ports the relay binds (first free) and the extension tries, in order.
var RelayPorts = []int{9010, 9011, 9012}

// Extension IDs accepted on /extension (checked against the WebSocket Origin header).
const (
	// ChromeWebStoreExtensionID is the ID of the Browser MCP extension in the Chrome Web Store.
	ChromeWebStoreExtensionID = "bjfgambnhccakkhmkepdoekmckoijdlc"
	// SelfBuiltExtensionID is the ID of the extension built from source (fixed manifest key).
	SelfBuiltExtensionID = "imlplcjlhgpajopdbpbngiadkcmdbgob"
)

// Close codes we may send on the WebSocket.
const (
	CloseUnsupportedProtocol = 4001
	CloseAnotherBrowser      = 4002
	CloseShuttingDown        = 4003
	CloseExpectedHello       = 1008
)

// Update reasons sent with CloseUnsupportedProtocol.
const (
	UpdateExtension = "update-extension"
	UpdateServer    = "update-server"
)

// Keepalive: we ping every PingIntervalMs and close the connection if no pong in PingTimeoutMs.
const (
	PingIntervalMs = 20000
	PingTimeoutMs  = 10000
)

// HelloTimeoutMs is how long we wait for the extension's hello after the WS opens.
const HelloTimeoutMs = 10000

// RequestTimeoutMs is how long a request waits for the extension's answer.
const RequestTimeoutMs = 30000

// CDPRequestTimeoutMs is the timeout for cdp requests (CDP commands can be long).
const CDPRequestTimeoutMs = 300000

// PathStatus is the HTTP endpoint the extension probes.
const PathStatus = "/browsermcp/status"

// PathExtension is the WebSocket path the extension connects to.
const PathExtension = "/extension"

// MaxBrowsers is how many browsers the relay accepts at once; the rest are refused
// with CloseAnotherBrowser.
const MaxBrowsers = 8

// PausedMessage is what the extension answers when the user pressed Stop.
const PausedMessage = "The user paused Browser MCP. Ask them to click Resume in its popup"

// PairingPayload is the string the daemon signs during the handshake. It names the port we
// listen on and the extension's challenge, so a MITM on another port can't replay a signature.
func PairingPayload(challenge string, port int, pairingCommitment string) string {
	return fmt.Sprintf("%s-pair-v1:%d:%s:%s", Product, port, challenge, pairingCommitment)
}

// CreatePairingNonce returns a fresh nonce: base64 of 32 random bytes.
func CreatePairingNonce() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// PairingCommitment is the base64 SHA-256 of the daemon nonce (decoded from base64).
func PairingCommitment(daemonNonce string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(daemonNonce)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

// PairingCode computes the six-digit pairing code ("NNN NNN") from the daemon's public key
// (base64 SPKI) and both nonces, exactly as the extension does.
func PairingCode(publicKey, extensionNonce, daemonNonce string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s-pair-code-v1:%s:%s:%s", Product, publicKey, extensionNonce, daemonNonce)))
	digits := fmt.Sprintf("%06d", binary.BigEndian.Uint32(digest[:4])%1000000)
	return digits[:3] + " " + digits[3:]
}

// NoExtensionMessage is what commands answer while no extension is connected.
const NoExtensionMessage = "No browser is connected. Install the Browser MCP extension, or update it to 2.0: open chrome://extensions, turn on Developer mode and click Update"

// --- Frames the extension sends ---

// BrowserInfo describes the browser the extension runs in.
type BrowserInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	UserAgent string `json:"userAgent"`
}

// ExposedTab is a tab agents may use.
type ExposedTab struct {
	TabID    int    `json:"tabId"`
	WindowID int    `json:"windowId"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Active   bool   `json:"active"`
	Pinned   bool   `json:"pinned"`
	AppID    string `json:"appId,omitempty"`
	Attached bool   `json:"attached"`
}

// ExtensionState is the state the extension reports in hello and state events.
type ExtensionState struct {
	Tabs      []ExposedTab `json:"tabs"`
	Paused    bool         `json:"paused"`
	Pairing   string       `json:"pairing"` // "paired" | "pending"
	Label     string       `json:"label,omitempty"`
	FocusedAt *int64       `json:"lastFocusedAt,omitempty"`
}

// Hello is the first frame the extension sends.
type Hello struct {
	Type             string         `json:"type"` // "hello"
	Protocol         int            `json:"protocol"`
	ExtensionVersion string         `json:"extensionVersion"`
	Browser          BrowserInfo    `json:"browser"`
	BrowserID        string         `json:"browserId,omitempty"`
	Capabilities     []string       `json:"capabilities"`
	Challenge        string         `json:"challenge"`
	State            ExtensionState `json:"state"`
}

// Response is the extension's answer to one of our requests.
type Response struct {
	Type   string          `json:"type"` // "response"
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Event is an event from the extension.
type Event struct {
	Type  string       `json:"type"` // "event"
	Event EventPayload `json:"event"`
}

// EventPayload is the event body.
type EventPayload struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// CDPEvent is a CDP event the extension relays from an attached tab (or a child
// session, e.g. an out-of-process iframe, carried in SessionID).
type CDPEvent struct {
	TabID     int             `json:"tabId"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// Detached is emitted when the browser detaches the debugger without the daemon
// asking (reasons: "canceled_by_user", "paused", "not_allowed", or a browser reason).
type Detached struct {
	TabID  int    `json:"tabId"`
	Reason string `json:"reason"`
}

// TabOpened is emitted when a page opens a tab (target=_blank / window.open).
type TabOpened struct {
	TabID       int    `json:"tabId"`
	OpenerTabID int    `json:"openerTabId"`
	URL         string `json:"url"`
}

// TabAssigned is emitted when the user drags a tab into or out of an app's group.
type TabAssigned struct {
	TabID int    `json:"tabId"`
	AppID string `json:"appId"`
}

// Pong is the extension's answer to our ping.
type Pong struct {
	Type string `json:"type"` // "pong"
	ID   int    `json:"id"`
}

// PairingNonce is sent by the extension only for a key it hasn't paired with.
type PairingNonce struct {
	Type  string `json:"type"` // "pairingNonce"
	Nonce string `json:"nonce"`
}

// ExtensionMessage is any frame from the extension.
type ExtensionMessage struct {
	Type string `json:"type"`
}

// --- Frames we send ---

// Welcome is our answer to hello.
type Welcome struct {
	Type              string   `json:"type"` // "welcome"
	Protocol          int      `json:"protocol"`
	DaemonVersion     string   `json:"daemonVersion"`
	Capabilities      []string `json:"capabilities"`
	PublicKey         string   `json:"publicKey"`
	PairingCommitment string   `json:"pairingCommitment"`
	Signature         string   `json:"signature"`
}

// PairingReveal answers the extension's pairingNonce.
type PairingReveal struct {
	Type  string `json:"type"` // "pairingReveal"
	Nonce string `json:"nonce"`
}

// Request is a command we send to the extension.
type Request struct {
	Type   string          `json:"type"` // "request"
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Ping keeps the connection alive.
type Ping struct {
	Type string `json:"type"` // "ping"
	ID   int    `json:"id"`
}

// Request params/results (daemon -> extension).

type AttachParams struct {
	TabID int `json:"tabId"`
}

type AttachResult struct {
	TargetInfo map[string]any `json:"targetInfo"`
}

type CDPParams struct {
	TabID     int             `json:"tabId"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type CreateTabParams struct {
	URL string  `json:"url,omitempty"`
	App AppInfo `json:"app"`
}

type CreateTabResult struct {
	TabID int `json:"tabId"`
}

type CloseTabParams struct {
	TabID int `json:"tabId"`
}

type ActivateTabParams struct {
	TabID int `json:"tabId"`
}

type DetachParams struct {
	TabID int `json:"tabId"`
}

type GroupTabParams struct {
	TabID int      `json:"tabId"`
	App   *AppInfo `json:"app"`
}

// AppInfo names our app for tab grouping.
type AppInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
