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

	return parseLinuxPlatformID(file)
}

func parseLinuxPlatformID(r io.Reader) string {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "ID=") {
			continue
		}
		value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "ID=")), `"`)
		if value == "" {
			return PlatformIDUnknown
		}
		return value
	}
	return PlatformIDUnknown
}
