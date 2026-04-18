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

func TestParseLinuxPlatformIDValuePreservesLiteralUnknown(t *testing.T) {
	got, ok := parseLinuxPlatformIDValue(strings.NewReader("NAME=Unknown Linux\nID=unknown\n"))
	if !ok {
		t.Fatal("parseLinuxPlatformIDValue() ok = false, want true")
	}
	if got != "unknown" {
		t.Fatalf("parseLinuxPlatformIDValue() = %q, want unknown", got)
	}
}
