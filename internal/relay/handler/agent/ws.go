package agent

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/logx"
	"yuanbohan/tunnel/internal/protocol"
	relaydevice "yuanbohan/tunnel/internal/relay/device"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
	"yuanbohan/tunnel/internal/relay/session"
)

var (
	allowAllOrigins         = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	errInvalidAgentRegister = errString("invalid agent register frame")
)

type errString string

func (e errString) Error() string { return string(e) }

func Handle(registry *session.Registry, deviceRegistry *relaydevice.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		authenticated := middleware.AuthenticatedAgent(c)

		conn, err := allowAllOrigins.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			logWSUpgradeFailed(c.Request, "agent")
			return
		}
		defer conn.Close()

		tracker := handlerws.NewTracker(c.Request.URL.Path, c.Request.RemoteAddr, httpx.RequestIDFromRequest(c.Request))
		fields := []logx.Field{
			logx.String("path", c.Request.URL.Path),
			logx.String("remote_addr", c.Request.RemoteAddr),
			logx.Int64("user_id", authenticated.User.ID),
			logx.String("agent_token_id", authenticated.Token.ID),
		}
		if requestID := httpx.RequestIDFromRequest(c.Request); requestID != "" {
			fields = append(fields, logx.String("request_id", requestID))
		}
		logx.Info("agent_ws_connected", fields...)

		var loopErr error
		defer func() {
			fields := tracker.SummaryFields(time.Now())
			fields = append(fields, handlerws.DisconnectLogFields(tracker.DisconnectError(loopErr))...)
			logx.Info("agent_disconnected", fields...)
		}()

		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(config.RelayAgentReadTimeout()))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(config.RelayAgentReadTimeout()))
		})

		var register protocol.AgentFrame
		payload, err := handlerws.ReadJSON(conn, &register)
		if err != nil {
			loopErr = err
			return
		}
		tracker.RecordInbound(len(payload))
		if register.Type != "register" || register.Session == nil {
			loopErr = errInvalidAgentRegister
			return
		}

		peer := newWSAgentPeer(conn, tracker)
		sessionOwner := session.SessionOwner{
			UserID:       authenticated.User.ID,
			AgentTokenID: authenticated.Token.ID,
		}
		registry.RegisterOwned(*register.Session, sessionOwner, peer)
		if deviceRegistry != nil && register.LaunchRequestID != "" {
			deviceOwner := relaydevice.DeviceOwner{
				UserID:       authenticated.User.ID,
				AgentTokenID: authenticated.Token.ID,
			}
			deviceRegistry.CompleteLaunchIfOwner(register.LaunchRequestID, deviceOwner, register.Session.SessionID)
		}
		defer registry.DisconnectIfOwner(register.Session.SessionID, peer)

		tracker.SetSessionID(register.Session.SessionID)
		fields = []logx.Field{
			logx.String("session_id", register.Session.SessionID),
			logx.String("launcher", register.Session.Launcher),
			logx.String("label", register.Session.Label),
			logx.String("cwd", register.Session.CWD),
			logx.String("launch_request_id", register.LaunchRequestID),
			logx.Int64("user_id", authenticated.User.ID),
			logx.String("agent_token_id", authenticated.Token.ID),
		}
		if requestID := httpx.RequestIDFromRequest(c.Request); requestID != "" {
			fields = append(fields, logx.String("request_id", requestID))
		}
		logx.Info("agent_registered", fields...)

		stopPings := handlerws.StartPingLoop(conn, config.RelayAgentPingInterval(), config.RelayAgentPingWriteTimeout())
		defer close(stopPings)

		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				loopErr = err
				return
			}
			tracker.RecordInbound(len(payload))

			switch messageType {
			case websocket.BinaryMessage:
				packet, err := protocol.DecodeAttachPacket(payload)
				if err != nil {
					continue
				}
				registry.RouteTerminalBytesIfOwner(register.Session.SessionID, peer, packet)
			case websocket.TextMessage:
				var frame protocol.AgentFrame
				if err := json.Unmarshal(payload, &frame); err != nil {
					continue
				}
				switch frame.Type {
				case "resize":
					if frame.Cols > 0 && frame.Rows > 0 {
						registry.RouteResizeIfOwner(register.Session.SessionID, peer, frame.Cols, frame.Rows)
					}
				case "attach_ready":
					if frame.ClientID != "" && frame.Cols > 0 && frame.Rows > 0 {
						registry.RouteAttachReadyIfOwner(register.Session.SessionID, peer, frame.ClientID, frame.Cols, frame.Rows)
					}
				case "snapshot_done":
					if frame.ClientID != "" {
						registry.RouteSnapshotDoneIfOwner(register.Session.SessionID, peer, frame.ClientID)
					}
				case "attach_close":
					if frame.ClientID != "" {
						registry.RouteAttachCloseIfOwner(register.Session.SessionID, peer, frame.ClientID, frame.Reason)
					}
				}
			}
		}
	}
}

func logWSUpgradeFailed(r *http.Request, role string) {
	fields := []logx.Field{
		logx.String("path", r.URL.Path),
		logx.String("role", role),
	}
	fields = append(fields, httpx.RequestLogFields(r)...)
	logx.Warn("ws_upgrade_failed", fields...)
}
