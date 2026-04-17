package main

import (
	"bytes"
	"errors"
	"io"
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
	setEnv(t, "TUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	cfg, err := parseRunArgsForTest([]string{"codex", "--profile", "prod"})
	if err != nil {
		t.Fatalf("parseRunArgsForTest returned error: %v", err)
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

func TestParseRunArgsVersionFastPath(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "")

	var stdout bytes.Buffer
	err := runWithArgs([]string{"tunnel", "--version"}, &stdout, io.Discard)
	if err != nil {
		t.Fatalf("runWithArgs returned error: %v", err)
	}
	if got := stdout.String(); got != "tunnel v0.1.0-dev\n" {
		t.Fatalf("stdout = %q, want tunnel v0.1.0-dev", got)
	}
}

func TestParseRunArgsHelpFastPath(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "")

	var stdout bytes.Buffer
	err := runWithArgs([]string{"tunnel", "--help"}, &stdout, io.Discard)
	if err != nil {
		t.Fatalf("runWithArgs returned error: %v", err)
	}
	if got := stdout.String(); got != tunnelHelpText() {
		t.Fatalf("help output = %q, want tunnelHelpText()", got)
	}
}

func TestParseRunArgsShortHelpFastPath(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "")

	var stdout bytes.Buffer
	err := runWithArgs([]string{"tunnel", "-h"}, &stdout, io.Discard)
	if err != nil {
		t.Fatalf("runWithArgs returned error: %v", err)
	}
	if got := stdout.String(); got != tunnelHelpText() {
		t.Fatalf("help output = %q, want tunnelHelpText()", got)
	}
}

func TestParseRunArgsFlagOverridesEnvForBaseURL(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	cfg, err := parseRunArgsForTest([]string{"--base-url", "https://relay.example.com", "codex"})
	if err != nil {
		t.Fatalf("parseRunArgsForTest returned error: %v", err)
	}
	if cfg.BaseURL != "https://relay.example.com" {
		t.Fatalf("BaseURL = %q, want https://relay.example.com", cfg.BaseURL)
	}
}

func TestParseRunArgsRejectsShortBaseURLFlag(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	_, err := parseRunArgsForTest([]string{"-b", "https://relay.example.com", "codex"})
	if err == nil {
		t.Fatal("expected short base-url flag to fail")
	}
	if !strings.Contains(err.Error(), "unknown shorthand flag: 'b'") {
		t.Fatalf("error = %q, want unknown shorthand flag for b", err)
	}
}

func TestParseRunArgsTreatsVersionAfterLauncherAsLauncherArg(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	cfg, err := parseRunArgsForTest([]string{"codex", "--version"})
	if err != nil {
		t.Fatalf("parseRunArgsForTest returned error: %v", err)
	}
	if cfg.Launcher != "codex" {
		t.Fatalf("Launcher = %q, want codex", cfg.Launcher)
	}
	if len(cfg.LauncherArgs) != 1 || cfg.LauncherArgs[0] != "--version" {
		t.Fatalf("LauncherArgs = %#v, want [--version]", cfg.LauncherArgs)
	}
}

func TestParseRunArgsTreatsHelpAfterLauncherAsLauncherArg(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	cfg, err := parseRunArgsForTest([]string{"codex", "--help"})
	if err != nil {
		t.Fatalf("parseRunArgsForTest returned error: %v", err)
	}
	if cfg.Launcher != "codex" {
		t.Fatalf("Launcher = %q, want codex", cfg.Launcher)
	}
	if len(cfg.LauncherArgs) != 1 || cfg.LauncherArgs[0] != "--help" {
		t.Fatalf("LauncherArgs = %#v, want [--help]", cfg.LauncherArgs)
	}
}

func TestParseRunArgsHelpWinsBeforeBaseURLValidation(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "ws://127.0.0.1:8586")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "")

	var stdout bytes.Buffer
	err := runWithArgs([]string{"tunnel", "--help"}, &stdout, io.Discard)
	if err != nil {
		t.Fatalf("runWithArgs returned error: %v", err)
	}
}

func TestParseRunArgsRejectsWebSocketScheme(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	_, err := parseRunArgsForTest([]string{"--base-url", "ws://127.0.0.1:8586", "codex"})
	if err == nil {
		t.Fatal("expected error for websocket scheme in base URL")
	}
	if !strings.Contains(err.Error(), "http://") {
		t.Fatalf("error = %q, want base URL guidance", err)
	}
}

func TestParseRunArgsRejectsWebSocketSchemeBeforeMissingLauncher(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	_, err := parseRunArgsForTest([]string{"--base-url", "ws://127.0.0.1:8586"})
	if err == nil {
		t.Fatal("expected error for websocket scheme in base URL without launcher")
	}
	if !strings.Contains(err.Error(), "http://") {
		t.Fatalf("error = %q, want base URL guidance", err)
	}
	var usageErr usageError
	if errors.As(err, &usageErr) {
		t.Fatalf("error = %#v, want base URL validation error before usageError", err)
	}
}

func TestParseRunArgsUsesDefaultBaseURL(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	cfg, err := parseRunArgsForTest([]string{"codex"})
	if err != nil {
		t.Fatalf("parseRunArgsForTest returned error: %v", err)
	}
	if cfg.BaseURL != defaultTunnelBaseURL {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, defaultTunnelBaseURL)
	}
}

func TestParseRunArgsRejectsBareHostBaseURL(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	_, err := parseRunArgsForTest([]string{"--base-url", "diaro.me", "codex"})
	if err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestParseRunArgsMissingToken(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "")

	_, err := parseRunArgsForTest([]string{"codex"})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestParseRunArgsMissingLauncher(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	_, err := parseRunArgsForTest(nil)
	if err == nil {
		t.Fatal("expected error for missing launcher")
	}
	var usageErr usageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("error = %#v, want usageError", err)
	}
}

func TestParseRunArgsWithLabelAndArgs(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")

	cfg, err := parseRunArgsForTest([]string{
		"--label", "api-fix",
		"--base-url", "https://relay.example.com",
		"codex",
		"--profile", "prod",
	})
	if err != nil {
		t.Fatalf("parseRunArgsForTest returned error: %v", err)
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

func TestParseRunArgsIgnoresLegacyBaseURLEnv(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "secret")
	setEnv(t, "AGENTUNNEL_BASE_URL", "http://127.0.0.1:8586")

	cfg, err := parseRunArgsForTest([]string{"codex"})
	if err != nil {
		t.Fatalf("parseRunArgsForTest returned error: %v", err)
	}
	if cfg.BaseURL != defaultTunnelBaseURL {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, defaultTunnelBaseURL)
	}
}

func TestParseRunArgsIgnoresLegacyAuthTokenEnv(t *testing.T) {
	setEnv(t, "TUNNEL_BASE_URL", "http://127.0.0.1:8586")
	setEnv(t, "TUNNEL_AUTH_TOKEN", "")
	setEnv(t, "AGENTUNNEL_AUTH_TOKEN", "legacy-secret")

	_, err := parseRunArgsForTest([]string{"codex"})
	if err == nil {
		t.Fatal("expected error for missing TUNNEL_AUTH_TOKEN")
	}
	if !strings.Contains(err.Error(), "TUNNEL_AUTH_TOKEN") {
		t.Fatalf("error = %q, want TUNNEL_AUTH_TOKEN guidance", err)
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
