package protocol

import "time"

const (
	SessionLaunchSourceLocal  = "local"
	SessionLaunchSourceMobile = "mobile"

	MaxSubmitAnchors = 256
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
	ClientID      string         `json:"client_id,omitempty"`
	Reason        string         `json:"reason,omitempty"`
	Text          string         `json:"text,omitempty"`
	Submit        bool           `json:"submit,omitempty"`
	Key           string         `json:"key,omitempty"`
	Cols          int            `json:"cols,omitempty"`
	Rows          int            `json:"rows,omitempty"`
	SubmitAnchors []SubmitAnchor `json:"submit_anchors,omitempty"`
	SubmitAnchor  *SubmitAnchor  `json:"submit_anchor,omitempty"`
}

// SubmitAnchor describes a content-free submit navigation hint. Snapshot
// anchors use a line relative to the buffer restored by the accompanying
// snapshot; live anchors use a line relative to the attached terminal buffer
// when the event is received.
type SubmitAnchor struct {
	ID          string `json:"id"`
	Line        int    `json:"line"`
	SubmittedAt int    `json:"submitted_at"`
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

func ResizeFrame(cols, rows int) AgentFrame {
	return AgentFrame{
		Type: "resize",
		Cols: cols,
		Rows: rows,
	}
}

func AttachOpenFrame(clientID string) AgentFrame {
	return AgentFrame{
		Type:     "attach_open",
		ClientID: clientID,
	}
}

func AttachReadyFrame(clientID string, cols, rows int) AgentFrame {
	return AgentFrame{
		Type:     "attach_ready",
		ClientID: clientID,
		Cols:     cols,
		Rows:     rows,
	}
}

func SnapshotDoneFrame(clientID string, anchors ...SubmitAnchor) AgentFrame {
	return AgentFrame{
		Type:          "snapshot_done",
		ClientID:      clientID,
		SubmitAnchors: cloneSubmitAnchors(anchors),
	}
}

func SubmitAnchorFrame(clientID string, anchor SubmitAnchor) AgentFrame {
	frame := AgentFrame{
		Type:     "submit_anchor",
		ClientID: clientID,
	}
	if sanitized, ok := sanitizeSubmitAnchor(anchor); ok {
		frame.SubmitAnchor = &sanitized
	}
	return frame
}

func AttachCloseFrame(clientID, reason string) AgentFrame {
	return AgentFrame{
		Type:     "attach_close",
		ClientID: clientID,
		Reason:   reason,
	}
}

func StopSessionFrame() AgentFrame {
	return AgentFrame{
		Type: "stop_session",
	}
}

func ForwardInputTextFrame(clientID, text string, submit bool) AgentFrame {
	return AgentFrame{
		Type:     "input_text",
		ClientID: clientID,
		Text:     text,
		Submit:   submit,
	}
}

func ForwardInputKeyFrame(clientID, key string) AgentFrame {
	return AgentFrame{
		Type:     "input_key",
		ClientID: clientID,
		Key:      key,
	}
}

type AttachControlMessage struct {
	Type          string         `json:"type"`
	SessionID     string         `json:"session_id,omitempty"`
	Cols          int            `json:"cols,omitempty"`
	Rows          int            `json:"rows,omitempty"`
	Reason        string         `json:"reason,omitempty"`
	SubmitAnchors []SubmitAnchor `json:"submit_anchors,omitempty"`
	SubmitAnchor  *SubmitAnchor  `json:"submit_anchor,omitempty"`
}

func AttachedMessage(sessionID string, cols, rows int) AttachControlMessage {
	return AttachControlMessage{
		Type:      "attached",
		SessionID: sessionID,
		Cols:      cols,
		Rows:      rows,
	}
}

func SnapshotDoneMessage(anchors ...SubmitAnchor) AttachControlMessage {
	return AttachControlMessage{
		Type:          "snapshot_done",
		SubmitAnchors: cloneSubmitAnchors(anchors),
	}
}

func SubmitAnchorMessage(anchor SubmitAnchor) AttachControlMessage {
	msg := AttachControlMessage{
		Type: "submit_anchor",
	}
	if sanitized, ok := sanitizeSubmitAnchor(anchor); ok {
		msg.SubmitAnchor = &sanitized
	}
	return msg
}

func ResizeMessage(cols, rows int) AttachControlMessage {
	return AttachControlMessage{
		Type: "resize",
		Cols: cols,
		Rows: rows,
	}
}

func ClosingMessage(reason string) AttachControlMessage {
	return AttachControlMessage{
		Type:   "closing",
		Reason: reason,
	}
}

func cloneSubmitAnchors(anchors []SubmitAnchor) []SubmitAnchor {
	if len(anchors) == 0 {
		return nil
	}
	out := make([]SubmitAnchor, 0, len(anchors))
	for _, anchor := range anchors {
		if len(out) >= MaxSubmitAnchors {
			break
		}
		sanitized, ok := sanitizeSubmitAnchor(anchor)
		if !ok {
			continue
		}
		out = append(out, sanitized)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sanitizeSubmitAnchor(anchor SubmitAnchor) (SubmitAnchor, bool) {
	if anchor.ID == "" || anchor.Line < 0 || anchor.SubmittedAt < 0 {
		return SubmitAnchor{}, false
	}
	return anchor, true
}

// ClientInputMessage is the client-to-relay envelope used by the
// session-scoped attach WebSocket.
type ClientInputMessage struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Submit bool   `json:"submit,omitempty"`
	Key    string `json:"key,omitempty"`
}

func EncodeClientInputText(text string, submit bool) ClientInputMessage {
	return ClientInputMessage{
		Type:   "input_text",
		Text:   text,
		Submit: submit,
	}
}

func EncodeClientInputKey(key string) ClientInputMessage {
	return ClientInputMessage{
		Type: "input_key",
		Key:  key,
	}
}

func (m ClientInputMessage) AgentFrame(clientID string) AgentFrame {
	switch m.Type {
	case "input_text":
		return ForwardInputTextFrame(clientID, m.Text, m.Submit)
	case "input_key":
		return ForwardInputKeyFrame(clientID, m.Key)
	default:
		return AgentFrame{Type: m.Type, ClientID: clientID}
	}
}
