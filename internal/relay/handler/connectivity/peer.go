package connectivity

import (
	"sync"

	"github.com/gorilla/websocket"
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
	return p.conn.WriteJSON(value)
}

func (p *wsPeer) Close() error {
	return p.conn.Close()
}
