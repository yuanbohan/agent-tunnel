package daemon

import (
	"context"
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

func TestOpenWorkspaceCreatesSessionWhenWorkspaceIsEmpty(t *testing.T) {
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

	if err := OpenWorkspace(context.Background(), paths, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}

	payload, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(payload), "new-session") {
		t.Fatalf("tmux args = %q, want new-session", string(payload))
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
