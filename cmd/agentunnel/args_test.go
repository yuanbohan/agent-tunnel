package main

import (
	"os"
	"testing"
)

func setEnv(t *testing.T, key, val string) {
	t.Helper()
	old, existed := os.LookupEnv(key)
	if val != "" {
		os.Setenv(key, val)
	} else {
		os.Unsetenv(key)
	}
	t.Cleanup(func() {
		if existed {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

func TestParseRunArgsValid(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_URL", "wss://relay.example")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	cfg, err := parseRunArgs([]string{"agentunnel", "codex", "--profile", "prod"})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.RelayURL != "wss://relay.example" {
		t.Fatalf("RelayURL = %q, want wss://relay.example", cfg.RelayURL)
	}
	if cfg.RelayToken != "secret" {
		t.Fatalf("RelayToken = %q, want secret", cfg.RelayToken)
	}
	if cfg.Launcher != "codex" {
		t.Fatalf("Launcher = %q, want codex", cfg.Launcher)
	}
	if len(cfg.LauncherArgs) != 2 || cfg.LauncherArgs[0] != "--profile" || cfg.LauncherArgs[1] != "prod" {
		t.Fatalf("LauncherArgs = %#v, want [--profile prod]", cfg.LauncherArgs)
	}
}

func TestParseRunArgsFlagOverridesEnvForRelayURL(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_URL", "wss://ignored.example")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	cfg, err := parseRunArgs([]string{"agentunnel", "--relay-url", "wss://flag.example", "codex"})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.RelayURL != "wss://flag.example" {
		t.Fatalf("RelayURL = %q, want wss://flag.example", cfg.RelayURL)
	}
}

func TestParseRunArgsMissingRelayURL(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_URL", "")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	_, err := parseRunArgs([]string{"agentunnel", "codex"})
	if err == nil {
		t.Fatal("expected error for missing relay URL")
	}
}

func TestParseRunArgsMissingToken(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_URL", "wss://relay.example")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "")

	_, err := parseRunArgs([]string{"agentunnel", "codex"})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestParseRunArgsMissingLauncher(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_URL", "wss://relay.example")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	_, err := parseRunArgs([]string{"agentunnel"})
	if err == nil {
		t.Fatal("expected error for missing launcher")
	}
}

func TestParseRunArgsWithLabelAndArgs(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_URL", "wss://relay.example")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	cfg, err := parseRunArgs([]string{
		"agentunnel",
		"--label", "api-fix",
		"--relay-url", "wss://custom.example",
		"codex",
		"--profile", "prod",
	})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.Label != "api-fix" {
		t.Fatalf("Label = %q, want api-fix", cfg.Label)
	}
	if cfg.RelayURL != "wss://custom.example" {
		t.Fatalf("RelayURL = %q, want wss://custom.example", cfg.RelayURL)
	}
	if cfg.Launcher != "codex" {
		t.Fatalf("Launcher = %q, want codex", cfg.Launcher)
	}
	if len(cfg.LauncherArgs) != 2 || cfg.LauncherArgs[0] != "--profile" || cfg.LauncherArgs[1] != "prod" {
		t.Fatalf("LauncherArgs = %#v, want [--profile prod]", cfg.LauncherArgs)
	}
}
