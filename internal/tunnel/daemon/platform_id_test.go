package daemon

import (
	"strings"
	"testing"
)

func TestParseLinuxPlatformIDReturnsUnknownForBlankID(t *testing.T) {
	got := parseLinuxPlatformID(strings.NewReader("NAME=Ubuntu\nID=\n"))
	if got != PlatformIDUnknown {
		t.Fatalf("parseLinuxPlatformID() = %q, want %q", got, PlatformIDUnknown)
	}
}

func TestParseLinuxPlatformIDReturnsTrimmedID(t *testing.T) {
	got := parseLinuxPlatformID(strings.NewReader("NAME=Ubuntu\nID=\"ubuntu\"\n"))
	if got != "ubuntu" {
		t.Fatalf("parseLinuxPlatformID() = %q, want ubuntu", got)
	}
}
