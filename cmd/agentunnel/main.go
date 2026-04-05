package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"yuanbohan/tunnel/codexapp"
	"yuanbohan/tunnel/connector"
	"yuanbohan/tunnel/launcher"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/session"
)

var (
	resolveLauncher      = launcher.Resolve
	prepareLocalTerminal = session.PrepareLocalTerminal
	startSession         = session.StartCommandWithInitialSinks
	// startCodexRuntime boots the local Codex app-server sidecar and returns
	// the rewritten command that must be attached to the PTY (`codex --remote ...`).
	startCodexRuntime = func(ctx context.Context, command launcher.Command) (codexRuntime, error) {
		return codexapp.Start(ctx, command)
	}
	// startCodexStateMonitor connects the Codex app-server lifecycle to the relay
	// connector. The monitor never talks to the relay directly; it reports state
	// changes into the Connector, which forwards them as `session_state` frames.
	startCodexStateMonitor = func(ctx context.Context, wsURL string, relay *connector.Connector) {
		codexapp.MonitorActionRequired(ctx, wsURL, relay)
	}
	newConnector = func(url, token string, info protocol.SessionInfo) *connector.Connector {
		return connector.New(url, token, info)
	}
)

// codexRuntime is the narrow contract that `cmd/agentunnel` needs from the
// Codex-specific sidecar manager. It lets the main launcher stay generic while
// still handling the extra lifecycle that Codex requires.
type codexRuntime interface {
	RemoteCommand() launcher.Command
	AppServerURL() string
	Wait() error
	Close() error
}

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

	fmt.Fprintf(
		stderr,
		"▶ agentunnel — %s\n  relay: %s\n  local terminal is interactive\n\n",
		command.Name,
		parsed.RelayAddr,
	)

	local, err := prepareLocalTerminal()
	if err != nil {
		return err
	}
	defer local.Restore()

	commandPreview := strings.TrimSpace(strings.Join(append([]string{filepath.Base(command.Path)}, command.Args...), " "))

	var sidecar codexRuntime
	if command.Name == "codex" {
		// Codex is the only launcher that needs a second local process. Start the
		// app-server first, wait until it is ready, then rewrite the PTY child to
		// `codex --remote <app-server-ws> ...`.
		sidecar, err = startCodexRuntime(ctx, command)
		if err != nil {
			return err
		}
		// If the PTY session exits or agentunnel shuts down, we must also stop the
		// sidecar so the wrapper does not leave a long-lived local helper behind.
		defer sidecar.Close()
		command = sidecar.RemoteCommand()
	}

	sinkID, sink := local.SinkRegistration()
	info := protocol.SessionInfo{
		SessionID:      fmt.Sprintf("%d", time.Now().UnixNano()),
		Launcher:       command.Name,
		Label:          parsed.Label,
		CWD:            cwd,
		CommandPreview: commandPreview,
		StartedAt:      time.Now().UTC(),
		State:          protocol.SessionStateNormal,
	}

	relay := newConnector(relayURL, parsed.RelayToken, info)
	if sidecar != nil {
		// This is the Codex-specific state bridge:
		// app-server websocket -> codexapp monitor -> Connector.UpdateSessionState
		// -> relay `/agent/ws` -> relay session snapshot + session-events stream.
		//
		// The connector can buffer outbound state messages before its websocket is
		// fully connected, so it is safe to start the monitor before relay.Run.
		go startCodexStateMonitor(ctx, sidecar.AppServerURL(), relay)
	}

	initialSinks := map[string]session.OutputSink{
		sinkID: sink,
		// The connector is registered as an output sink so PTY bytes and Codex
		// terminal UI output flow through the same hub fanout as local stdout.
		"relay": relay,
	}

	running, err := startSession(ctx, command.Path, command.Args, initialSinks)
	if err != nil {
		return err
	}
	defer running.Close()

	// BindHub completes the connector <-> PTY relationship:
	// - connector -> hub: relay input frames become PTY stdin writes
	// - hub -> connector: local resize updates become relay `resize` frames
	//
	// For Codex, session-state updates do not go through the hub; they enter the
	// same connector directly from the app-server monitor.
	relay.BindHub(running.Hub)
	go relay.Run(ctx)

	done := local.Start(ctx, running.Hub)

	waitErr := make(chan error, 1)
	go func() {
		// The PTY child is the user-visible agent process (`codex --remote ...`
		// for Codex). Its exit usually defines the end of the local session.
		waitErr <- running.Wait()
	}()

	var sidecarWaitErr <-chan error
	if sidecar != nil {
		ch := make(chan error, 1)
		go func() {
			// If the app-server dies first, the Codex remote session is no longer
			// trustworthy. Treat that as a fatal error so the wrapper tears down the
			// PTY side as well instead of leaving a half-connected Codex session.
			err := sidecar.Wait()
			if err == nil {
				err = errors.New("codex app-server exited unexpectedly")
			}
			ch <- err
		}()
		sidecarWaitErr = ch
	}

	return waitForSessionOrShutdown(ctx, done, waitErr, sidecarWaitErr)
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

func waitForSessionOrShutdown(ctx context.Context, localDone <-chan struct{}, waitErr <-chan error, sidecarWaitErr <-chan error) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-localDone:
			localDone = nil
		case err := <-waitErr:
			return err
		case err := <-sidecarWaitErr:
			// Codex is the only launcher with a second process. Surfacing its exit
			// here makes the sidecar part of the same failure domain as the PTY child.
			return err
		}
	}
}
