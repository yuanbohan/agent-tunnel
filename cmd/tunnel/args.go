package main

import (
	"fmt"
	"net/url"
	"strings"
)

const defaultTunnelBaseURL = "https://diaro.me"

const (
	tunnelBaseURLEnv        = "TUNNEL_BASE_URL"
	tunnelAuthTokenEnv      = "TUNNEL_AUTH_TOKEN"
	tunnelUpdateDisabledEnv = "TUNNEL_UPDATE_DISABLED"
)

type runArgs struct {
	Verbose         bool
	Label           string
	BaseURL         string
	DaemonMode      string
	LaunchSource    string
	LaunchRequestID string
	Launcher        string
	LauncherArgs    []string
}

const (
	runDaemonModeAuto     = "auto"
	runDaemonModeOff      = "off"
	runDaemonModeRequired = "required"
)

type sessionCommandArgs struct {
	BaseURL string
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
  tunnel session <command>
  tunnel daemon <command>
  tunnel update
  tunnel rollback
  tunnel --help
  tunnel --version

Commands:
  run         Launch a local command and connect it to the relay
  auth        Manage local tunnel authentication
  session     List and stop live tunnel sessions
  daemon      Manage the background mobile-launch daemon
  update      Update tunnel to the latest official release
  rollback    Roll back tunnel to the previous official release
  help        Show help for a command

Flags:
  -h, --help       Show this help message and exit
      --version    Print tunnel version and exit

Environment:
  %s  Higher-priority auth token override for tunnel run
  %s    Optional relay base URL (default: %s)
  %s  Disable automatic update checks before tunnel run

Examples:
  tunnel auth login
  tunnel auth status
  tunnel session list
  tunnel session stop 1700000000000000000
  tunnel daemon start
  tunnel daemon open
  tunnel daemon close
  tunnel daemon sessions
  tunnel daemon pair
  tunnel daemon devices
  tunnel update
  tunnel rollback
  tunnel run claude
  tunnel run -l api-fix codex --profile prod
`, tunnelAuthTokenEnv, tunnelBaseURLEnv, defaultTunnelBaseURL, tunnelUpdateDisabledEnv)
}

func sessionHelpText() string {
	return fmt.Sprintf(`Usage:
  tunnel session list [--base-url url]
  tunnel session stop [--base-url url] <session-id>
  tunnel session --help

Commands:
  list       List live sessions for the current account
  stop       Stop one live session

Flags:
  -h, --help       Show this help message and exit
      --base-url   Relay base URL (fallback: %s, default: %s)

Examples:
  tunnel session list
  tunnel session stop 1700000000000000000
`, tunnelBaseURLEnv, defaultTunnelBaseURL)
}

func sessionListHelpText() string {
	return fmt.Sprintf(`Usage:
  tunnel session list [--base-url url]

Flags:
  -h, --help       Show this help message and exit
      --base-url   Relay base URL (fallback: %s, default: %s)
`, tunnelBaseURLEnv, defaultTunnelBaseURL)
}

func sessionStopHelpText() string {
	return fmt.Sprintf(`Usage:
  tunnel session stop [--base-url url] <session-id>

Flags:
  -h, --help       Show this help message and exit
      --base-url   Relay base URL (fallback: %s, default: %s)
`, tunnelBaseURLEnv, defaultTunnelBaseURL)
}

func runHelpText() string {
	return fmt.Sprintf(`Usage:
  tunnel run [-l label] [--base-url url] [--daemon auto|off|required] <command> [args...]
  tunnel run --help

Arguments:
  <command>  Launcher command resolved from PATH. Any remaining args are passed
             through to that launcher unchanged.

Flags:
  -h, --help       Show this help message and exit
  -v, --verbose    Print relay connection status on successful startup
  -l, --label      Optional session label for relay clients
      --base-url   Relay base URL (fallback: %s, default: %s)
      --daemon     Daemon broker mode: auto, off, or required (default: auto)

Environment:
  %s  Higher-priority auth token override for tunnel run
  %s    Optional relay base URL (default: %s)
  %s  Disable automatic update checks before tunnel run

Examples:
  tunnel run claude
  tunnel run -l api-fix codex --profile prod
`, tunnelBaseURLEnv, defaultTunnelBaseURL, tunnelAuthTokenEnv, tunnelBaseURLEnv, defaultTunnelBaseURL, tunnelUpdateDisabledEnv)
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
  broker      Inspect daemon local broker state
  open        Open the daemon tmux workspace
  close       Close one open daemon tmux workspace view
  sessions    List daemon tmux sessions
  pair        Create or confirm pairing invitations
  devices     List paired client devices
  revoke      Revoke a paired client device

Flags:
  -h, --help       Show this help message and exit

Notes:
  Use "tunnel daemon start --help" for start-specific flags, including --base-url.

Examples:
  tunnel daemon start
  tunnel daemon status
  tunnel daemon stop
  tunnel daemon doctor
  tunnel daemon broker sessions --json
  tunnel daemon open
  tunnel daemon close
  tunnel daemon sessions
  tunnel daemon pair [--json]
  tunnel daemon pair pending [--json]
  tunnel daemon pair confirm <invitation-id> <sas> [--json]
  tunnel daemon devices [--json]
  tunnel daemon revoke <fingerprint> [--json]
`
}

func daemonStartHelpText() string {
	return fmt.Sprintf(`Usage:
  tunnel daemon start [--base-url url] [--json]
  tunnel daemon start --help

Flags:
  -h, --help       Show this help message and exit
      --base-url   Relay base URL (fallback: %s, default: %s)
      --json       Print daemon status as JSON
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
