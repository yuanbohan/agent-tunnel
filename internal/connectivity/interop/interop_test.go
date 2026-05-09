package interop_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/carrier"
	"yuanbohan/tunnel/internal/connectivity/frame"
	"yuanbohan/tunnel/internal/connectivity/identity"
	"yuanbohan/tunnel/internal/connectivity/interop"
	"yuanbohan/tunnel/internal/connectivity/sessionproto"
	"yuanbohan/tunnel/internal/connectivity/transport"
)

func TestGoMobileSimulatorValidatesProtocolDataOverUDP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script := probeScript(interop.PathDirect)

	serverTLS, clientTLS := interopTLSConfigs(t)
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP returned error: %v", err)
	}

	listener, err := quic.Listen(packetConn, serverTLS, transport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Listen returned error: %v", err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	clientDone := make(chan struct{})
	go func() {
		serverErr <- runDaemonProbe(ctx, listener, script, clientDone)
	}()

	client := interop.MobileClient{TLSConfig: clientTLS}
	clientErr := client.DialAddr(ctx, packetConn.LocalAddr().String(), script)
	close(clientDone)
	if clientErr != nil {
		t.Fatalf("mobile DialAddr returned error: %v", clientErr)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("daemon probe returned error: %v", err)
	}
}

func TestGoMobileSimulatorValidatesProtocolDataOverRelayCarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script := probeScript(interop.PathRelay)

	relay := carrier.NewRelay()
	clientPacketConn := relay.NewPacketConn("android")
	serverPacketConn := relay.NewPacketConn("daemon")
	defer clientPacketConn.Close()
	defer serverPacketConn.Close()

	serverTLS, clientTLS := interopTLSConfigs(t)
	listener, err := quic.Listen(serverPacketConn, serverTLS, transport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Listen returned error: %v", err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	clientDone := make(chan struct{})
	go func() {
		serverErr <- runDaemonProbe(ctx, listener, script, clientDone)
	}()

	client := interop.MobileClient{TLSConfig: clientTLS}
	clientErr := client.DialPacketConn(ctx, clientPacketConn, serverPacketConn.LocalAddr(), script)
	close(clientDone)
	if clientErr != nil {
		t.Fatalf("mobile DialPacketConn returned error: %v", clientErr)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("daemon probe returned error: %v", err)
	}

	needles := [][]byte{
		[]byte("android-fingerprint-go-simulator"),
		[]byte("daemon-fingerprint-go-simulator"),
		[]byte("session-interop-1"),
		[]byte("SNAPSHOT_SECRET_"),
		[]byte("LIVE_SECRET_"),
	}
	for _, packet := range relay.ObservedPackets() {
		for _, needle := range needles {
			if bytes.Contains(packet, needle) {
				t.Fatalf("relay observed application plaintext %q in QUIC packet: %x", needle, packet)
			}
		}
	}
}

func TestGoMobileSimulatorDirectFirstUsesDirectWhenHandshakeSucceeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script := probeScript(interop.PathDirect)

	serverTLS, clientTLS := interopTLSConfigs(t)
	serverPacketConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP server returned error: %v", err)
	}
	listener, err := quic.Listen(serverPacketConn, serverTLS, transport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Listen returned error: %v", err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	clientDone := make(chan struct{})
	go func() {
		serverErr <- runDaemonProbe(ctx, listener, script, clientDone)
	}()

	clientPacketConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP client returned error: %v", err)
	}
	defer clientPacketConn.Close()
	client := interop.MobileClient{TLSConfig: clientTLS}
	result, err := client.DialDirectFirst(ctx, interop.DirectFirstDial{
		DirectPacketConn: clientPacketConn,
		DirectAddr:       serverPacketConn.LocalAddr(),
		DirectScript:     script,
		Deadline:         time.Second,
	})
	close(clientDone)
	if err != nil {
		t.Fatalf("DialDirectFirst returned error: %v", err)
	}
	if result.PathKind != interop.PathDirect {
		t.Fatalf("result = %#v, want direct path", result)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("daemon probe returned error: %v", err)
	}
}

func TestGoMobileSimulatorDirectFirstFallsBackToRelayAfterDirectTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fallbackScript := probeScript(interop.PathRelay)

	relay := carrier.NewRelay()
	fallbackClientPacketConn := relay.NewPacketConn("android")
	fallbackServerPacketConn := relay.NewPacketConn("daemon")
	defer fallbackClientPacketConn.Close()
	defer fallbackServerPacketConn.Close()

	serverTLS, clientTLS := interopTLSConfigs(t)
	listener, err := quic.Listen(fallbackServerPacketConn, serverTLS, transport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Listen returned error: %v", err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	clientDone := make(chan struct{})
	go func() {
		serverErr <- runDaemonProbe(ctx, listener, fallbackScript, clientDone)
	}()

	directPacketConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP direct returned error: %v", err)
	}
	defer directPacketConn.Close()
	unreachable := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}

	client := interop.MobileClient{TLSConfig: clientTLS}
	result, err := client.DialDirectFirst(ctx, interop.DirectFirstDial{
		DirectPacketConn:   directPacketConn,
		DirectAddr:         unreachable,
		DirectScript:       probeScript(interop.PathDirect),
		FallbackPacketConn: fallbackClientPacketConn,
		FallbackAddr:       fallbackServerPacketConn.LocalAddr(),
		FallbackScript:     fallbackScript,
		Deadline:           50 * time.Millisecond,
	})
	close(clientDone)
	if err != nil {
		t.Fatalf("DialDirectFirst returned error: %v", err)
	}
	if result.PathKind != interop.PathRelay || result.FallbackReason != "direct_timeout" {
		t.Fatalf("result = %#v, want relay direct_timeout", result)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("daemon probe returned error: %v", err)
	}
}

func TestMobileClientRequiresTLSConfig(t *testing.T) {
	err := interop.MobileClient{}.DialAddr(context.Background(), "127.0.0.1:1", interop.ProbeScript{})
	if err != interop.ErrMissingTLSConfig {
		t.Fatalf("err = %v, want ErrMissingTLSConfig", err)
	}
}

func runDaemonProbe(ctx context.Context, listener *quic.Listener, script interop.ProbeScript, clientDone <-chan struct{}) error {
	conn, err := listener.Accept(ctx)
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "done")
	if err := transport.ValidateConnectionState(conn.ConnectionState()); err != nil {
		return err
	}

	control, err := conn.AcceptStream(ctx)
	if err != nil {
		return err
	}
	mobileHello, err := readJSONFrame[interop.Hello](control, frame.TypeHello)
	if err != nil {
		return err
	}
	if mobileHello != script.MobileHello {
		return fmt.Errorf("mobile hello=%#v want %#v", mobileHello, script.MobileHello)
	}
	if err := writeJSONFrameWithUnknown(control, frame.TypeHello, script.DaemonHello); err != nil {
		return err
	}
	if err := writeJSONFrameWithUnknown(control, frame.TypeSessionIndex, script.SessionIndex); err != nil {
		return err
	}

	request, err := readJSONFrame[interop.InteractiveRequest](control, frame.TypeInteractiveRequest)
	if err != nil {
		return err
	}
	if request != script.InteractiveRequest {
		return fmt.Errorf("interactive request=%#v want %#v", request, script.InteractiveRequest)
	}

	interactive, err := conn.OpenUniStreamSync(ctx)
	if err != nil {
		return err
	}
	defer interactive.Close()

	granted := script.InteractiveGranted
	granted.InteractiveStreamID = int64(interactive.StreamID())
	if err := writeJSONFrameWithUnknown(control, frame.TypeInteractiveGranted, granted); err != nil {
		return err
	}
	if err := writeJSONFrameWithUnknown(interactive, frame.TypeSnapshotBegin, script.SnapshotBegin); err != nil {
		return err
	}
	if err := frame.Write(interactive, frame.Frame{Type: frame.TypeSnapshotChunk, Payload: script.SnapshotChunk}); err != nil {
		return err
	}
	if err := writeJSONFrameWithUnknown(interactive, frame.TypeSnapshotEnd, script.SnapshotEnd); err != nil {
		return err
	}
	if err := frame.Write(interactive, frame.Frame{Type: frame.TypeLiveBytes, Payload: script.LiveBytes}); err != nil {
		return err
	}
	select {
	case <-clientDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func readJSONFrame[T any](r io.Reader, typ byte) (T, error) {
	var payload T
	got, err := frame.Read(r, frame.DefaultMaxPayload)
	if err != nil {
		return payload, err
	}
	if got.Type != typ {
		return payload, fmt.Errorf("frame type=0x%02x want 0x%02x", got.Type, typ)
	}
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func writeJSONFrameWithUnknown(w io.Writer, typ byte, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	var withUnknown map[string]any
	if err := json.Unmarshal(raw, &withUnknown); err != nil {
		return err
	}
	withUnknown["future_field"] = "ignored"

	raw, err = json.Marshal(withUnknown)
	if err != nil {
		return err
	}
	return frame.Write(w, frame.Frame{Type: typ, Payload: raw})
}

func probeScript(pathKind string) interop.ProbeScript {
	return interop.ProbeScript{
		MobileHello: interop.Hello{
			ProtocolVersion:   sessionproto.ProtocolVersion,
			ActorType:         interop.ActorMobile,
			ClientFingerprint: "android-fingerprint-go-simulator",
			PathKind:          pathKind,
		},
		DaemonHello: interop.Hello{
			ProtocolVersion:   sessionproto.ProtocolVersion,
			ActorType:         interop.ActorDaemon,
			ClientFingerprint: "daemon-fingerprint-go-simulator",
			PathKind:          pathKind,
		},
		SessionIndex: interop.SessionIndex{Sessions: []interop.SessionMetadata{
			{
				SessionID:      "session-interop-1",
				Label:          "Interop shell",
				CommandPreview: "codex",
				CWD:            "/Users/example/project",
				GitBranch:      "feat/direct-p2p",
				StartedAt:      1_700_000_000,
				UpdatedAt:      1_700_000_010,
				Online:         true,
			},
		}},
		InteractiveRequest: interop.InteractiveRequest{
			SessionID: "session-interop-1",
			Cols:      120,
			Rows:      40,
		},
		InteractiveGranted: interop.InteractiveGranted{
			SessionID: "session-interop-1",
			Cols:      120,
			Rows:      40,
		},
		SnapshotBegin: interop.SnapshotBegin{
			SessionID: "session-interop-1",
			Cols:      120,
			Rows:      40,
		},
		SnapshotChunk: bytes.Repeat([]byte("SNAPSHOT_SECRET_"), 80),
		SnapshotEnd: interop.SnapshotEnd{
			SessionID:  "session-interop-1",
			ChunkCount: 1,
		},
		LiveBytes: bytes.Repeat([]byte("LIVE_SECRET_"), 100),
	}
}

func interopTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()

	daemonKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x41}, ed25519.SeedSize))
	androidKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))

	daemonCert, err := identity.SelfSignedCertificate(daemonKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("daemon SelfSignedCertificate returned error: %v", err)
	}
	androidCert, err := identity.SelfSignedCertificate(androidKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("android SelfSignedCertificate returned error: %v", err)
	}

	return transport.DaemonTLSConfig(transport.EndpointConfig{
			Certificate:         daemonCert,
			PinnedPeerPublicKey: androidKey.Public().(ed25519.PublicKey),
		}),
		transport.AndroidTLSConfig(transport.EndpointConfig{
			Certificate:         androidCert,
			PinnedPeerPublicKey: daemonKey.Public().(ed25519.PublicKey),
			ServerName:          "interop.daemon",
		})
}
