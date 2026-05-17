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

func TestLaunchReadyFrame(t *testing.T) {
	frame := LaunchReadyFrame(LaunchContext{Source: SessionLaunchSourceMobile, RequestID: "req-123"})
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var decoded AgentFrame
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if decoded.Type != "launch_ready" {
		t.Fatalf("Type = %q, want launch_ready", decoded.Type)
	}
	if decoded.LaunchContext == nil || decoded.LaunchContext.Source != SessionLaunchSourceMobile || decoded.LaunchContext.RequestID != "req-123" {
		t.Fatalf("LaunchContext = %#v, want mobile req-123", decoded.LaunchContext)
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
	for _, removed := range []string{"latest_seq", "terminate_supported", `"origin"`, "client_id"} {
		if strings.Contains(got, removed) {
			t.Fatalf("json = %s, did not expect removed field %q", got, removed)
		}
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
