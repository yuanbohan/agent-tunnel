package connectivity

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/protocol"
	relayauth "yuanbohan/tunnel/internal/relay/auth"
	relayconnectivity "yuanbohan/tunnel/internal/relay/connectivity"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
)

func Daemon(registry *relayconnectivity.Registry, agentTokens *relayauth.AgentTokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authenticated := middleware.AuthenticatedAgent(c)

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		peer := newWSPeer(conn)
		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(config.RelayAgentReadTimeout()))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(config.RelayAgentReadTimeout()))
		})

		var register protocol.ConnectivityFrame
		if _, err := handlerws.ReadJSON(conn, &register); err != nil {
			return
		}
		if register.Type != "computer_register" || register.ProtocolVersion != protocol.ConnectivityProtocolVersion || register.Daemon == nil {
			_ = peer.SendJSON(protocol.ConnectivityErrorFrame(register.RequestID, "invalid_register"))
			return
		}
		if strings.TrimSpace(register.Daemon.DeviceID) == "" || strings.TrimSpace(register.Daemon.DaemonFingerprint) == "" {
			_ = peer.SendJSON(protocol.ConnectivityErrorFrame(register.RequestID, "invalid_register"))
			return
		}
		owner := relayconnectivity.DaemonOwner{
			UserID:       authenticated.User.ID,
			AgentTokenID: authenticated.Token.ID,
		}
		if _, ok := registry.RegisterDaemonIfValidWithDirectSessions(owner, *register.Daemon, register.TrustedDevices, register.DirectSessions, peer, func() bool {
			return agentAuthStillValid(c, agentTokens, authenticated)
		}); !ok {
			return
		}
		defer registry.DisconnectDaemon(register.Daemon.DeviceID, peer)

		stopPings := handlerws.StartPingLoop(conn, config.RelayAgentPingInterval(), config.RelayAgentPingWriteTimeout())
		defer close(stopPings)

		for {
			var frame protocol.ConnectivityFrame
			if _, err := handlerws.ReadJSON(conn, &frame); err != nil {
				return
			}
			if !agentAuthStillValid(c, agentTokens, authenticated) {
				return
			}
			switch frame.Type {
			case "client_revoked":
				if frame.AndroidFingerprint == "" {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "invalid_client_fingerprint"))
					continue
				}
				registry.RevokeTrustedAndroid(register.Daemon.DeviceID, peer, frame.AndroidFingerprint)
			case "pair_completed":
				if frame.AndroidFingerprint == "" {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "invalid_client_fingerprint"))
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
			case "rendezvous_hint":
				if err := registry.ForwardRendezvousHintFromDaemon(owner, register.Daemon.DeviceID, peer, frame.AttemptID, frame.RequestID, frame.AndroidFingerprint, frame.PublicUDPAddr, frame.PrivateUDPAddrs); err != nil {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "rendezvous_unavailable"))
				}
			case "rendezvous_close":
				if !registry.CloseRendezvousFromDaemon(owner, register.Daemon.DeviceID, peer, frame.AttemptID, frame.AndroidFingerprint) {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "rendezvous_unavailable"))
				}
			case "direct_session_open":
				if !registry.OpenDirectSessionFromDaemon(owner, register.Daemon.DeviceID, peer, frame.AttemptID, frame.RequestID, frame.AndroidFingerprint) {
					_ = peer.SendJSON(protocol.ConnectivityDirectSessionCloseFrame(frame.RequestID, frame.AttemptID, register.Daemon.DeviceID, frame.AndroidFingerprint))
				}
			case "direct_session_close":
				if !registry.CloseDirectSessionFromDaemon(owner, register.Daemon.DeviceID, peer, frame.AttemptID, frame.AndroidFingerprint) {
					_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "direct_session_unavailable"))
				}
			default:
				_ = peer.SendJSON(protocol.ConnectivityErrorFrame(frame.RequestID, "unsupported_event"))
			}
		}
	}
}

func agentAuthStillValid(c *gin.Context, agentTokens *relayauth.AgentTokenService, authenticated relayauth.AuthenticatedAgentToken) bool {
	if agentTokens == nil {
		return false
	}
	token, ok := httpx.BearerTokenFromRequest(c.Request)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), connectivityAuthRevalidationTimeout)
	defer cancel()
	current, err := agentTokens.Authenticate(ctx, token)
	if err != nil {
		return false
	}
	return current.User.ID == authenticated.User.ID && current.Token.ID == authenticated.Token.ID
}
