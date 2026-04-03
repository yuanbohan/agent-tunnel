package main

import (
	"flag"
	"fmt"
	"io"
)

type runArgs struct {
	Label        string
	RelayURL     string
	Launcher     string
	LauncherArgs []string
}

func parseRunArgs(argv []string) (runArgs, error) {
	fs := flag.NewFlagSet("agentunnel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var cfg runArgs
	fs.StringVar(&cfg.Label, "label", "", "optional session label for relay dashboard")
	fs.StringVar(&cfg.RelayURL, "relay-url", "", "relay websocket URL")

	if err := fs.Parse(argv[1:]); err != nil {
		return runArgs{}, err
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return runArgs{}, fmt.Errorf("usage: agentunnel [--label label] [--relay-url url] <claude|codex|gemini> [args...]")
	}

	cfg.Launcher = rest[0]
	cfg.LauncherArgs = append([]string(nil), rest[1:]...)
	return cfg, nil
}
