package device

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/protocol"
	relaysession "yuanbohan/tunnel/internal/relay/device"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
)

var allowAllDeviceOrigins = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

type errString string

func (e errString) Error() string { return string(e) }

var errInvalidDeviceRegister = errString("invalid device register frame")

func Handle(registry *relaysession.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		authenticated := middleware.AuthenticatedAgent(c)

		conn, err := allowAllDeviceOrigins.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		tracker := handlerws.NewTracker(c.Request.URL.Path, c.Request.RemoteAddr, httpx.RequestIDFromRequest(c.Request))
		peer := newWSDevicePeer(conn, tracker)
		owner := relaysession.DeviceOwner{
			UserID:       authenticated.User.ID,
			AgentTokenID: authenticated.Token.ID,
		}
		registry.RegisterPending(owner, peer)
		defer registry.DisconnectPending(peer)
		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(config.RelayAgentReadTimeout()))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(config.RelayAgentReadTimeout()))
		})

		var register protocol.DeviceFrame
		if _, err := handlerws.ReadJSON(conn, &register); err != nil {
			return
		}
		if register.Type != "register" || register.Device == nil {
			return
		}
		if strings.TrimSpace(register.Device.DeviceID) == "" {
			return
		}

		if !registry.ActivatePending(*register.Device, owner, peer) {
			return
		}
		defer registry.DisconnectIfOwner(register.Device.DeviceID, peer)

		stopPings := handlerws.StartPingLoop(conn, config.RelayAgentPingInterval(), config.RelayAgentPingWriteTimeout())
		defer close(stopPings)

		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			tracker.RecordInbound(len(payload))
			if messageType != websocket.TextMessage {
				continue
			}
			var frame protocol.DeviceFrame
			if err := json.Unmarshal(payload, &frame); err != nil {
				continue
			}
			switch frame.Type {
			case "launch_result":
				registry.ResolveLaunchIfOwner(register.Device.DeviceID, peer, frame.RequestID, frame.Status, frame.Reason)
			}
		}
	}
}
