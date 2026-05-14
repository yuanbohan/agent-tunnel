package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"yuanbohan/tunnel/internal/connectivity/direct"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "stun check failed: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("stuncheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", time.Second, "per-attempt STUN response timeout")
	retries := fs.Int("retries", 3, "STUN request retry count")
	localAddrRaw := fs.String("local-addr", "0.0.0.0:0", "local UDP listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: stuncheck [--timeout 1s] [--retries 3] host:port")
	}
	if *timeout <= 0 {
		return fmt.Errorf("--timeout must be greater than zero")
	}
	if *retries <= 0 {
		return fmt.Errorf("--retries must be greater than zero")
	}

	serverAddr, err := net.ResolveUDPAddr("udp", fs.Arg(0))
	if err != nil {
		return fmt.Errorf("resolve STUN target %q: %w", fs.Arg(0), err)
	}
	localAddr, err := net.ResolveUDPAddr("udp", *localAddrRaw)
	if err != nil {
		return fmt.Errorf("resolve local UDP address %q: %w", *localAddrRaw, err)
	}
	socket, err := direct.ListenUDPSocket(localAddr)
	if err != nil {
		return fmt.Errorf("open local UDP socket %q: %w", localAddr, err)
	}
	defer socket.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*retries)*(*timeout)+*timeout)
	defer cancel()
	mappedAddr, err := (direct.STUNClient{
		ServerAddr: serverAddr,
		Retries:    *retries,
		Timeout:    *timeout,
	}).Discover(ctx, socket)
	if err != nil {
		if errors.Is(err, direct.ErrSTUNTimeout) {
			return fmt.Errorf("UDP timeout waiting for valid STUN Binding response from %s", serverAddr)
		}
		if errors.Is(err, direct.ErrSTUNUnexpectedResponse) {
			return fmt.Errorf("invalid STUN response from %s: %w", serverAddr, err)
		}
		return err
	}

	fmt.Fprintf(stdout, "target=%s mapped=%s local=%s\n", serverAddr, mappedAddr, socket.LocalUDPAddr())
	return nil
}
