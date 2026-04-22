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
	"yuanbohan/tunnel/internal/relay/auth"
	relaydevice "yuanbohan/tunnel/internal/relay/device"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
	relaysession "yuanbohan/tunnel/internal/relay/session"
)

var allowAllDeviceOrigins = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

type errString string

func (e errString) Error() string { return string(e) }

var errInvalidDeviceRegister = errString("invalid device register frame")

func Handle(registry *relaydevice.Registry, sessionRegistry *relaysession.Registry, agentTokens *auth.AgentTokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authenticated := middleware.AuthenticatedAgent(c)
		token, ok := httpx.BearerTokenFromRequest(c.Request)
		if !ok {
			return
		}

		conn, err := allowAllDeviceOrigins.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		tracker := handlerws.NewTracker(c.Request.URL.Path, c.Request.RemoteAddr, httpx.RequestIDFromRequest(c.Request))
		peer := newWSDevicePeer(conn, tracker)
		owner := relaydevice.DeviceOwner{
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

		reauthenticated, err := agentTokens.Authenticate(c.Request.Context(), token)
		if err != nil {
			return
		}
		if reauthenticated.User.ID != authenticated.User.ID || reauthenticated.Token.ID != authenticated.Token.ID {
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
				if completion, ok := registry.ResolveLaunchIfOwner(register.Device.DeviceID, peer, frame.RequestID, frame.Status, frame.Reason, frame.WorkspaceSession); ok && completion.SessionID != "" && sessionRegistry != nil {
					sessionRegistry.SetLaunchSourceForUser(completion.SessionID, authenticated.User.ID, protocol.SessionLaunchSourceMobile)
				}
			case "terminate_result":
				registry.ResolveTerminateIfOwner(register.Device.DeviceID, peer, frame.RequestID, frame.Status, frame.Reason)
			case "update":
				if frame.Device == nil {
					continue
				}
				incomingID := strings.TrimSpace(frame.Device.DeviceID)
				if incomingID == "" || incomingID != register.Device.DeviceID {
					continue
				}
				registry.UpdateIfOwner(register.Device.DeviceID, peer, *frame.Device)
			}
		}
	}
}
