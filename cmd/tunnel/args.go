package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

const defaultTunnelBaseURL = "https://diaro.me"

const (
	tunnelBaseURLEnv   = "TUNNEL_BASE_URL"
	tunnelAuthTokenEnv = "TUNNEL_AUTH_TOKEN"
)

type runArgs struct {
	ShowHelp     bool
	ShowVersion  bool
	Label        string
	BaseURL      string
	AuthToken    string
	Launcher     string
	LauncherArgs []string
}

type usageError struct {
	msg string
}

func (e usageError) Error() string {
	return e.msg
}

func usagef(format string, args ...any) error {
	return usageError{msg: fmt.Sprintf(format, args...)}
}

func tunnelHelpText() string {
	return fmt.Sprintf(`Usage:
  tunnel [--label label] [--base-url url] <command> [args...]
  tunnel --help
  tunnel --version

Flags:
  -h, --help   Show this help message and exit
  --version    Print tunnel version and exit
  --label      Optional session label for relay clients
  --base-url   Relay base URL (fallback: %s, default: %s)

Environment:
  %s  Required agent token for normal execution
  %s   Optional relay base URL (default: %s)

Examples:
  tunnel claude
  tunnel --label api-fix codex --profile prod
`, tunnelBaseURLEnv, defaultTunnelBaseURL, tunnelAuthTokenEnv, tunnelBaseURLEnv, defaultTunnelBaseURL)
}

func parseRunArgs(argv []string) (runArgs, error) {
	fs := flag.NewFlagSet("tunnel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var cfg runArgs
	fs.BoolVar(&cfg.ShowVersion, "version", false, "print tunnel version and exit")
	fs.StringVar(&cfg.Label, "label", "", "optional session label for relay clients")
	fs.StringVar(&cfg.BaseURL, "base-url", "", "relay base URL (fallback: TUNNEL_BASE_URL, default: https://diaro.me)")

	if err := fs.Parse(argv[1:]); errors.Is(err, flag.ErrHelp) {
		cfg.ShowHelp = true
		return cfg, nil
	} else if err != nil {
		return runArgs{}, err
	}
	if cfg.ShowHelp {
		return cfg, nil
	}
	if cfg.ShowVersion {
		return cfg, nil
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = strings.TrimSpace(os.Getenv(tunnelBaseURLEnv))
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultTunnelBaseURL
	}
	baseURL, err := validateBaseURL(cfg.BaseURL)
	if err != nil {
		return runArgs{}, err
	}
	cfg.BaseURL = baseURL

	rest := fs.Args()
	if len(rest) == 0 {
		return runArgs{}, usagef("missing launcher command")
	}

	cfg.AuthToken = strings.TrimSpace(os.Getenv(tunnelAuthTokenEnv))
	if cfg.AuthToken == "" {
		return runArgs{}, fmt.Errorf("TUNNEL_AUTH_TOKEN environment variable is required")
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
