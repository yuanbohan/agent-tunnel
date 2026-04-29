package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"yuanbohan/tunnel/internal/protocol"
)

const readySignalEnv = "TUNNEL_DAEMON_READY_FD"
const daemonAuthTokenEnv = "TUNNEL_AUTH_TOKEN"

var daemonStartupLockTimeout = 10 * time.Second

type RuntimeOptions struct {
	Paths     Paths
	BaseURL   string
	AuthToken string
}

type StartOptions struct {
	Executable string
	Paths      Paths
	BaseURL    string
	AuthToken  string
}

type StartResult struct {
	AlreadyRunning    bool
	Status            StatusInfo
	PreservedSessions int
}

type runtimeState struct {
	mu                  sync.RWMutex
	status              StatusInfo
	stopCh              chan struct{}
	stopOnce            sync.Once
	paths               Paths
	broker              *Broker
	connectivityEvents  chan protocol.ConnectivityFrame
	connectivityReplies map[string]chan protocol.ConnectivityFrame
}

func StartBackground(ctx context.Context, options StartOptions) (StartResult, error) {
	if err := EnsureRuntimeDirs(options.Paths); err != nil {
		return StartResult{}, err
	}
	releaseLock, err := acquireStartupLock(ctx, options.Paths)
	if err != nil {
		return StartResult{}, err
	}
	defer releaseLock()

	status, err := currentRunningStatus(ctx, options.Paths)
	if err == nil && status.Running {
		return StartResult{AlreadyRunning: true, Status: status}, nil
	}
	preservedSessions := 0
	if err := EnsureTmuxAvailable(); err == nil {
		preservedSessions, err = CountWorkspaceSessions(ctx, options.Paths)
		if err != nil {
			return StartResult{}, err
		}
	}

	if err := cleanupStaleRuntime(options.Paths); err != nil {
		return StartResult{}, err
	}

	if strings.TrimSpace(options.Executable) == "" {
		return StartResult{}, errors.New("daemon executable is required")
	}
	if strings.TrimSpace(options.AuthToken) == "" {
		return StartResult{}, errors.New("TUNNEL_AUTH_TOKEN environment variable is required")
	}

	readReady, writeReady, err := os.Pipe()
	if err != nil {
		return StartResult{}, err
	}
	defer readReady.Close()

	cmd := exec.CommandContext(ctx, options.Executable, "daemon", "internal-run", "--base-url", options.BaseURL)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("%s=%s", daemonAuthTokenEnv, options.AuthToken),
		fmt.Sprintf("%s=%d", readySignalEnv, 3),
	)
	cmd.ExtraFiles = []*os.File{writeReady}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		_ = writeReady.Close()
		return StartResult{}, err
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	configureBackgroundProcess(cmd)

	if err := cmd.Start(); err != nil {
		_ = writeReady.Close()
		return StartResult{}, err
	}
	_ = writeReady.Close()

	readyCh := make(chan error, 1)
	go func() {
		buffer := make([]byte, 256)
		n, readErr := readReady.Read(buffer)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			readyCh <- readErr
			return
		}
		message := strings.TrimSpace(string(buffer[:n]))
		switch {
		case strings.HasPrefix(message, "error:"):
			readyCh <- errors.New(strings.TrimPrefix(message, "error:"))
		case message == "ready":
			readyCh <- nil
		default:
			readyCh <- fmt.Errorf("daemon failed to start")
		}
	}()

	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	select {
	case err := <-readyCh:
		if err != nil {
			_ = cmd.Wait()
			return StartResult{}, err
		}
	case <-timeout.C:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return StartResult{}, errors.New("timed out waiting for daemon startup")
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return StartResult{}, ctx.Err()
	}

	if err := cmd.Process.Release(); err != nil {
		return StartResult{}, err
	}

	status, err = Status(ctx, options.Paths)
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{Status: status, PreservedSessions: preservedSessions}, nil
}

func Run(ctx context.Context, options RuntimeOptions, readyWriter io.Writer) error {
	if err := EnsureRuntimeDirs(options.Paths); err != nil {
		return err
	}
	if strings.TrimSpace(options.AuthToken) == "" {
		return errors.New("missing auth token")
	}

	identity, err := readOrCreateDeviceIdentityFn(options.Paths)
	if err != nil {
		return err
	}
	connectivityIdentity, err := readOrCreateConnectivityIdentityFn(options.Paths)
	if err != nil {
		return err
	}
	if _, err := LoadPairingState(options.Paths); err != nil {
		return err
	}

	metadata := collectDeviceMetadataFn()
	launchHealth := LaunchHealthHealthy
	lastFailure := ""
	if err := EnsureTmuxAvailable(); err != nil {
		if !errors.Is(err, ErrTmuxNotFound) {
			return err
		}
		launchHealth = LaunchHealthDegraded
		lastFailure = "tmux_not_found"
	}
	state := &runtimeState{
		status: StatusInfo{
			Running:                true,
			PID:                    os.Getpid(),
			StartedAt:              time.Now().UTC().Unix(),
			BaseURL:                options.BaseURL,
			AuthContextFingerprint: AuthContextFingerprint(options.AuthToken),
			DeviceID:               identity.DeviceID,
			DaemonFingerprint:      connectivityIdentity.Fingerprint,
			DisplayName:            metadata.DisplayName,
			Hostname:               metadata.Hostname,
			PlatformFamily:         metadata.PlatformFamily,
			PlatformID:             metadata.PlatformID,
			RelayConnected:         false,
			LaunchHealth:           launchHealth,
			WorkspaceBackend:       workspaceBackendTmux,
			LastFailure:            lastFailure,
		},
		stopCh:              make(chan struct{}),
		paths:               options.Paths,
		broker:              NewBroker(),
		connectivityEvents:  make(chan protocol.ConnectivityFrame, 16),
		connectivityReplies: make(map[string]chan protocol.ConnectivityFrame),
	}
	if err := state.persist(); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(options.Paths.SocketPath)
		_ = os.Remove(options.Paths.BrokerSocketPath)
		_ = os.Remove(options.Paths.PIDFile)
		state.markStopped()
	}()
	if err := os.WriteFile(options.Paths.PIDFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		return err
	}

	server, err := NewServer(options.Paths.SocketPath, func(requestCtx context.Context, request Request) Response {
		switch request.Action {
		case actionStatus:
			status := state.snapshot()
			return Response{Status: &status}
		case actionDoctor:
			report := BuildDoctorReport(requestCtx, options.Paths, state.snapshot())
			return Response{Doctor: &report}
		case actionPair:
			correlationID, err := newOpaqueID("corr", 12)
			if err != nil {
				return Response{Error: err.Error()}
			}
			reserveCtx, cancel := context.WithTimeout(requestCtx, 5*time.Second)
			accountID, err := state.reservePairing(reserveCtx, correlationID)
			cancel()
			if err != nil {
				return Response{Error: err.Error()}
			}
			invitation, err := CreatePairInvitation(options.Paths, PairInvitationOptions{
				BaseURL:        options.BaseURL,
				DeviceID:       identity.DeviceID,
				DisplayName:    metadata.DisplayName,
				AccountID:      accountID,
				CorrelationID:  correlationID,
				DaemonIdentity: connectivityIdentity,
			})
			if err != nil {
				return Response{Error: err.Error()}
			}
			return Response{PairInvitation: &invitation}
		case actionPendingPairing:
			pending, err := ListPendingPairingResponses(options.Paths)
			if err != nil {
				return Response{Error: err.Error()}
			}
			return Response{PendingPairing: pending}
		case actionConfirmPendingPairing:
			completion, err := ConfirmPendingPairingResponse(options.Paths, request.InvitationID, request.SAS, time.Now().UTC())
			if err != nil {
				return Response{Error: err.Error()}
			}
			state.sendConnectivityEvent(protocol.ConnectivityPairCompletedFrame(completion.Device.Fingerprint))
			return Response{PairCompletion: &completion}
		case actionDevices:
			devices, err := ListTrustedAndroidDevices(options.Paths)
			if err != nil {
				return Response{Error: err.Error()}
			}
			return Response{TrustedDevices: devices}
		case actionBrokerSessions:
			return Response{BrokerSessions: state.broker.Snapshot()}
		case actionRevokeDevice:
			revoked, err := RevokeTrustedAndroidDevice(options.Paths, request.DeviceFingerprint)
			if err != nil {
				return Response{Error: err.Error()}
			}
			state.sendConnectivityEvent(protocol.ConnectivityFrame{
				Type:               "paired_device_revoked",
				AndroidFingerprint: revoked.Fingerprint,
			})
			return Response{TrustedDevice: &revoked}
		case actionStop:
			state.stop()
			state.markStopped()
			status := state.snapshot()
			return Response{Status: &status}
		default:
			return Response{Error: "unsupported action"}
		}
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = server.Close()
	}()
	brokerServer, err := NewBrokerServer(options.Paths.BrokerSocketPath, state.broker)
	if err != nil {
		return err
	}
	defer func() {
		_ = brokerServer.Close()
	}()

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Serve(ctx)
	}()
	brokerErrCh := make(chan error, 1)
	go func() {
		brokerErrCh <- brokerServer.Serve(ctx)
	}()
	go newDeviceConnector(options.BaseURL, options.AuthToken, state).Run(ctx, newLaunchHandler(options.BaseURL, options.AuthToken, options.Paths, state))
	go newConnectivityConnector(options.BaseURL, options.AuthToken, options.Paths, state).Run(ctx)

	if readyWriter != nil {
		if _, err := io.WriteString(readyWriter, "ready\n"); err != nil {
			return err
		}
		if closer, ok := readyWriter.(io.Closer); ok {
			_ = closer.Close()
		}
	}

	select {
	case <-ctx.Done():
		return nil
	case <-state.stopCh:
		return nil
	case err := <-serverErrCh:
		return err
	case err := <-brokerErrCh:
		return err
	}
}

func LoadStatus(paths Paths) (StatusInfo, error) {
	payload, err := os.ReadFile(paths.StatusFile)
	if err != nil {
		return StatusInfo{}, err
	}
	var status StatusInfo
	if err := json.Unmarshal(payload, &status); err != nil {
		return StatusInfo{}, err
	}
	return status, nil
}

func (s *runtimeState) snapshot() StatusInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *runtimeState) stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
}

func (s *runtimeState) markStopped() {
	s.mu.Lock()
	s.status.Running = false
	s.status.RelayConnected = false
	s.mu.Unlock()
	_ = s.persist()
}

func (s *runtimeState) persist() error {
	s.mu.RLock()
	status := s.status
	s.mu.RUnlock()
	return writeJSONFile(s.paths.StatusFile, status)
}

func (s *runtimeState) setRelayConnected(connected bool) {
	s.mu.Lock()
	s.status.RelayConnected = connected
	s.mu.Unlock()
	_ = s.persist()
}

func (s *runtimeState) setLastFailure(reason string, degrade bool) {
	s.mu.Lock()
	s.status.LastFailure = reason
	if degrade {
		s.status.LaunchHealth = LaunchHealthDegraded
	}
	s.mu.Unlock()
	_ = s.persist()
}

func (s *runtimeState) clearLastFailure() {
	s.mu.Lock()
	s.status.LastFailure = ""
	s.status.LaunchHealth = LaunchHealthHealthy
	s.mu.Unlock()
	_ = s.persist()
}

func (s *runtimeState) sendConnectivityEvent(frame protocol.ConnectivityFrame) bool {
	if s == nil || s.connectivityEvents == nil {
		return false
	}
	select {
	case s.connectivityEvents <- frame:
		return true
	default:
		return false
	}
}

func (s *runtimeState) reservePairing(ctx context.Context, correlationID string) (string, error) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return "", errors.New("missing pairing correlation id")
	}
	replyCh := s.registerConnectivityReply(correlationID)
	defer s.unregisterConnectivityReply(correlationID)
	if !s.sendConnectivityEvent(protocol.ConnectivityFrame{
		Type:      "pair_invitation_reserve",
		RequestID: correlationID,
	}) {
		return "", errors.New("relay pairing reservation unavailable")
	}
	select {
	case <-ctx.Done():
		return "", errors.New("relay pairing reservation timed out")
	case frame := <-replyCh:
		if frame.Type == "pair_invitation_reserved" && strings.TrimSpace(frame.AccountID) != "" {
			return strings.TrimSpace(frame.AccountID), nil
		}
		if frame.Type == "error" && frame.Reason != "" {
			return "", fmt.Errorf("relay pairing reservation failed: %s", frame.Reason)
		}
		return "", errors.New("relay pairing reservation failed")
	}
}

func (s *runtimeState) registerConnectivityReply(requestID string) chan protocol.ConnectivityFrame {
	ch := make(chan protocol.ConnectivityFrame, 1)
	s.mu.Lock()
	if s.connectivityReplies == nil {
		s.connectivityReplies = make(map[string]chan protocol.ConnectivityFrame)
	}
	s.connectivityReplies[requestID] = ch
	s.mu.Unlock()
	return ch
}

func (s *runtimeState) unregisterConnectivityReply(requestID string) {
	s.mu.Lock()
	delete(s.connectivityReplies, requestID)
	s.mu.Unlock()
}

func (s *runtimeState) deliverConnectivityReply(frame protocol.ConnectivityFrame) bool {
	if frame.RequestID == "" {
		return false
	}
	s.mu.RLock()
	ch := s.connectivityReplies[frame.RequestID]
	s.mu.RUnlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- frame:
	default:
	}
	return true
}

func cleanupStaleRuntime(paths Paths) error {
	if status, err := LoadStatus(paths); err == nil {
		if status.PID > 0 && processRunning(status.PID) {
			return nil
		}
	}
	for _, path := range []string{paths.SocketPath, paths.BrokerSocketPath, paths.PIDFile} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func acquireStartupLock(ctx context.Context, paths Paths) (func(), error) {
	lockPath := filepath.Join(paths.RuntimeDir, defaultStartupLockFileName)
	deadline := time.NewTimer(daemonStartupLockTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		release, err := tryAcquireStartupLock(lockPath)
		if err == nil {
			return release, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		cleanupStaleStartupLock(lockPath)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("timed out waiting for daemon startup lock")
		case <-ticker.C:
		}
	}
}

func tryAcquireStartupLock(lockPath string) (func(), error) {
	tmpFile, err := os.CreateTemp(filepath.Dir(lockPath), ".startup.lock.*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := fmt.Fprintf(tmpFile, "%d\n", os.Getpid()); err != nil {
		_ = tmpFile.Close()
		return nil, err
	}
	if err := tmpFile.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(tmpPath, lockPath); err != nil {
		return nil, err
	}
	cleanupTmp = false
	_ = os.Remove(tmpPath)

	return func() {
		_ = os.Remove(lockPath)
	}, nil
}

func cleanupStaleStartupLock(lockPath string) {
	payload, err := os.ReadFile(lockPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(payload)))
	if err != nil || pid <= 0 || !processRunning(pid) {
		_ = os.Remove(lockPath)
	}
}

func currentRunningStatus(ctx context.Context, paths Paths) (StatusInfo, error) {
	status, err := Status(ctx, paths)
	if err == nil && status.Running {
		return status, nil
	}

	persisted, loadErr := LoadStatus(paths)
	if loadErr != nil {
		if err != nil {
			return StatusInfo{}, err
		}
		return StatusInfo{}, loadErr
	}
	if persisted.PID > 0 && processRunning(persisted.PID) {
		persisted.Running = true
		return persisted, nil
	}
	if err != nil {
		return StatusInfo{}, err
	}
	return persisted, nil
}

func writeJSONFile(path string, value any) error {
	return writeJSONFileMode(path, value, 0o644)
}

func writePrivateJSONFile(path string, value any) error {
	return writeJSONFileMode(path, value, 0o600)
}

func writeJSONFileMode(path string, value any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	tmpFile, err := os.CreateTemp(filepath.Dir(path), "tmp-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := tmpFile.Write(payload); err != nil {
		cleanup()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func getenv(key string) string {
	return os.Getenv(key)
}

func runtimeGOOS() string {
	if override := strings.TrimSpace(os.Getenv("GOOS_OVERRIDE_FOR_TESTS")); override != "" {
		return override
	}
	return runtime.GOOS
}
