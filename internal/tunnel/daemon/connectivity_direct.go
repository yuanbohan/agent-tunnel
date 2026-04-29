package daemon

import (
	"context"
	"net"
	"strconv"
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
	if hint.Actor != "android" || strings.TrimSpace(hint.AttemptID) == "" {
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
	public := direct.NormalizeUDPAddr(local)
	if local.IP.IsUnspecified() {
		public = net.JoinHostPort("127.0.0.1", strconv.Itoa(local.Port))
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

	acceptCtx, cancel := context.WithTimeout(ctx, connectivityDirectAcceptTimeout)
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
	defer quicConn.CloseWithError(0, "done")
	transport := &ConnectivityTransport{
		Broker:             c.state.broker,
		DaemonFingerprint:  daemonIdentity.Fingerprint,
		AndroidFingerprint: android.Fingerprint,
		PathKind:           "direct",
	}
	return transport.Serve(ctx, quicConn)
}
