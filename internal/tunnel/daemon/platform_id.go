package daemon

import (
	"bufio"
	"os"
	"strings"
)

func detectLinuxPlatformID() string {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return PlatformIDUnknown
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "ID=") {
			continue
		}
		value := strings.TrimPrefix(line, "ID=")
		return strings.Trim(strings.TrimSpace(value), `"`)
	}
	return PlatformIDUnknown
}
