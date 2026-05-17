package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	CheckStatusOK   = "ok"
	CheckStatusWarn = "warn"
	CheckStatusFail = "fail"
)

var (
	loadConfigFn       = LoadConfig
	probeRelayHealthFn = probeRelayHealth
	listWorkspaceFn    = ListWorkspaceSessions
)

type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type DoctorReport struct {
	Checks []DoctorCheck `json:"checks"`
}

func (r DoctorReport) ExitCode() int {
	for _, check := range r.Checks {
		if check.Status != CheckStatusOK {
			return 1
		}
	}
	return 0
}

func BuildDoctorReport(ctx context.Context, paths Paths, status StatusInfo) DoctorReport {
	checks := []DoctorCheck{
		daemonProcessCheck(status),
		relayServerCheck(ctx, status),
		relayConnectivityCheck(status),
		tmuxCheck(),
		workspaceCheck(ctx, paths),
		daemonConfigCheck(paths),
		connectivityPathCheck(status),
		lastLaunchFailureCheck(status),
	}
	return DoctorReport{Checks: checks}
}

func relayServerCheck(ctx context.Context, status StatusInfo) DoctorCheck {
	baseURL := strings.TrimSpace(status.BaseURL)
	if baseURL == "" {
		return DoctorCheck{
			Name:   "relay server",
			Status: CheckStatusWarn,
			Detail: "relay server URL is unknown because this daemon has not been started yet",
		}
	}

	if err := probeRelayHealthFn(ctx, baseURL); err != nil {
		return DoctorCheck{
			Name:   "relay server",
			Status: CheckStatusWarn,
			Detail: fmt.Sprintf("relay server %s is not reachable right now: %v", baseURL, err),
		}
	}

	return DoctorCheck{
		Name:   "relay server",
		Status: CheckStatusOK,
		Detail: fmt.Sprintf("relay server %s responded to /healthz", baseURL),
	}
}

func daemonProcessCheck(status StatusInfo) DoctorCheck {
	if status.Running {
		return DoctorCheck{
			Name:   "daemon process",
			Status: CheckStatusOK,
			Detail: "background daemon is running and can accept remote launch work",
		}
	}
	return DoctorCheck{
		Name:   "daemon process",
		Status: CheckStatusFail,
		Detail: "background daemon is not running, so remote launches cannot start on this machine",
	}
}

func relayConnectivityCheck(status StatusInfo) DoctorCheck {
	if status.RelayConnected {
		return DoctorCheck{
			Name:   "relay connectivity",
			Status: CheckStatusOK,
			Detail: "relay connection is active, so remote clients can reach this machine",
		}
	}
	if !status.Running {
		return DoctorCheck{
			Name:   "relay connectivity",
			Status: CheckStatusWarn,
			Detail: "daemon is not running, so relay connection is inactive",
		}
	}
	return DoctorCheck{
		Name:   "relay connectivity",
		Status: CheckStatusWarn,
		Detail: "daemon is running locally, but relay connection is down, so remote clients cannot reach this machine",
	}
}

func tmuxCheck() DoctorCheck {
	if err := EnsureTmuxAvailable(); err != nil {
		return DoctorCheck{
			Name:   "tmux",
			Status: CheckStatusFail,
			Detail: "tmux is not installed, so remote launches cannot create persistent workspace sessions",
		}
	}
	return DoctorCheck{
		Name:   "tmux",
		Status: CheckStatusOK,
		Detail: "tmux is installed, so remote launches can create persistent workspace sessions",
	}
}

func workspaceCheck(ctx context.Context, paths Paths) DoctorCheck {
	if err := EnsureTmuxAvailable(); err != nil {
		return DoctorCheck{
			Name:   "workspace",
			Status: CheckStatusWarn,
			Detail: "tmux is unavailable, so the daemon-managed workspace cannot be opened yet",
		}
	}

	sessions, err := listWorkspaceFn(ctx, paths)
	if err != nil {
		return DoctorCheck{
			Name:   "workspace",
			Status: CheckStatusWarn,
			Detail: "the daemon-managed tmux workspace is not reachable right now",
		}
	}

	if len(sessions) == 0 {
		return DoctorCheck{
			Name:   "workspace",
			Status: CheckStatusOK,
			Detail: "the daemon-managed tmux workspace is ready and currently has no sessions",
		}
	}
	return DoctorCheck{
		Name:   "workspace",
		Status: CheckStatusOK,
		Detail: fmt.Sprintf("the daemon-managed tmux workspace is reachable and currently has %d session(s)", len(sessions)),
	}
}

func probeRelayHealth(ctx context.Context, baseURL string) error {
	healthURL, err := relayHealthURL(baseURL)
	if err != nil {
		return err
	}

	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP %d", resp.StatusCode)
	}
	return nil
}

func relayHealthURL(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported base URL scheme: %s", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/healthz"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func daemonConfigCheck(paths Paths) DoctorCheck {
	if _, err := loadConfigFn(paths); err == nil {
		return DoctorCheck{
			Name:   "daemon config",
			Status: CheckStatusOK,
			Detail: "local daemon config is readable",
		}
	}
	return DoctorCheck{
		Name:   "daemon config",
		Status: CheckStatusFail,
		Detail: "local daemon config could not be read",
	}
}

func connectivityPathCheck(status StatusInfo) DoctorCheck {
	pathKind := strings.TrimSpace(status.LastConnectivityPath)
	failure := strings.TrimSpace(status.LastConnectivityFailure)
	if pathKind == "" && failure == "" {
		return DoctorCheck{
			Name:   "connectivity path",
			Status: CheckStatusOK,
			Detail: "no direct or relay connectivity attempt has been recorded yet",
		}
	}
	if failure != "" {
		return DoctorCheck{
			Name:   "connectivity path",
			Status: CheckStatusWarn,
			Detail: fmt.Sprintf("last connectivity path attempt used %s and recorded failure: %s", pathKindOrUnknown(pathKind), failure),
		}
	}
	return DoctorCheck{
		Name:   "connectivity path",
		Status: CheckStatusOK,
		Detail: fmt.Sprintf("last connectivity path completed over %s", pathKind),
	}
}

func pathKindOrUnknown(pathKind string) string {
	if strings.TrimSpace(pathKind) == "" {
		return "unknown path"
	}
	return pathKind
}

func lastLaunchFailureCheck(status StatusInfo) DoctorCheck {
	if strings.TrimSpace(status.LastFailure) == "" {
		return DoctorCheck{
			Name:   "last launch failure",
			Status: CheckStatusOK,
			Detail: "no recent remote-launch failure is recorded",
		}
	}
	return DoctorCheck{
		Name:   "last launch failure",
		Status: CheckStatusWarn,
		Detail: "most recent remote launch failed: " + status.LastFailure,
	}
}
