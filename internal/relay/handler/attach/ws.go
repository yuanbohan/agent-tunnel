package attach

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/logx"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/relay/handler/api"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
	"yuanbohan/tunnel/internal/relay/session"
)

var clientUpgrader = websocket.Upgrader{CheckOrigin: checkOrigin}

func Handle(registry *session.Registry, attachSessions *session.AttachSessionIndex) gin.HandlerFunc {
	return func(c *gin.Context) {
		app := middleware.AuthenticatedApp(c)
		sessionID := c.Param("sessionID")
		if _, ok := registry.SessionForUser(sessionID, app.User.ID); !ok {
			api.WriteJSONError(c.Writer, http.StatusNotFound, "session_not_found")
			return
		}
		if !checkOrigin(c.Request) {
			api.WriteJSONError(c.Writer, http.StatusForbidden, "forbidden")
			return
		}

		clientID, err := newClientID()
		if err != nil {
			api.WriteJSONError(c.Writer, http.StatusInternalServerError, "internal_error")
			return
		}

		conn, err := clientUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			logWSUpgradeFailed(c.Request)
			return
		}
		defer conn.Close()

		tracker := handlerws.NewTracker(c.Request.URL.Path, c.Request.RemoteAddr, httpx.RequestIDFromRequest(c.Request))
		tracker.SetSessionID(sessionID)
		fields := []logx.Field{
			logx.String("path", c.Request.URL.Path),
			logx.String("remote_addr", c.Request.RemoteAddr),
			logx.String("session_id", sessionID),
			logx.Int64("user_id", app.User.ID),
		}
		if requestID := httpx.RequestIDFromRequest(c.Request); requestID != "" {
			fields = append(fields, logx.String("request_id", requestID))
		}
		logx.Info("attach_ws_connected", fields...)

		var loopErr error
		defer func() {
			fields := tracker.SummaryFields(time.Now())
			fields = append(fields, handlerws.DisconnectLogFields(tracker.DisconnectError(loopErr))...)
			logx.Info("attach_ws_disconnected", fields...)
		}()

		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(config.RelayClientReadTimeout()))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(config.RelayClientReadTimeout()))
		})

		attachPeer := newWSAttachPeer(conn, tracker)
		owner, err := registry.StartAttachForUser(sessionID, clientID, app.User.ID, attachPeer)
		if err != nil {
			loopErr = err
			_ = attachPeer.Close(reasonForAttachStartError(err))
			return
		}

		attachRef := session.AttachSessionRef{
			UserID:       app.User.ID,
			AppSessionID: app.Session.ID,
			SessionID:    sessionID,
			ClientID:     clientID,
		}
		attachSessions.Add(attachRef)
		defer attachSessions.Remove(attachRef)
		defer registry.DetachClient(sessionID, clientID, "client_closed")

		if err := owner.SendJSON(protocol.AttachOpenFrame(clientID)); err != nil {
			loopErr = err
			_ = registry.DetachClient(sessionID, clientID, "session_offline")
			return
		}

		stopPings := handlerws.StartPingLoop(conn, config.RelayClientPingInterval(), config.RelayClientPingWriteTimeout())
		defer close(stopPings)

		for {
			var msg protocol.ClientInputMessage
			payload, err := handlerws.ReadJSON(conn, &msg)
			if err != nil {
				loopErr = err
				return
			}
			tracker.RecordInbound(len(payload))

			switch msg.Type {
			case "input_text":
			case "input_key":
				if msg.Key == "" {
					continue
				}
			default:
				continue
			}

			if err := registry.WriteAttachInput(sessionID, msg.AgentFrame(clientID)); err != nil {
				loopErr = err
				return
			}
		}
	}
}

func checkOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return false
	}
	return sameOrigin(parsed.Scheme, parsed.Host, requestScheme(r), requestHost(r))
}

func requestHost(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		return strings.TrimSpace(forwarded)
	}
	return strings.TrimSpace(r.Host)
}

func requestScheme(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		return strings.ToLower(strings.TrimSpace(forwarded))
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func sameOrigin(originScheme, originHost, requestScheme, requestHost string) bool {
	if !strings.EqualFold(strings.TrimSpace(originScheme), strings.TrimSpace(requestScheme)) {
		return false
	}
	return strings.EqualFold(normalizeHost(originHost, originScheme), normalizeHost(requestHost, requestScheme))
}

func normalizeHost(host, scheme string) string {
	host = strings.TrimSpace(host)
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	parsed := &url.URL{Scheme: scheme, Host: host}
	name := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port == "" || isDefaultPort(port, scheme) {
		return name
	}
	return net.JoinHostPort(name, port)
}

func isDefaultPort(port, scheme string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}

func reasonForAttachStartError(err error) string {
	switch {
	case errors.Is(err, session.ErrSessionNotFound):
		return "session_offline"
	case errors.Is(err, session.ErrSessionOffline):
		return "session_offline"
	default:
		return "session_offline"
	}
}

func newClientID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	var raw [36]byte
	hex.Encode(raw[0:8], buf[0:4])
	raw[8] = '-'
	hex.Encode(raw[9:13], buf[4:6])
	raw[13] = '-'
	hex.Encode(raw[14:18], buf[6:8])
	raw[18] = '-'
	hex.Encode(raw[19:23], buf[8:10])
	raw[23] = '-'
	hex.Encode(raw[24:36], buf[10:16])
	return string(raw[:]), nil
}

func logWSUpgradeFailed(r *http.Request) {
	fields := []logx.Field{
		logx.String("path", r.URL.Path),
		logx.String("role", "attach"),
	}
	fields = append(fields, httpx.RequestLogFields(r)...)
	logx.Warn("ws_upgrade_failed", fields...)
}
