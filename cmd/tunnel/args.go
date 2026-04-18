package main

import (
	"fmt"
	"net/url"
	"strings"
)

const defaultTunnelBaseURL = "https://diaro.me"

const (
	tunnelBaseURLEnv         = "TUNNEL_BASE_URL"
	tunnelAuthTokenEnv       = "TUNNEL_AUTH_TOKEN"
	tunnelLaunchRequestIDEnv = "TUNNEL_LAUNCH_REQUEST_ID"
)

type runArgs struct {
	Verbose      bool
	Label        string
	BaseURL      string
	Launcher     string
	LauncherArgs []string
}

type usageError struct {
	msg    string
	detail string
	help   string
}

type exitError struct {
	code int
}

func (e usageError) Error() string {
	return e.msg
}

func (e exitError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

func usagef(format string, args ...any) error {
	return usageError{msg: fmt.Sprintf(format, args...)}
}

func usageWithHelp(helpText, format string, args ...any) error {
	return usageError{
		msg:  fmt.Sprintf(format, args...),
		help: helpText,
	}
}

func guidancef(detail string) error {
	return usageError{msg: detail, detail: detail, help: rootHelpText()}
}

func rootHelpText() string {
	return fmt.Sprintf(`Usage:
  tunnel run [options] <command> [args...]
  tunnel auth <command>
  tunnel daemon <command>
  tunnel --help
  tunnel --version

Commands:
  run         Launch a local command and connect it to the relay
  auth        Manage local tunnel authentication
  daemon      Manage the background mobile-launch daemon
  help        Show help for a command

Flags:
  -h, --help       Show this help message and exit
      --version    Print tunnel version and exit

Environment:
  %s  Higher-priority auth token override for tunnel run
  %s    Optional relay base URL (default: %s)

Examples:
  tunnel auth login
  tunnel auth status
  tunnel daemon start
  tunnel daemon open
  tunnel daemon sessions
  tunnel run claude
  tunnel run -l api-fix codex --profile prod
`, tunnelAuthTokenEnv, tunnelBaseURLEnv, defaultTunnelBaseURL)
}

func runHelpText() string {
	return fmt.Sprintf(`Usage:
  tunnel run [-l label] [--base-url url] <command> [args...]
  tunnel run --help

Arguments:
  <command>  Launcher command resolved from PATH. Any remaining args are passed
             through to that launcher unchanged.

Flags:
  -h, --help       Show this help message and exit
  -v, --verbose    Print relay connection status on successful startup
  -l, --label      Optional session label for relay clients
      --base-url   Relay base URL (fallback: %s, default: %s)

Environment:
  %s  Higher-priority auth token override for tunnel run
  %s    Optional relay base URL (default: %s)

Examples:
  tunnel run claude
  tunnel run -l api-fix codex --profile prod
`, tunnelBaseURLEnv, defaultTunnelBaseURL, tunnelAuthTokenEnv, tunnelBaseURLEnv, defaultTunnelBaseURL)
}

func authHelpText() string {
	return `Usage:
  tunnel auth login [--base-url url]
  tunnel auth logout
  tunnel auth status
  tunnel auth --help

Commands:
  login       Sign in and save one local agent token
  logout      Remove the local saved login
  status      Show local auth source status as JSON

Examples:
  tunnel auth login
  tunnel auth logout
  tunnel auth status
`
}

func daemonHelpText() string {
	return `Usage:
  tunnel daemon <command>
  tunnel daemon --help

Commands:
  start       Start the background daemon
  status      Show daemon status
  stop        Stop the background daemon
  doctor      Run daemon diagnostics
  open        Open the daemon tmux workspace
  sessions    List daemon tmux sessions

Flags:
  -h, --help       Show this help message and exit

Notes:
  Use "tunnel daemon start --help" for start-specific flags, including --base-url.

Examples:
  tunnel daemon start
  tunnel daemon status
  tunnel daemon stop
  tunnel daemon doctor
  tunnel daemon open
  tunnel daemon sessions
`
}

func daemonStartHelpText() string {
	return fmt.Sprintf(`Usage:
  tunnel daemon start [--base-url url]
  tunnel daemon start --help

Flags:
  -h, --help       Show this help message and exit
      --base-url   Relay base URL (fallback: %s, default: %s)
`, tunnelBaseURLEnv, defaultTunnelBaseURL)
}

func authLoginHelpText() string {
	return fmt.Sprintf(`Usage:
  tunnel auth login [--base-url url]
  tunnel auth login --help

Flags:
  -h, --help       Show this help message and exit
      --base-url   Relay base URL (fallback: %s, default: %s)

Environment:
  %s  Optional relay base URL (default: %s)

Examples:
  tunnel auth login
  tunnel auth login --base-url http://127.0.0.1:8586
`, tunnelBaseURLEnv, defaultTunnelBaseURL, tunnelBaseURLEnv, defaultTunnelBaseURL)
}

func resolveBaseURL(explicit string, getenv func(string) string) (string, error) {
	resolved := strings.TrimSpace(explicit)
	if resolved == "" && getenv != nil {
		resolved = strings.TrimSpace(getenv(tunnelBaseURLEnv))
	}
	if resolved == "" {
		resolved = defaultTunnelBaseURL
	}
	return validateBaseURL(resolved)
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
