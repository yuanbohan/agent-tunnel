package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

const (
	actionStatus                = "status"
	actionStop                  = "stop"
	actionDoctor                = "doctor"
	actionPair                  = "pair"
	actionPendingPairing        = "pending_pairing"
	actionConfirmPendingPairing = "confirm_pending_pairing"
	actionDevices               = "devices"
	actionRevokeDevice          = "revoke_device"
)

var ErrNotRunning = errors.New("daemon is not running")

type StatusInfo struct {
	Running           bool   `json:"running"`
	PID               int    `json:"pid,omitempty"`
	StartedAt         int64  `json:"started_at,omitempty"`
	BaseURL           string `json:"base_url,omitempty"`
	DeviceID          string `json:"device_id,omitempty"`
	DaemonFingerprint string `json:"daemon_fingerprint,omitempty"`
	DisplayName       string `json:"display_name,omitempty"`
	Hostname          string `json:"hostname,omitempty"`
	PlatformFamily    string `json:"platform_family,omitempty"`
	PlatformID        string `json:"platform_id,omitempty"`
	RelayConnected    bool   `json:"relay_connected"`
	LaunchHealth      string `json:"launch_health,omitempty"`
	WorkspaceBackend  string `json:"workspace_backend,omitempty"`
	LastFailure       string `json:"last_failure,omitempty"`
}

type Request struct {
	Action            string `json:"action"`
	DeviceFingerprint string `json:"device_fingerprint,omitempty"`
	InvitationID      string `json:"invitation_id,omitempty"`
	SAS               string `json:"sas,omitempty"`
}

type Response struct {
	Status         *StatusInfo              `json:"status,omitempty"`
	Doctor         *DoctorReport            `json:"doctor,omitempty"`
	PairInvitation *PairInvitation          `json:"pair_invitation,omitempty"`
	PendingPairing []PendingPairingResponse `json:"pending_pairing,omitempty"`
	PairCompletion *PairingCompletion       `json:"pair_completion,omitempty"`
	TrustedDevices []TrustedAndroidDevice   `json:"trusted_devices,omitempty"`
	TrustedDevice  *TrustedAndroidDevice    `json:"trusted_device,omitempty"`
	Error          string                   `json:"error,omitempty"`
}

type Server struct {
	listener net.Listener
	handler  func(context.Context, Request) Response
}

func NewServer(socketPath string, handler func(context.Context, Request) Response) (*Server, error) {
	info, err := os.Lstat(socketPath)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("socket path exists and is not a unix socket")
		}
		if err := os.Remove(socketPath); err != nil {
			return nil, err
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return &Server{listener: listener, handler: handler}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	if s == nil || s.listener == nil {
		return nil
	}

	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				if errors.Is(err, net.ErrClosed) {
					return nil
				}
				return err
			}
		}
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) Close() error {
	if s == nil || s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var request Request
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: err.Error()})
		return
	}
	response := Response{Error: "unsupported action"}
	if s.handler != nil {
		response = s.handler(ctx, request)
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func Status(ctx context.Context, paths Paths) (StatusInfo, error) {
	response, err := request(ctx, paths.SocketPath, Request{Action: actionStatus})
	if err != nil {
		status, loadErr := LoadStatus(paths)
		if loadErr == nil {
			status.Running = false
			status.RelayConnected = false
			return status, nil
		}
		if errors.Is(loadErr, os.ErrNotExist) {
			return StatusInfo{}, fmt.Errorf("%w; start it with `tunnel daemon start`", ErrNotRunning)
		}
		return StatusInfo{}, err
	}
	if response.Error != "" {
		return StatusInfo{}, errors.New(response.Error)
	}
	if response.Status == nil {
		return StatusInfo{}, errors.New("daemon returned empty status response")
	}
	return *response.Status, nil
}

func Stop(ctx context.Context, paths Paths) error {
	response, err := request(ctx, paths.SocketPath, Request{Action: actionStop})
	if err != nil {
		status, loadErr := LoadStatus(paths)
		switch {
		case loadErr == nil:
			if !status.Running || (status.PID > 0 && !processRunning(status.PID)) {
				return fmt.Errorf("%w; daemon is already stopped", ErrNotRunning)
			}
		case errors.Is(loadErr, os.ErrNotExist):
			return fmt.Errorf("%w; daemon is already stopped", ErrNotRunning)
		}
		return err
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}

func Doctor(ctx context.Context, paths Paths) (DoctorReport, error) {
	response, err := request(ctx, paths.SocketPath, Request{Action: actionDoctor})
	if err == nil && response.Error == "" && response.Doctor != nil {
		return *response.Doctor, nil
	}

	status, _ := LoadStatus(paths)
	report := BuildDoctorReport(ctx, paths, status)
	return report, nil
}

func Pair(ctx context.Context, paths Paths) (PairInvitation, error) {
	response, err := request(ctx, paths.SocketPath, Request{Action: actionPair})
	if err != nil {
		return PairInvitation{}, fmt.Errorf("%w; start it with `tunnel daemon start`", ErrNotRunning)
	}
	if response.Error != "" {
		return PairInvitation{}, errors.New(response.Error)
	}
	if response.PairInvitation == nil {
		return PairInvitation{}, errors.New("daemon returned empty pairing invitation response")
	}
	return *response.PairInvitation, nil
}

func PendingPairing(ctx context.Context, paths Paths) ([]PendingPairingResponse, error) {
	response, err := request(ctx, paths.SocketPath, Request{Action: actionPendingPairing})
	if err != nil {
		return nil, fmt.Errorf("%w; start it with `tunnel daemon start`", ErrNotRunning)
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	return response.PendingPairing, nil
}

func ConfirmPendingPairing(ctx context.Context, paths Paths, invitationID, sas string) (PairingCompletion, error) {
	response, err := request(ctx, paths.SocketPath, Request{
		Action:       actionConfirmPendingPairing,
		InvitationID: invitationID,
		SAS:          sas,
	})
	if err != nil {
		return PairingCompletion{}, fmt.Errorf("%w; start it with `tunnel daemon start`", ErrNotRunning)
	}
	if response.Error != "" {
		return PairingCompletion{}, errors.New(response.Error)
	}
	if response.PairCompletion == nil {
		return PairingCompletion{}, errors.New("daemon returned empty pairing completion response")
	}
	return *response.PairCompletion, nil
}

func TrustedDevices(ctx context.Context, paths Paths) ([]TrustedAndroidDevice, error) {
	response, err := request(ctx, paths.SocketPath, Request{Action: actionDevices})
	if err != nil {
		return nil, fmt.Errorf("%w; start it with `tunnel daemon start`", ErrNotRunning)
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	return response.TrustedDevices, nil
}

func RevokeTrustedDevice(ctx context.Context, paths Paths, fingerprint string) (TrustedAndroidDevice, error) {
	response, err := request(ctx, paths.SocketPath, Request{
		Action:            actionRevokeDevice,
		DeviceFingerprint: fingerprint,
	})
	if err != nil {
		return TrustedAndroidDevice{}, fmt.Errorf("%w; start it with `tunnel daemon start`", ErrNotRunning)
	}
	if response.Error != "" {
		return TrustedAndroidDevice{}, errors.New(response.Error)
	}
	if response.TrustedDevice == nil {
		return TrustedAndroidDevice{}, errors.New("daemon returned empty trusted device response")
	}
	return *response.TrustedDevice, nil
}

func request(ctx context.Context, socketPath string, payload Request) (Response, error) {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(payload); err != nil {
		return Response{}, err
	}
	var response Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return Response{}, err
	}
	return response, nil
}
