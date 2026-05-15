package daemon

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/direct"
	connidentity "yuanbohan/tunnel/internal/connectivity/identity"
	conntransport "yuanbohan/tunnel/internal/connectivity/transport"
	"yuanbohan/tunnel/internal/protocol"
)

const connectivityDirectAcceptTimeout = direct.DefaultAttemptDeadline

type connectivityFrameWriter func(any) error

func (c *connectivityConnector) handleRendezvousHint(ctx context.Context, hint protocol.ConnectivityFrame, write connectivityFrameWriter) error {
	if hint.Actor != "client" || strings.TrimSpace(hint.AttemptID) == "" {
		return nil
	}
	android, err := c.trustedAndroidDevice(hint.AndroidFingerprint)
	if err != nil {
		return err
	}
	androidPublicKey, err := decodeTrustedAndroidPublicKey(android.PublicKey)
	if err != nil {
		return err
	}
	attemptCtx, cancelAttempt := context.WithCancel(ctx)
	c.registerDirectAttempt(hint.AttemptID, android.Fingerprint, cancelAttempt)
	unregisterPending := c.state.registerPendingDirectAttempt(hint.AttemptID, android.Fingerprint, cancelAttempt)
	defer cancelAttempt()
	defer c.unregisterDirectAttempt(hint.AttemptID, android.Fingerprint)
	defer unregisterPending()
	daemonIdentity, err := ReadConnectivityIdentity(c.paths)
	if err != nil {
		return err
	}
	daemonCert, err := connidentity.SelfSignedCertificate(daemonIdentity.PrivateKey, connidentity.CertificateOptions{})
	if err != nil {
		return err
	}

	socket, err := direct.ListenUDPSocket(&net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return err
	}
	defer socket.Close()

	local := socket.LocalUDPAddr()
	private, _ := direct.CollectPrivateUDPAddrs(local.Port, direct.PrivateAddressOptions{AllowLoopback: true})
	if c.stunDiscover == nil {
		return direct.ErrSTUNUnexpectedResponse
	}
	publicAddr, err := c.stunDiscover(attemptCtx, socket)
	if err != nil {
		return err
	}
	public := direct.NormalizeUDPAddr(publicAddr)
	if _, err := c.trustedAndroidDevice(android.Fingerprint); err != nil {
		return err
	}
	if write != nil {
		if err := write(protocol.ConnectivityRendezvousHintFrame(
			hint.RequestID,
			hint.AttemptID,
			"daemon",
			hint.DaemonID,
			android.Fingerprint,
			public,
			private,
			time.Now().Add(connectivityDirectAcceptTimeout).Unix(),
		)); err != nil {
			return err
		}
	}

	candidates := append([]string{hint.PublicUDPAddr}, hint.PrivateUDPAddrs...)
	direct.ProbeBurst(socket, candidates, 3, 20*time.Millisecond)

	listener, err := quic.Listen(socket.PacketConn(), conntransport.DaemonTLSConfig(conntransport.EndpointConfig{
		Certificate:         daemonCert,
		PinnedPeerPublicKey: androidPublicKey,
	}), conntransport.QUICConfig())
	if err != nil {
		return err
	}
	defer listener.Close()

	acceptCtx, cancel := context.WithTimeout(attemptCtx, connectivityDirectAcceptTimeout)
	defer cancel()
	stopCleanup := context.AfterFunc(acceptCtx, func() {
		_ = listener.Close()
		_ = socket.Close()
	})
	defer stopCleanup()

	quicConn, err := listener.Accept(acceptCtx)
	if err != nil {
		return err
	}
	_ = stopCleanup()
	cancel()
	c.unregisterDirectAttempt(hint.AttemptID, android.Fingerprint)
	unregisterPending()
	defer quicConn.CloseWithError(0, "done")
	if write != nil {
		if err := write(protocol.ConnectivityDirectSessionOpenFrame(hint.RequestID, hint.AttemptID, hint.DaemonID, android.Fingerprint)); err != nil {
			return err
		}
	}
	if _, err := c.trustedAndroidDevice(android.Fingerprint); err != nil {
		return err
	}
	transport := &ConnectivityTransport{
		Broker:             c.state.broker,
		DaemonFingerprint:  daemonIdentity.Fingerprint,
		AndroidFingerprint: android.Fingerprint,
		PathKind:           "direct",
		AttemptID:          hint.AttemptID,
	}
	c.state.setConnectivityPath("direct", "")
	serveCtx, cancelServe := context.WithCancel(ctx)
	unregisterTransport := c.state.registerActiveDirectTransport(hint.AttemptID, android.Fingerprint, cancelServe)
	defer unregisterTransport()
	defer cancelServe()
	err = transport.Serve(serveCtx, quicConn)
	if write != nil {
		_ = write(protocol.ConnectivityDirectSessionCloseFrame("", hint.AttemptID, hint.DaemonID, android.Fingerprint))
	}
	return err
}
