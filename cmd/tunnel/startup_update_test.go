package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/buildinfo"
	tunnelupdate "yuanbohan/tunnel/internal/tunnel/update"
)

type fakeStartupUpdater struct {
	manifest           tunnelupdate.LatestManifest
	available          bool
	updateAvailableErr error
	installResult      tunnelupdate.InstallResult
	installErr         error
	installVersion     string
	beforeReplace      func(tunnelupdate.InstallResult) error
	onReplaceFailure   func(tunnelupdate.InstallResult) error
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func (f *fakeStartupUpdater) UpdateAvailable(context.Context) (tunnelupdate.LatestManifest, bool, error) {
	return f.manifest, f.available, f.updateAvailableErr
}

func (f *fakeStartupUpdater) InstallLatest(context.Context) (tunnelupdate.InstallResult, error) {
	return tunnelupdate.InstallResult{}, nil
}

func (f *fakeStartupUpdater) InstallVersion(_ context.Context, version string) (tunnelupdate.InstallResult, error) {
	f.installVersion = version
	if f.installErr == nil && f.beforeReplace != nil && f.installResult.Updated {
		if err := f.beforeReplace(f.installResult); err != nil {
			return tunnelupdate.InstallResult{}, err
		}
	}
	return f.installResult, f.installErr
}

func TestRenderStartupUpdatePromptUsesRawTerminalLineBreaks(t *testing.T) {
	var stdout bytes.Buffer
	if err := renderStartupUpdatePrompt(&stdout, "v0.1.5", "v0.1.6", 0); err != nil {
		t.Fatalf("renderStartupUpdatePrompt returned error: %v", err)
	}

	got := stdout.String()
	want := "\rA new Tunnel version is available\r\n\r\nCurrent: v0.1.5\r\nLatest:  v0.1.6\r\n\r\n? Update Tunnel now?\r\n\r\x1b[2K> Update now\r\n\r\x1b[2K  Skip and continue\r\n"
	if got != want {
		t.Fatalf("prompt render = %q, want %q", got, want)
	}
}

func TestRenderStartupUpdatePromptPropagatesWriterErrors(t *testing.T) {
	errBoom := errors.New("write failed")
	err := renderStartupUpdatePrompt(errorWriter{err: errBoom}, "v0.1.5", "v0.1.6", 0)
	if !errors.Is(err, errBoom) {
		t.Fatalf("renderStartupUpdatePrompt error = %v, want %v", err, errBoom)
	}
}

func TestRenderStartupUpdateOptionsRerenderStartsFromColumnZero(t *testing.T) {
	var stdout bytes.Buffer
	if err := renderStartupUpdateOptions(&stdout, 1, true); err != nil {
		t.Fatalf("renderStartupUpdateOptions returned error: %v", err)
	}

	got := stdout.String()
	want := "\x1b[2F\r\x1b[2K  Update now\r\n\r\x1b[2K> Skip and continue\r\n"
	if got != want {
		t.Fatalf("option rerender = %q, want %q", got, want)
	}
}

func TestMaybeHandleStartupUpdateSkipsWhenDisabledViaSettings(t *testing.T) {
	homeDir := withTempHome(t)
	settingsPath := filepath.Join(homeDir, ".tunnel", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"env":{"TUNNEL_UPDATE_DISABLED":"1"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	oldInteractive := isInteractiveTerminal
	oldNewUpdaterEngine := newUpdaterEngine
	t.Cleanup(func() {
		isInteractiveTerminal = oldInteractive
		newUpdaterEngine = oldNewUpdaterEngine
	})

	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	newUpdaterEngine = func(updaterCallbacks) updaterEngine {
		t.Fatal("newUpdaterEngine should not be called when updates are disabled")
		return nil
	}

	if err := maybeHandleStartupUpdate(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("maybeHandleStartupUpdate returned error: %v", err)
	}
}

func TestMaybeHandleStartupUpdateRecordsCheckAndSkipsWhenNoUpdateAvailable(t *testing.T) {
	withTempHome(t)
	engine := &fakeStartupUpdater{
		manifest:  tunnelupdate.LatestManifest{Version: "v0.1.9"},
		available: false,
	}

	oldInteractive := isInteractiveTerminal
	oldNewUpdaterEngine := newUpdaterEngine
	oldNow := startupUpdateNow
	oldPrompt := startupUpdatePrompt
	t.Cleanup(func() {
		isInteractiveTerminal = oldInteractive
		newUpdaterEngine = oldNewUpdaterEngine
		startupUpdateNow = oldNow
		startupUpdatePrompt = oldPrompt
	})

	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	startupUpdateNow = func() time.Time { return time.Unix(1_712_345_678, 0) }
	startupUpdatePrompt = func(io.Reader, io.Writer, string, string) (bool, error) {
		t.Fatal("startupUpdatePrompt should not be called when no update is available")
		return false, nil
	}

	if err := maybeHandleStartupUpdate(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("maybeHandleStartupUpdate returned error: %v", err)
	}

	state, err := loadUpdaterState()
	if err != nil {
		t.Fatalf("loadUpdaterState returned error: %v", err)
	}
	if state.LastCheckedAt != 1_712_345_678 {
		t.Fatalf("LastCheckedAt = %d, want 1712345678", state.LastCheckedAt)
	}
}

func TestMaybeHandleStartupUpdateSkipChoiceContinues(t *testing.T) {
	withTempHome(t)
	engine := &fakeStartupUpdater{
		manifest:  tunnelupdate.LatestManifest{Version: "v0.1.9"},
		available: true,
	}

	oldInteractive := isInteractiveTerminal
	oldNewUpdaterEngine := newUpdaterEngine
	oldNow := startupUpdateNow
	oldPrompt := startupUpdatePrompt
	oldVersion := buildinfo.Version
	t.Cleanup(func() {
		isInteractiveTerminal = oldInteractive
		newUpdaterEngine = oldNewUpdaterEngine
		startupUpdateNow = oldNow
		startupUpdatePrompt = oldPrompt
		buildinfo.Version = oldVersion
	})

	buildinfo.Version = "v0.1.7"
	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	startupUpdateNow = func() time.Time { return time.Unix(1_712_345_678, 0) }
	startupUpdatePrompt = func(_ io.Reader, _ io.Writer, currentVersion, latestVersion string) (bool, error) {
		if currentVersion != "v0.1.7" || latestVersion != "v0.1.9" {
			t.Fatalf("prompt versions = %q / %q, want v0.1.7 / v0.1.9", currentVersion, latestVersion)
		}
		return false, nil
	}

	if err := maybeHandleStartupUpdate(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("maybeHandleStartupUpdate returned error: %v", err)
	}
	if engine.installVersion != "" {
		t.Fatalf("InstallVersion called with %q, want skip path", engine.installVersion)
	}
}

func TestMaybeHandleStartupUpdateInstallFailureContinuesCurrentRun(t *testing.T) {
	withTempHome(t)
	engine := &fakeStartupUpdater{
		manifest:      tunnelupdate.LatestManifest{Version: "v0.1.9"},
		available:     true,
		installErr:    errors.New("network exploded"),
		installResult: tunnelupdate.InstallResult{InstalledVersion: "v0.1.9"},
	}

	oldInteractive := isInteractiveTerminal
	oldNewUpdaterEngine := newUpdaterEngine
	oldNow := startupUpdateNow
	oldPrompt := startupUpdatePrompt
	t.Cleanup(func() {
		isInteractiveTerminal = oldInteractive
		newUpdaterEngine = oldNewUpdaterEngine
		startupUpdateNow = oldNow
		startupUpdatePrompt = oldPrompt
	})

	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	startupUpdateNow = func() time.Time { return time.Unix(1_712_345_678, 0) }
	startupUpdatePrompt = func(io.Reader, io.Writer, string, string) (bool, error) { return true, nil }

	var stderr bytes.Buffer
	if err := maybeHandleStartupUpdate(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("maybeHandleStartupUpdate returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "tunnel update failed") {
		t.Fatalf("stderr = %q, want update failure", stderr.String())
	}
}

func TestMaybeHandleStartupUpdateReexecFailureReturnsRecoveryError(t *testing.T) {
	withTempHome(t)
	engine := &fakeStartupUpdater{
		manifest:  tunnelupdate.LatestManifest{Version: "v0.1.9"},
		available: true,
		installResult: tunnelupdate.InstallResult{
			Updated:           true,
			CurrentVersion:    "v0.1.7",
			InstalledVersion:  "v0.1.9",
			RollbackAvailable: true,
			RollbackVersion:   "v0.1.7",
		},
	}

	oldInteractive := isInteractiveTerminal
	oldNewUpdaterEngine := newUpdaterEngine
	oldNow := startupUpdateNow
	oldPrompt := startupUpdatePrompt
	oldReexec := reexecTunnelProcess
	t.Cleanup(func() {
		isInteractiveTerminal = oldInteractive
		newUpdaterEngine = oldNewUpdaterEngine
		startupUpdateNow = oldNow
		startupUpdatePrompt = oldPrompt
		reexecTunnelProcess = oldReexec
	})

	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	startupUpdateNow = func() time.Time { return time.Unix(1_712_345_678, 0) }
	startupUpdatePrompt = func(io.Reader, io.Writer, string, string) (bool, error) { return true, nil }
	reexecTunnelProcess = func() error { return errors.New("exec format error") }

	err := maybeHandleStartupUpdate(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("maybeHandleStartupUpdate error = nil, want re-exec failure")
	}
	if !strings.Contains(err.Error(), "tunnel rollback") {
		t.Fatalf("error = %q, want rollback guidance", err)
	}

	state, loadErr := loadUpdaterState()
	if loadErr != nil {
		t.Fatalf("loadUpdaterState returned error: %v", loadErr)
	}
	if state.RollbackVersion != "v0.1.7" {
		t.Fatalf("RollbackVersion = %q, want v0.1.7", state.RollbackVersion)
	}
}

func TestMaybeHandleStartupUpdateSkipsRecentCheckWithoutNetwork(t *testing.T) {
	withTempHome(t)
	if err := saveUpdaterState(updaterState{LastCheckedAt: 1_712_345_678}); err != nil {
		t.Fatalf("saveUpdaterState returned error: %v", err)
	}

	oldInteractive := isInteractiveTerminal
	oldNewUpdaterEngine := newUpdaterEngine
	oldNow := startupUpdateNow
	t.Cleanup(func() {
		isInteractiveTerminal = oldInteractive
		newUpdaterEngine = oldNewUpdaterEngine
		startupUpdateNow = oldNow
	})

	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	newUpdaterEngine = func(updaterCallbacks) updaterEngine {
		t.Fatal("newUpdaterEngine should not be called when the check interval has not expired")
		return nil
	}
	startupUpdateNow = func() time.Time { return time.Unix(1_712_345_678+60, 0) }

	if err := maybeHandleStartupUpdate(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("maybeHandleStartupUpdate returned error: %v", err)
	}
}

func TestMaybeHandleStartupUpdateFailedCheckStillAdvancesGate(t *testing.T) {
	withTempHome(t)
	engine := &fakeStartupUpdater{updateAvailableErr: errors.New("network exploded")}

	oldInteractive := isInteractiveTerminal
	oldNewUpdaterEngine := newUpdaterEngine
	oldNow := startupUpdateNow
	t.Cleanup(func() {
		isInteractiveTerminal = oldInteractive
		newUpdaterEngine = oldNewUpdaterEngine
		startupUpdateNow = oldNow
	})

	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	startupUpdateNow = func() time.Time { return time.Unix(1_712_345_678, 0) }

	if err := maybeHandleStartupUpdate(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("maybeHandleStartupUpdate returned error: %v", err)
	}

	state, err := loadUpdaterState()
	if err != nil {
		t.Fatalf("loadUpdaterState returned error: %v", err)
	}
	if state.LastCheckedAt != 1_712_345_678 {
		t.Fatalf("LastCheckedAt = %d, want 1712345678", state.LastCheckedAt)
	}
}

func TestMaybeHandleStartupUpdateWarnsAndContinuesWithBrokenSettings(t *testing.T) {
	homeDir := withTempHome(t)
	settingsPath := filepath.Join(homeDir, ".tunnel", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	engine := &fakeStartupUpdater{
		manifest:  tunnelupdate.LatestManifest{Version: "v0.1.9"},
		available: false,
	}

	oldInteractive := isInteractiveTerminal
	oldNewUpdaterEngine := newUpdaterEngine
	oldNow := startupUpdateNow
	t.Cleanup(func() {
		isInteractiveTerminal = oldInteractive
		newUpdaterEngine = oldNewUpdaterEngine
		startupUpdateNow = oldNow
	})

	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	startupUpdateNow = func() time.Time { return time.Unix(1_712_345_678, 0) }

	var stderr bytes.Buffer
	if err := maybeHandleStartupUpdate(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("maybeHandleStartupUpdate returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "warning: ignoring broken settings.json") {
		t.Fatalf("stderr = %q, want broken settings warning", stderr.String())
	}
}

func TestMaybeHandleStartupUpdateRecoversBrokenUpdaterStateDuringInstall(t *testing.T) {
	homeDir := withTempHome(t)
	updaterPath := filepath.Join(homeDir, ".tunnel", "updater.json")
	if err := os.MkdirAll(filepath.Dir(updaterPath), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(updaterPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	engine := &fakeStartupUpdater{
		manifest:  tunnelupdate.LatestManifest{Version: "v0.1.9"},
		available: true,
		installResult: tunnelupdate.InstallResult{
			Updated:           true,
			CurrentVersion:    "v0.1.7",
			InstalledVersion:  "v0.1.9",
			RollbackAvailable: true,
			RollbackVersion:   "v0.1.7",
		},
	}

	oldInteractive := isInteractiveTerminal
	oldNewUpdaterEngine := newUpdaterEngine
	oldNow := startupUpdateNow
	oldPrompt := startupUpdatePrompt
	oldReexec := reexecTunnelProcess
	t.Cleanup(func() {
		isInteractiveTerminal = oldInteractive
		newUpdaterEngine = oldNewUpdaterEngine
		startupUpdateNow = oldNow
		startupUpdatePrompt = oldPrompt
		reexecTunnelProcess = oldReexec
	})

	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
		engine.beforeReplace = callbacks.beforeReplace
		engine.onReplaceFailure = callbacks.onReplaceFailure
		return engine
	}
	startupUpdateNow = func() time.Time { return time.Unix(1_712_345_678, 0) }
	startupUpdatePrompt = func(io.Reader, io.Writer, string, string) (bool, error) { return true, nil }
	reexecTunnelProcess = func() error { return errors.New("exec format error") }

	var stderr bytes.Buffer
	err := maybeHandleStartupUpdate(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{}, &stderr)
	if err == nil {
		t.Fatal("maybeHandleStartupUpdate error = nil, want re-exec failure after successful install")
	}
	if !strings.Contains(stderr.String(), "warning: ignoring broken updater.json") {
		t.Fatalf("stderr = %q, want updater recovery warning", stderr.String())
	}

	state, loadErr := loadUpdaterState()
	if loadErr != nil {
		t.Fatalf("loadUpdaterState returned error: %v", loadErr)
	}
	if state.RollbackVersion != "v0.1.7" {
		t.Fatalf("RollbackVersion = %q, want v0.1.7", state.RollbackVersion)
	}
}
