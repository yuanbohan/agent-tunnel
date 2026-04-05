package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"yuanbohan/tunnel/connector"
	"yuanbohan/tunnel/launcher"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/session"
)

func setTestEnv(t *testing.T) {
	t.Helper()
	for _, kv := range [][2]string{
		{"AGENTUNNEL_RELAY_ADDR", "127.0.0.1:8586"},
		{"AGENTUNNEL_RELAY_TOKEN", "test-token"},
	} {
		old, existed := os.LookupEnv(kv[0])
		os.Setenv(kv[0], kv[1])
		t.Cleanup(func() {
			if existed {
				os.Setenv(kv[0], old)
			} else {
				os.Unsetenv(kv[0])
			}
		})
	}
}

func TestRunWithArgsStopsBeforeStartingSessionWhenLocalTerminalPreparationFails(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartCodexRuntime := startCodexRuntime
	oldStartCodexStateMonitor := startCodexStateMonitor
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startCodexRuntime = oldStartCodexRuntime
		startCodexStateMonitor = oldStartCodexStateMonitor
		newConnector = oldNewConnector
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/bin/echo", Args: append([]string(nil), args...)}, nil
	}

	wantErr := errors.New("inappropriate ioctl for device")
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return nil, wantErr
	}

	startedSession := false
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		startedSession = true
		return nil, nil
	}

	newConnector = func(url, token string, info protocol.SessionInfo) *connector.Connector {
		return connector.New(url, token, info)
	}

	var stderr bytes.Buffer
	err := runWithArgs([]string{"agentunnel", "codex", "--version"}, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if startedSession {
		t.Fatal("runWithArgs started the child session before local terminal preparation succeeded")
	}
	if got := stderr.String(); got != "▶ agentunnel — codex\n  relay: 127.0.0.1:8586\n  local terminal is interactive\n\n" {
		t.Fatalf("stderr = %q, want startup banner before terminal preparation", got)
	}
}

func TestRunWithArgsPrintsStartupBannerBeforePreparingLocalTerminal(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartCodexRuntime := startCodexRuntime
	oldStartCodexStateMonitor := startCodexStateMonitor
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startCodexRuntime = oldStartCodexRuntime
		startCodexStateMonitor = oldStartCodexStateMonitor
		newConnector = oldNewConnector
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}

	var stderr bytes.Buffer
	wantBanner := "▶ agentunnel — codex\n  relay: 127.0.0.1:8586\n  local terminal is interactive\n\n"
	wantErr := errors.New("inappropriate ioctl for device")
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		if got := stderr.String(); got != wantBanner {
			t.Fatalf("stderr at terminal preparation = %q, want %q", got, wantBanner)
		}
		return nil, wantErr
	}

	startedSession := false
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		startedSession = true
		return nil, nil
	}

	newConnector = func(url, token string, info protocol.SessionInfo) *connector.Connector {
		return connector.New(url, token, info)
	}

	err := runWithArgs([]string{"agentunnel", "codex"}, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if startedSession {
		t.Fatal("runWithArgs started the child session before local terminal preparation succeeded")
	}
}

func TestRunWithArgsAddsRelayConnectorToInitialSinks(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartCodexRuntime := startCodexRuntime
	oldStartCodexStateMonitor := startCodexStateMonitor
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startCodexRuntime = oldStartCodexRuntime
		startCodexStateMonitor = oldStartCodexStateMonitor
		newConnector = oldNewConnector
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}

	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}

	startCodexRuntime = func(context.Context, launcher.Command) (codexRuntime, error) {
		return fakeCodexRuntime{
			command: launcher.Command{
				Name: "codex",
				Path: "/usr/bin/codex",
				Args: []string{"--remote", "ws://127.0.0.1:51723", "--profile", "prod"},
			},
			appServerURL: "ws://127.0.0.1:51723",
		}, nil
	}
	startCodexStateMonitor = func(context.Context, string, *connector.Connector) {}

	var gotInfo protocol.SessionInfo
	var gotURL, gotToken string
	fakeConnector := connector.New("", "", protocol.SessionInfo{})
	newConnector = func(url, token string, info protocol.SessionInfo) *connector.Connector {
		gotURL = url
		gotToken = token
		gotInfo = info
		return fakeConnector
	}

	wantErr := errors.New("start session failed")
	var gotSinks map[string]session.OutputSink
	startSession = func(_ context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		gotSinks = sinks
		return nil, wantErr
	}

	var stderr bytes.Buffer
	err := runWithArgs([]string{"agentunnel", "--label", "api-fix", "codex", "--profile", "prod"}, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if gotURL != "ws://127.0.0.1:8586" {
		t.Fatalf("connector URL = %q, want ws://127.0.0.1:8586", gotURL)
	}
	if gotToken != "test-token" {
		t.Fatalf("connector Token = %q, want test-token", gotToken)
	}
	if gotSinks == nil {
		t.Fatal("startSession did not receive initial sinks")
	}
	if _, ok := gotSinks["relay"]; !ok {
		t.Fatalf("initial sinks = %#v, want relay sink", gotSinks)
	}
	if gotInfo.Launcher != "codex" {
		t.Fatalf("Launcher = %q, want codex", gotInfo.Launcher)
	}
	if gotInfo.Label != "api-fix" {
		t.Fatalf("Label = %q, want api-fix", gotInfo.Label)
	}
	if gotInfo.CommandPreview != "codex --profile prod" {
		t.Fatalf("CommandPreview = %q, want codex --profile prod", gotInfo.CommandPreview)
	}
	if gotInfo.CWD == "" {
		t.Fatal("CWD = empty, want current working directory")
	}
	if gotInfo.SessionID == "" {
		t.Fatal("SessionID = empty, want generated session id")
	}
	if gotInfo.StartedAt.IsZero() {
		t.Fatal("StartedAt = zero, want current time")
	}
	if got := stderr.String(); got != "▶ agentunnel — codex\n  relay: 127.0.0.1:8586\n  local terminal is interactive\n\n" {
		t.Fatalf("stderr = %q, want startup banner before session start", got)
	}
}

func TestRunWithArgsReturnsCodexRuntimeErrorBeforeStartingSession(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartCodexRuntime := startCodexRuntime
	oldStartCodexStateMonitor := startCodexStateMonitor
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startCodexRuntime = oldStartCodexRuntime
		startCodexStateMonitor = oldStartCodexStateMonitor
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}

	wantErr := errors.New("app-server failed")
	startCodexRuntime = func(context.Context, launcher.Command) (codexRuntime, error) {
		return nil, wantErr
	}

	startedSession := false
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		startedSession = true
		return nil, nil
	}

	err := runWithArgs([]string{"agentunnel", "codex"}, &bytes.Buffer{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if startedSession {
		t.Fatal("runWithArgs started the child session after codex runtime failed")
	}
}

func TestWaitForProcessOrShutdownIgnoresLocalTerminalCompletion(t *testing.T) {
	localDone := make(chan struct{})
	close(localDone)

	waitErr := make(chan error, 1)
	result := make(chan error, 1)

	go func() {
		result <- waitForProcessOrShutdown(context.Background(), localDone, waitErr)
	}()

	select {
	case err := <-result:
		t.Fatalf("returned before process exit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	want := errors.New("child exited")
	waitErr <- want

	select {
	case err := <-result:
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not return after process exit")
	}
}

func TestWaitForProcessOrShutdownReturnsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	localDone := make(chan struct{})
	waitErr := make(chan error)

	result := make(chan error, 1)
	go func() {
		result <- waitForProcessOrShutdown(ctx, localDone, waitErr)
	}()

	cancel()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not return after context cancellation")
	}
}

func TestWaitForSessionOrShutdownReturnsOnSidecarExit(t *testing.T) {
	sidecarWait := make(chan error, 1)
	want := errors.New("app-server exited")

	result := make(chan error, 1)
	go func() {
		result <- waitForSessionOrShutdown(context.Background(), make(chan struct{}), make(chan error), sidecarWait)
	}()

	sidecarWait <- want

	select {
	case err := <-result:
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not return after sidecar exit")
	}
}

type fakeCodexRuntime struct {
	command      launcher.Command
	appServerURL string
	waitErr      error
	closeErr     error
}

func (f fakeCodexRuntime) RemoteCommand() launcher.Command { return f.command }
func (f fakeCodexRuntime) AppServerURL() string            { return f.appServerURL }
func (f fakeCodexRuntime) Wait() error                     { return f.waitErr }
func (f fakeCodexRuntime) Close() error                    { return f.closeErr }
