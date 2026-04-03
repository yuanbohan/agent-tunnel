package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

type runArgs struct {
	Label        string
	RelayURL     string
	RelayToken   string
	Launcher     string
	LauncherArgs []string
}

func parseRunArgs(argv []string) (runArgs, error) {
	fs := flag.NewFlagSet("agentunnel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var cfg runArgs
	fs.StringVar(&cfg.Label, "label", "", "optional session label for relay dashboard")
	fs.StringVar(&cfg.RelayURL, "relay-url", "", "relay websocket URL (fallback: AGENTUNNEL_RELAY_URL)")

	if err := fs.Parse(argv[1:]); err != nil {
		return runArgs{}, err
	}

	if cfg.RelayURL == "" {
		cfg.RelayURL = os.Getenv("AGENTUNNEL_RELAY_URL")
	}
	if cfg.RelayURL == "" {
		return runArgs{}, fmt.Errorf("relay URL is required: set --relay-url or AGENTUNNEL_RELAY_URL")
	}

	cfg.RelayToken = os.Getenv("AGENTUNNEL_RELAY_TOKEN")
	if cfg.RelayToken == "" {
		return runArgs{}, fmt.Errorf("AGENTUNNEL_RELAY_TOKEN environment variable is required")
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return runArgs{}, fmt.Errorf("usage: agentunnel [--label label] [--relay-url url] <claude|codex|gemini> [args...]")
	}

	cfg.Launcher = rest[0]
	cfg.LauncherArgs = append([]string(nil), rest[1:]...)
	return cfg, nil
}
