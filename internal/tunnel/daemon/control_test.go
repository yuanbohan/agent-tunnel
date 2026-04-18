package daemon

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStatusReadsLiveDaemonResponse(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}

	server, err := NewServer(paths.SocketPath, func(context.Context, Request) Response {
		status := StatusInfo{Running: true, PID: 123, DeviceID: "dev_123"}
		return Response{Status: &status}
	})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Serve(ctx)
	}()
	time.Sleep(50 * time.Millisecond)

	status, err := Status(context.Background(), paths)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !status.Running || status.PID != 123 || status.DeviceID != "dev_123" {
		t.Fatalf("status = %#v, want live daemon status", status)
	}
}

func TestStatusFallsBackToPersistedStateWhenSocketUnavailable(t *testing.T) {
	paths := testPaths(t)
	status := StatusInfo{Running: true, PID: 321, DeviceID: "dev_321", LaunchHealth: LaunchHealthDegraded}
	if err := writeJSONFile(paths.StatusFile, status); err != nil {
		t.Fatalf("writeJSONFile returned error: %v", err)
	}

	got, err := Status(context.Background(), paths)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if got.Running {
		t.Fatalf("Running = %t, want false for offline persisted status", got.Running)
	}
	if got.DeviceID != "dev_321" || got.LaunchHealth != LaunchHealthDegraded {
		t.Fatalf("status = %#v, want persisted fields preserved", got)
	}
}

func TestStatusReturnsNotRunningWhenNoDaemonStateExists(t *testing.T) {
	paths := testPaths(t)

	_, err := Status(context.Background(), paths)
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status error = %v, want ErrNotRunning", err)
	}
}

func TestStopReturnsNotRunningWhenNoDaemonStateExists(t *testing.T) {
	paths := testPaths(t)

	err := Stop(context.Background(), paths)
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Stop error = %v, want ErrNotRunning", err)
	}
}

func TestNewServerRejectsNonSocketPath(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	if err := os.WriteFile(paths.SocketPath, []byte("not-a-socket"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	_, err := NewServer(paths.SocketPath, nil)
	if err == nil {
		t.Fatal("NewServer error = nil, want non-socket path failure")
	}
	if !strings.Contains(err.Error(), "not a unix socket") {
		t.Fatalf("error = %q, want non-socket path guidance", err)
	}

	payload, readErr := os.ReadFile(paths.SocketPath)
	if readErr != nil {
		t.Fatalf("ReadFile returned error: %v", readErr)
	}
	if string(payload) != "not-a-socket" {
		t.Fatalf("payload = %q, want original file preserved", string(payload))
	}
}
