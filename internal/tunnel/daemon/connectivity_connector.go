package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/buildinfo"
	connectivitypairing "yuanbohan/tunnel/internal/connectivity/pairing"
	"yuanbohan/tunnel/internal/protocol"
)

type connectivityConnector struct {
	baseURL string
	token   string
	paths   Paths
	state   *runtimeState
	dialer  *websocket.Dialer
}

func newConnectivityConnector(baseURL, token string, paths Paths, state *runtimeState) *connectivityConnector {
	return &connectivityConnector{
		baseURL: baseURL,
		token:   token,
		paths:   paths,
		state:   state,
		dialer:  websocket.DefaultDialer,
	}
}

func (c *connectivityConnector) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := c.serveOnce(ctx); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 15*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

func (c *connectivityConnector) serveOnce(ctx context.Context) error {
	wsURL, err := connectivityDaemonWebSocketURL(c.baseURL)
	if err != nil {
		return err
	}
	headers := http.Header{"Authorization": {"Bearer " + c.token}}
	conn, _, err := c.dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return err
	}
	defer conn.Close()

	register, err := c.registerFrame()
	if err != nil {
		return err
	}
	if err := conn.WriteJSON(register); err != nil {
		return err
	}

	readErr := make(chan error, 1)
	go func() {
		for {
			var frame protocol.ConnectivityFrame
			if err := conn.ReadJSON(&frame); err != nil {
				readErr <- err
				return
			}
			switch frame.Type {
			case "pair_invitation_reserved", "error":
				c.state.deliverConnectivityReply(frame)
			case "pair_response_forward":
				_ = c.handlePairResponseForward(frame)
			default:
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			return err
		case frame := <-c.state.connectivityEvents:
			if err := conn.WriteJSON(frame); err != nil {
				return err
			}
		}
	}
}

func (c *connectivityConnector) handlePairResponseForward(frame protocol.ConnectivityFrame) error {
	if frame.PairingResponse == nil {
		return nil
	}
	_, err := StorePendingPairingResponse(c.paths, connectivitypairing.AndroidResponse{
		Version:            connectivitypairing.Version,
		AccountID:          frame.PairingResponse.AccountID,
		InvitationID:       frame.PairingResponse.InvitationID,
		CorrelationID:      frame.PairingResponse.CorrelationID,
		AndroidPublicKey:   frame.PairingResponse.AndroidPublicKey,
		AndroidFingerprint: frame.PairingResponse.AndroidFingerprint,
		AndroidDisplayName: frame.PairingResponse.AndroidDisplayName,
		Signature:          frame.PairingResponse.Signature,
	}, time.Now().UTC())
	return err
}

func (c *connectivityConnector) registerFrame() (protocol.ConnectivityFrame, error) {
	identity, err := ReadConnectivityIdentity(c.paths)
	if err != nil {
		return protocol.ConnectivityFrame{}, err
	}
	devices, err := ListTrustedAndroidDevices(c.paths)
	if err != nil {
		return protocol.ConnectivityFrame{}, err
	}
	trusted := make([]protocol.ConnectivityTrustedAndroid, 0, len(devices))
	for _, device := range devices {
		trusted = append(trusted, protocol.ConnectivityTrustedAndroid{
			Fingerprint: device.Fingerprint,
			DisplayName: device.DisplayName,
		})
	}
	status := c.state.snapshot()
	return protocol.ConnectivityDaemonRegisterFrame(protocol.ConnectivityDaemonInfo{
		DeviceID:          status.DeviceID,
		DisplayName:       status.DisplayName,
		PlatformFamily:    status.PlatformFamily,
		PlatformID:        status.PlatformID,
		DaemonPublicKey:   fmt.Sprintf("%x", identity.PublicKey),
		DaemonFingerprint: identity.Fingerprint,
		TunnelVersion:     buildinfo.Version,
	}, trusted), nil
}

func connectivityDaemonWebSocketURL(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported base URL scheme: %s", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/connectivity/daemon/ws"
	return parsed.String(), nil
}
