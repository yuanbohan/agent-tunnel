package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/tunnel/daemon"
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

type sessionListJSONRow struct {
	Scope          string `json:"scope"`
	Source         string `json:"source"`
	SessionID      string `json:"session_id"`
	DeviceID       string `json:"device_id,omitempty"`
	Label          string `json:"label,omitempty"`
	Launcher       string `json:"launcher,omitempty"`
	Command        string `json:"command,omitempty"`
	Machine        string `json:"machine,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	StartedAt      int    `json:"started_at,omitempty"`
	PlatformFamily string `json:"platform_family,omitempty"`
	PlatformID     string `json:"platform_id,omitempty"`
	ComputerName   string `json:"computer_name,omitempty"`
}

const sessionCWDColumnWidth = 32

var sessionTableColumns = []tableColumn{
	{title: "Scope", width: 7},
	{title: "Source", width: 7},
	{title: "Session", width: 19},
	{title: "Label", width: 16},
	{title: "Command", width: 24},
	{title: "Machine", width: 22},
	{title: "CWD", width: sessionCWDColumnWidth, truncateMiddle: true},
	{title: "Age", width: 8},
}

func runSessionList(ctx context.Context, args sessionCommandArgs, stdout, stderr io.Writer) error {
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	sessions, err := daemon.ListSessions(ctx, paths)
	if err != nil {
		return err
	}
	if args.JSON {
		jsonRows := make([]sessionListJSONRow, 0, len(sessions))
		for _, snapshot := range sessions {
			jsonRows = append(jsonRows, buildSessionJSONRow(sessionInfoFromBrokerSnapshot(snapshot)))
		}
		return writeIndentedJSON(stdout, jsonRows)
	}
	rows := make([]sessionRow, 0, len(sessions))
	now := time.Now()
	for _, snapshot := range sessions {
		rows = append(rows, buildSessionRow(sessionInfoFromBrokerSnapshot(snapshot), now))
	}
	if len(rows) == 0 {
		_, _ = io.WriteString(stdout, "No local live sessions.\n")
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
	paths, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	if err := daemon.StopSession(ctx, paths, sessionID); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "stopped session %s\n", sessionID)
	return nil
}

func sessionInfoFromBrokerSnapshot(snapshot daemon.BrokerSessionSnapshot) protocol.SessionInfo {
	return protocol.SessionInfo{
		SessionID:      strings.TrimSpace(snapshot.SessionID),
		DeviceID:       strings.TrimSpace(snapshot.DeviceID),
		Launcher:       strings.TrimSpace(snapshot.Launcher),
		Label:          strings.TrimSpace(snapshot.Label),
		CWD:            strings.TrimSpace(snapshot.CWD),
		CommandPreview: strings.TrimSpace(snapshot.CommandPreview),
		GitBranch:      strings.TrimSpace(snapshot.GitBranch),
		StartedAt:      snapshot.StartedAt,
		PlatformFamily: strings.TrimSpace(snapshot.PlatformFamily),
		PlatformID:     strings.TrimSpace(snapshot.PlatformID),
		ComputerName:   strings.TrimSpace(snapshot.ComputerName),
		LaunchSource:   strings.TrimSpace(snapshot.LaunchSource),
	}
}

func buildSessionRow(info protocol.SessionInfo, now time.Time) sessionRow {
	scope := sessionScope()
	machine := sessionMachine(info)
	if scope == "local" {
		machine = "This machine"
	}
	return sessionRow{
		scope:   scope,
		source:  sessionLaunchSource(info.LaunchSource),
		id:      emptyValue(info.SessionID),
		label:   emptyValue(info.Label),
		command: emptyValue(sessionCommand(info)),
		machine: emptyValue(machine),
		cwd:     emptyValue(sessionCWD(info.CWD)),
		age:     sessionAge(info.StartedAt, now),
	}
}

func buildSessionJSONRow(info protocol.SessionInfo) sessionListJSONRow {
	return sessionListJSONRow{
		Scope:          sessionScope(),
		Source:         sessionLaunchSource(info.LaunchSource),
		SessionID:      strings.TrimSpace(info.SessionID),
		DeviceID:       strings.TrimSpace(info.DeviceID),
		Label:          strings.TrimSpace(info.Label),
		Launcher:       strings.TrimSpace(info.Launcher),
		Command:        strings.TrimSpace(sessionCommand(info)),
		Machine:        strings.TrimSpace(sessionMachine(info)),
		CWD:            strings.TrimSpace(info.CWD),
		StartedAt:      info.StartedAt,
		PlatformFamily: strings.TrimSpace(info.PlatformFamily),
		PlatformID:     strings.TrimSpace(info.PlatformID),
		ComputerName:   strings.TrimSpace(info.ComputerName),
	}
}

func sessionScope() string {
	return "local"
}

func sessionLaunchSource(launchSource string) string {
	switch strings.TrimSpace(launchSource) {
	case protocol.SessionLaunchSourceLocal:
		return protocol.SessionLaunchSourceLocal
	case protocol.SessionLaunchSourceMobile:
		return protocol.SessionLaunchSourceMobile
	default:
		return "unknown"
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
	tableRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		tableRows = append(tableRows, []string{
			row.scope,
			row.source,
			row.id,
			row.label,
			row.command,
			row.machine,
			row.cwd,
			row.age,
		})
	}
	renderTable(w, sessionTableColumns, tableRows)
}
