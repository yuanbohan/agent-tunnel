package carrier

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/frame"
	"yuanbohan/tunnel/internal/connectivity/identity"
	"yuanbohan/tunnel/internal/connectivity/transport"
)

func TestWSPacketConnExchangesBinaryPackets(t *testing.T) {
	leftWS, rightWS, closeServer := websocketPair(t)
	defer closeServer()
	left := NewWSPacketConn(leftWS, "left", "right", frame.DefaultMaxPayload)
	defer left.Close()
	right := NewWSPacketConn(rightWS, "right", "left", frame.DefaultMaxPayload)
	defer right.Close()

	payload := []byte("encrypted packet")
	if _, err := left.WriteTo(payload, right.LocalAddr()); err != nil {
		t.Fatalf("left WriteTo returned error: %v", err)
	}
	buf := make([]byte, 1024)
	n, addr, err := right.ReadFrom(buf)
	if err != nil {
		t.Fatalf("right ReadFrom returned error: %v", err)
	}
	if addr.String() != "left" || string(buf[:n]) != string(payload) {
		t.Fatalf("ReadFrom addr=%s payload=%q, want left payload", addr, buf[:n])
	}
}

func TestWSPacketConnReadDeadline(t *testing.T) {
	leftWS, _, closeServer := websocketPair(t)
	defer closeServer()
	left := NewWSPacketConn(leftWS, "left", "right", frame.DefaultMaxPayload)
	defer left.Close()
	if err := left.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	var buf [8]byte
	_, _, err := left.ReadFrom(buf[:])
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("ReadFrom err = %v, want deadline exceeded", err)
	}
}

func TestWSPacketConnRejectsUnknownPeer(t *testing.T) {
	leftWS, _, closeServer := websocketPair(t)
	defer closeServer()
	left := NewWSPacketConn(leftWS, "left", "right", frame.DefaultMaxPayload)
	defer left.Close()
	if _, err := left.WriteTo([]byte("packet"), Addr("other")); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("WriteTo err = %v, want ErrUnknownEndpoint", err)
	}
}

func TestWSPacketConnDefaultsWriteDeadline(t *testing.T) {
	now := time.Unix(1700000000, 0)
	got := wsPacketWriteDeadline(time.Time{}, now)
	if got.IsZero() || got.Sub(now) != defaultWSPacketWriteTimeout {
		t.Fatalf("default write deadline = %v, want %s after now", got, defaultWSPacketWriteTimeout)
	}
	explicit := now.Add(time.Second)
	if got := wsPacketWriteDeadline(explicit, now); !got.Equal(explicit) {
		t.Fatalf("explicit write deadline = %v, want %v", got, explicit)
	}
}

func TestWSPacketConnRunsQUICOverWebSocketPackets(t *testing.T) {
	leftWS, rightWS, closeServer := websocketPair(t)
	defer closeServer()
	serverPacketConn := NewWSPacketConn(leftWS, "daemon", "android", frame.DefaultMaxPayload)
	defer serverPacketConn.Close()
	clientPacketConn := NewWSPacketConn(rightWS, "android", "daemon", frame.DefaultMaxPayload)
	defer clientPacketConn.Close()

	serverTLS, clientTLS := interopTLSConfigs(t)
	listener, err := quic.Listen(serverPacketConn, serverTLS, transport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Listen returned error: %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.CloseWithError(0, "done")
		serverErr <- transport.ValidateConnectionState(conn.ConnectionState())
	}()

	conn, err := quic.Dial(ctx, clientPacketConn, serverPacketConn.LocalAddr(), clientTLS, transport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Dial returned error: %v", err)
	}
	defer conn.CloseWithError(0, "done")
	if err := transport.ValidateConnectionState(conn.ConnectionState()); err != nil {
		t.Fatalf("client ValidateConnectionState returned error: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server returned error: %v", err)
	}
}

func websocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	accepted := make(chan *websocket.Conn, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade returned error: %v", err)
			return
		}
		accepted <- conn
	}))
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	left, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		server.Close()
		t.Fatalf("left Dial returned error: %v", err)
	}
	right, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		left.Close()
		server.Close()
		t.Fatalf("right Dial returned error: %v", err)
	}
	serverLeft := <-accepted
	serverRight := <-accepted
	go proxyWebSockets(serverLeft, serverRight)
	go proxyWebSockets(serverRight, serverLeft)
	return left, right, func() {
		left.Close()
		right.Close()
		serverLeft.Close()
		serverRight.Close()
		server.Close()
	}
}

func proxyWebSockets(from, to *websocket.Conn) {
	var writeMu syncWriter
	for {
		messageType, payload, err := from.ReadMessage()
		if err != nil {
			_ = to.Close()
			return
		}
		writeMu.write(to, messageType, payload)
	}
}

type syncWriter struct {
	ch chan struct{}
}

func (w *syncWriter) write(conn *websocket.Conn, messageType int, payload []byte) {
	if w.ch == nil {
		w.ch = make(chan struct{}, 1)
		w.ch <- struct{}{}
	}
	<-w.ch
	defer func() { w.ch <- struct{}{} }()
	_ = conn.WriteMessage(messageType, payload)
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
