package daemon

import (
	"bufio"
	"io"
	"os"
	"strings"
)

func detectLinuxPlatformID() string {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return PlatformIDUnknown
	}
	defer file.Close()

	id, ok := parseLinuxPlatformIDValue(file)
	if !ok {
		return PlatformIDUnknown
	}
	return id
}

func parseLinuxPlatformID(r io.Reader) string {
	id, ok := parseLinuxPlatformIDValue(r)
	if !ok {
		return PlatformIDUnknown
	}
	return id
}

func parseLinuxPlatformIDValue(r io.Reader) (string, bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "ID=") {
			continue
		}
		value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "ID=")), `"`)
		if value == "" {
			return "", false
		}
		return value, true
	}
	return "", false
}
