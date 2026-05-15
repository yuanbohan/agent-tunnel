package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	gozxingqrcode "github.com/makiuchi-d/gozxing/qrcode"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/tunnel/connector"
	"yuanbohan/tunnel/internal/tunnel/daemon"
	"yuanbohan/tunnel/internal/tunnel/launcher"
	"yuanbohan/tunnel/internal/tunnel/session"
)

type fakeRelayConnector struct {
	waitConnected bool
	state         connector.State
	runCalledCh   chan struct{}
	runCalledOnce sync.Once
	boundHub      *session.Hub
	initialCols   int
	initialRows   int
	connectTTL    time.Duration
	launchContext protocol.LaunchContext
	launchReady   protocol.LaunchContext
	stopHandler   func()
	stateCh       chan connector.State
}

type fakeLocalRegistration struct {
	runCalledCh   chan struct{}
	runCalledOnce sync.Once
	closeCalled   bool
	output        [][]byte
	waitErr       error
	waitHook      func(context.Context)
}

func (f *fakeLocalRegistration) Run(context.Context) {
	if f.runCalledCh != nil {
		f.runCalledOnce.Do(func() {
			close(f.runCalledCh)
		})
	}
}

func (f *fakeLocalRegistration) Close() error {
	f.closeCalled = true
	return nil
}

func (f *fakeLocalRegistration) WriteOutput(data []byte) error {
	f.output = append(f.output, append([]byte(nil), data...))
	return nil
}

func (f *fakeLocalRegistration) BindHub(*session.Hub) {}

func (f *fakeLocalRegistration) WaitUntilRegistered(ctx context.Context) error {
	if f.waitHook != nil {
		f.waitHook(ctx)
	}
	return f.waitErr
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

func (f *fakeRelayConnector) SetLaunchContext(launchContext protocol.LaunchContext) {
	f.launchContext = launchContext
}

func (f *fakeRelayConnector) MarkLaunchReady(launchContext protocol.LaunchContext) {
	f.launchReady = launchContext
}

func (f *fakeRelayConnector) SetStopHandler(handler func()) {
	f.stopHandler = handler
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
	oldReadOrCreateSessionDeviceIdentity := readOrCreateSessionDeviceIdentity
	oldEnsureTunnelRunDaemon := ensureTunnelRunDaemon
	oldNewSessionRegistration := newSessionRegistration
	readSessionDeviceIdentity = func(daemon.Paths) (daemon.DeviceIdentity, error) {
		return daemon.DeviceIdentity{}, os.ErrNotExist
	}
	readOrCreateSessionDeviceIdentity = func(daemon.Paths) (daemon.DeviceIdentity, error) {
		return daemon.DeviceIdentity{}, os.ErrNotExist
	}
	ensureTunnelRunDaemon = func(context.Context, string, string) (tunnelRunDaemonEnsureResult, error) {
		return tunnelRunDaemonEnsureResult{}, nil
	}
	newSessionRegistration = func(daemon.Paths, string, string, protocol.SessionInfo) localSessionRegistration {
		return &fakeLocalRegistration{}
	}
	t.Cleanup(func() {
		readSessionDeviceIdentity = oldReadSessionDeviceIdentity
		readOrCreateSessionDeviceIdentity = oldReadOrCreateSessionDeviceIdentity
		ensureTunnelRunDaemon = oldEnsureTunnelRunDaemon
		newSessionRegistration = oldNewSessionRegistration
	})
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
		"Usage:\n  tunnel run [options] <command> [args...]",
		"tunnel auth <command>",
		"tunnel daemon <command>",
		"tunnel pair [command]",
		"tunnel workspace <command>",
		"tunnel update",
		"tunnel rollback",
		"Commands:\n  run",
		"auth",
		"daemon",
		"pair",
		"workspace",
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
		"tunnel pair",
		"tunnel pair devices",
		"tunnel workspace open",
		"tunnel workspace close",
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
	oldEnsureTunnelRunDaemon := ensureTunnelRunDaemon
	oldNewSessionRegistration := newSessionRegistration
	oldDaemonStop := daemonStop
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
		ensureTunnelRunDaemon = oldEnsureTunnelRunDaemon
		newSessionRegistration = oldNewSessionRegistration
		daemonStop = oldDaemonStop
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

func TestRunWithArgsAddsDaemonBrokerRegistrationSinkWhenDaemonAvailable(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldStartLocalTerminal := startLocalTerminal
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	oldEnsureTunnelRunDaemon := ensureTunnelRunDaemon
	oldNewSessionRegistration := newSessionRegistration
	oldReadSessionDeviceIdentity := readSessionDeviceIdentity
	oldReadOrCreateSessionDeviceIdentity := readOrCreateSessionDeviceIdentity
	oldDaemonStop := daemonStop
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
		ensureTunnelRunDaemon = oldEnsureTunnelRunDaemon
		newSessionRegistration = oldNewSessionRegistration
		readSessionDeviceIdentity = oldReadSessionDeviceIdentity
		readOrCreateSessionDeviceIdentity = oldReadOrCreateSessionDeviceIdentity
		daemonStop = oldDaemonStop
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		if info.DeviceID != "dev-started" {
			t.Fatalf("connector DeviceID = %q, want dev-started", info.DeviceID)
		}
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}
	ensureTunnelRunDaemon = func(ctx context.Context, baseURL, authToken string) (tunnelRunDaemonEnsureResult, error) {
		if baseURL != "http://127.0.0.1:8586" || authToken != "test-token" {
			t.Fatalf("ensure daemon got baseURL=%q authToken=%q", baseURL, authToken)
		}
		return tunnelRunDaemonEnsureResult{
			Paths:   daemon.Paths{SocketPath: "/tmp/daemon.sock", BrokerSocketPath: "/tmp/broker.sock", DeviceFile: "/tmp/device.json"},
			Started: true,
		}, nil
	}
	readSessionDeviceIdentity = func(paths daemon.Paths) (daemon.DeviceIdentity, error) {
		if paths.DeviceFile != "/tmp/device.json" {
			t.Fatalf("DeviceFile = %q, want /tmp/device.json", paths.DeviceFile)
		}
		return daemon.DeviceIdentity{DeviceID: "dev-started"}, nil
	}
	readOrCreateSessionDeviceIdentity = func(daemon.Paths) (daemon.DeviceIdentity, error) {
		return daemon.DeviceIdentity{DeviceID: "dev-started"}, nil
	}
	registration := &fakeLocalRegistration{runCalledCh: make(chan struct{})}
	stopCalled := false
	daemonStop = func(ctx context.Context, paths daemon.Paths) error {
		stopCalled = true
		if paths.SocketPath != "/tmp/daemon.sock" {
			t.Fatalf("daemonStop paths = %#v, want auto-started daemon paths", paths)
		}
		return nil
	}
	newSessionRegistration = func(paths daemon.Paths, baseURL, authToken string, info protocol.SessionInfo) localSessionRegistration {
		if paths.BrokerSocketPath != "/tmp/broker.sock" {
			t.Fatalf("BrokerSocketPath = %q, want /tmp/broker.sock", paths.BrokerSocketPath)
		}
		if baseURL != "http://127.0.0.1:8586" {
			t.Fatalf("baseURL = %q, want http://127.0.0.1:8586", baseURL)
		}
		if authToken != "test-token" {
			t.Fatalf("authToken = %q, want test-token", authToken)
		}
		if info.SessionID == "" || info.CommandPreview != "codex" {
			t.Fatalf("session info = %#v, want generated metadata", info)
		}
		if info.DeviceID != "dev-started" {
			t.Fatalf("registration DeviceID = %q, want dev-started", info.DeviceID)
		}
		return registration
	}

	wantErr := errors.New("start session failed")
	var gotSinks map[string]session.OutputSink
	startSession = func(_ context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		gotSinks = sinks
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
	if gotSinks["daemon-broker"] != registration {
		t.Fatalf("initial sinks = %#v, want daemon-broker registration sink", gotSinks)
	}
	if !stopCalled {
		t.Fatal("runWithArgs did not stop the daemon it auto-started after startSession failure")
	}
	select {
	case <-registration.runCalledCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("local registration Run was not called")
	}
	if !registration.closeCalled {
		t.Fatal("local registration Close was not called")
	}
}

func TestRunWithArgsStopsBeforeTerminalPrepWhenBrokerRegistrationFails(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldNewConnector := newConnector
	oldEnsureTunnelRunDaemon := ensureTunnelRunDaemon
	oldNewSessionRegistration := newSessionRegistration
	oldDaemonStop := daemonStop
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		newConnector = oldNewConnector
		ensureTunnelRunDaemon = oldEnsureTunnelRunDaemon
		newSessionRegistration = oldNewSessionRegistration
		daemonStop = oldDaemonStop
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		t.Fatal("newConnector should not be called before broker registration succeeds")
		return nil
	}
	ensureTunnelRunDaemon = func(context.Context, string, string) (tunnelRunDaemonEnsureResult, error) {
		return tunnelRunDaemonEnsureResult{
			Paths:   daemon.Paths{SocketPath: "/tmp/daemon.sock", BrokerSocketPath: "/tmp/broker.sock"},
			Started: true,
		}, nil
	}
	stopCalled := false
	daemonStop = func(ctx context.Context, paths daemon.Paths) error {
		stopCalled = true
		if paths.SocketPath != "/tmp/daemon.sock" {
			t.Fatalf("daemonStop paths = %#v, want auto-started daemon paths", paths)
		}
		return nil
	}
	newSessionRegistration = func(daemon.Paths, string, string, protocol.SessionInfo) localSessionRegistration {
		return &fakeLocalRegistration{waitErr: errors.New("timed out waiting for daemon broker to accept the session")}
	}
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		t.Fatal("prepareLocalTerminal should not be called before broker registration succeeds")
		return nil, nil
	}
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		t.Fatal("startSession should not be called before broker registration succeeds")
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel", "run", "codex"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "daemon broker registration failed") {
		t.Fatalf("runWithArgs error = %v, want broker registration failure", err)
	}
	if !stopCalled {
		t.Fatal("runWithArgs did not stop the daemon it auto-started after broker registration failure")
	}
}

func TestRunWithArgsTreatsBrokerRegistrationCancellationAsCleanExit(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldNewConnector := newConnector
	oldEnsureTunnelRunDaemon := ensureTunnelRunDaemon
	oldNewSessionRegistration := newSessionRegistration
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		newConnector = oldNewConnector
		ensureTunnelRunDaemon = oldEnsureTunnelRunDaemon
		newSessionRegistration = oldNewSessionRegistration
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}
	ensureTunnelRunDaemon = func(context.Context, string, string) (tunnelRunDaemonEnsureResult, error) {
		return tunnelRunDaemonEnsureResult{Paths: daemon.Paths{BrokerSocketPath: "/tmp/broker.sock"}}, nil
	}
	newSessionRegistration = func(daemon.Paths, string, string, protocol.SessionInfo) localSessionRegistration {
		return &fakeLocalRegistration{waitErr: context.Canceled}
	}
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		t.Fatal("prepareLocalTerminal should not be called after broker registration cancellation")
		return nil, nil
	}
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		t.Fatal("startSession should not be called after broker registration cancellation")
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runWithArgs([]string{"tunnel", "run", "codex"}, &stdout, &stderr); err != nil {
		t.Fatalf("runWithArgs error = %v, want clean cancellation", err)
	}
}

func TestRunWithArgsTreatsCancellationAfterBrokerRegistrationAsCleanExit(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldNewConnector := newConnector
	oldEnsureTunnelRunDaemon := ensureTunnelRunDaemon
	oldNewSessionRegistration := newSessionRegistration
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		newConnector = oldNewConnector
		ensureTunnelRunDaemon = oldEnsureTunnelRunDaemon
		newSessionRegistration = oldNewSessionRegistration
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}
	ensureTunnelRunDaemon = func(context.Context, string, string) (tunnelRunDaemonEnsureResult, error) {
		return tunnelRunDaemonEnsureResult{Paths: daemon.Paths{BrokerSocketPath: "/tmp/broker.sock"}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	newSessionRegistration = func(daemon.Paths, string, string, protocol.SessionInfo) localSessionRegistration {
		return &fakeLocalRegistration{waitHook: func(context.Context) {
			cancel()
		}}
	}
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		t.Fatal("prepareLocalTerminal should not be called after cancellation")
		return nil, nil
	}
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		t.Fatal("startSession should not be called after cancellation")
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runTunnelSession(ctx, emptyReader{}, runArgs{
		BaseURL:  "http://127.0.0.1:8586",
		Launcher: "codex",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runTunnelSession error = %v, want clean cancellation", err)
	}
}

func TestRunWithArgsRejectsPublicDaemonFlag(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldWaitForExit := waitForExit
	oldNewConnector := newConnector
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		return &session.LocalTerminal{}, nil
	}
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}
	var gotSinks map[string]session.OutputSink
	startSession = func(_ context.Context, path string, args []string, sinks map[string]session.OutputSink) (*session.Running, error) {
		gotSinks = sinks
		return nil, errors.New("start session should not be called")
	}
	waitForExit = func(context.Context, <-chan struct{}, <-chan error) error {
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel", "run", "--daemon", "off", "codex"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --daemon") {
		t.Fatalf("runWithArgs error = %v, want unknown --daemon flag", err)
	}
	if gotSinks != nil {
		t.Fatalf("startSession received sinks = %#v, want not called", gotSinks)
	}
}

func TestRunWithArgsFailsWhenRequiredDaemonUnavailable(t *testing.T) {
	setTestEnv(t)

	oldResolve := resolveLauncher
	oldPrepare := prepareLocalTerminal
	oldStartSession := startSession
	oldNewConnector := newConnector
	oldEnsureTunnelRunDaemon := ensureTunnelRunDaemon
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		newConnector = oldNewConnector
		ensureTunnelRunDaemon = oldEnsureTunnelRunDaemon
	})

	resolveLauncher = func(name string, args []string) (launcher.Command, error) {
		return launcher.Command{Name: name, Path: "/usr/bin/codex", Args: append([]string(nil), args...)}, nil
	}
	prepareLocalTerminal = func() (*session.LocalTerminal, error) {
		t.Fatal("prepareLocalTerminal should not be called when required daemon is unavailable")
		return nil, nil
	}
	startSession = func(context.Context, string, []string, map[string]session.OutputSink) (*session.Running, error) {
		t.Fatal("startSession should not be called when required daemon is unavailable")
		return nil, nil
	}
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		return &fakeRelayConnector{waitConnected: true, state: connector.StateConnected}
	}
	ensureTunnelRunDaemon = func(context.Context, string, string) (tunnelRunDaemonEnsureResult, error) {
		return tunnelRunDaemonEnsureResult{}, errors.New("daemon is not running")
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithArgs([]string{"tunnel", "run", "codex"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "daemon is required for tunnel run") {
		t.Fatalf("runWithArgs error = %v, want required daemon error", err)
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
	t.Cleanup(func() {
		newAuthStore = oldNewStore
		resolveDaemonPaths = oldResolvePaths
		startDaemon = oldStartDaemon
	})
	newAuthStore = func() authStore { return store }
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
	t.Cleanup(func() {
		newAuthStore = oldNewStore
		resolveDaemonPaths = oldResolvePaths
		startDaemon = oldStartDaemon
	})
	newAuthStore = func() authStore { return store }
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

func TestEnsureDaemonForTunnelRunRejectsDifferentAuthContext(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	oldStartDaemon := startDaemon
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
		startDaemon = oldStartDaemon
	})

	paths := daemon.Paths{BrokerSocketPath: "/tmp/broker.sock"}
	resolveDaemonPaths = func() (daemon.Paths, error) {
		return paths, nil
	}
	daemonStatus = func(context.Context, daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{
			Running:                true,
			BaseURL:                "https://relay.example.com",
			AuthContextFingerprint: daemon.AuthContextFingerprint("token-a"),
		}, nil
	}
	startDaemon = func(context.Context, daemon.StartOptions) (daemon.StartResult, error) {
		t.Fatal("startDaemon should not be called when daemon is already running")
		return daemon.StartResult{}, nil
	}

	got, err := ensureDaemonForTunnelRun(context.Background(), "https://relay.example.com", "token-b")
	if err == nil {
		t.Fatal("ensureDaemonForTunnelRun err = nil, want auth-context mismatch")
	}
	if got.Paths.BrokerSocketPath != paths.BrokerSocketPath {
		t.Fatalf("paths = %#v, want resolved daemon paths", got.Paths)
	}
}

func TestEnsureDaemonForTunnelRunAcceptsMatchingAuthContext(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	oldStartDaemon := startDaemon
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
		startDaemon = oldStartDaemon
	})

	paths := daemon.Paths{BrokerSocketPath: "/tmp/broker.sock"}
	resolveDaemonPaths = func() (daemon.Paths, error) {
		return paths, nil
	}
	daemonStatus = func(context.Context, daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{
			Running:                true,
			BaseURL:                "https://relay.example.com",
			AuthContextFingerprint: daemon.AuthContextFingerprint("token-a"),
		}, nil
	}
	startDaemon = func(context.Context, daemon.StartOptions) (daemon.StartResult, error) {
		t.Fatal("startDaemon should not be called when daemon is already running")
		return daemon.StartResult{}, nil
	}

	got, err := ensureDaemonForTunnelRun(context.Background(), "https://relay.example.com", "token-a")
	if err != nil {
		t.Fatalf("ensureDaemonForTunnelRun error = %v, want matching auth context", err)
	}
	if got.Paths.BrokerSocketPath != paths.BrokerSocketPath {
		t.Fatalf("paths = %#v, want resolved daemon paths", got.Paths)
	}
	if got.Started {
		t.Fatal("Started = true, want false for already-running daemon")
	}
}

func TestRunDaemonStartReturnsExistingDaemonWhenAuthContextMatches(t *testing.T) {
	t.Setenv(tunnelAuthTokenEnv, "token-a")
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	oldNewStore := newAuthStore
	oldStartDaemon := startDaemon
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
		newAuthStore = oldNewStore
		startDaemon = oldStartDaemon
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStatus = func(ctx context.Context, paths daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{
			Running:                true,
			PID:                    42,
			DeviceID:               "dev_existing",
			AuthContextFingerprint: daemon.AuthContextFingerprint("token-a"),
		}, nil
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

func TestRunDaemonStartReturnsExistingDaemonWhenLocalAuthUnavailable(t *testing.T) {
	oldEnv, existed := os.LookupEnv(tunnelAuthTokenEnv)
	os.Unsetenv(tunnelAuthTokenEnv)
	t.Cleanup(func() {
		if existed {
			os.Setenv(tunnelAuthTokenEnv, oldEnv)
		} else {
			os.Unsetenv(tunnelAuthTokenEnv)
		}
	})

	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	oldNewStore := newAuthStore
	oldStartDaemon := startDaemon
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
		newAuthStore = oldNewStore
		startDaemon = oldStartDaemon
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStatus = func(ctx context.Context, paths daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{
			Running:                true,
			PID:                    42,
			DeviceID:               "dev_existing",
			AuthContextFingerprint: daemon.AuthContextFingerprint("token-a"),
		}, nil
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

func TestRunDaemonStartRejectsRunningDaemonWithDifferentAuthContext(t *testing.T) {
	t.Setenv(tunnelAuthTokenEnv, "token-b")
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	oldStartDaemon := startDaemon
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
		startDaemon = oldStartDaemon
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStatus = func(ctx context.Context, paths daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{
			Running:                true,
			PID:                    42,
			DeviceID:               "dev_existing",
			BaseURL:                defaultTunnelBaseURL,
			AuthContextFingerprint: daemon.AuthContextFingerprint("token-a"),
		}, nil
	}
	startDaemon = func(ctx context.Context, options daemon.StartOptions) (daemon.StartResult, error) {
		t.Fatal("startDaemon should not be called when daemon is already running")
		return daemon.StartResult{}, nil
	}

	err := runDaemonStart(context.Background(), defaultTunnelBaseURL, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "different auth context") {
		t.Fatalf("runDaemonStart error = %v, want auth-context mismatch", err)
	}
}

func TestRunDaemonStartRejectsRunningDaemonWithoutAuthContextFingerprint(t *testing.T) {
	t.Setenv(tunnelAuthTokenEnv, "token-a")
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	oldStartDaemon := startDaemon
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
		startDaemon = oldStartDaemon
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStatus = func(ctx context.Context, paths daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{Running: true, PID: 42, DeviceID: "dev_existing", BaseURL: defaultTunnelBaseURL}, nil
	}
	startDaemon = func(ctx context.Context, options daemon.StartOptions) (daemon.StartResult, error) {
		t.Fatal("startDaemon should not be called when daemon is already running")
		return daemon.StartResult{}, nil
	}

	err := runDaemonStart(context.Background(), defaultTunnelBaseURL, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "different auth context") {
		t.Fatalf("runDaemonStart error = %v, want missing auth-context mismatch", err)
	}
}

func TestRunDaemonStartRejectsChangingBaseURLWhileDaemonIsRunning(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	oldNewStore := newAuthStore
	oldStartDaemon := startDaemon
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
		newAuthStore = oldNewStore
		startDaemon = oldStartDaemon
	})

	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStatus = func(ctx context.Context, paths daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{
			Running:  true,
			PID:      42,
			DeviceID: "dev_existing",
			BaseURL:  defaultTunnelBaseURL,
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
	if got := err.Error(); got != "daemon already running against "+defaultTunnelBaseURL+"; stop it before starting with http://1.12.249.160" {
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

func TestRunDaemonStatusJSON(t *testing.T) {
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
			Running:      true,
			PID:          42,
			DeviceID:     "dev_123",
			BaseURL:      "https://relay.example.com",
			LaunchHealth: daemon.LaunchHealthHealthy,
		}, nil
	}

	var stdout bytes.Buffer
	if err := runDaemonStatusWithOptions(context.Background(), &stdout, io.Discard, true); err != nil {
		t.Fatalf("runDaemonStatusWithOptions returned error: %v", err)
	}
	var status daemon.StatusInfo
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("status JSON unmarshal returned error: %v\n%s", err, stdout.String())
	}
	if !status.Running || status.PID != 42 || status.DeviceID != "dev_123" {
		t.Fatalf("status = %#v, want JSON daemon status", status)
	}
}

func TestRunDaemonStartDoesNotRequireTmuxForDaemonProcess(t *testing.T) {
	t.Setenv(tunnelAuthTokenEnv, "env-token")
	oldResolvePaths := resolveDaemonPaths
	oldDaemonStatus := daemonStatus
	oldStartDaemon := startDaemon
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonStatus = oldDaemonStatus
		startDaemon = oldStartDaemon
	})
	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{}, nil
	}
	daemonStatus = func(context.Context, daemon.Paths) (daemon.StatusInfo, error) {
		return daemon.StatusInfo{}, daemon.ErrNotRunning
	}
	startDaemon = func(context.Context, daemon.StartOptions) (daemon.StartResult, error) {
		return daemon.StartResult{
			Status: daemon.StatusInfo{
				Running:      true,
				PID:          42,
				DeviceID:     "dev_started",
				LaunchHealth: daemon.LaunchHealthDegraded,
				LastFailure:  "tmux_not_found",
			},
		}, nil
	}

	var stdout bytes.Buffer
	if err := runDaemonStart(context.Background(), "http://127.0.0.1:8586", &stdout, io.Discard); err != nil {
		t.Fatalf("runDaemonStart returned error: %v", err)
	}
	want := "daemon started (pid=42 device_id=dev_started)\nwarning: launch readiness is degraded (tmux_not_found); remote launch may be unavailable\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want daemon-started message", got)
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

func TestRunWorkspaceOpenUsesWorkspaceHelper(t *testing.T) {
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

	if err := runWorkspaceOpen(context.Background(), strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("runWorkspaceOpen returned error: %v", err)
	}
	if !called {
		t.Fatal("runWorkspaceOpen did not call workspace helper")
	}
}

func TestRunWorkspaceOpenPrintsFriendlyMessageWhenWorkspaceIsEmpty(t *testing.T) {
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
	if err := runWorkspaceOpen(context.Background(), strings.NewReader(""), &stdout, io.Discard); err != nil {
		t.Fatalf("runWorkspaceOpen returned error: %v", err)
	}
	if got := stdout.String(); got != "no workspace sessions yet; start one from the mobile app first\n" {
		t.Fatalf("stdout = %q, want no-sessions message", got)
	}
}

func TestRunWorkspaceCloseUsesWorkspaceHelper(t *testing.T) {
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
	if err := runWorkspaceClose(context.Background(), &stdout, io.Discard); err != nil {
		t.Fatalf("runWorkspaceClose returned error: %v", err)
	}
	if !called {
		t.Fatal("runWorkspaceClose did not call workspace helper")
	}
	if got := stdout.String(); got != "Tunnel workspace view closed\n" {
		t.Fatalf("stdout = %q, want closed message", got)
	}
}

func TestRunWorkspaceClosePrintsFriendlyMessageWhenWorkspaceIsNotOpen(t *testing.T) {
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
	if err := runWorkspaceClose(context.Background(), &stdout, io.Discard); err != nil {
		t.Fatalf("runWorkspaceClose returned error: %v", err)
	}
	if got := stdout.String(); got != "no open Tunnel workspace view to close\n" {
		t.Fatalf("stdout = %q, want no-open-workspace message", got)
	}
}

func TestRunWorkspaceCloseReturnsTmuxInstallGuidanceWhenTmuxIsMissing(t *testing.T) {
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
	err := runWorkspaceClose(context.Background(), &stdout, io.Discard)
	if err == nil {
		t.Fatal("runWorkspaceClose returned nil error, want tmux install guidance")
	}
	if !strings.Contains(err.Error(), "tmux is required") {
		t.Fatalf("error = %q, want tmux install guidance", err.Error())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty stdout", got)
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
			BaseURL:          defaultTunnelBaseURL,
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
		"Relay URL: " + defaultTunnelBaseURL + "\n",
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
		return defaultTunnelBaseURL
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
		"relay_base_url: " + defaultTunnelBaseURL + "; healthz: unavailable (dial timeout)\n",
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
	if !strings.Contains(stdout.String(), "relay_base_url: "+defaultTunnelBaseURL+"; healthz: ok") {
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
	for _, removed := range []string{"broker", "open", "close", "sessions", "pair", "devices", "revoke"} {
		if strings.Contains(stdout.String(), "  "+removed+" ") {
			t.Fatalf("daemon help = %q, did not expect removed command %q", stdout.String(), removed)
		}
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

func TestRunWithArgsPrintsPairAndWorkspaceHelp(t *testing.T) {
	setTestEnv(t)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "pair", args: []string{"tunnel", "pair", "--help"}, want: pairHelpText()},
		{name: "pair devices", args: []string{"tunnel", "pair", "devices", "--help"}, want: pairDevicesHelpText()},
		{name: "pair revoke", args: []string{"tunnel", "pair", "revoke", "--help"}, want: pairRevokeHelpText()},
		{name: "workspace", args: []string{"tunnel", "workspace", "--help"}, want: workspaceHelpText()},
		{name: "workspace open", args: []string{"tunnel", "workspace", "open", "--help"}, want: workspaceOpenHelpText()},
		{name: "workspace close", args: []string{"tunnel", "workspace", "close", "--help"}, want: workspaceCloseHelpText()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			if err := runWithArgs(tc.args, &stdout, io.Discard); err != nil {
				t.Fatalf("runWithArgs error = %v", err)
			}
			if got := stdout.String(); got != tc.want {
				t.Fatalf("stdout = %q, want help", got)
			}
		})
	}
}

func TestRunWithArgsRejectsRemovedDaemonSubcommands(t *testing.T) {
	setTestEnv(t)

	for _, args := range [][]string{
		{"tunnel", "daemon", "open"},
		{"tunnel", "daemon", "close"},
		{"tunnel", "daemon", "sessions"},
		{"tunnel", "daemon", "pair"},
		{"tunnel", "daemon", "devices"},
		{"tunnel", "daemon", "revoke", strings.Repeat("a", 64)},
		{"tunnel", "daemon", "broker", "sessions"},
	} {
		t.Run(strings.Join(args[2:], "_"), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := runWithArgs(args, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "unknown command") {
				t.Fatalf("runWithArgs(%v) error = %v, want unknown command", args, err)
			}
		})
	}
}

func TestRunPairPrintsQRCodeAndCompletesInteractiveFlow(t *testing.T) {
	setTestEnv(t)

	oldResolvePaths := resolveDaemonPaths
	oldPair := daemonPair
	oldPending := daemonPendingPairing
	oldConfirm := daemonConfirmPairing
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonPair = oldPair
		daemonPendingPairing = oldPending
		daemonConfirmPairing = oldConfirm
	})
	fingerprint := strings.Repeat("b", 64)
	resolveDaemonPaths = func() (daemon.Paths, error) { return daemon.Paths{}, nil }
	daemonPair = func(context.Context, daemon.Paths) (daemon.PairInvitation, error) {
		return daemon.PairInvitation{
			Version:           1,
			InvitationID:      "pair_123",
			CorrelationID:     "corr_123",
			DaemonFingerprint: strings.Repeat("a", 64),
			ExpiresAt:         time.Now().Add(time.Minute).Unix(),
		}, nil
	}
	daemonPendingPairing = func(context.Context, daemon.Paths) ([]daemon.PendingPairingResponse, error) {
		return []daemon.PendingPairingResponse{{
			InvitationID:       "pair_123",
			AndroidFingerprint: fingerprint,
			AndroidDisplayName: "Pixel",
			SAS:                "123456",
		}}, nil
	}
	daemonConfirmPairing = func(_ context.Context, _ daemon.Paths, invitationID, sas string) (daemon.PairingCompletion, error) {
		if invitationID != "pair_123" || sas != "123456" {
			t.Fatalf("confirm args = %q %q, want pair_123 123456", invitationID, sas)
		}
		return daemon.PairingCompletion{Device: daemon.TrustedAndroidDevice{Fingerprint: fingerprint, DisplayName: "Pixel"}}, nil
	}

	var stdout bytes.Buffer
	if err := runPair(context.Background(), strings.NewReader("123456\n"), &stdout, io.Discard, false); err != nil {
		t.Fatalf("runPair returned error: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, "Scan this QR in the mobile app to pair this computer.") {
		t.Fatalf("stdout = %q, want QR scan guidance", got)
	}
	if !strings.Contains(got, "Client: Pixel") || !strings.Contains(got, "Paired Pixel ("+fingerprint+")") {
		t.Fatalf("stdout = %q, want client and paired summary", got)
	}
	if !strings.Contains(got, qrDarkBackground) || !strings.Contains(got, qrLightBackground) {
		t.Fatalf("stdout = %q, want rendered QR background colors", got)
	}
}

func TestRunPairSanitizesDisplayNamesAndPrintsCompletionWarning(t *testing.T) {
	setTestEnv(t)

	oldResolvePaths := resolveDaemonPaths
	oldPair := daemonPair
	oldPending := daemonPendingPairing
	oldConfirm := daemonConfirmPairing
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonPair = oldPair
		daemonPendingPairing = oldPending
		daemonConfirmPairing = oldConfirm
	})
	fingerprint := strings.Repeat("c", 64)
	longName := "Pixel " + strings.Repeat("A", 80) + "\u202Ehidden"
	resolveDaemonPaths = func() (daemon.Paths, error) { return daemon.Paths{}, nil }
	daemonPair = func(context.Context, daemon.Paths) (daemon.PairInvitation, error) {
		return daemon.PairInvitation{
			Version:           1,
			InvitationID:      "pair_123",
			CorrelationID:     "corr_123",
			DaemonFingerprint: strings.Repeat("a", 64),
			ExpiresAt:         time.Now().Add(time.Minute).Unix(),
		}, nil
	}
	daemonPendingPairing = func(context.Context, daemon.Paths) ([]daemon.PendingPairingResponse, error) {
		return []daemon.PendingPairingResponse{{
			InvitationID:       "pair_123",
			AndroidFingerprint: fingerprint,
			AndroidDisplayName: longName,
			SAS:                "123456",
		}}, nil
	}
	daemonConfirmPairing = func(context.Context, daemon.Paths, string, string) (daemon.PairingCompletion, error) {
		return daemon.PairingCompletion{
			Device:  daemon.TrustedAndroidDevice{Fingerprint: fingerprint, DisplayName: longName},
			Warning: "relay connectivity event queue unavailable",
		}, nil
	}

	var stdout bytes.Buffer
	if err := runPair(context.Background(), strings.NewReader("123456\n"), &stdout, io.Discard, false); err != nil {
		t.Fatalf("runPair returned error: %v", err)
	}
	got := stdout.String()
	if strings.Contains(got, "\u202E") || strings.Contains(got, "hidden") {
		t.Fatalf("stdout = %q, want bidi controls and hidden suffix stripped from display names", got)
	}
	if !strings.Contains(got, "Client: Pixel ") || !strings.Contains(got, "...\nFingerprint:") {
		t.Fatalf("stdout = %q, want capped client display name", got)
	}
	if !strings.Contains(got, "Paired Pixel ") || !strings.Contains(got, "... ("+fingerprint+")") {
		t.Fatalf("stdout = %q, want capped paired display name", got)
	}
	if strings.Contains(got, strings.Repeat("A", 80)) {
		t.Fatalf("stdout = %q, want long display name truncated", got)
	}
	if !strings.Contains(got, "Warning: local trust changed, but relay visibility update is delayed") {
		t.Fatalf("stdout = %q, want completion warning", got)
	}
}

func TestRunPairCommandRejectsInteractiveModeWithoutTTY(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runWithIOArgs([]string{"tunnel", "pair"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "interactive pairing requires a terminal") {
		t.Fatalf("runWithIOArgs error = %v, want non-TTY pairing guidance", err)
	}
}

func TestRunPairReturnsFriendlySASMismatch(t *testing.T) {
	setTestEnv(t)

	oldResolvePaths := resolveDaemonPaths
	oldPair := daemonPair
	oldPending := daemonPendingPairing
	oldConfirm := daemonConfirmPairing
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonPair = oldPair
		daemonPendingPairing = oldPending
		daemonConfirmPairing = oldConfirm
	})
	fingerprint := strings.Repeat("b", 64)
	resolveDaemonPaths = func() (daemon.Paths, error) { return daemon.Paths{}, nil }
	daemonPair = func(context.Context, daemon.Paths) (daemon.PairInvitation, error) {
		return daemon.PairInvitation{
			Version:           1,
			InvitationID:      "pair_123",
			CorrelationID:     "corr_123",
			DaemonFingerprint: strings.Repeat("a", 64),
			ExpiresAt:         time.Now().Add(time.Minute).Unix(),
		}, nil
	}
	daemonPendingPairing = func(context.Context, daemon.Paths) ([]daemon.PendingPairingResponse, error) {
		return []daemon.PendingPairingResponse{{
			InvitationID:       "pair_123",
			AndroidFingerprint: fingerprint,
			AndroidDisplayName: "Pixel",
			SAS:                "123456",
		}}, nil
	}
	daemonConfirmPairing = func(context.Context, daemon.Paths, string, string) (daemon.PairingCompletion, error) {
		return daemon.PairingCompletion{}, daemon.ErrPairingSASMismatch
	}

	var stdout bytes.Buffer
	err := runPair(context.Background(), strings.NewReader("000000\n"), &stdout, io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "pairing code did not match") {
		t.Fatalf("runPair error = %v, want friendly pairing mismatch", err)
	}
}

func TestRunPairCancelsWhenCodeIsEmpty(t *testing.T) {
	setTestEnv(t)

	oldResolvePaths := resolveDaemonPaths
	oldPair := daemonPair
	oldPending := daemonPendingPairing
	oldConfirm := daemonConfirmPairing
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonPair = oldPair
		daemonPendingPairing = oldPending
		daemonConfirmPairing = oldConfirm
	})
	fingerprint := strings.Repeat("b", 64)
	resolveDaemonPaths = func() (daemon.Paths, error) { return daemon.Paths{}, nil }
	daemonPair = func(context.Context, daemon.Paths) (daemon.PairInvitation, error) {
		return daemon.PairInvitation{
			Version:           1,
			InvitationID:      "pair_123",
			CorrelationID:     "corr_123",
			DaemonFingerprint: strings.Repeat("a", 64),
			ExpiresAt:         time.Now().Add(time.Minute).Unix(),
		}, nil
	}
	daemonPendingPairing = func(context.Context, daemon.Paths) ([]daemon.PendingPairingResponse, error) {
		return []daemon.PendingPairingResponse{{
			InvitationID:       "pair_123",
			AndroidFingerprint: fingerprint,
			AndroidDisplayName: "Pixel",
			SAS:                "123456",
		}}, nil
	}
	daemonConfirmPairing = func(context.Context, daemon.Paths, string, string) (daemon.PairingCompletion, error) {
		t.Fatal("daemonConfirmPairing should not be called when code is empty")
		return daemon.PairingCompletion{}, nil
	}

	var stdout bytes.Buffer
	if err := runPair(context.Background(), strings.NewReader("\n"), &stdout, io.Discard, false); err != nil {
		t.Fatalf("runPair returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Pairing cancelled.") {
		t.Fatalf("stdout = %q, want pairing cancellation message", stdout.String())
	}
}

func TestRunPairReportsExpiredInvitation(t *testing.T) {
	setTestEnv(t)

	oldResolvePaths := resolveDaemonPaths
	oldPair := daemonPair
	oldPending := daemonPendingPairing
	oldPairNow := pairNow
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonPair = oldPair
		daemonPendingPairing = oldPending
		pairNow = oldPairNow
	})
	now := time.Unix(1_700_000_000, 0).UTC()
	resolveDaemonPaths = func() (daemon.Paths, error) { return daemon.Paths{}, nil }
	daemonPair = func(context.Context, daemon.Paths) (daemon.PairInvitation, error) {
		return daemon.PairInvitation{
			Version:           1,
			InvitationID:      "pair_123",
			CorrelationID:     "corr_123",
			DaemonFingerprint: strings.Repeat("a", 64),
			ExpiresAt:         now.Add(-time.Second).Unix(),
		}, nil
	}
	daemonPendingPairing = func(context.Context, daemon.Paths) ([]daemon.PendingPairingResponse, error) {
		return nil, nil
	}
	pairNow = func() time.Time { return now }

	var stdout bytes.Buffer
	if err := runPair(context.Background(), strings.NewReader("123456\n"), &stdout, io.Discard, false); err != nil {
		t.Fatalf("runPair returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Pairing invitation expired.") {
		t.Fatalf("stdout = %q, want expired invitation message", stdout.String())
	}
}

func TestRenderQRCodeDecodesOriginalPayload(t *testing.T) {
	payload := `{"version":1,"invitation_id":"pair_123","correlation_id":"corr_123","client_fingerprint":"` + strings.Repeat("a", 64) + `"}`
	rendered, err := renderQRCode(payload)
	if err != nil {
		t.Fatalf("renderQRCode returned error: %v", err)
	}
	image := terminalQRCodeImage(t, rendered, 8)
	bitmap, err := gozxing.NewBinaryBitmapFromImage(image)
	if err != nil {
		t.Fatalf("NewBinaryBitmapFromImage returned error: %v", err)
	}
	result, err := gozxingqrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if result.GetText() != payload {
		t.Fatalf("decoded payload = %q, want original payload", result.GetText())
	}
}

func TestRunPairPrintsInvitationJSONWithFlag(t *testing.T) {
	setTestEnv(t)

	oldResolvePaths := resolveDaemonPaths
	oldPair := daemonPair
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonPair = oldPair
	})
	resolveDaemonPaths = func() (daemon.Paths, error) { return daemon.Paths{}, nil }
	daemonPair = func(context.Context, daemon.Paths) (daemon.PairInvitation, error) {
		return daemon.PairInvitation{
			Version:           1,
			InvitationID:      "pair_123",
			CorrelationID:     "corr_123",
			DaemonFingerprint: strings.Repeat("a", 64),
		}, nil
	}

	var stdout bytes.Buffer
	if err := runPair(context.Background(), emptyReader{}, &stdout, io.Discard, true); err != nil {
		t.Fatalf("runPair returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"invitation_id": "pair_123"`) {
		t.Fatalf("stdout = %q, want invitation JSON", stdout.String())
	}
}

func terminalQRCodeImage(t *testing.T, rendered string, scale int) image.Image {
	t.Helper()
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("rendered QR has no rows")
	}
	firstRow := terminalQRCodeModules(t, lines[0], 0)
	modules := len(firstRow)
	img := image.NewGray(image.Rect(0, 0, modules*scale, len(lines)*scale))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetGray(x, y, color.Gray{Y: 0xff})
		}
	}
	for row, line := range lines {
		rowModules := firstRow
		if row != 0 {
			rowModules = terminalQRCodeModules(t, line, row)
		}
		if len(rowModules) != modules {
			t.Fatalf("line %d width = %d, want %d", row, len(rowModules), modules)
		}
		for col, dark := range rowModules {
			if !dark {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetGray(col*scale+dx, row*scale+dy, color.Gray{Y: 0x00})
				}
			}
		}
	}
	return img
}

func terminalQRCodeModules(t *testing.T, line string, row int) []bool {
	t.Helper()
	var modules []bool
	dark := false
	for i := 0; i < len(line); {
		switch {
		case strings.HasPrefix(line[i:], qrDarkBackground):
			dark = true
			i += len(qrDarkBackground)
		case strings.HasPrefix(line[i:], qrLightBackground):
			dark = false
			i += len(qrLightBackground)
		case strings.HasPrefix(line[i:], qrBackgroundReset):
			dark = false
			i += len(qrBackgroundReset)
		case strings.HasPrefix(line[i:], "  "):
			modules = append(modules, dark)
			i += 2
		default:
			t.Fatalf("line %d has unexpected QR token at byte %d: %q", row, i, line[i:])
		}
	}
	return modules
}

func TestRunWithArgsPairJSONCommandPrintsErrorEnvelope(t *testing.T) {
	setTestEnv(t)

	oldResolvePaths := resolveDaemonPaths
	oldDevices := daemonTrustedDevices
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonTrustedDevices = oldDevices
	})
	resolveDaemonPaths = func() (daemon.Paths, error) { return daemon.Paths{}, nil }
	daemonTrustedDevices = func(context.Context, daemon.Paths) ([]daemon.TrustedAndroidDevice, error) {
		return nil, daemon.ErrNotRunning
	}

	var stdout bytes.Buffer
	err := runWithArgs([]string{"tunnel", "pair", "devices", "--json"}, &stdout, io.Discard)
	if !errors.Is(err, daemon.ErrNotRunning) {
		t.Fatalf("runWithArgs error = %v, want ErrNotRunning", err)
	}
	var envelope daemonCommandErrorEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("error JSON unmarshal returned error: %v\n%s", err, stdout.String())
	}
	if envelope.Error.Code != "daemon_not_running" || envelope.Error.Message == "" {
		t.Fatalf("error envelope = %#v, want daemon_not_running with message", envelope)
	}
}

func TestRunWithArgsDaemonJSONCommandsWrapSetupErrors(t *testing.T) {
	setTestEnv(t)

	oldResolvePaths := resolveDaemonPaths
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
	})
	setupErr := errors.New("paths exploded")
	resolveDaemonPaths = func() (daemon.Paths, error) { return daemon.Paths{}, setupErr }

	for _, args := range [][]string{
		{"tunnel", "daemon", "start", "--json"},
		{"tunnel", "daemon", "status", "--json"},
		{"tunnel", "daemon", "doctor", "--json"},
		{"tunnel", "pair", "--json"},
	} {
		t.Run(strings.Join(args[2:], "_"), func(t *testing.T) {
			var stdout bytes.Buffer
			err := runWithArgs(args, &stdout, io.Discard)
			if !errors.Is(err, setupErr) {
				t.Fatalf("runWithArgs error = %v, want setup error", err)
			}
			var envelope daemonCommandErrorEnvelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatalf("error JSON unmarshal returned error: %v\n%s", err, stdout.String())
			}
			if envelope.Error.Code != "daemon_command_failed" || envelope.Error.Message == "" {
				t.Fatalf("error envelope = %#v, want daemon_command_failed with message", envelope)
			}
		})
	}
}

func TestRunPairDevicesPrintsTrustedDevices(t *testing.T) {
	setTestEnv(t)

	oldResolvePaths := resolveDaemonPaths
	oldDevices := daemonTrustedDevices
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonTrustedDevices = oldDevices
	})
	resolveDaemonPaths = func() (daemon.Paths, error) { return daemon.Paths{}, nil }
	daemonTrustedDevices = func(context.Context, daemon.Paths) ([]daemon.TrustedAndroidDevice, error) {
		return []daemon.TrustedAndroidDevice{{
			Fingerprint: strings.Repeat("a", 64),
			DisplayName: "Pixel",
			PairedAt:    123,
		}}, nil
	}

	var stdout bytes.Buffer
	if err := runPairDevices(context.Background(), &stdout, io.Discard, false); err != nil {
		t.Fatalf("runPairDevices returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Pixel") || !strings.Contains(stdout.String(), strings.Repeat("a", 64)) {
		t.Fatalf("stdout = %q, want trusted device row", stdout.String())
	}
	stdout.Reset()
	if err := runPairDevices(context.Background(), &stdout, io.Discard, true); err != nil {
		t.Fatalf("runPairDevices JSON returned error: %v", err)
	}
	var devices []daemon.TrustedAndroidDevice
	if err := json.Unmarshal(stdout.Bytes(), &devices); err != nil {
		t.Fatalf("devices JSON unmarshal returned error: %v\n%s", err, stdout.String())
	}
	if len(devices) != 1 || devices[0].DisplayName != "Pixel" {
		t.Fatalf("devices = %#v, want Pixel JSON", devices)
	}
}

func TestRunPairRevokePrintsRevokedFingerprint(t *testing.T) {
	setTestEnv(t)

	oldResolvePaths := resolveDaemonPaths
	oldRevoke := daemonRevokeTrustedDevice
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
		daemonRevokeTrustedDevice = oldRevoke
	})
	fingerprint := strings.Repeat("b", 64)
	resolveDaemonPaths = func() (daemon.Paths, error) { return daemon.Paths{}, nil }
	daemonRevokeTrustedDevice = func(_ context.Context, _ daemon.Paths, got string) (daemon.TrustedAndroidDevice, error) {
		if got != fingerprint {
			t.Fatalf("fingerprint = %q, want %q", got, fingerprint)
		}
		return daemon.TrustedAndroidDevice{Fingerprint: fingerprint}, nil
	}

	var stdout bytes.Buffer
	if err := runPairRevoke(context.Background(), fingerprint, &stdout, io.Discard, false); err != nil {
		t.Fatalf("runPairRevoke returned error: %v", err)
	}
	if got := stdout.String(); got != "Revoked "+fingerprint+"\n" {
		t.Fatalf("stdout = %q, want revoked fingerprint", got)
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

func TestRunWithArgsForwardsLaunchContextFromInternalFlags(t *testing.T) {
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

	err := runWithArgs([]string{"tunnel", "run", "--launch-source", "mobile", "--launch-request-id", "req-123", "codex"}, io.Discard, io.Discard)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithArgs error = %v, want %v", err, wantErr)
	}
	if fakeConnector.launchContext.Source != protocol.SessionLaunchSourceMobile {
		t.Fatalf("launchContext.Source = %q, want mobile", fakeConnector.launchContext.Source)
	}
	if fakeConnector.launchContext.RequestID != "req-123" {
		t.Fatalf("launchContext.RequestID = %q, want req-123", fakeConnector.launchContext.RequestID)
	}
	if fakeConnector.launchReady != (protocol.LaunchContext{}) {
		t.Fatalf("launchReady = %#v, want no ready signal when session start fails", fakeConnector.launchReady)
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
	oldEnsureTunnelRunDaemon := ensureTunnelRunDaemon
	t.Cleanup(func() {
		resolveLauncher = oldResolve
		prepareLocalTerminal = oldPrepare
		startSession = oldStartSession
		startLocalTerminal = oldStartLocalTerminal
		waitForExit = oldWaitForExit
		newConnector = oldNewConnector
		ensureTunnelRunDaemon = oldEnsureTunnelRunDaemon
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
	registration := &fakeLocalRegistration{}
	ensureTunnelRunDaemon = func(context.Context, string, string) (tunnelRunDaemonEnsureResult, error) {
		return tunnelRunDaemonEnsureResult{
			Paths:   daemon.Paths{SocketPath: "/tmp/daemon.sock", BrokerSocketPath: "/tmp/broker.sock"},
			Started: true,
		}, nil
	}
	newSessionRegistration = func(daemon.Paths, string, string, protocol.SessionInfo) localSessionRegistration {
		return registration
	}
	stopCalled := false
	daemonStop = func(ctx context.Context, paths daemon.Paths) error {
		stopCalled = true
		if paths.SocketPath != "/tmp/daemon.sock" {
			t.Fatalf("daemonStop paths = %#v, want auto-started daemon paths", paths)
		}
		return nil
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
	if !registration.closeCalled {
		t.Fatal("runWithArgs did not close daemon broker registration after relay failure")
	}
	if !stopCalled {
		t.Fatal("runWithArgs did not stop the daemon it auto-started after relay failure")
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
