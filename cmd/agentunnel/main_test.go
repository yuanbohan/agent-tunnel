package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"yuanbohan/tunnel/launcher"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/internal/relayclient"
	"yuanbohan/tunnel/internal/server"
	"yuanbohan/tunnel/session"
)

type fakeRelaySink struct{}

func (fakeRelaySink) WriteOutput([]byte) error { return nil }
func (fakeRelaySink) BindHub(*session.Hub)     {}
func (fakeRelaySink) Run(context.Context)      {}

func TestRunWithArgsStopsBeforeStartingSessionWhenLocalTerminalPreparationFails(t *testing.T) {
	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartServer := startServer
	oldLoadRelayConfig := loadRelayConfig
	oldNewRelayConnector := newRelayConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startServer = oldStartServer
		loadRelayConfig = oldLoadRelayConfig
		newRelayConnector = oldNewRelayConnector
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

	startedServer := false
	startServer = func(server.LiveSession) (*server.Running, error) {
		startedServer = true
		return nil, nil
	}

	var stderr bytes.Buffer
	err := runWithArgs([]string{"agentunnel", "codex", "--version"}, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if startedSession {
		t.Fatal("runWithArgs started the child session before local terminal preparation succeeded")
	}
	if startedServer {
		t.Fatal("runWithArgs started the web server before local terminal preparation succeeded")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want no startup banner on terminal preparation failure", got)
	}
}

func TestRunWithArgsAddsRelayConnectorToInitialSinksWhenEnabled(t *testing.T) {
	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartServer := startServer
	oldLoadRelayConfig := loadRelayConfig
	oldNewRelayConnector := newRelayConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startServer = oldStartServer
		loadRelayConfig = oldLoadRelayConfig
		newRelayConnector = oldNewRelayConnector
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}

	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}

	loadRelayConfig = func(func(string) string, string) (relayclient.Config, bool, error) {
		return relayclient.Config{URL: "wss://relay.example", Token: "token"}, true, nil
	}

	var gotInfo protocol.SessionInfo
	fakeConnector := fakeRelaySink{}
	newRelayConnector = func(cfg relayclient.Config, info protocol.SessionInfo) relaySink {
		gotInfo = info
		return fakeConnector
	}

	wantErr := errors.New("start session failed")
	var gotSinks map[string]session.OutputSink
	startSession = func(_ context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		gotSinks = sinks
		return nil, wantErr
	}

	startServer = func(server.LiveSession) (*server.Running, error) {
		t.Fatal("startServer should not be called when startSession fails")
		return nil, nil
	}

	var stderr bytes.Buffer
	err := runWithArgs([]string{"agentunnel", "--label", "api-fix", "codex", "--profile", "prod"}, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if gotSinks == nil {
		t.Fatal("startSession did not receive initial sinks")
	}
	if _, ok := gotSinks["relay"]; !ok {
		t.Fatalf("initial sinks = %#v, want relay sink", gotSinks)
	}
	if gotSinks["relay"] != fakeConnector {
		t.Fatalf("relay sink = %#v, want fake connector", gotSinks["relay"])
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
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want no startup banner on startSession failure", got)
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
