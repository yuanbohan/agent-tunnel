package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

const defaultTunnelBaseURL = "https://diaro.me"

type runArgs struct {
	Label        string
	BaseURL      string
	AuthToken    string
	Launcher     string
	LauncherArgs []string
}

func parseRunArgs(argv []string) (runArgs, error) {
	fs := flag.NewFlagSet("tunnel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var cfg runArgs
	fs.StringVar(&cfg.Label, "label", "", "optional session label for relay clients")
	fs.StringVar(&cfg.BaseURL, "base-url", "", "relay base URL (fallback: AGENTUNNEL_BASE_URL, default: https://diaro.me)")

	if err := fs.Parse(argv[1:]); err != nil {
		return runArgs{}, err
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = strings.TrimSpace(os.Getenv("AGENTUNNEL_BASE_URL"))
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultTunnelBaseURL
	}
	baseURL, err := validateBaseURL(cfg.BaseURL)
	if err != nil {
		return runArgs{}, err
	}
	cfg.BaseURL = baseURL

	cfg.AuthToken = strings.TrimSpace(os.Getenv("AGENTUNNEL_AUTH_TOKEN"))
	if cfg.AuthToken == "" {
		return runArgs{}, fmt.Errorf("AGENTUNNEL_AUTH_TOKEN environment variable is required")
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return runArgs{}, fmt.Errorf("usage: tunnel [--label label] [--base-url url] <launcher> [args...]")
	}

	cfg.Launcher = rest[0]
	cfg.LauncherArgs = append([]string(nil), rest[1:]...)
	return cfg, nil
}

func validateBaseURL(raw string) (string, error) {
	baseURL := strings.TrimSpace(raw)
	if baseURL == "" {
		return "", fmt.Errorf("base URL must be http://127.0.0.1:8586 or https://diaro.me")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", fmt.Errorf("base URL must be http://127.0.0.1:8586 or https://diaro.me")
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("base URL must use http:// or https://")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("base URL must not include query or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	return parsed.String(), nil
}

func relayWebSocketBaseURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	return parsed.String()
}
