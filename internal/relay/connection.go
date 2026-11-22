package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/thovilabs/browser-ctl/internal/protocol"
)

// HelloTimeout is how long Accept waits for the extension's hello.
const HelloTimeout = protocol.HelloTimeoutMs * time.Millisecond

type pendingRequest struct {
	method string
	ch     chan responseResult
}

type responseResult struct {
	err    error
	result json.RawMessage
}

// Connection is one extension WebSocket after hello/welcome: requests to the extension,
// its state, and a ping every 20s that closes a connection whose service worker stopped answering.
type Connection struct {
	ws    *websocket.Conn
	hello *protocol.Hello
	port  int
	key   *PairingKey

	mu          sync.Mutex
	closed      bool
	nextID      int
	pending     map[int]*pendingRequest
	pongChans   map[int]chan bool
	state       protocol.ExtensionState
	pairingCode string
	daemonNonce string
	daemonVersion string

	onState  func(protocol.ExtensionState)
	onClose  func(code int, reason string)
}

// Accept waits for the extension's hello, checks the protocol and answers welcome with our
// key, a commitment to a fresh nonce and our signature of both with the challenge.
func Accept(ws *websocket.Conn, port int, key *PairingKey, version string,
	onState func(protocol.ExtensionState), onClose func(int, string),
) (*Connection, error) {
	msg, err := readFrame(ws)
	if err != nil {
		ws.Close()
		return nil, fmt.Errorf("waiting for hello: %w", err)
	}
	var hello protocol.Hello
	if err := json.Unmarshal(msg, &hello); err != nil || hello.Type != "hello" || hello.Challenge == "" {
		closeCode(ws, protocol.CloseExpectedHello, "Expected hello")
		return nil, errors.New("the extension didn't send hello")
	}
	if hello.Protocol < protocol.MinProtocolVersion {
		closeCode(ws, protocol.CloseUnsupportedProtocol, protocol.UpdateExtension)
		return nil, fmt.Errorf("extension protocol %d is no longer supported", hello.Protocol)
	}

	daemonNonce, err := protocol.CreatePairingNonce()
	if err != nil {
		return nil, err
	}
	commitment, err := protocol.PairingCommitment(daemonNonce)
	if err != nil {
		return nil, err
	}
	sig, err := key.sign(protocol.PairingPayload(hello.Challenge, port, commitment))
	if err != nil {
		return nil, err
	}

	welcome := protocol.Welcome{
		Type:              "welcome",
		Protocol:          protocol.ProtocolVersion,
		DaemonVersion:     version,
		Capabilities:      []string{},
		PublicKey:         key.PublicKey,
		PairingCommitment: commitment,
		Signature:         sig,
	}
	if os.Getenv("BROWSER_CTL_NOSIG") != "" {
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"welcome","protocol":1,"daemonVersion":%q,"capabilities":[],"publicKey":%q,"pairingCommitment":%q,"signature":null}`, version, key.PublicKey, commitment)))
		fmt.Fprintf(os.Stderr, "DBG welcome (no signature) sent\n")
	} else if err := sendJSON(ws, welcome); err != nil {
		ws.Close()
		return nil, err
	}

	c := &Connection{
		ws:            ws,
		hello:         &hello,
		port:          port,
		key:           key,
		nextID:        1,
		pending:       map[int]*pendingRequest{},
		pongChans:     map[int]chan bool{},
		state:         hello.State,
		daemonNonce:   daemonNonce,
		daemonVersion: version,
		onState:       onState,
		onClose:       onClose,
	}
	go c.readLoop()
	go c.pingLoop()
	return c, nil
}

func (c *Connection) Hello() *protocol.Hello { return c.hello }

func (c *Connection) State() protocol.ExtensionState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Request sends a request to the extension and waits for its response.
func (c *Connection) Request(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("the browser disconnected")
	}
	id := c.nextID
	c.nextID++
	pr := &pendingRequest{method: method, ch: make(chan responseResult, 1)}
	c.pending[id] = pr
	c.mu.Unlock()

	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if err := sendJSON(c.ws, protocol.Request{Type: "request", ID: id, Method: method, Params: rawParams}); err != nil {
		c.failPending(id, err)
		return nil, err
	}

	timeout := protocol.RequestTimeoutMs
	if method == "cdp" {
		timeout = protocol.CDPRequestTimeoutMs
	}
	select {
	case res := <-pr.ch:
		return res.result, res.err
	case <-time.After(time.Duration(timeout) * time.Millisecond):
		c.failPending(id, fmt.Errorf("the browser didn't answer %s in time", method))
		return nil, fmt.Errorf("the browser didn't answer %s in time", method)
	}
}

func (c *Connection) failPending(id int, err error) {
	c.mu.Lock()
	pr := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if pr != nil {
		pr.ch <- responseResult{err: err}
	}
}

// Close closes the WebSocket with the given code and reason.
func (c *Connection) Close(code int, reason string) {
	closeCode(c.ws, code, reason)
	c.handleClose(code, reason)
}

func (c *Connection) handleClose(code int, reason string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = map[int]*pendingRequest{}
	pongs := c.pongChans
	c.pongChans = map[int]chan bool{}
	c.mu.Unlock()

	disconnectErr := errors.New("the browser disconnected while handling the command")
	for _, pr := range pending {
		pr.ch <- responseResult{err: disconnectErr}
	}
	for _, ch := range pongs {
		ch <- false
	}
	if c.onClose != nil {
		c.onClose(code, reason)
	}
}

func (c *Connection) readLoop() {
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			c.handleClose(1006, "connection closed")
			return
		}
		var frame map[string]json.RawMessage
		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}
		c.handleFrame(frame)
	}
}

func (c *Connection) handleFrame(frame map[string]json.RawMessage) {
	var typ string
	_ = json.Unmarshal(frame["type"], &typ)
	switch typ {
	case "response":
		var r protocol.Response
		if err := json.Unmarshal(mustMarshal(frame), &r); err != nil {
			return
		}
		c.mu.Lock()
		pr := c.pending[r.ID]
		delete(c.pending, r.ID)
		c.mu.Unlock()
		if pr == nil {
			return
		}
		if r.Error != "" {
			pr.ch <- responseResult{err: errors.New(r.Error)}
		} else {
			pr.ch <- responseResult{result: r.Result}
		}
	case "event":
		var e protocol.Event
		if err := json.Unmarshal(mustMarshal(frame), &e); err != nil {
			return
		}
		if e.Event.Type == "state" {
			var state protocol.ExtensionState
			if err := json.Unmarshal(e.Event.Data, &state); err == nil {
				c.mu.Lock()
				c.state = state
				c.mu.Unlock()
				if c.onState != nil {
					c.onState(state)
				}
			}
		}
	case "pong":
		var p protocol.Pong
		if err := json.Unmarshal(mustMarshal(frame), &p); err != nil {
			return
		}
		c.mu.Lock()
		ch := c.pongChans[p.ID]
		delete(c.pongChans, p.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- true
		}
	case "pairingNonce":
		var pn protocol.PairingNonce
		if err := json.Unmarshal(mustMarshal(frame), &pn); err != nil {
			return
		}
		// Answer the reveal and compute the pairing code to show the user.
		c.mu.Lock()
		c.pairingCode = protocol.PairingCode(c.key.PublicKey, pn.Nonce, c.daemonNonce)
		c.mu.Unlock()
		sendJSON(c.ws, protocol.PairingReveal{Type: "pairingReveal", Nonce: c.daemonNonce})
	}
}

func (c *Connection) pingLoop() {
	ticker := time.NewTicker(time.Duration(protocol.PingIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		id := c.nextID
		c.nextID++
		ch := make(chan bool, 1)
		c.pongChans[id] = ch
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		if err := sendJSON(c.ws, protocol.Ping{Type: "ping", ID: id}); err != nil {
			c.handleClose(1006, "ping failed")
			return
		}
		select {
		case <-ch:
		case <-time.After(time.Duration(protocol.PingTimeoutMs) * time.Millisecond):
			c.handleClose(1006, "the extension stopped answering pings")
			return
		}
	}
}

func readFrame(ws *websocket.Conn) (json.RawMessage, error) {
	_, data, err := ws.ReadMessage()
	return data, err
}

func sendJSON(ws *websocket.Conn, v any) error {
	return ws.WriteJSON(v)
}

func closeCode(ws *websocket.Conn, code int, reason string) {
	ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(3*time.Second))
	ws.Close()
}

func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
