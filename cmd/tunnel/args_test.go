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
	setEnv(t, "AGENTUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_AUTH_TOKEN", "secret")

	cfg, err := parseRunArgs([]string{"tunnel", "codex", "--profile", "prod"})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.BaseURL != "http://127.0.0.1:8586" {
		t.Fatalf("BaseURL = %q, want http://127.0.0.1:8586", cfg.BaseURL)
	}
	if cfg.AuthToken != "secret" {
		t.Fatalf("AuthToken = %q, want secret", cfg.AuthToken)
	}
	if cfg.Launcher != "codex" {
		t.Fatalf("Launcher = %q, want codex", cfg.Launcher)
	}
	if len(cfg.LauncherArgs) != 2 || cfg.LauncherArgs[0] != "--profile" || cfg.LauncherArgs[1] != "prod" {
		t.Fatalf("LauncherArgs = %#v, want [--profile prod]", cfg.LauncherArgs)
	}
}

func TestParseRunArgsFlagOverridesEnvForBaseURL(t *testing.T) {
	setEnv(t, "AGENTUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_AUTH_TOKEN", "secret")

	cfg, err := parseRunArgs([]string{"tunnel", "--base-url", "https://relay.example.com", "codex"})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.BaseURL != "https://relay.example.com" {
		t.Fatalf("BaseURL = %q, want https://relay.example.com", cfg.BaseURL)
	}
}

func TestParseRunArgsRejectsWebSocketScheme(t *testing.T) {
	setEnv(t, "AGENTUNNEL_BASE_URL", "")
	setEnv(t, "AGENTUNNEL_AUTH_TOKEN", "secret")

	_, err := parseRunArgs([]string{"tunnel", "--base-url", "ws://127.0.0.1:8586", "codex"})
	if err == nil {
		t.Fatal("expected error for websocket scheme in base URL")
	}
	if !strings.Contains(err.Error(), "http://") {
		t.Fatalf("error = %q, want base URL guidance", err)
	}
}

func TestParseRunArgsUsesDefaultBaseURL(t *testing.T) {
	setEnv(t, "AGENTUNNEL_BASE_URL", "")
	setEnv(t, "AGENTUNNEL_AUTH_TOKEN", "secret")

	cfg, err := parseRunArgs([]string{"tunnel", "codex"})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.BaseURL != defaultTunnelBaseURL {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, defaultTunnelBaseURL)
	}
}

func TestParseRunArgsRejectsBareHostBaseURL(t *testing.T) {
	setEnv(t, "AGENTUNNEL_BASE_URL", "")
	setEnv(t, "AGENTUNNEL_AUTH_TOKEN", "secret")

	_, err := parseRunArgs([]string{"tunnel", "--base-url", "diaro.me", "codex"})
	if err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestParseRunArgsMissingToken(t *testing.T) {
	setEnv(t, "AGENTUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_AUTH_TOKEN", "")

	_, err := parseRunArgs([]string{"tunnel", "codex"})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestParseRunArgsMissingLauncher(t *testing.T) {
	setEnv(t, "AGENTUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_AUTH_TOKEN", "secret")

	_, err := parseRunArgs([]string{"tunnel"})
	if err == nil {
		t.Fatal("expected error for missing launcher")
	}
}

func TestParseRunArgsWithLabelAndArgs(t *testing.T) {
	setEnv(t, "AGENTUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "AGENTUNNEL_AUTH_TOKEN", "secret")

	cfg, err := parseRunArgs([]string{
		"tunnel",
		"--label", "api-fix",
		"--base-url", "https://relay.example.com",
		"codex",
		"--profile", "prod",
	})
	if err != nil {
		t.Fatalf("parseRunArgs returned error: %v", err)
	}
	if cfg.Label != "api-fix" {
		t.Fatalf("Label = %q, want api-fix", cfg.Label)
	}
	if cfg.BaseURL != "https://relay.example.com" {
		t.Fatalf("BaseURL = %q, want https://relay.example.com", cfg.BaseURL)
	}
	if cfg.Launcher != "codex" {
		t.Fatalf("Launcher = %q, want codex", cfg.Launcher)
	}
	if len(cfg.LauncherArgs) != 2 || cfg.LauncherArgs[0] != "--profile" || cfg.LauncherArgs[1] != "prod" {
		t.Fatalf("LauncherArgs = %#v, want [--profile prod]", cfg.LauncherArgs)
	}
}

func TestRelayWebSocketBaseURL(t *testing.T) {
	if got := relayWebSocketBaseURL("https://diaro.me"); got != "wss://diaro.me" {
		t.Fatalf("relayWebSocketBaseURL(https) = %q, want wss://diaro.me", got)
	}
	if got := relayWebSocketBaseURL("http://127.0.0.1:8586/base"); got != "ws://127.0.0.1:8586/base" {
		t.Fatalf("relayWebSocketBaseURL(http) = %q, want ws://127.0.0.1:8586/base", got)
	}
}
