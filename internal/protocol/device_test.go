package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeviceLaunchFrames(t *testing.T) {
	register := DeviceRegisterFrame(DeviceInfo{DeviceID: "dev_1"})
	if register.Type != "register" || register.Device == nil || register.Device.DeviceID != "dev_1" {
		t.Fatalf("register = %#v, want register frame with device info", register)
	}
	registerPayload, err := json.Marshal(register)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(registerPayload), `"status"`) {
		t.Fatalf("register payload = %s, want status omitted", registerPayload)
	}

	update := DeviceUpdateFrame(DeviceInfo{DeviceID: "dev_1", LaunchHealth: "healthy"})
	if update.Type != "update" || update.Device == nil || update.Device.LaunchHealth != "healthy" {
		t.Fatalf("update = %#v, want update frame with launch health", update)
	}

	request := DeviceLaunchRequestFrame("req-1", "codex --help", "/repo", "api-fix")
	if request.Type != "launch_request" || request.RequestID != "req-1" || request.Command != "codex --help" || request.CWD != "/repo" || request.Label != "api-fix" {
		t.Fatalf("request = %#v, want launch_request frame", request)
	}
	requestPayload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(requestPayload), `"cwd":"/repo"`) {
		t.Fatalf("request payload = %s, want cwd", requestPayload)
	}

	result := DeviceLaunchResultFrame("req-1", "failed", "busy")
	if result.Type != "launch_result" || result.RequestID != "req-1" || result.Status != "failed" || result.Reason != "busy" {
		t.Fatalf("result = %#v, want launch_result frame", result)
	}

	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(payload), `"status":"failed"`) {
		t.Fatalf("payload = %s, want explicit status", payload)
	}

	accepted := DeviceLaunchResultFrameWithWorkspace("req-2", "accepted", "", "launch_fixed")
	if accepted.Type != "launch_result" || accepted.WorkspaceSession != "launch_fixed" {
		t.Fatalf("accepted = %#v, want workspace session on accepted launch result", accepted)
	}

	terminateRequest := DeviceTerminateRequestFrame("term-1", "sess-1", "launch_fixed")
	if terminateRequest.Type != "terminate_request" || terminateRequest.RequestID != "term-1" || terminateRequest.SessionID != "sess-1" || terminateRequest.WorkspaceSession != "launch_fixed" {
		t.Fatalf("terminateRequest = %#v, want terminate_request frame", terminateRequest)
	}

	terminateResult := DeviceTerminateResultFrame("term-1", "terminated", "")
	if terminateResult.Type != "terminate_result" || terminateResult.RequestID != "term-1" || terminateResult.Status != "terminated" {
		t.Fatalf("terminateResult = %#v, want terminate_result frame", terminateResult)
	}
}
