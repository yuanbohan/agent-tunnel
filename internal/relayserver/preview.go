package relayserver

import (
	"regexp"
	"strings"
	"unicode"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func ExtractPreview(raw []byte) (string, bool) {
	clean := ansiPattern.ReplaceAllString(string(raw), "")
	lines := strings.Split(clean, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if !looksTextLike(line) {
			continue
		}
		if len(line) > 140 {
			line = line[:140]
		}
		return line, true
	}
	return "", false
}

func looksTextLike(s string) bool {
	for _, r := range s {
		if unicode.IsPrint(r) {
			return true
		}
	}
	return false
}
