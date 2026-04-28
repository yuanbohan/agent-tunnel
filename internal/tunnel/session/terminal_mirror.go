package session

import (
	"fmt"
	"sync"
	"time"

	xterm "github.com/gitpod-io/xterm-go"
)

const (
	defaultMirrorCols         = 80
	defaultMirrorRows         = 24
	defaultMirrorScrollback   = 256
	defaultSnapshotScrollback = 256
	defaultSubmitAnchorLimit  = 256
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

func (m *TerminalMirror) RecordSubmitAnchor() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.term.IsAltBufferActive() {
		return ""
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
	return id
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
		if anchor.marker == nil || anchor.marker.IsDisposed {
			continue
		}
		line := anchor.marker.Line
		if line < startLine || line > endLine {
			continue
		}
		out = append(out, SubmitAnchor{
			ID:          anchor.id,
			Line:        line - startLine,
			SubmittedAt: anchor.submittedAt,
		})
	}
	return out
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
