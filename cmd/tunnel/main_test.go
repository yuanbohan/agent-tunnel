package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/tunnel/connector"
	"yuanbohan/tunnel/internal/tunnel/daemon"
	"yuanbohan/tunnel/internal/tunnel/launcher"
	"yuanbohan/tunnel/internal/tunnel/session"
)

type fakeRelayConnector struct {
	waitConnected   bool
	state           connector.State
	runCalledCh     chan struct{}
	runCalledOnce   sync.Once
	boundHub        *session.Hub
	initialCols     int
	initialRows     int
	connectTTL      time.Duration
	launchRequestID string
	stateCh         chan connector.State
}

func stubDetectGitBranch(t *testing.T, branch string) {
	t.Helper()

	oldDetectGitBranch := detectGitBranch
	detectGitBranch = func(context.Context, string) string { return branch }
	t.Cleanup(func() {
		detectGitBranch = oldDetectGitBranch
	})
}

func (f *fakeRelayConnector) SetInitialSize(cols, rows int) {
	f.initialCols = cols
	f.initialRows = rows
}

func (f *fakeRelayConnector) SetInitialConnectTimeout(timeout time.Duration) {
	f.connectTTL = timeout
}

func (f *fakeRelayConnector) SetLaunchRequestID(launchRequestID string) {
	f.launchRequestID = launchRequestID
}

func (f *fakeRelayConnector) BindHub(hub *session.Hub) {
	f.boundHub = hub
}

func (f *fakeRelayConnector) Run(context.Context) {
	if f.runCalledCh != nil {
		f.runCalledOnce.Do(func() {
			close(f.runCalledCh)
		})
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
	oldReadSessionDeviceIdentity := readSessionDeviceIdentity
	readSessionDeviceIdentity = func(daemon.Paths) (daemon.DeviceIdentity, error) {
		return daemon.DeviceIdentity{}, os.ErrNotExist
	}
	t.Cleanup(func() {
		readSessionDeviceIdentity = oldReadSessionDeviceIdentity
	})
	for _, kv := range [][2]string{
		{"TUNNEL_BASE_URL", "http://127.0.0.1:8586"},
		{"TUNNEL_AUTH_TOKEN", "test-token"},
		{"TUNNEL_LAUNCH_REQUEST_ID", ""},
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
		"Usage:\n  tunnel run [options] <command> [args...]",
		"tunnel auth <command>",
		"tunnel daemon <command>",
		"tunnel update",
		"tunnel rollback",
		"Commands:\n  run",
		"auth",
		"daemon",
		"update",
		"rollback",
		"-h, --help",
		"--version",
		"TUNNEL_BASE_URL",
		"TUNNEL_AUTH_TOKEN",
		"TUNNEL_UPDATE_DISABLED",
		defaultTunnelBaseURL,
		"tunnel auth login",
		"tunnel auth status",
		"tunnel daemon start",
		"tunnel daemon open",
		"tunnel daemon close",
		"tunnel daemon sessions",
		"tunnel run claude",
		"tunnel run -l api-fix codex --profile prod",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("help text = %q, want fragment %q", text, fragment)
		}
	}
	wantEnvBlock := "Environment:\n  TUNNEL_AUTH_TOKEN  Higher-priority auth token override for tunnel run\n  TUNNEL_BASE_URL    Optional relay base URL (default: " + defaultTunnelBaseURL + ")\n  TUNNEL_UPDATE_DISABLED  Disable automatic update checks before tunnel run"
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
	oldCollectSessionMetadata := collectSessionMetadata
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
		collectSessionMetadata = oldCollectSessionMetadata
	})
	stubDetectGitBranch(t, "")

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
	err := runWithArgs([]string{"tunnel", "run", "codex", "--profile", "prod"}, &stdout, &stderr)
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

func TestRunWithArgsGuidesLegacyLauncherInvocation(t *testing.T) {
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
	err := runWithArgs([]string{"tunnel", "claude"}, &stdout, &stderr)
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
	if !strings.Contains(stderr.String(), "tunnel run claude") {
		t.Fatalf("stderr = %q, want migration guidance for tunnel run claude", stderr.String())
	}
	if resolveCalled || prepareCalled || startCalled || connectorCalled {
		t.Fatalf("legacy-launcher path unexpectedly touched runtime: resolve=%v prepare=%v start=%v connector=%v", resolveCalled, prepareCalled, startCalled, connectorCalled)
	}
}

func TestRunWithArgsPrintsRunHelpForMissingRunLauncher(t *testing.T) {
	setTestEnv(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel", "run"}, &stdout, &stderr)
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
	if got := stderr.String(); got != runHelpText() {
		t.Fatalf("stderr = %q, want runHelpText()", got)
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
	stubDetectGitBranch(t, "main")
	collectSessionMetadata = func() daemon.DeviceMetadata {
		return daemon.DeviceMetadata{
			DisplayName:    "Office Linux",
			Hostname:       "office-linux",
			PlatformFamily: daemon.PlatformFamilyLinux,
			PlatformID:     "ubuntu",
		}
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
	err := runWithArgs([]string{"tunnel", "run", "--label", "api-fix", "codex", "--profile", "prod"}, &stdout, &stderr)
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
	if gotInfo.GitBranch != "main" {
		t.Fatalf("GitBranch = %q, want main", gotInfo.GitBranch)
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
	if gotInfo.PlatformFamily != daemon.PlatformFamilyLinux {
		t.Fatalf("PlatformFamily = %q, want %q", gotInfo.PlatformFamily, daemon.PlatformFamilyLinux)
	}
	if gotInfo.PlatformID != "ubuntu" {
		t.Fatalf("PlatformID = %q, want ubuntu", gotInfo.PlatformID)
	}
	if gotInfo.ComputerName != "Office Linux" {
		t.Fatalf("ComputerName = %q, want Office Linux", gotInfo.ComputerName)
	}
	select {
	case <-fakeConnector.runCalledCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("connector Run was not called")
	}
	if fakeConnector.connectTTL != startupRelayWait {
		t.Fatalf("initial connect timeout = %v, want %v", fakeConnector.connectTTL, startupRelayWait)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want no startup banner when session start fails", got)
	}
}

func TestRunDaemonStartUsesStoredAuthFallback(t *testing.T) {
	store := &fakeStore{
		authPath: "/tmp/.tunnel/auth.json",
		record:   newStoredAuth("alice", "stored-token", "tok_123", "file-token", 1_700_000_000, time.Unix(1_700_000_100, 0)),
	}
	oldNewStore := newAuthStore
	oldResolvePaths := resolveDaemonPaths
	oldStartDaemon := startDaemon
	oldEnsureDaemonTmuxAvailable := ensureDaemonTmuxAvailable
	t.Cleanup(func() {
		newAuthStore = oldNewStore
		resolveDaemonPaths = oldResolvePaths
		startDaemon = oldStartDaemon
		ensureDaemonTmuxAvailable = oldEnsureDaemonTmuxAvailable
	})
	newAuthStore = func() authStore { return store }
	ensureDaemonTmuxAvailable = func() error { return nil }
	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	var got daemon.StartOptions
	startDaemon = func(ctx context.Context, options daemon.StartOptions) (daemon.StartResult, error) {
		got = options
		return daemon.StartResult{Status: daemon.StatusInfo{PID: 123, DeviceID: "dev_123"}}, nil
	}
	oldEnv, existed := os.LookupEnv(tunnelAuthTokenEnv)
	os.Unsetenv(tunnelAuthTokenEnv)
	t.Cleanup(func() {
		if existed {
			os.Setenv(tunnelAuthTokenEnv, oldEnv)
		} else {
			os.Unsetenv(tunnelAuthTokenEnv)
		}
	})

	var stdout bytes.Buffer
	if err := runDaemonStart(context.Background(), "http://127.0.0.1:8586", &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonStart returned error: %v", err)
	}
	if got.AuthToken != "file-token" {
		t.Fatalf("AuthToken = %q, want file-token", got.AuthToken)
	}
}

func TestRunDaemonStartPrefersEnvAuthOverride(t *testing.T) {
	store := &fakeStore{
		authPath: "/tmp/.tunnel/auth.json",
		record:   newStoredAuth("alice", "stored-token", "tok_123", "file-token", 1_700_000_000, time.Unix(1_700_000_100, 0)),
	}
	oldNewStore := newAuthStore
	oldResolvePaths := resolveDaemonPaths
	oldStartDaemon := startDaemon
	oldEnsureDaemonTmuxAvailable := ensureDaemonTmuxAvailable
	t.Cleanup(func() {
		newAuthStore = oldNewStore
		resolveDaemonPaths = oldResolvePaths
		startDaemon = oldStartDaemon
		ensureDaemonTmuxAvailable = oldEnsureDaemonTmuxAvailable
	})
	newAuthStore = func() authStore { return store }
	ensureDaemonTmuxAvailable = func() error { return nil }
	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	var got daemon.StartOptions
	startDaemon = func(ctx context.Context, options daemon.StartOptions) (daemon.StartResult, error) {
		got = options
		return daemon.StartResult{Status: daemon.StatusInfo{PID: 123, DeviceID: "dev_123"}}, nil
	}
	oldEnv, existed := os.LookupEnv(tunnelAuthTokenEnv)
	os.Setenv(tunnelAuthTokenEnv, "env-token")
	t.Cleanup(func() {
		if existed {
			os.Setenv(tunnelAuthTokenEnv, oldEnv)
		} else {
			os.Unsetenv(tunnelAuthTokenEnv)
		}
	})

	var stdout bytes.Buffer
	if err := runDaemonStart(context.Background(), "http://127.0.0.1:8586", &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonStart returned error: %v", err)
	}
	if got.AuthToken != "env-token" {
		t.Fatalf("AuthToken = %q, want env-token", got.AuthToken)
	}
}

func TestRunDaemonStartReturnsExistingDaemonWithoutResolvingAuth(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	oldNewStore := newAuthStore
	oldStartDaemon := startDaemon
	oldEnsureDaemonTmuxAvailable := ensureDaemonTmuxAvailable
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
		newAuthStore = oldNewStore
		startDaemon = oldStartDaemon
		ensureDaemonTmuxAvailable = oldEnsureDaemonTmuxAvailable
	})

	ensureDaemonTmuxAvailable = func() error { return nil }
	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStatus = func(ctx context.Context, paths daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{Running: true, PID: 42, DeviceID: "dev_existing"}, nil
	}
	newAuthStore = func() authStore {
		return &fakeStore{loadErr: errStoredAuthNotFound}
	}
	startDaemon = func(ctx context.Context, options daemon.StartOptions) (daemon.StartResult, error) {
		t.Fatal("startDaemon should not be called when daemon is already running")
		return daemon.StartResult{}, nil
	}

	var stdout bytes.Buffer
	if err := runDaemonStart(context.Background(), "http://127.0.0.1:8586", &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonStart returned error: %v", err)
	}
	if got := stdout.String(); got != "daemon already running (pid=42 device_id=dev_existing)\n" {
		t.Fatalf("stdout = %q, want already-running message", got)
	}
}

func TestRunDaemonStartRejectsChangingBaseURLWhileDaemonIsRunning(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	oldNewStore := newAuthStore
	oldStartDaemon := startDaemon
	oldEnsureDaemonTmuxAvailable := ensureDaemonTmuxAvailable
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
		newAuthStore = oldNewStore
		startDaemon = oldStartDaemon
		ensureDaemonTmuxAvailable = oldEnsureDaemonTmuxAvailable
	})

	ensureDaemonTmuxAvailable = func() error { return nil }
	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStatus = func(ctx context.Context, paths daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{
			Running:  true,
			PID:      42,
			DeviceID: "dev_existing",
			BaseURL:  "https://diaro.me",
		}, nil
	}
	newAuthStore = func() authStore {
		t.Fatal("newAuthStore should not be called when daemon is already running")
		return nil
	}
	startDaemon = func(ctx context.Context, options daemon.StartOptions) (daemon.StartResult, error) {
		t.Fatal("startDaemon should not be called when daemon is already running")
		return daemon.StartResult{}, nil
	}

	err := runDaemonStart(context.Background(), "http://1.12.249.160", io.Discard, io.Discard)
	if err == nil {
		t.Fatal("runDaemonStart error = nil, want base-url mismatch error")
	}
	if got := err.Error(); got != "daemon already running against https://diaro.me; stop it before starting with http://1.12.249.160" {
		t.Fatalf("error = %q, want mismatch guidance", got)
	}
}

func TestRunDaemonStatusPrintsFriendlyMessageWhenDaemonNotStarted(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStatus = func(context.Context, daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{}, daemon.ErrNotRunning
	}

	var stdout bytes.Buffer
	if err := runDaemonStatus(context.Background(), &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonStatus returned error: %v", err)
	}
	const want = "running: false\nstatus: not started\nhint: start it with `tunnel daemon start`\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunDaemonStartPrintsTmuxInstallGuidanceWhenTmuxMissing(t *testing.T) {
	oldEnsureDaemonTmuxAvailable := ensureDaemonTmuxAvailable
	t.Cleanup(func() {
		ensureDaemonTmuxAvailable = oldEnsureDaemonTmuxAvailable
	})
	ensureDaemonTmuxAvailable = func() error { return daemon.ErrTmuxNotFound }

	err := runDaemonStart(context.Background(), "http://127.0.0.1:8586", io.Discard, io.Discard)
	if err == nil {
		t.Fatal("runDaemonStart error = nil, want tmux guidance")
	}
	if !strings.Contains(err.Error(), "tmux is required for `tunnel daemon`") {
		t.Fatalf("error = %q, want tmux install guidance", err.Error())
	}
}

func TestRunDaemonStopPrintsFriendlyMessageWhenDaemonNotRunning(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStop := daemonStop
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStop = oldDaemonStop
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStop = func(context.Context, daemon.Paths) error {
		return daemon.ErrNotRunning
	}

	var stdout bytes.Buffer
	if err := runDaemonStop(context.Background(), &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonStop returned error: %v", err)
	}
	if got := stdout.String(); got != "daemon already stopped\n" {
		t.Fatalf("stdout = %q, want already-stopped message", got)
	}
}

func TestRunDaemonOpenUsesWorkspaceHelper(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldOpenDaemonWorkspace := openDaemonWorkspace
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		openDaemonWorkspace = oldOpenDaemonWorkspace
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	called := false
	openDaemonWorkspace = func(context.Context, daemon.Paths, io.Reader, io.Writer, io.Writer) error {
		called = true
		return nil
	}

	if err := runDaemonOpen(context.Background(), strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("runDaemonOpen returned error: %v", err)
	}
	if !called {
		t.Fatal("runDaemonOpen did not call workspace helper")
	}
}

func TestRunDaemonOpenPrintsFriendlyMessageWhenWorkspaceIsEmpty(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldOpenDaemonWorkspace := openDaemonWorkspace
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		openDaemonWorkspace = oldOpenDaemonWorkspace
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	openDaemonWorkspace = func(context.Context, daemon.Paths, io.Reader, io.Writer, io.Writer) error {
		return daemon.ErrNoWorkspaceSessions
	}

	var stdout bytes.Buffer
	if err := runDaemonOpen(context.Background(), strings.NewReader(""), &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonOpen returned error: %v", err)
	}
	if got := stdout.String(); got != "no daemon-managed sessions; start one from a remote launch first\n" {
		t.Fatalf("stdout = %q, want no-sessions message", got)
	}
}

func TestRunDaemonCloseUsesWorkspaceHelper(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldCloseDaemonWorkspace := closeDaemonWorkspace
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		closeDaemonWorkspace = oldCloseDaemonWorkspace
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	called := false
	closeDaemonWorkspace = func(context.Context, daemon.Paths) error {
		called = true
		return nil
	}

	var stdout bytes.Buffer
	if err := runDaemonClose(context.Background(), &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonClose returned error: %v", err)
	}
	if !called {
		t.Fatal("runDaemonClose did not call workspace helper")
	}
	if got := stdout.String(); got != "daemon workspace view closed\n" {
		t.Fatalf("stdout = %q, want closed message", got)
	}
}

func TestRunDaemonClosePrintsFriendlyMessageWhenWorkspaceIsNotOpen(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldCloseDaemonWorkspace := closeDaemonWorkspace
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		closeDaemonWorkspace = oldCloseDaemonWorkspace
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	closeDaemonWorkspace = func(context.Context, daemon.Paths) error {
		return daemon.ErrNoOpenWorkspace
	}

	var stdout bytes.Buffer
	if err := runDaemonClose(context.Background(), &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonClose returned error: %v", err)
	}
	if got := stdout.String(); got != "no open daemon workspace to close\n" {
		t.Fatalf("stdout = %q, want no-open-workspace message", got)
	}
}

func TestRunDaemonCloseReturnsTmuxInstallGuidanceWhenTmuxIsMissing(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldCloseDaemonWorkspace := closeDaemonWorkspace
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		closeDaemonWorkspace = oldCloseDaemonWorkspace
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	closeDaemonWorkspace = func(context.Context, daemon.Paths) error {
		return daemon.ErrTmuxNotFound
	}

	var stdout bytes.Buffer
	err := runDaemonClose(context.Background(), &stdout, io.Discard)
	if err == nil {
		t.Fatal("runDaemonClose returned nil error, want tmux install guidance")
	}
	if !strings.Contains(err.Error(), "tmux is required") {
		t.Fatalf("error = %q, want tmux install guidance", err.Error())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty stdout", got)
	}
}

func TestRunDaemonSessionsPrintsThinWorkspaceListing(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldListDaemonWorkspace := listDaemonWorkspace
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		listDaemonWorkspace = oldListDaemonWorkspace
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	listDaemonWorkspace = func(context.Context, daemon.Paths) ([]daemon.WorkspaceSession, error) {
		return []daemon.WorkspaceSession{{Name: "launch_abc", Windows: 1, Attached: 0}}, nil
	}

	var stdout bytes.Buffer
	if err := runDaemonSessions(context.Background(), &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonSessions returned error: %v", err)
	}
	got := stdout.String()
	for _, want := range []string{"NAME", "WINDOWS", "ATTACHED", "launch_abc"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want fragment %q", got, want)
		}
	}
}

func TestRunDaemonStatusPrintsFriendlyPanelWhenDaemonIsRunning(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStatus = func(context.Context, daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{
			Running:          true,
			PID:              98338,
			BaseURL:          "https://diaro.me",
			DeviceID:         "dev_8c0bfb5ee7c954f71d6dd3e4",
			DisplayName:      "yuanbo's MacBook Air",
			Hostname:         "yuanbos-MacBook-Air.local",
			PlatformFamily:   "macos",
			RelayConnected:   false,
			LaunchHealth:     daemon.LaunchHealthHealthy,
			WorkspaceBackend: "tmux",
		}, nil
	}

	var stdout bytes.Buffer
	if err := runDaemonStatus(context.Background(), &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonStatus returned error: %v", err)
	}
	got := stdout.String()
	for _, want := range []string{
		"Tunnel Daemon Status\n",
		"✅ Status: running\n",
		"⚠️ Relay: disconnected\n",
		"✅ Launch Readiness: ready\n",
		"Device: yuanbo's MacBook Air\n",
		"Device ID: dev_8c0bfb5ee7c954f71d6dd3e4\n",
		"Host: yuanbos-MacBook-Air.local (macos)\n",
		"PID: 98338\n",
		"Relay URL: https://diaro.me\n",
		"Workspace: tmux\n",
		"Last Launch Failure: none\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want fragment %q", got, want)
		}
	}
}

func TestRunDaemonDoctorPrintsFriendlyReportAndReturnsExitError(t *testing.T) {
	t.Setenv(tunnelAuthTokenEnv, "")

	oldResolvePaths := resolveDaemonPaths
	oldDaemonDoctor := daemonDoctor
	oldNewStore := newAuthStore
	oldResolveDoctorRelayBaseURL := resolveDoctorRelayBaseURL
	oldDoctorProbeRelayHealth := doctorProbeRelayHealth
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonDoctor = oldDaemonDoctor
		newAuthStore = oldNewStore
		resolveDoctorRelayBaseURL = oldResolveDoctorRelayBaseURL
		doctorProbeRelayHealth = oldDoctorProbeRelayHealth
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	newAuthStore = func() authStore {
		return &fakeStore{loadErr: errStoredAuthNotFound}
	}
	resolveDoctorRelayBaseURL = func(context.Context, daemon.Paths) string {
		return "https://diaro.me"
	}
	doctorProbeRelayHealth = func(context.Context, string) error {
		return errors.New("dial timeout")
	}
	daemonDoctor = func(context.Context, daemon.Paths) (daemon.DoctorReport, error) {
		return daemon.DoctorReport{
			Checks: []daemon.DoctorCheck{
				{
					Name:   "daemon process",
					Status: daemon.CheckStatusFail,
					Detail: "background daemon is not running, so remote launches cannot start on this machine",
				},
				{
					Name:   "tmux",
					Status: daemon.CheckStatusOK,
					Detail: "tmux is installed, so remote launches can create persistent workspace sessions",
				},
			},
		}, nil
	}

	var stdout bytes.Buffer
	err := runDaemonDoctor(context.Background(), &stdout, io.Discard)
	var exitErr exitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("runDaemonDoctor error = %v, want exitError", err)
	}
	if exitErr.code != 1 {
		t.Fatalf("exitErr.code = %d, want 1", exitErr.code)
	}
	got := stdout.String()
	for _, want := range []string{
		"Tunnel Daemon Doctor\n",
		"Status: not ready for remote launch (2 fail, 1 warn, 1 ok)\n",
		"❌ Auth Token\n",
		"⚠️ Relay Server\n",
		"relay_base_url: https://diaro.me; healthz: unavailable (dial timeout)\n",
		"❌ Daemon\n",
		"background daemon is not running, so remote launches cannot start on this machine\n",
		"✅ Tmux\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want fragment %q", got, want)
		}
	}
}

func TestRunDaemonDoctorUsesDefaultRelayBaseURLWhenNothingIsRecorded(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldDaemonDoctor := daemonDoctor
	oldNewStore := newAuthStore
	oldResolveDoctorRelayBaseURL := resolveDoctorRelayBaseURL
	oldDoctorProbeRelayHealth := doctorProbeRelayHealth
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonDoctor = oldDaemonDoctor
		newAuthStore = oldNewStore
		resolveDoctorRelayBaseURL = oldResolveDoctorRelayBaseURL
		doctorProbeRelayHealth = oldDoctorProbeRelayHealth
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	newAuthStore = func() authStore {
		return &fakeStore{
			authPath: "/tmp/.tunnel/auth.json",
			record:   newStoredAuth("alice", "stored-token", "tok_123", "file-token", 1_700_000_000, time.Unix(1_700_000_100, 0)),
		}
	}
	resolveDoctorRelayBaseURL = func(context.Context, daemon.Paths) string {
		return defaultTunnelBaseURL
	}
	doctorProbeRelayHealth = func(context.Context, string) error {
		return nil
	}
	daemonDoctor = func(context.Context, daemon.Paths) (daemon.DoctorReport, error) {
		return daemon.DoctorReport{Checks: []daemon.DoctorCheck{{Name: "daemon process", Status: daemon.CheckStatusOK, Detail: "background daemon is running"}}}, nil
	}

	var stdout bytes.Buffer
	if err := runDaemonDoctor(context.Background(), &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonDoctor returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "relay_base_url: https://diaro.me; healthz: ok") {
		t.Fatalf("stdout = %q, want default relay base URL healthz line", stdout.String())
	}
}

func TestRunWithArgsPrintsDaemonHelp(t *testing.T) {
	setTestEnv(t)

	var stdout bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "daemon", "--help"}, &stdout, io.Discard); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}
	if got := stdout.String(); got != daemonHelpText() {
		t.Fatalf("stdout = %q, want daemonHelpText()", got)
	}
}

func TestRunWithArgsPrintsDaemonStartHelp(t *testing.T) {
	setTestEnv(t)

	var stdout bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "daemon", "start", "--help"}, &stdout, io.Discard); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}
	if got := stdout.String(); got != daemonStartHelpText() {
		t.Fatalf("stdout = %q, want daemonStartHelpText()", got)
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
	oldCollectSessionMetadata := collectSessionMetadata
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
		collectSessionMetadata = oldCollectSessionMetadata
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
	stubDetectGitBranch(t, "feature/custom-agent")
	collectSessionMetadata = func() daemon.DeviceMetadata {
		return daemon.DeviceMetadata{
			DisplayName:    "",
			Hostname:       "fallback-host",
			PlatformFamily: daemon.PlatformFamilyMacOS,
			PlatformID:     daemon.PlatformFamilyMacOS,
		}
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
	err := runWithArgs([]string{"tunnel", "run", "/opt/bin/custom-agent", "--mode", "fast"}, &stdout, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if gotInfo.Launcher != "/opt/bin/custom-agent" {
		t.Fatalf("Launcher = %q, want /opt/bin/custom-agent", gotInfo.Launcher)
	}
	if gotInfo.CommandPreview != "/opt/bin/custom-agent --mode fast" {
		t.Fatalf("CommandPreview = %q, want /opt/bin/custom-agent --mode fast", gotInfo.CommandPreview)
	}
	if gotInfo.GitBranch != "feature/custom-agent" {
		t.Fatalf("GitBranch = %q, want feature/custom-agent", gotInfo.GitBranch)
	}
	if gotInfo.DeviceID != "" {
		t.Fatalf("DeviceID = %q, want empty for direct local run", gotInfo.DeviceID)
	}
	if gotInfo.PlatformFamily != daemon.PlatformFamilyMacOS {
		t.Fatalf("PlatformFamily = %q, want %q", gotInfo.PlatformFamily, daemon.PlatformFamilyMacOS)
	}
	if gotInfo.PlatformID != daemon.PlatformFamilyMacOS {
		t.Fatalf("PlatformID = %q, want %q", gotInfo.PlatformID, daemon.PlatformFamilyMacOS)
	}
	if gotInfo.ComputerName != "fallback-host" {
		t.Fatalf("ComputerName = %q, want fallback-host", gotInfo.ComputerName)
	}
}

func TestSessionDeviceIDReadsExistingDaemonIdentity(t *testing.T) {
	oldResolveDaemonPaths := resolveDaemonPaths
	oldReadSessionDeviceIdentity := readSessionDeviceIdentity
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolveDaemonPaths
		readSessionDeviceIdentity = oldReadSessionDeviceIdentity
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{DeviceFile: "/tmp/tunnel-device.json"}, nil
	}
	readSessionDeviceIdentity = func(paths daemon.Paths) (daemon.DeviceIdentity, error) {
		if paths.DeviceFile != "/tmp/tunnel-device.json" {
			t.Fatalf("DeviceFile = %q, want /tmp/tunnel-device.json", paths.DeviceFile)
		}
		return daemon.DeviceIdentity{DeviceID: " dev-existing "}, nil
	}

	if got := sessionDeviceID(); got != "dev-existing" {
		t.Fatalf("sessionDeviceID() = %q, want dev-existing", got)
	}
}

func TestRunWithArgsNormalizesUnavailableSessionIdentityMetadata(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	oldCollectSessionMetadata := collectSessionMetadata
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
		collectSessionMetadata = oldCollectSessionMetadata
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}

	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}

	var gotInfo protocol.SessionInfo
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		gotInfo = info
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}
	stubDetectGitBranch(t, "")
	collectSessionMetadata = func() daemon.DeviceMetadata {
		return daemon.DeviceMetadata{
			DisplayName:    "",
			Hostname:       "   ",
			PlatformFamily: "",
			PlatformID:     "",
		}
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
	err := runWithArgs([]string{"tunnel", "run", "codex"}, &stdout, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if gotInfo.PlatformFamily != "" {
		t.Fatalf("PlatformFamily = %q, want empty", gotInfo.PlatformFamily)
	}
	if gotInfo.PlatformID != "" {
		t.Fatalf("PlatformID = %q, want empty", gotInfo.PlatformID)
	}
	if gotInfo.ComputerName != "" {
		t.Fatalf("ComputerName = %q, want empty", gotInfo.ComputerName)
	}
	if gotInfo.GitBranch != "" {
		t.Fatalf("GitBranch = %q, want empty", gotInfo.GitBranch)
	}
}

func TestRunWithArgsPreservesLiteralUnknownIdentityValues(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	oldCollectSessionMetadata := collectSessionMetadata
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
		collectSessionMetadata = oldCollectSessionMetadata
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}

	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}

	var gotInfo protocol.SessionInfo
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		gotInfo = info
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}
	stubDetectGitBranch(t, "main")
	collectSessionMetadata = func() daemon.DeviceMetadata {
		return daemon.DeviceMetadata{
			DisplayName:    "Unknown Device",
			Hostname:       "real-host",
			PlatformFamily: daemon.PlatformFamilyLinux,
			PlatformID:     "unknown",
		}
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
	err := runWithArgs([]string{"tunnel", "run", "codex"}, &stdout, &stderr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if gotInfo.ComputerName != "Unknown Device" {
		t.Fatalf("ComputerName = %q, want literal Unknown Device preserved", gotInfo.ComputerName)
	}
	if gotInfo.PlatformID != "unknown" {
		t.Fatalf("PlatformID = %q, want literal unknown preserved", gotInfo.PlatformID)
	}
	if gotInfo.GitBranch != "main" {
		t.Fatalf("GitBranch = %q, want main", gotInfo.GitBranch)
	}
}

func TestRunWithArgsForwardsLaunchRequestIDFromEnv(t *testing.T) {
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
	stubDetectGitBranch(t, "")

	oldLaunchRequestID, hadLaunchRequestID := os.LookupEnv(tunnelLaunchRequestIDEnv)
	os.Setenv(tunnelLaunchRequestIDEnv, "req-123")
	t.Cleanup(func() {
		if hadLaunchRequestID {
			os.Setenv(tunnelLaunchRequestIDEnv, oldLaunchRequestID)
		} else {
			os.Unsetenv(tunnelLaunchRequestIDEnv)
		}
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}

	fakeConnector := &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		return fakeConnector
	}

	wantErr := errors.New("start session failed")
	startSession = func(_ context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		return nil, wantErr
	}
	waitForExit = func(context.Context, <-chan struct{}, <-chan error) error {
		return nil
	}

	err := runWithArgs([]string{"tunnel", "run", "codex"}, io.Discard, io.Discard)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if fakeConnector.launchRequestID != "req-123" {
		t.Fatalf("launchRequestID = %q, want req-123", fakeConnector.launchRequestID)
	}
}

func TestStartupBannerUsesRelayState(t *testing.T) {
	if got := startupBanner("codex", "sess-123"); got != "\r\x1b[2K\x1b[92m▶ tunnel codex — session sess-123; relay server connected\x1b[0m\r" {
		t.Fatalf("connected banner = %q", got)
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
	stubDetectGitBranch(t, "")

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
	if err := runWithArgs([]string{"tunnel", "run", "--verbose", "codex"}, &stdout, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}

	if got := stderr.String(); got != startupBanner("codex", sessionID) {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunWithArgsSuppressesStartupBannerByDefault(t *testing.T) {
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
	stubDetectGitBranch(t, "")

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
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "run", "codex"}, &stdout, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v", err)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty without --verbose", got)
	}
}

func TestRunWithArgsFailsStartupWhenRelayCannotConnect(t *testing.T) {
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
	stubDetectGitBranch(t, "")

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/bin/sh", Args: []string{"-c", "exit 0"}}, nil
	}

	prepareCalled := false
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		prepareCalled = true
		return &session.LocalTerminal{}, nil
	}

	startCalled := false
	startSession = func(ctx context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		startCalled = true
		return session.StartCommandWithInitialSinks(ctx, path, args, sinks)
	}

	done := make(chan struct{})
	startLocalTerminal = func(context.Context, *session.LocalTerminal, *session.Hub) <-chan struct{} {
		close(done)
		return done
	}

	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		return &fakeRelayConnector{waitConnected: false, state: connector.StateReconnecting}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel", "run", "codex"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("runWithArgs error = nil, want startup connection failure")
	}
	if !strings.Contains(err.Error(), "failed to connect to the relay server") {
		t.Fatalf("runWithArgs error = %v, want failed relay connect error", err)
	}
	if prepareCalled {
		t.Fatal("runWithArgs called prepareLocalTerminal despite startup relay failure")
	}
	if startCalled {
		t.Fatal("runWithArgs started child despite startup relay failure")
	}

	if got := stderr.String(); got != "" {
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
