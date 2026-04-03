package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

type runArgs struct {
	Label        string
	RelayAddr    string
	RelayToken   string
	Launcher     string
	LauncherArgs []string
}

func parseRunArgs(argv []string) (runArgs, error) {
	fs := flag.NewFlagSet("agentunnel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var cfg runArgs
	fs.StringVar(&cfg.Label, "label", "", "optional session label for relay dashboard")
	fs.StringVar(&cfg.RelayAddr, "relay-addr", "", "relay address in host:port form (fallback: AGENTUNNEL_RELAY_ADDR)")

	if err := fs.Parse(argv[1:]); err != nil {
		return runArgs{}, err
	}

	if cfg.RelayAddr == "" {
		cfg.RelayAddr = os.Getenv("AGENTUNNEL_RELAY_ADDR")
	}
	if cfg.RelayAddr == "" {
		return runArgs{}, fmt.Errorf("relay address is required: set --relay-addr or AGENTUNNEL_RELAY_ADDR")
	}
	relayAddr, err := validateRelayAddr(cfg.RelayAddr)
	if err != nil {
		return runArgs{}, err
	}
	cfg.RelayAddr = relayAddr

	cfg.RelayToken = os.Getenv("AGENTUNNEL_RELAY_TOKEN")
	if cfg.RelayToken == "" {
		return runArgs{}, fmt.Errorf("AGENTUNNEL_RELAY_TOKEN environment variable is required")
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return runArgs{}, fmt.Errorf("usage: agentunnel [--label label] [--relay-addr host:port] <claude|codex|gemini> [args...]")
	}

	cfg.Launcher = rest[0]
	cfg.LauncherArgs = append([]string(nil), rest[1:]...)
	return cfg, nil
}

func validateRelayAddr(raw string) (string, error) {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return "", fmt.Errorf("relay address must be host:port, e.g. 127.0.0.1:8586")
	}
	if strings.Contains(addr, "://") {
		return "", fmt.Errorf("relay address must be host:port without ws:// or wss://, e.g. 127.0.0.1:8586")
	}
	if strings.ContainsAny(addr, "/?#") {
		return "", fmt.Errorf("relay address must be host:port, e.g. 127.0.0.1:8586")
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("relay address must be host:port, e.g. 127.0.0.1:8586")
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("relay address must be host:port, e.g. 127.0.0.1:8586")
	}

	return addr, nil
}

func relayWebSocketURL(addr string) string {
	return "ws://" + addr
}
