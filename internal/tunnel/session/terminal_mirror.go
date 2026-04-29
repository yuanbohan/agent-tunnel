package session

import (
	"sync"

	xterm "github.com/gitpod-io/xterm-go"
)

const (
	defaultMirrorCols         = 80
	defaultMirrorRows         = 24
	defaultMirrorScrollback   = 10000
	defaultSnapshotScrollback = 10000
)

type TerminalMirror struct {
	mu         sync.Mutex
	term       *xterm.Terminal
	serializer *xterm.SerializeAddon
	cols       int
	rows       int
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

func (m *TerminalMirror) Snapshot() ([]byte, int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot := m.snapshotLocked()
	return append([]byte(nil), snapshot...), m.cols, m.rows
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

func (m *TerminalMirror) snapshotLocked() []byte {
	scrollback := defaultSnapshotScrollback
	snapshot := m.serializer.Serialize(&xterm.SerializeOptions{
		Scrollback: &scrollback,
	})
	return snapshot
}
