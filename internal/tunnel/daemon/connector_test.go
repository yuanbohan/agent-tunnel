package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildShellWrapperScopesInjectedTokenToTunnelCommand(t *testing.T) {
	wrapper := buildShellWrapper("https://relay.example.com", "secret-token", "req-123", "/repo", "api-fix", []string{"codex", "--profile", "prod"})

	if !strings.Contains(wrapper, `TUNNEL_AUTH_TOKEN=`) || !strings.Contains(wrapper, `cd /repo && TUNNEL_BASE_URL=`) || !strings.Contains(wrapper, ` tunnel run --launch-source mobile --launch-request-id req-123 --label api-fix codex --profile prod`) {
		t.Fatalf("wrapper = %q, want cwd-scoped tunnel run command", wrapper)
	}
	if strings.Contains(wrapper, `export TUNNEL_AUTH_TOKEN=secret-token`) {
		t.Fatalf("wrapper = %q, want no persistent export of the injected auth token", wrapper)
	}
	if !strings.Contains(wrapper, `if [ -n "$__tunnel_had_auth" ]; then export TUNNEL_AUTH_TOKEN="$__tunnel_prev_auth"; else unset TUNNEL_AUTH_TOKEN; fi`) {
		t.Fatalf("wrapper = %q, want auth token restoration before interactive shell", wrapper)
	}
	if strings.Contains(wrapper, `TUNNEL_LAUNCH_REQUEST_ID`) {
		t.Fatalf("wrapper = %q, did not expect launch request id environment injection", wrapper)
	}
	if strings.Contains(wrapper, `DEVICE_ID`) {
		t.Fatalf("wrapper = %q, did not expect device id environment injection", wrapper)
	}
}

func TestResolveLaunchCWDReturnsAbsolutePath(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("Chdir restore returned error: %v", err)
		}
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir returned error: %v", err)
	}

	resolved, err := resolveLaunchCWD("project")
	if err != nil {
		t.Fatalf("resolveLaunchCWD returned error: %v", err)
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		t.Fatalf("Stat resolved returned error: %v", err)
	}
	projectInfo, err := os.Stat(projectDir)
	if err != nil {
		t.Fatalf("Stat projectDir returned error: %v", err)
	}
	if !os.SameFile(resolvedInfo, projectInfo) {
		t.Fatalf("resolved = %q, want same directory as %q", resolved, projectDir)
	}
}

func TestResolveLaunchCWDRejectsBlankPath(t *testing.T) {
	if _, err := resolveLaunchCWD("   "); err == nil {
		t.Fatal("resolveLaunchCWD error = nil, want blank path rejection")
	}
}

func TestLaunchHandlerReturnsBusyWithoutStartingAnotherLaunch(t *testing.T) {
	handler := &launchHandler{
		state:    &runtimeState{},
		inFlight: true,
	}

	result := handler.Handle(context.Background(), "req-1", "codex", "/repo", "")
	if result.Status != "failed" || result.Reason != "busy" {
		t.Fatalf("result = %#v, want busy failure", result)
	}
}

func TestLaunchHandlerFailsWhenTmuxIsMissing(t *testing.T) {
	paths := testPaths(t)
	handler := &launchHandler{
		baseURL:   "https://relay.example.com",
		authToken: "secret-token",
		paths:     paths,
		state: &runtimeState{
			status: StatusInfo{LaunchHealth: LaunchHealthHealthy},
			paths:  paths,
		},
	}

	oldTmuxLookPath := tmuxLookPathFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
	})
	tmuxLookPathFn = func(string) (string, error) {
		return "", errors.New("not found")
	}

	result := handler.Handle(context.Background(), "req-1", "codex", t.TempDir(), "")
	if result.Status != "failed" || result.Reason != "tmux_not_found" {
		t.Fatalf("result = %#v, want tmux_not_found failure", result)
	}
	if got := handler.state.snapshot(); got.LastFailure != "tmux_not_found" || got.LaunchHealth != LaunchHealthDegraded {
		t.Fatalf("status = %#v, want degraded tmux_not_found state", got)
	}
}

func TestLaunchHandlerFailsWhenTmuxSessionCreationFails(t *testing.T) {
	paths := testPaths(t)
	handler := &launchHandler{
		baseURL:   "https://relay.example.com",
		authToken: "secret-token",
		paths:     paths,
		state: &runtimeState{
			status: StatusInfo{LaunchHealth: LaunchHealthHealthy},
			paths:  paths,
		},
	}

	tunnelDir := t.TempDir()
	tunnelPath := filepath.Join(tunnelDir, "tunnel")
	if err := os.WriteFile(tunnelPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", tunnelDir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})

	oldTmuxLookPath := tmuxLookPathFn
	oldTmuxCommandContext := tmuxCommandContextFn
	oldSessionName := workspaceSessionNameFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		tmuxCommandContextFn = oldTmuxCommandContext
		workspaceSessionNameFn = oldSessionName
	})
	tmuxLookPathFn = func(string) (string, error) { return "/usr/bin/tmux", nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo tmux failed >&2; exit 1")
	}
	workspaceSessionNameFn = func() (string, error) { return "launch_fixed", nil }

	result := handler.Handle(context.Background(), "req-1", "codex", t.TempDir(), "api-fix")
	if result.Status != "failed" || result.Reason != "session_start_failed" {
		t.Fatalf("result = %#v, want session_start_failed", result)
	}
	if got := handler.state.snapshot(); got.LastFailure != "session_start_failed" || got.LaunchHealth != LaunchHealthDegraded {
		t.Fatalf("status = %#v, want degraded session_start_failed state", got)
	}
}

func TestLaunchHandlerPreservesTmuxNotFoundWhenTmuxDisappearsAfterPreflight(t *testing.T) {
	paths := testPaths(t)
	handler := &launchHandler{
		baseURL:   "https://relay.example.com",
		authToken: "secret-token",
		paths:     paths,
		state: &runtimeState{
			status: StatusInfo{LaunchHealth: LaunchHealthHealthy},
			paths:  paths,
		},
	}

	tunnelDir := t.TempDir()
	tunnelPath := filepath.Join(tunnelDir, "tunnel")
	if err := os.WriteFile(tunnelPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", tunnelDir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})

	oldTmuxLookPath := tmuxLookPathFn
	oldSessionName := workspaceSessionNameFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		workspaceSessionNameFn = oldSessionName
	})

	lookups := 0
	tmuxLookPathFn = func(string) (string, error) {
		lookups++
		if lookups == 1 {
			return "/usr/bin/tmux", nil
		}
		return "", errors.New("not found")
	}
	workspaceSessionNameFn = func() (string, error) { return "launch_fixed", nil }

	result := handler.Handle(context.Background(), "req-1", "codex", t.TempDir(), "api-fix")
	if result.Status != "failed" || result.Reason != "tmux_not_found" {
		t.Fatalf("result = %#v, want tmux_not_found", result)
	}
	if got := handler.state.snapshot(); got.LastFailure != "tmux_not_found" || got.LaunchHealth != LaunchHealthDegraded {
		t.Fatalf("status = %#v, want degraded tmux_not_found state", got)
	}
}

func TestLaunchHandlerCreatesTmuxSessionAndClearsPreviousFailure(t *testing.T) {
	paths := testPaths(t)
	argFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeTmuxScript(t, "#!/bin/sh\nprintf '%s\n' \"$@\" > "+shellEscape(argFile)+"\n")
	workdir := t.TempDir()

	handler := &launchHandler{
		baseURL:   "https://relay.example.com",
		authToken: "secret-token",
		paths:     paths,
		state: &runtimeState{
			status: StatusInfo{LastFailure: "session_start_failed", LaunchHealth: LaunchHealthDegraded},
			paths:  paths,
		},
	}

	tunnelDir := t.TempDir()
	tunnelPath := filepath.Join(tunnelDir, "tunnel")
	if err := os.WriteFile(tunnelPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", tunnelDir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})

	oldTmuxLookPath := tmuxLookPathFn
	oldTmuxCommandContext := tmuxCommandContextFn
	oldSessionName := workspaceSessionNameFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		tmuxCommandContextFn = oldTmuxCommandContext
		workspaceSessionNameFn = oldSessionName
	})
	tmuxLookPathFn = func(string) (string, error) { return script, nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, script, args...)
	}
	workspaceSessionNameFn = func() (string, error) { return "launch_fixed", nil }

	result := handler.Handle(context.Background(), "req-1", "codex --profile prod", workdir, "api-fix")
	if result.Status != "accepted" || result.Reason != "" || result.WorkspaceSession != "launch_fixed" {
		t.Fatalf("result = %#v, want accepted launch", result)
	}

	payload, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	gotArgs := string(payload)
	for _, want := range []string{"-S", paths.TmuxSocketPath, "new-session", "-d", "-s", "launch_fixed", "-c", workdir} {
		if !strings.Contains(gotArgs, want+"\n") {
			t.Fatalf("tmux args = %q, want %q", gotArgs, want)
		}
	}
	if !strings.Contains(gotArgs, "tunnel run --launch-source mobile --launch-request-id req-1 --label api-fix codex --profile prod") {
		t.Fatalf("tmux args = %q, want wrapped tunnel run command", gotArgs)
	}

	if got := handler.state.snapshot(); got.LastFailure != "" || got.LaunchHealth != LaunchHealthHealthy {
		t.Fatalf("status = %#v, want cleared failure and healthy launch state", got)
	}
}

func TestLaunchHandlerTerminatesWorkspaceSession(t *testing.T) {
	paths := testPaths(t)
	argFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeTmuxScript(t, "#!/bin/sh\nprintf '%s\n' \"$@\" > "+shellEscape(argFile)+"\n")
	handler := &launchHandler{
		paths: paths,
		state: &runtimeState{
			status: StatusInfo{LaunchHealth: LaunchHealthHealthy},
			paths:  paths,
		},
	}

	oldTmuxLookPath := tmuxLookPathFn
	oldTmuxCommandContext := tmuxCommandContextFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		tmuxCommandContextFn = oldTmuxCommandContext
	})
	tmuxLookPathFn = func(string) (string, error) { return script, nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, script, args...)
	}

	result := handler.Terminate(context.Background(), "launch_fixed")
	if result.Status != "terminated" || result.Reason != "" {
		t.Fatalf("result = %#v, want terminated", result)
	}
	payload, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	gotArgs := string(payload)
	for _, want := range []string{"-S", paths.TmuxSocketPath, "kill-session", "-t", "launch_fixed"} {
		if !strings.Contains(gotArgs, want+"\n") {
			t.Fatalf("tmux args = %q, want %q", gotArgs, want)
		}
	}
}

func TestLaunchHandlerTerminateReportsMissingWorkspaceSession(t *testing.T) {
	paths := testPaths(t)
	handler := &launchHandler{
		paths: paths,
		state: &runtimeState{
			status: StatusInfo{LaunchHealth: LaunchHealthHealthy},
			paths:  paths,
		},
	}

	oldTmuxLookPath := tmuxLookPathFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
	})
	tmuxLookPathFn = func(string) (string, error) { return "/usr/bin/tmux", nil }

	result := handler.Terminate(context.Background(), "")
	if result.Status != "failed" || result.Reason != "session_not_found" {
		t.Fatalf("result = %#v, want session_not_found failure", result)
	}
}

func TestLaunchHandlerFailsWhenLaunchContextIsCancelled(t *testing.T) {
	paths := testPaths(t)
	handler := &launchHandler{
		baseURL:   "https://relay.example.com",
		authToken: "secret-token",
		paths:     paths,
		state: &runtimeState{
			status: StatusInfo{LaunchHealth: LaunchHealthHealthy},
			paths:  paths,
		},
	}

	tunnelDir := t.TempDir()
	tunnelPath := filepath.Join(tunnelDir, "tunnel")
	if err := os.WriteFile(tunnelPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", tunnelDir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})

	oldTmuxLookPath := tmuxLookPathFn
	oldTmuxCommandContext := tmuxCommandContextFn
	oldSessionName := workspaceSessionNameFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		tmuxCommandContextFn = oldTmuxCommandContext
		workspaceSessionNameFn = oldSessionName
	})
	tmuxLookPathFn = func(string) (string, error) { return "/usr/bin/tmux", nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "sleep 5")
	}
	workspaceSessionNameFn = func() (string, error) { return "launch_fixed", nil }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := handler.Handle(ctx, "req-1", "codex", t.TempDir(), "api-fix")
	if result.Status != "failed" || result.Reason != "session_start_failed" {
		t.Fatalf("result = %#v, want session_start_failed", result)
	}
	if got := handler.state.snapshot(); got.LastFailure != "session_start_failed" || got.LaunchHealth != LaunchHealthDegraded {
		t.Fatalf("status = %#v, want degraded session_start_failed state", got)
	}
}
