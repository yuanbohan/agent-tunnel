package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	LaunchHealthUnknown  = "unknown"
	LaunchHealthHealthy  = "healthy"
	LaunchHealthDegraded = "degraded"

	PlatformFamilyMacOS = "macos"
	PlatformFamilyLinux = "linux"

	PlatformIDUnknown = "unknown"
)

var (
	readOrCreateDeviceIdentityFn = ReadOrCreateDeviceIdentity
	collectDeviceMetadataFn      = CollectDeviceMetadata
)

type DeviceIdentity struct {
	DeviceID string `json:"device_id"`
}

type DeviceMetadata struct {
	DisplayName    string `json:"display_name"`
	Hostname       string `json:"hostname"`
	PlatformFamily string `json:"platform_family"`
	PlatformID     string `json:"platform_id"`
}

func ReadOrCreateDeviceIdentity(paths Paths) (DeviceIdentity, error) {
	payload, err := os.ReadFile(paths.DeviceFile)
	if err == nil {
		var identity DeviceIdentity
		if err := json.Unmarshal(payload, &identity); err != nil {
			return DeviceIdentity{}, err
		}
		if strings.TrimSpace(identity.DeviceID) == "" {
			return DeviceIdentity{}, fmt.Errorf("daemon device identity at %s is missing device_id", paths.DeviceFile)
		}
		return identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return DeviceIdentity{}, err
	}

	deviceID, err := newOpaqueID("dev", 12)
	if err != nil {
		return DeviceIdentity{}, err
	}
	identity := DeviceIdentity{DeviceID: deviceID}
	if err := writeJSONFile(paths.DeviceFile, identity); err != nil {
		return DeviceIdentity{}, err
	}
	return identity, nil
}

func CollectDeviceMetadata() DeviceMetadata {
	hostname, _ := os.Hostname()
	displayName := detectDisplayName(hostname)
	family, id := detectPlatform()
	return DeviceMetadata{
		DisplayName:    displayName,
		Hostname:       hostname,
		PlatformFamily: family,
		PlatformID:     id,
	}
}

func CollectSessionMetadata() DeviceMetadata {
	hostname, _ := os.Hostname()
	displayName := detectSessionDisplayName()
	family, id := detectSessionPlatform()
	return DeviceMetadata{
		DisplayName:    displayName,
		Hostname:       hostname,
		PlatformFamily: family,
		PlatformID:     id,
	}
}

func detectDisplayName(hostname string) string {
	switch runtime.GOOS {
	case "darwin":
		if displayName := detectSessionDisplayName(); displayName != "" {
			return displayName
		}
	}
	if trimmed := strings.TrimSpace(hostname); trimmed != "" {
		return trimmed
	}
	return "Unknown Device"
}

func detectSessionDisplayName() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	if output, err := exec.Command("scutil", "--get", "ComputerName").Output(); err == nil {
		if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func detectPlatform() (string, string) {
	switch runtime.GOOS {
	case "darwin":
		return PlatformFamilyMacOS, PlatformFamilyMacOS
	case "linux":
		return PlatformFamilyLinux, detectLinuxPlatformID()
	default:
		return runtime.GOOS, PlatformIDUnknown
	}
}

func detectSessionPlatform() (string, string) {
	switch runtime.GOOS {
	case "darwin":
		return PlatformFamilyMacOS, PlatformFamilyMacOS
	case "linux":
		id, ok := detectSessionLinuxPlatformID()
		if !ok {
			return PlatformFamilyLinux, ""
		}
		return PlatformFamilyLinux, id
	default:
		return "", ""
	}
}

func detectSessionLinuxPlatformID() (string, bool) {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return "", false
	}
	defer file.Close()
	return parseLinuxPlatformIDValue(file)
}

func newOpaqueID(prefix string, numBytes int) (string, error) {
	buffer := make([]byte, numBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(buffer)), nil
}
