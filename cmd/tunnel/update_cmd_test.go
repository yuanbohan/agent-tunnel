package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tunnelupdate "yuanbohan/tunnel/internal/tunnel/update"
)

type fakeUpdaterEngine struct {
	installLatestResult  tunnelupdate.InstallResult
	installLatestErr     error
	installVersionResult tunnelupdate.InstallResult
	installVersionErr    error
	installedVersion     string
	beforeReplace        func(tunnelupdate.InstallResult) error
	onReplaceFailure     func(tunnelupdate.InstallResult) error
}

func (f *fakeUpdaterEngine) UpdateAvailable(context.Context) (tunnelupdate.LatestManifest, bool, error) {
	return tunnelupdate.LatestManifest{}, false, nil
}

func (f *fakeUpdaterEngine) InstallLatest(context.Context) (tunnelupdate.InstallResult, error) {
	if f.installLatestErr == nil && f.beforeReplace != nil && f.installLatestResult.Updated {
		if err := f.beforeReplace(f.installLatestResult); err != nil {
			return tunnelupdate.InstallResult{}, err
		}
	}
	return f.installLatestResult, f.installLatestErr
}

func (f *fakeUpdaterEngine) InstallVersion(_ context.Context, version string) (tunnelupdate.InstallResult, error) {
	f.installedVersion = version
	if f.installVersionErr == nil && f.beforeReplace != nil && f.installVersionResult.Updated {
		if err := f.beforeReplace(f.installVersionResult); err != nil {
			return tunnelupdate.InstallResult{}, err
		}
	}
	return f.installVersionResult, f.installVersionErr
}

func TestRunManualUpdateSavesRollbackTarget(t *testing.T) {
	withTempHome(t)
	engine := &fakeUpdaterEngine{
		installLatestResult: tunnelupdate.InstallResult{
			Updated:           true,
			CurrentVersion:    "v0.1.7",
			InstalledVersion:  "v0.1.9",
			RollbackAvailable: true,
			RollbackVersion:   "v0.1.7",
		},
	}

	oldNewUpdaterEngine := newUpdaterEngine
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	t.Cleanup(func() {
		newUpdaterEngine = oldNewUpdaterEngine
	})

	var stdout bytes.Buffer
	if err := runManualUpdate(context.Background(), &stdout, ioDiscard{}); err != nil {
		t.Fatalf("runManualUpdate returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "updated tunnel from v0.1.7 to v0.1.9") {
		t.Fatalf("stdout = %q, want update message", stdout.String())
	}

	state, err := loadUpdaterState()
	if err != nil {
		t.Fatalf("loadUpdaterState returned error: %v", err)
	}
	if state.RollbackVersion != "v0.1.7" {
		t.Fatalf("RollbackVersion = %q, want v0.1.7", state.RollbackVersion)
	}
	if state.RollbackReason != "" {
		t.Fatalf("RollbackReason = %q, want empty", state.RollbackReason)
	}
}

func TestRunManualUpdatePrintsRollbackUnavailableReason(t *testing.T) {
	withTempHome(t)
	engine := &fakeUpdaterEngine{
		installLatestResult: tunnelupdate.InstallResult{
			Updated:                   true,
			CurrentVersion:            "v0.1.9-dev",
			InstalledVersion:          "v0.1.9",
			RollbackUnavailableReason: "rollback is unavailable because the previous build was not an official release",
		},
	}

	oldNewUpdaterEngine := newUpdaterEngine
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	t.Cleanup(func() {
		newUpdaterEngine = oldNewUpdaterEngine
	})

	var stdout bytes.Buffer
	if err := runManualUpdate(context.Background(), &stdout, ioDiscard{}); err != nil {
		t.Fatalf("runManualUpdate returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "rollback is unavailable") {
		t.Fatalf("stdout = %q, want rollback guidance", stdout.String())
	}
}

func TestRunManualRollbackUsesRecordedVersionAndClearsState(t *testing.T) {
	withTempHome(t)
	if err := saveUpdaterState(updaterState{RollbackVersion: "v0.1.7"}); err != nil {
		t.Fatalf("saveUpdaterState returned error: %v", err)
	}
	engine := &fakeUpdaterEngine{
		installVersionResult: tunnelupdate.InstallResult{
			Updated:          true,
			CurrentVersion:   "v0.1.9",
			InstalledVersion: "v0.1.7",
		},
	}

	oldNewUpdaterEngine := newUpdaterEngine
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	t.Cleanup(func() {
		newUpdaterEngine = oldNewUpdaterEngine
	})

	var stdout bytes.Buffer
	if err := runManualRollback(context.Background(), &stdout, ioDiscard{}); err != nil {
		t.Fatalf("runManualRollback returned error: %v", err)
	}
	if engine.installedVersion != "v0.1.7" {
		t.Fatalf("InstallVersion called with %q, want v0.1.7", engine.installedVersion)
	}
	if !strings.Contains(stdout.String(), "rolled back tunnel from v0.1.9 to v0.1.7") {
		t.Fatalf("stdout = %q, want rollback message", stdout.String())
	}

	state, err := loadUpdaterState()
	if err != nil {
		t.Fatalf("loadUpdaterState returned error: %v", err)
	}
	if state.RollbackVersion != "" || state.RollbackReason != "" {
		t.Fatalf("state = %#v, want cleared rollback fields", state)
	}
}

func TestRunManualRollbackFailsWithoutRecordedVersion(t *testing.T) {
	withTempHome(t)
	if err := saveUpdaterState(updaterState{
		RollbackReason: "rollback is unavailable because the previous build was not an official release",
	}); err != nil {
		t.Fatalf("saveUpdaterState returned error: %v", err)
	}

	err := runManualRollback(context.Background(), ioDiscard{}, ioDiscard{})
	if err == nil {
		t.Fatal("runManualRollback error = nil, want unavailable rollback")
	}
	if !strings.Contains(err.Error(), "rollback unavailable") {
		t.Fatalf("error = %q, want rollback unavailable", err)
	}
}

func TestRunManualUpdateNoopDoesNotWriteRollbackState(t *testing.T) {
	withTempHome(t)
	engine := &fakeUpdaterEngine{
		installLatestResult: tunnelupdate.InstallResult{
			Updated:          false,
			CurrentVersion:   "v0.1.9",
			InstalledVersion: "v0.1.9",
		},
	}

	oldNewUpdaterEngine := newUpdaterEngine
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	t.Cleanup(func() {
		newUpdaterEngine = oldNewUpdaterEngine
	})

	var stdout bytes.Buffer
	if err := runManualUpdate(context.Background(), &stdout, ioDiscard{}); err != nil {
		t.Fatalf("runManualUpdate returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "already up to date") {
		t.Fatalf("stdout = %q, want no-op update message", stdout.String())
	}

	state, err := loadUpdaterState()
	if err != nil {
		t.Fatalf("loadUpdaterState returned error: %v", err)
	}
	if state.RollbackVersion != "" || state.RollbackReason != "" {
		t.Fatalf("state = %#v, want empty rollback state", state)
	}
}

func TestRunManualUpdateRecoversFromBrokenUpdaterState(t *testing.T) {
	homeDir := withTempHome(t)
	updaterPath := filepath.Join(homeDir, ".tunnel", "updater.json")
	if err := os.MkdirAll(filepath.Dir(updaterPath), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(updaterPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	engine := &fakeUpdaterEngine{
		installLatestResult: tunnelupdate.InstallResult{
			Updated:           true,
			CurrentVersion:    "v0.1.7",
			InstalledVersion:  "v0.1.9",
			RollbackAvailable: true,
			RollbackVersion:   "v0.1.7",
		},
	}

	oldNewUpdaterEngine := newUpdaterEngine
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	t.Cleanup(func() {
		newUpdaterEngine = oldNewUpdaterEngine
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runManualUpdate(context.Background(), &stdout, &stderr); err != nil {
		t.Fatalf("runManualUpdate returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "warning: ignoring broken updater.json") {
		t.Fatalf("stderr = %q, want recovery warning", stderr.String())
	}

	state, err := loadUpdaterState()
	if err != nil {
		t.Fatalf("loadUpdaterState returned error: %v", err)
	}
	if state.RollbackVersion != "v0.1.7" {
		t.Fatalf("RollbackVersion = %q, want v0.1.7", state.RollbackVersion)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
