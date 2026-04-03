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

	"yuanbohan/tunnel/connector"
	"yuanbohan/tunnel/launcher"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/session"
)

var (
	resolveLauncher      = launcher.Resolve
	prepareLocalTerminal = session.PrepareLocalTerminal
	startSession         = session.StartCommandWithInitialSinks
	newConnector         = func(url, token string, info protocol.SessionInfo) *connector.Connector {
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

	local, err := prepareLocalTerminal()
	if err != nil {
		return err
	}
	defer local.Restore()

	sinkID, sink := local.SinkRegistration()
	info := protocol.SessionInfo{
		SessionID:      fmt.Sprintf("%d", time.Now().UnixNano()),
		Launcher:       command.Name,
		Label:          parsed.Label,
		CWD:            cwd,
		CommandPreview: strings.TrimSpace(strings.Join(append([]string{filepath.Base(command.Path)}, command.Args...), " ")),
		StartedAt:      time.Now().UTC(),
	}

	relay := newConnector(relayURL, parsed.RelayToken, info)

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

	fmt.Fprintf(
		stderr,
		"▶ agentunnel — %s\n  relay: %s\n  local terminal is interactive\n\n",
		command.Name,
		parsed.RelayAddr,
	)

	done := local.Start(ctx, running.Hub)

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- running.Wait()
	}()

	return waitForProcessOrShutdown(ctx, done, waitErr)
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
