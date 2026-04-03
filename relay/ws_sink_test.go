package relay

import (
	"errors"
	"testing"
	"time"
)

type blockingWSConn struct {
	writeStarted chan struct{}
	releaseWrite chan struct{}
	closeCalls   int
}

func (c *blockingWSConn) WriteJSON(v any) error {
	close(c.writeStarted)
	<-c.releaseWrite
	return nil
}

func (c *blockingWSConn) SetWriteDeadline(time.Time) error { return nil }

func (c *blockingWSConn) Close() error {
	c.closeCalls++
	return nil
}

func TestWSSinkBackpressureCallsHook(t *testing.T) {
	conn := &blockingWSConn{
		writeStarted: make(chan struct{}),
		releaseWrite: make(chan struct{}),
	}
	backpressure := make(chan struct{}, 1)
	sink := newWSSinkWithConfig(conn, 1, 0, func() {
		backpressure <- struct{}{}
	})
	defer func() {
		close(conn.releaseWrite)
		_ = sink.Close()
	}()

	if err := sink.WriteOutputFrame(1, []byte("first")); err != nil {
		t.Fatalf("first WriteOutputFrame error = %v, want nil", err)
	}

	select {
	case <-conn.writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sink writer to start")
	}

	if err := sink.WriteOutputFrame(2, []byte("second")); err != nil {
		t.Fatalf("second WriteOutputFrame error = %v, want nil", err)
	}

	if err := sink.WriteOutputFrame(3, []byte("third")); !errors.Is(err, errWSSinkBackpressure) {
		t.Fatalf("third WriteOutputFrame error = %v, want errWSSinkBackpressure", err)
	}

	select {
	case <-backpressure:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for backpressure hook")
	}
}
