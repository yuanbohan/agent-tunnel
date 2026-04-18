package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLocalStatePaths(t *testing.T) {
	homeDir := withTempHome(t)

	paths, err := resolveLocalStatePaths()
	if err != nil {
		t.Fatalf("resolveLocalStatePaths returned error: %v", err)
	}

	root := filepath.Join(homeDir, ".tunnel")
	if paths.RootDir != root {
		t.Fatalf("RootDir = %q, want %q", paths.RootDir, root)
	}
	if paths.AuthFile != filepath.Join(root, "auth.json") {
		t.Fatalf("AuthFile = %q, want auth.json under %q", paths.AuthFile, root)
	}
	if paths.SettingsFile != filepath.Join(root, "settings.json") {
		t.Fatalf("SettingsFile = %q, want settings.json under %q", paths.SettingsFile, root)
	}
	if paths.UpdaterFile != filepath.Join(root, "updater.json") {
		t.Fatalf("UpdaterFile = %q, want updater.json under %q", paths.UpdaterFile, root)
	}
}

func TestLoadTunnelSettingsMissingReturnsEmpty(t *testing.T) {
	withTempHome(t)

	settings, err := loadTunnelSettings()
	if err != nil {
		t.Fatalf("loadTunnelSettings returned error: %v", err)
	}
	if len(settings.Env) != 0 {
		t.Fatalf("settings.Env = %#v, want empty map", settings.Env)
	}
}

func TestSettingsEnvFallsBackToSettingsFile(t *testing.T) {
	homeDir := withTempHome(t)
	settingsPath := filepath.Join(homeDir, ".tunnel", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	payload := []byte("{\"env\":{\"TUNNEL_UPDATE_DISABLED\":\"1\"}}\n")
	if err := os.WriteFile(settingsPath, payload, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	value, err := settingsEnv("TUNNEL_UPDATE_DISABLED", func(string) string { return "" })
	if err != nil {
		t.Fatalf("settingsEnv returned error: %v", err)
	}
	if value != "1" {
		t.Fatalf("settingsEnv = %q, want 1", value)
	}
}

func TestSettingsEnvPrefersProcessEnv(t *testing.T) {
	homeDir := withTempHome(t)
	settingsPath := filepath.Join(homeDir, ".tunnel", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	payload := []byte("{\"env\":{\"TUNNEL_UPDATE_DISABLED\":\"0\"}}\n")
	if err := os.WriteFile(settingsPath, payload, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	value, err := settingsEnv("TUNNEL_UPDATE_DISABLED", func(string) string { return "1" })
	if err != nil {
		t.Fatalf("settingsEnv returned error: %v", err)
	}
	if value != "1" {
		t.Fatalf("settingsEnv = %q, want process env override", value)
	}
}

func TestLoadTunnelSettingsRejectsMalformedJSON(t *testing.T) {
	homeDir := withTempHome(t)
	settingsPath := filepath.Join(homeDir, ".tunnel", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	_, err := loadTunnelSettings()
	if err == nil {
		t.Fatal("loadTunnelSettings error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), settingsPath) {
		t.Fatalf("loadTunnelSettings error = %q, want settings path", err)
	}
}

func TestSaveAndLoadUpdaterState(t *testing.T) {
	homeDir := withTempHome(t)
	state := updaterState{
		LastCheckedAt:   1_712_345_678,
		RollbackVersion: "v0.1.7",
		RollbackReason:  "previous build was non-release",
	}

	if err := saveUpdaterState(state); err != nil {
		t.Fatalf("saveUpdaterState returned error: %v", err)
	}

	updaterPath := filepath.Join(homeDir, ".tunnel", "updater.json")
	info, err := os.Stat(updaterPath)
	if err != nil {
		t.Fatalf("Stat(updaterPath) returned error: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("updater file mode = %v, want 0600", got)
	}

	loaded, err := loadUpdaterState()
	if err != nil {
		t.Fatalf("loadUpdaterState returned error: %v", err)
	}
	if loaded.Version != updaterSchemaVersion {
		t.Fatalf("Version = %d, want %d", loaded.Version, updaterSchemaVersion)
	}
	if loaded.LastCheckedAt != state.LastCheckedAt {
		t.Fatalf("LastCheckedAt = %d, want %d", loaded.LastCheckedAt, state.LastCheckedAt)
	}
	if loaded.RollbackVersion != state.RollbackVersion {
		t.Fatalf("RollbackVersion = %q, want %q", loaded.RollbackVersion, state.RollbackVersion)
	}
	if loaded.RollbackReason != state.RollbackReason {
		t.Fatalf("RollbackReason = %q, want %q", loaded.RollbackReason, state.RollbackReason)
	}
}

func TestResolveLocalStatePathsFailsWhenHomeUnavailable(t *testing.T) {
	oldUserHomeDir := userHomeDir
	userHomeDir = func() (string, error) { return "", os.ErrPermission }
	t.Cleanup(func() {
		userHomeDir = oldUserHomeDir
	})

	_, err := resolveLocalStatePaths()
	if err == nil {
		t.Fatal("resolveLocalStatePaths error = nil, want home directory error")
	}
}

func TestLoadTunnelSettingsRejectsSymlinkedConfigDir(t *testing.T) {
	homeDir := withTempHome(t)
	targetDir := filepath.Join(homeDir, "other")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.Symlink(targetDir, filepath.Join(homeDir, ".tunnel")); err != nil {
		t.Fatalf("Symlink returned error: %v", err)
	}

	_, err := loadTunnelSettings()
	if err == nil {
		t.Fatal("loadTunnelSettings error = nil, want symlink rejection")
	}
	if !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("loadTunnelSettings error = %q, want symlink rejection", err)
	}
}

func TestSaveUpdaterStateRejectsSymlinkedConfigDir(t *testing.T) {
	homeDir := withTempHome(t)
	targetDir := filepath.Join(homeDir, "other")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.Symlink(targetDir, filepath.Join(homeDir, ".tunnel")); err != nil {
		t.Fatalf("Symlink returned error: %v", err)
	}

	err := saveUpdaterState(updaterState{LastCheckedAt: 1})
	if err == nil {
		t.Fatal("saveUpdaterState error = nil, want symlink rejection")
	}
	if !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("saveUpdaterState error = %q, want symlink rejection", err)
	}
}
