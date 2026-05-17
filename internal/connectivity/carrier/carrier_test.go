package carrier

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/frame"
	"yuanbohan/tunnel/internal/connectivity/identity"
	"yuanbohan/tunnel/internal/connectivity/transport"
)

func TestRelayPacketConnForwardsDatagrams(t *testing.T) {
	relay := NewRelay()
	client := relay.NewPacketConn("client")
	server := relay.NewPacketConn("server")
	defer client.Close()
	defer server.Close()

	if _, err := client.WriteTo([]byte("hello"), server.LocalAddr()); err != nil {
		t.Fatalf("client WriteTo returned error: %v", err)
	}

	buf := make([]byte, 32)
	n, from, err := server.ReadFrom(buf)
	if err != nil {
		t.Fatalf("server ReadFrom returned error: %v", err)
	}
	if from.String() != "client" || string(buf[:n]) != "hello" {
		t.Fatalf("server got %q from %q", buf[:n], from)
	}

	if _, err := server.WriteTo([]byte("world"), from); err != nil {
		t.Fatalf("server WriteTo returned error: %v", err)
	}
	n, from, err = client.ReadFrom(buf)
	if err != nil {
		t.Fatalf("client ReadFrom returned error: %v", err)
	}
	if from.String() != "server" || string(buf[:n]) != "world" {
		t.Fatalf("client got %q from %q", buf[:n], from)
	}

	if got := len(relay.ObservedPackets()); got != 2 {
		t.Fatalf("relay observed %d packets, want 2", got)
	}
}

func TestReadDeadlineWakesPendingRead(t *testing.T) {
	relay := NewRelay()
	client := relay.NewPacketConn("client")
	defer client.Close()

	errc := make(chan error, 1)
	go func() {
		_, _, err := client.ReadFrom(make([]byte, 8))
		errc <- err
	}()

	time.Sleep(10 * time.Millisecond)
	if err := client.SetReadDeadline(time.Now().Add(-time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}

	select {
	case err := <-errc:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("ReadFrom err = %v, want os.ErrDeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadFrom did not wake after read deadline changed")
	}
}

func TestPastReadDeadlinePrecedesQueuedPacket(t *testing.T) {
	relay := NewRelay()
	client := relay.NewPacketConn("client")
	server := relay.NewPacketConn("server")
	defer client.Close()
	defer server.Close()

	if _, err := server.WriteTo([]byte("queued"), client.LocalAddr()); err != nil {
		t.Fatalf("server WriteTo returned error: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(-time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}

	_, _, err := client.ReadFrom(make([]byte, 32))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("ReadFrom err = %v, want os.ErrDeadlineExceeded", err)
	}
}

func TestWriteDeadlineWakesPendingWrite(t *testing.T) {
	relay := NewRelay()
	client := relay.NewPacketConn("client")
	server := relay.NewPacketConn("server")
	defer client.Close()
	defer server.Close()

	for i := 0; i < cap(server.inbound); i++ {
		if _, err := client.WriteTo([]byte("x"), server.LocalAddr()); err != nil {
			t.Fatalf("fill WriteTo %d returned error: %v", i, err)
		}
	}

	errc := make(chan error, 1)
	go func() {
		_, err := client.WriteTo([]byte("blocked"), server.LocalAddr())
		errc <- err
	}()

	time.Sleep(10 * time.Millisecond)
	if err := client.SetWriteDeadline(time.Now().Add(-time.Millisecond)); err != nil {
		t.Fatalf("SetWriteDeadline returned error: %v", err)
	}

	select {
	case err := <-errc:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("WriteTo err = %v, want os.ErrDeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WriteTo did not wake after write deadline changed")
	}
}

func TestPastWriteDeadlinePrecedesAvailablePeer(t *testing.T) {
	relay := NewRelay()
	client := relay.NewPacketConn("client")
	server := relay.NewPacketConn("server")
	defer client.Close()
	defer server.Close()

	if err := client.SetWriteDeadline(time.Now().Add(-time.Millisecond)); err != nil {
		t.Fatalf("SetWriteDeadline returned error: %v", err)
	}
	_, err := client.WriteTo([]byte("late"), server.LocalAddr())
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("WriteTo err = %v, want os.ErrDeadlineExceeded", err)
	}
}

func TestCloseReleasesEndpointAndRejectsQueuedReads(t *testing.T) {
	relay := NewRelay()
	client := relay.NewPacketConn("client")
	server := relay.NewPacketConn("server")
	defer client.Close()

	if _, err := client.WriteTo([]byte("queued"), server.LocalAddr()); err != nil {
		t.Fatalf("WriteTo returned error: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("server Close returned error: %v", err)
	}
	if relay.endpoints["server"] != nil {
		t.Fatal("closed server endpoint remained registered")
	}

	_, _, err := server.ReadFrom(make([]byte, 32))
	if !errors.Is(err, ErrClosedEndpoint) {
		t.Fatalf("ReadFrom after Close err = %v, want ErrClosedEndpoint", err)
	}
	_, err = client.WriteTo([]byte("after-close"), server.LocalAddr())
	if !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("WriteTo closed endpoint err = %v, want ErrUnknownEndpoint", err)
	}
}

func TestPacketConnRejectsClosedAndUnknownWrites(t *testing.T) {
	relay := NewRelay()
	client := relay.NewPacketConn("client")
	if err := client.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	_, err := client.WriteTo([]byte("x"), Addr("server"))
	if !errors.Is(err, ErrClosedEndpoint) {
		t.Fatalf("WriteTo after Close err = %v, want ErrClosedEndpoint", err)
	}

	live := relay.NewPacketConn("live")
	defer live.Close()
	_, err = live.WriteTo([]byte("x"), nil)
	if !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("WriteTo nil addr err = %v, want ErrUnknownEndpoint", err)
	}
	_, err = live.WriteTo([]byte("x"), Addr("missing"))
	if !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("WriteTo unknown addr err = %v, want ErrUnknownEndpoint", err)
	}
}

func TestQUICOverRelayCarrierKeepsApplicationBytesOpaque(t *testing.T) {
	secretControl := []byte(`{"protocol_version":1,"terminal":"SECRET_TERMINAL_JSON"}`)
	secretLive := []byte("SECRET_LIVE_BYTES")

	relay := NewRelay()
	clientPacketConn := relay.NewPacketConn("android")
	serverPacketConn := relay.NewPacketConn("daemon")
	defer clientPacketConn.Close()
	defer serverPacketConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverTLS, clientTLS := carrierTLSConfigs(t)
	listener, err := quic.Listen(serverPacketConn, serverTLS, transport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Listen returned error: %v", err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	clientDone := make(chan struct{})
	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.CloseWithError(0, "done")

		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			serverErr <- err
			return
		}

		got, err := frame.Read(stream, frame.DefaultMaxPayload)
		if err != nil {
			serverErr <- err
			return
		}
		if !bytes.Equal(got.Payload, secretControl) {
			serverErr <- io.ErrUnexpectedEOF
			return
		}

		if err := frame.Write(stream, frame.Frame{Type: frame.TypeLiveBytes, Payload: secretLive}); err != nil {
			serverErr <- err
			return
		}
		if err := stream.Close(); err != nil {
			serverErr <- err
			return
		}
		<-clientDone
		serverErr <- nil
	}()

	conn, err := quic.Dial(ctx, clientPacketConn, serverPacketConn.LocalAddr(), clientTLS, transport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Dial returned error: %v", err)
	}
	defer conn.CloseWithError(0, "done")

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}
	if err := frame.Write(stream, frame.Frame{Type: frame.TypeHello, Payload: secretControl}); err != nil {
		t.Fatalf("frame.Write returned error: %v", err)
	}

	reply, err := frame.Read(stream, frame.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("frame.Read reply returned error: %v", err)
	}
	if !bytes.Equal(reply.Payload, secretLive) {
		t.Fatalf("reply payload = %q, want %q", reply.Payload, secretLive)
	}

	close(clientDone)
	if err := <-serverErr; err != nil {
		t.Fatalf("server returned error: %v", err)
	}

	for _, packet := range relay.ObservedPackets() {
		if bytes.Contains(packet, secretControl) || bytes.Contains(packet, secretLive) {
			t.Fatalf("relay observed application plaintext in QUIC packet: %x", packet)
		}
	}
}

func carrierTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()

	daemonKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x31}, ed25519.SeedSize))
	androidKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x32}, ed25519.SeedSize))

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
			ServerName:          "daemon.carrier",
		})
}

var _ net.PacketConn = (*PacketConn)(nil)
