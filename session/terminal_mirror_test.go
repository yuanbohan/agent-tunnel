package session

import (
	"strings"
	"testing"

	xterm "github.com/gitpod-io/xterm-go"
)

func snapshotRoundTrip(t *testing.T, mirror *TerminalMirror) (*xterm.Terminal, []byte, int, int) {
	t.Helper()

	snapshot, cols, rows := mirror.Snapshot()
	restored := xterm.New(xterm.WithCols(cols), xterm.WithRows(rows), xterm.WithScrollback(0))
	_, _ = restored.Write(snapshot)
	return restored, snapshot, cols, rows
}

func viewportCell(t *testing.T, term *xterm.Terminal, row, col int) *xterm.CellData {
	t.Helper()

	line := term.Buffer().Lines.Get(term.Buffer().YBase + row)
	if line == nil {
		t.Fatalf("line %d = nil", row)
	}
	cell := xterm.NewCellData()
	line.LoadCell(col, cell)
	return cell
}

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

func TestTerminalMirrorSnapshotPreservesStyledText(t *testing.T) {
	mirror := NewTerminalMirror(20, 5)
	term := xterm.New(xterm.WithCols(20), xterm.WithRows(5), xterm.WithScrollback(0))
	input := []byte("\x1b[1;4;38;2;50;100;150mA")

	mirror.WriteOutput(input)
	_, _ = term.Write(input)

	restored, _, _, _ := snapshotRoundTrip(t, mirror)
	want := viewportCell(t, term, 0, 0)
	got := viewportCell(t, restored, 0, 0)

	if got.GetChars() != "A" {
		t.Fatalf("chars = %q, want %q", got.GetChars(), "A")
	}
	if !got.AttributesEqual(want) {
		t.Fatalf("restored attributes do not match original: got=%#v want=%#v", got, want)
	}
}

func TestTerminalMirrorSnapshotPreservesWideCharacters(t *testing.T) {
	mirror := NewTerminalMirror(20, 5)
	term := xterm.New(xterm.WithCols(20), xterm.WithRows(5), xterm.WithScrollback(0))
	input := []byte("世A")

	mirror.WriteOutput(input)
	_, _ = term.Write(input)

	restored, _, _, _ := snapshotRoundTrip(t, mirror)

	for _, col := range []int{0, 1, 2} {
		want := viewportCell(t, term, 0, col)
		got := viewportCell(t, restored, 0, col)
		if got.GetChars() != want.GetChars() || got.GetWidth() != want.GetWidth() {
			t.Fatalf("cell[%d] = (%q,%d), want (%q,%d)", col, got.GetChars(), got.GetWidth(), want.GetChars(), want.GetWidth())
		}
	}
}

func TestTerminalMirrorSnapshotPreservesHiddenCursor(t *testing.T) {
	mirror := NewTerminalMirror(20, 5)
	input := []byte("\x1b[?25lhidden cursor")

	mirror.WriteOutput(input)

	restored, _, _, _ := snapshotRoundTrip(t, mirror)
	if !restored.IsCursorHidden() {
		t.Fatal("restored terminal cursor is visible, want hidden")
	}
}
