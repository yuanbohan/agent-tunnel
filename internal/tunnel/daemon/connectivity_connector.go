package daemon

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
	"yuanbohan/tunnel/internal/buildinfo"
	"yuanbohan/tunnel/internal/connectivity/carrier"
	"yuanbohan/tunnel/internal/connectivity/direct"
	connframe "yuanbohan/tunnel/internal/connectivity/frame"
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
	connectivityConnectorReconnectJitter  = 500 * time.Millisecond
)

type connectivityConnector struct {
	baseURL        string
	token          string
	paths          Paths
	state          *runtimeState
	dialer         *websocket.Dialer
	stunDiscover   func(context.Context, *direct.UDPSocket) (*net.UDPAddr, error)
	directMu       sync.Mutex
	directAttempts map[string]directAttemptCancel
}

type directAttemptCancel struct {
	androidFingerprint string
	cancel             context.CancelFunc
}

func newConnectivityConnector(baseURL, token string, paths Paths, state *runtimeState) *connectivityConnector {
	return &connectivityConnector{
		baseURL:        baseURL,
		token:          token,
		paths:          paths,
		state:          state,
		dialer:         websocket.DefaultDialer,
		stunDiscover:   newConnectivitySTUNDiscoverer(baseURL),
		directAttempts: make(map[string]directAttemptCancel),
	}
}

func (c *connectivityConnector) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := c.serveOnce(ctx); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitteredReconnectDelay(backoff)):
			}
			if backoff < 15*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

func jitteredReconnectDelay(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	jitter := connectivityConnectorReconnectJitter
	if base < jitter {
		jitter = base
	}
	if jitter <= 0 {
		return base
	}
	return base + time.Duration(time.Now().UnixNano()%int64(jitter))
}

func (c *connectivityConnector) serveOnce(ctx context.Context) error {
	serveCtx, cancelServe := context.WithCancel(ctx)
	defer cancelServe()
	defer c.cancelAllDirectAttempts()

	wsURL, err := connectivityDaemonWebSocketURL(c.baseURL)
	if err != nil {
		return err
	}
	headers := http.Header{"Authorization": {"Bearer " + c.token}}
	conn, _, err := c.dialer.DialContext(serveCtx, wsURL, headers)
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
	writeMu := &sync.Mutex{}

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
					if err := c.handleRelayTunnelReady(serveCtx, frame); err != nil {
						c.state.setLastFailure("relay_tunnel_failed", false)
					}
				}(frame)
			case "rendezvous_hint":
				go func(frame protocol.ConnectivityFrame) {
					err := c.handleRendezvousHint(serveCtx, frame, func(value any) error {
						writeMu.Lock()
						defer writeMu.Unlock()
						return writeConnectivityConnectorJSON(conn, value)
					})
					if err != nil {
						reason := connectivityDirectFailureReason(err)
						c.state.setConnectivityPath("direct", reason)
						c.state.setLastFailure(reason, false)
					}
				}(frame)
			case "rendezvous_close":
				c.cancelDirectAttempt(frame.AttemptID, frame.AndroidFingerprint)
				c.state.cancelPendingDirectAttempts(frame.AttemptID, frame.AndroidFingerprint)
			case "direct_session_close":
				c.state.cancelPendingDirectAttempts(frame.AttemptID, frame.AndroidFingerprint)
				c.state.cancelActiveDirectTransports(frame.AttemptID, frame.AndroidFingerprint)
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
			writeMu.Lock()
			if err := writeConnectivityConnectorJSON(conn, frame); err != nil {
				writeMu.Unlock()
				return err
			}
			writeMu.Unlock()
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

func connectivityDirectFailureReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, direct.ErrSTUNTimeout):
		return "stun_timeout"
	case errors.Is(err, direct.ErrSTUNUnexpectedResponse):
		return "stun_unavailable"
	case errors.Is(err, context.Canceled):
		return "direct_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "direct_timeout"
	default:
		return "direct_attempt_failed"
	}
}

func newConnectivitySTUNDiscoverer(baseURL string) func(context.Context, *direct.UDPSocket) (*net.UDPAddr, error) {
	serverAddr, err := connectivitySTUNServerAddr(baseURL)
	if err != nil {
		return func(context.Context, *direct.UDPSocket) (*net.UDPAddr, error) {
			return nil, err
		}
	}
	client := direct.STUNClient{ServerAddr: serverAddr}
	return client.Discover
}

func connectivitySTUNServerAddr(baseURL string) (*net.UDPAddr, error) {
	if override := strings.TrimSpace(os.Getenv("TUNNEL_STUN_ADDR")); override != "" {
		switch strings.ToLower(override) {
		case "off", "disabled", "none", "false":
			return nil, fmt.Errorf("stun disabled")
		default:
			return net.ResolveUDPAddr("udp", override)
		}
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, fmt.Errorf("missing relay host for stun discovery")
	}
	return net.ResolveUDPAddr("udp", net.JoinHostPort(host, "3478"))
}

func (c *connectivityConnector) registerDirectAttempt(attemptID, androidFingerprint string, cancel context.CancelFunc) {
	attemptID = strings.TrimSpace(attemptID)
	androidFingerprint = strings.ToLower(strings.TrimSpace(androidFingerprint))
	if attemptID == "" || androidFingerprint == "" || cancel == nil {
		return
	}
	c.directMu.Lock()
	defer c.directMu.Unlock()
	if c.directAttempts == nil {
		c.directAttempts = make(map[string]directAttemptCancel)
	}
	key := directAttemptKey(attemptID, androidFingerprint)
	if existing := c.directAttempts[key]; existing.cancel != nil {
		existing.cancel()
	}
	c.directAttempts[key] = directAttemptCancel{androidFingerprint: androidFingerprint, cancel: cancel}
}

func (c *connectivityConnector) unregisterDirectAttempt(attemptID, androidFingerprint string) {
	attemptID = strings.TrimSpace(attemptID)
	androidFingerprint = strings.ToLower(strings.TrimSpace(androidFingerprint))
	c.directMu.Lock()
	delete(c.directAttempts, directAttemptKey(attemptID, androidFingerprint))
	c.directMu.Unlock()
}

func (c *connectivityConnector) cancelDirectAttempt(attemptID, androidFingerprint string) {
	attemptID = strings.TrimSpace(attemptID)
	androidFingerprint = strings.ToLower(strings.TrimSpace(androidFingerprint))
	if attemptID == "" {
		return
	}
	c.directMu.Lock()
	var attempts []directAttemptCancel
	if androidFingerprint != "" {
		key := directAttemptKey(attemptID, androidFingerprint)
		if attempt := c.directAttempts[key]; attempt.cancel != nil {
			attempts = append(attempts, attempt)
			delete(c.directAttempts, key)
		}
	} else {
		for key, attempt := range c.directAttempts {
			if strings.HasPrefix(key, attemptID+"\x00") {
				attempts = append(attempts, attempt)
				delete(c.directAttempts, key)
			}
		}
	}
	c.directMu.Unlock()
	for _, attempt := range attempts {
		attempt.cancel()
	}
}

func (c *connectivityConnector) cancelAllDirectAttempts() {
	c.directMu.Lock()
	attempts := make([]directAttemptCancel, 0, len(c.directAttempts))
	for key, attempt := range c.directAttempts {
		attempts = append(attempts, attempt)
		delete(c.directAttempts, key)
	}
	c.directMu.Unlock()
	for _, attempt := range attempts {
		if attempt.cancel != nil {
			attempt.cancel()
		}
	}
}

func directAttemptKey(attemptID, androidFingerprint string) string {
	return strings.TrimSpace(attemptID) + "\x00" + strings.ToLower(strings.TrimSpace(androidFingerprint))
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
	tunnelURL, err := connectivityTunnelWebSocketURL(c.baseURL)
	if err != nil {
		return err
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+frame.TunnelToken)
	conn, _, err := c.dialer.DialContext(ctx, tunnelURL, headers)
	if err != nil {
		return err
	}
	packetConn := carrier.NewWSPacketConn(conn, "daemon", "android", connframe.DefaultMaxPayload)
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
		AttemptID:          frame.AttemptID,
		FallbackReason:     frame.FallbackReason,
		DirectSetupLatency: time.Duration(frame.DirectSetupLatencyMS) * time.Millisecond,
		RelaySetupLatency:  time.Duration(frame.RelaySetupLatencyMS) * time.Millisecond,
	}
	c.state.setConnectivityPath("relay", frame.FallbackReason)
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
	frame := protocol.ConnectivityDaemonRegisterFrame(protocol.ConnectivityDaemonInfo{
		DeviceID:          status.DeviceID,
		DisplayName:       status.DisplayName,
		PlatformFamily:    status.PlatformFamily,
		PlatformID:        status.PlatformID,
		DaemonPublicKey:   fmt.Sprintf("%x", identity.PublicKey),
		DaemonFingerprint: identity.Fingerprint,
		TunnelVersion:     buildinfo.Version,
	}, trusted)
	frame.DirectSessions = c.state.activeDirectSessions()
	return frame, nil
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
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/connectivity/computer/ws"
	return parsed.String(), nil
}

func connectivityTunnelWebSocketURL(baseURL string) (string, error) {
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
	parsed.RawQuery = ""
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
