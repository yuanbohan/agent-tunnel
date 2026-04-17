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
	if strings.Contains(string(registerPayload), `"accepted"`) {
		t.Fatalf("register payload = %s, want accepted omitted", registerPayload)
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
}

func TestDeviceLaunchResultLegacyAcceptedFieldStillDecodes(t *testing.T) {
	var frame DeviceFrame
	if err := json.Unmarshal([]byte(`{"type":"launch_result","request_id":"req-1","accepted":true}`), &frame); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if frame.Accepted == nil || !*frame.Accepted {
		t.Fatalf("frame = %#v, want accepted=true", frame)
	}
}
