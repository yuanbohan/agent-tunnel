package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"yuanbohan/tunnel/internal/launcher"
	"yuanbohan/tunnel/internal/server"
	"yuanbohan/tunnel/internal/session"
)

var (
	resolveLauncher      = launcher.Resolve
	prepareLocalTerminal = session.PrepareLocalTerminal
	startSession         = session.StartCommandWithInitialSinks
	startServer          = server.StartLocal
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
	if len(args) < 2 {
		fmt.Fprintf(stderr, "usage: agentunnel <claude|codex|gemini> [args...]\n")
		os.Exit(2)
	}

	command, err := resolveLauncher(args[1], args[2:])
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
	running, err := startSession(ctx, command.Path, command.Args, map[string]session.OutputSink{
		sinkID: sink,
	})
	if err != nil {
		return err
	}
	defer running.Close()

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
