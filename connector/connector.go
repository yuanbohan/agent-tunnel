package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/session"
)

// Connector is the one place where the local runtime meets the relay protocol.
//
// Upstream producers feeding this object:
// - session.Hub output fanout via WriteOutput()
// - session.Hub resize callback via BindHub()
// - codexapp.MonitorActionRequired via UpdateSessionState()
//
// Downstream consumer:
// - relay `/agent/ws`, which receives register/output/resize/session_state frames
//
// Reverse data path:
// - relay input frames come back through handleMessage() into the bound Hub
type Connector struct {
	url   string
	token string
	// info is the session snapshot sent in the initial register frame for each
	// websocket connection attempt.
	info protocol.SessionInfo
	// hub is bound after the PTY session has been created. It is required for the
	// inbound path from relay input frames back into PTY stdin.
	hub *session.Hub

	// outbound multiplexes three logical sources onto a single websocket write loop:
	// terminal output, resize updates, and structured session-state changes.
	outbound chan protocol.Message
	dialer   *websocket.Dialer
}

type readResult struct {
	msg protocol.Message
	err error
}

func New(url, token string, info protocol.SessionInfo) *Connector {
	return &Connector{
		url:      url,
		token:    token,
		info:     info,
		outbound: make(chan protocol.Message, 128),
		dialer:   websocket.DefaultDialer,
	}
}

// BindHub completes the connector's relationship with the PTY session:
// - inbound relay input can be written into the hub
// - local resize notifications can be exported to the relay
func (c *Connector) BindHub(hub *session.Hub) {
	c.hub = hub
	hub.OnResize(func(cols, rows int) {
		msg := protocol.Message{Type: "resize", Cols: cols, Rows: rows}
		select {
		case c.outbound <- msg:
		default:
		}
	})
}

// WriteOutput lets Connector satisfy session.OutputSink, so the PTY output fanout
// can deliver terminal bytes to the relay without special Codex logic.
func (c *Connector) WriteOutput(data []byte) error {
	msg := protocol.EncodeOutput(append([]byte(nil), data...))
	select {
	case c.outbound <- msg:
	default:
	}
	return nil
}

// UpdateSessionState is the Codex-specific state ingress. The app-server monitor
// calls this method, and the connector forwards the resulting `session_state`
// message over the same websocket used for terminal output.
func (c *Connector) UpdateSessionState(state protocol.SessionState, changedAt time.Time, actionRequiredSince *time.Time) {
	msg := protocol.EncodeSessionState(state, changedAt, actionRequiredSince)
	select {
	case c.outbound <- msg:
	default:
	}
}

// Run keeps reconnecting until the context is canceled. This means the PTY
// runtime and the Codex app-server monitor can continue producing messages into
// the connector while the websocket is being re-established.
func (c *Connector) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.runOnce(ctx); err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

func (c *Connector) runOnce(ctx context.Context) error {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.token)

	conn, _, err := c.dialer.DialContext(ctx, c.url+"/agent/ws", headers)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Register establishes the live session on the relay before any output or
	// session-state messages are accepted for this websocket connection.
	if err := conn.WriteJSON(protocol.RegisterFrame(c.info)); err != nil {
		return err
	}

	incoming := make(chan readResult, 1)
	go c.readLoop(conn, incoming)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result, ok := <-incoming:
			if !ok {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			c.handleMessage(result.msg)
		case msg := <-c.outbound:
			// Output, resize, and session-state messages all share this write path.
			if err := conn.WriteJSON(msg); err != nil {
				return err
			}
		}
	}
}

func (c *Connector) readLoop(conn *websocket.Conn, incoming chan<- readResult) {
	defer close(incoming)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			incoming <- readResult{err: err}
			return
		}

		var msg protocol.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		incoming <- readResult{msg: msg}
	}
}

// handleMessage is intentionally small because relay -> agent traffic currently
// carries only input bytes. Session-state is one-way: local Codex runtime to relay.
func (c *Connector) handleMessage(msg protocol.Message) {
	if msg.Type == "input" {
		data, err := protocol.DecodeData(msg)
		if err == nil && c.hub != nil {
			_ = c.hub.WriteInput(data)
		}
	}
}
