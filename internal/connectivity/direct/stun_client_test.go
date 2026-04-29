package direct

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	stunwire "yuanbohan/tunnel/internal/connectivity/stun"
)

func TestSTUNClientDiscoversPublicAddressAndKeepsSocketUsable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket server: %v", err)
	}
	go func() {
		_ = (&stunwire.Server{}).Serve(ctx, serverConn)
	}()

	socket, err := ListenUDPSocket(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDPSocket: %v", err)
	}
	defer socket.Close()

	got, err := (STUNClient{
		ServerAddr: serverConn.LocalAddr().(*net.UDPAddr),
		Rand:       bytes.NewReader([]byte("abcdefghijkl")),
		Timeout:    time.Second,
		Retries:    1,
	}).Discover(context.Background(), socket)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if want := socket.LocalUDPAddr(); !got.IP.Equal(want.IP) || got.Port != want.Port {
		t.Fatalf("discovered address = %v, want %v", got, want)
	}

	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP peer: %v", err)
	}
	defer peer.Close()
	if _, err := peer.WriteToUDP([]byte("after-stun"), socket.LocalUDPAddr()); err != nil {
		t.Fatalf("peer WriteToUDP: %v", err)
	}
	if err := socket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 64)
	n, _, err := socket.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP after STUN: %v", err)
	}
	if string(buf[:n]) != "after-stun" {
		t.Fatalf("payload = %q, want after-stun", string(buf[:n]))
	}
}

func TestSTUNClientTimesOutAfterRetryBudget(t *testing.T) {
	socket, err := ListenUDPSocket(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDPSocket: %v", err)
	}
	defer socket.Close()

	serverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP server: %v", err)
	}
	defer serverConn.Close()

	_, err = (STUNClient{
		ServerAddr: serverConn.LocalAddr().(*net.UDPAddr),
		Rand:       bytes.NewReader([]byte("abcdefghijkl")),
		Timeout:    20 * time.Millisecond,
		Retries:    2,
	}).Discover(context.Background(), socket)
	if !errors.Is(err, ErrSTUNTimeout) {
		t.Fatalf("Discover err = %v, want ErrSTUNTimeout", err)
	}
}

func TestSTUNClientIgnoresMalformedWrongTransactionAndUnexpectedServer(t *testing.T) {
	socket, err := ListenUDPSocket(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDPSocket: %v", err)
	}
	defer socket.Close()

	serverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP server: %v", err)
	}
	defer serverConn.Close()

	unexpectedConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP unexpected: %v", err)
	}
	defer unexpectedConn.Close()

	go func() {
		buf := make([]byte, 1500)
		n, clientAddr, err := serverConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		_, _ = serverConn.WriteToUDP([]byte("malformed"), clientAddr)
		wrongRequest := stunwire.BuildBindingRequest([12]byte{9, 9, 9})
		wrongResponse, ok := stunwire.BuildBindingResponse(wrongRequest, clientAddr)
		if ok {
			_, _ = serverConn.WriteToUDP(wrongResponse, clientAddr)
		}
		response, ok := stunwire.BuildBindingResponse(buf[:n], clientAddr)
		if ok {
			_, _ = unexpectedConn.WriteToUDP(response, clientAddr)
		}
	}()

	_, err = (STUNClient{
		ServerAddr: serverConn.LocalAddr().(*net.UDPAddr),
		Rand:       bytes.NewReader([]byte("abcdefghijkl")),
		Timeout:    50 * time.Millisecond,
		Retries:    1,
	}).Discover(context.Background(), socket)
	if !errors.Is(err, ErrSTUNTimeout) {
		t.Fatalf("Discover err = %v, want ErrSTUNTimeout", err)
	}
}
