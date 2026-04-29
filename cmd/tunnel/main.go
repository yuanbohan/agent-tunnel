package main

import (
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/tunnel/connector"
	"yuanbohan/tunnel/internal/tunnel/daemon"
	"yuanbohan/tunnel/internal/tunnel/launcher"
	"yuanbohan/tunnel/internal/tunnel/session"
)

const startupRelayWait = 10 * time.Second
const tunnelRunDaemonAutoTimeout = 3 * time.Second

const (
	startupBannerGreen = "\x1b[92m"
	startupBannerRed   = "\x1b[31m"
	startupBannerReset = "\x1b[0m"
)

const startupBannerClear = "\r\x1b[2K"

type relayConnector interface {
	session.OutputSink
	SetInitialSize(cols, rows int)
	SetInitialConnectTimeout(timeout time.Duration)
	SetLaunchContext(protocol.LaunchContext)
	SetStopHandler(func())
	BindHub(hub *session.Hub)
	Run(ctx context.Context)
	WaitUntilConnected(ctx context.Context, timeout time.Duration) bool
	SubscribeStateChanges() (<-chan connector.State, func())
	CurrentState() connector.State
}

type localSessionRegistration interface {
	session.OutputSink
	Run(context.Context)
	Close() error
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
	ensureTunnelRunDaemon     = ensureDaemonForTunnelRun
	collectSessionMetadata    = daemon.CollectSessionMetadata
	readSessionDeviceIdentity = daemon.ReadDeviceIdentity
	resolveDaemonPaths        = daemon.ResolvePaths
	startDaemon               = daemon.StartBackground
	runDaemonRuntime          = daemon.Run
	daemonStatus              = daemon.Status
	daemonStop                = daemon.Stop
	daemonDoctor              = daemon.Doctor
	daemonPair                = daemon.Pair
	daemonPendingPairing      = daemon.PendingPairing
	daemonConfirmPairing      = daemon.ConfirmPendingPairing
	daemonTrustedDevices      = daemon.TrustedDevices
	daemonRevokeTrustedDevice = daemon.RevokeTrustedDevice
	daemonBrokerSessions      = daemon.BrokerSessions
	openDaemonWorkspace       = daemon.OpenWorkspace
	closeDaemonWorkspace      = daemon.CloseWorkspace
	listDaemonWorkspace       = daemon.ListWorkspaceSessions
	resolveDoctorRelayBaseURL = func(ctx context.Context, paths daemon.Paths) string {
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
		DeviceID:       sessionDeviceID(),
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

	relay := newConnector(relayURL, resolvedAuth.Token, info)
	relay.SetLaunchContext(launchContext)
	relay.SetInitialConnectTimeout(startupRelayWait)
	var (
		runningMu     sync.Mutex
		running       *session.Running
		stopRequested bool
	)
	relay.SetStopHandler(func() {
		runningMu.Lock()
		current := running
		if current == nil {
			stopRequested = true
		}
		runningMu.Unlock()
		if current != nil {
			_ = current.Close()
		}
	})
	go relay.Run(ctx)
	if !relay.WaitUntilConnected(ctx, startupRelayWait) {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("failed to connect to the relay server")
	}
	if ctx.Err() != nil {
		return nil
	}

	var localRegistration localSessionRegistration
	switch daemonMode := normalizedRunDaemonMode(parsed.DaemonMode); daemonMode {
	case runDaemonModeOff:
	case runDaemonModeRequired, runDaemonModeAuto:
		ensureCtx := ctx
		cancelEnsure := func() {}
		if daemonMode == runDaemonModeAuto {
			ensureCtx, cancelEnsure = context.WithTimeout(ctx, tunnelRunDaemonAutoTimeout)
		}
		paths, ok := ensureTunnelRunDaemon(ensureCtx, parsed.BaseURL, resolvedAuth.Token)
		cancelEnsure()
		if ok {
			localRegistration = newSessionRegistration(paths, parsed.BaseURL, resolvedAuth.Token, info)
			go localRegistration.Run(ctx)
			defer localRegistration.Close()
		} else if daemonMode == runDaemonModeRequired {
			return errors.New("daemon broker registration required but daemon is unavailable or auth context does not match")
		}
	}

	local, err := prepareLocalTerminal()
	if err != nil {
		return err
	}
	defer local.Restore()

	sinkID, sink := local.SinkRegistration()
	if cols, rows, sizeErr := local.CurrentSize(); sizeErr == nil {
		relay.SetInitialSize(cols, rows)
	}
	initialSinks := map[string]session.OutputSink{
		sinkID:  sink,
		"relay": relay,
	}
	if localRegistration != nil {
		initialSinks["daemon-broker"] = localRegistration
	}

	started, err := startSession(ctx, command.Path, command.Args, initialSinks)
	if err != nil {
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

	relay.BindHub(started.Hub)

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

func normalizedRunDaemonMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case runDaemonModeOff:
		return runDaemonModeOff
	case runDaemonModeRequired:
		return runDaemonModeRequired
	default:
		return runDaemonModeAuto
	}
}

func ensureDaemonForTunnelRun(ctx context.Context, baseURL, authToken string) (daemon.Paths, bool) {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return daemon.Paths{}, false
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return daemon.Paths{}, false
	}
	if status, err := daemonStatus(ctx, paths); err == nil && status.Running {
		if runningBaseURL := strings.TrimSpace(status.BaseURL); runningBaseURL != "" && runningBaseURL != baseURL {
			return paths, false
		}
		if !daemon.AuthContextMatches(status, authToken) {
			return paths, false
		}
		return paths, true
	}
	if strings.TrimSpace(authToken) == "" {
		return paths, false
	}
	executable, err := os.Executable()
	if err != nil {
		return paths, false
	}
	result, err := startDaemon(ctx, daemon.StartOptions{
		Executable: executable,
		Paths:      paths,
		BaseURL:    baseURL,
		AuthToken:  authToken,
	})
	if err != nil {
		return paths, false
	}
	status := result.Status
	if result.AlreadyRunning {
		status = result.Status
	}
	if runningBaseURL := strings.TrimSpace(status.BaseURL); runningBaseURL != "" && runningBaseURL != baseURL {
		return paths, false
	}
	if !daemon.AuthContextMatches(status, authToken) {
		return paths, false
	}
	return paths, true
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
	identity, err := readSessionDeviceIdentity(paths)
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
	case "run", "auth", "session", "daemon", "update", "rollback", "help", "version":
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

func daemonDisplayValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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

func runDaemonOpen(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
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
			_, _ = io.WriteString(stdout, "no daemon-managed sessions; start one from a remote launch first\n")
			return nil
		}
		return err
	}
	return nil
}

func runDaemonClose(ctx context.Context, stdout, stderr io.Writer) error {
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
			_, _ = io.WriteString(stdout, "no open daemon workspace to close\n")
			return nil
		}
		return err
	}
	_, _ = io.WriteString(stdout, "daemon workspace view closed\n")
	return nil
}

func runDaemonSessions(ctx context.Context, stdout, stderr io.Writer) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	sessions, err := listDaemonWorkspace(ctx, paths)
	if err != nil {
		if errors.Is(err, daemon.ErrTmuxNotFound) {
			return errors.New(daemonTmuxInstallGuidance())
		}
		return err
	}
	if len(sessions) == 0 {
		_, _ = io.WriteString(stdout, "no workspace sessions\n")
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tWINDOWS\tATTACHED")
	for _, session := range sessions {
		_, _ = fmt.Fprintf(w, "%s\t%d\t%d\n", session.Name, session.Windows, session.Attached)
	}
	return w.Flush()
}

func runDaemonPair(ctx context.Context, stdout, stderr io.Writer) error {
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
	payload, err := json.MarshalIndent(invitation, "", "  ")
	if err != nil {
		return err
	}
	_, _ = io.WriteString(stdout, string(payload))
	_, _ = io.WriteString(stdout, "\n")
	return nil
}

func runDaemonPairPending(ctx context.Context, stdout, stderr io.Writer) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	pending, err := daemonPendingPairing(ctx, paths)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		_, _ = io.WriteString(stdout, "no pending pairing responses\n")
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "INVITATION ID\tFINGERPRINT\tDISPLAY NAME\tSAS\tRECEIVED AT\tEXPIRES AT")
	for _, response := range pending {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\n",
			response.InvitationID,
			response.AndroidFingerprint,
			daemonDisplayValue(response.AndroidDisplayName, "unknown"),
			response.SAS,
			response.ReceivedAt,
			response.ExpiresAt,
		)
	}
	return w.Flush()
}

func runDaemonPairConfirm(ctx context.Context, invitationID, sas string, stdout, stderr io.Writer) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	completion, err := daemonConfirmPairing(ctx, paths, invitationID, sas)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "paired %s\n", completion.Device.Fingerprint)
	return nil
}

func runDaemonDevices(ctx context.Context, stdout, stderr io.Writer) error {
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
	if len(devices) == 0 {
		_, _ = io.WriteString(stdout, "no paired devices\n")
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "FINGERPRINT\tDISPLAY NAME\tPAIRED AT")
	for _, device := range devices {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\n", device.Fingerprint, daemonDisplayValue(device.DisplayName, "unknown"), device.PairedAt)
	}
	return w.Flush()
}

func runDaemonBrokerSessions(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	sessions, err := daemonBrokerSessions(ctx, paths)
	if err != nil {
		return err
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].SessionID < sessions[j].SessionID
	})
	if jsonOutput {
		return writeIndentedJSON(stdout, sessions)
	}
	if len(sessions) == 0 {
		_, _ = io.WriteString(stdout, "no broker sessions\n")
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SESSION ID\tLABEL\tCWD\tUPDATED AT\tPREVIEW")
	for _, session := range sessions {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
			session.SessionID,
			daemonDisplayValue(session.Label, "-"),
			daemonDisplayValue(session.CWD, "-"),
			session.UpdatedAt,
			daemonDisplayValue(session.LatestPreview, "-"),
		)
	}
	return w.Flush()
}

func runDaemonRevoke(ctx context.Context, fingerprint string, stdout, stderr io.Writer) error {
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
	_, _ = fmt.Fprintf(stdout, "revoked %s\n", device.Fingerprint)
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
		return "tmux is required for `tunnel daemon`; install it with `brew install tmux` and try again"
	case "linux":
		switch detectLinuxDistroID() {
		case "ubuntu", "debian":
			return "tmux is required for `tunnel daemon`; install it with `sudo apt install tmux` and try again"
		case "fedora":
			return "tmux is required for `tunnel daemon`; install it with `sudo dnf install tmux` and try again"
		case "centos", "rhel", "rocky", "almalinux":
			return "tmux is required for `tunnel daemon`; install it with `sudo yum install tmux` and try again"
		case "arch":
			return "tmux is required for `tunnel daemon`; install it with `sudo pacman -S tmux` and try again"
		default:
			return "tmux is required for `tunnel daemon`; install `tmux` with your system package manager and try again"
		}
	default:
		return "tmux is required for `tunnel daemon`; install `tmux` and try again"
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
