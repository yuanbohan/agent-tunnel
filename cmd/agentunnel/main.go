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

	"yuanbohan/tunnel/internal/launcher"
	"yuanbohan/tunnel/internal/relayapi"
	"yuanbohan/tunnel/internal/relayclient"
	"yuanbohan/tunnel/internal/server"
	"yuanbohan/tunnel/internal/session"
)

var (
	resolveLauncher      = launcher.Resolve
	prepareLocalTerminal = session.PrepareLocalTerminal
	startSession         = session.StartCommandWithInitialSinks
	startServer          = server.StartLocal
	loadRelayConfig      = relayclient.LoadConfig
	newRelayConnector    = func(cfg relayclient.Config, info relayapi.SessionInfo) relaySink {
		return relayclient.New(cfg, info)
	}
)

type relaySink interface {
	session.OutputSink
	BindHub(*session.Hub)
	Run(context.Context)
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

	command, err := resolveLauncher(parsed.Launcher, parsed.LauncherArgs)
	if err != nil {
		return err
	}

	relayCfg, relayEnabled, err := loadRelayConfig(os.Getenv, parsed.RelayURL)
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
	initialSinks := map[string]session.OutputSink{
		sinkID: sink,
	}

	var connector relaySink
	if relayEnabled {
		connector = newRelayConnector(relayCfg, relayapi.SessionInfo{
			SessionID:      fmt.Sprintf("%d", time.Now().UnixNano()),
			Launcher:       command.Name,
			Label:          parsed.Label,
			CWD:            cwd,
			CommandPreview: strings.TrimSpace(strings.Join(append([]string{filepath.Base(command.Path)}, command.Args...), " ")),
			StartedAt:      time.Now().UTC(),
		})
		initialSinks["relay"] = connector
	}

	running, err := startSession(ctx, command.Path, command.Args, initialSinks)
	if err != nil {
		return err
	}
	defer running.Close()

	if connector != nil {
		connector.BindHub(running.Hub)
		go connector.Run(ctx)
	}

	web, err := startServer(running.Hub)
	if err != nil {
		return err
	}
	defer web.Close(context.Background())

	fmt.Fprintf(
		stderr,
		"▶ agentunnel — %s\n  open %s\n  local terminal and browser share the same live session\n\n",
		command.Name,
		web.URL,
	)

	done := local.Start(ctx, running.Hub)

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- running.Wait()
	}()

	if err := waitForProcessOrShutdown(ctx, done, waitErr); err != nil {
		return err
	}

	return nil
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
