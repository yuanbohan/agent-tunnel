package session

import (
	"bytes"
	"testing"
)

func TestStatusLineShowRendersBottomLine(t *testing.T) {
	var buf bytes.Buffer
	line := NewStatusLine(&buf)
	line.SetSize(40, 24)
	line.Show("relay reconnecting; local session continues")

	want := "\x1b7\x1b[24;1H\x1b[2K\x1b[2mrelay reconnecting; local session con...\x1b[0m\x1b8"
	if got := buf.String(); got != want {
		t.Fatalf("render = %q, want %q", got, want)
	}
}

func TestStatusLineClearRemovesVisibleLine(t *testing.T) {
	var buf bytes.Buffer
	line := NewStatusLine(&buf)
	line.SetSize(20, 10)
	line.Show("relay reconnecting")
	buf.Reset()

	line.Clear()

	if got := buf.String(); got != "\x1b7\x1b[10;1H\x1b[2K\x1b8" {
		t.Fatalf("clear = %q", got)
	}
}

func TestStatusLineSetSizeRedrawsVisibleMessage(t *testing.T) {
	var buf bytes.Buffer
	line := NewStatusLine(&buf)
	line.SetSize(20, 10)
	line.Show("relay reconnecting")
	buf.Reset()

	line.SetSize(30, 12)

	want := "\x1b7\x1b[12;1H\x1b[2K\x1b[2mrelay reconnecting\x1b[0m\x1b8"
	if got := buf.String(); got != want {
		t.Fatalf("redraw = %q, want %q", got, want)
	}
}

func TestStatusLineFallsBackToNoticeWithoutTerminalSize(t *testing.T) {
	var buf bytes.Buffer
	line := NewStatusLine(&buf)
	line.Show("relay reconnecting")

	if got := buf.String(); got != "[agentunnel] relay reconnecting\n" {
		t.Fatalf("fallback = %q", got)
	}
}

func TestTruncateStatusLinePreservesShortMessages(t *testing.T) {
	if got := truncateStatusLine("ok", 10); got != "ok" {
		t.Fatalf("truncate short = %q", got)
	}
}
