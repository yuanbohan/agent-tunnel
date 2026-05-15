package main

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tableColumn struct {
	title          string
	width          int
	truncateMiddle bool
}

func renderTable(w io.Writer, columns []tableColumn, rows [][]string) {
	border := tableBorder(columns)
	_, _ = io.WriteString(w, border)
	renderTableLine(w, columns, func(col int) string { return columns[col].title })
	_, _ = io.WriteString(w, border)
	for _, row := range rows {
		renderTableLine(w, columns, func(col int) string {
			if col >= len(row) {
				return ""
			}
			return row[col]
		})
	}
	_, _ = io.WriteString(w, border)
}

func renderTableLine(w io.Writer, columns []tableColumn, value func(int) string) {
	_, _ = io.WriteString(w, "|")
	for idx, col := range columns {
		_, _ = fmt.Fprintf(w, " %-*s |", col.width, tableCell(value(idx), col))
	}
	_, _ = io.WriteString(w, "\n")
}

func tableBorder(columns []tableColumn) string {
	var b strings.Builder
	b.WriteString("+")
	for _, col := range columns {
		b.WriteString(strings.Repeat("-", col.width+2))
		b.WriteString("+")
	}
	b.WriteString("\n")
	return b.String()
}

func tableCell(value string, col tableColumn) string {
	if col.truncateMiddle {
		return truncateMiddleCell(value, col.width)
	}
	return truncateCell(value, col.width)
}

func truncateCell(value string, width int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(terminalSafeText(value))), " ")
	if value == "" {
		value = "-"
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func truncateMiddleCell(value string, width int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(terminalSafeText(value))), " ")
	if value == "" {
		return "-"
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 3 {
		return string(runes[:width])
	}
	remaining := width - 3
	left := (remaining + 1) / 2
	right := remaining - left
	return string(runes[:left]) + "..." + string(runes[len(runes)-right:])
}

func emptyValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func terminalDisplayValue(value, fallback string) string {
	value = strings.TrimSpace(terminalSafeText(value))
	if value == "" {
		return fallback
	}
	return value
}

func terminalSafeText(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		if r == '\x1b' {
			i += terminalEscapeSequenceLength(value[i:])
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			if r == '\n' || r == '\r' || r == '\t' {
				b.WriteByte(' ')
			}
			i += size
			continue
		}
		if unicode.Is(unicode.Cf, r) {
			i += size
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func terminalEscapeSequenceLength(value string) int {
	if len(value) < 2 || value[0] != '\x1b' {
		return 1
	}
	switch value[1] {
	case '[':
		for i := 2; i < len(value); i++ {
			if value[i] >= 0x40 && value[i] <= 0x7e {
				return i + 1
			}
		}
		return len(value)
	case ']':
		for i := 2; i < len(value); i++ {
			if value[i] == '\a' {
				return i + 1
			}
			if value[i] == '\x1b' && i+1 < len(value) && value[i+1] == '\\' {
				return i + 2
			}
		}
		return len(value)
	default:
		return 2
	}
}
