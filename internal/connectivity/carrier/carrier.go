package carrier

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

var (
	ErrClosedEndpoint  = errors.New("carrier endpoint closed")
	ErrUnknownEndpoint = errors.New("carrier endpoint unknown")
)

type Relay struct {
	mu        sync.Mutex
	endpoints map[string]*PacketConn
	observed  [][]byte
}

func NewRelay() *Relay {
	return &Relay{
		endpoints: make(map[string]*PacketConn),
	}
}

func (r *Relay) NewPacketConn(name string) *PacketConn {
	conn := &PacketConn{
		relay:                r,
		addr:                 Addr(name),
		inbound:              make(chan packet, 4096),
		done:                 make(chan struct{}),
		readDeadlineChanged:  make(chan struct{}),
		writeDeadlineChanged: make(chan struct{}),
	}

	r.mu.Lock()
	r.endpoints[name] = conn
	r.mu.Unlock()
	return conn
}

func (r *Relay) ObservedPackets() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([][]byte, len(r.observed))
	for i := range r.observed {
		out[i] = append([]byte(nil), r.observed[i]...)
	}
	return out
}

func (r *Relay) forward(from *PacketConn, to net.Addr, payload []byte) error {
	r.mu.Lock()
	peer := r.endpoints[to.String()]
	r.observed = append(r.observed, append([]byte(nil), payload...))
	r.mu.Unlock()

	if peer == nil {
		return ErrUnknownEndpoint
	}

	copied := append([]byte(nil), payload...)
	for {
		if from.closed() || peer.closed() {
			return ErrClosedEndpoint
		}

		deadline, changed := from.writeDeadlineSnapshot()
		timer, stop := deadlineTimer(deadline)
		select {
		case peer.inbound <- packet{from: from.addr, payload: copied}:
			stop()
			if peer.closed() {
				return ErrClosedEndpoint
			}
			return nil
		case <-from.done:
			stop()
			return ErrClosedEndpoint
		case <-peer.done:
			stop()
			return ErrClosedEndpoint
		case <-changed:
			stop()
			continue
		case <-timer:
			stop()
			return os.ErrDeadlineExceeded
		}
	}
}

func (r *Relay) remove(conn *PacketConn) {
	r.mu.Lock()
	if r.endpoints[conn.addr.String()] == conn {
		delete(r.endpoints, conn.addr.String())
	}
	r.mu.Unlock()
}

type Addr string

func (a Addr) Network() string {
	return "relay-carrier"
}

func (a Addr) String() string {
	return string(a)
}

type PacketConn struct {
	relay   *Relay
	addr    Addr
	inbound chan packet
	done    chan struct{}

	mu            sync.Mutex
	closedFlag    bool
	readDeadline  time.Time
	writeDeadline time.Time

	readDeadlineChanged  chan struct{}
	writeDeadlineChanged chan struct{}
}

type packet struct {
	from    Addr
	payload []byte
}

func (c *PacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		if c.closed() {
			return 0, nil, ErrClosedEndpoint
		}

		deadline, changed := c.readDeadlineSnapshot()
		timer, stop := deadlineTimer(deadline)
		select {
		case pkt := <-c.inbound:
			stop()
			if c.closed() {
				return 0, nil, ErrClosedEndpoint
			}
			return copy(p, pkt.payload), pkt.from, nil
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

func (c *PacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.closed() {
		return 0, ErrClosedEndpoint
	}
	if addr == nil {
		return 0, ErrUnknownEndpoint
	}

	c.mu.Lock()
	deadline := c.writeDeadline
	c.mu.Unlock()
	if !deadline.IsZero() && time.Now().After(deadline) {
		return 0, os.ErrDeadlineExceeded
	}

	if err := c.relay.forward(c, addr, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *PacketConn) Close() error {
	c.mu.Lock()
	if !c.closedFlag {
		c.closedFlag = true
		close(c.done)
		c.notifyReadDeadlineChanged()
		c.notifyWriteDeadlineChanged()
	}
	c.mu.Unlock()
	c.relay.remove(c)
	return nil
}

func (c *PacketConn) LocalAddr() net.Addr {
	return c.addr
}

func (c *PacketConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.writeDeadline = t
	c.notifyReadDeadlineChanged()
	c.notifyWriteDeadlineChanged()
	c.mu.Unlock()
	return nil
}

func (c *PacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.notifyReadDeadlineChanged()
	c.mu.Unlock()
	return nil
}

func (c *PacketConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.notifyWriteDeadlineChanged()
	c.mu.Unlock()
	return nil
}

func (c *PacketConn) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closedFlag
}

func (c *PacketConn) readDeadlineSnapshot() (time.Time, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readDeadline, c.readDeadlineChanged
}

func (c *PacketConn) writeDeadlineSnapshot() (time.Time, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeDeadline, c.writeDeadlineChanged
}

func (c *PacketConn) notifyReadDeadlineChanged() {
	close(c.readDeadlineChanged)
	c.readDeadlineChanged = make(chan struct{})
}

func (c *PacketConn) notifyWriteDeadlineChanged() {
	close(c.writeDeadlineChanged)
	c.writeDeadlineChanged = make(chan struct{})
}

func deadlineTimer(deadline time.Time) (<-chan time.Time, func()) {
	if deadline.IsZero() {
		return nil, func() {}
	}
	duration := time.Until(deadline)
	if duration <= 0 {
		ready := make(chan time.Time, 1)
		ready <- time.Now()
		return ready, func() {}
	}
	timer := time.NewTimer(duration)
	return timer.C, func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
}
