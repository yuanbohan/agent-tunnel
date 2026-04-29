package direct

import (
	"net"
	"strings"
	"time"
)

var ProbePayload = []byte("tunnel-direct-probe/1")

func SendProbes(conn interface {
	WriteToUDP([]byte, *net.UDPAddr) (int, error)
}, candidates []string) int {
	if conn == nil {
		return 0
	}
	sent := 0
	for _, raw := range candidates {
		addr, err := net.ResolveUDPAddr("udp", strings.TrimSpace(raw))
		if err != nil || addr == nil {
			continue
		}
		if _, err := conn.WriteToUDP(ProbePayload, addr); err == nil {
			sent++
		}
	}
	return sent
}

func ProbeBurst(conn interface {
	WriteToUDP([]byte, *net.UDPAddr) (int, error)
}, candidates []string, count int, interval time.Duration) int {
	if count <= 0 {
		count = 1
	}
	sent := 0
	for i := 0; i < count; i++ {
		sent += SendProbes(conn, candidates)
		if i+1 < count && interval > 0 {
			time.Sleep(interval)
		}
	}
	return sent
}
