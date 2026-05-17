package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderTablePreservesWideIdentifierColumn(t *testing.T) {
	fingerprint := strings.Repeat("a", 64)
	var out bytes.Buffer
	renderTable(&out, []tableColumn{
		{title: "Fingerprint", width: 64},
		{title: "Name", width: 8},
	}, [][]string{{fingerprint, "Pixel Phone With A Long Name"}})

	got := out.String()
	if !strings.Contains(got, fingerprint) {
		t.Fatalf("table = %q, want full fingerprint", got)
	}
	if !strings.Contains(got, "Pixel...") {
		t.Fatalf("table = %q, want name truncated after identifier is preserved", got)
	}
}

func TestTruncateMiddleCellKeepsPathEnds(t *testing.T) {
	got := truncateMiddleCell("/Users/alice/workspace/github.com/example/repo", 32)
	if len([]rune(got)) != 32 {
		t.Fatalf("len(%q) = %d, want 32", got, len([]rune(got)))
	}
	if !strings.HasPrefix(got, "/Users/alice/") || !strings.HasSuffix(got, "example/repo") {
		t.Fatalf("truncateMiddleCell = %q, want leading and trailing path context", got)
	}
}

func TestRenderTableStripsTerminalControlSequences(t *testing.T) {
	var out bytes.Buffer
	renderTable(&out, []tableColumn{{title: "Name", width: 32}}, [][]string{{
		"\x1b]0;pwnd\aPixel\x1b[31m Phone\x1b[0m\nNext",
	}})

	got := out.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\a") {
		t.Fatalf("table = %q, want terminal control sequences stripped", got)
	}
	if !strings.Contains(got, "Pixel Phone Next") {
		t.Fatalf("table = %q, want sanitized visible text", got)
	}
}

func TestTerminalDisplayValueFallsBackAfterSanitizingControlOnlyValue(t *testing.T) {
	if got := terminalDisplayValue("\x1b[31m\x1b[0m", "unknown"); got != "unknown" {
		t.Fatalf("terminalDisplayValue = %q, want fallback", got)
	}
}
