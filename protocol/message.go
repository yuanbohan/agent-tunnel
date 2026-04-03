package protocol

import "encoding/base64"

// Message is the JSON frame exchanged over WebSocket.
// Type is one of "input", "output", or "resize".
type Message struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"` // base64-encoded bytes
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// EncodeOutput wraps raw PTY bytes into an output Message.
func EncodeOutput(b []byte) Message {
	return Message{
		Type: "output",
		Data: base64.StdEncoding.EncodeToString(b),
	}
}

// DecodeData decodes the base64 Data field of an input or output Message.
func DecodeData(m Message) ([]byte, error) {
	return base64.StdEncoding.DecodeString(m.Data)
}
