package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/carrier"
	"yuanbohan/tunnel/internal/connectivity/frame"
	"yuanbohan/tunnel/internal/connectivity/identity"
	"yuanbohan/tunnel/internal/connectivity/sessionproto"
	conntransport "yuanbohan/tunnel/internal/connectivity/transport"
)

func TestConnectivityTransportSendsSessionIndexAndPreviewSnapshots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	broker := NewBroker()
	owner := &brokerConnection{}
	broker.register(BrokerSession{
		SessionID:      "sess-1",
		Label:          "API fix",
		CommandPreview: "codex",
		CWD:            "/repo",
		GitBranch:      "main",
		StartedAt:      100,
		UpdatedAt:      101,
	}, owner)
	broker.updatePreview("sess-1", "cached preview", 102, owner)

	clientConn, serverErr := startConnectivityTransportForTest(t, ctx, broker, "android-fp")
	defer clientConn.CloseWithError(0, "done")
	control, err := clientConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(control, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorMobile,
		DeviceFingerprint: "android-fp",
		PathKind:          sessionproto.PathRelay,
	}); err != nil {
		t.Fatalf("write hello returned error: %v", err)
	}

	hello := readTestJSONFrame[sessionproto.Hello](t, control, frame.TypeHello)
	if hello.ActorType != sessionproto.ActorDaemon || hello.DeviceFingerprint != "daemon-fp" || hello.PathKind != sessionproto.PathRelay {
		t.Fatalf("hello = %#v, want daemon relay hello", hello)
	}
	index := readTestJSONFrame[sessionproto.SessionIndex](t, control, frame.TypeSessionIndex)
	if len(index.Sessions) != 1 || index.Sessions[0].SessionID != "sess-1" || index.Sessions[0].CommandPreview != "codex" {
		t.Fatalf("session index = %#v, want sess-1 metadata", index)
	}
	pathState := readTestJSONFrame[sessionproto.PathState](t, control, frame.TypePathState)
	if pathState.PathKind != sessionproto.PathRelay || pathState.AttemptID != "attempt-test" || pathState.FallbackReason != "direct_timeout" || pathState.DirectSetupLatencyMS != 3000 || pathState.RelaySetupLatencyMS != 120 {
		t.Fatalf("pathState = %#v, want relay attempt diagnostics", pathState)
	}

	if err := writeTestJSONFrame(control, frame.TypePreviewSubscribe, sessionproto.PreviewSubscribe{SessionID: "sess-1"}); err != nil {
		t.Fatalf("write preview_subscribe returned error: %v", err)
	}
	preview := readTestJSONFrame[sessionproto.PreviewSnapshot](t, control, frame.TypePreviewSnapshot)
	if preview.SessionID != "sess-1" || preview.Preview != "cached preview" || preview.UpdatedAt != 102 {
		t.Fatalf("preview = %#v, want cached preview", preview)
	}

	broker.updatePreview("sess-1", "new preview", 103, owner)
	preview = readTestJSONFrame[sessionproto.PreviewSnapshot](t, control, frame.TypePreviewSnapshot)
	if preview.Preview != "new preview" || preview.UpdatedAt != 103 {
		t.Fatalf("preview = %#v, want updated preview", preview)
	}

	broker.updateSession(BrokerSession{SessionID: "sess-1", Label: "Renamed", CWD: "/repo", CommandPreview: "claude", StartedAt: 100, UpdatedAt: 104}, owner)
	upsert := readTestJSONFrame[sessionproto.SessionUpsert](t, control, frame.TypeSessionUpsert)
	if upsert.Session.Label != "Renamed" || upsert.Session.CommandPreview != "claude" {
		t.Fatalf("upsert = %#v, want replacement metadata", upsert)
	}

	broker.remove("sess-1", owner)
	gone := readTestJSONFrame[sessionproto.SessionGone](t, control, frame.TypeSessionGone)
	if gone.SessionID != "sess-1" {
		t.Fatalf("gone = %#v, want sess-1", gone)
	}

	clientConn.CloseWithError(0, "done")
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("server returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for server")
	}
}

func TestConnectivityTransportRejectsUntrustedDeviceBeforeSessionIndex(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientConn, serverErr := startConnectivityTransportForTest(t, ctx, NewBroker(), "trusted-fp")
	defer clientConn.CloseWithError(0, "done")
	control, err := clientConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(control, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorMobile,
		DeviceFingerprint: "other-fp",
		PathKind:          sessionproto.PathRelay,
	}); err != nil {
		t.Fatalf("write hello returned error: %v", err)
	}
	got := readTestJSONFrame[sessionproto.Error](t, control, frame.TypeError)
	if got.Code != "device_not_trusted" {
		t.Fatalf("error = %#v, want device_not_trusted", got)
	}
	if err := <-serverErr; err != ErrConnectivityDeviceUntrusted {
		t.Fatalf("server err = %v, want ErrConnectivityDeviceUntrusted", err)
	}
}

func TestConnectivityTransportServeStopsWhenContextCancelsDuringHandshake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clientConn, serverErr := startConnectivityTransportForTest(t, ctx, NewBroker(), "android-fp")
	defer clientConn.CloseWithError(0, "done")
	if _, err := clientConn.OpenStreamSync(ctx); err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}

	cancel()
	select {
	case <-serverErr:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Serve to stop after context cancellation")
	}
}

func TestConnectivityTransportJSONHelpersSetDeadlines(t *testing.T) {
	var writer deadlineBuffer
	if err := writeConnectivityJSON(&writer, frame.TypeHello, sessionproto.Hello{ProtocolVersion: sessionproto.ProtocolVersion}); err != nil {
		t.Fatalf("writeConnectivityJSON returned error: %v", err)
	}
	if writer.writeDeadline.IsZero() {
		t.Fatal("write deadline was not set")
	}

	var reader deadlineBuffer
	if err := writeTestJSONFrame(&reader, frame.TypeHello, sessionproto.Hello{ProtocolVersion: sessionproto.ProtocolVersion}); err != nil {
		t.Fatalf("write test frame returned error: %v", err)
	}
	if _, err := readConnectivityJSON[sessionproto.Hello](&reader, frame.TypeHello, frame.DefaultMaxPayload); err != nil {
		t.Fatalf("readConnectivityJSON returned error: %v", err)
	}
	if reader.readDeadlineSet.IsZero() {
		t.Fatal("read deadline was not set")
	}
	if !reader.readDeadlineCleared {
		t.Fatal("read deadline was not cleared after the frame read")
	}
}

func TestConnectivityFallbackSimulatorRoutesInputAndReconnectsFreshState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	broker := NewBroker()
	brokerClientConn, brokerServerConn := net.Pipe()
	defer brokerClientConn.Close()
	defer brokerServerConn.Close()
	owner := &brokerConnection{conn: brokerServerConn}
	broker.register(BrokerSession{
		SessionID:      "sess-1",
		Label:          "API fix",
		CommandPreview: "codex",
		CWD:            "/repo",
		GitBranch:      "main",
		StartedAt:      100,
		UpdatedAt:      101,
	}, owner)
	broker.updatePreview("sess-1", "cached preview", 102, owner)

	firstConn, firstServerErr := startConnectivityTransportForTest(t, ctx, broker, "android-fp")
	firstControl, err := firstConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("first OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(firstControl, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorMobile,
		DeviceFingerprint: "android-fp",
		PathKind:          sessionproto.PathRelay,
	}); err != nil {
		t.Fatalf("write first hello returned error: %v", err)
	}
	_ = readTestJSONFrame[sessionproto.Hello](t, firstControl, frame.TypeHello)
	_ = readTestJSONFrame[sessionproto.SessionIndex](t, firstControl, frame.TypeSessionIndex)
	_ = readTestJSONFrame[sessionproto.PathState](t, firstControl, frame.TypePathState)
	if err := writeTestJSONFrame(firstControl, frame.TypeInteractiveRequest, sessionproto.InteractiveRequest{
		SessionID: "sess-1",
		Cols:      100,
		Rows:      30,
	}); err != nil {
		t.Fatalf("write interactive_request returned error: %v", err)
	}
	granted := readTestJSONFrame[sessionproto.InteractiveGranted](t, firstControl, frame.TypeInteractiveGranted)
	if granted.SessionID != "sess-1" || granted.InteractiveStreamID == 0 {
		t.Fatalf("interactive granted = %#v, want sess-1 with interactive stream", granted)
	}
	interactiveStream, err := firstConn.AcceptUniStream(ctx)
	if err != nil {
		t.Fatalf("AcceptUniStream returned error: %v", err)
	}
	if int64(interactiveStream.StreamID()) != granted.InteractiveStreamID {
		t.Fatalf("interactive stream id = %d, want %d", interactiveStream.StreamID(), granted.InteractiveStreamID)
	}
	snapshotBegin := readTestJSONFrame[sessionproto.SnapshotBegin](t, interactiveStream, frame.TypeSnapshotBegin)
	if snapshotBegin.SessionID != "sess-1" || snapshotBegin.Cols != 100 || snapshotBegin.Rows != 30 {
		t.Fatalf("snapshot_begin = %#v, want sess-1 100x30", snapshotBegin)
	}
	snapshotEnd := readTestJSONFrame[sessionproto.SnapshotEnd](t, interactiveStream, frame.TypeSnapshotEnd)
	if snapshotEnd.SessionID != "sess-1" {
		t.Fatalf("snapshot_end = %#v, want sess-1", snapshotEnd)
	}

	if err := writeTestJSONFrame(firstControl, frame.TypeInputText, sessionproto.InputText{
		SessionID: "sess-1",
		Text:      "echo fallback",
		Submit:    true,
	}); err != nil {
		t.Fatalf("write input_text returned error: %v", err)
	}
	var routed BrokerFrame
	if err := json.NewDecoder(brokerClientConn).Decode(&routed); err != nil {
		t.Fatalf("Decode broker frame returned error: %v", err)
	}
	if routed.Type != brokerFrameInputText || routed.SessionID != "sess-1" || routed.Text != "echo fallback" || !routed.Submit {
		t.Fatalf("broker frame = %#v, want submitted fallback input", routed)
	}
	firstConn.CloseWithError(0, "done")
	if err := <-firstServerErr; err != nil {
		t.Fatalf("first server returned error: %v", err)
	}

	broker.updatePreview("sess-1", "after reconnect", 103, owner)
	secondConn, secondServerErr := startConnectivityTransportForTest(t, ctx, broker, "android-fp")
	secondControl, err := secondConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("second OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(secondControl, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorMobile,
		DeviceFingerprint: "android-fp",
		PathKind:          sessionproto.PathRelay,
	}); err != nil {
		t.Fatalf("write second hello returned error: %v", err)
	}
	_ = readTestJSONFrame[sessionproto.Hello](t, secondControl, frame.TypeHello)
	index := readTestJSONFrame[sessionproto.SessionIndex](t, secondControl, frame.TypeSessionIndex)
	if len(index.Sessions) != 1 || index.Sessions[0].SessionID != "sess-1" {
		t.Fatalf("reconnect index = %#v, want fresh sess-1 state", index)
	}
	_ = readTestJSONFrame[sessionproto.PathState](t, secondControl, frame.TypePathState)
	if err := writeTestJSONFrame(secondControl, frame.TypePreviewSubscribe, sessionproto.PreviewSubscribe{SessionID: "sess-1"}); err != nil {
		t.Fatalf("write reconnect preview_subscribe returned error: %v", err)
	}
	preview := readTestJSONFrame[sessionproto.PreviewSnapshot](t, secondControl, frame.TypePreviewSnapshot)
	if preview.Preview != "after reconnect" || preview.UpdatedAt != 103 {
		t.Fatalf("reconnect preview = %#v, want fresh preview", preview)
	}
	secondConn.CloseWithError(0, "done")
	if err := <-secondServerErr; err != nil {
		t.Fatalf("second server returned error: %v", err)
	}
}

func startConnectivityTransportForTest(t *testing.T, ctx context.Context, broker *Broker, androidFingerprint string) (*quic.Conn, <-chan error) {
	t.Helper()
	relay := carrier.NewRelay()
	clientPacketConn := relay.NewPacketConn("android")
	serverPacketConn := relay.NewPacketConn("daemon")
	t.Cleanup(func() {
		_ = clientPacketConn.Close()
		_ = serverPacketConn.Close()
	})
	serverTLS, clientTLS := connectivityTransportTLSConfigs(t)
	listener, err := quic.Listen(serverPacketConn, serverTLS, conntransport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Listen returned error: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			serverErr <- err
			return
		}
		serverErr <- (&ConnectivityTransport{
			Broker:             broker,
			DaemonFingerprint:  "daemon-fp",
			AndroidFingerprint: androidFingerprint,
			PathKind:           sessionproto.PathRelay,
			AttemptID:          "attempt-test",
			FallbackReason:     "direct_timeout",
			DirectSetupLatency: 3 * time.Second,
			RelaySetupLatency:  120 * time.Millisecond,
		}).Serve(ctx, conn)
	}()
	clientConn, err := quic.Dial(ctx, clientPacketConn, serverPacketConn.LocalAddr(), clientTLS, conntransport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Dial returned error: %v", err)
	}
	return clientConn, serverErr
}

func writeTestJSONFrame(w io.Writer, typ byte, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return frame.Write(w, frame.Frame{Type: typ, Payload: raw})
}

func readTestJSONFrame[T any](t *testing.T, r io.Reader, typ byte) T {
	t.Helper()
	got, err := frame.Read(r, frame.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("frame.Read returned error: %v", err)
	}
	if got.Type != typ {
		t.Fatalf("frame type = 0x%02x, want 0x%02x", got.Type, typ)
	}
	var payload T
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	return payload
}

type deadlineBuffer struct {
	bytes.Buffer
	readDeadlineSet     time.Time
	readDeadlineCleared bool
	writeDeadline       time.Time
}

func (b *deadlineBuffer) SetReadDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		b.readDeadlineCleared = true
		return nil
	}
	b.readDeadlineSet = deadline
	return nil
}

func (b *deadlineBuffer) SetWriteDeadline(deadline time.Time) error {
	b.writeDeadline = deadline
	return nil
}

func connectivityTransportTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
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

	return conntransport.DaemonTLSConfig(conntransport.EndpointConfig{
			Certificate:         daemonCert,
			PinnedPeerPublicKey: androidKey.Public().(ed25519.PublicKey),
		}),
		conntransport.AndroidTLSConfig(conntransport.EndpointConfig{
			Certificate:         androidCert,
			PinnedPeerPublicKey: daemonKey.Public().(ed25519.PublicKey),
			ServerName:          "connectivity.daemon",
		})
}
