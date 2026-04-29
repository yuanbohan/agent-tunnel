package session

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	xterm "github.com/gitpod-io/xterm-go"
)

const (
	defaultMirrorCols         = 80
	defaultMirrorRows         = 24
	defaultMirrorScrollback   = 10000
	defaultSnapshotScrollback = 10000
	defaultSubmitAnchorLimit  = 256
	DefaultPreviewMaxChars    = 2000
)

type TerminalMirror struct {
	mu               sync.Mutex
	term             *xterm.Terminal
	serializer       *xterm.SerializeAddon
	cols             int
	rows             int
	nextSubmitAnchor int
	submitAnchors    []submitAnchorMarker
	now              func() time.Time
}

type SubmitAnchor struct {
	ID          string
	Line        int
	SubmittedAt int
}

type submitAnchorMarker struct {
	id          string
	marker      *xterm.Marker
	submittedAt int
}

func NewTerminalMirror(cols, rows int) *TerminalMirror {
	if cols <= 0 {
		cols = defaultMirrorCols
	}
	if rows <= 0 {
		rows = defaultMirrorRows
	}

	term := xterm.New(
		xterm.WithCols(cols),
		xterm.WithRows(rows),
		xterm.WithScrollback(defaultMirrorScrollback),
	)

	return &TerminalMirror{
		term:       term,
		serializer: xterm.NewSerializeAddon(term),
		cols:       cols,
		rows:       rows,
		now:        time.Now,
	}
}

func (m *TerminalMirror) WriteOutput(data []byte) {
	if len(data) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	_, _ = m.term.Write(data)
}

func (m *TerminalMirror) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.term.Resize(cols, rows)
	m.cols = cols
	m.rows = rows
}

func (m *TerminalMirror) RecordSubmitAnchor() (SubmitAnchor, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.term.IsAltBufferActive() {
		return SubmitAnchor{}, false
	}

	buffer := m.term.NormalBuffer()
	m.nextSubmitAnchor++
	id := fmt.Sprintf("submit-%d", m.nextSubmitAnchor)
	anchor := submitAnchorMarker{
		id:          id,
		marker:      buffer.AddMarker(buffer.YBase + buffer.Y),
		submittedAt: int(m.now().Unix()),
	}
	m.submitAnchors = append(m.submitAnchors, anchor)
	m.compactSubmitAnchorsLocked()
	return m.liveSubmitAnchorLocked(anchor)
}

func (m *TerminalMirror) RemoveSubmitAnchor(id string) {
	if id == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, anchor := range m.submitAnchors {
		if anchor.id == id && anchor.marker != nil {
			anchor.marker.Dispose()
			break
		}
	}
	m.compactSubmitAnchorsLocked()
}

func (m *TerminalMirror) Snapshot() ([]byte, int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, _ := m.snapshotLocked()
	return append([]byte(nil), snapshot...), m.cols, m.rows
}

func (m *TerminalMirror) SnapshotWithSubmitAnchors() ([]byte, int, int, []SubmitAnchor) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot, startLine := m.snapshotLocked()
	anchors := m.submitAnchorsForSnapshotLocked(startLine)
	return append([]byte(nil), snapshot...), m.cols, m.rows, anchors
}

func (m *TerminalMirror) ViewportText() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.term.String()
}

func (m *TerminalMirror) PreviewText(maxChars int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return NormalizePreviewText(m.term.String(), maxChars)
}

func NormalizePreviewText(raw string, maxChars int) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")

	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r == '\n':
			b.WriteRune('\n')
		case r == '\t':
			b.WriteRune(' ')
		case r == utf8.RuneError:
			continue
		case unicode.IsControl(r):
			continue
		default:
			b.WriteRune(r)
		}
	}

	lines := strings.Split(b.String(), "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	normalized := strings.TrimSpace(strings.Join(lines, "\n"))
	if maxChars <= 0 || len([]rune(normalized)) <= maxChars {
		return normalized
	}
	runes := []rune(normalized)
	tail := string(runes[len(runes)-maxChars:])
	if newline := strings.IndexRune(tail, '\n'); newline >= 0 && newline+1 < len(tail) {
		tail = tail[newline+1:]
	}
	return strings.TrimSpace(tail)
}

func (m *TerminalMirror) AltBufferActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.term.IsAltBufferActive()
}

func (m *TerminalMirror) snapshotLocked() ([]byte, int) {
	scrollback := defaultSnapshotScrollback
	startLine := snapshotStartLine(m.term.NormalBuffer(), m.rows, scrollback)
	snapshot := m.serializer.Serialize(&xterm.SerializeOptions{
		Scrollback: &scrollback,
	})
	return snapshot, startLine
}

func (m *TerminalMirror) submitAnchorsForSnapshotLocked(startLine int) []SubmitAnchor {
	m.compactSubmitAnchorsLocked()
	if m.term.IsAltBufferActive() {
		return nil
	}

	buffer := m.term.NormalBuffer()
	endLine := buffer.Lines.Length() - 1
	out := make([]SubmitAnchor, 0, len(m.submitAnchors))
	for _, anchor := range m.submitAnchors {
		if mapped, ok := m.submitAnchorForBufferLocked(anchor, startLine); ok {
			if mapped.Line+startLine <= endLine {
				out = append(out, mapped)
			}
		}
	}
	return out
}

func (m *TerminalMirror) submitAnchorForBufferLocked(anchor submitAnchorMarker, startLine int) (SubmitAnchor, bool) {
	if anchor.marker == nil || anchor.marker.IsDisposed {
		return SubmitAnchor{}, false
	}
	line := anchor.marker.Line
	if line < startLine {
		return SubmitAnchor{}, false
	}
	return SubmitAnchor{
		ID:          anchor.id,
		Line:        line - startLine,
		SubmittedAt: anchor.submittedAt,
	}, true
}

// liveSubmitAnchorLocked returns the anchor with a line relative to the
// current terminal buffer (absolute marker position). This is distinct from
// submitAnchorForBufferLocked which maps lines relative to a snapshot's
// serialized start line. Live anchors are interpreted against the client's
// attached terminal state when the event is received, not against a snapshot.
func (m *TerminalMirror) liveSubmitAnchorLocked(anchor submitAnchorMarker) (SubmitAnchor, bool) {
	if anchor.marker == nil || anchor.marker.IsDisposed {
		return SubmitAnchor{}, false
	}
	return SubmitAnchor{
		ID:          anchor.id,
		Line:        anchor.marker.Line,
		SubmittedAt: anchor.submittedAt,
	}, true
}

func (m *TerminalMirror) compactSubmitAnchorsLocked() {
	kept := m.submitAnchors[:0]
	for _, anchor := range m.submitAnchors {
		if anchor.marker == nil || anchor.marker.IsDisposed {
			continue
		}
		kept = append(kept, anchor)
	}
	m.submitAnchors = kept

	if extra := len(m.submitAnchors) - defaultSubmitAnchorLimit; extra > 0 {
		for _, anchor := range m.submitAnchors[:extra] {
			anchor.marker.Dispose()
		}
		copy(m.submitAnchors, m.submitAnchors[extra:])
		m.submitAnchors = m.submitAnchors[:len(m.submitAnchors)-extra]
	}
}

func snapshotStartLine(buffer *xterm.Buffer, rows, scrollback int) int {
	maxRows := buffer.Lines.Length()
	correctRows := scrollback + rows
	if correctRows < 0 {
		correctRows = 0
	}
	if correctRows > maxRows {
		correctRows = maxRows
	}
	return maxRows - correctRows
}
