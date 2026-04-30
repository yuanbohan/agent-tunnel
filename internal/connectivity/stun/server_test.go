package stun

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

func TestServerAnswersBindingRequestWithObservedAddress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket server: %v", err)
	}
	go func() {
		_ = (&Server{}).Serve(ctx, serverConn)
	}()

	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket client: %v", err)
	}
	defer clientConn.Close()

	transactionID := [transactionIDLen]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if _, err := clientConn.WriteTo(BuildBindingRequest(transactionID), serverConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo binding request: %v", err)
	}

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 1500)
	n, _, err := clientConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom response: %v", err)
	}
	got, err := ParseXORMappedAddress(buf[:n])
	if err != nil {
		t.Fatalf("ParseXORMappedAddress: %v", err)
	}

	want := clientConn.LocalAddr().(*net.UDPAddr)
	if !got.IP.Equal(want.IP) || got.Port != want.Port {
		t.Fatalf("XOR-MAPPED-ADDRESS = %v, want %v", got, want)
	}
}

func TestBuildBindingResponseIgnoresMalformedDatagram(t *testing.T) {
	if response, ok := BuildBindingResponse([]byte("not stun"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}); ok || response != nil {
		t.Fatalf("BuildBindingResponse returned (%x, %v), want nil false", response, ok)
	}
}

func TestBuildBindingResponseIgnoresNonBindingMethod(t *testing.T) {
	msg := BuildBindingRequest([transactionIDLen]byte{1})
	binary.BigEndian.PutUint16(msg[0:2], 0x0003)

	if response, ok := BuildBindingResponse(msg, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}); ok || response != nil {
		t.Fatalf("BuildBindingResponse returned (%x, %v), want nil false", response, ok)
	}
}

func TestServerIgnoresMalformedDatagram(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket server: %v", err)
	}
	go func() {
		_ = (&Server{}).Serve(ctx, serverConn)
	}()

	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket client: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.WriteTo([]byte("bad"), serverConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo malformed datagram: %v", err)
	}
	if err := clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 1500)
	_, _, err = clientConn.ReadFrom(buf)
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("ReadFrom err = %v, want timeout", err)
	}
}
