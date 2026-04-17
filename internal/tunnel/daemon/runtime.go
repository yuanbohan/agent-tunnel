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
	"strings"
	"sync"
	"time"
)

const readySignalEnv = "TUNNEL_DAEMON_READY_FD"
const daemonAuthTokenEnv = "TUNNEL_AUTH_TOKEN"

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
	AlreadyRunning bool
	Status         StatusInfo
}

type runtimeState struct {
	mu       sync.RWMutex
	status   StatusInfo
	stopCh   chan struct{}
	stopOnce sync.Once
	paths    Paths
}

func StartBackground(ctx context.Context, options StartOptions) (StartResult, error) {
	status, err := currentRunningStatus(ctx, options.Paths)
	if err == nil && status.Running {
		return StartResult{AlreadyRunning: true, Status: status}, nil
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
	return StartResult{Status: status}, nil
}

func Run(ctx context.Context, options RuntimeOptions, readyWriter io.Writer) error {
	if err := EnsureRuntimeDirs(options.Paths); err != nil {
		return err
	}
	if strings.TrimSpace(options.AuthToken) == "" {
		return errors.New("missing auth token")
	}
	if !hasDesktopSession() {
		return errors.New("desktop session unavailable")
	}

	identity, err := readOrCreateDeviceIdentityFn(options.Paths)
	if err != nil {
		return err
	}
	recipe, err := inferRecipeFn()
	if err != nil {
		return err
	}
	if err := PersistRecipe(options.Paths, recipe); err != nil {
		return err
	}

	metadata := collectDeviceMetadataFn()
	state := &runtimeState{
		status: StatusInfo{
			Running:          true,
			PID:              os.Getpid(),
			StartedAt:        time.Now().UTC().Unix(),
			BaseURL:          options.BaseURL,
			DeviceID:         identity.DeviceID,
			DisplayName:      metadata.DisplayName,
			Hostname:         metadata.Hostname,
			PlatformFamily:   metadata.PlatformFamily,
			PlatformID:       metadata.PlatformID,
			RelayConnected:   false,
			LaunchHealth:     LaunchHealthHealthy,
			LauncherStrategy: recipe.Strategy,
		},
		stopCh: make(chan struct{}),
		paths:  options.Paths,
	}
	if err := state.persist(); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(options.Paths.SocketPath)
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

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Serve(ctx)
	}()
	go newDeviceConnector(options.BaseURL, options.AuthToken, state, recipe).Run(ctx, newLaunchHandler(options.BaseURL, options.AuthToken, options.Paths, state, recipe))

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

func cleanupStaleRuntime(paths Paths) error {
	if status, err := LoadStatus(paths); err == nil {
		if status.PID > 0 && processRunning(status.PID) {
			return nil
		}
	}
	for _, path := range []string{paths.SocketPath, paths.PIDFile} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
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
