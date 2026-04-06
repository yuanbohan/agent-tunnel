package protocol

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRegisterFrameRoundTrip(t *testing.T) {
	started := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	frame := RegisterFrame(SessionInfo{
		SessionID:      "sess-123",
		Launcher:       "codex",
		Label:          "api-fix",
		CWD:            "/tmp/project",
		CommandPreview: "codex --profile prod",
		StartedAt:      started,
	})

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded AgentFrame
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "register" {
		t.Fatalf("Type = %q, want register", decoded.Type)
	}
	if decoded.Session == nil {
		t.Fatal("Session = nil, want payload")
	}
	if decoded.Session.SessionID != "sess-123" {
		t.Fatalf("SessionID = %q, want sess-123", decoded.Session.SessionID)
	}
	if decoded.Session.StartedAt != started {
		t.Fatalf("StartedAt = %v, want %v", decoded.Session.StartedAt, started)
	}
}

func TestSessionSummaryJSONUsesStableFieldNames(t *testing.T) {
	info := SessionInfo{
		SessionID:      "sess-1",
		Launcher:       "gemini",
		Label:          "docs",
		CWD:            "/Users/test/project",
		CommandPreview: "gemini",
	}

	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	got := string(raw)
	for _, want := range []string{
		"session_id",
		"launcher",
		"label",
		"cwd",
		"command_preview",
		"latest_seq",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("json = %s, want field %q", got, want)
		}
	}
}

func TestSessionSummaryOmittedUnsetOptionalFields(t *testing.T) {
	info := SessionInfo{
		SessionID:      "sess-2",
		Launcher:       "claude",
		CWD:            "/Users/test/project",
		CommandPreview: "claude",
	}

	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	if strings.Contains(string(raw), "last_active_at") {
		t.Fatalf("json = %s, did not expect last_active_at", raw)
	}
}

func TestClientUpdateMessageOutputRoundTrip(t *testing.T) {
	ts := time.Date(2026, 4, 6, 2, 10, 2, 0, time.UTC)
	frame := EncodeClientOutput("sess-1", 42, []byte("hello"), 132, 43, ts)

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded ClientUpdateMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.SessionID != "sess-1" || decoded.Type != "output" || decoded.Seq != 42 {
		t.Fatalf("decoded = %#v, want sess-1 output seq 42", decoded)
	}
	if decoded.TS == nil || !decoded.TS.Equal(ts) {
		t.Fatalf("ts = %v, want %v", decoded.TS, ts)
	}
	data, err := base64.StdEncoding.DecodeString(decoded.Data)
	if err != nil {
		t.Fatalf("DecodeString returned error: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q, want hello", string(data))
	}
}

func TestClientInputMessageLegacyRawRoundTrip(t *testing.T) {
	frame := EncodeClientInput("sess-3", []byte("ls\n"))

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded ClientInputMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.SessionID != "sess-3" || decoded.Type != "input" {
		t.Fatalf("decoded = %#v, want sess-3 input", decoded)
	}
	data, err := base64.StdEncoding.DecodeString(decoded.Data)
	if err != nil {
		t.Fatalf("DecodeString returned error: %v", err)
	}
	if string(data) != "ls\n" {
		t.Fatalf("data = %q, want ls\\n", string(data))
	}
}

func TestClientInputMessageTextRoundTrip(t *testing.T) {
	frame := EncodeClientInputText("sess-4", "hello")

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded ClientInputMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.SessionID != "sess-4" || decoded.Type != "input_text" || decoded.Text != "hello" {
		t.Fatalf("decoded = %#v, want sess-4 input_text hello", decoded)
	}
}

func TestClientInputMessageKeyRoundTrip(t *testing.T) {
	frame := EncodeClientInputKey("sess-5", "TAB", false, false, true)

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded ClientInputMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.SessionID != "sess-5" || decoded.Type != "input_key" || decoded.Key != "TAB" || !decoded.Shift {
		t.Fatalf("decoded = %#v, want sess-5 input_key TAB shift=true", decoded)
	}
}

func TestAgentMessageInputTextRoundTrip(t *testing.T) {
	frame := EncodeInputText("hello")

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "input_text" || decoded.Text != "hello" {
		t.Fatalf("decoded = %#v, want input_text hello", decoded)
	}
}

func TestAgentMessageInputKeyRoundTrip(t *testing.T) {
	frame := EncodeInputKey("C", true, false, false)

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "input_key" || decoded.Key != "C" || !decoded.Ctrl {
		t.Fatalf("decoded = %#v, want input_key ctrl-C", decoded)
	}
}
