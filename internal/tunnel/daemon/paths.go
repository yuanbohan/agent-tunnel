package daemon

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

const (
	appDirName                          = "tunnel"
	daemonDirName                       = "daemon"
	defaultConfigFileName               = "daemon.json"
	defaultSocketFileName               = "daemon.sock"
	defaultBrokerSocketFileName         = "broker.sock"
	defaultTmuxSocketName               = "tmux.sock"
	defaultStartupLockFileName          = "startup.lock"
	defaultPIDFileName                  = "daemon.pid"
	defaultStatusFileName               = "status.json"
	defaultDeviceFileName               = "device.json"
	defaultConnectivityIdentityFileName = "connectivity_identity.json"
	defaultPairingStateFileName         = "pairing_state.json"
)

type Paths struct {
	ConfigDir                string
	ConfigFile               string
	StateDir                 string
	RuntimeDir               string
	SocketPath               string
	BrokerSocketPath         string
	TmuxSocketPath           string
	PIDFile                  string
	StatusFile               string
	DeviceFile               string
	ConnectivityIdentityFile string
	PairingStateFile         string
}

func ResolvePaths() (Paths, error) {
	configBase, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	cacheBase, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, err
	}
	runtimeBase, err := runtimeBaseDir()
	if err != nil {
		return Paths{}, err
	}

	configDir := filepath.Join(configBase, appDirName)
	stateDir := filepath.Join(cacheBase, appDirName, daemonDirName)
	runtimeDir := filepath.Join(runtimeBase, appDirName, daemonDirName)
	return Paths{
		ConfigDir:                configDir,
		ConfigFile:               filepath.Join(configDir, defaultConfigFileName),
		StateDir:                 stateDir,
		RuntimeDir:               runtimeDir,
		SocketPath:               filepath.Join(runtimeDir, defaultSocketFileName),
		BrokerSocketPath:         filepath.Join(runtimeDir, defaultBrokerSocketFileName),
		TmuxSocketPath:           filepath.Join(runtimeDir, defaultTmuxSocketName),
		PIDFile:                  filepath.Join(stateDir, defaultPIDFileName),
		StatusFile:               filepath.Join(stateDir, defaultStatusFileName),
		DeviceFile:               filepath.Join(stateDir, defaultDeviceFileName),
		ConnectivityIdentityFile: filepath.Join(stateDir, defaultConnectivityIdentityFileName),
		PairingStateFile:         filepath.Join(stateDir, defaultPairingStateFileName),
	}, nil
}

func EnsureRuntimeDirs(paths Paths) error {
	for _, dir := range []string{paths.ConfigDir, paths.StateDir, paths.RuntimeDir} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func runtimeBaseDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); dir != "" {
		return dir, nil
	}

	tmp := os.TempDir()
	suffix := "unknown"
	if current, err := user.Current(); err == nil {
		if trimmed := strings.TrimSpace(current.Uid); trimmed != "" {
			suffix = trimmed
		} else if trimmed := strings.TrimSpace(current.Username); trimmed != "" {
			suffix = sanitizePathComponent(trimmed)
		}
	}
	return filepath.Join(tmp, fmt.Sprintf("%s-%s", appDirName, suffix)), nil
}

func sanitizePathComponent(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-")
	return replacer.Replace(raw)
}
