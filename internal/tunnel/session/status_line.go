package session

import (
	"fmt"
	"io"
	"sync"
)

// StatusLine paints a single transient status line near the bottom of the
// terminal without mutating normal PTY output flow.
type StatusLine struct {
	mu      sync.Mutex
	writer  io.Writer
	cols    int
	rows    int
	visible bool
	message string
}

func NewStatusLine(writer io.Writer) *StatusLine {
	return &StatusLine{writer: writer}
}

func (s *StatusLine) SetSize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cols = cols
	s.rows = rows
	if s.visible {
		s.renderLocked(s.message)
	}
}

func (s *StatusLine) Show(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.message = message
	s.visible = true
	s.renderLocked(message)
}

func (s *StatusLine) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.visible {
		return
	}
	s.visible = false
	s.message = ""
	s.clearLocked()
}

func (s *StatusLine) renderLocked(message string) {
	if s.writer == nil || message == "" {
		return
	}
	if s.rows <= 0 || s.cols <= 0 {
		fmt.Fprintf(s.writer, "[tunnel] %s\n", message)
		return
	}

	line := truncateStatusLine(message, s.cols)
	fmt.Fprintf(s.writer, "\x1b7\x1b[%d;1H\x1b[2K\x1b[2m%s\x1b[0m\x1b8", s.rows, line)
}

func (s *StatusLine) clearLocked() {
	if s.writer == nil || s.rows <= 0 {
		return
	}
	fmt.Fprintf(s.writer, "\x1b7\x1b[%d;1H\x1b[2K\x1b8", s.rows)
}

func truncateStatusLine(message string, cols int) string {
	if cols <= 0 {
		return message
	}
	runes := []rune(message)
	if len(runes) <= cols {
		return message
	}
	if cols <= 3 {
		return string(runes[:cols])
	}
	return string(runes[:cols-3]) + "..."
}
