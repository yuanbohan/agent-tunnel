package daemon

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
	"yuanbohan/tunnel/internal/buildinfo"
	"yuanbohan/tunnel/internal/connectivity/carrier"
	connidentity "yuanbohan/tunnel/internal/connectivity/identity"
	connectivitypairing "yuanbohan/tunnel/internal/connectivity/pairing"
	conntransport "yuanbohan/tunnel/internal/connectivity/transport"
	"yuanbohan/tunnel/internal/protocol"
)

const (
	connectivityConnectorReadTimeout      = 30 * time.Second
	connectivityConnectorPingInterval     = 10 * time.Second
	connectivityConnectorPingWriteTimeout = 5 * time.Second
	connectivityRelayTunnelAcceptTimeout  = 10 * time.Second
)

type connectivityConnector struct {
	baseURL string
	token   string
	paths   Paths
	state   *runtimeState
	dialer  *websocket.Dialer
}

func newConnectivityConnector(baseURL, token string, paths Paths, state *runtimeState) *connectivityConnector {
	return &connectivityConnector{
		baseURL: baseURL,
		token:   token,
		paths:   paths,
		state:   state,
		dialer:  websocket.DefaultDialer,
	}
}

func (c *connectivityConnector) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := c.serveOnce(ctx); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 15*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

func (c *connectivityConnector) serveOnce(ctx context.Context) error {
	wsURL, err := connectivityDaemonWebSocketURL(c.baseURL)
	if err != nil {
		return err
	}
	headers := http.Header{"Authorization": {"Bearer " + c.token}}
	conn, _, err := c.dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(connectivityConnectorReadTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(connectivityConnectorReadTimeout))
	})
	stopPings := startConnectivityConnectorPingLoop(conn)
	defer close(stopPings)

	register, err := c.registerFrame()
	if err != nil {
		return err
	}
	if err := writeConnectivityConnectorJSON(conn, register); err != nil {
		return err
	}

	readErr := make(chan error, 1)
	go func() {
		for {
			var frame protocol.ConnectivityFrame
			if err := conn.ReadJSON(&frame); err != nil {
				readErr <- err
				return
			}
			switch frame.Type {
			case "pair_invitation_reserved", "error":
				c.state.deliverConnectivityReply(frame)
			case "pair_response_forward":
				_ = c.handlePairResponseForward(frame)
			case "relay_tunnel_ready":
				go func(frame protocol.ConnectivityFrame) {
					if err := c.handleRelayTunnelReady(ctx, frame); err != nil {
						c.state.setLastFailure("relay_tunnel_failed", false)
					}
				}(frame)
			default:
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			return err
		case frame := <-c.state.connectivityEvents:
			if !c.shouldForwardConnectivityEvent(frame) {
				continue
			}
			if err := writeConnectivityConnectorJSON(conn, frame); err != nil {
				return err
			}
		}
	}
}

func (c *connectivityConnector) shouldForwardConnectivityEvent(frame protocol.ConnectivityFrame) bool {
	if frame.Type != "pair_completed" {
		return true
	}
	fingerprint := strings.ToLower(strings.TrimSpace(frame.AndroidFingerprint))
	if fingerprint == "" {
		return false
	}
	devices, err := ListTrustedAndroidDevices(c.paths)
	if err != nil {
		return false
	}
	for _, device := range devices {
		if device.Fingerprint == fingerprint {
			return true
		}
	}
	return false
}

func (c *connectivityConnector) handlePairResponseForward(frame protocol.ConnectivityFrame) error {
	if frame.PairingResponse == nil {
		return nil
	}
	_, err := StorePendingPairingResponse(c.paths, connectivitypairing.AndroidResponse{
		Version:            connectivitypairing.Version,
		AccountID:          frame.PairingResponse.AccountID,
		InvitationID:       frame.PairingResponse.InvitationID,
		CorrelationID:      frame.PairingResponse.CorrelationID,
		AndroidPublicKey:   frame.PairingResponse.AndroidPublicKey,
		AndroidFingerprint: frame.PairingResponse.AndroidFingerprint,
		AndroidDisplayName: frame.PairingResponse.AndroidDisplayName,
		Signature:          frame.PairingResponse.Signature,
	}, time.Now().UTC())
	return err
}

func (c *connectivityConnector) handleRelayTunnelReady(ctx context.Context, frame protocol.ConnectivityFrame) error {
	if frame.Actor != "daemon" || strings.TrimSpace(frame.TunnelToken) == "" {
		return nil
	}
	android, err := c.trustedAndroidDevice(frame.AndroidFingerprint)
	if err != nil {
		return err
	}
	androidPublicKey, err := decodeTrustedAndroidPublicKey(android.PublicKey)
	if err != nil {
		return err
	}
	daemonIdentity, err := ReadConnectivityIdentity(c.paths)
	if err != nil {
		return err
	}
	daemonCert, err := connidentity.SelfSignedCertificate(daemonIdentity.PrivateKey, connidentity.CertificateOptions{})
	if err != nil {
		return err
	}
	tunnelURL, err := connectivityTunnelWebSocketURL(c.baseURL, frame.TunnelToken)
	if err != nil {
		return err
	}
	conn, _, err := c.dialer.DialContext(ctx, tunnelURL, nil)
	if err != nil {
		return err
	}
	packetConn := carrier.NewWSPacketConn(conn, "daemon", "android", 0)
	defer packetConn.Close()

	listener, err := quic.Listen(packetConn, conntransport.DaemonTLSConfig(conntransport.EndpointConfig{
		Certificate:         daemonCert,
		PinnedPeerPublicKey: androidPublicKey,
	}), conntransport.QUICConfig())
	if err != nil {
		return err
	}
	defer listener.Close()
	acceptCtx, cancelAccept := context.WithTimeout(ctx, connectivityRelayTunnelAcceptTimeout)
	defer cancelAccept()
	stopAcceptCleanup := context.AfterFunc(acceptCtx, func() {
		_ = listener.Close()
		_ = packetConn.Close()
	})
	defer stopAcceptCleanup()

	quicConn, err := listener.Accept(acceptCtx)
	if err != nil {
		return err
	}
	_ = stopAcceptCleanup()
	cancelAccept()
	defer quicConn.CloseWithError(0, "done")
	transport := &ConnectivityTransport{
		Broker:             c.state.broker,
		DaemonFingerprint:  daemonIdentity.Fingerprint,
		AndroidFingerprint: android.Fingerprint,
		PathKind:           "relay",
	}
	return transport.Serve(ctx, quicConn)
}

func writeConnectivityConnectorJSON(conn *websocket.Conn, value any) error {
	if conn == nil {
		return fmt.Errorf("nil websocket connection")
	}
	deadline := time.Now().Add(connectivityConnectorPingWriteTimeout)
	if connectivityConnectorPingWriteTimeout <= 0 {
		deadline = time.Time{}
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return conn.WriteJSON(value)
}

func startConnectivityConnectorPingLoop(conn *websocket.Conn) chan struct{} {
	stop := make(chan struct{})
	if conn == nil || connectivityConnectorPingInterval <= 0 {
		return stop
	}
	go func() {
		ticker := time.NewTicker(connectivityConnectorPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				deadline := time.Now().Add(connectivityConnectorPingWriteTimeout)
				if connectivityConnectorPingWriteTimeout <= 0 {
					deadline = time.Time{}
				}
				if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					return
				}
			}
		}
	}()
	return stop
}

func (c *connectivityConnector) trustedAndroidDevice(fingerprint string) (TrustedAndroidDevice, error) {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		return TrustedAndroidDevice{}, ErrTrustedDeviceNotFound
	}
	devices, err := ListTrustedAndroidDevices(c.paths)
	if err != nil {
		return TrustedAndroidDevice{}, err
	}
	for _, device := range devices {
		if device.Fingerprint == fingerprint {
			return device, nil
		}
	}
	return TrustedAndroidDevice{}, ErrTrustedDeviceNotFound
}

func (c *connectivityConnector) registerFrame() (protocol.ConnectivityFrame, error) {
	identity, err := ReadConnectivityIdentity(c.paths)
	if err != nil {
		return protocol.ConnectivityFrame{}, err
	}
	devices, err := ListTrustedAndroidDevices(c.paths)
	if err != nil {
		return protocol.ConnectivityFrame{}, err
	}
	trusted := make([]protocol.ConnectivityTrustedAndroid, 0, len(devices))
	for _, device := range devices {
		trusted = append(trusted, protocol.ConnectivityTrustedAndroid{
			Fingerprint: device.Fingerprint,
			DisplayName: device.DisplayName,
		})
	}
	status := c.state.snapshot()
	return protocol.ConnectivityDaemonRegisterFrame(protocol.ConnectivityDaemonInfo{
		DeviceID:          status.DeviceID,
		DisplayName:       status.DisplayName,
		PlatformFamily:    status.PlatformFamily,
		PlatformID:        status.PlatformID,
		DaemonPublicKey:   fmt.Sprintf("%x", identity.PublicKey),
		DaemonFingerprint: identity.Fingerprint,
		TunnelVersion:     buildinfo.Version,
	}, trusted), nil
}

func connectivityDaemonWebSocketURL(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported base URL scheme: %s", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/connectivity/daemon/ws"
	return parsed.String(), nil
}

func connectivityTunnelWebSocketURL(baseURL, token string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported base URL scheme: %s", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/connectivity/tunnel/ws"
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func decodeTrustedAndroidPublicKey(raw string) (ed25519.PublicKey, error) {
	decoded, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid android public key length")
	}
	return ed25519.PublicKey(decoded), nil
}
