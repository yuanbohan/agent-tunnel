package direct

import (
	"net"
	"strconv"
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
		addr := parseLiteralUDPAddr(raw)
		if addr == nil {
			continue
		}
		if _, err := conn.WriteToUDP(ProbePayload, addr); err == nil {
			sent++
		}
	}
	return sent
}

func parseLiteralUDPAddr(raw string) *net.UDPAddr {
	host, port, err := net.SplitHostPort(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return nil
	}
	return &net.UDPAddr{IP: ip, Port: portNum}
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
