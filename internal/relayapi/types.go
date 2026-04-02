package relayapi

import "time"

type SessionInfo struct {
	SessionID      string    `json:"session_id"`
	Launcher       string    `json:"launcher"`
	Label          string    `json:"label,omitempty"`
	CWD            string    `json:"cwd"`
	CommandPreview string    `json:"command_preview"`
	StartedAt      time.Time `json:"started_at"`
	LastPreview    string    `json:"last_preview,omitempty"`
	LastActiveAt   time.Time `json:"last_active_at,omitempty"`
}

type AgentFrame struct {
	Type    string       `json:"type"`
	Session *SessionInfo `json:"session,omitempty"`
	Data    string       `json:"data,omitempty"`
	Cols    int          `json:"cols,omitempty"`
	Rows    int          `json:"rows,omitempty"`
}

func RegisterFrame(info SessionInfo) AgentFrame {
	return AgentFrame{
		Type:    "register",
		Session: &info,
	}
}
