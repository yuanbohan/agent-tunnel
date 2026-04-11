package logx

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestInfoWritesJSONEvent(t *testing.T) {
	var buf bytes.Buffer
	restore := UseWriterForTest(&buf)
	defer restore()

	Info("agent_registered",
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
	if _, ok := got["msg"]; ok {
		t.Fatalf("msg present = %v, want absent", got["msg"])
	}
	if got["session_id"] != "sess-1" {
		t.Fatalf("session_id = %v, want sess-1", got["session_id"])
	}
	if got["launcher"] != "codex" {
		t.Fatalf("launcher = %v, want codex", got["launcher"])
	}
}

func TestInfoIgnoresCallerEventField(t *testing.T) {
	var buf bytes.Buffer
	restore := UseWriterForTest(&buf)
	defer restore()

	Info("agent_registered",
		String("event", "caller_override"),
		String("session_id", "sess-1"),
	)

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal log line: %v\noutput: %q", err, buf.String())
	}

	if got["event"] != "agent_registered" {
		t.Fatalf("event = %v, want agent_registered", got["event"])
	}
	if got["session_id"] != "sess-1" {
		t.Fatalf("session_id = %v, want sess-1", got["session_id"])
	}
	if got["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", got["level"])
	}
}

func TestJSONUsesTsKey(t *testing.T) {
	var buf bytes.Buffer
	restore := UseWriterForTest(&buf)
	defer restore()

	Warn("agent_timeout", String("session_id", "sess-1"))

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal log line: %v\noutput: %q", err, buf.String())
	}

	if _, ok := got["time"]; ok {
		t.Fatalf("time present = %v, want absent", got["time"])
	}
	if _, ok := got["ts"]; !ok {
		t.Fatalf("ts present = %v, want present", got["ts"])
	}
}

func TestWarnOmitsFieldsNotPassed(t *testing.T) {
	var buf bytes.Buffer
	restore := UseWriterForTest(&buf)
	defer restore()

	Warn("agent_timeout", String("launcher", "codex"))

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
	if got["launcher"] != "codex" {
		t.Fatalf("launcher = %v, want codex", got["launcher"])
	}
}
