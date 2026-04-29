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
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func App(registry *relayconnectivity.Registry, appAuth *relayauth.AppAuthService) gin.HandlerFunc {
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
		snapshot, ok := registry.RegisterAppIfValid(relayconnectivity.AppOwner{
			UserID:            app.User.ID,
			AppSessionID:      app.Session.ID,
			DeviceFingerprint: app.Session.DeviceFingerprint,
			SessionCreatedAt:  app.Session.CreatedAt,
		}, peer, func() bool {
			return appAuthStillValid(c, appAuth, app)
		})
		if !ok {
			return
		}
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
				if !appAuthStillValid(c, appAuth, app) {
					return
				}
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
				if err := registry.ForwardPairingResponseFromApp(relayconnectivity.AppOwner{
					UserID:            app.User.ID,
					AppSessionID:      app.Session.ID,
					DeviceFingerprint: app.Session.DeviceFingerprint,
					SessionCreatedAt:  app.Session.CreatedAt,
				}, peer, frame.PairingResponse.CorrelationID, *frame.PairingResponse); err != nil {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "pairing_correlation_not_found"))
				}
			case "relay_tunnel_request":
				if !appAuthStillValid(c, appAuth, app) {
					return
				}
				ready, err := registry.RequestRelayTunnelFromApp(relayconnectivity.AppOwner{
					UserID:            app.User.ID,
					AppSessionID:      app.Session.ID,
					DeviceFingerprint: app.Session.DeviceFingerprint,
					SessionCreatedAt:  app.Session.CreatedAt,
				}, peer, frame.DaemonID, frame.AttemptID, frame.RequestID, 30*time.Second)
				if err != nil {
					if err == relayconnectivity.ErrRelayTunnelRateLimited {
						_ = peer.SendJSON(protocol.ConnectivityErrorFrameWithRetryAfter(frame.RequestID, "relay_rate_limited", int(relayconnectivity.RelayTunnelRequestWindow.Seconds())))
						continue
					}
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "relay_tunnel_unavailable"))
					continue
				}
				_ = peer.SendJSON(ready)
			case "rendezvous_open":
				if !appAuthStillValid(c, appAuth, app) {
					return
				}
				if _, err := registry.OpenRendezvousFromApp(relayconnectivity.AppOwner{
					UserID:            app.User.ID,
					AppSessionID:      app.Session.ID,
					DeviceFingerprint: app.Session.DeviceFingerprint,
					SessionCreatedAt:  app.Session.CreatedAt,
				}, peer, frame.DaemonID, frame.AttemptID, frame.RequestID, frame.PublicUDPAddr, frame.PrivateUDPAddrs, 30*time.Second); err != nil {
					if err == relayconnectivity.ErrRendezvousRateLimited {
						_ = peer.SendJSON(protocol.ConnectivityErrorFrameWithRetryAfter(frame.RequestID, "relay_rate_limited", int(relayconnectivity.RelayTunnelRequestWindow.Seconds())))
						continue
					}
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "rendezvous_unavailable"))
				}
			case "rendezvous_close":
				if !appAuthStillValid(c, appAuth, app) {
					return
				}
				if !registry.CloseRendezvousFromApp(relayconnectivity.AppOwner{
					UserID:            app.User.ID,
					AppSessionID:      app.Session.ID,
					DeviceFingerprint: app.Session.DeviceFingerprint,
					SessionCreatedAt:  app.Session.CreatedAt,
				}, peer, frame.DaemonID, frame.AttemptID) {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "rendezvous_unavailable"))
				}
			default:
				_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "unsupported_event"))
			}
		}
	}
}

func appAuthStillValid(c *gin.Context, appAuth *relayauth.AppAuthService, authenticated relayauth.AuthenticatedApp) bool {
	if appAuth == nil {
		return false
	}
	token, ok := httpx.BearerTokenFromRequest(c.Request)
	if !ok {
		return false
	}
	current, err := appAuth.AuthenticateAccessToken(c.Request.Context(), token)
	if err != nil {
		return false
	}
	return current.User.ID == authenticated.User.ID &&
		current.Session.ID == authenticated.Session.ID &&
		current.Session.DeviceFingerprint == authenticated.Session.DeviceFingerprint
}
