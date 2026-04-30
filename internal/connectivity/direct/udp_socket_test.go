package direct

import (
	"bytes"
	"crypto/ed25519"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/identity"
	"yuanbohan/tunnel/internal/connectivity/transport"
)

func TestUDPSocketRemainsUsableForPacketReadsAndWrites(t *testing.T) {
	socket, err := ListenUDPSocket(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDPSocket: %v", err)
	}
	defer socket.Close()

	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP peer: %v", err)
	}
	defer peer.Close()

	if _, err := peer.WriteToUDP([]byte("probe"), socket.LocalUDPAddr()); err != nil {
		t.Fatalf("peer WriteToUDP: %v", err)
	}
	if err := socket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 64)
	n, from, err := socket.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("socket ReadFromUDP: %v", err)
	}
	if string(buf[:n]) != "probe" {
		t.Fatalf("payload = %q, want probe", string(buf[:n]))
	}
	if _, err := socket.WriteToUDP([]byte("ack"), from); err != nil {
		t.Fatalf("socket WriteToUDP: %v", err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("peer SetReadDeadline: %v", err)
	}
	n, _, err = peer.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("peer ReadFromUDP: %v", err)
	}
	if string(buf[:n]) != "ack" {
		t.Fatalf("payload = %q, want ack", string(buf[:n]))
	}
}

func TestUDPSocketCanBackQUICListenerAfterPacketUse(t *testing.T) {
	socket, err := ListenUDPSocket(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDPSocket: %v", err)
	}
	defer socket.Close()

	daemonKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize))
	androidKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, ed25519.SeedSize))
	daemonCert, err := identity.SelfSignedCertificate(daemonKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("SelfSignedCertificate: %v", err)
	}

	listener, err := quic.Listen(socket.PacketConn(), transport.DaemonTLSConfig(transport.EndpointConfig{
		Certificate:         daemonCert,
		PinnedPeerPublicKey: androidKey.Public().(ed25519.PublicKey),
	}), transport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Listen: %v", err)
	}
	defer listener.Close()
}
