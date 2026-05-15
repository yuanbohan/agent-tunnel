package agent

import (
	"encoding/json"
	"net/http"
	"strings"
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
		sessionInfo := *register.Session
		sessionInfo.LaunchSource = protocol.SessionLaunchSourceLocal
		launchRequestID := ""
		if register.LaunchContext != nil && register.LaunchContext.Source == protocol.SessionLaunchSourceMobile {
			launchRequestID = strings.TrimSpace(register.LaunchContext.RequestID)
		}
		registry.RegisterOwned(sessionInfo, sessionOwner, peer)
		defer registry.DisconnectIfOwner(register.Session.SessionID, peer)

		tracker.SetSessionID(register.Session.SessionID)
		fields = []logx.Field{
			logx.String("session_id", register.Session.SessionID),
			logx.String("launcher", register.Session.Launcher),
			logx.String("label", register.Session.Label),
			logx.String("cwd", register.Session.CWD),
			logx.String("launch_request_id", launchRequestID),
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
				continue
			case websocket.TextMessage:
				var frame protocol.AgentFrame
				if err := json.Unmarshal(payload, &frame); err != nil {
					continue
				}
				switch frame.Type {
				case "launch_ready":
					if deviceRegistry != nil && frame.LaunchContext != nil && frame.LaunchContext.Source == protocol.SessionLaunchSourceMobile {
						requestID := strings.TrimSpace(frame.LaunchContext.RequestID)
						if requestID != "" {
							deviceOwner := relaydevice.DeviceOwner{
								UserID:       authenticated.User.ID,
								AgentTokenID: authenticated.Token.ID,
							}
							if _, ok := deviceRegistry.CompleteLaunchIfOwner(requestID, deviceOwner, register.Session.SessionID); ok {
								registry.SetLaunchSourceForUser(register.Session.SessionID, authenticated.User.ID, protocol.SessionLaunchSourceMobile)
							}
						}
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
