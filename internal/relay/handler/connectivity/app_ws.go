package connectivity

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/protocol"
	relayauth "yuanbohan/tunnel/internal/relay/auth"
	relayconnectivity "yuanbohan/tunnel/internal/relay/connectivity"
	"yuanbohan/tunnel/internal/relay/handler/api"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func App(registry *relayconnectivity.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		app := middleware.AuthenticatedApp(c)
		if app.Session.DeviceFingerprint == "" {
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

		var register protocol.ConnectivityFrame
		if _, err := handlerws.ReadJSON(conn, &register); err != nil {
			return
		}
		if register.Type != "app_register" || register.ProtocolVersion != protocol.ConnectivityProtocolVersion {
			_ = conn.WriteJSON(protocol.ConnectivityErrorFrame(register.RequestID, "invalid_register"))
			return
		}

		peer := newWSPeer(conn)
		snapshot := registry.RegisterApp(relayconnectivity.AppOwner{
			UserID:            app.User.ID,
			AppSessionID:      app.Session.ID,
			DeviceFingerprint: app.Session.DeviceFingerprint,
		}, peer)
		defer registry.DisconnectApp(peer)
		if err := peer.SendJSON(protocol.ConnectivityDaemonSnapshotFrame(snapshot)); err != nil {
			return
		}

		stopPings := handlerws.StartPingLoop(conn, config.RelayClientPingInterval(), config.RelayClientPingWriteTimeout())
		defer close(stopPings)

		for {
			var frame protocol.ConnectivityFrame
			if _, err := handlerws.ReadJSON(conn, &frame); err != nil {
				return
			}
			switch frame.Type {
			case "pair_response_submit":
				if frame.PairingResponse == nil {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "invalid_pairing_response"))
					continue
				}
				androidFingerprint, err := relayauth.NormalizeDeviceFingerprint(frame.PairingResponse.AndroidFingerprint)
				if err != nil || androidFingerprint != app.Session.DeviceFingerprint {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "device_fingerprint_mismatch"))
					continue
				}
				if frame.PairingResponse.AccountID != strconv.FormatInt(app.User.ID, 10) {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "pairing_account_mismatch"))
					continue
				}
				if err := registry.ForwardPairingResponse(app.User.ID, frame.PairingResponse.CorrelationID, *frame.PairingResponse); err != nil {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "pairing_correlation_not_found"))
				}
			default:
				_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "unsupported_event"))
			}
		}
	}
}
