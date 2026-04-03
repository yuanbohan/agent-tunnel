package main

import "testing"

func TestParseRunArgsParsesLabelAndRelayURL(t *testing.T) {
	cfg, err := parseRunArgs([]string{
		"agentunnel",
		"--label", "api-fix",
		"--relay-url", "wss://relay.example",
		"codex",
		"--profile", "prod",
	})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.Label != "api-fix" {
		t.Fatalf("Label = %q, want api-fix", cfg.Label)
	}
	if cfg.RelayURL != "wss://relay.example" {
		t.Fatalf("RelayURL = %q, want wss://relay.example", cfg.RelayURL)
	}
	if cfg.Launcher != "codex" {
		t.Fatalf("Launcher = %q, want codex", cfg.Launcher)
	}
	if len(cfg.LauncherArgs) != 2 || cfg.LauncherArgs[0] != "--profile" || cfg.LauncherArgs[1] != "prod" {
		t.Fatalf("LauncherArgs = %#v, want [--profile prod]", cfg.LauncherArgs)
	}
}

func TestParseRunArgsRequiresLauncher(t *testing.T) {
	_, err := parseRunArgs([]string{"agentunnel", "--label", "docs"})
	if err == nil {
		t.Fatal("expected an error when launcher is missing")
	}
}
