package relay

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestLoggerInfoWritesJSONEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf)

	logger.Info("agent registered",
		String("event", "agent_registered"),
		String("session_id", "sess-1"),
		String("launcher", "codex"),
	)

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal log line: %v\noutput: %q", err, buf.String())
	}

	if got["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", got["level"])
	}
	if got["event"] != "agent_registered" {
		t.Fatalf("event = %v, want agent_registered", got["event"])
	}
	if got["session_id"] != "sess-1" {
		t.Fatalf("session_id = %v, want sess-1", got["session_id"])
	}
	if got["launcher"] != "codex" {
		t.Fatalf("launcher = %v, want codex", got["launcher"])
	}
}

func TestLoggerWarnOmitsFieldsNotPassed(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf)

	logger.Warn("",
		String("event", "agent_timeout"),
	)

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal log line: %v\noutput: %q", err, buf.String())
	}

	if got["level"] != "WARN" {
		t.Fatalf("level = %v, want WARN", got["level"])
	}
	if got["event"] != "agent_timeout" {
		t.Fatalf("event = %v, want agent_timeout", got["event"])
	}
	if _, ok := got["session_id"]; ok {
		t.Fatalf("session_id present = %v, want absent", got["session_id"])
	}
	if _, ok := got["msg"]; ok {
		t.Fatalf("msg present = %v, want absent", got["msg"])
	}
}
