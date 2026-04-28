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
		relay:   r,
		addr:    Addr(name),
		inbound: make(chan packet, 4096),
		done:    make(chan struct{}),
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

func (r *Relay) forward(from Addr, to net.Addr, payload []byte) error {
	r.mu.Lock()
	peer := r.endpoints[to.String()]
	r.observed = append(r.observed, append([]byte(nil), payload...))
	r.mu.Unlock()

	if peer == nil {
		return ErrUnknownEndpoint
	}
	if peer.closed() {
		return ErrClosedEndpoint
	}

	copied := append([]byte(nil), payload...)
	select {
	case peer.inbound <- packet{from: from, payload: copied}:
		return nil
	case <-peer.done:
		return ErrClosedEndpoint
	}
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
}

type packet struct {
	from    Addr
	payload []byte
}

func (c *PacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.mu.Lock()
	deadline := c.readDeadline
	c.mu.Unlock()

	var timer <-chan time.Time
	if !deadline.IsZero() {
		duration := time.Until(deadline)
		if duration <= 0 {
			return 0, nil, os.ErrDeadlineExceeded
		}
		t := time.NewTimer(duration)
		defer t.Stop()
		timer = t.C
	}

	select {
	case pkt := <-c.inbound:
		return copy(p, pkt.payload), pkt.from, nil
	case <-c.done:
		return 0, nil, ErrClosedEndpoint
	case <-timer:
		return 0, nil, os.ErrDeadlineExceeded
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

	if err := c.relay.forward(c.addr, addr, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *PacketConn) Close() error {
	c.mu.Lock()
	if !c.closedFlag {
		c.closedFlag = true
		close(c.done)
	}
	c.mu.Unlock()
	return nil
}

func (c *PacketConn) LocalAddr() net.Addr {
	return c.addr
}

func (c *PacketConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.writeDeadline = t
	c.mu.Unlock()
	return nil
}

func (c *PacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.mu.Unlock()
	return nil
}

func (c *PacketConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.mu.Unlock()
	return nil
}

func (c *PacketConn) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closedFlag
}
