package stun

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"time"
)

const (
	bindingRequest  = 0x0001
	bindingResponse = 0x0101

	xorMappedAddress = 0x0020
	magicCookie      = 0x2112A442

	headerLen        = 20
	transactionIDLen = 12
)

var errInvalidMessage = errors.New("invalid STUN message")

// Server answers Binding requests with XOR-MAPPED-ADDRESS. It is stateless and
// intentionally implements only the small RFC 8489 surface needed for UDP
// address discovery.
type Server struct{}

func (s *Server) Serve(ctx context.Context, conn net.PacketConn) error {
	defer conn.Close()

	var buf [1500]byte
	for {
		if deadlineErr := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); deadlineErr != nil {
			return deadlineErr
		}
		n, addr, err := conn.ReadFrom(buf[:])
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		response, ok := BuildBindingResponse(buf[:n], addr)
		if !ok {
			continue
		}
		if _, err := conn.WriteTo(response, addr); err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
	}
}

func BuildBindingRequest(transactionID [transactionIDLen]byte) []byte {
	msg := make([]byte, headerLen)
	binary.BigEndian.PutUint16(msg[0:2], bindingRequest)
	binary.BigEndian.PutUint32(msg[4:8], magicCookie)
	copy(msg[8:20], transactionID[:])
	return msg
}

func BuildBindingResponse(request []byte, from net.Addr) ([]byte, bool) {
	transactionID, ok := bindingTransactionID(request)
	if !ok {
		return nil, false
	}

	udpAddr, ok := from.(*net.UDPAddr)
	if !ok || udpAddr == nil {
		return nil, false
	}
	value, ok := xorMappedAddressValue(udpAddr, transactionID)
	if !ok {
		return nil, false
	}

	msgLen := 4 + len(value)
	response := make([]byte, headerLen+msgLen)
	binary.BigEndian.PutUint16(response[0:2], bindingResponse)
	binary.BigEndian.PutUint16(response[2:4], uint16(msgLen))
	binary.BigEndian.PutUint32(response[4:8], magicCookie)
	copy(response[8:20], transactionID[:])
	binary.BigEndian.PutUint16(response[20:22], xorMappedAddress)
	binary.BigEndian.PutUint16(response[22:24], uint16(len(value)))
	copy(response[24:], value)
	return response, true
}

func ParseXORMappedAddress(message []byte) (*net.UDPAddr, error) {
	addr, _, err := ParseBindingSuccess(message)
	return addr, err
}

func ParseBindingSuccess(message []byte) (*net.UDPAddr, [transactionIDLen]byte, error) {
	transactionID, err := responseTransactionID(message)
	if err != nil {
		return nil, transactionID, err
	}

	attrs := message[headerLen:]
	for len(attrs) >= 4 {
		attrType := binary.BigEndian.Uint16(attrs[0:2])
		attrLen := int(binary.BigEndian.Uint16(attrs[2:4]))
		valueStart := 4
		valueEnd := valueStart + attrLen
		if valueEnd > len(attrs) {
			return nil, transactionID, errInvalidMessage
		}
		if attrType == xorMappedAddress {
			addr, err := parseXORMappedAddressValue(attrs[valueStart:valueEnd], transactionID)
			return addr, transactionID, err
		}
		padded := valueEnd + padding(attrLen)
		if padded > len(attrs) {
			return nil, transactionID, errInvalidMessage
		}
		attrs = attrs[padded:]
	}
	return nil, transactionID, errInvalidMessage
}

func bindingTransactionID(message []byte) ([transactionIDLen]byte, bool) {
	var transactionID [transactionIDLen]byte
	if len(message) < headerLen {
		return transactionID, false
	}
	if binary.BigEndian.Uint16(message[0:2]) != bindingRequest {
		return transactionID, false
	}
	msgLen := int(binary.BigEndian.Uint16(message[2:4]))
	if msgLen != len(message)-headerLen || msgLen%4 != 0 {
		return transactionID, false
	}
	if binary.BigEndian.Uint32(message[4:8]) != magicCookie {
		return transactionID, false
	}
	copy(transactionID[:], message[8:20])
	return transactionID, true
}

func responseTransactionID(message []byte) ([transactionIDLen]byte, error) {
	var transactionID [transactionIDLen]byte
	if len(message) < headerLen {
		return transactionID, errInvalidMessage
	}
	if binary.BigEndian.Uint16(message[0:2]) != bindingResponse {
		return transactionID, errInvalidMessage
	}
	msgLen := int(binary.BigEndian.Uint16(message[2:4]))
	if msgLen != len(message)-headerLen || msgLen%4 != 0 {
		return transactionID, errInvalidMessage
	}
	if binary.BigEndian.Uint32(message[4:8]) != magicCookie {
		return transactionID, errInvalidMessage
	}
	copy(transactionID[:], message[8:20])
	return transactionID, nil
}

func xorMappedAddressValue(addr *net.UDPAddr, transactionID [transactionIDLen]byte) ([]byte, bool) {
	if ip4 := addr.IP.To4(); ip4 != nil {
		value := make([]byte, 8)
		value[1] = 0x01
		binary.BigEndian.PutUint16(value[2:4], uint16(addr.Port)^uint16(magicCookie>>16))
		cookie := uint32(magicCookie)
		for i := range ip4 {
			value[4+i] = ip4[i] ^ byte(cookie>>(24-(8*i)))
		}
		return value, true
	}
	if ip16 := addr.IP.To16(); ip16 != nil {
		value := make([]byte, 20)
		value[1] = 0x02
		binary.BigEndian.PutUint16(value[2:4], uint16(addr.Port)^uint16(magicCookie>>16))
		xorKey := make([]byte, 16)
		binary.BigEndian.PutUint32(xorKey[0:4], magicCookie)
		copy(xorKey[4:], transactionID[:])
		for i := range ip16 {
			value[4+i] = ip16[i] ^ xorKey[i]
		}
		return value, true
	}
	return nil, false
}

func parseXORMappedAddressValue(value []byte, transactionID [transactionIDLen]byte) (*net.UDPAddr, error) {
	if len(value) < 4 || value[0] != 0 {
		return nil, errInvalidMessage
	}
	port := int(binary.BigEndian.Uint16(value[2:4]) ^ uint16(magicCookie>>16))
	switch value[1] {
	case 0x01:
		if len(value) != 8 {
			return nil, errInvalidMessage
		}
		ip := make(net.IP, net.IPv4len)
		cookie := uint32(magicCookie)
		for i := range ip {
			ip[i] = value[4+i] ^ byte(cookie>>(24-(8*i)))
		}
		return &net.UDPAddr{IP: ip, Port: port}, nil
	case 0x02:
		if len(value) != 20 {
			return nil, errInvalidMessage
		}
		ip := make(net.IP, net.IPv6len)
		xorKey := make([]byte, 16)
		binary.BigEndian.PutUint32(xorKey[0:4], magicCookie)
		copy(xorKey[4:], transactionID[:])
		for i := range ip {
			ip[i] = value[4+i] ^ xorKey[i]
		}
		return &net.UDPAddr{IP: ip, Port: port}, nil
	default:
		return nil, errInvalidMessage
	}
}

func padding(n int) int {
	remainder := n % 4
	if remainder == 0 {
		return 0
	}
	return 4 - remainder
}
