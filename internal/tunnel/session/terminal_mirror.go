package session

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	xterm "github.com/gitpod-io/xterm-go"
)

const (
	defaultMirrorCols         = 80
	defaultMirrorRows         = 24
	defaultMirrorScrollback   = 10000
	defaultSnapshotScrollback = 10000
	DefaultPreviewMaxChars    = 2000
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

func (m *TerminalMirror) snapshotLocked() []byte {
	scrollback := defaultSnapshotScrollback
	snapshot := m.serializer.Serialize(&xterm.SerializeOptions{
		Scrollback: &scrollback,
	})
	return snapshot
}
