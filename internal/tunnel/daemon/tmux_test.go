package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestListWorkspaceSessionsReturnsEmptyWhenNoServerExists(t *testing.T) {
	paths := testPaths(t)
	script := writeFakeTmuxScript(t, `#!/bin/sh
echo "no server running on $2" >&2
exit 1
`)

	oldLookPath := tmuxLookPathFn
	oldCommandContext := tmuxCommandContextFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldLookPath
		tmuxCommandContextFn = oldCommandContext
	})
	tmuxLookPathFn = func(string) (string, error) { return script, nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, script, args...)
	}

	sessions, err := ListWorkspaceSessions(context.Background(), paths)
	if err != nil {
		t.Fatalf("ListWorkspaceSessions returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want empty", sessions)
	}
}

func TestCreateLaunchSessionUsesDedicatedSocketAndReturnsGeneratedName(t *testing.T) {
	paths := testPaths(t)
	argFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeTmuxScript(t, "#!/bin/sh\nprintf '%s\n' \"$@\" > "+shellEscape(argFile)+"\n")

	oldLookPath := tmuxLookPathFn
	oldCommandContext := tmuxCommandContextFn
	oldSessionName := workspaceSessionNameFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldLookPath
		tmuxCommandContextFn = oldCommandContext
		workspaceSessionNameFn = oldSessionName
	})
	tmuxLookPathFn = func(string) (string, error) { return script, nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, script, args...)
	}
	workspaceSessionNameFn = func() (string, error) { return "launch_fixed", nil }

	sessionName, err := CreateLaunchSession(context.Background(), paths, "/repo", "echo hello")
	if err != nil {
		t.Fatalf("CreateLaunchSession returned error: %v", err)
	}
	if sessionName != "launch_fixed" {
		t.Fatalf("sessionName = %q, want launch_fixed", sessionName)
	}

	payload, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	got := string(payload)
	for _, want := range []string{"-S", paths.TmuxSocketPath, "new-session", "-d", "-s", "launch_fixed", "-c", "/repo", "echo hello"} {
		if !strings.Contains(got, want+"\n") {
			t.Fatalf("tmux args = %q, want %q", got, want)
		}
	}
}

func TestOpenWorkspaceReturnsNoSessionsWhenWorkspaceIsEmpty(t *testing.T) {
	paths := testPaths(t)
	argFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeTmuxScript(t, `#!/bin/sh
if [ "$3" = "list-sessions" ]; then
  echo "no server running" >&2
  exit 1
fi
printf '%s\n' "$@" > `+shellEscape(argFile)+`
`)

	oldLookPath := tmuxLookPathFn
	oldCommandContext := tmuxCommandContextFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldLookPath
		tmuxCommandContextFn = oldCommandContext
	})
	tmuxLookPathFn = func(string) (string, error) { return script, nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, script, args...)
	}

	if err := OpenWorkspace(context.Background(), paths, strings.NewReader(""), io.Discard, io.Discard); !errors.Is(err, ErrNoWorkspaceSessions) {
		t.Fatalf("OpenWorkspace error = %v, want ErrNoWorkspaceSessions", err)
	}

	if _, err := os.Stat(argFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenWorkspace created or touched tmux session args file, stat err = %v", err)
	}
}

func TestCloseWorkspaceDetachesOneClientFromDedicatedSocket(t *testing.T) {
	paths := testPaths(t)
	argFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeTmuxScript(t, `#!/bin/sh
if [ "$3" = "list-clients" ]; then
  echo "client-b"
  echo "client-a"
  exit 0
fi
printf '%s\n' "$@" > `+shellEscape(argFile)+`
`)

	oldLookPath := tmuxLookPathFn
	oldCommandContext := tmuxCommandContextFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldLookPath
		tmuxCommandContextFn = oldCommandContext
	})
	tmuxLookPathFn = func(string) (string, error) { return script, nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, script, args...)
	}

	if err := CloseWorkspace(context.Background(), paths); err != nil {
		t.Fatalf("CloseWorkspace returned error: %v", err)
	}
	payload, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	got := string(payload)
	for _, want := range []string{"-S", paths.TmuxSocketPath, "detach-client", "-t", "client-a"} {
		if !strings.Contains(got, want+"\n") {
			t.Fatalf("tmux args = %q, want %q", got, want)
		}
	}
}

func TestCloseWorkspaceReturnsNoOpenWorkspaceWhenNoServerExists(t *testing.T) {
	paths := testPaths(t)
	script := writeFakeTmuxScript(t, `#!/bin/sh
echo "no server running" >&2
exit 1
`)

	oldLookPath := tmuxLookPathFn
	oldCommandContext := tmuxCommandContextFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldLookPath
		tmuxCommandContextFn = oldCommandContext
	})
	tmuxLookPathFn = func(string) (string, error) { return script, nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, script, args...)
	}

	if err := CloseWorkspace(context.Background(), paths); !errors.Is(err, ErrNoOpenWorkspace) {
		t.Fatalf("CloseWorkspace error = %v, want ErrNoOpenWorkspace", err)
	}
}

func TestTerminateWorkspaceSessionKillsNamedSession(t *testing.T) {
	paths := testPaths(t)
	argFile := filepath.Join(t.TempDir(), "args.txt")
	script := writeFakeTmuxScript(t, "#!/bin/sh\nprintf '%s\n' \"$@\" > "+shellEscape(argFile)+"\n")

	oldLookPath := tmuxLookPathFn
	oldCommandContext := tmuxCommandContextFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldLookPath
		tmuxCommandContextFn = oldCommandContext
	})
	tmuxLookPathFn = func(string) (string, error) { return script, nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, script, args...)
	}

	if err := TerminateWorkspaceSession(context.Background(), paths, "launch_fixed"); err != nil {
		t.Fatalf("TerminateWorkspaceSession returned error: %v", err)
	}
	payload, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	got := string(payload)
	for _, want := range []string{"-S", paths.TmuxSocketPath, "kill-session", "-t", "launch_fixed"} {
		if !strings.Contains(got, want+"\n") {
			t.Fatalf("tmux args = %q, want %q", got, want)
		}
	}
}

func TestTerminateWorkspaceSessionReturnsNotFoundForMissingSession(t *testing.T) {
	paths := testPaths(t)
	script := writeFakeTmuxScript(t, `#!/bin/sh
echo "can't find session: launch_missing" >&2
exit 1
`)

	oldLookPath := tmuxLookPathFn
	oldCommandContext := tmuxCommandContextFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldLookPath
		tmuxCommandContextFn = oldCommandContext
	})
	tmuxLookPathFn = func(string) (string, error) { return script, nil }
	tmuxCommandContextFn = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, script, args...)
	}

	if err := TerminateWorkspaceSession(context.Background(), paths, "launch_missing"); !errors.Is(err, ErrWorkspaceSessionNotFound) {
		t.Fatalf("TerminateWorkspaceSession error = %v, want ErrWorkspaceSessionNotFound", err)
	}
}

func writeFakeTmuxScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}

func shellEscape(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
}
