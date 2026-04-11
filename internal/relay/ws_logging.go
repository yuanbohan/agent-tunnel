package relay

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var errWSTextFrameRequired = errors.New("websocket text frame required")

type wsWriteConn interface {
	WriteMessage(messageType int, data []byte) error
}

type wsTrafficTracker struct {
	path        string
	remoteAddr  string
	requestID   string
	connectedAt time.Time

	mu               sync.Mutex
	sessionID        string
	inboundMessages  int64
	inboundBytes     int64
	outboundMessages int64
	outboundBytes    int64
	disconnectErr    error
}

func newWSTrafficTracker(path, remoteAddr, requestID string) *wsTrafficTracker {
	return &wsTrafficTracker{
		path:        path,
		remoteAddr:  remoteAddr,
		requestID:   requestID,
		connectedAt: time.Now(),
	}
}

func (t *wsTrafficTracker) SetSessionID(sessionID string) {
	if t == nil || sessionID == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessionID == "" {
		t.sessionID = sessionID
	}
}

func (t *wsTrafficTracker) RecordInbound(size int) {
	if t == nil || size < 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.inboundMessages++
	t.inboundBytes += int64(size)
}

func (t *wsTrafficTracker) RecordOutbound(size int) {
	if t == nil || size < 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.outboundMessages++
	t.outboundBytes += int64(size)
}

func (t *wsTrafficTracker) NoteDisconnectError(err error) {
	if t == nil || err == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.disconnectErr == nil {
		t.disconnectErr = err
	}
}

func (t *wsTrafficTracker) DisconnectError(fallback error) error {
	if t == nil {
		return fallback
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.disconnectErr != nil {
		return t.disconnectErr
	}
	return fallback
}

func (t *wsTrafficTracker) SummaryFields(now time.Time) []Field {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	path := t.path
	remoteAddr := t.remoteAddr
	requestID := t.requestID
	sessionID := t.sessionID
	inboundMessages := t.inboundMessages
	inboundBytes := t.inboundBytes
	outboundMessages := t.outboundMessages
	outboundBytes := t.outboundBytes
	connectedAt := t.connectedAt
	t.mu.Unlock()

	fields := []Field{
		String("path", path),
		String("remote_addr", remoteAddr),
		Int64("duration_ms", now.Sub(connectedAt).Milliseconds()),
		Int64("inbound_messages", inboundMessages),
		Int64("inbound_bytes", inboundBytes),
		Int64("outbound_messages", outboundMessages),
		Int64("outbound_bytes", outboundBytes),
	}
	if requestID != "" {
		fields = append(fields, String("request_id", requestID))
	}
	if sessionID != "" {
		fields = append(fields, String("session_id", sessionID))
	}
	return fields
}

func readWSJSON(conn *websocket.Conn, v any) ([]byte, error) {
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if messageType != websocket.TextMessage {
		return nil, errWSTextFrameRequired
	}
	if err := json.Unmarshal(payload, v); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeWSJSON(conn wsWriteConn, v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return nil, err
	}
	return payload, nil
}
