package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/tunnel/connector"
	"yuanbohan/tunnel/internal/tunnel/launcher"
	"yuanbohan/tunnel/internal/tunnel/session"
)

type fakeRelayConnector struct {
	waitConnected bool
	state         connector.State
	runCalledCh   chan struct{}
	boundHub      *session.Hub
	initialCols   int
	initialRows   int
	connectTTL    time.Duration
	stateCh       chan connector.State
}

func (f *fakeRelayConnector) SetInitialSize(cols, rows int) {
	f.initialCols = cols
	f.initialRows = rows
}

func (f *fakeRelayConnector) SetInitialConnectTimeout(timeout time.Duration) {
	f.connectTTL = timeout
}

func (f *fakeRelayConnector) BindHub(hub *session.Hub) {
	f.boundHub = hub
}

func (f *fakeRelayConnector) Run(context.Context) {
	if f.runCalledCh != nil {
		select {
		case <-f.runCalledCh:
		default:
			close(f.runCalledCh)
		}
	}
}

func (f *fakeRelayConnector) WaitUntilConnected(context.Context, time.Duration) bool {
	return f.waitConnected
}

func (f *fakeRelayConnector) SubscribeStateChanges() (<-chan connector.State, func()) {
	if f.stateCh == nil {
		f.stateCh = make(chan connector.State, 1)
	}
	return f.stateCh, func() {}
}

func (f *fakeRelayConnector) CurrentState() connector.State {
	return f.state
}

func (f *fakeRelayConnector) WriteOutput([]byte) error {
	return nil
}

func setTestEnv(t *testing.T) {
	t.Helper()
	for _, kv := range [][2]string{
		{"TUNNEL_BASE_URL", "http://127.0.0.1:8586"},
		{"TUNNEL_AUTH_TOKEN", "test-token"},
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

func assertHelpText(t *testing.T, text string) {
	t.Helper()
	for _, fragment := range []string{
		"Usage:\n  tunnel [--label label] [--base-url url] <command> [args...]",
		"-h, --help",
		"--version",
		"--label",
		"--base-url",
		"TUNNEL_BASE_URL",
		"TUNNEL_AUTH_TOKEN",
		defaultTunnelBaseURL,
		"tunnel claude",
		"tunnel --label api-fix codex --profile prod",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("help text = %q, want fragment %q", text, fragment)
		}
	}
	const wantEnvBlock = "Environment:\n  TUNNEL_AUTH_TOKEN  Required agent token for normal execution\n  TUNNEL_BASE_URL    Optional relay base URL (default: https://diaro.me)"
	if !strings.Contains(text, wantEnvBlock) {
		t.Fatalf("help text = %q, want aligned environment block %q", text, wantEnvBlock)
	}
}

func TestRunWithArgsStopsBeforeStartingSessionWhenLocalTerminalPreparationFails(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
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

	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel", "codex", "--profile", "prod"}, &stdout, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if startedSession {
		t.Fatal("runWithArgs started the child session before local terminal preparation succeeded")
	}
}

func TestRunWithArgsPrintsVersionWithoutStartingSession(t *testing.T) {
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

	resolveCalled := false
	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		resolveCalled = true
		return launcher.Command{}, nil
	}

	prepareCalled := false
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		prepareCalled = true
		return &session.LocalTerminal{}, nil
	}

	startCalled := false
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		startCalled = true
		return nil, nil
	}

	connectorCalled := false
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		connectorCalled = true
		return &fakeRelayConnector{}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}

	if got := stdout.String(); got != "tunnel v0.1.0-dev\n" {
		t.Fatalf("stdout = %q, want tunnel v0.1.0-dev", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if resolveCalled || prepareCalled || startCalled || connectorCalled {
		t.Fatalf("version fast path unexpectedly touched runtime: resolve=%v prepare=%v start=%v connector=%v", resolveCalled, prepareCalled, startCalled, connectorCalled)
	}
}

func TestRunWithArgsPrintsHelpWithoutStartingSession(t *testing.T) {
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

	resolveCalled := false
	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		resolveCalled = true
		return launcher.Command{}, nil
	}

	prepareCalled := false
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		prepareCalled = true
		return &session.LocalTerminal{}, nil
	}

	startCalled := false
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		startCalled = true
		return nil, nil
	}

	connectorCalled := false
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		connectorCalled = true
		return &fakeRelayConnector{}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}

	assertHelpText(t, stdout.String())
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if resolveCalled || prepareCalled || startCalled || connectorCalled {
		t.Fatalf("help fast path unexpectedly touched runtime: resolve=%v prepare=%v start=%v connector=%v", resolveCalled, prepareCalled, startCalled, connectorCalled)
	}
}

func TestRunWithArgsPrintsShortHelpWithoutStartingSession(t *testing.T) {
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

	resolveCalled := false
	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		resolveCalled = true
		return launcher.Command{}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "-h"}, &stdout, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}

	assertHelpText(t, stdout.String())
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if resolveCalled {
		t.Fatal("help fast path unexpectedly resolved launcher")
	}
}

func TestRunWithArgsPrintsUsageHelpForMissingLauncher(t *testing.T) {
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

	resolveCalled := false
	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		resolveCalled = true
		return launcher.Command{}, nil
	}

	prepareCalled := false
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		prepareCalled = true
		return &session.LocalTerminal{}, nil
	}

	startCalled := false
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		startCalled = true
		return nil, nil
	}

	connectorCalled := false
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		connectorCalled = true
		return &fakeRelayConnector{}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("runWithArgs error = nil, want usage error")
	}
	var usageErr usageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("error = %#v, want usageError", err)
	}

	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	assertHelpText(t, stderr.String())
	if strings.Contains(stderr.String(), "missing launcher command") {
		t.Fatalf("stderr = %q, want clean help text without extra error line", stderr.String())
	}
	if resolveCalled || prepareCalled || startCalled || connectorCalled {
		t.Fatalf("missing-launcher path unexpectedly touched runtime: resolve=%v prepare=%v start=%v connector=%v", resolveCalled, prepareCalled, startCalled, connectorCalled)
	}
}

func TestRunWithArgsAddsRelayConnectorToInitialSinks(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
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
	fakeConnector := &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	fakeConnector.runCalledCh = make(chan struct{})
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
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

	waitForExit = func(context.Context, <-chan struct{}, <-chan error) error {
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel", "--label", "api-fix", "codex", "--profile", "prod"}, &stdout, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if gotURL != "ws://127.0.0.1:8586" {
		t.Fatalf("connector URL = %q, want ws://127.0.0.1:8586", gotURL)
	}
	if gotToken != "test-token" {
		t.Fatalf("connector token = %q, want test-token", gotToken)
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
	if gotInfo.StartedAt <= 0 {
		t.Fatal("StartedAt = 0, want current Unix timestamp")
	}
	select {
	case <-fakeConnector.runCalledCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("connector Run was not called")
	}
	if fakeConnector.connectTTL != startupRelayWait {
		t.Fatalf("initial connect timeout = %v, want %v", fakeConnector.connectTTL, startupRelayWait)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want no startup banner when session start fails", got)
	}
}

func TestRunWithArgsUsesUserProvidedCommandForPreview(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/private/tmp/custom-agent", Args: append([]string(nil), args...)}, nil
	}

	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}

	var gotInfo protocol.SessionInfo
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		gotInfo = info
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}

	wantErr := errors.New("start session failed")
	startSession = func(_ context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		return nil, wantErr
	}

	waitForExit = func(context.Context, <-chan struct{}, <-chan error) error {
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel", "/opt/bin/custom-agent", "--mode", "fast"}, &stdout, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if gotInfo.Launcher != "/opt/bin/custom-agent" {
		t.Fatalf("Launcher = %q, want /opt/bin/custom-agent", gotInfo.Launcher)
	}
	if gotInfo.CommandPreview != "/opt/bin/custom-agent --mode fast" {
		t.Fatalf("CommandPreview = %q, want /opt/bin/custom-agent --mode fast", gotInfo.CommandPreview)
	}
}

func TestStartupBannerUsesRelayState(t *testing.T) {
	if got := startupBanner("codex", "sess-123", "127.0.0.1:8586", connector.StateConnected); got != "\r\x1b[2K\x1b[92m▶ tunnel codex — session sess-123; relay connected (127.0.0.1:8586)\x1b[0m\r" {
		t.Fatalf("connected banner = %q", got)
	}
	if got := startupBanner("codex", "sess-456", "127.0.0.1:8586", connector.StateReconnecting); got != "\r\x1b[2K\x1b[31m▶ tunnel codex — session sess-456; relay reconnecting (127.0.0.1:8586)\x1b[0m\r" {
		t.Fatalf("reconnecting banner = %q", got)
	}
}

func TestRunWithArgsPrintsStartupBannerOnExit(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/bin/sh", Args: []string{"-c", "exit 0"}}, nil
	}

	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}

	startSession = func(ctx context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		return session.StartCommandWithInitialSinks(ctx, path, args, sinks)
	}

	done := make(chan struct{})
	startLocalTerminal = func(context.Context, *session.LocalTerminal, *session.Hub) <-chan struct{} {
		close(done)
		return done
	}

	var sessionID string
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		sessionID = info.SessionID
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "codex"}, &stdout, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}

	if got := stderr.String(); got != startupBanner("codex", sessionID, "http://127.0.0.1:8586", connector.StateConnected) {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunWithArgsPrintsReconnectingBannerAfterStartupWait(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/bin/sh", Args: []string{"-c", "exit 0"}}, nil
	}

	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}

	startSession = func(ctx context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		return session.StartCommandWithInitialSinks(ctx, path, args, sinks)
	}

	done := make(chan struct{})
	startLocalTerminal = func(context.Context, *session.LocalTerminal, *session.Hub) <-chan struct{} {
		close(done)
		return done
	}

	var sessionID string
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		sessionID = info.SessionID
		return &fakeRelayConnector{waitConnected: false, state: connector.StateReconnecting}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "codex"}, &stdout, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}

	if got := stderr.String(); got != startupBanner("codex", sessionID, "http://127.0.0.1:8586", connector.StateReconnecting) {
		t.Fatalf("stderr = %q", got)
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
