package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/session"
)

type State string

const (
	StateDisconnected State = "disconnected"
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateReconnecting State = "reconnecting"
)

var defaultReconnectBackoff = []time.Duration{
	3 * time.Second,
	5 * time.Second,
	10 * time.Second,
	20 * time.Second,
	60 * time.Second,
	5 * time.Minute,
}

const defaultPendingInputLimit = 128

// Connector is the one place where the local runtime meets the relay protocol.
//
// Upstream producers feeding this object:
// - session.Hub output fanout via WriteOutput()
//
// Downstream consumer:
// - relay `/agent/ws`, which receives register/output frames
//
// Reverse data path:
// - relay input frames come back through handleMessage() into the bound Hub
type Connector struct {
	url   string
	token string
	info  protocol.SessionInfo

	initialCols int
	initialRows int
	outbound    chan protocol.Message
	dialer      *websocket.Dialer
	hubMu       sync.RWMutex
	hub         *session.Hub
	pendingIn   []protocol.Message
	connectTTL  time.Duration

	stateMu      sync.RWMutex
	state        State
	subscribers  map[chan State]struct{}
	retryBackoff []time.Duration
	sleep        func(context.Context, time.Duration) bool
}

type readResult struct {
	msg protocol.Message
	err error
}

func New(url, token string, info protocol.SessionInfo) *Connector {
	return &Connector{
		url:          url,
		token:        token,
		info:         info,
		outbound:     make(chan protocol.Message, 128),
		dialer:       websocket.DefaultDialer,
		state:        StateDisconnected,
		subscribers:  make(map[chan State]struct{}),
		retryBackoff: append([]time.Duration(nil), defaultReconnectBackoff...),
		sleep: func(ctx context.Context, d time.Duration) bool {
			timer := time.NewTimer(d)
			defer timer.Stop()

			select {
			case <-ctx.Done():
				return false
			case <-timer.C:
				return true
			}
		},
	}
}

func (c *Connector) BindHub(hub *session.Hub) {
	if hub == nil {
		return
	}

	c.hubMu.Lock()
	c.hub = hub
	pending := append([]protocol.Message(nil), c.pendingIn...)
	c.pendingIn = nil
	c.hubMu.Unlock()

	for _, msg := range pending {
		c.deliverInput(msg)
	}
}

func (c *Connector) SetInitialSize(cols, rows int) {
	c.initialCols = cols
	c.initialRows = rows
}

func (c *Connector) SetInitialConnectTimeout(timeout time.Duration) {
	c.connectTTL = timeout
}

func (c *Connector) CurrentState() State {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state
}

func (c *Connector) SubscribeStateChanges() (<-chan State, func()) {
	ch := make(chan State, 8)

	c.stateMu.Lock()
	c.subscribers[ch] = struct{}{}
	current := c.state
	c.stateMu.Unlock()

	c.pushState(ch, current)

	cancel := func() {
		c.stateMu.Lock()
		delete(c.subscribers, ch)
		c.stateMu.Unlock()
	}

	return ch, cancel
}

func (c *Connector) WaitUntilConnected(ctx context.Context, timeout time.Duration) bool {
	if c.CurrentState() == StateConnected {
		return true
	}

	if timeout <= 0 {
		return false
	}

	stateCh, cancel := c.SubscribeStateChanges()
	defer cancel()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		if c.CurrentState() == StateConnected {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return c.CurrentState() == StateConnected
		case state, ok := <-stateCh:
			if !ok {
				return c.CurrentState() == StateConnected
			}
			if state == StateConnected {
				return true
			}
		}
	}
}

func (c *Connector) WriteOutput(data []byte) error {
	cols, rows := c.initialCols, c.initialRows
	c.hubMu.RLock()
	hub := c.hub
	c.hubMu.RUnlock()
	if hub != nil {
		if currentCols, currentRows := hub.CurrentSize(); currentCols > 0 && currentRows > 0 {
			cols, rows = currentCols, currentRows
		}
	}

	msg := protocol.EncodeOutputWithSeqAndSize(0, append([]byte(nil), data...), cols, rows)
	select {
	case c.outbound <- msg:
	default:
	}
	return nil
}

func (c *Connector) Run(ctx context.Context) {
	c.setState(StateConnecting)
	attempt := 0

	for {
		if ctx.Err() != nil {
			c.setState(StateDisconnected)
			return
		}

		connected, _ := c.runOnce(ctx, c.initialConnectTimeout(attempt))
		if ctx.Err() != nil {
			c.setState(StateDisconnected)
			return
		}

		if connected {
			attempt = 0
		}

		c.setState(StateReconnecting)
		if !c.sleep(ctx, c.nextRetryDelay(attempt)) {
			c.setState(StateDisconnected)
			return
		}
		attempt++
	}
}

func (c *Connector) runOnce(ctx context.Context, connectTimeout time.Duration) (bool, error) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.token)

	connectCtx := ctx
	var cancel context.CancelFunc
	if connectTimeout > 0 {
		connectCtx, cancel = context.WithTimeout(ctx, connectTimeout)
		defer cancel()
	}

	conn, _, err := c.dialer.DialContext(connectCtx, c.url+"/agent/ws", headers)
	if err != nil {
		return false, err
	}

	if err := conn.WriteJSON(protocol.RegisterFrame(c.info)); err != nil {
		_ = conn.Close()
		return false, err
	}

	c.setState(StateConnected)
	return true, c.serveConnection(ctx, conn)
}

func (c *Connector) serveConnection(ctx context.Context, conn *websocket.Conn) error {
	defer conn.Close()

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
			c.routeInput(result.msg)
		case msg := <-c.outbound:
			if err := conn.WriteJSON(msg); err != nil {
				return err
			}
		}
	}
}

func (c *Connector) initialConnectTimeout(attempt int) time.Duration {
	if attempt == 0 && c.connectTTL > 0 {
		return c.connectTTL
	}
	return 0
}

func (c *Connector) nextRetryDelay(attempt int) time.Duration {
	if len(c.retryBackoff) == 0 {
		return 0
	}
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(c.retryBackoff) {
		return c.retryBackoff[len(c.retryBackoff)-1]
	}
	return c.retryBackoff[attempt]
}

func (c *Connector) setState(state State) {
	c.stateMu.Lock()
	if c.state == state {
		c.stateMu.Unlock()
		return
	}
	c.state = state
	subscribers := make([]chan State, 0, len(c.subscribers))
	for ch := range c.subscribers {
		subscribers = append(subscribers, ch)
	}
	c.stateMu.Unlock()

	for _, ch := range subscribers {
		c.pushState(ch, state)
	}
}

func (c *Connector) pushState(ch chan State, state State) {
	select {
	case ch <- state:
		return
	default:
	}

	select {
	case <-ch:
	default:
	}

	select {
	case ch <- state:
	default:
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

func (c *Connector) routeInput(msg protocol.Message) {
	c.hubMu.RLock()
	hub := c.hub
	c.hubMu.RUnlock()
	if hub != nil {
		c.deliverInputToHub(hub, msg)
		return
	}

	c.hubMu.Lock()
	if c.hub != nil {
		hub = c.hub
		c.hubMu.Unlock()
		c.deliverInputToHub(hub, msg)
		return
	}
	if len(c.pendingIn) >= defaultPendingInputLimit {
		c.pendingIn = c.pendingIn[1:]
	}
	c.pendingIn = append(c.pendingIn, msg)
	c.hubMu.Unlock()
}

func (c *Connector) deliverInput(msg protocol.Message) {
	c.hubMu.RLock()
	hub := c.hub
	c.hubMu.RUnlock()
	if hub == nil {
		return
	}
	c.deliverInputToHub(hub, msg)
}

func (c *Connector) deliverInputToHub(hub *session.Hub, msg protocol.Message) {
	switch msg.Type {
	case "input_text":
		_ = hub.WriteInput(session.EncodeRemoteTextInput(msg.Text, msg.Submit))
	case "input_key":
		if data, ok := session.EncodeRemoteKeyInput(msg.Key, msg.Ctrl, msg.Alt, msg.Shift); ok {
			_ = hub.WriteInput(data)
		}
	}
}
