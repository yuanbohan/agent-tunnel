package connectivity

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/config"
)

type wsPeer struct {
	conn       *websocket.Conn
	mu         sync.Mutex
	beforeSend func() bool
}

func newWSPeer(conn *websocket.Conn) *wsPeer {
	return &wsPeer{conn: conn}
}

func newWSPeerWithValidator(conn *websocket.Conn, beforeSend func() bool) *wsPeer {
	return &wsPeer{conn: conn, beforeSend: beforeSend}
}

func (p *wsPeer) SendJSON(value any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.beforeSend != nil && !p.beforeSend() {
		_ = p.conn.Close()
		return websocket.ErrCloseSent
	}
	if err := p.conn.SetWriteDeadline(time.Now().Add(config.RelayClientPingWriteTimeout())); err != nil {
		return err
	}
	return p.conn.WriteJSON(value)
}

func (p *wsPeer) Close() error {
	return p.conn.Close()
}
