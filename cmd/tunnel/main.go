package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/tunnel/connector"
	"yuanbohan/tunnel/internal/tunnel/daemon"
	"yuanbohan/tunnel/internal/tunnel/launcher"
	"yuanbohan/tunnel/internal/tunnel/pairingqr"
	"yuanbohan/tunnel/internal/tunnel/session"
)

const startupRelayWait = 10 * time.Second
const tunnelRunBrokerRegistrationTimeout = 5 * time.Second
const tunnelRunDaemonCleanupTimeout = 2 * time.Second

const (
	startupBannerGreen = "\x1b[92m"
	startupBannerRed   = "\x1b[31m"
	startupBannerReset = "\x1b[0m"
)

const pairDisplayNameWidth = 48
const pairPromptRows = 2

const startupBannerClear = "\r\x1b[2K"

type relayConnector interface {
	SetInitialConnectTimeout(timeout time.Duration)
	SetLaunchContext(protocol.LaunchContext)
	MarkLaunchReady(protocol.LaunchContext)
	Run(ctx context.Context)
	WaitUntilConnected(ctx context.Context, timeout time.Duration) bool
	SubscribeStateChanges() (<-chan connector.State, func())
	CurrentState() connector.State
}

type localSessionRegistration interface {
	session.OutputSink
	SetStopHandler(func())
	BindHub(hub *session.Hub)
	Run(context.Context)
	WaitUntilRegistered(context.Context) error
	Close() error
}

type tunnelRunDaemonEnsureResult struct {
	Paths   daemon.Paths
	Started bool
}

var (
	resolveLauncher      = launcher.Resolve
	prepareLocalTerminal = session.PrepareLocalTerminal
	startSession         = session.StartCommandWithInitialSinks
	startLocalTerminal   = func(ctx context.Context, local *session.LocalTerminal, hub *session.Hub) <-chan struct{} {
		return local.Start(ctx, hub)
	}
	waitForExit  = waitForProcessOrShutdown
	newConnector = func(url, token string, info protocol.SessionInfo) relayConnector {
		return connector.New(url, token, info)
	}
	newSessionRegistration = func(paths daemon.Paths, baseURL, authToken string, info protocol.SessionInfo) localSessionRegistration {
		client := daemon.NewSessionRegistrationClient(paths, info)
		client.SetExpectedDaemonContext(baseURL, authToken)
		return client
	}
	ensureTunnelRunDaemon             = ensureDaemonForTunnelRun
	collectSessionMetadata            = daemon.CollectSessionMetadata
	readSessionDeviceIdentity         = daemon.ReadDeviceIdentity
	readOrCreateSessionDeviceIdentity = daemon.ReadOrCreateDeviceIdentity
	resolveDaemonPaths                = daemon.ResolvePaths
	startDaemon                       = daemon.StartBackground
	runDaemonRuntime                  = daemon.Run
	daemonStatus                      = daemon.Status
	daemonStop                        = daemon.Stop
	daemonDoctor                      = daemon.Doctor
	daemonPair                        = daemon.Pair
	daemonPendingPairing              = daemon.PendingPairing
	daemonConfirmPairing              = daemon.ConfirmPendingPairing
	daemonTrustedDevices              = daemon.TrustedDevices
	daemonRevokeTrustedDevice         = daemon.RevokeTrustedDevice
	openDaemonWorkspace               = daemon.OpenWorkspace
	closeDaemonWorkspace              = daemon.CloseWorkspace
	resolveDoctorRelayBaseURL         = func(ctx context.Context, paths daemon.Paths) string {
		status, err := daemonStatus(ctx, paths)
		if err == nil && strings.TrimSpace(status.BaseURL) != "" {
			return status.BaseURL
		}
		baseURL, resolveErr := resolveCLIBaseURL("")
		if resolveErr == nil {
			return baseURL
		}
		return defaultTunnelBaseURL
	}
	doctorProbeRelayHealth = probeDoctorRelayHealth
	pairNow                = func() time.Time { return time.Now().UTC() }
	pairPollInterval       = time.Second
	pairSleep              = func(ctx context.Context, d time.Duration) bool {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	pairTerminalSize = pairingqr.TerminalSizeForWriter
)

func main() {
	if err := run(); err != nil {
		var usageErr usageError
		if errors.As(err, &usageErr) {
			os.Exit(2)
		}
		var exitErr exitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		log.Fatal(err)
	}
}

func run() error {
	return runWithIOArgs(os.Args, os.Stdin, os.Stdout, os.Stderr)
}

func runWithArgs(args []string, stdout, stderr io.Writer) error {
	return runWithIOArgs(args, os.Stdin, stdout, stderr)
}

func runWithIOArgs(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if legacy := legacyLauncherCommand(args[1:]); legacy != "" {
		_, _ = io.WriteString(stderr, rootHelpText())
		return guidancef(fmt.Sprintf("launcher commands now require `tunnel run <command>`; try `tunnel run %s`", legacy))
	}

	cmd := newRootCmd(defaultCommandHandlers())
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args[1:])
	if err := cmd.Execute(); err != nil {
		var usageErr usageError
		if errors.As(err, &usageErr) {
			helpText := usageErr.help
			if strings.TrimSpace(helpText) == "" {
				helpText = rootHelpText()
			}
			_, _ = io.WriteString(stderr, helpText)
			if usageErr.detail != "" {
				_, _ = io.WriteString(stderr, "\n")
				_, _ = io.WriteString(stderr, usageErr.detail)
				_, _ = io.WriteString(stderr, "\n")
			}
		}
		return err
	}
	return nil
}

func runTunnelSession(ctx context.Context, stdin io.Reader, parsed runArgs, stdout, stderr io.Writer) error {
	if err := maybeHandleStartupUpdate(ctx, stdin, stdout, stderr); err != nil {
		return err
	}

	relayURL := relayWebSocketBaseURL(parsed.BaseURL)
	resolvedAuth, err := resolveRuntimeAuth(newAuthStore(), osEnv)
	if err != nil {
		return err
	}

	command, err := resolveLauncher(parsed.Launcher, parsed.LauncherArgs)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	ctx, stopSignals := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	commandPreview := strings.TrimSpace(strings.Join(append([]string{command.Name}, command.Args...), " "))
	gitBranch := detectGitBranch(ctx, cwd)
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	platformFamily, platformID, computerName := sessionIdentityFromMetadata(collectSessionMetadata())
	launchContext := launchContextFromRunArgs(parsed)
	launchSource := protocol.SessionLaunchSourceLocal
	if launchContext.Source == protocol.SessionLaunchSourceMobile {
		launchSource = protocol.SessionLaunchSourceMobile
	}
	info := protocol.SessionInfo{
		SessionID:      sessionID,
		DeviceID:       tunnelRunSessionDeviceID(),
		Launcher:       command.Name,
		Label:          parsed.Label,
		CWD:            cwd,
		CommandPreview: commandPreview,
		GitBranch:      gitBranch,
		StartedAt:      protocol.UnixTimestamp(time.Now().UTC()),
		PlatformFamily: platformFamily,
		PlatformID:     platformID,
		ComputerName:   computerName,
		LaunchSource:   launchSource,
	}

	daemonEnsure, err := ensureTunnelRunDaemon(ctx, parsed.BaseURL, resolvedAuth.Token)
	if err != nil {
		return fmt.Errorf("daemon is required for tunnel run: %w", err)
	}
	if ctx.Err() != nil {
		stopAutoStartedDaemon(daemonEnsure)
		return nil
	}
	paths := daemonEnsure.Paths
	brokerInfo := info
	if strings.TrimSpace(brokerInfo.DeviceID) == "" {
		brokerInfo.DeviceID = sessionDeviceIDFromPaths(paths)
	}

	var (
		runningMu     sync.Mutex
		running       *session.Running
		stopRequested bool
	)
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	localRegistration := newSessionRegistration(paths, parsed.BaseURL, resolvedAuth.Token, brokerInfo)
	localRegistration.SetStopHandler(func() {
		runningMu.Lock()
		current := running
		if current == nil {
			stopRequested = true
		}
		runningMu.Unlock()
		if current != nil {
			_ = current.Close()
			return
		}
		_ = localRegistration.Close()
		cancelRun()
	})
	go localRegistration.Run(runCtx)
	registrationCtx, cancelRegistration := context.WithTimeout(runCtx, tunnelRunBrokerRegistrationTimeout)
	if err := localRegistration.WaitUntilRegistered(registrationCtx); err != nil {
		cancelRegistration()
		_ = localRegistration.Close()
		stopAutoStartedDaemon(daemonEnsure)
		if errors.Is(err, context.Canceled) || errors.Is(runCtx.Err(), context.Canceled) {
			return nil
		}
		return fmt.Errorf("daemon broker registration failed: %w; run `tunnel daemon start` and try again", err)
	}
	cancelRegistration()
	if runCtx.Err() != nil {
		_ = localRegistration.Close()
		stopAutoStartedDaemon(daemonEnsure)
		return nil
	}
	defer localRegistration.Close()

	relayInfo := brokerInfo
	relay := newConnector(relayURL, resolvedAuth.Token, relayInfo)
	relay.SetLaunchContext(launchContext)
	relay.SetInitialConnectTimeout(startupRelayWait)
	relayCtx, cancelRelay := context.WithCancel(runCtx)
	defer cancelRelay()
	go relay.Run(relayCtx)
	if !relay.WaitUntilConnected(runCtx, startupRelayWait) {
		cancelRelay()
		stopAutoStartedDaemon(daemonEnsure)
		if runCtx.Err() != nil {
			return nil
		}
		return fmt.Errorf("failed to connect to the relay server")
	}
	if runCtx.Err() != nil {
		cancelRelay()
		stopAutoStartedDaemon(daemonEnsure)
		return nil
	}

	local, err := prepareLocalTerminal()
	if err != nil {
		cancelRelay()
		stopAutoStartedDaemon(daemonEnsure)
		return err
	}
	defer local.Restore()

	sinkID, sink := local.SinkRegistration()
	initialSinks := map[string]session.OutputSink{
		sinkID:          sink,
		"daemon-broker": localRegistration,
	}

	started, err := startSession(runCtx, command.Path, command.Args, initialSinks)
	if err != nil {
		cancelRelay()
		stopAutoStartedDaemon(daemonEnsure)
		return err
	}
	runningMu.Lock()
	running = started
	shouldStop := stopRequested
	runningMu.Unlock()
	if shouldStop {
		_ = started.Close()
	}
	defer started.Close()

	localRegistration.BindHub(started.Hub)
	if !shouldStop {
		relay.MarkLaunchReady(launchContext)
	}

	if parsed.Verbose {
		fmt.Fprint(stderr, startupBanner(command.Name, sessionID))
	}

	done := startLocalTerminal(ctx, local, started.Hub)

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- started.Wait()
	}()

	return waitForExit(ctx, done, waitErr)
}

func ensureDaemonForTunnelRun(ctx context.Context, baseURL, authToken string) (tunnelRunDaemonEnsureResult, error) {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return tunnelRunDaemonEnsureResult{}, err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return tunnelRunDaemonEnsureResult{}, err
	}
	if status, err := daemonStatus(ctx, paths); err == nil && status.Running {
		if runningBaseURL := strings.TrimSpace(status.BaseURL); runningBaseURL != "" && runningBaseURL != baseURL {
			return tunnelRunDaemonEnsureResult{Paths: paths}, fmt.Errorf("daemon is running against %s; stop it before running against %s", runningBaseURL, baseURL)
		}
		if !daemon.AuthContextMatches(status, authToken) {
			return tunnelRunDaemonEnsureResult{Paths: paths}, errors.New("daemon auth context does not match this tunnel run; stop and restart the daemon")
		}
		return tunnelRunDaemonEnsureResult{Paths: paths}, nil
	}
	if strings.TrimSpace(authToken) == "" {
		return tunnelRunDaemonEnsureResult{Paths: paths}, errors.New("missing auth token")
	}
	executable, err := os.Executable()
	if err != nil {
		return tunnelRunDaemonEnsureResult{Paths: paths}, err
	}
	result, err := startDaemon(ctx, daemon.StartOptions{
		Executable: executable,
		Paths:      paths,
		BaseURL:    baseURL,
		AuthToken:  authToken,
	})
	if err != nil {
		return tunnelRunDaemonEnsureResult{Paths: paths}, fmt.Errorf("failed to start daemon: %w", err)
	}
	status := result.Status
	ensureResult := tunnelRunDaemonEnsureResult{Paths: paths, Started: !result.AlreadyRunning}
	if runningBaseURL := strings.TrimSpace(status.BaseURL); runningBaseURL != "" && runningBaseURL != baseURL {
		stopAutoStartedDaemon(ensureResult)
		return tunnelRunDaemonEnsureResult{Paths: paths}, fmt.Errorf("daemon is running against %s; stop it before running against %s", runningBaseURL, baseURL)
	}
	if !daemon.AuthContextMatches(status, authToken) {
		stopAutoStartedDaemon(ensureResult)
		return tunnelRunDaemonEnsureResult{Paths: paths}, errors.New("daemon auth context does not match this tunnel run; stop and restart the daemon")
	}
	return ensureResult, nil
}

func stopAutoStartedDaemon(result tunnelRunDaemonEnsureResult) {
	if !result.Started {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), tunnelRunDaemonCleanupTimeout)
	defer cancel()
	_ = daemonStop(ctx, result.Paths)
}

func launchContextFromRunArgs(parsed runArgs) protocol.LaunchContext {
	source := strings.TrimSpace(parsed.LaunchSource)
	requestID := strings.TrimSpace(parsed.LaunchRequestID)
	if source != protocol.SessionLaunchSourceMobile || requestID == "" {
		return protocol.LaunchContext{}
	}
	return protocol.LaunchContext{
		Source:    protocol.SessionLaunchSourceMobile,
		RequestID: requestID,
	}
}

func sessionDeviceID() string {
	paths, err := resolveDaemonPaths()
	if err != nil {
		return ""
	}
	return sessionDeviceIDFromPaths(paths)
}

func sessionDeviceIDFromPaths(paths daemon.Paths) string {
	identity, err := readSessionDeviceIdentity(paths)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(identity.DeviceID)
}

func tunnelRunSessionDeviceID() string {
	paths, err := resolveDaemonPaths()
	if err != nil {
		return ""
	}
	identity, err := readOrCreateSessionDeviceIdentity(paths)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(identity.DeviceID)
}

func sessionIdentityFromMetadata(metadata daemon.DeviceMetadata) (platformFamily, platformID, computerName string) {
	computerName = strings.TrimSpace(metadata.DisplayName)
	if computerName == "" {
		computerName = strings.TrimSpace(metadata.Hostname)
	}

	platformFamily = strings.TrimSpace(metadata.PlatformFamily)
	switch platformFamily {
	case daemon.PlatformFamilyLinux, daemon.PlatformFamilyMacOS:
	default:
		platformFamily = ""
	}

	platformID = strings.TrimSpace(metadata.PlatformID)
	if platformFamily == "" {
		platformID = ""
	}

	return platformFamily, platformID, computerName
}

func legacyLauncherCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}
	first := strings.TrimSpace(args[0])
	if first == "" || strings.HasPrefix(first, "-") {
		return ""
	}
	switch first {
	case "run", "auth", "session", "daemon", "pair", "workspace", "update", "rollback", "help", "version":
		return ""
	default:
		return first
	}
}

func waitForProcessOrShutdown(ctx context.Context, localDone <-chan struct{}, waitErr <-chan error) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-localDone:
			localDone = nil
		case err := <-waitErr:
			return err
		}
	}
}

func startupBanner(launcherName, sessionID string) string {
	return fmt.Sprintf(
		"%s%s▶ tunnel %s — session %s; relay server connected%s\r",
		startupBannerClear,
		startupBannerGreen,
		launcherName,
		sessionID,
		startupBannerReset,
	)
}

func resolveCLIBaseURL(raw string) (string, error) {
	resolved := strings.TrimSpace(raw)
	if resolved == "" {
		resolved = strings.TrimSpace(os.Getenv(tunnelBaseURLEnv))
	}
	if resolved == "" {
		resolved = defaultTunnelBaseURL
	}
	return validateBaseURL(resolved)
}

func resolveDaemonAuth() (resolvedAuth, error) {
	return resolveRuntimeAuth(newAuthStore(), osEnv)
}

func authUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no auth token available")
}

func ensureDaemonPlatformSupported() error {
	switch runtime.GOOS {
	case "darwin", "linux":
		return nil
	default:
		return fmt.Errorf("daemon unsupported on platform: %s", runtime.GOOS)
	}
}

type daemonStartOptions struct {
	JSON bool
}

func runDaemonStart(ctx context.Context, rawBaseURL string, stdout, stderr io.Writer) error {
	return runDaemonStartWithOptions(ctx, rawBaseURL, stdout, stderr, daemonStartOptions{})
}

func runDaemonStartWithOptions(ctx context.Context, rawBaseURL string, stdout, stderr io.Writer, options daemonStartOptions) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	baseURL, err := resolveCLIBaseURL(rawBaseURL)
	if err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	if status, err := daemonStatus(ctx, paths); err == nil && status.Running {
		if runningBaseURL := strings.TrimSpace(status.BaseURL); runningBaseURL != "" && runningBaseURL != baseURL {
			return fmt.Errorf("daemon already running against %s; stop it before starting with %s", runningBaseURL, baseURL)
		}
		auth, err := resolveDaemonAuth()
		if err != nil && !authUnavailable(err) {
			return err
		}
		if err == nil && !daemon.AuthContextMatches(status, auth.Token) {
			return errors.New("daemon already running with a different auth context; stop it with `tunnel daemon stop`, then run `tunnel daemon start` again")
		}
		if options.JSON {
			return writeIndentedJSON(stdout, status)
		}
		_, _ = fmt.Fprintf(stdout, "daemon already running (pid=%d device_id=%s)\n", status.PID, status.DeviceID)
		writeLaunchHealthWarning(stdout, status)
		return nil
	}
	auth, err := resolveDaemonAuth()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	result, err := startDaemon(ctx, daemon.StartOptions{
		Executable: executable,
		Paths:      paths,
		BaseURL:    baseURL,
		AuthToken:  auth.Token,
	})
	if err != nil {
		return err
	}
	if result.AlreadyRunning {
		if runningBaseURL := strings.TrimSpace(result.Status.BaseURL); runningBaseURL != "" && runningBaseURL != baseURL {
			return fmt.Errorf("daemon already running against %s; stop it before starting with %s", runningBaseURL, baseURL)
		}
		if !daemon.AuthContextMatches(result.Status, auth.Token) {
			return errors.New("daemon already running with a different auth context; stop it with `tunnel daemon stop`, then run `tunnel daemon start` again")
		}
		if options.JSON {
			return writeIndentedJSON(stdout, result.Status)
		}
		_, _ = fmt.Fprintf(stdout, "daemon already running (pid=%d device_id=%s)\n", result.Status.PID, result.Status.DeviceID)
		writeLaunchHealthWarning(stdout, result.Status)
		return nil
	}
	if options.JSON {
		return writeIndentedJSON(stdout, result.Status)
	}
	_, _ = fmt.Fprintf(stdout, "daemon started (pid=%d device_id=%s)\n", result.Status.PID, result.Status.DeviceID)
	if result.PreservedSessions > 0 {
		_, _ = fmt.Fprintf(stdout, "preserved %d existing workspace sessions\n", result.PreservedSessions)
	}
	writeLaunchHealthWarning(stdout, result.Status)
	return nil
}

func runDaemonStatus(ctx context.Context, stdout, stderr io.Writer) error {
	return runDaemonStatusWithOptions(ctx, stdout, stderr, false)
}

func runDaemonStatusWithOptions(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	status, err := daemonStatus(ctx, paths)
	if err != nil {
		if errors.Is(err, daemon.ErrNotRunning) {
			if jsonOutput {
				return writeIndentedJSON(stdout, daemon.StatusInfo{Running: false})
			}
			_, _ = io.WriteString(stdout, "running: false\n")
			_, _ = io.WriteString(stdout, "status: not started\n")
			_, _ = io.WriteString(stdout, "hint: start it with `tunnel daemon start`\n")
			return nil
		}
		return err
	}
	if jsonOutput {
		return writeIndentedJSON(stdout, status)
	}
	renderDaemonStatus(stdout, status)
	return nil
}

func writeLaunchHealthWarning(stdout io.Writer, status daemon.StatusInfo) {
	if strings.TrimSpace(status.LaunchHealth) != daemon.LaunchHealthDegraded {
		return
	}
	detail := daemonLaunchHealthSummary(status)
	if failure := strings.TrimSpace(status.LastFailure); failure != "" {
		detail = fmt.Sprintf("%s (%s)", detail, failure)
	}
	_, _ = fmt.Fprintf(stdout, "warning: launch readiness is %s; remote launch may be unavailable\n", detail)
}

func writeIndentedJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type daemonCommandErrorEnvelope struct {
	Error daemonCommandError `json:"error"`
}

type daemonCommandError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func runDaemonJSONCommand(stdout io.Writer, run func() error) error {
	err := run()
	if err == nil {
		return nil
	}
	var exit exitError
	if errors.As(err, &exit) {
		return err
	}
	_ = writeIndentedJSON(stdout, daemonCommandErrorEnvelope{
		Error: daemonCommandError{
			Code:    daemonCommandErrorCode(err),
			Message: err.Error(),
		},
	})
	return err
}

func runSessionJSONCommand(stdout io.Writer, run func() error) error {
	err := run()
	if err == nil {
		return nil
	}
	var exit exitError
	if errors.As(err, &exit) {
		return err
	}
	_ = writeIndentedJSON(stdout, daemonCommandErrorEnvelope{
		Error: daemonCommandError{
			Code:    sessionCommandErrorCode(err),
			Message: err.Error(),
		},
	})
	return err
}

func sessionCommandErrorCode(err error) string {
	message := strings.TrimSpace(err.Error())
	switch {
	case errors.Is(err, daemon.ErrNotRunning) || strings.Contains(message, daemon.ErrNotRunning.Error()):
		return "daemon_not_running"
	case errors.Is(err, daemon.ErrSessionNotFound) || strings.Contains(message, daemon.ErrSessionNotFound.Error()):
		return "session_not_found"
	default:
		return "session_command_failed"
	}
}

func daemonCommandErrorCode(err error) string {
	message := strings.TrimSpace(err.Error())
	switch {
	case errors.Is(err, daemon.ErrNotRunning) || strings.Contains(message, daemon.ErrNotRunning.Error()):
		return "daemon_not_running"
	case errors.Is(err, daemon.ErrPairingInvitationNotFound) || message == daemon.ErrPairingInvitationNotFound.Error():
		return "pairing_invitation_not_found"
	case errors.Is(err, daemon.ErrPairingInvitationExpired) || message == daemon.ErrPairingInvitationExpired.Error():
		return "pairing_invitation_expired"
	case errors.Is(err, daemon.ErrPairingInvitationConsumed) || message == daemon.ErrPairingInvitationConsumed.Error():
		return "pairing_invitation_consumed"
	case errors.Is(err, daemon.ErrPairingSASMismatch) || message == daemon.ErrPairingSASMismatch.Error():
		return "pairing_sas_mismatch"
	case errors.Is(err, daemon.ErrTrustedDeviceNotFound) || message == daemon.ErrTrustedDeviceNotFound.Error():
		return "trusted_device_not_found"
	case errors.Is(err, daemon.ErrInvalidAndroidFingerprint) || message == daemon.ErrInvalidAndroidFingerprint.Error():
		return "invalid_client_fingerprint"
	case message == "relay connectivity event queue unavailable":
		return "connectivity_event_queue_unavailable"
	default:
		return "daemon_command_failed"
	}
}

func renderDaemonStatus(stdout io.Writer, status daemon.StatusInfo) {
	color := doctorColorEnabled(stdout)

	_, _ = io.WriteString(stdout, "Tunnel Daemon Status\n")
	_, _ = fmt.Fprintf(stdout, "%s\n", daemonStatusHeadline("Status", daemonRunningSummary(status), daemonRunningState(status), color))
	_, _ = fmt.Fprintf(stdout, "%s\n", daemonStatusHeadline("Relay", daemonRelaySummary(status), daemonRelayState(status), color))
	_, _ = fmt.Fprintf(stdout, "%s\n\n", daemonStatusHeadline("Launch Readiness", daemonLaunchHealthSummary(status), daemonLaunchHealthState(status), color))

	_, _ = fmt.Fprintf(stdout, "%s Device: %s\n", doctorStyled("🖥️", "36", color), daemonDisplayValue(status.DisplayName, "unknown"))
	_, _ = fmt.Fprintf(stdout, "%s Device ID: %s\n", doctorStyled("🆔", "36", color), daemonDisplayValue(status.DeviceID, "unknown"))
	_, _ = fmt.Fprintf(stdout, "%s Host: %s\n", doctorStyled("🌐", "36", color), daemonHostSummary(status))
	_, _ = fmt.Fprintf(stdout, "%s PID: %s\n", doctorStyled("⚙️", "36", color), daemonPIDSummary(status))
	_, _ = fmt.Fprintf(stdout, "%s Relay URL: %s\n", doctorStyled("🔗", "36", color), daemonRelayURLSummary(status))
	_, _ = fmt.Fprintf(stdout, "%s Workspace: %s\n", doctorStyled("🧰", "36", color), daemonWorkspaceSummary(status))
	_, _ = fmt.Fprintf(stdout, "%s Last Launch Failure: %s\n", doctorStyled("📝", "36", color), daemonLastFailureSummary(status))
	_, _ = fmt.Fprintf(stdout, "%s Connectivity Path: %s\n", doctorStyled("📡", "36", color), daemonConnectivityPathSummary(status))
	_, _ = fmt.Fprintf(stdout, "%s Last Connectivity Failure: %s\n", doctorStyled("🧭", "36", color), daemonConnectivityFailureSummary(status))
}

func daemonStatusHeadline(label, summary string, state string, color bool) string {
	icon, code := daemonStateIcon(state)
	return fmt.Sprintf("%s %s: %s", doctorStyled(icon, code, color), label, summary)
}

func daemonStateIcon(state string) (string, string) {
	switch state {
	case daemon.CheckStatusOK:
		return "✅", "32"
	case daemon.CheckStatusWarn:
		return "⚠️", "33"
	default:
		return "❌", "31"
	}
}

func daemonRunningState(status daemon.StatusInfo) string {
	if status.Running {
		return daemon.CheckStatusOK
	}
	return daemon.CheckStatusFail
}

func daemonRelayState(status daemon.StatusInfo) string {
	if status.RelayConnected {
		return daemon.CheckStatusOK
	}
	return daemon.CheckStatusWarn
}

func daemonLaunchHealthState(status daemon.StatusInfo) string {
	switch strings.TrimSpace(status.LaunchHealth) {
	case daemon.LaunchHealthHealthy:
		return daemon.CheckStatusOK
	case daemon.LaunchHealthDegraded:
		return daemon.CheckStatusWarn
	default:
		return daemon.CheckStatusWarn
	}
}

func daemonRunningSummary(status daemon.StatusInfo) string {
	if status.Running {
		return "running"
	}
	return "stopped"
}

func daemonRelaySummary(status daemon.StatusInfo) string {
	if status.RelayConnected {
		return "connected"
	}
	if status.Running {
		return "disconnected"
	}
	return "inactive"
}

func daemonLaunchHealthSummary(status daemon.StatusInfo) string {
	switch strings.TrimSpace(status.LaunchHealth) {
	case daemon.LaunchHealthHealthy:
		return "ready"
	case daemon.LaunchHealthDegraded:
		return "degraded"
	case "":
		return "unknown"
	default:
		return status.LaunchHealth
	}
}

func daemonHostSummary(status daemon.StatusInfo) string {
	host := daemonDisplayValue(status.Hostname, "unknown")
	platform := strings.TrimSpace(status.PlatformFamily)
	if platform == "" {
		platform = strings.TrimSpace(status.PlatformID)
	}
	if platform == "" {
		return host
	}
	return fmt.Sprintf("%s (%s)", host, platform)
}

func daemonPIDSummary(status daemon.StatusInfo) string {
	if status.PID <= 0 {
		return "unknown"
	}
	return strconv.Itoa(status.PID)
}

func daemonRelayURLSummary(status daemon.StatusInfo) string {
	if baseURL := strings.TrimSpace(status.BaseURL); baseURL != "" {
		return baseURL
	}
	return defaultTunnelBaseURL
}

func daemonWorkspaceSummary(status daemon.StatusInfo) string {
	if strings.TrimSpace(status.WorkspaceBackend) != "" {
		return status.WorkspaceBackend
	}
	return "tmux"
}

func daemonLastFailureSummary(status daemon.StatusInfo) string {
	if strings.TrimSpace(status.LastFailure) == "" {
		return "none"
	}
	return status.LastFailure
}

func daemonConnectivityPathSummary(status daemon.StatusInfo) string {
	if strings.TrimSpace(status.LastConnectivityPath) == "" {
		return "unknown"
	}
	return status.LastConnectivityPath
}

func daemonConnectivityFailureSummary(status daemon.StatusInfo) string {
	if strings.TrimSpace(status.LastConnectivityFailure) == "" {
		return "none"
	}
	return status.LastConnectivityFailure
}

func daemonDisplayValue(value, fallback string) string {
	return terminalDisplayValue(value, fallback)
}

func runDaemonStop(ctx context.Context, stdout, stderr io.Writer) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	if err := daemonStop(ctx, paths); err != nil {
		if errors.Is(err, daemon.ErrNotRunning) {
			_, _ = io.WriteString(stdout, "daemon already stopped\n")
			return nil
		}
		return err
	}
	_, _ = io.WriteString(stdout, "daemon stopped\n")
	return nil
}

func runWorkspaceOpen(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	if err := openDaemonWorkspace(ctx, paths, stdin, stdout, stderr); err != nil {
		if errors.Is(err, daemon.ErrTmuxNotFound) {
			return errors.New(daemonTmuxInstallGuidance())
		}
		if errors.Is(err, daemon.ErrNoWorkspaceSessions) {
			_, _ = io.WriteString(stdout, "no workspace sessions yet; start one from the mobile app first\n")
			return nil
		}
		return err
	}
	return nil
}

func runWorkspaceClose(ctx context.Context, stdout, stderr io.Writer) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	if err := closeDaemonWorkspace(ctx, paths); err != nil {
		if errors.Is(err, daemon.ErrTmuxNotFound) {
			return errors.New(daemonTmuxInstallGuidance())
		}
		if errors.Is(err, daemon.ErrNoOpenWorkspace) {
			_, _ = io.WriteString(stdout, "no open Tunnel workspace view to close\n")
			return nil
		}
		return err
	}
	_, _ = io.WriteString(stdout, "Tunnel workspace view closed\n")
	return nil
}

func runPair(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, jsonOutput bool) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	invitation, err := daemonPair(ctx, paths)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeIndentedJSON(stdout, invitation)
	}
	qrPayload, err := pairingqr.CompactPayload(pairingqr.CompactInvitation{
		Version:         invitation.Version,
		InvitationID:    invitation.InvitationID,
		CorrelationID:   invitation.CorrelationID,
		Nonce:           invitation.Nonce,
		DeviceID:        invitation.DeviceID,
		DisplayName:     invitation.DisplayName,
		DaemonPublicKey: invitation.DaemonPublicKey,
		ExpiresAt:       invitation.ExpiresAt,
		Signature:       invitation.Signature,
	})
	if err != nil {
		return err
	}
	qr, err := pairingqr.RenderTerminal(qrPayload)
	if err != nil {
		return err
	}
	_, _ = io.WriteString(stdout, "Scan this QR in the mobile app to pair this computer.\n\n")
	if warning := pairQRSizeWarning(qr, stdout); warning != "" {
		_, _ = io.WriteString(stdout, warning)
		_, _ = io.WriteString(stdout, "\n")
	}
	_, _ = io.WriteString(stdout, qr.Output)
	_, _ = fmt.Fprintf(stdout, "\nWaiting for a client response. This invitation expires at %s.\n", time.Unix(invitation.ExpiresAt, 0).Format(time.RFC3339))
	response, err := waitForPairingResponse(ctx, paths, invitation)
	if err != nil {
		if errors.Is(err, daemon.ErrPairingInvitationExpired) {
			_, _ = io.WriteString(stdout, "Pairing invitation expired. Run `tunnel pair` again to create a new QR code.\n")
			return nil
		}
		return err
	}
	_, _ = fmt.Fprintf(stdout, "\nClient: %s\n", pairingDisplayName(response.AndroidDisplayName, "unknown"))
	_, _ = fmt.Fprintf(stdout, "Fingerprint: %s\n", response.AndroidFingerprint)
	_, _ = io.WriteString(stdout, "Enter the 6-digit pairing code shown on the client: ")
	sas, err := readPairingSAS(stdin)
	if err != nil {
		return err
	}
	if sas == "" {
		_, _ = io.WriteString(stdout, "Pairing cancelled.\n")
		return nil
	}
	completion, err := daemonConfirmPairing(ctx, paths, response.InvitationID, sas)
	if err != nil {
		if errors.Is(err, daemon.ErrPairingSASMismatch) {
			return fmt.Errorf("pairing code did not match; run `tunnel pair` again")
		}
		return err
	}
	name := pairingDisplayName(completion.Device.DisplayName, "client")
	_, _ = fmt.Fprintf(stdout, "Paired %s (%s)\n", name, completion.Device.Fingerprint)
	if warning := pairingWarningText(completion.Warning); warning != "" {
		_, _ = fmt.Fprintf(stdout, "Warning: %s\n", warning)
	}
	return nil
}

func pairInteractiveTerminal(stdin io.Reader, stdout io.Writer) bool {
	inFile, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	outFile, ok := stdout.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(inFile.Fd())) && term.IsTerminal(int(outFile.Fd()))
}

func pairingDisplayName(value, fallback string) string {
	return truncateCell(terminalDisplayValue(value, fallback), pairDisplayNameWidth)
}

func pairingWarningText(warning string) string {
	warning = strings.TrimSpace(warning)
	if warning == "" {
		return ""
	}
	if warning == "relay connectivity event queue unavailable" {
		return "local trust changed, but relay visibility update is delayed; daemon reconnect will refresh mobile visibility"
	}
	return terminalDisplayValue(warning, "pairing completed with a warning")
}

func waitForPairingResponse(ctx context.Context, paths daemon.Paths, invitation daemon.PairInvitation) (daemon.PendingPairingResponse, error) {
	invitationID := strings.TrimSpace(invitation.InvitationID)
	for {
		pending, err := daemonPendingPairing(ctx, paths)
		if err != nil {
			return daemon.PendingPairingResponse{}, err
		}
		for _, response := range pending {
			if strings.TrimSpace(response.InvitationID) == invitationID {
				return response, nil
			}
		}
		if pairNow().Unix() >= invitation.ExpiresAt {
			return daemon.PendingPairingResponse{}, daemon.ErrPairingInvitationExpired
		}
		interval := pairPollInterval
		if interval <= 0 {
			interval = 10 * time.Millisecond
		}
		if !pairSleep(ctx, interval) {
			if ctx.Err() != nil {
				return daemon.PendingPairingResponse{}, ctx.Err()
			}
			return daemon.PendingPairingResponse{}, errors.New("pairing wait cancelled")
		}
	}
}

func readPairingSAS(stdin io.Reader) (string, error) {
	if stdin == nil {
		return "", nil
	}
	reader := bufio.NewReader(stdin)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) != 6 {
		return "", errors.New("pairing code must be 6 digits")
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return "", errors.New("pairing code must be 6 digits")
		}
	}
	return value, nil
}

func pairQRSizeWarning(qr pairingqr.TerminalQRCode, stdout io.Writer) string {
	size, ok := pairTerminalSize(stdout)
	if !ok {
		return ""
	}
	return pairingqr.SizeWarning(qr, size, pairPromptRows)
}

func runPairDevices(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	devices, err := daemonTrustedDevices(ctx, paths)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeIndentedJSON(stdout, devices)
	}
	if len(devices) == 0 {
		_, _ = io.WriteString(stdout, "No paired devices.\n")
		return nil
	}
	columns := []tableColumn{
		{title: "Fingerprint", width: 64},
		{title: "Name", width: 22},
		{title: "Paired", width: 16},
	}
	rows := make([][]string, 0, len(devices))
	for _, device := range devices {
		rows = append(rows, []string{
			device.Fingerprint,
			daemonDisplayValue(device.DisplayName, "unknown"),
			formatPairingTimestamp(device.PairedAt),
		})
	}
	renderTable(stdout, columns, rows)
	return nil
}

func formatPairingTimestamp(unixSeconds int64) string {
	if unixSeconds <= 0 {
		return "-"
	}
	return time.Unix(unixSeconds, 0).Local().Format("2006-01-02 15:04")
}

func runPairRevoke(ctx context.Context, fingerprint string, stdout, stderr io.Writer, jsonOutput bool) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	device, err := daemonRevokeTrustedDevice(ctx, paths, fingerprint)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeIndentedJSON(stdout, device)
	}
	_, _ = fmt.Fprintf(stdout, "Revoked %s\n", device.Fingerprint)
	if warning := pairingWarningText(device.Warning); warning != "" {
		_, _ = fmt.Fprintf(stdout, "Warning: %s\n", warning)
	}
	return nil
}

func runDaemonDoctor(ctx context.Context, stdout, stderr io.Writer) error {
	return runDaemonDoctorWithOptions(ctx, stdout, stderr, false)
}

func runDaemonDoctorWithOptions(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	report, err := daemonDoctor(ctx, paths)
	if err != nil {
		return err
	}
	report = augmentDaemonDoctorReport(ctx, paths, report)
	if jsonOutput {
		if err := writeIndentedJSON(stdout, report); err != nil {
			return err
		}
		if code := report.ExitCode(); code != 0 {
			return exitError{code: code}
		}
		return nil
	}
	renderDaemonDoctor(stdout, report)
	if code := report.ExitCode(); code != 0 {
		return exitError{code: code}
	}
	return nil
}

func augmentDaemonDoctorReport(ctx context.Context, paths daemon.Paths, report daemon.DoctorReport) daemon.DoctorReport {
	authCheck := daemonDoctorAuthCheck(report)
	relayServerCheck := daemonDoctorRelayServerCheck(ctx, paths)
	checks := append([]daemon.DoctorCheck(nil), report.Checks...)
	insertAt := len(checks)
	for idx, check := range checks {
		if check.Name == "daemon process" {
			insertAt = idx + 1
			break
		}
	}
	checks = append(checks, daemon.DoctorCheck{})
	copy(checks[insertAt+1:], checks[insertAt:])
	checks[insertAt] = authCheck
	report.Checks = upsertDoctorCheck(daemon.DoctorReport{Checks: checks}, relayServerCheck).Checks
	return report
}

func upsertDoctorCheck(report daemon.DoctorReport, replacement daemon.DoctorCheck) daemon.DoctorReport {
	for idx, check := range report.Checks {
		if check.Name == replacement.Name {
			report.Checks[idx] = replacement
			return report
		}
	}
	report.Checks = append(report.Checks, replacement)
	return report
}

func daemonDoctorRelayServerCheck(ctx context.Context, paths daemon.Paths) daemon.DoctorCheck {
	baseURL := strings.TrimSpace(resolveDoctorRelayBaseURL(ctx, paths))
	if baseURL == "" {
		baseURL = defaultTunnelBaseURL
	}
	if err := doctorProbeRelayHealth(ctx, baseURL); err != nil {
		return daemon.DoctorCheck{
			Name:   "relay server",
			Status: daemon.CheckStatusWarn,
			Detail: fmt.Sprintf("relay_base_url: %s; healthz: unavailable (%v)", baseURL, err),
		}
	}
	return daemon.DoctorCheck{
		Name:   "relay server",
		Status: daemon.CheckStatusOK,
		Detail: fmt.Sprintf("relay_base_url: %s; healthz: ok", baseURL),
	}
}

func daemonDoctorAuthCheck(report daemon.DoctorReport) daemon.DoctorCheck {
	running := false
	for _, check := range report.Checks {
		if check.Name == "daemon process" && check.Status == daemon.CheckStatusOK {
			running = true
			break
		}
	}

	auth, err := resolveRuntimeAuth(newAuthStore(), osEnv)
	if err == nil {
		switch auth.Source {
		case authSourceEnv:
			return daemon.DoctorCheck{
				Name:   "auth token",
				Status: daemon.CheckStatusOK,
				Detail: "relay auth token is available from `TUNNEL_AUTH_TOKEN`",
			}
		case authSourceFile:
			return daemon.DoctorCheck{
				Name:   "auth token",
				Status: daemon.CheckStatusOK,
				Detail: "relay auth token is available from saved local login",
			}
		default:
			return daemon.DoctorCheck{
				Name:   "auth token",
				Status: daemon.CheckStatusOK,
				Detail: "relay auth token is available",
			}
		}
	}

	if running {
		return daemon.DoctorCheck{
			Name:   "auth token",
			Status: daemon.CheckStatusWarn,
			Detail: "daemon is already running, but no local auth token is available for the next reconnect or restart",
		}
	}

	return daemon.DoctorCheck{
		Name:   "auth token",
		Status: daemon.CheckStatusFail,
		Detail: "no local auth token is available, so the daemon cannot authenticate to the relay",
	}
}

func renderDaemonDoctor(stdout io.Writer, report daemon.DoctorReport) {
	color := doctorColorEnabled(stdout)
	failCount, warnCount, okCount := doctorStatusCounts(report)

	_, _ = io.WriteString(stdout, "Tunnel Daemon Doctor\n")
	_, _ = fmt.Fprintf(stdout, "Status: %s (%d fail, %d warn, %d ok)\n\n",
		doctorOverallStatus(report), failCount, warnCount, okCount)

	for _, check := range report.Checks {
		icon, label := doctorDisplayParts(check, color)
		_, _ = fmt.Fprintf(stdout, "%s %s\n", icon, label)
		_, _ = fmt.Fprintf(stdout, "   %s\n\n", check.Detail)
	}
}

func doctorOverallStatus(report daemon.DoctorReport) string {
	if report.ExitCode() == 0 {
		return "ready for remote launch"
	}

	failCount, warnCount, _ := doctorStatusCounts(report)
	switch {
	case failCount > 0:
		return "not ready for remote launch"
	case warnCount > 0:
		return "partially ready for remote launch"
	default:
		return "unknown"
	}
}

func doctorStatusCounts(report daemon.DoctorReport) (failCount, warnCount, okCount int) {
	for _, check := range report.Checks {
		switch check.Status {
		case daemon.CheckStatusFail:
			failCount++
		case daemon.CheckStatusWarn:
			warnCount++
		case daemon.CheckStatusOK:
			okCount++
		}
	}
	return failCount, warnCount, okCount
}

func doctorDisplayParts(check daemon.DoctorCheck, color bool) (icon string, label string) {
	label = doctorCheckLabel(check.Name)
	switch check.Status {
	case daemon.CheckStatusOK:
		return doctorStyled("✅", "32", color), doctorStyled(label, "32", color)
	case daemon.CheckStatusWarn:
		return doctorStyled("⚠️", "33", color), doctorStyled(label, "33", color)
	default:
		return doctorStyled("❌", "31", color), doctorStyled(label, "31", color)
	}
}

func doctorCheckLabel(name string) string {
	switch name {
	case "daemon process":
		return "Daemon"
	case "auth token":
		return "Auth Token"
	case "relay server":
		return "Relay Server"
	case "relay connectivity":
		return "Relay Connection"
	case "tmux":
		return "Tmux"
	case "workspace":
		return "Workspace"
	case "daemon config":
		return "Config"
	case "last launch failure":
		return "Last Launch"
	default:
		return name
	}
}

func daemonTmuxInstallGuidance() string {
	switch runtime.GOOS {
	case "darwin":
		return "tmux is required for mobile-created Tunnel workspaces; install it with `brew install tmux` and try again"
	case "linux":
		switch detectLinuxDistroID() {
		case "ubuntu", "debian":
			return "tmux is required for mobile-created Tunnel workspaces; install it with `sudo apt install tmux` and try again"
		case "fedora":
			return "tmux is required for mobile-created Tunnel workspaces; install it with `sudo dnf install tmux` and try again"
		case "centos", "rhel", "rocky", "almalinux":
			return "tmux is required for mobile-created Tunnel workspaces; install it with `sudo yum install tmux` and try again"
		case "arch":
			return "tmux is required for mobile-created Tunnel workspaces; install it with `sudo pacman -S tmux` and try again"
		default:
			return "tmux is required for mobile-created Tunnel workspaces; install `tmux` with your system package manager and try again"
		}
	default:
		return "tmux is required for mobile-created Tunnel workspaces; install `tmux` and try again"
	}
}

func detectLinuxDistroID() string {
	payload, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ID=") {
			continue
		}
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "ID=")), `"`)
	}
	return ""
}

func doctorStyled(text, code string, enabled bool) string {
	if !enabled {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func doctorColorEnabled(w io.Writer) bool {
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func probeDoctorRelayHealth(ctx context.Context, baseURL string) error {
	healthURL, err := doctorRelayHealthURL(baseURL)
	if err != nil {
		return err
	}

	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP %d", resp.StatusCode)
	}
	return nil
}

func doctorRelayHealthURL(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported base URL scheme: %s", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/healthz"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func runDaemonInternal(ctx context.Context, rawBaseURL string, stdout, stderr io.Writer) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	baseURL, err := resolveCLIBaseURL(rawBaseURL)
	if err != nil {
		return err
	}
	auth, err := resolveDaemonAuth()
	if err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var readyFile *os.File
	var readyWriter io.Writer
	if rawFD := strings.TrimSpace(os.Getenv("TUNNEL_DAEMON_READY_FD")); rawFD != "" {
		if fd, parseErr := strconv.Atoi(rawFD); parseErr == nil {
			readyFile = os.NewFile(uintptr(fd), "daemon-ready")
			readyWriter = readyFile
		}
	}
	if readyFile != nil {
		defer readyFile.Close()
	}
	if err := runDaemonRuntime(ctx, daemon.RuntimeOptions{
		Paths:     paths,
		BaseURL:   baseURL,
		AuthToken: auth.Token,
	}, readyWriter); err != nil {
		if readyWriter != nil {
			_, _ = io.WriteString(readyWriter, "error:"+err.Error()+"\n")
		}
		return err
	}
	return nil
}
