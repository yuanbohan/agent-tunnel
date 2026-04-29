package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBuildDoctorReportReturnsNonZeroForWarnOrFail(t *testing.T) {
	paths := testPaths(t)

	oldLoadConfig := loadConfigFn
	oldProbeRelayHealth := probeRelayHealthFn
	oldTmuxLookPath := tmuxLookPathFn
	oldListWorkspace := listWorkspaceFn
	t.Cleanup(func() {
		loadConfigFn = oldLoadConfig
		probeRelayHealthFn = oldProbeRelayHealth
		tmuxLookPathFn = oldTmuxLookPath
		listWorkspaceFn = oldListWorkspace
	})

	loadConfigFn = func(Paths) (Config, error) {
		return DefaultConfig(), nil
	}
	probeRelayHealthFn = func(context.Context, string) error {
		return errors.New("unreachable")
	}
	tmuxLookPathFn = func(string) (string, error) { return "", errors.New("missing") }
	listWorkspaceFn = func(context.Context, Paths) ([]WorkspaceSession, error) { return nil, nil }

	report := BuildDoctorReport(context.Background(), paths, StatusInfo{Running: false})
	if report.ExitCode() == 0 {
		t.Fatal("ExitCode() = 0, want non-zero when report contains warn/fail checks")
	}
}

func TestDoctorReturnsHealthyExitCodeWhenChecksAreOK(t *testing.T) {
	paths := testPaths(t)
	oldLoadConfig := loadConfigFn
	oldProbeRelayHealth := probeRelayHealthFn
	oldTmuxLookPath := tmuxLookPathFn
	oldListWorkspace := listWorkspaceFn
	t.Cleanup(func() {
		loadConfigFn = oldLoadConfig
		probeRelayHealthFn = oldProbeRelayHealth
		tmuxLookPathFn = oldTmuxLookPath
		listWorkspaceFn = oldListWorkspace
	})

	loadConfigFn = func(Paths) (Config, error) {
		return DefaultConfig(), nil
	}
	probeRelayHealthFn = func(context.Context, string) error {
		return nil
	}
	tmuxLookPathFn = func(string) (string, error) { return "/usr/bin/tmux", nil }
	listWorkspaceFn = func(context.Context, Paths) ([]WorkspaceSession, error) {
		return []WorkspaceSession{{Name: "launch_1"}}, nil
	}

	report := BuildDoctorReport(context.Background(), paths, StatusInfo{
		Running:        true,
		RelayConnected: true,
		BaseURL:        "https://relay.example.com",
	})
	if report.ExitCode() != 0 {
		t.Fatalf("ExitCode() = %d, want 0", report.ExitCode())
	}
}

func TestWorkspaceCheckReportsReachableWorkspace(t *testing.T) {
	paths := testPaths(t)

	oldTmuxLookPath := tmuxLookPathFn
	oldListWorkspace := listWorkspaceFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		listWorkspaceFn = oldListWorkspace
	})

	tmuxLookPathFn = func(string) (string, error) { return "/usr/bin/tmux", nil }
	listWorkspaceFn = func(context.Context, Paths) ([]WorkspaceSession, error) {
		return []WorkspaceSession{{Name: "launch_1"}}, nil
	}

	check := workspaceCheck(context.Background(), paths)
	if check.Status != CheckStatusOK {
		t.Fatalf("Status = %q, want ok", check.Status)
	}
	if check.Detail != "the daemon-managed tmux workspace is reachable and currently has 1 session(s)" {
		t.Fatalf("Detail = %q, want workspace explanation", check.Detail)
	}
}

func TestConnectivityPathCheckReportsPathWithoutSessionContent(t *testing.T) {
	check := connectivityPathCheck(StatusInfo{
		LastConnectivityPath:    "relay",
		LastConnectivityFailure: "direct_timeout",
	})
	if check.Status != CheckStatusWarn {
		t.Fatalf("Status = %q, want warn", check.Status)
	}
	if !strings.Contains(check.Detail, "relay") || !strings.Contains(check.Detail, "direct_timeout") {
		t.Fatalf("Detail = %q, want path and failure", check.Detail)
	}
	for _, forbidden := range []string{"preview", "snapshot", "live bytes", "input text"} {
		if strings.Contains(check.Detail, forbidden) {
			t.Fatalf("Detail = %q, want no session content term %q", check.Detail, forbidden)
		}
	}
}
