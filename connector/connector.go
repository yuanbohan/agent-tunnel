package connector

import (
	"context"
	"encoding/json"
	"errors"
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
const defaultWSWriteTimeout = 5 * time.Second

// Some terminal UIs only react to submit correctly when text and Enter arrive
// as distinct input events instead of one combined PTY write.
const defaultSubmitEnterGap = 120 * time.Millisecond

var errOutboundBackpressure = errors.New("connector outbound backpressure")

// Connector is the one place where the local runtime meets the relay protocol.
//
// Upstream producers feeding this object:
// - session.Hub output fanout via WriteOutput()
//
// Downstream consumer:
// - relay `/agent/ws`, which receives register/activity/attach frames
//
// Reverse data path:
// - relay input frames come back through handleInbound() into the bound Hub
type Connector struct {
	url    string
	token  string
	info   protocol.SessionInfo
	infoMu sync.RWMutex

	initialCols int
	initialRows int
	outbound    chan outboundFrame
	ephemeral   chan outboundFrame
	dialer      *websocket.Dialer
	hubMu       sync.RWMutex
	hub         *session.Hub
	pendingIn   []protocol.AgentFrame
	connectTTL  time.Duration

	stateMu      sync.RWMutex
	state        State
	subscribers  map[chan State]struct{}
	retryBackoff []time.Duration
	sleep        func(context.Context, time.Duration) bool
	mirror       *session.TerminalMirror
	attachMu     sync.Mutex
	attached     map[string]struct{}
	writeTimeout time.Duration

	connMu       sync.Mutex
	activeConn   connectionCloser
	overflowChan chan error
}

type outboundFrame struct {
	json   any
	binary []byte
}

type readResult struct {
	control *protocol.AgentFrame
	err     error
}

type connectionCloser interface {
	Close() error
}

func New(url, token string, info protocol.SessionInfo) *Connector {
	return &Connector{
		url:          url,
		token:        token,
		info:         info,
		outbound:     make(chan outboundFrame, 128),
		ephemeral:    make(chan outboundFrame, 128),
		dialer:       websocket.DefaultDialer,
		state:        StateDisconnected,
		subscribers:  make(map[chan State]struct{}),
		retryBackoff: append([]time.Duration(nil), defaultReconnectBackoff...),
		mirror:       session.NewTerminalMirror(0, 0),
		attached:     make(map[string]struct{}),
		writeTimeout: defaultWSWriteTimeout,
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

	if cols, rows := hub.CurrentSize(); cols > 0 && rows > 0 {
		c.applyResize(cols, rows, false)
	}
	hub.AddResizeListener("connector", func(cols, rows int) {
		c.applyResize(cols, rows, true)
	})

	c.hubMu.Lock()
	c.hub = hub
	pending := append([]protocol.AgentFrame(nil), c.pendingIn...)
	c.pendingIn = nil
	c.hubMu.Unlock()

	for _, frame := range pending {
		c.deliverInput(frame)
	}
}

func (c *Connector) SetInitialSize(cols, rows int) {
	c.initialCols = cols
	c.initialRows = rows
	c.applyResize(cols, rows, false)
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
	dataCopy := append([]byte(nil), data...)

	c.attachMu.Lock()
	c.mirror.WriteOutput(dataCopy)
	attached := make([]string, 0, len(c.attached))
	for clientID := range c.attached {
		attached = append(attached, clientID)
	}
	c.attachMu.Unlock()

	now := time.Now().UTC()
	lastActiveAt := protocol.UnixTimestamp(now)
	c.setLastActiveAt(lastActiveAt)
	if c.CurrentState() == StateConnected {
		c.enqueuePersistentJSON(protocol.ActivityFrame(lastActiveAt))
	}

	for _, clientID := range attached {
		packet, err := protocol.EncodeTerminalBytesPacket(clientID, dataCopy)
		if err != nil {
			continue
		}
		c.enqueueEphemeralBinary(packet)
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

	if err := c.writeJSON(conn, protocol.RegisterFrame(c.infoSnapshot())); err != nil {
		_ = conn.Close()
		return false, err
	}

	c.setState(StateConnected)
	return true, c.serveConnection(ctx, conn)
}

func (c *Connector) serveConnection(ctx context.Context, conn *websocket.Conn) error {
	defer conn.Close()
	defer c.clearConnectionState()
	defer c.setState(StateReconnecting)

	done := make(chan struct{})
	defer close(done)
	incoming := make(chan readResult, 1)
	overflow := make(chan error, 1)
	c.setActiveConnection(conn, overflow)
	defer c.clearActiveConnection()

	go c.readLoop(conn, done, incoming)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-overflow:
			if err != nil {
				return err
			}
		case result, ok := <-incoming:
			if !ok {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			if err := c.handleInbound(conn, result); err != nil {
				return err
			}
			continue
		default:
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-overflow:
			if err != nil {
				return err
			}
		case result, ok := <-incoming:
			if !ok {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			if err := c.handleInbound(conn, result); err != nil {
				return err
			}
		case frame := <-c.ephemeral:
			if err := c.writeOutboundFrame(conn, frame); err != nil {
				return err
			}
		case frame := <-c.outbound:
			if err := c.writeOutboundFrame(conn, frame); err != nil {
				return err
			}
		}
	}
}

func (c *Connector) handleInbound(conn *websocket.Conn, result readResult) error {
	if result.control == nil {
		return nil
	}

	switch result.control.Type {
	case "attach_open":
		if result.control.ClientID == "" {
			return nil
		}
		if err := c.handleAttachOpen(conn, result.control.ClientID); err != nil {
			return err
		}
	case "attach_close":
		if result.control.ClientID == "" {
			return nil
		}
		c.handleAttachClose(result.control.ClientID)
	case "input_text", "input_key":
		c.routeInput(*result.control)
	}
	return nil
}

func (c *Connector) writeOutboundFrame(conn *websocket.Conn, frame outboundFrame) error {
	if frame.binary != nil {
		return c.writeBinary(conn, frame.binary)
	}
	if frame.json == nil {
		return nil
	}
	return c.writeJSON(conn, frame.json)
}

func (c *Connector) infoSnapshot() protocol.SessionInfo {
	c.infoMu.RLock()
	defer c.infoMu.RUnlock()
	return c.info
}

func (c *Connector) setLastActiveAt(ts int) {
	c.infoMu.Lock()
	defer c.infoMu.Unlock()
	if ts > 0 {
		tsCopy := ts
		c.info.LastActiveAt = &tsCopy
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

func (c *Connector) readLoop(conn *websocket.Conn, done <-chan struct{}, incoming chan<- readResult) {
	defer close(incoming)

	for {
		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			if !deliverReadResult(done, incoming, readResult{err: err}) {
				return
			}
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}

		var frame protocol.AgentFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			continue
		}
		if !deliverReadResult(done, incoming, readResult{control: &frame}) {
			return
		}
	}
}

func (c *Connector) routeInput(frame protocol.AgentFrame) {
	c.hubMu.RLock()
	hub := c.hub
	c.hubMu.RUnlock()
	if hub != nil {
		c.deliverInputToHub(hub, frame)
		return
	}

	c.hubMu.Lock()
	if c.hub != nil {
		hub = c.hub
		c.hubMu.Unlock()
		c.deliverInputToHub(hub, frame)
		return
	}
	if len(c.pendingIn) >= defaultPendingInputLimit {
		c.pendingIn = c.pendingIn[1:]
	}
	c.pendingIn = append(c.pendingIn, frame)
	c.hubMu.Unlock()
}

func (c *Connector) deliverInput(frame protocol.AgentFrame) {
	c.hubMu.RLock()
	hub := c.hub
	c.hubMu.RUnlock()
	if hub == nil {
		return
	}
	c.deliverInputToHub(hub, frame)
}

func (c *Connector) deliverInputToHub(hub *session.Hub, frame protocol.AgentFrame) {
	switch frame.Type {
	case "input_text":
		if frame.Submit {
			_ = hub.WriteInputSequenceWithGap(defaultSubmitEnterGap, session.EncodeRemoteSubmitInput(frame.Text)...)
			return
		}
		_ = hub.WriteInput(session.EncodeRemoteTextInput(frame.Text))
	case "input_key":
		if data, ok := session.EncodeRemoteKeyInput(frame.Key); ok {
			_ = hub.WriteInput(data)
		}
	}
}

func (c *Connector) handleAttachOpen(conn *websocket.Conn, clientID string) error {
	c.attachMu.Lock()
	snapshot, cols, rows := c.mirror.Snapshot()
	c.attached[clientID] = struct{}{}
	c.attachMu.Unlock()

	if err := c.writeOutboundFrame(conn, outboundFrame{
		json: protocol.AttachReadyFrame(clientID, cols, rows),
	}); err != nil {
		return err
	}
	if len(snapshot) > 0 {
		if packet, err := protocol.EncodeTerminalBytesPacket(clientID, snapshot); err == nil {
			if err := c.writeOutboundFrame(conn, outboundFrame{binary: packet}); err != nil {
				return err
			}
		}
	}
	return c.writeOutboundFrame(conn, outboundFrame{
		json: protocol.SnapshotDoneFrame(clientID),
	})
}

func (c *Connector) handleAttachClose(clientID string) {
	c.attachMu.Lock()
	defer c.attachMu.Unlock()
	delete(c.attached, clientID)
}

func (c *Connector) applyResize(cols, rows int, emit bool) {
	if cols <= 0 || rows <= 0 {
		return
	}

	c.attachMu.Lock()
	c.mirror.Resize(cols, rows)
	hasAttached := len(c.attached) > 0
	c.attachMu.Unlock()

	if emit && hasAttached {
		c.enqueueEphemeralJSON(protocol.ResizeFrame(cols, rows))
	}
}

func (c *Connector) enqueuePersistentJSON(v any) {
	if !c.tryEnqueue(c.outbound, outboundFrame{json: v}) {
		c.signalOverflow(errOutboundBackpressure)
	}
}

func (c *Connector) enqueueEphemeralJSON(v any) {
	if !c.tryEnqueue(c.ephemeral, outboundFrame{json: v}) {
		c.signalOverflow(errOutboundBackpressure)
	}
}

func (c *Connector) enqueueEphemeralBinary(payload []byte) {
	if !c.tryEnqueue(c.ephemeral, outboundFrame{binary: append([]byte(nil), payload...)}) {
		c.signalOverflow(errOutboundBackpressure)
	}
}

func (c *Connector) clearConnectionState() {
	c.attachMu.Lock()
	c.attached = make(map[string]struct{})
	c.attachMu.Unlock()

	for {
		select {
		case <-c.ephemeral:
		case <-c.outbound:
		default:
			return
		}
	}
}

func (c *Connector) tryEnqueue(ch chan outboundFrame, frame outboundFrame) bool {
	select {
	case ch <- frame:
		return true
	default:
		return false
	}
}

func (c *Connector) signalOverflow(err error) {
	c.connMu.Lock()
	overflow := c.overflowChan
	conn := c.activeConn
	c.connMu.Unlock()

	if overflow != nil {
		select {
		case overflow <- err:
		default:
		}
	}
	if conn != nil {
		_ = conn.Close()
	}
}

func (c *Connector) setActiveConnection(conn connectionCloser, overflow chan error) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	c.activeConn = conn
	c.overflowChan = overflow
}

func (c *Connector) clearActiveConnection() {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	c.activeConn = nil
	c.overflowChan = nil
}

func (c *Connector) writeJSON(conn *websocket.Conn, v any) error {
	if err := c.setWriteDeadline(conn); err != nil {
		return err
	}
	return conn.WriteJSON(v)
}

func (c *Connector) writeBinary(conn *websocket.Conn, payload []byte) error {
	if err := c.setWriteDeadline(conn); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (c *Connector) setWriteDeadline(conn *websocket.Conn) error {
	if c.writeTimeout <= 0 {
		return nil
	}
	return conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
}

func deliverReadResult(done <-chan struct{}, incoming chan<- readResult, result readResult) bool {
	select {
	case incoming <- result:
		return true
	case <-done:
		return false
	}
}
