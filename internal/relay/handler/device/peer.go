package device

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/config"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
)

type wsDevicePeer struct {
	conn    deviceWSConn
	tracker *handlerws.Tracker
	mu      sync.Mutex
	writeMu sync.Mutex
	active  bool
}

type deviceWSConn interface {
	WriteMessage(messageType int, data []byte) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

func newWSDevicePeer(conn *websocket.Conn, tracker *handlerws.Tracker) *wsDevicePeer {
	return &wsDevicePeer{
		conn:    conn,
		tracker: tracker,
		active:  true,
	}
}

func (p *wsDevicePeer) SendJSON(v any) error {
	p.mu.Lock()
	if !p.active {
		p.mu.Unlock()
		return handlerws.ErrSinkClosed
	}
	conn := p.conn
	tracker := p.tracker
	p.mu.Unlock()

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	writeTimeout := config.RelayWSWriteTimeout()
	if writeTimeout > 0 {
		if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
			if tracker != nil {
				tracker.NoteDisconnectError(err)
			}
			return err
		}
	}
	payload, err := handlerws.WriteJSON(conn, v)
	if err != nil {
		if tracker != nil {
			tracker.NoteDisconnectError(err)
		}
		return err
	}
	if tracker != nil {
		tracker.RecordOutbound(len(payload))
	}
	return nil
}

func (p *wsDevicePeer) Close() error {
	p.mu.Lock()
	p.active = false
	conn := p.conn
	p.mu.Unlock()
	return conn.Close()
}

func (p *wsDevicePeer) Deactivate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active = false
}
