package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"yuanbohan/tunnel/internal/protocol"
)

type sessionRow struct {
	scope   string
	source  string
	id      string
	label   string
	command string
	machine string
	cwd     string
	age     string
}

type tableColumn struct {
	title string
	width int
	value func(sessionRow) string
}

const sessionCWDColumnWidth = 32

var sessionTableColumns = []tableColumn{
	{title: "Scope", width: 7, value: func(row sessionRow) string { return row.scope }},
	{title: "Source", width: 6, value: func(row sessionRow) string { return row.source }},
	{title: "Session", width: 14, value: func(row sessionRow) string { return row.id }},
	{title: "Label", width: 16, value: func(row sessionRow) string { return row.label }},
	{title: "Command", width: 24, value: func(row sessionRow) string { return row.command }},
	{title: "Machine", width: 22, value: func(row sessionRow) string { return row.machine }},
	{title: "CWD", width: sessionCWDColumnWidth, value: func(row sessionRow) string { return row.cwd }},
	{title: "Age", width: 8, value: func(row sessionRow) string { return row.age }},
}

func runSessionList(ctx context.Context, args sessionCommandArgs, stdout, stderr io.Writer) error {
	auth, err := resolveRuntimeAuth(newAuthStore(), osEnv)
	if err != nil {
		return err
	}
	sessions, err := newAuthAPI(args.BaseURL).listSessions(ctx, auth.Token)
	if err != nil {
		return err
	}
	localDeviceID := sessionDeviceID()
	rows := make([]sessionRow, 0, len(sessions))
	now := time.Now()
	for _, info := range sessions {
		rows = append(rows, buildSessionRow(info, localDeviceID, now))
	}
	if len(rows) == 0 {
		_, _ = io.WriteString(stdout, "No live sessions.\n")
		return nil
	}
	renderSessionTable(stdout, rows)
	return nil
}

func runSessionStop(ctx context.Context, args sessionCommandArgs, sessionID string, stdout, stderr io.Writer) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return usageWithHelp(sessionStopHelpText(), "session id must not be empty")
	}
	auth, err := resolveRuntimeAuth(newAuthStore(), osEnv)
	if err != nil {
		return err
	}
	if _, err := newAuthAPI(args.BaseURL).stopSession(ctx, auth.Token, sessionID); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "stopped session %s\n", sessionID)
	return nil
}

func buildSessionRow(info protocol.SessionInfo, localDeviceID string, now time.Time) sessionRow {
	return sessionRow{
		scope:   sessionScope(info, localDeviceID),
		source:  sessionLaunchSource(info.LaunchSource),
		id:      emptyValue(info.SessionID),
		label:   emptyValue(info.Label),
		command: emptyValue(sessionCommand(info)),
		machine: emptyValue(sessionMachine(info)),
		cwd:     emptyValue(sessionCWD(info.CWD)),
		age:     sessionAge(info.StartedAt, now),
	}
}

func sessionScope(info protocol.SessionInfo, localDeviceID string) string {
	localDeviceID = strings.TrimSpace(localDeviceID)
	sessionDeviceID := strings.TrimSpace(info.DeviceID)
	if localDeviceID == "" || sessionDeviceID == "" {
		return "unknown"
	}
	if localDeviceID == sessionDeviceID {
		return "local"
	}
	return "remote"
}

func sessionLaunchSource(launchSource string) string {
	switch strings.TrimSpace(launchSource) {
	case protocol.SessionLaunchSourceMobile:
		return protocol.SessionLaunchSourceMobile
	default:
		return protocol.SessionLaunchSourceLocal
	}
}

func sessionCommand(info protocol.SessionInfo) string {
	if command := strings.TrimSpace(info.CommandPreview); command != "" {
		return command
	}
	return strings.TrimSpace(info.Launcher)
}

func sessionMachine(info protocol.SessionInfo) string {
	name := strings.TrimSpace(info.ComputerName)
	platform := strings.TrimSpace(info.PlatformFamily)
	switch {
	case name != "" && platform != "":
		return name + " (" + platform + ")"
	case name != "":
		return name
	case platform != "":
		return platform
	default:
		return ""
	}
}

func sessionCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	return truncateMiddleCell(cwd, sessionCWDColumnWidth)
}

func sessionAge(startedAt int, now time.Time) string {
	if startedAt <= 0 || now.IsZero() {
		return "-"
	}
	elapsed := now.Sub(time.Unix(int64(startedAt), 0))
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
	}
}

func renderSessionTable(w io.Writer, rows []sessionRow) {
	border := sessionTableBorder()
	_, _ = io.WriteString(w, border)
	renderSessionTableLine(w, func(col tableColumn) string { return col.title })
	_, _ = io.WriteString(w, border)
	for _, row := range rows {
		renderSessionTableLine(w, func(col tableColumn) string { return col.value(row) })
	}
	_, _ = io.WriteString(w, border)
}

func renderSessionTableLine(w io.Writer, value func(tableColumn) string) {
	_, _ = io.WriteString(w, "|")
	for _, col := range sessionTableColumns {
		_, _ = fmt.Fprintf(w, " %-*s |", col.width, truncateCell(value(col), col.width))
	}
	_, _ = io.WriteString(w, "\n")
}

func sessionTableBorder() string {
	var b strings.Builder
	b.WriteString("+")
	for _, col := range sessionTableColumns {
		b.WriteString(strings.Repeat("-", col.width+2))
		b.WriteString("+")
	}
	b.WriteString("\n")
	return b.String()
}

func truncateCell(value string, width int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
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
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
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
