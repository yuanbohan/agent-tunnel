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

type fakeRelayConnector struct {
	waitConnected bool
	state         connector.State
	runCalled     bool
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
	f.runCalled = true
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
	oldStartLocalTerminal := startLocalTerminal
	oldStartSleepPrevention := startSleepPrevention
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		startSleepPrevention = oldStartSleepPrevention
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

	startedSleepPrevention := false
	startSleepPrevention = func(int) sleepPrevention {
		startedSleepPrevention = true
		return newSleepPrevention(sleepPreventionActive, nil)
	}

	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}

	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel", "codex", "--version"}, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if startedSession {
		t.Fatal("runWithArgs started the child session before local terminal preparation succeeded")
	}
	if startedSleepPrevention {
		t.Fatal("runWithArgs started sleep prevention before local terminal preparation succeeded")
	}
}

func TestRunWithArgsAddsRelayConnectorToInitialSinks(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldStartSleepPrevention := startSleepPrevention
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		startSleepPrevention = oldStartSleepPrevention
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
	startedSleepPrevention := false
	startSession = func(_ context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		gotPath = path
		gotArgs = append([]string(nil), args...)
		gotSinks = sinks
		return nil, wantErr
	}

	startSleepPrevention = func(int) sleepPrevention {
		startedSleepPrevention = true
		return newSleepPrevention(sleepPreventionActive, nil)
	}

	waitForExit = func(context.Context, <-chan struct{}, <-chan error) error {
		return nil
	}

	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel", "--label", "api-fix", "codex", "--profile", "prod"}, &stderr)
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
	if fakeConnector.runCalled != true {
		t.Fatal("connector Run was not called")
	}
	if fakeConnector.connectTTL != startupRelayWait {
		t.Fatalf("initial connect timeout = %v, want %v", fakeConnector.connectTTL, startupRelayWait)
	}
	if startedSleepPrevention {
		t.Fatal("sleep prevention should not start when session start fails")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want no startup banner when session start fails", got)
	}
}

func TestStartupBannerUsesRelayState(t *testing.T) {
	if got := startupBanner("codex", "sess-123", "127.0.0.1:8586", connector.StateConnected, sleepPreventionActive); got != "\x1b[32m▶ tunnel codex — session sess-123; relay connected (127.0.0.1:8586); sleep prevented\x1b[0m\n" {
		t.Fatalf("connected banner = %q", got)
	}
	if got := startupBanner("codex", "sess-456", "127.0.0.1:8586", connector.StateReconnecting, sleepPreventionFailed); got != "\x1b[31m▶ tunnel codex — session sess-456; relay reconnecting (127.0.0.1:8586); sleep prevention failed\x1b[0m\n" {
		t.Fatalf("reconnecting banner = %q", got)
	}
}

func TestRunWithArgsPrintsStartupBannerWithSleepStatusAndStopsSleepPreventionOnExit(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldStartSleepPrevention := startSleepPrevention
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		startSleepPrevention = oldStartSleepPrevention
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

	stopCalls := 0
	startSleepPrevention = func(pid int) sleepPrevention {
		if pid != os.Getpid() {
			t.Fatalf("pid = %d, want %d", pid, os.Getpid())
		}
		return newSleepPrevention(sleepPreventionActive, func() {
			stopCalls++
		})
	}

	var sessionID string
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		sessionID = info.SessionID
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}

	var stderr bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "codex"}, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}

	if got := stderr.String(); got != startupBanner("codex", sessionID, "127.0.0.1:8586", connector.StateConnected, sleepPreventionActive) {
		t.Fatalf("stderr = %q", got)
	}
	if stopCalls != 1 {
		t.Fatalf("sleep stop calls = %d, want 1", stopCalls)
	}
}

func TestRunWithArgsContinuesWhenSleepPreventionFails(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldStartSleepPrevention := startSleepPrevention
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		startSleepPrevention = oldStartSleepPrevention
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

	startSleepPrevention = func(int) sleepPrevention {
		return newSleepPrevention(sleepPreventionFailed, nil)
	}

	var sessionID string
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		sessionID = info.SessionID
		return &fakeRelayConnector{waitConnected: false, state: connector.StateReconnecting}
	}

	var stderr bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "codex"}, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}

	if got := stderr.String(); got != startupBanner("codex", sessionID, "127.0.0.1:8586", connector.StateReconnecting, sleepPreventionFailed) {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunWithArgsContinuesWhenSleepPreventionIsUnsupported(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldStartSleepPrevention := startSleepPrevention
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		startSleepPrevention = oldStartSleepPrevention
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

	startSleepPrevention = func(int) sleepPrevention {
		return newSleepPrevention(sleepPreventionUnsupported, nil)
	}

	var sessionID string
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		sessionID = info.SessionID
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}

	var stderr bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "codex"}, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}

	if got := stderr.String(); got != startupBanner("codex", sessionID, "127.0.0.1:8586", connector.StateConnected, sleepPreventionUnsupported) {
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
