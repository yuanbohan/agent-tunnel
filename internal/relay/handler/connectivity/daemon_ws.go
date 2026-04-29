package connectivity

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/protocol"
	relayconnectivity "yuanbohan/tunnel/internal/relay/connectivity"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
)

func Daemon(registry *relayconnectivity.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		authenticated := middleware.AuthenticatedAgent(c)

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(config.RelayAgentReadTimeout()))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(config.RelayAgentReadTimeout()))
		})

		var register protocol.ConnectivityFrame
		if _, err := handlerws.ReadJSON(conn, &register); err != nil {
			return
		}
		if register.Type != "daemon_register" || register.ProtocolVersion != protocol.ConnectivityProtocolVersion || register.Daemon == nil {
			_ = conn.WriteJSON(protocol.ConnectivityErrorFrame(register.RequestID, "invalid_register"))
			return
		}
		if strings.TrimSpace(register.Daemon.DeviceID) == "" || strings.TrimSpace(register.Daemon.DaemonFingerprint) == "" {
			_ = conn.WriteJSON(protocol.ConnectivityErrorFrame(register.RequestID, "invalid_register"))
			return
		}

		peer := newWSPeer(conn)
		owner := relayconnectivity.DaemonOwner{
			UserID:       authenticated.User.ID,
			AgentTokenID: authenticated.Token.ID,
		}
		registry.RegisterDaemon(owner, *register.Daemon, register.TrustedDevices, peer)
		defer registry.DisconnectDaemon(register.Daemon.DeviceID, peer)

		stopPings := handlerws.StartPingLoop(conn, config.RelayAgentPingInterval(), config.RelayAgentPingWriteTimeout())
		defer close(stopPings)

		for {
			var frame protocol.ConnectivityFrame
			if _, err := handlerws.ReadJSON(conn, &frame); err != nil {
				return
			}
			switch frame.Type {
			case "paired_device_revoked":
				if frame.AndroidFingerprint == "" {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "invalid_device_fingerprint"))
					continue
				}
				registry.RevokeTrustedAndroid(register.Daemon.DeviceID, peer, frame.AndroidFingerprint)
			case "pair_completed":
				if frame.AndroidFingerprint == "" {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "invalid_device_fingerprint"))
					continue
				}
				registry.CompletePairing(register.Daemon.DeviceID, peer, protocol.ConnectivityTrustedAndroid{
					Fingerprint: frame.AndroidFingerprint,
				})
			case "pair_invitation_reserve":
				if frame.RequestID == "" {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "invalid_pairing_correlation"))
					continue
				}
				if !registry.ReservePairing(register.Daemon.DeviceID, owner, peer, frame.RequestID, 5*time.Minute) {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "invalid_pairing_correlation"))
					continue
				}
				_ = peer.SendJSON(protocol.ConnectivityPairInvitationReservedFrame(frame.RequestID, strconv.FormatInt(authenticated.User.ID, 10)))
			default:
				_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "unsupported_event"))
			}
		}
	}
}
