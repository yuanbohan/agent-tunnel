package direct

import (
	"net"
	"time"
)

type UDPSocket struct {
	conn *net.UDPConn
}

func ListenUDPSocket(addr *net.UDPAddr) (*UDPSocket, error) {
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	return &UDPSocket{conn: conn}, nil
}

func NewUDPSocket(conn *net.UDPConn) *UDPSocket {
	return &UDPSocket{conn: conn}
}

func (s *UDPSocket) PacketConn() net.PacketConn {
	if s == nil {
		return nil
	}
	return s.conn
}

func (s *UDPSocket) LocalUDPAddr() *net.UDPAddr {
	if s == nil || s.conn == nil {
		return nil
	}
	addr, _ := s.conn.LocalAddr().(*net.UDPAddr)
	return addr
}

func (s *UDPSocket) WriteToUDP(payload []byte, addr *net.UDPAddr) (int, error) {
	return s.conn.WriteToUDP(payload, addr)
}

func (s *UDPSocket) ReadFromUDP(payload []byte) (int, *net.UDPAddr, error) {
	return s.conn.ReadFromUDP(payload)
}

func (s *UDPSocket) SetReadDeadline(t time.Time) error {
	return s.conn.SetReadDeadline(t)
}

func (s *UDPSocket) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.Close()
}
