package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const workspaceBackendTmux = "tmux"

var ErrTmuxNotFound = errors.New("tmux is not installed")
var ErrNoWorkspaceSessions = errors.New("no daemon-managed workspace sessions")
var ErrNoOpenWorkspace = errors.New("no open daemon workspace")
var ErrWorkspaceSessionNotFound = errors.New("daemon workspace session not found")

type WorkspaceSession struct {
	Name     string
	Windows  int
	Attached int
}

var (
	tmuxLookPathFn         = exec.LookPath
	tmuxCommandContextFn   = exec.CommandContext
	workspaceSessionNameFn = defaultWorkspaceSessionName
)

func EnsureTmuxAvailable() error {
	if _, err := tmuxLookPathFn("tmux"); err != nil {
		return ErrTmuxNotFound
	}
	return nil
}

func ListWorkspaceSessions(ctx context.Context, paths Paths) ([]WorkspaceSession, error) {
	if err := EnsureTmuxAvailable(); err != nil {
		return nil, err
	}
	output, err := runTmuxOutput(ctx, paths, "list-sessions", "-F", "#{session_name}\t#{session_windows}\t#{session_attached}")
	if err != nil {
		if isTmuxNoServerError(err) {
			return []WorkspaceSession{}, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	sessions := make([]WorkspaceSession, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		session := WorkspaceSession{Name: parts[0]}
		if len(parts) > 1 {
			session.Windows, _ = strconv.Atoi(parts[1])
		}
		if len(parts) > 2 {
			session.Attached, _ = strconv.Atoi(parts[2])
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func CountWorkspaceSessions(ctx context.Context, paths Paths) (int, error) {
	sessions, err := ListWorkspaceSessions(ctx, paths)
	if err != nil {
		return 0, err
	}
	return len(sessions), nil
}

func OpenWorkspace(ctx context.Context, paths Paths, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := EnsureTmuxAvailable(); err != nil {
		return err
	}
	sessions, err := ListWorkspaceSessions(ctx, paths)
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		return ErrNoWorkspaceSessions
	}
	return runTmuxInteractive(ctx, paths, stdin, stdout, stderr, "attach-session")
}

func CloseWorkspace(ctx context.Context, paths Paths) error {
	if err := EnsureTmuxAvailable(); err != nil {
		return err
	}
	output, err := runTmuxOutput(ctx, paths, "list-clients", "-F", "#{client_name}")
	if err != nil {
		if isTmuxNoServerError(err) {
			return ErrNoOpenWorkspace
		}
		return err
	}
	clientName := firstSortedNonEmptyLine(output)
	if clientName == "" {
		return ErrNoOpenWorkspace
	}
	if _, err := runTmuxOutput(ctx, paths, "detach-client", "-t", clientName); err != nil {
		return err
	}
	return nil
}

func CreateLaunchSession(ctx context.Context, paths Paths, cwd, command string) (string, error) {
	if err := EnsureTmuxAvailable(); err != nil {
		return "", err
	}
	sessionName, err := workspaceSessionNameFn()
	if err != nil {
		return "", err
	}
	args := []string{"new-session", "-d", "-s", sessionName}
	if strings.TrimSpace(cwd) != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, command)
	if _, err := runTmuxOutput(ctx, paths, args...); err != nil {
		return "", err
	}
	return sessionName, nil
}

func TerminateWorkspaceSession(ctx context.Context, paths Paths, sessionName string) error {
	if err := EnsureTmuxAvailable(); err != nil {
		return err
	}
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return ErrWorkspaceSessionNotFound
	}
	if _, err := runTmuxOutput(ctx, paths, "kill-session", "-t", sessionName); err != nil {
		if isTmuxNoServerError(err) || isTmuxMissingTargetError(err) {
			return ErrWorkspaceSessionNotFound
		}
		return err
	}
	return nil
}

func defaultWorkspaceSessionName() (string, error) {
	return newOpaqueID("launch", 6)
}

func runTmuxOutput(ctx context.Context, paths Paths, args ...string) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := tmuxCommandContextFn(timeoutCtx, "tmux", tmuxArgs(paths, args...)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("tmux timed out after 10s: %w", err)
		}
		return "", annotateTmuxError(err, stderr.String())
	}
	return stdout.String(), nil
}

func runTmuxInteractive(ctx context.Context, paths Paths, stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	cmd := tmuxCommandContextFn(ctx, "tmux", tmuxArgs(paths, args...)...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return annotateTmuxError(err, "")
	}
	return nil
}

func tmuxArgs(paths Paths, args ...string) []string {
	return append([]string{"-S", paths.TmuxSocketPath}, args...)
}

func annotateTmuxError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}

func isTmuxNoServerError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "no server running") ||
		strings.Contains(lower, "error connecting to") ||
		strings.Contains(lower, "no such file or directory")
}

func isTmuxMissingTargetError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "can't find session") ||
		strings.Contains(lower, "can't find window") ||
		strings.Contains(lower, "can't find pane")
}

func firstSortedNonEmptyLine(output string) string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}
