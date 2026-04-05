package relay

import (
	"sync"
	"time"

	"yuanbohan/tunnel/protocol"
)

type wsSessionStateSink struct {
	conn         wsConn
	writeTimeout time.Duration

	mu        sync.RWMutex
	closed    bool
	outbound  chan protocol.SessionStateEvent
	closeOnce sync.Once
}

func newWSSessionStateSink(conn wsConn, bufferSize int, writeTimeout time.Duration) *wsSessionStateSink {
	if bufferSize <= 0 {
		bufferSize = 1
	}

	sink := &wsSessionStateSink{
		conn:         conn,
		writeTimeout: writeTimeout,
		outbound:     make(chan protocol.SessionStateEvent, bufferSize),
	}
	go sink.run()
	return sink
}

func (s *wsSessionStateSink) WriteSessionStateEvent(event protocol.SessionStateEvent) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errWSSinkClosed
	}
	select {
	case s.outbound <- event:
		s.mu.RUnlock()
		return nil
	default:
		s.mu.RUnlock()
		_ = s.Close()
		return errWSSinkBackpressure
	}
}

func (s *wsSessionStateSink) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.outbound)
		s.mu.Unlock()
		_ = s.conn.Close()
	})
	return nil
}

func (s *wsSessionStateSink) run() {
	defer s.Close()

	for event := range s.outbound {
		if s.writeTimeout > 0 {
			if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
				return
			}
		}
		if err := s.conn.WriteJSON(event); err != nil {
			return
		}
	}
}
