package session

import (
	"fmt"
	"strings"
	"testing"

	xterm "github.com/gitpod-io/xterm-go"
)

func snapshotRoundTrip(t *testing.T, mirror *TerminalMirror) (*xterm.Terminal, []byte, int, int) {
	t.Helper()

	snapshot, cols, rows := mirror.Snapshot()
	restored := xterm.New(xterm.WithCols(cols), xterm.WithRows(rows), xterm.WithScrollback(defaultMirrorScrollback))
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

func bufferText(t *testing.T, buf *xterm.Buffer) []string {
	t.Helper()

	lines := make([]string, buf.Lines.Length())
	for i := range lines {
		line := buf.Lines.Get(i)
		if line == nil {
			t.Fatalf("line %d = nil", i)
		}
		lines[i] = line.TranslateToString(true, 0, -1)
	}
	return lines
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
	mirror := NewTerminalMirror(20, 2)
	mirror.WriteOutput([]byte("shell line 1\r\nshell line 2\r\nshell line 3"))
	mirror.WriteOutput([]byte("\x1b[?1049h"))
	mirror.WriteOutput([]byte("alt line 1\r\nalt line 2\r\nalt line 3"))

	snapshot, cols, rows := mirror.Snapshot()
	restored := xterm.New(xterm.WithCols(cols), xterm.WithRows(rows), xterm.WithScrollback(defaultMirrorScrollback))
	_, _ = restored.Write(snapshot)

	if !restored.IsAltBufferActive() {
		t.Fatal("restored terminal is not in alt buffer")
	}
	if got := restored.String(); !strings.Contains(got, "alt line 2") || !strings.Contains(got, "alt line 3") {
		t.Fatalf("restored alt viewport = %q, want latest alt content", got)
	}
	if got := restored.String(); strings.Contains(got, "alt line 1") {
		t.Fatalf("restored alt viewport = %q, did not expect trimmed alt line", got)
	}

	normalBuffer := strings.Join(bufferText(t, restored.NormalBuffer()), "\n")
	for _, want := range []string{"shell line 1", "shell line 2", "shell line 3"} {
		if !strings.Contains(normalBuffer, want) {
			t.Fatalf("restored normal buffer = %q, want %q", normalBuffer, want)
		}
	}
}

func TestTerminalMirrorSnapshotIncludesBoundedScrollback(t *testing.T) {
	mirror := NewTerminalMirror(20, 3)
	totalLines := defaultSnapshotScrollback + 8
	for i := range totalLines {
		if i > 0 {
			mirror.WriteOutput([]byte("\r\n"))
		}
		mirror.WriteOutput([]byte(fmt.Sprintf("line%03d", i)))
	}

	snapshot, cols, rows := mirror.Snapshot()
	restored := xterm.New(xterm.WithCols(cols), xterm.WithRows(rows), xterm.WithScrollback(defaultMirrorScrollback))
	_, _ = restored.Write(snapshot)

	gotViewport := restored.String()
	for _, want := range []string{
		fmt.Sprintf("line%03d", totalLines-3),
		fmt.Sprintf("line%03d", totalLines-2),
		fmt.Sprintf("line%03d", totalLines-1),
	} {
		if !strings.Contains(gotViewport, want) {
			t.Fatalf("restored viewport = %q, want %q", gotViewport, want)
		}
	}

	gotBuffer := strings.Join(bufferText(t, restored.NormalBuffer()), "\n")
	oldestKept := totalLines - (defaultSnapshotScrollback + rows)
	if !strings.Contains(gotBuffer, fmt.Sprintf("line%03d", oldestKept)) {
		t.Fatalf("restored buffer = %q, want oldest kept line line%03d", gotBuffer, oldestKept)
	}
	if strings.Contains(gotBuffer, "line000") {
		t.Fatalf("restored buffer = %q, did not expect trimmed oldest line", gotBuffer)
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

func TestTerminalMirrorPreviewTextIsPlainText(t *testing.T) {
	mirror := NewTerminalMirror(20, 5)
	mirror.WriteOutput([]byte("\x1b[31mred\x1b[0m\ttext\r\nnext\x00line"))

	got := mirror.PreviewText(DefaultPreviewMaxChars)
	if got != "red text\nnextline" {
		t.Fatalf("PreviewText = %q, want plain normalized text", got)
	}
}

func TestNormalizePreviewTextKeepsRecentBoundedContent(t *testing.T) {
	got := NormalizePreviewText("alpha\nbravo\ncharlie\n", 12)
	if got != "charlie" {
		t.Fatalf("NormalizePreviewText = %q, want recent bounded content", got)
	}
}
