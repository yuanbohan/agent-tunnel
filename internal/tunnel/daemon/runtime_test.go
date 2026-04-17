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
	if err := os.Setenv("GOOS_OVERRIDE_FOR_TESTS", "linux"); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	if err := os.Setenv("DISPLAY", ":0"); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	t.Cleanup(func() {
		os.Unsetenv("GOOS_OVERRIDE_FOR_TESTS")
		os.Unsetenv("DISPLAY")
	})

	oldInferRecipe := inferRecipeFn
	oldReadIdentity := readOrCreateDeviceIdentityFn
	oldCollectMetadata := collectDeviceMetadataFn
	t.Cleanup(func() {
		inferRecipeFn = oldInferRecipe
		readOrCreateDeviceIdentityFn = oldReadIdentity
		collectDeviceMetadataFn = oldCollectMetadata
	})

	inferRecipeFn = func() (LauncherRecipe, error) {
		return LauncherRecipe{Strategy: "test_strategy"}, nil
	}
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
			if !status.Running || status.DeviceID != "dev_test" || status.LauncherStrategy != "test_strategy" {
				t.Fatalf("status = %#v, want running daemon status", status)
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

func TestRunFailsWithoutDesktopSession(t *testing.T) {
	paths := testPaths(t)
	if err := os.Setenv("GOOS_OVERRIDE_FOR_TESTS", "linux"); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	os.Unsetenv("DISPLAY")
	os.Unsetenv("WAYLAND_DISPLAY")
	t.Cleanup(func() {
		os.Unsetenv("GOOS_OVERRIDE_FOR_TESTS")
	})

	err := Run(context.Background(), RuntimeOptions{
		Paths:     paths,
		BaseURL:   "https://relay.example.com",
		AuthToken: "token",
	}, nil)
	if err == nil || err.Error() != "desktop session unavailable" {
		t.Fatalf("Run error = %v, want desktop session unavailable", err)
	}
}

func TestRunCleansUpPersistedStateOnSocketStartupFailure(t *testing.T) {
	paths := testPaths(t)
	paths.SocketPath = filepath.Join(paths.RuntimeDir, "missing", "daemon.sock")
	if err := os.Setenv("GOOS_OVERRIDE_FOR_TESTS", "linux"); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	if err := os.Setenv("DISPLAY", ":0"); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	t.Cleanup(func() {
		os.Unsetenv("GOOS_OVERRIDE_FOR_TESTS")
		os.Unsetenv("DISPLAY")
	})

	oldInferRecipe := inferRecipeFn
	oldReadIdentity := readOrCreateDeviceIdentityFn
	oldCollectMetadata := collectDeviceMetadataFn
	t.Cleanup(func() {
		inferRecipeFn = oldInferRecipe
		readOrCreateDeviceIdentityFn = oldReadIdentity
		collectDeviceMetadataFn = oldCollectMetadata
	})
	inferRecipeFn = func() (LauncherRecipe, error) { return LauncherRecipe{Strategy: "test_strategy"}, nil }
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
