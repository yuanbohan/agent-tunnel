package transport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/identity"
)

func TestPinnedQUICHandshakeAndStreams(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 1024)
	reply := bytes.Repeat([]byte("b"), 1024)
	uniPayload := bytes.Repeat([]byte("c"), 1024)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server, clientConfig := newHarness(t)
	defer server.Close()

	clientDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := server.Listener.Accept(ctx)
		if err != nil {
			t.Errorf("Accept returned error: %v", err)
			return
		}
		defer conn.CloseWithError(0, "done")

		if err := ValidateConnectionState(conn.ConnectionState()); err != nil {
			t.Errorf("server connection state: %v", err)
			return
		}

		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			t.Errorf("AcceptStream returned error: %v", err)
			return
		}
		got, err := io.ReadAll(stream)
		if err != nil {
			t.Errorf("server ReadAll returned error: %v", err)
			return
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("server got %d bidi bytes, want %d", len(got), len(payload))
			return
		}
		if _, err := stream.Write(reply); err != nil {
			t.Errorf("server Write returned error: %v", err)
			return
		}
		if err := stream.Close(); err != nil {
			t.Errorf("server stream Close returned error: %v", err)
			return
		}

		uni, err := conn.OpenUniStreamSync(ctx)
		if err != nil {
			t.Errorf("OpenUniStreamSync returned error: %v", err)
			return
		}
		if _, err := uni.Write(uniPayload); err != nil {
			t.Errorf("uni Write returned error: %v", err)
			return
		}
		if err := uni.Close(); err != nil {
			t.Errorf("uni Close returned error: %v", err)
			return
		}
		<-clientDone
	}()

	conn, err := quic.DialAddr(ctx, server.Addr, clientConfig, QUICConfig())
	if err != nil {
		t.Fatalf("DialAddr returned error: %v", err)
	}
	defer conn.CloseWithError(0, "done")
	if err := ValidateConnectionState(conn.ConnectionState()); err != nil {
		t.Fatalf("client connection state: %v", err)
	}

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}
	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("client Write returned error: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("client stream Close returned error: %v", err)
	}

	gotReply, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("client ReadAll returned error: %v", err)
	}
	if !bytes.Equal(gotReply, reply) {
		t.Fatalf("client got %d reply bytes, want %d", len(gotReply), len(reply))
	}

	uni, err := conn.AcceptUniStream(ctx)
	if err != nil {
		t.Fatalf("AcceptUniStream returned error: %v", err)
	}
	gotUni, err := io.ReadAll(uni)
	if err != nil {
		t.Fatalf("client uni ReadAll returned error: %v", err)
	}
	if !bytes.Equal(gotUni, uniPayload) {
		t.Fatalf("client got %d uni bytes, want %d", len(gotUni), len(uniPayload))
	}

	close(clientDone)
	wg.Wait()
}

func TestRejectsALPNMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	server, clientConfig := newHarness(t)
	defer server.Close()

	badClientConfig := clientConfig.Clone()
	badClientConfig.NextProtos = []string{"wrong-proto/1"}
	_, err := quic.DialAddr(ctx, server.Addr, badClientConfig, QUICConfig())
	if err == nil {
		t.Fatal("DialAddr succeeded with mismatched ALPN")
	}
}

func TestRejectsPeerSPKIMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	server, clientConfig := newHarness(t)
	defer server.Close()

	wrongDaemon := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x99}, ed25519.SeedSize))

	badClientConfig := clientConfig.Clone()
	badClientConfig.VerifyPeerCertificate = AndroidTLSConfig(EndpointConfig{
		Certificate:         clientConfig.Certificates[0],
		PinnedPeerPublicKey: wrongDaemon.Public().(ed25519.PublicKey),
		ServerName:          "daemon.local",
	}).VerifyPeerCertificate

	_, err := quic.DialAddr(ctx, server.Addr, badClientConfig, QUICConfig())
	if err == nil {
		t.Fatal("DialAddr succeeded with wrong pinned daemon SPKI")
	}
	if !errors.Is(err, identity.ErrPinnedKeyMismatch) {
		t.Fatalf("err = %v, want ErrPinnedKeyMismatch", err)
	}
}

func TestDaemonTLSConfigRejectsClientSPKIMismatch(t *testing.T) {
	daemonKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize))
	androidKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, ed25519.SeedSize))
	wrongAndroidKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x98}, ed25519.SeedSize))

	daemonCert, err := identity.SelfSignedCertificate(daemonKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("daemon SelfSignedCertificate returned error: %v", err)
	}
	androidCert, err := identity.SelfSignedCertificate(androidKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("android SelfSignedCertificate returned error: %v", err)
	}

	config := DaemonTLSConfig(EndpointConfig{
		Certificate:         daemonCert,
		PinnedPeerPublicKey: wrongAndroidKey.Public().(ed25519.PublicKey),
	})
	err = config.VerifyPeerCertificate(androidCert.Certificate, nil)
	if !errors.Is(err, identity.ErrPinnedKeyMismatch) {
		t.Fatalf("err = %v, want ErrPinnedKeyMismatch", err)
	}
}

func TestDaemonTLSConfigRejectsMissingClientCertificate(t *testing.T) {
	daemonKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize))
	androidKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, ed25519.SeedSize))
	daemonCert, err := identity.SelfSignedCertificate(daemonKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("daemon SelfSignedCertificate returned error: %v", err)
	}

	config := DaemonTLSConfig(EndpointConfig{
		Certificate:         daemonCert,
		PinnedPeerPublicKey: androidKey.Public().(ed25519.PublicKey),
	})
	err = config.VerifyPeerCertificate(nil, nil)
	if !errors.Is(err, identity.ErrMissingPeerCertificate) {
		t.Fatalf("err = %v, want ErrMissingPeerCertificate", err)
	}
}

func TestQUICConfigDisables0RTT(t *testing.T) {
	if QUICConfig().Allow0RTT {
		t.Fatal("QUICConfig().Allow0RTT = true, want false")
	}
}

func TestTLSConfigsDisableSessionTickets(t *testing.T) {
	daemonKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize))
	androidKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, ed25519.SeedSize))
	daemonCert, err := identity.SelfSignedCertificate(daemonKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("daemon SelfSignedCertificate returned error: %v", err)
	}
	androidCert, err := identity.SelfSignedCertificate(androidKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("android SelfSignedCertificate returned error: %v", err)
	}

	daemonConfig := DaemonTLSConfig(EndpointConfig{
		Certificate:         daemonCert,
		PinnedPeerPublicKey: androidKey.Public().(ed25519.PublicKey),
	})
	if !daemonConfig.SessionTicketsDisabled {
		t.Fatal("DaemonTLSConfig SessionTicketsDisabled = false, want true")
	}

	clientConfig := AndroidTLSConfig(EndpointConfig{
		Certificate:         androidCert,
		PinnedPeerPublicKey: daemonKey.Public().(ed25519.PublicKey),
		ServerName:          "daemon.local",
	})
	if !clientConfig.SessionTicketsDisabled {
		t.Fatal("AndroidTLSConfig SessionTicketsDisabled = false, want true")
	}
}

func TestValidateConnectionStateRejectsBadALPNAnd0RTT(t *testing.T) {
	if err := ValidateConnectionState(quic.ConnectionState{}); !errors.Is(err, ErrALPNMismatch) {
		t.Fatalf("err = %v, want ErrALPNMismatch", err)
	}
	if err := ValidateConnectionState(quic.ConnectionState{
		TLS:      tls.ConnectionState{NegotiatedProtocol: ALPN},
		Used0RTT: true,
	}); !errors.Is(err, ErrEarlyData) {
		t.Fatalf("err = %v, want ErrEarlyData", err)
	}
}

func TestReconnectLoopClosesConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	before := runtime.NumGoroutine()
	server, clientConfig := newHarness(t)
	defer server.Close()

	for i := 0; i < 10; i++ {
		accepted := make(chan *quic.Conn, 1)
		acceptErr := make(chan error, 1)
		go func() {
			conn, err := server.Listener.Accept(ctx)
			if err != nil {
				acceptErr <- err
				return
			}
			accepted <- conn
		}()

		conn, err := quic.DialAddr(ctx, server.Addr, clientConfig, QUICConfig())
		if err != nil {
			t.Fatalf("DialAddr iteration %d returned error: %v", i, err)
		}
		if err := ValidateConnectionState(conn.ConnectionState()); err != nil {
			t.Fatalf("client connection state iteration %d: %v", i, err)
		}

		var serverConn *quic.Conn
		select {
		case serverConn = <-accepted:
		case err := <-acceptErr:
			t.Fatalf("Accept iteration %d returned error: %v", i, err)
		case <-ctx.Done():
			t.Fatalf("Accept iteration %d timed out: %v", i, ctx.Err())
		}
		if err := ValidateConnectionState(serverConn.ConnectionState()); err != nil {
			t.Fatalf("server connection state iteration %d: %v", i, err)
		}

		if err := conn.CloseWithError(0, "done"); err != nil {
			t.Fatalf("CloseWithError iteration %d returned error: %v", i, err)
		}
		_ = serverConn.CloseWithError(0, "done")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= before+20 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutine count after reconnect loop = %d, before = %d", runtime.NumGoroutine(), before)
}

type harnessServer struct {
	Addr     string
	Listener *quic.Listener
}

func (s harnessServer) Close() {
	_ = s.Listener.Close()
}

func newHarness(t *testing.T) (harnessServer, *tls.Config) {
	t.Helper()

	daemonKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize))
	androidKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, ed25519.SeedSize))

	daemonCert, err := identity.SelfSignedCertificate(daemonKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("daemon SelfSignedCertificate returned error: %v", err)
	}
	androidCert, err := identity.SelfSignedCertificate(androidKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("android SelfSignedCertificate returned error: %v", err)
	}

	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP returned error: %v", err)
	}

	listener, err := quic.Listen(packetConn, DaemonTLSConfig(EndpointConfig{
		Certificate:         daemonCert,
		PinnedPeerPublicKey: androidKey.Public().(ed25519.PublicKey),
	}), QUICConfig())
	if err != nil {
		t.Fatalf("quic.Listen returned error: %v", err)
	}

	clientConfig := AndroidTLSConfig(EndpointConfig{
		Certificate:         androidCert,
		PinnedPeerPublicKey: daemonKey.Public().(ed25519.PublicKey),
		ServerName:          "daemon.local",
	})

	return harnessServer{Addr: packetConn.LocalAddr().String(), Listener: listener}, clientConfig
}
