package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegisterFrameRoundTrip(t *testing.T) {
	started := 1775131200
	frame := RegisterFrameWithLaunchContext(SessionInfo{
		SessionID:      "sess-123",
		DeviceID:       "dev-123",
		Launcher:       "codex",
		Label:          "api-fix",
		CWD:            "/tmp/project",
		CommandPreview: "codex --profile prod",
		GitBranch:      "main",
		StartedAt:      started,
		PlatformFamily: "linux",
		PlatformID:     "ubuntu",
		ComputerName:   "Office Linux",
		LaunchSource:   "mobile",
	}, LaunchContext{Source: SessionLaunchSourceMobile, RequestID: "req-123"})

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
	if decoded.Session.DeviceID != "dev-123" {
		t.Fatalf("DeviceID = %q, want dev-123", decoded.Session.DeviceID)
	}
	if decoded.Session.StartedAt != started {
		t.Fatalf("StartedAt = %v, want %v", decoded.Session.StartedAt, started)
	}
	if decoded.Session.GitBranch != "main" {
		t.Fatalf("GitBranch = %q, want main", decoded.Session.GitBranch)
	}
	if decoded.Session.PlatformFamily != "linux" {
		t.Fatalf("PlatformFamily = %q, want linux", decoded.Session.PlatformFamily)
	}
	if decoded.Session.PlatformID != "ubuntu" {
		t.Fatalf("PlatformID = %q, want ubuntu", decoded.Session.PlatformID)
	}
	if decoded.Session.ComputerName != "Office Linux" {
		t.Fatalf("ComputerName = %q, want Office Linux", decoded.Session.ComputerName)
	}
	if decoded.Session.LaunchSource != "mobile" {
		t.Fatalf("LaunchSource = %q, want mobile", decoded.Session.LaunchSource)
	}
	if decoded.LaunchContext == nil {
		t.Fatal("LaunchContext = nil, want context")
	}
	if decoded.LaunchContext.Source != SessionLaunchSourceMobile {
		t.Fatalf("LaunchContext.Source = %q, want mobile", decoded.LaunchContext.Source)
	}
	if decoded.LaunchContext.RequestID != "req-123" {
		t.Fatalf("LaunchContext.RequestID = %q, want req-123", decoded.LaunchContext.RequestID)
	}
}

func TestStopSessionFrame(t *testing.T) {
	frame := StopSessionFrame()
	if frame.Type != "stop_session" {
		t.Fatalf("Type = %q, want stop_session", frame.Type)
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
		DeviceID:       "dev-1",
		Launcher:       "gemini",
		Label:          "docs",
		CWD:            "/Users/test/project",
		CommandPreview: "gemini",
		GitBranch:      "release/docs",
		PlatformFamily: "macos",
		PlatformID:     "macos",
		ComputerName:   "Yuanbo's MacBook Pro",
		LaunchSource:   SessionLaunchSourceLocal,
	}

	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	got := string(raw)
	for _, want := range []string{
		"session_id",
		"device_id",
		"launcher",
		"label",
		"cwd",
		"command_preview",
		"git_branch",
		"platform_family",
		"platform_id",
		"computer_name",
		"launch_source",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("json = %s, want field %q", got, want)
		}
	}
	if strings.Contains(got, "latest_seq") {
		t.Fatalf("json = %s, did not expect latest_seq", got)
	}
	if strings.Contains(got, "terminate_supported") {
		t.Fatalf("json = %s, did not expect terminate_supported", got)
	}
	if strings.Contains(got, `"origin"`) {
		t.Fatalf("json = %s, did not expect legacy origin", got)
	}
}

func TestSessionSummaryUsesStableDeviceIdentityKeysWhenUnset(t *testing.T) {
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

	if strings.Contains(string(raw), `"label":`) {
		t.Fatalf("json = %s, did not expect label", raw)
	}
	if !strings.Contains(string(raw), `"platform_family":""`) {
		t.Fatalf("json = %s, want empty platform_family", raw)
	}
	if !strings.Contains(string(raw), `"platform_id":""`) {
		t.Fatalf("json = %s, want empty platform_id", raw)
	}
	if !strings.Contains(string(raw), `"computer_name":""`) {
		t.Fatalf("json = %s, want empty computer_name", raw)
	}
	if !strings.Contains(string(raw), `"git_branch":""`) {
		t.Fatalf("json = %s, want empty git_branch", raw)
	}
	if !strings.Contains(string(raw), `"device_id":""`) {
		t.Fatalf("json = %s, want empty device_id", raw)
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
