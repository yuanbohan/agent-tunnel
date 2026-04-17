package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withTempHome(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	oldUserHomeDir := userHomeDir
	userHomeDir = func() (string, error) { return tmpDir, nil }
	t.Cleanup(func() {
		userHomeDir = oldUserHomeDir
	})
	return tmpDir
}

func TestFileAuthStoreSaveAndLoad(t *testing.T) {
	homeDir := withTempHome(t)
	store := fileAuthStore{}
	now := time.Unix(1_712_345_678, 0)
	want := newStoredAuth("alice", "tunnel-devbox-20260417-120000", "tok_123", "secret-token", 1_712_345_600, now)

	if err := store.Save(want); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	authPath := filepath.Join(homeDir, ".tunnel", "auth.json")
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("Stat(authPath) returned error: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("auth file mode = %v, want 0600", got)
	}

	dirInfo, err := os.Stat(filepath.Dir(authPath))
	if err != nil {
		t.Fatalf("Stat(configDir) returned error: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("config dir mode = %v, want 0700", got)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got.Username != want.Username || got.TokenName != want.TokenName || got.Token != want.Token {
		t.Fatalf("Load = %#v, want %#v", got, want)
	}
	if got.StoredAt != want.StoredAt || got.TokenCreatedAt != want.TokenCreatedAt {
		t.Fatalf("loaded timestamps = %#v, want %#v", got, want)
	}
}

func TestFileAuthStoreLoadMissingReturnsSentinel(t *testing.T) {
	withTempHome(t)

	_, err := fileAuthStore{}.Load()
	if !errors.Is(err, errStoredAuthNotFound) {
		t.Fatalf("Load error = %v, want errStoredAuthNotFound", err)
	}
}

func TestFileAuthStoreLoadRejectsInvalidJSON(t *testing.T) {
	homeDir := withTempHome(t)
	authPath := filepath.Join(homeDir, ".tunnel", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(authPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	_, err := fileAuthStore{}.Load()
	if err == nil {
		t.Fatal("Load error = nil, want parse error")
	}
}

func TestResolveRuntimeAuthPrefersEnvOverFile(t *testing.T) {
	withTempHome(t)
	store := fileAuthStore{}
	if err := store.Save(newStoredAuth("alice", "stored-token", "tok_123", "file-token", 1_700_000_000, time.Unix(1_700_000_100, 0))); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	resolved, err := resolveRuntimeAuth(store, func(key string) string {
		if key == tunnelAuthTokenEnv {
			return "env-token"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("resolveRuntimeAuth returned error: %v", err)
	}
	if resolved.Source != authSourceEnv {
		t.Fatalf("Source = %q, want env", resolved.Source)
	}
	if resolved.Token != "env-token" {
		t.Fatalf("Token = %q, want env-token", resolved.Token)
	}
}

func TestBuildAuthStatusShowsShadowedFileWhenEnvExists(t *testing.T) {
	withTempHome(t)
	store := fileAuthStore{}
	if err := store.Save(newStoredAuth("alice", "stored-token", "tok_123", "file-token", 1_700_000_000, time.Unix(1_700_000_100, 0))); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	status, err := buildAuthStatus(store, func(key string) string {
		if key == tunnelAuthTokenEnv {
			return "env-token"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("buildAuthStatus returned error: %v", err)
	}
	if !status.LoggedIn {
		t.Fatal("LoggedIn = false, want true")
	}
	if status.ActiveSource != authSourceEnv {
		t.Fatalf("ActiveSource = %q, want env", status.ActiveSource)
	}
	if !status.Sources.Env.Active {
		t.Fatal("env source not marked active")
	}
	if !status.Sources.File.Available || !status.Sources.File.Shadowed {
		t.Fatalf("file source = %#v, want available+shadowed", status.Sources.File)
	}
	if status.Sources.File.Username != "alice" {
		t.Fatalf("file username = %q, want alice", status.Sources.File.Username)
	}
}
