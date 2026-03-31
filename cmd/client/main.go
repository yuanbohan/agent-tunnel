package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"yuanbohan/tunnel/internal/client"
)

func main() {
	url := flag.String("url", "ws://localhost:8585/ws", "agent WebSocket URL")
	flag.Parse()

	fmt.Fprintf(os.Stderr, "▶ agent-tunnel — %s\n  type 'exit' or Ctrl+D to disconnect\n\n", *url)

	restore, err := client.EnterRawMode()
	if err != nil {
		log.Fatal(err)
	}
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		cancel()
	}()

	if err := client.Connect(ctx, *url); err != nil && err != context.Canceled {
		restore() // restore before printing error
		log.Fatal(err)
	}
}
