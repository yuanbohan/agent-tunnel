package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	tunnelConfigDirName    = ".tunnel"
	tunnelAuthFileName     = "auth.json"
	tunnelSettingsFileName = "settings.json"
	tunnelUpdaterFileName  = "updater.json"
	updaterSchemaVersion   = 1
)

var errRecoverableUpdaterState = errors.New("recoverable updater state")

type localStatePaths struct {
	RootDir      string
	AuthFile     string
	SettingsFile string
	UpdaterFile  string
}

type tunnelSettings struct {
	Env map[string]string `json:"env,omitempty"`
}

type updaterState struct {
	Version         int    `json:"version"`
	LastCheckedAt   int64  `json:"last_checked_at,omitempty"`
	RollbackVersion string `json:"rollback_version,omitempty"`
	RollbackReason  string `json:"rollback_reason,omitempty"`
}

func tunnelConfigDirPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", fmt.Errorf("resolve home directory: empty path")
	}
	return filepath.Join(home, tunnelConfigDirName), nil
}

func resolveLocalStatePaths() (localStatePaths, error) {
	root, err := tunnelConfigDirPath()
	if err != nil {
		return localStatePaths{}, err
	}
	if err := rejectSymlinkPath(root, "config dir"); err != nil {
		return localStatePaths{}, err
	}
	return localStatePaths{
		RootDir:      root,
		AuthFile:     filepath.Join(root, tunnelAuthFileName),
		SettingsFile: filepath.Join(root, tunnelSettingsFileName),
		UpdaterFile:  filepath.Join(root, tunnelUpdaterFileName),
	}, nil
}

func ensurePrivateDir(dir string) error {
	if err := rejectSymlinkPath(dir, "config dir"); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}
	if err := rejectSymlinkPath(dir, "config dir"); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod config dir %s: %w", dir, err)
	}
	return nil
}

func writePrivateJSONFile(path string, value any) error {
	dir := filepath.Dir(path)
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}
	if err := rejectSymlinkPath(path, "config file"); err != nil {
		return err
	}

	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON for %s: %w", path, err)
	}
	payload = append(payload, '\n')

	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tmpPath := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmpFile.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp file for %s: %w", path, err)
	}
	if _, err := tmpFile.Write(payload); err != nil {
		cleanup()
		return fmt.Errorf("write temp file for %s: %w", path, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file for %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

func loadTunnelSettings() (tunnelSettings, error) {
	paths, err := resolveLocalStatePaths()
	if err != nil {
		return tunnelSettings{}, err
	}
	if err := rejectSymlinkPath(paths.SettingsFile, "settings file"); err != nil {
		return tunnelSettings{}, err
	}

	payload, err := os.ReadFile(paths.SettingsFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return tunnelSettings{}, nil
		}
		return tunnelSettings{}, fmt.Errorf("read settings file %s: %w", paths.SettingsFile, err)
	}

	var settings tunnelSettings
	if err := json.Unmarshal(payload, &settings); err != nil {
		return tunnelSettings{}, fmt.Errorf("parse settings file %s: %w", paths.SettingsFile, err)
	}
	if settings.Env == nil {
		settings.Env = make(map[string]string)
	}
	return settings, nil
}

func settingsEnv(key string, getenv func(string) string) (string, error) {
	if getenv != nil {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			return value, nil
		}
	}

	settings, err := loadTunnelSettings()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(settings.Env[key]), nil
}

func loadUpdaterState() (updaterState, error) {
	paths, err := resolveLocalStatePaths()
	if err != nil {
		return updaterState{}, err
	}
	if err := rejectSymlinkPath(paths.UpdaterFile, "updater state file"); err != nil {
		return updaterState{}, err
	}

	payload, err := os.ReadFile(paths.UpdaterFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return updaterState{Version: updaterSchemaVersion}, nil
		}
		return updaterState{}, fmt.Errorf("%w: read updater state %s: %w", errRecoverableUpdaterState, paths.UpdaterFile, err)
	}

	var state updaterState
	if err := json.Unmarshal(payload, &state); err != nil {
		return updaterState{}, fmt.Errorf("%w: parse updater state %s: %w", errRecoverableUpdaterState, paths.UpdaterFile, err)
	}
	if state.Version == 0 {
		state.Version = updaterSchemaVersion
	}
	return state, nil
}

func saveUpdaterState(state updaterState) error {
	paths, err := resolveLocalStatePaths()
	if err != nil {
		return err
	}
	state.Version = updaterSchemaVersion
	return writePrivateJSONFile(paths.UpdaterFile, state)
}

func rejectSymlinkPath(path, kind string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect %s %s: %w", kind, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %s must not be a symlink", kind, path)
	}
	return nil
}
