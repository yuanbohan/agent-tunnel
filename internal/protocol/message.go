package protocol

import "time"

const (
	SessionLaunchSourceLocal  = "local"
	SessionLaunchSourceMobile = "mobile"
)

func UnixTimestamp(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	return int(t.Unix())
}

// SessionInfo describes a live agent session registered with the relay.
type SessionInfo struct {
	SessionID      string `json:"session_id"`
	DeviceID       string `json:"device_id"`
	Launcher       string `json:"launcher"`
	Label          string `json:"label,omitempty"`
	CWD            string `json:"cwd"`
	CommandPreview string `json:"command_preview"`
	GitBranch      string `json:"git_branch"`
	StartedAt      int    `json:"started_at"`
	PlatformFamily string `json:"platform_family"`
	PlatformID     string `json:"platform_id"`
	ComputerName   string `json:"computer_name"`
	LaunchSource   string `json:"launch_source,omitempty"`
}

// LaunchContext carries relay launch correlation for sessions created through
// a remote device launch request. The relay validates it before exposing
// SessionInfo.LaunchSource to clients.
type LaunchContext struct {
	Source    string `json:"source,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// AgentFrame is the JSON envelope sent over the agent WebSocket to the relay.
type AgentFrame struct {
	Type          string         `json:"type"`
	Session       *SessionInfo   `json:"session,omitempty"`
	LaunchContext *LaunchContext `json:"launch_context,omitempty"`
}

// RegisterFrame builds an AgentFrame of type "register".
func RegisterFrame(info SessionInfo) AgentFrame {
	return RegisterFrameWithLaunchContext(info, LaunchContext{})
}

func RegisterFrameWithLaunchContext(info SessionInfo, launchContext LaunchContext) AgentFrame {
	frame := AgentFrame{
		Type:    "register",
		Session: &info,
	}
	if launchContext.Source != "" || launchContext.RequestID != "" {
		frame.LaunchContext = &launchContext
	}
	return frame
}

func LaunchReadyFrame(launchContext LaunchContext) AgentFrame {
	frame := AgentFrame{Type: "launch_ready"}
	if launchContext.Source != "" || launchContext.RequestID != "" {
		frame.LaunchContext = &launchContext
	}
	return frame
}
