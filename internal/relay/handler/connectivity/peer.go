package connectivity

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/config"
)

type wsPeer struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func newWSPeer(conn *websocket.Conn) *wsPeer {
	return &wsPeer{conn: conn}
}

func (p *wsPeer) SendJSON(value any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.conn.SetWriteDeadline(time.Now().Add(config.RelayClientPingWriteTimeout())); err != nil {
		return err
	}
	return p.conn.WriteJSON(value)
}

func (p *wsPeer) Close() error {
	return p.conn.Close()
}
