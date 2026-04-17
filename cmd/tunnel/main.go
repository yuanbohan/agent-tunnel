package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/tunnel/connector"
	"yuanbohan/tunnel/internal/tunnel/daemon"
	"yuanbohan/tunnel/internal/tunnel/launcher"
	"yuanbohan/tunnel/internal/tunnel/session"
)

const startupRelayWait = 10 * time.Second

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
	SetLaunchRequestID(launchRequestID string)
	BindHub(hub *session.Hub)
	Run(ctx context.Context)
	WaitUntilConnected(ctx context.Context, timeout time.Duration) bool
	SubscribeStateChanges() (<-chan connector.State, func())
	CurrentState() connector.State
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
	resolveDaemonPaths = daemon.ResolvePaths
	startDaemon        = daemon.StartBackground
	runDaemonRuntime   = daemon.Run
	daemonStatus       = daemon.Status
	daemonStop         = daemon.Stop
	daemonDoctor       = daemon.Doctor
)

func main() {
	if err := run(); err != nil {
		var usageErr usageError
		if errors.As(err, &usageErr) {
			os.Exit(2)
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

func runTunnelSession(ctx context.Context, parsed runArgs, stdout, stderr io.Writer) error {
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

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	commandPreview := strings.TrimSpace(strings.Join(append([]string{command.Name}, command.Args...), " "))
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	info := protocol.SessionInfo{
		SessionID:      sessionID,
		Launcher:       command.Name,
		Label:          parsed.Label,
		CWD:            cwd,
		CommandPreview: commandPreview,
		StartedAt:      protocol.UnixTimestamp(time.Now().UTC()),
	}

	relay := newConnector(relayURL, resolvedAuth.Token, info)
	relay.SetLaunchRequestID(strings.TrimSpace(os.Getenv(tunnelLaunchRequestIDEnv)))
	relay.SetInitialConnectTimeout(startupRelayWait)
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

	running, err := startSession(ctx, command.Path, command.Args, initialSinks)
	if err != nil {
		return err
	}
	defer running.Close()

	relay.BindHub(running.Hub)

	if parsed.Verbose {
		fmt.Fprint(stderr, startupBanner(command.Name, sessionID))
	}

	done := startLocalTerminal(ctx, local, running.Hub)

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- running.Wait()
	}()

	return waitForExit(ctx, done, waitErr)
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
	case "run", "auth", "daemon", "help", "version":
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

func runDaemonStart(ctx context.Context, rawBaseURL string, stdout, stderr io.Writer) error {
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
		_, _ = fmt.Fprintf(stdout, "daemon already running (pid=%d device_id=%s)\n", status.PID, status.DeviceID)
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
		_, _ = fmt.Fprintf(stdout, "daemon already running (pid=%d device_id=%s)\n", result.Status.PID, result.Status.DeviceID)
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "daemon started (pid=%d device_id=%s)\n", result.Status.PID, result.Status.DeviceID)
	return nil
}

func runDaemonStatus(ctx context.Context, stdout, stderr io.Writer) error {
	if err := ensureDaemonPlatformSupported(); err != nil {
		return err
	}
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	status, err := daemonStatus(ctx, paths)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "running: %t\n", status.Running)
	_, _ = fmt.Fprintf(stdout, "pid: %d\n", status.PID)
	_, _ = fmt.Fprintf(stdout, "device_id: %s\n", status.DeviceID)
	_, _ = fmt.Fprintf(stdout, "display_name: %s\n", status.DisplayName)
	_, _ = fmt.Fprintf(stdout, "hostname: %s\n", status.Hostname)
	_, _ = fmt.Fprintf(stdout, "platform_family: %s\n", status.PlatformFamily)
	_, _ = fmt.Fprintf(stdout, "platform_id: %s\n", status.PlatformID)
	_, _ = fmt.Fprintf(stdout, "relay_connected: %t\n", status.RelayConnected)
	_, _ = fmt.Fprintf(stdout, "launch_health: %s\n", status.LaunchHealth)
	_, _ = fmt.Fprintf(stdout, "launcher_strategy: %s\n", status.LauncherStrategy)
	_, _ = fmt.Fprintf(stdout, "last_failure: %s\n", status.LastFailure)
	return nil
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
		return err
	}
	_, _ = io.WriteString(stdout, "daemon stopped\n")
	return nil
}

func runDaemonDoctor(ctx context.Context, stdout, stderr io.Writer) error {
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
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(stdout, "[%s] %s: %s\n", check.Status, check.Name, check.Detail)
	}
	if report.ExitCode() != 0 {
		return errors.New("daemon doctor reported non-ok checks")
	}
	return nil
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
