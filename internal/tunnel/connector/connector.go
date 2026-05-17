package connector

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
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

const defaultWSWriteTimeout = 5 * time.Second

var errOutboundBackpressure = errors.New("connector outbound backpressure")

// Connector is the one place where the local runtime meets the relay protocol.
//
// Downstream consumer:
// - relay `/agent/ws`, which receives register and launch correlation frames.
type Connector struct {
	url           string
	token         string
	info          protocol.SessionInfo
	launchContext protocol.LaunchContext
	infoMu        sync.RWMutex
	launchReadyMu sync.RWMutex
	launchReady   *protocol.LaunchContext

	outbound   chan outboundFrame
	dialer     *websocket.Dialer
	reconnect  atomic.Bool
	connectTTL time.Duration

	stateMu      sync.RWMutex
	state        State
	subscribers  map[chan State]struct{}
	retryBackoff []time.Duration
	sleep        func(context.Context, time.Duration) bool
	writeTimeout time.Duration

	connMu       sync.Mutex
	activeConn   connectionCloser
	overflowChan chan error
}

type outboundFrame struct {
	json any
}

type connectionCloser interface {
	Close() error
}

func New(url, token string, info protocol.SessionInfo) *Connector {
	c := &Connector{
		url:          url,
		token:        token,
		info:         info,
		outbound:     make(chan outboundFrame, 128),
		dialer:       websocket.DefaultDialer,
		state:        StateDisconnected,
		subscribers:  make(map[chan State]struct{}),
		retryBackoff: append([]time.Duration(nil), defaultReconnectBackoff...),
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
	c.reconnect.Store(true)
	return c
}

func (c *Connector) SetLaunchContext(launchContext protocol.LaunchContext) {
	c.infoMu.Lock()
	defer c.infoMu.Unlock()
	c.launchContext = launchContext
}

func (c *Connector) MarkLaunchReady(launchContext protocol.LaunchContext) {
	if launchContext.Source != protocol.SessionLaunchSourceMobile || launchContext.RequestID == "" {
		return
	}
	c.launchReadyMu.Lock()
	c.launchReady = &protocol.LaunchContext{
		Source:    launchContext.Source,
		RequestID: launchContext.RequestID,
	}
	c.launchReadyMu.Unlock()
	c.enqueuePersistentJSON(protocol.LaunchReadyFrame(launchContext))
}

func (c *Connector) SetInitialConnectTimeout(timeout time.Duration) {
	c.connectTTL = timeout
}

func (c *Connector) SetReconnectEnabled(enabled bool) {
	c.reconnect.Store(enabled)
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

		if !c.reconnect.Load() {
			c.setState(StateDisconnected)
			return
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

	info, launchContext := c.infoSnapshot()
	if err := c.writeJSON(conn, protocol.RegisterFrameWithLaunchContext(info, launchContext)); err != nil {
		_ = conn.Close()
		return false, err
	}
	if err := c.writeStickyLaunchReady(conn); err != nil {
		_ = conn.Close()
		return false, err
	}

	c.setState(StateConnected)
	return true, c.serveConnection(ctx, conn)
}

func (c *Connector) serveConnection(ctx context.Context, conn *websocket.Conn) error {
	defer conn.Close()
	defer c.setState(StateReconnecting)

	done := make(chan struct{})
	defer close(done)
	readErr := make(chan error, 1)
	overflow := make(chan error, 1)
	c.setActiveConnection(conn, overflow)
	defer c.clearActiveConnection()

	go c.readLoop(conn, done, readErr)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-overflow:
			if err != nil {
				return err
			}
		case err, ok := <-readErr:
			if !ok {
				return nil
			}
			if err != nil {
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
		case err, ok := <-readErr:
			if !ok {
				return nil
			}
			if err != nil {
				return err
			}
		case frame := <-c.outbound:
			if err := c.writeJSON(conn, frame.json); err != nil {
				return err
			}
		}
	}
}

func (c *Connector) infoSnapshot() (protocol.SessionInfo, protocol.LaunchContext) {
	c.infoMu.RLock()
	defer c.infoMu.RUnlock()
	return c.info, c.launchContext
}

func (c *Connector) launchReadySnapshot() (protocol.LaunchContext, bool) {
	c.launchReadyMu.RLock()
	defer c.launchReadyMu.RUnlock()
	if c.launchReady == nil {
		return protocol.LaunchContext{}, false
	}
	return *c.launchReady, true
}

func (c *Connector) writeStickyLaunchReady(conn *websocket.Conn) error {
	launchContext, ok := c.launchReadySnapshot()
	if !ok {
		return nil
	}
	return c.writeJSON(conn, protocol.LaunchReadyFrame(launchContext))
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

func (c *Connector) readLoop(conn *websocket.Conn, done <-chan struct{}, readErr chan<- error) {
	defer close(readErr)

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if !deliverReadErr(done, readErr, err) {
				return
			}
			return
		}
	}
}

func (c *Connector) enqueuePersistentJSON(v any) {
	if !c.tryEnqueue(c.outbound, outboundFrame{json: v}) {
		c.signalOverflow(errOutboundBackpressure)
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

func (c *Connector) setWriteDeadline(conn *websocket.Conn) error {
	if c.writeTimeout <= 0 {
		return nil
	}
	return conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
}

func deliverReadErr(done <-chan struct{}, readErr chan<- error, err error) bool {
	select {
	case readErr <- err:
		return true
	case <-done:
		return false
	}
}
