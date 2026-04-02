package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"yuanbohan/tunnel/internal/launcher"
	"yuanbohan/tunnel/internal/server"
	"yuanbohan/tunnel/internal/session"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: agentunnel <claude|codex|gemini> [args...]\n")
		os.Exit(2)
	}

	command, err := launcher.Resolve(os.Args[1], os.Args[2:])
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	running, err := session.StartCommand(ctx, command.Path, command.Args)
	if err != nil {
		return err
	}
	defer running.Close()

	web, err := server.StartLocal(running.Hub)
	if err != nil {
		return err
	}
	defer web.Close(context.Background())

	fmt.Fprintf(
		os.Stderr,
		"▶ agentunnel — %s\n  open %s\n  local terminal and browser share the same live session\n\n",
		command.Name,
		web.URL,
	)

	restore, done, err := session.AttachLocalTerminal(ctx, running.Hub)
	if err != nil {
		return err
	}
	defer restore()

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- running.Wait()
	}()

	select {
	case <-ctx.Done():
	case <-done:
	case err := <-waitErr:
		if err != nil {
			return err
		}
	}

	return nil
}
