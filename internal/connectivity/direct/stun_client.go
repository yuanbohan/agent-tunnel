package direct

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	stunwire "yuanbohan/tunnel/internal/connectivity/stun"
)

const (
	DefaultSTUNRetries = 3
	DefaultSTUNTimeout = 250 * time.Millisecond
)

var (
	ErrSTUNTimeout            = errors.New("stun_timeout")
	ErrSTUNUnexpectedResponse = errors.New("stun_unexpected_response")
)

type STUNClient struct {
	ServerAddr *net.UDPAddr
	Retries    int
	Timeout    time.Duration
	Rand       io.Reader
}

func (c STUNClient) Discover(ctx context.Context, socket *UDPSocket) (*net.UDPAddr, error) {
	if c.ServerAddr == nil {
		return nil, fmt.Errorf("%w: missing server address", ErrSTUNUnexpectedResponse)
	}
	if socket == nil || socket.conn == nil {
		return nil, fmt.Errorf("%w: missing UDP socket", ErrSTUNUnexpectedResponse)
	}

	retries := c.Retries
	if retries <= 0 {
		retries = DefaultSTUNRetries
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultSTUNTimeout
	}
	random := c.Rand
	if random == nil {
		random = rand.Reader
	}

	var transactionID [12]byte
	if _, err := io.ReadFull(random, transactionID[:]); err != nil {
		return nil, err
	}

	defer socket.SetReadDeadline(time.Time{})
	request := stunwire.BuildBindingRequest(transactionID)
	buf := make([]byte, 1500)

	for attempt := 0; attempt < retries; attempt++ {
		if _, err := socket.WriteToUDP(request, c.ServerAddr); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(timeout)
		for {
			if err := socket.SetReadDeadline(deadline); err != nil {
				return nil, err
			}
			n, from, err := socket.ReadFromUDP(buf)
			if err != nil {
				if errors.Is(err, os.ErrDeadlineExceeded) {
					break
				}
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					break
				}
				return nil, err
			}
			if !sameUDPAddr(from, c.ServerAddr) {
				continue
			}
			addr, responseTransactionID, err := stunwire.ParseBindingSuccess(buf[:n])
			if err != nil {
				continue
			}
			if responseTransactionID != transactionID {
				continue
			}
			return addr, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return nil, ErrSTUNTimeout
}

func sameUDPAddr(left, right *net.UDPAddr) bool {
	if left == nil || right == nil {
		return false
	}
	return left.Port == right.Port && left.IP.Equal(right.IP)
}
