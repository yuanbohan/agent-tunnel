package direct

import (
	"net"
	"testing"
	"time"
)

func TestSendProbesWritesToValidCandidates(t *testing.T) {
	receiver, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP receiver: %v", err)
	}
	defer receiver.Close()

	sender, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP sender: %v", err)
	}
	defer sender.Close()

	sent := SendProbes(sender, []string{"bad", receiver.LocalAddr().String()})
	if sent != 1 {
		t.Fatalf("SendProbes = %d, want 1", sent)
	}

	if err := receiver.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 64)
	n, _, err := receiver.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	if string(buf[:n]) != string(ProbePayload) {
		t.Fatalf("payload = %q, want probe payload", string(buf[:n]))
	}
}
