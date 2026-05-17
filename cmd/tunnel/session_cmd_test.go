package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yuanbohan/tunnel/internal/tunnel/daemon"
)

func TestRunSessionListRendersLocalDaemonTable(t *testing.T) {
	paths, cleanup := startSessionControlServer(t, []daemon.BrokerSessionSnapshot{
		{BrokerSession: daemon.BrokerSession{
			SessionID:      "1700000000000000000",
			DeviceID:       "dev-local",
			Launcher:       "codex",
			Label:          "very-long-label-that-should-truncate",
			CWD:            "/Users/alice/workspace/github.com/example/repo",
			CommandPreview: "codex --profile production --very-long-flag",
			StartedAt:      1700000000,
			PlatformFamily: "macos",
			PlatformID:     "macos-arm64",
			ComputerName:   "Alice Very Long MacBook Pro",
			LaunchSource:   "local",
		}},
		{BrokerSession: daemon.BrokerSession{
			SessionID:      "1700000000000000001",
			Launcher:       "claude",
			CWD:            "/repo",
			CommandPreview: "claude",
			StartedAt:      1700000001,
			LaunchSource:   "mobile",
		}},
	}, nil)
	defer cleanup()
	stubSessionDaemonPaths(t, paths)

	var stdout bytes.Buffer
	if err := runSessionList(context.Background(), sessionCommandArgs{}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("runSessionList returned error: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"+---------+", "| Scope", "| Source", "| Session", "mobile", "local", "This machine", "..."} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want substring %q", output, want)
		}
	}
	if strings.Contains(output, "remote") || strings.Contains(output, "unknown") {
		t.Fatalf("output = %q, did not expect remote/account-wide scope", output)
	}
	if strings.Contains(output, "macos-arm64") {
		t.Fatalf("output = %q, did not expect platform_id", output)
	}
}

func TestRunSessionListPrintsLocalDaemonJSON(t *testing.T) {
	paths, cleanup := startSessionControlServer(t, []daemon.BrokerSessionSnapshot{
		{BrokerSession: daemon.BrokerSession{
			SessionID:      "sess-1",
			DeviceID:       "dev-local",
			Launcher:       "codex",
			Label:          "api",
			CWD:            "/repo",
			CommandPreview: "codex --profile prod",
			StartedAt:      1700000000,
			PlatformFamily: "macos",
			PlatformID:     "macos-arm64",
			ComputerName:   "Alice Mac",
			LaunchSource:   "mobile",
		}},
	}, nil)
	defer cleanup()
	stubSessionDaemonPaths(t, paths)

	var stdout bytes.Buffer
	if err := runSessionList(context.Background(), sessionCommandArgs{JSON: true}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("runSessionList returned error: %v", err)
	}
	var rows []sessionListJSONRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("session list JSON unmarshal returned error: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || rows[0].SessionID != "sess-1" || rows[0].Scope != "local" || rows[0].Source != "mobile" || rows[0].Machine != "Alice Mac (macos)" {
		t.Fatalf("rows = %#v, want normalized local session JSON", rows)
	}
}

func TestRunWithArgsSessionListJSONCommandPrintsDaemonErrorEnvelope(t *testing.T) {
	oldResolvePaths := resolveDaemonPaths
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
	})
	resolveDaemonPaths = func() (daemon.Paths, error) {
		return daemon.Paths{SocketPath: filepath.Join(t.TempDir(), "missing.sock")}, nil
	}

	var stdout bytes.Buffer
	err := runWithArgs([]string{"tunnel", "session", "list", "--json"}, &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "daemon is not running") {
		t.Fatalf("runWithArgs error = %v, want local daemon error", err)
	}
	var envelope daemonCommandErrorEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("error JSON unmarshal returned error: %v\n%s", err, stdout.String())
	}
	if envelope.Error.Code != "daemon_not_running" || envelope.Error.Message == "" {
		t.Fatalf("error envelope = %#v, want daemon_not_running with message", envelope)
	}
}

func TestRunSessionStopCallsLocalDaemon(t *testing.T) {
	stopped := make(chan string, 1)
	paths, cleanup := startSessionControlServer(t, nil, stopped)
	defer cleanup()
	stubSessionDaemonPaths(t, paths)

	var stdout bytes.Buffer
	if err := runSessionStop(context.Background(), sessionCommandArgs{}, "sess-1", &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("runSessionStop returned error: %v", err)
	}
	if got := <-stopped; got != "sess-1" {
		t.Fatalf("stopped session = %q, want sess-1", got)
	}
	if got := stdout.String(); got != "stopped session sess-1\n" {
		t.Fatalf("stdout = %q, want stopped message", got)
	}
}

func TestRunSessionStopReturnsLocalNotFound(t *testing.T) {
	paths, cleanup := startSessionControlServer(t, nil, nil)
	defer cleanup()
	stubSessionDaemonPaths(t, paths)

	err := runSessionStop(context.Background(), sessionCommandArgs{}, "missing", &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, daemon.ErrSessionNotFound) {
		t.Fatalf("runSessionStop error = %v, want ErrSessionNotFound", err)
	}
}

func TestSessionCWDUsesMiddleTruncation(t *testing.T) {
	got := sessionCWD("/Users/alice/workspace/github.com/example/repo")
	if len([]rune(got)) != sessionCWDColumnWidth {
		t.Fatalf("len(%q) = %d, want %d", got, len([]rune(got)), sessionCWDColumnWidth)
	}
	if !strings.HasPrefix(got, "/Users/alice/") {
		t.Fatalf("sessionCWD = %q, want leading path context", got)
	}
	if !strings.HasSuffix(got, "example/repo") {
		t.Fatalf("sessionCWD = %q, want final directory context", got)
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("sessionCWD = %q, want middle truncation marker", got)
	}
}

func TestSessionCWDLeavesShortPathUnchanged(t *testing.T) {
	if got := sessionCWD("~/repo"); got != "~/repo" {
		t.Fatalf("sessionCWD = %q, want ~/repo", got)
	}
}

func startSessionControlServer(t *testing.T, sessions []daemon.BrokerSessionSnapshot, stopped chan<- string) (daemon.Paths, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tunnel-session-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	paths := daemon.Paths{SocketPath: filepath.Join(dir, "daemon.sock")}
	server, err := daemon.NewServer(paths.SocketPath, func(ctx context.Context, request daemon.Request) daemon.Response {
		switch request.Action {
		case "session_list":
			return daemon.Response{Sessions: sessions}
		case "session_stop":
			if stopped == nil {
				return daemon.Response{Error: daemon.ErrSessionNotFound.Error()}
			}
			stopped <- strings.TrimSpace(request.SessionID)
			return daemon.Response{}
		default:
			return daemon.Response{Error: "unsupported action"}
		}
	})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()
	return paths, func() {
		cancel()
		_ = server.Close()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("daemon control Serve returned error: %v", err)
			}
		case <-ctx.Done():
		}
	}
}

func stubSessionDaemonPaths(t *testing.T, paths daemon.Paths) {
	t.Helper()
	oldResolvePaths := resolveDaemonPaths
	t.Cleanup(func() {
		resolveDaemonPaths = oldResolvePaths
	})
	resolveDaemonPaths = func() (daemon.Paths, error) { return paths, nil }
}
