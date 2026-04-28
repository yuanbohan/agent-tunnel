package carrier

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"io"
	"net"
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

	daemonSPKI, err := identity.PublicKeySPKI(daemonKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatalf("daemon PublicKeySPKI returned error: %v", err)
	}
	androidSPKI, err := identity.PublicKeySPKI(androidKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatalf("android PublicKeySPKI returned error: %v", err)
	}

	return transport.DaemonTLSConfig(transport.EndpointConfig{
			Certificate:    daemonCert,
			PinnedPeerSPKI: androidSPKI,
		}),
		transport.AndroidTLSConfig(transport.EndpointConfig{
			Certificate:    androidCert,
			PinnedPeerSPKI: daemonSPKI,
			ServerName:     "daemon.carrier",
		})
}

var _ net.PacketConn = (*PacketConn)(nil)
