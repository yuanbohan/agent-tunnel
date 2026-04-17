package daemon

import (
	"context"
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
