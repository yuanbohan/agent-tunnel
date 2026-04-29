package carrier

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultWSPacketQueue        = 4096
	defaultWSPacketWriteTimeout = 15 * time.Second
)

var ErrUnexpectedMessageType = errors.New("unexpected websocket message type")

type WSPacketConn struct {
	conn *websocket.Conn
	addr Addr
	peer Addr

	inbound chan []byte
	done    chan struct{}

	closeOnce sync.Once
	writeMu   sync.Mutex

	mu            sync.Mutex
	closedFlag    bool
	readDeadline  time.Time
	writeDeadline time.Time

	readDeadlineChanged  chan struct{}
	writeDeadlineChanged chan struct{}
}

func NewWSPacketConn(conn *websocket.Conn, localAddr, peerAddr string, maxPacketSize int) *WSPacketConn {
	if maxPacketSize > 0 {
		conn.SetReadLimit(int64(maxPacketSize))
	}
	packetConn := &WSPacketConn{
		conn:                 conn,
		addr:                 Addr(localAddr),
		peer:                 Addr(peerAddr),
		inbound:              make(chan []byte, defaultWSPacketQueue),
		done:                 make(chan struct{}),
		readDeadlineChanged:  make(chan struct{}),
		writeDeadlineChanged: make(chan struct{}),
	}
	go packetConn.readLoop()
	return packetConn
}

func (c *WSPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		if c.closed() {
			return 0, nil, ErrClosedEndpoint
		}
		deadline, changed := c.readDeadlineSnapshot()
		if deadlineExpired(deadline) {
			return 0, nil, os.ErrDeadlineExceeded
		}
		timer, stop := deadlineTimer(deadline)
		select {
		case payload := <-c.inbound:
			stop()
			if c.closed() {
				return 0, nil, ErrClosedEndpoint
			}
			return copy(p, payload), c.peer, nil
		case <-c.done:
			stop()
			return 0, nil, ErrClosedEndpoint
		case <-changed:
			stop()
			continue
		case <-timer:
			stop()
			return 0, nil, os.ErrDeadlineExceeded
		}
	}
}

func (c *WSPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.closed() {
		return 0, ErrClosedEndpoint
	}
	if addr == nil || addr.String() != c.peer.String() {
		return 0, ErrUnknownEndpoint
	}
	deadline, _ := c.writeDeadlineSnapshot()
	if deadlineExpired(deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed() {
		return 0, ErrClosedEndpoint
	}
	_ = c.conn.SetWriteDeadline(wsPacketWriteDeadline(deadline, time.Now()))
	if err := c.conn.WriteMessage(websocket.BinaryMessage, append([]byte(nil), p...)); err != nil {
		_ = c.Close()
		return 0, err
	}
	return len(p), nil
}

func (c *WSPacketConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closedFlag = true
		close(c.done)
		c.notifyReadDeadlineChanged()
		c.notifyWriteDeadlineChanged()
		c.mu.Unlock()
		err = c.conn.Close()
	})
	return err
}

func (c *WSPacketConn) LocalAddr() net.Addr {
	return c.addr
}

func (c *WSPacketConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.writeDeadline = t
	c.notifyReadDeadlineChanged()
	c.notifyWriteDeadlineChanged()
	c.mu.Unlock()
	return nil
}

func (c *WSPacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.notifyReadDeadlineChanged()
	c.mu.Unlock()
	return nil
}

func (c *WSPacketConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.notifyWriteDeadlineChanged()
	c.mu.Unlock()
	return nil
}

func (c *WSPacketConn) readLoop() {
	defer c.Close()
	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.BinaryMessage {
			return
		}
		copied := append([]byte(nil), payload...)
		select {
		case c.inbound <- copied:
		case <-c.done:
			return
		default:
			return
		}
	}
}

func (c *WSPacketConn) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closedFlag
}

func (c *WSPacketConn) readDeadlineSnapshot() (time.Time, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readDeadline, c.readDeadlineChanged
}

func (c *WSPacketConn) writeDeadlineSnapshot() (time.Time, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeDeadline, c.writeDeadlineChanged
}

func (c *WSPacketConn) notifyReadDeadlineChanged() {
	close(c.readDeadlineChanged)
	c.readDeadlineChanged = make(chan struct{})
}

func (c *WSPacketConn) notifyWriteDeadlineChanged() {
	close(c.writeDeadlineChanged)
	c.writeDeadlineChanged = make(chan struct{})
}

func wsPacketWriteDeadline(deadline, now time.Time) time.Time {
	if !deadline.IsZero() {
		return deadline
	}
	if defaultWSPacketWriteTimeout <= 0 {
		return time.Time{}
	}
	return now.Add(defaultWSPacketWriteTimeout)
}
