package ws

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/logx"
)

var (
	ErrTextFrameRequired = errors.New("websocket text frame required")
	ErrSinkClosed        = errors.New("websocket sink closed")
	ErrSinkBackpressure  = errors.New("websocket sink backpressure")
)

type WriteConn interface {
	WriteMessage(messageType int, data []byte) error
}

type Tracker struct {
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

func NewTracker(path, remoteAddr, requestID string) *Tracker {
	return &Tracker{
		path:        path,
		remoteAddr:  remoteAddr,
		requestID:   requestID,
		connectedAt: time.Now(),
	}
}

func (t *Tracker) SetSessionID(sessionID string) {
	if t == nil || sessionID == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessionID == "" {
		t.sessionID = sessionID
	}
}

func (t *Tracker) RecordInbound(size int) {
	if t == nil || size < 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.inboundMessages++
	t.inboundBytes += int64(size)
}

func (t *Tracker) RecordOutbound(size int) {
	if t == nil || size < 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.outboundMessages++
	t.outboundBytes += int64(size)
}

func (t *Tracker) NoteDisconnectError(err error) {
	if t == nil || err == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.disconnectErr == nil {
		t.disconnectErr = err
	}
}

func (t *Tracker) DisconnectError(fallback error) error {
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

func (t *Tracker) SummaryFields(now time.Time) []logx.Field {
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

	fields := []logx.Field{
		logx.String("path", path),
		logx.String("remote_addr", remoteAddr),
		logx.Int64("duration_ms", now.Sub(connectedAt).Milliseconds()),
		logx.Int64("inbound_messages", inboundMessages),
		logx.Int64("inbound_bytes", inboundBytes),
		logx.Int64("outbound_messages", outboundMessages),
		logx.Int64("outbound_bytes", outboundBytes),
	}
	if requestID != "" {
		fields = append(fields, logx.String("request_id", requestID))
	}
	if sessionID != "" {
		fields = append(fields, logx.String("session_id", sessionID))
	}
	return fields
}

func ReadJSON(conn *websocket.Conn, v any) ([]byte, error) {
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if messageType != websocket.TextMessage {
		return nil, ErrTextFrameRequired
	}
	if err := json.Unmarshal(payload, v); err != nil {
		return nil, err
	}
	return payload, nil
}

func WriteJSON(conn WriteConn, v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func StartPingLoop(conn *websocket.Conn, interval, writeTimeout time.Duration) chan struct{} {
	stop := make(chan struct{})
	if conn == nil || interval <= 0 {
		return stop
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				deadline := time.Now().Add(writeTimeout)
				if writeTimeout <= 0 {
					deadline = time.Time{}
				}
				if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					return
				}
			}
		}
	}()

	return stop
}

func DisconnectLogFields(err error) []logx.Field {
	if errors.Is(err, ErrSinkBackpressure) {
		return []logx.Field{logx.String("reason", "backpressure")}
	}
	if err == nil {
		return []logx.Field{logx.String("reason", "client_closed")}
	}
	fields := make([]logx.Field, 0, 4)
	reason := "read_error"
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		reason = "client_closed"
	}
	fields = append(fields, logx.String("reason", reason))

	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		fields = append(fields, logx.Int("close_code", closeErr.Code))
		if closeErr.Text != "" {
			fields = append(fields, logx.String("close_text", closeErr.Text))
		}
	}

	return fields
}
