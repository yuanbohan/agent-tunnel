package main

import (
	"os"
	"strings"
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
	setEnv(t, "AGENTUNNEL_RELAY_ADDR", "127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	cfg, err := parseRunArgs([]string{"agentunnel", "codex", "--profile", "prod"})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.RelayAddr != "127.0.0.1:8586" {
		t.Fatalf("RelayAddr = %q, want 127.0.0.1:8586", cfg.RelayAddr)
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

func TestParseRunArgsFlagOverridesEnvForRelayAddr(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_ADDR", "127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	cfg, err := parseRunArgs([]string{"agentunnel", "--relay-addr", "127.0.0.1:9000", "codex"})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.RelayAddr != "127.0.0.1:9000" {
		t.Fatalf("RelayAddr = %q, want 127.0.0.1:9000", cfg.RelayAddr)
	}
}

func TestParseRunArgsRejectsWebSocketScheme(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_ADDR", "")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	_, err := parseRunArgs([]string{"agentunnel", "--relay-addr", "ws://127.0.0.1:8586", "codex"})
	if err == nil {
		t.Fatal("expected error for websocket scheme in relay address")
	}
	if !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("error = %q, want host:port guidance", err)
	}
}

func TestParseRunArgsDoesNotReadLegacyRelayURLEnv(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_URL", "ws://127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_RELAY_ADDR", "")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	_, err := parseRunArgs([]string{"agentunnel", "codex"})
	if err == nil {
		t.Fatal("expected error when only legacy relay URL env is set")
	}
	if !strings.Contains(err.Error(), "AGENTUNNEL_RELAY_ADDR") {
		t.Fatalf("error = %q, want AGENTUNNEL_RELAY_ADDR guidance", err)
	}
}

func TestParseRunArgsMissingRelayAddr(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_ADDR", "")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	_, err := parseRunArgs([]string{"agentunnel", "codex"})
	if err == nil {
		t.Fatal("expected error for missing relay address")
	}
}

func TestParseRunArgsMissingToken(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_ADDR", "127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "")

	_, err := parseRunArgs([]string{"agentunnel", "codex"})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestParseRunArgsMissingLauncher(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_ADDR", "127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	_, err := parseRunArgs([]string{"agentunnel"})
	if err == nil {
		t.Fatal("expected error for missing launcher")
	}
}

func TestParseRunArgsWithLabelAndArgs(t *testing.T) {
	setEnv(t, "AGENTUNNEL_RELAY_ADDR", "127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_RELAY_TOKEN", "secret")

	cfg, err := parseRunArgs([]string{
		"agentunnel",
		"--label", "api-fix",
		"--relay-addr", "127.0.0.1:9000",
		"codex",
		"--profile", "prod",
	})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.Label != "api-fix" {
		t.Fatalf("Label = %q, want api-fix", cfg.Label)
	}
	if cfg.RelayAddr != "127.0.0.1:9000" {
		t.Fatalf("RelayAddr = %q, want 127.0.0.1:9000", cfg.RelayAddr)
	}
	if cfg.Launcher != "codex" {
		t.Fatalf("Launcher = %q, want codex", cfg.Launcher)
	}
	if len(cfg.LauncherArgs) != 2 || cfg.LauncherArgs[0] != "--profile" || cfg.LauncherArgs[1] != "prod" {
		t.Fatalf("LauncherArgs = %#v, want [--profile prod]", cfg.LauncherArgs)
	}
}
