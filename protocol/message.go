package protocol

import (
	"encoding/base64"
	"time"
)

// Message is the agent-side JSON frame exchanged over session-scoped WebSockets.
// Type is one of "input", "input_text", "input_submit", "input_key", or "output".
type Message struct {
	Type  string `json:"type"`
	Seq   uint64 `json:"seq,omitempty"`
	Data  string `json:"data,omitempty"` // base64-encoded bytes
	Text  string `json:"text,omitempty"`
	Key   string `json:"key,omitempty"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
	Ctrl  bool   `json:"ctrl,omitempty"`
	Alt   bool   `json:"alt,omitempty"`
	Shift bool   `json:"shift,omitempty"`
}

// EncodeOutput wraps raw PTY bytes into an output Message.
func EncodeOutput(b []byte) Message {
	return EncodeOutputWithSeq(0, b)
}

// EncodeOutputWithSeq wraps raw PTY bytes and a sequence into an output Message.
func EncodeOutputWithSeq(seq uint64, b []byte) Message {
	return EncodeOutputWithSeqAndSize(seq, b, 0, 0)
}

// EncodeOutputWithSeqAndSize wraps raw PTY bytes, sequence, and terminal size
// into an output Message.
func EncodeOutputWithSeqAndSize(seq uint64, b []byte, cols, rows int) Message {
	return Message{
		Type: "output",
		Seq:  seq,
		Data: base64.StdEncoding.EncodeToString(b),
		Cols: cols,
		Rows: rows,
	}
}

func EncodeInput(b []byte) Message {
	return Message{
		Type: "input",
		Data: base64.StdEncoding.EncodeToString(b),
	}
}

func EncodeInputText(text string) Message {
	return Message{
		Type: "input_text",
		Text: text,
	}
}

func EncodeInputSubmit(text string) Message {
	return Message{
		Type: "input_submit",
		Text: text,
	}
}

func EncodeInputKey(key string, ctrl, alt, shift bool) Message {
	return Message{
		Type:  "input_key",
		Key:   key,
		Ctrl:  ctrl,
		Alt:   alt,
		Shift: shift,
	}
}

// DecodeData decodes the base64 Data field of an input or output Message.
func DecodeData(m Message) ([]byte, error) {
	return base64.StdEncoding.DecodeString(m.Data)
}

// SessionInfo describes a live agent session registered with the relay.
type SessionInfo struct {
	SessionID      string     `json:"session_id"`
	Launcher       string     `json:"launcher"`
	Label          string     `json:"label,omitempty"`
	CWD            string     `json:"cwd"`
	CommandPreview string     `json:"command_preview"`
	StartedAt      time.Time  `json:"started_at"`
	LastActiveAt   *time.Time `json:"last_active_at,omitempty"`
	LatestSeq      uint64     `json:"latest_seq"`
}

// AgentFrame is the JSON envelope sent over the agent WebSocket to the relay.
type AgentFrame struct {
	Type    string       `json:"type"`
	Session *SessionInfo `json:"session,omitempty"`
	Data    string       `json:"data,omitempty"`
	Cols    int          `json:"cols,omitempty"`
	Rows    int          `json:"rows,omitempty"`
}

// RegisterFrame builds an AgentFrame of type "register".
func RegisterFrame(info SessionInfo) AgentFrame {
	return AgentFrame{
		Type:    "register",
		Session: &info,
	}
}

// ClientInputMessage is the client-to-relay envelope used by /api/updates/ws.
type ClientInputMessage struct {
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	Data      string `json:"data,omitempty"` // legacy raw bytes during migration
	Text      string `json:"text,omitempty"`
	Key       string `json:"key,omitempty"`
	Ctrl      bool   `json:"ctrl,omitempty"`
	Alt       bool   `json:"alt,omitempty"`
	Shift     bool   `json:"shift,omitempty"`
}

func EncodeClientInput(sessionID string, b []byte) ClientInputMessage {
	return ClientInputMessage{
		SessionID: sessionID,
		Type:      "input",
		Data:      base64.StdEncoding.EncodeToString(b),
	}
}

func EncodeClientInputText(sessionID, text string) ClientInputMessage {
	return ClientInputMessage{
		SessionID: sessionID,
		Type:      "input_text",
		Text:      text,
	}
}

func EncodeClientInputSubmit(sessionID, text string) ClientInputMessage {
	return ClientInputMessage{
		SessionID: sessionID,
		Type:      "input_submit",
		Text:      text,
	}
}

func EncodeClientInputKey(sessionID, key string, ctrl, alt, shift bool) ClientInputMessage {
	return ClientInputMessage{
		SessionID: sessionID,
		Type:      "input_key",
		Key:       key,
		Ctrl:      ctrl,
		Alt:       alt,
		Shift:     shift,
	}
}

func (m ClientInputMessage) AgentMessage() Message {
	switch m.Type {
	case "input_text":
		return EncodeInputText(m.Text)
	case "input_submit":
		return EncodeInputSubmit(m.Text)
	case "input_key":
		return EncodeInputKey(m.Key, m.Ctrl, m.Alt, m.Shift)
	default:
		return EncodeInputFromBase64(m.Data)
	}
}

func EncodeInputFromBase64(data string) Message {
	return Message{
		Type: "input",
		Data: data,
	}
}

// ClientUpdateMessage is the client-facing multiplexed envelope used by the
// global relay stream. Unlike Message, it always carries session identity so a
// single foreground connection can route live output for many sessions.
type ClientUpdateMessage struct {
	SessionID string     `json:"session_id"`
	Type      string     `json:"type"`
	Seq       uint64     `json:"seq,omitempty"`
	Data      string     `json:"data,omitempty"`
	Cols      int        `json:"cols,omitempty"`
	Rows      int        `json:"rows,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	TS        *time.Time `json:"ts,omitempty"`
}

func EncodeClientOutput(sessionID string, seq uint64, b []byte, cols, rows int, ts time.Time) ClientUpdateMessage {
	tsCopy := ts
	return ClientUpdateMessage{
		SessionID: sessionID,
		Type:      "output",
		Seq:       seq,
		Data:      base64.StdEncoding.EncodeToString(b),
		Cols:      cols,
		Rows:      rows,
		TS:        &tsCopy,
	}
}

func EncodeClientSessionRemoved(sessionID, reason string) ClientUpdateMessage {
	return ClientUpdateMessage{
		SessionID: sessionID,
		Type:      "session_removed",
		Reason:    reason,
	}
}
