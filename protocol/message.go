package protocol

import (
	"encoding/base64"
	"time"
)

// Message is the agent-side JSON frame exchanged over session-scoped WebSockets.
// Type is one of "input", "output", or "resize".
type Message struct {
	Type string `json:"type"`
	Seq  uint64 `json:"seq,omitempty"`
	Data string `json:"data,omitempty"` // base64-encoded bytes
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
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
	LastReadSeq    uint64     `json:"last_read_seq"`
	UnreadCount    uint64     `json:"unread_count"`
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

// ClientUpdateMessage is the client-facing multiplexed envelope used by the
// global relay stream. Unlike Message, it always carries session identity so a
// single foreground connection can route both live output and client input for
// many sessions.
type ClientUpdateMessage struct {
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	Seq       uint64 `json:"seq,omitempty"`
	Data      string `json:"data,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func EncodeClientOutput(sessionID string, seq uint64, b []byte, cols, rows int) ClientUpdateMessage {
	return ClientUpdateMessage{
		SessionID: sessionID,
		Type:      "output",
		Seq:       seq,
		Data:      base64.StdEncoding.EncodeToString(b),
		Cols:      cols,
		Rows:      rows,
	}
}

func EncodeClientSessionRemoved(sessionID, reason string) ClientUpdateMessage {
	return ClientUpdateMessage{
		SessionID: sessionID,
		Type:      "session_removed",
		Reason:    reason,
	}
}
