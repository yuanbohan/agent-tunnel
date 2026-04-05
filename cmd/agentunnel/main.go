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
	startCodexRuntime    = func(ctx context.Context, command launcher.Command) (codexRuntime, error) {
		return codexapp.Start(ctx, command)
	}
	startCodexStateMonitor = func(ctx context.Context, wsURL string, relay *connector.Connector) {
		codexapp.MonitorActionRequired(ctx, wsURL, relay)
	}
	newConnector = func(url, token string, info protocol.SessionInfo) *connector.Connector {
		return connector.New(url, token, info)
	}
)

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
		sidecar, err = startCodexRuntime(ctx, command)
		if err != nil {
			return err
		}
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
		go startCodexStateMonitor(ctx, sidecar.AppServerURL(), relay)
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
	go relay.Run(ctx)

	done := local.Start(ctx, running.Hub)

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- running.Wait()
	}()

	var sidecarWaitErr <-chan error
	if sidecar != nil {
		ch := make(chan error, 1)
		go func() {
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
			return err
		}
	}
}
