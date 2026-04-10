package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegisterFrameRoundTrip(t *testing.T) {
	started := 1775131200
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

func TestActivityFrameRoundTrip(t *testing.T) {
	ts := 1775736000
	frame := ActivityFrame(ts)

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded AgentFrame
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "activity" {
		t.Fatalf("Type = %q, want activity", decoded.Type)
	}
	if decoded.LastActiveAt == nil || *decoded.LastActiveAt != ts {
		t.Fatalf("LastActiveAt = %v, want %v", decoded.LastActiveAt, ts)
	}
}

func TestAttachOpenFrameRoundTrip(t *testing.T) {
	frame := AttachOpenFrame("4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1")

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded AgentFrame
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "attach_open" || decoded.ClientID != "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1" {
		t.Fatalf("decoded = %#v, want attach_open with client id", decoded)
	}
}

func TestAttachedMessageRoundTrip(t *testing.T) {
	frame := AttachedMessage("sess-1", 132, 43)

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded AttachControlMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "attached" || decoded.SessionID != "sess-1" || decoded.Cols != 132 || decoded.Rows != 43 {
		t.Fatalf("decoded = %#v, want attached sess-1 132x43", decoded)
	}
}

func TestSessionSummaryJSONUsesStableFieldNames(t *testing.T) {
	info := SessionInfo{
		SessionID:      "sess-1",
		Launcher:       "gemini",
		Label:          "docs",
		CWD:            "/Users/test/project",
		CommandPreview: "gemini",
		State:          SessionStateConnected,
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
		"state",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("json = %s, want field %q", got, want)
		}
	}
	if strings.Contains(got, "latest_seq") {
		t.Fatalf("json = %s, did not expect latest_seq", got)
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
	if strings.Contains(string(raw), "state") {
		t.Fatalf("json = %s, did not expect state", raw)
	}
}

func TestClientInputMessageTextRoundTrip(t *testing.T) {
	frame := EncodeClientInputText("hello", false)

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	if strings.Contains(string(raw), `"session_id":`) {
		t.Fatalf("json = %s, did not expect session_id field", raw)
	}
	if strings.Contains(string(raw), `"submit":`) {
		t.Fatalf("json = %s, did not expect submit field when false", raw)
	}

	var decoded ClientInputMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "input_text" || decoded.Text != "hello" || decoded.Submit {
		t.Fatalf("decoded = %#v, want input_text hello submit=false", decoded)
	}
}

func TestClientInputMessageSubmitRoundTrip(t *testing.T) {
	frame := EncodeClientInputText("hello\nworld", true)

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded ClientInputMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "input_text" || decoded.Text != "hello\nworld" || !decoded.Submit {
		t.Fatalf("decoded = %#v, want input_text submit hello\\nworld", decoded)
	}
}

func TestClientInputMessageTextDefaultsSubmitFalseWhenOmitted(t *testing.T) {
	raw := []byte(`{"type":"input_text","text":"hello"}`)

	var decoded ClientInputMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Submit {
		t.Fatalf("Submit = true, want false for omitted field")
	}

	got := decoded.AgentFrame("client-1")
	if got.Type != "input_text" || got.ClientID != "client-1" || got.Text != "hello" || got.Submit {
		t.Fatalf("AgentFrame() = %#v, want input_text client-1 hello submit=false", got)
	}
}

func TestClientInputMessageKeyRoundTrip(t *testing.T) {
	frame := EncodeClientInputKey("TAB")

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded ClientInputMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "input_key" || decoded.Key != "TAB" {
		t.Fatalf("decoded = %#v, want input_key TAB", decoded)
	}
}

func TestClientInputMessageAgentFrameIncludesClientID(t *testing.T) {
	msg := EncodeClientInputText("hello", true)

	got := msg.AgentFrame("client-1")
	if got.Type != "input_text" || got.ClientID != "client-1" || got.Text != "hello" || !got.Submit {
		t.Fatalf("AgentFrame() = %#v, want input_text client-1 hello submit=true", got)
	}
}

func TestForwardInputTextFrameRoundTrip(t *testing.T) {
	frame := ForwardInputTextFrame("client-1", "hello", true)

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded AgentFrame
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "input_text" || decoded.ClientID != "client-1" || decoded.Text != "hello" || !decoded.Submit {
		t.Fatalf("decoded = %#v, want input_text client-1 hello submit=true", decoded)
	}
}

func TestForwardInputKeyFrameRoundTrip(t *testing.T) {
	frame := ForwardInputKeyFrame("client-1", "TAB")

	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded AgentFrame
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	if decoded.Type != "input_key" || decoded.ClientID != "client-1" || decoded.Key != "TAB" {
		t.Fatalf("decoded = %#v, want input_key client-1 TAB", decoded)
	}
}

func TestClientInputMessageAgentFrameSubmit(t *testing.T) {
	msg := EncodeClientInputText("", true)

	got := msg.AgentFrame("client-1")
	if got.Type != "input_text" || got.ClientID != "client-1" || got.Text != "" || !got.Submit {
		t.Fatalf("AgentFrame() = %#v, want input_text client-1 empty submit=true", got)
	}
}
