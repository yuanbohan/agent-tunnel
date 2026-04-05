package codexapp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"yuanbohan/tunnel/launcher"
)

const (
	appServerListenAddr     = "ws://127.0.0.1:0"
	startupTimeout          = 10 * time.Second
	readyPollInterval       = 100 * time.Millisecond
	readyRequestTimeout     = 500 * time.Millisecond
	shutdownGracePeriod     = 3 * time.Second
	listeningOnPrefix       = "listening on:"
	readyzPrefix            = "readyz:"
	unexpectedExitErrorText = "codex app-server exited unexpectedly"
)

var execCommandContext = exec.CommandContext

// Runtime owns the local Codex app-server sidecar. It is responsible for:
// - starting `codex app-server`
// - discovering the loopback websocket and readyz endpoints it prints
// - exposing the rewritten `codex --remote <ws-url>` command for the PTY child
// - shutting down and reaping the sidecar process
type Runtime struct {
	// cmd is the managed `codex app-server` process, not the PTY-attached
	// `codex --remote` process that the operator interacts with.
	cmd *exec.Cmd
	// appServerURL is used by the action-required monitor to query Codex thread
	// state over websocket after startup succeeds.
	appServerURL string
	// remoteCommand is what `cmd/agentunnel` should actually run under the PTY
	// once the app-server is ready.
	remoteCommand launcher.Command

	waitOnce sync.Once
	waitDone chan struct{}
	waitErr  error

	closeOnce sync.Once

	mu             sync.Mutex
	closeInitiated bool
}

type appServerEndpoints struct {
	wsURL    string
	readyURL string
}

// Start boots the Codex sidecar and blocks until it is safe to launch the PTY
// child against it. The returned Runtime is later wired into:
// - cmd/agentunnel, which runs RemoteCommand() under the PTY
// - codexapp.MonitorActionRequired, which consumes AppServerURL()
func Start(ctx context.Context, command launcher.Command) (*Runtime, error) {
	if command.Name != "codex" {
		return nil, fmt.Errorf("codexapp runtime only supports codex launcher, got %q", command.Name)
	}

	// The Codex app-server announces its dynamically chosen websocket and readyz
	// endpoints on stdout/stderr. We capture that stream through a pipe so startup
	// can discover the addresses before the PTY child is launched.
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	// Listen on loopback only. Port 0 lets Codex pick a free local port.
	cmd := execCommandContext(ctx, command.Path, "app-server", "--listen", appServerListenAddr)
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, err
	}
	_ = writer.Close()

	runtime := &Runtime{
		cmd:      cmd,
		waitDone: make(chan struct{}),
	}
	go runtime.waitForProcess()

	endpointsCh := make(chan appServerEndpoints, 1)
	// Endpoint discovery runs concurrently with process supervision because the
	// app-server may exit before it prints both endpoint lines.
	go discoverEndpoints(reader, endpointsCh)

	endpoints, err := waitForEndpoints(ctx, runtime, endpointsCh)
	if err != nil {
		_ = runtime.Close()
		return nil, err
	}
	if endpoints.readyURL == "" {
		return nil, fmt.Errorf("codex app-server did not report readyz URL")
	}
	if err := waitForReady(ctx, runtime, endpoints.readyURL); err != nil {
		_ = runtime.Close()
		return nil, err
	}

	runtime.remoteCommand = launcher.Command{
		Name: command.Name,
		Path: command.Path,
		// The original launcher args are preserved. We only insert the remote
		// websocket that tells the PTY child to attach to the local app-server.
		Args: append([]string{"--remote", endpoints.wsURL}, command.Args...),
	}
	runtime.appServerURL = endpoints.wsURL
	return runtime, nil
}

func (r *Runtime) RemoteCommand() launcher.Command {
	return launcher.Command{
		Name: r.remoteCommand.Name,
		Path: r.remoteCommand.Path,
		Args: append([]string(nil), r.remoteCommand.Args...),
	}
}

func (r *Runtime) AppServerURL() string {
	return r.appServerURL
}

// Wait reaps the sidecar process. The normal shutdown path intentionally sends
// a signal first, so signaled exits are treated as success after Close().
func (r *Runtime) Wait() error {
	r.waitOnce.Do(func() {
		r.waitErr = r.cmd.Wait()
		close(r.waitDone)
	})
	<-r.waitDone

	if r.closeWasInitiated() && isExpectedShutdownError(r.waitErr) {
		return nil
	}
	return r.waitErr
}

// Close terminates the sidecar process that backs the Codex remote session.
// This is called from cmd/agentunnel cleanup when either:
// - the PTY child exits, or
// - agentunnel itself is shutting down.
func (r *Runtime) Close() error {
	r.closeOnce.Do(func() {
		r.markCloseInitiated()
		if r.cmd.Process == nil {
			return
		}

		_ = r.cmd.Process.Signal(syscall.SIGTERM)

		done := make(chan struct{})
		go func() {
			_ = r.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(shutdownGracePeriod):
			_ = r.cmd.Process.Kill()
		}
	})
	return r.Wait()
}

// discoverEndpoints parses the app-server log stream until both of the startup
// endpoints have been observed. Those values are later used for two different
// downstream consumers:
// - wsURL -> RemoteCommand() and MonitorActionRequired()
// - readyURL -> waitForReady()
func discoverEndpoints(reader *os.File, endpointsCh chan<- appServerEndpoints) {
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)

	var endpoints appServerEndpoints
	sent := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.Contains(line, listeningOnPrefix):
			endpoints.wsURL = strings.TrimSpace(strings.TrimPrefix(line, listeningOnPrefix))
		case strings.Contains(line, readyzPrefix):
			endpoints.readyURL = strings.TrimSpace(strings.TrimPrefix(line, readyzPrefix))
		}

		if !sent && endpoints.wsURL != "" && endpoints.readyURL != "" {
			endpointsCh <- endpoints
			sent = true
		}
	}

	if !sent && endpoints.wsURL != "" && endpoints.readyURL != "" {
		endpointsCh <- endpoints
	}
	close(endpointsCh)
}

// waitForEndpoints ties together three failure domains during startup:
// - context cancellation
// - timeout waiting for the endpoint announcements
// - early process exit from the sidecar itself
func waitForEndpoints(ctx context.Context, runtime *Runtime, endpointsCh <-chan appServerEndpoints) (appServerEndpoints, error) {
	timer := time.NewTimer(startupTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return appServerEndpoints{}, ctx.Err()
	case <-timer.C:
		return appServerEndpoints{}, fmt.Errorf("timed out waiting for codex app-server startup")
	case endpoints, ok := <-endpointsCh:
		if !ok || endpoints.wsURL == "" {
			err := runtime.Wait()
			if err == nil {
				err = errors.New(unexpectedExitErrorText)
			}
			return appServerEndpoints{}, err
		}
		return endpoints, nil
	case <-runtime.waitDone:
		err := runtime.Wait()
		if err == nil {
			err = errors.New(unexpectedExitErrorText)
		}
		return appServerEndpoints{}, err
	}
}

// waitForReady prevents the PTY child from connecting too early. Endpoint
// discovery only tells us where the sidecar intends to listen; readyz confirms
// that the websocket API is actually ready to serve `codex --remote` and the
// action-required monitor.
func waitForReady(ctx context.Context, runtime *Runtime, readyURL string) error {
	client := &http.Client{Timeout: readyRequestTimeout}
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	timer := time.NewTimer(startupTimeout)
	defer timer.Stop()

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("timed out waiting for codex app-server readiness")
		case <-runtime.waitDone:
			err := runtime.Wait()
			if err == nil {
				err = errors.New(unexpectedExitErrorText)
			}
			return err
		case <-ticker.C:
		}
	}
}

func (r *Runtime) markCloseInitiated() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeInitiated = true
}

func (r *Runtime) closeWasInitiated() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeInitiated
}

func (r *Runtime) waitForProcess() {
	r.waitOnce.Do(func() {
		r.waitErr = r.cmd.Wait()
		close(r.waitDone)
	})
}

// isExpectedShutdownError is shared with Close() so a process that exits because
// agentunnel intentionally signaled it is not reported as an application error.
func isExpectedShutdownError(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus)
	return ok && status.Signaled()
}
