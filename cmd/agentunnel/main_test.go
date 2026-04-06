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
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
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
}

func TestRunWithArgsAddsRelayConnectorToInitialSinks(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		newConnector = oldNewConnector
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}

	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}

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
	var gotPath string
	var gotArgs []string
	startSession = func(_ context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		gotPath = path
		gotArgs = append([]string(nil), args...)
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
	if gotPath != "/usr/bin/codex" {
		t.Fatalf("path = %q, want /usr/bin/codex", gotPath)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "--profile" || gotArgs[1] != "prod" {
		t.Fatalf("args = %#v, want untouched launcher args", gotArgs)
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
