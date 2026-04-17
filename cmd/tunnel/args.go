package main

import (
	"fmt"
	"net/url"
	"strings"
)

const defaultTunnelBaseURL = "https://diaro.me"

const (
	tunnelBaseURLEnv   = "TUNNEL_BASE_URL"
	tunnelAuthTokenEnv = "TUNNEL_AUTH_TOKEN"
)

type runArgs struct {
	Verbose      bool
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
  tunnel [-l label] [--base-url url] <command> [args...]
  tunnel --help
  tunnel --version

Arguments:
  <command>  Launcher command resolved from PATH. Any remaining args are passed
             through to that launcher unchanged.

Flags:
  -h, --help       Show this help message and exit
      --version    Print tunnel version and exit
  -v, --verbose    Print relay connection status on successful startup
  -l, --label      Optional session label for relay clients
      --base-url   Relay base URL (fallback: %s, default: %s)

Environment:
  %s  Required agent token for normal execution
  %s    Optional relay base URL (default: %s)

Examples:
  tunnel claude
  tunnel -l api-fix codex --profile prod
`, tunnelBaseURLEnv, defaultTunnelBaseURL, tunnelAuthTokenEnv, tunnelBaseURLEnv, defaultTunnelBaseURL)
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
