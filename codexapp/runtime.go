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

type Runtime struct {
	cmd           *exec.Cmd
	appServerURL  string
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

func Start(ctx context.Context, command launcher.Command) (*Runtime, error) {
	if command.Name != "codex" {
		return nil, fmt.Errorf("codexapp runtime only supports codex launcher, got %q", command.Name)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}

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
