package connectivity

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/config"
	relayconnectivity "yuanbohan/tunnel/internal/relay/connectivity"
	"yuanbohan/tunnel/internal/relay/handler/api"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
)

const tunnelPairTimeout = 30 * time.Second

type TunnelHub struct {
	mu      sync.Mutex
	waiting map[string]*tunnelEndpoint
}

func NewTunnelHub() *TunnelHub {
	return &TunnelHub{waiting: make(map[string]*tunnelEndpoint)}
}

func Tunnel(registry *relayconnectivity.Registry, hub *TunnelHub) gin.HandlerFunc {
	if hub == nil {
		hub = NewTunnelHub()
	}
	return func(c *gin.Context) {
		if registry == nil {
			api.WriteJSONError(c.Writer, http.StatusServiceUnavailable, "connectivity_unavailable")
			return
		}
		token, ok := httpx.BearerTokenFromRequest(c.Request)
		redemption, err := registry.RedeemRelayTunnelToken(token)
		if err != nil {
			api.WriteJSONError(c.Writer, http.StatusForbidden, "forbidden")
			return
		}

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(config.RelayClientReadTimeout()))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(config.RelayClientReadTimeout()))
		})
		stopPings := handlerws.StartPingLoop(conn, config.RelayClientPingInterval(), config.RelayClientPingWriteTimeout())
		defer close(stopPings)

		endpoint := newTunnelEndpoint(redemption, conn)
		removeCloser := registry.AddTunnelCloser(redemption.TunnelKey, endpoint.close)
		defer removeCloser()
		defer hub.remove(endpoint)
		defer registry.CloseRelayTunnel(redemption.TunnelKey)

		partner, ok := hub.join(endpoint)
		if !ok {
			endpoint.close()
			return
		}
		registry.MarkRelayTunnelActive(redemption.TunnelKey)
		endpoint.forwardTo(partner)
	}
}

type tunnelEndpoint struct {
	redemption relayconnectivity.TunnelRedemption
	conn       *websocket.Conn
	partnerCh  chan *tunnelEndpoint
	done       chan struct{}
	closeOnce  sync.Once
	writeMu    sync.Mutex
}

func newTunnelEndpoint(redemption relayconnectivity.TunnelRedemption, conn *websocket.Conn) *tunnelEndpoint {
	return &tunnelEndpoint{
		redemption: redemption,
		conn:       conn,
		partnerCh:  make(chan *tunnelEndpoint, 1),
		done:       make(chan struct{}),
	}
}

func (h *TunnelHub) join(endpoint *tunnelEndpoint) (*tunnelEndpoint, bool) {
	h.mu.Lock()
	if partner := h.waiting[endpoint.redemption.TunnelKey]; partner != nil && partner.redemption.Actor != endpoint.redemption.Actor {
		delete(h.waiting, endpoint.redemption.TunnelKey)
		h.mu.Unlock()
		partner.partnerCh <- endpoint
		return partner, true
	}
	h.waiting[endpoint.redemption.TunnelKey] = endpoint
	h.mu.Unlock()

	timer := time.NewTimer(tunnelPairTimeout)
	defer timer.Stop()
	select {
	case partner := <-endpoint.partnerCh:
		return partner, true
	case <-endpoint.done:
		return nil, false
	case <-timer.C:
		return nil, false
	}
}

func (h *TunnelHub) remove(endpoint *tunnelEndpoint) {
	if h == nil || endpoint == nil {
		return
	}
	h.mu.Lock()
	if h.waiting[endpoint.redemption.TunnelKey] == endpoint {
		delete(h.waiting, endpoint.redemption.TunnelKey)
	}
	h.mu.Unlock()
}

func (e *tunnelEndpoint) forwardTo(partner *tunnelEndpoint) {
	defer e.close()
	defer partner.close()

	for {
		messageType, payload, err := e.conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.BinaryMessage {
			return
		}
		if err := partner.writeBinary(payload); err != nil {
			return
		}
	}
}

func (e *tunnelEndpoint) writeBinary(payload []byte) error {
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	select {
	case <-e.done:
		return errors.New("tunnel endpoint closed")
	default:
	}
	if err := e.conn.SetWriteDeadline(time.Now().Add(config.RelayClientPingWriteTimeout())); err != nil {
		return err
	}
	return e.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (e *tunnelEndpoint) close() {
	e.closeOnce.Do(func() {
		close(e.done)
		_ = e.conn.Close()
	})
}
