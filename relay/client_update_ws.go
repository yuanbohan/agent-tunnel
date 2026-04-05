package relay

import (
	"sync"
	"time"

	"yuanbohan/tunnel/protocol"
)

type wsClientUpdateSink struct {
	conn         wsConn
	writeTimeout time.Duration

	mu        sync.RWMutex
	closed    bool
	outbound  chan protocol.ClientUpdateMessage
	closeOnce sync.Once
}

func newWSClientUpdateSink(conn wsConn, bufferSize int, writeTimeout time.Duration) *wsClientUpdateSink {
	if bufferSize <= 0 {
		bufferSize = 1
	}

	sink := &wsClientUpdateSink{
		conn:         conn,
		writeTimeout: writeTimeout,
		outbound:     make(chan protocol.ClientUpdateMessage, bufferSize),
	}
	go sink.run()
	return sink
}

func (s *wsClientUpdateSink) WriteClientUpdate(msg protocol.ClientUpdateMessage) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errWSSinkClosed
	}
	select {
	case s.outbound <- msg:
		s.mu.RUnlock()
		return nil
	default:
		s.mu.RUnlock()
		_ = s.Close()
		return errWSSinkBackpressure
	}
}

func (s *wsClientUpdateSink) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.outbound)
		s.mu.Unlock()
		_ = s.conn.Close()
	})
	return nil
}

func (s *wsClientUpdateSink) run() {
	defer s.Close()

	for msg := range s.outbound {
		if s.writeTimeout > 0 {
			if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
				return
			}
		}
		if err := s.conn.WriteJSON(msg); err != nil {
			return
		}
	}
}
