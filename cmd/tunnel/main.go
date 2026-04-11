package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/tunnel/connector"
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
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	return runWithArgs(os.Args, os.Stderr)
}

func runWithArgs(args []string, stderr io.Writer) error {
	parsed, err := parseRunArgs(args)
	if err != nil {
		return err
	}
	relayURL := relayWebSocketURL(parsed.RelayAddr)

	command, err := resolveLauncher(parsed.Launcher, parsed.LauncherArgs)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	commandPreview := strings.TrimSpace(strings.Join(append([]string{filepath.Base(command.Path)}, command.Args...), " "))
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	info := protocol.SessionInfo{
		SessionID:      sessionID,
		Launcher:       command.Name,
		Label:          parsed.Label,
		CWD:            cwd,
		CommandPreview: commandPreview,
		StartedAt:      protocol.UnixTimestamp(time.Now().UTC()),
	}

	relay := newConnector(relayURL, parsed.RelayToken, info)
	relay.SetInitialConnectTimeout(startupRelayWait)
	go relay.Run(ctx)
	_ = relay.WaitUntilConnected(ctx, startupRelayWait)
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

	statusLine := session.NewStatusLine(stderr)
	if cols, rows, sizeErr := local.CurrentSize(); sizeErr == nil {
		statusLine.SetSize(cols, rows)
		running.Hub.AddResizeListener("status-line", func(cols, rows int) {
			statusLine.SetSize(cols, rows)
		})
	}

	fmt.Fprint(stderr, startupBanner(command.Name, sessionID, parsed.RelayAddr, relay.CurrentState()))

	stateCh, cancelStates := relay.SubscribeStateChanges()
	defer cancelStates()
	go followRelayState(ctx, statusLine, stateCh)

	done := startLocalTerminal(ctx, local, running.Hub)

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- running.Wait()
	}()

	return waitForExit(ctx, done, waitErr)
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

func startupBanner(launcherName, sessionID, relayAddr string, state connector.State) string {
	status := "connected"
	if state != connector.StateConnected {
		status = "reconnecting"
	}
	color := startupBannerGreen
	if state != connector.StateConnected {
		color = startupBannerRed
	}
	return fmt.Sprintf("%s%s▶ tunnel %s — session %s; relay %s (%s)%s\r", startupBannerClear, color, launcherName, sessionID, status, relayAddr, startupBannerReset)
}

func followRelayState(ctx context.Context, statusLine *session.StatusLine, stateCh <-chan connector.State) {
	for {
		select {
		case <-ctx.Done():
			statusLine.Clear()
			return
		case state, ok := <-stateCh:
			if !ok {
				statusLine.Clear()
				return
			}
			switch state {
			case connector.StateConnected:
				statusLine.Clear()
			case connector.StateConnecting, connector.StateReconnecting:
				statusLine.Show("relay reconnecting; local session continues")
			case connector.StateDisconnected:
				statusLine.Clear()
			}
		}
	}
}
