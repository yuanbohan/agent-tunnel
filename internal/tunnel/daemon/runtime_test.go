package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunWritesStatusAndAnswersControlRequests(t *testing.T) {
	paths := testPaths(t)
	oldTmuxLookPath := tmuxLookPathFn
	oldReadIdentity := readOrCreateDeviceIdentityFn
	oldCollectMetadata := collectDeviceMetadataFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		readOrCreateDeviceIdentityFn = oldReadIdentity
		collectDeviceMetadataFn = oldCollectMetadata
	})

	tmuxLookPathFn = func(string) (string, error) { return "/usr/bin/tmux", nil }
	readOrCreateDeviceIdentityFn = func(Paths) (DeviceIdentity, error) {
		return DeviceIdentity{DeviceID: "dev_test"}, nil
	}
	collectDeviceMetadataFn = func() DeviceMetadata {
		return DeviceMetadata{
			DisplayName:    "Test Device",
			Hostname:       "test-host",
			PlatformFamily: PlatformFamilyMacOS,
			PlatformID:     PlatformFamilyMacOS,
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyReader, readyWriter := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, RuntimeOptions{
			Paths:     paths,
			BaseURL:   "https://relay.example.com",
			AuthToken: "token",
		}, readyWriter)
	}()

	buffer := make([]byte, 16)
	if _, err := readyReader.Read(buffer); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ready pipe read returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		status, err := Status(context.Background(), paths)
		if err == nil {
			if !status.Running || status.DeviceID != "dev_test" || status.WorkspaceBackend != workspaceBackendTmux {
				t.Fatalf("status = %#v, want running daemon status", status)
			}
			if status.AuthContextFingerprint != AuthContextFingerprint("token") {
				t.Fatalf("AuthContextFingerprint = %q, want token fingerprint", status.AuthContextFingerprint)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Status never succeeded: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := Stop(context.Background(), paths); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	persisted, err := LoadStatus(paths)
	if err != nil {
		t.Fatalf("LoadStatus returned error after stop: %v", err)
	}
	if persisted.Running || persisted.RelayConnected {
		t.Fatalf("persisted status = %#v, want stopped state persisted before stop returns", persisted)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after stop request")
	}
}

func TestRunStartsWithoutTmuxAndReportsDegradedLaunchHealth(t *testing.T) {
	paths := testPaths(t)
	oldTmuxLookPath := tmuxLookPathFn
	oldReadIdentity := readOrCreateDeviceIdentityFn
	oldCollectMetadata := collectDeviceMetadataFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		readOrCreateDeviceIdentityFn = oldReadIdentity
		collectDeviceMetadataFn = oldCollectMetadata
	})
	tmuxLookPathFn = func(string) (string, error) {
		return "", errors.New("not found")
	}
	readOrCreateDeviceIdentityFn = func(Paths) (DeviceIdentity, error) {
		return DeviceIdentity{DeviceID: "dev_tmuxless"}, nil
	}
	collectDeviceMetadataFn = func() DeviceMetadata {
		return DeviceMetadata{DisplayName: "No Tmux", Hostname: "no-tmux", PlatformFamily: PlatformFamilyLinux, PlatformID: "linux"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readyReader, readyWriter := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, RuntimeOptions{
			Paths:     paths,
			BaseURL:   "https://relay.example.com",
			AuthToken: "token",
		}, readyWriter)
	}()

	buffer := make([]byte, 16)
	if _, err := readyReader.Read(buffer); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ready pipe read returned error: %v", err)
	}
	status, err := Status(context.Background(), paths)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !status.Running || status.LaunchHealth != LaunchHealthDegraded || status.LastFailure != "tmux_not_found" {
		t.Fatalf("status = %#v, want running degraded tmux_not_found daemon", status)
	}
	if err := Stop(context.Background(), paths); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after stop request")
	}
}

func TestRunCleansUpPersistedStateOnSocketStartupFailure(t *testing.T) {
	paths := testPaths(t)
	paths.SocketPath = filepath.Join(paths.RuntimeDir, "missing", "daemon.sock")
	oldTmuxLookPath := tmuxLookPathFn
	oldReadIdentity := readOrCreateDeviceIdentityFn
	oldCollectMetadata := collectDeviceMetadataFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		readOrCreateDeviceIdentityFn = oldReadIdentity
		collectDeviceMetadataFn = oldCollectMetadata
	})
	tmuxLookPathFn = func(string) (string, error) { return "/usr/bin/tmux", nil }
	readOrCreateDeviceIdentityFn = func(Paths) (DeviceIdentity, error) { return DeviceIdentity{DeviceID: "dev_test"}, nil }
	collectDeviceMetadataFn = func() DeviceMetadata {
		return DeviceMetadata{DisplayName: "Test Device", Hostname: "test-host", PlatformFamily: PlatformFamilyLinux, PlatformID: "ubuntu"}
	}

	err := Run(context.Background(), RuntimeOptions{
		Paths:     paths,
		BaseURL:   "https://relay.example.com",
		AuthToken: "token",
	}, nil)
	if err == nil {
		t.Fatal("Run error = nil, want socket startup failure")
	}

	status, loadErr := LoadStatus(paths)
	if loadErr != nil {
		t.Fatalf("LoadStatus returned error: %v", loadErr)
	}
	if status.Running {
		t.Fatalf("status = %#v, want stopped persisted state after startup failure", status)
	}
	if _, statErr := os.Stat(paths.PIDFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("PID file stat error = %v, want not exists", statErr)
	}
}

func TestAcquireStartupLockWaitsForRelease(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	oldTimeout := daemonStartupLockTimeout
	daemonStartupLockTimeout = 2 * time.Second
	t.Cleanup(func() {
		daemonStartupLockTimeout = oldTimeout
	})

	releaseFirst, err := acquireStartupLock(context.Background(), paths)
	if err != nil {
		t.Fatalf("first acquireStartupLock returned error: %v", err)
	}
	acquired := make(chan func(), 1)
	errCh := make(chan error, 1)
	go func() {
		release, err := acquireStartupLock(context.Background(), paths)
		if err != nil {
			errCh <- err
			return
		}
		acquired <- release
	}()

	select {
	case release := <-acquired:
		release()
		t.Fatal("second acquireStartupLock succeeded before first release")
	case err := <-errCh:
		t.Fatalf("second acquireStartupLock returned error before release: %v", err)
	case <-time.After(120 * time.Millisecond):
	}

	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case err := <-errCh:
		t.Fatalf("second acquireStartupLock returned error after release: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("second acquireStartupLock did not acquire after release")
	}
}
