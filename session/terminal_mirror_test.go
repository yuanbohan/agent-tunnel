package session

import (
	"strings"
	"testing"

	xterm "github.com/gitpod-io/xterm-go"
)

func TestTerminalMirrorSnapshotRoundTripPlainText(t *testing.T) {
	mirror := NewTerminalMirror(20, 5)
	mirror.WriteOutput([]byte("hello\r\nworld"))

	snapshot, cols, rows := mirror.Snapshot()
	if cols != 20 || rows != 5 {
		t.Fatalf("Snapshot size = %dx%d, want 20x5", cols, rows)
	}

	restored := xterm.New(xterm.WithCols(cols), xterm.WithRows(rows), xterm.WithScrollback(0))
	_, _ = restored.Write(snapshot)

	if got := restored.String(); got != "hello\nworld" {
		t.Fatalf("restored viewport = %q, want %q", got, "hello\nworld")
	}
}

func TestTerminalMirrorSnapshotPreservesAlternateBuffer(t *testing.T) {
	mirror := NewTerminalMirror(20, 5)
	mirror.WriteOutput([]byte("\x1b[?1049h"))
	mirror.WriteOutput([]byte("alt screen"))

	snapshot, cols, rows := mirror.Snapshot()
	restored := xterm.New(xterm.WithCols(cols), xterm.WithRows(rows), xterm.WithScrollback(0))
	_, _ = restored.Write(snapshot)

	if !restored.IsAltBufferActive() {
		t.Fatal("restored terminal is not in alt buffer")
	}
	if !strings.Contains(restored.String(), "alt screen") {
		t.Fatalf("restored viewport = %q, want alt screen content", restored.String())
	}
}

func TestTerminalMirrorSnapshotExcludesScrollback(t *testing.T) {
	mirror := NewTerminalMirror(20, 3)
	mirror.WriteOutput([]byte("line1\r\nline2\r\nline3\r\nline4"))

	snapshot, cols, rows := mirror.Snapshot()
	restored := xterm.New(xterm.WithCols(cols), xterm.WithRows(rows), xterm.WithScrollback(0))
	_, _ = restored.Write(snapshot)

	got := restored.String()
	if strings.Contains(got, "line1") {
		t.Fatalf("restored viewport = %q, did not expect scrollback line1", got)
	}
	for _, want := range []string{"line2", "line3", "line4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("restored viewport = %q, want %q", got, want)
		}
	}
}

func TestTerminalMirrorResizeAffectsSnapshotSize(t *testing.T) {
	mirror := NewTerminalMirror(80, 24)
	mirror.Resize(100, 40)

	_, cols, rows := mirror.Snapshot()
	if cols != 100 || rows != 40 {
		t.Fatalf("Snapshot size = %dx%d, want 100x40", cols, rows)
	}
}
