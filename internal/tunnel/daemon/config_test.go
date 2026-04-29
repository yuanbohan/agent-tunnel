package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaultsWhenFileMissing(t *testing.T) {
	paths := testPaths(t)

	config, err := LoadConfig(paths)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if !config.Allows("codex") || !config.Allows("claude") || !config.Allows("gemini") {
		t.Fatalf("default config = %#v, want default allowed commands", config.AllowedCommands)
	}
}

func TestLoadConfigRejectsMalformedJSON(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(paths.ConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if _, err := LoadConfig(paths); err == nil {
		t.Fatal("LoadConfig error = nil, want malformed JSON failure")
	}
}

func TestConfigAllowsCaseInsensitiveCommand(t *testing.T) {
	config := Config{AllowedCommands: []string{"Codex"}}
	config.Normalize()
	if !config.Allows("codex") {
		t.Fatal("Allows(codex) = false, want true")
	}
}

func testPaths(t *testing.T) Paths {
	t.Helper()
	root, err := os.MkdirTemp("", "td-")
	if err != nil {
		t.Fatalf("MkdirTemp returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
	})
	return Paths{
		ConfigDir:                filepath.Join(root, "c"),
		ConfigFile:               filepath.Join(root, "c", "daemon.json"),
		StateDir:                 filepath.Join(root, "s"),
		RuntimeDir:               filepath.Join(root, "r"),
		SocketPath:               filepath.Join(root, "r", "d.sock"),
		TmuxSocketPath:           filepath.Join(root, "r", "tmux.sock"),
		PIDFile:                  filepath.Join(root, "s", "pid"),
		StatusFile:               filepath.Join(root, "s", "status.json"),
		DeviceFile:               filepath.Join(root, "s", "device.json"),
		ConnectivityIdentityFile: filepath.Join(root, "s", "connectivity_identity.json"),
		PairingStateFile:         filepath.Join(root, "s", "pairing_state.json"),
	}
}
