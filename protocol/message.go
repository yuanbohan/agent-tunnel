package protocol

import (
	"encoding/base64"
	"time"
)

// Message is the JSON frame exchanged over WebSocket.
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
	LastPreview    string     `json:"last_preview,omitempty"`
	LastActiveAt   *time.Time `json:"last_active_at,omitempty"`
	LatestSeq      uint64     `json:"latest_seq"`
	LastReadSeq    uint64     `json:"last_read_seq"`
	UnreadCount    uint64     `json:"unread_count"`
	PreviewSeq     uint64     `json:"preview_seq"`
	PreviewB64     string     `json:"preview_b64"`
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
