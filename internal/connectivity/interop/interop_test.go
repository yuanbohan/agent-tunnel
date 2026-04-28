package interop_test

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

func TestGoPinnedQUICInteropHarness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientHello := bytes.Repeat([]byte("a"), 1024)
	daemonHello := bytes.Repeat([]byte("d"), 1024)
	daemonLive := bytes.Repeat([]byte("u"), 1024)

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
		conn, err := listener.Accept(ctx)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.CloseWithError(0, "done")
		if err := transport.ValidateConnectionState(conn.ConnectionState()); err != nil {
			serverErr <- err
			return
		}

		control, err := conn.AcceptStream(ctx)
		if err != nil {
			serverErr <- err
			return
		}
		hello, err := frame.Read(control, frame.DefaultMaxPayload)
		if err != nil {
			serverErr <- err
			return
		}
		if hello.Type != frame.TypeHello || !bytes.Equal(hello.Payload, clientHello) {
			serverErr <- io.ErrUnexpectedEOF
			return
		}
		if err := frame.Write(control, frame.Frame{Type: frame.TypeHello, Payload: daemonHello}); err != nil {
			serverErr <- err
			return
		}
		if err := control.Close(); err != nil {
			serverErr <- err
			return
		}

		interactive, err := conn.OpenUniStreamSync(ctx)
		if err != nil {
			serverErr <- err
			return
		}
		if err := frame.Write(interactive, frame.Frame{Type: frame.TypeLiveBytes, Payload: daemonLive}); err != nil {
			serverErr <- err
			return
		}
		if err := interactive.Close(); err != nil {
			serverErr <- err
			return
		}
		<-clientDone
		serverErr <- nil
	}()

	conn, err := quic.DialAddr(ctx, packetConn.LocalAddr().String(), clientTLS, transport.QUICConfig())
	if err != nil {
		t.Fatalf("DialAddr returned error: %v", err)
	}
	defer conn.CloseWithError(0, "done")
	if err := transport.ValidateConnectionState(conn.ConnectionState()); err != nil {
		t.Fatalf("client connection state: %v", err)
	}

	control, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}
	if err := frame.Write(control, frame.Frame{Type: frame.TypeHello, Payload: clientHello}); err != nil {
		t.Fatalf("client frame.Write returned error: %v", err)
	}
	reply, err := frame.Read(control, frame.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("client frame.Read returned error: %v", err)
	}
	if reply.Type != frame.TypeHello || !bytes.Equal(reply.Payload, daemonHello) {
		t.Fatalf("reply frame = %#v, want 1KB hello", reply)
	}

	interactive, err := conn.AcceptUniStream(ctx)
	if err != nil {
		t.Fatalf("AcceptUniStream returned error: %v", err)
	}
	live, err := frame.Read(interactive, frame.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("interactive frame.Read returned error: %v", err)
	}
	if live.Type != frame.TypeLiveBytes || !bytes.Equal(live.Payload, daemonLive) {
		t.Fatalf("interactive frame = %#v, want 1KB live bytes", live)
	}

	close(clientDone)
	if err := <-serverErr; err != nil {
		t.Fatalf("server returned error: %v", err)
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
