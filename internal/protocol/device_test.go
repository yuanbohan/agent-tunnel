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

	request := DeviceLaunchRequestFrame("req-1", "codex --help")
	if request.Type != "launch_request" || request.RequestID != "req-1" || request.Command != "codex --help" {
		t.Fatalf("request = %#v, want launch_request frame", request)
	}
	requestPayload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(requestPayload), `"accepted"`) {
		t.Fatalf("request payload = %s, want accepted omitted", requestPayload)
	}

	result := DeviceLaunchResultFrame("req-1", false, "busy")
	if result.Type != "launch_result" || result.RequestID != "req-1" || result.Accepted || result.Reason != "busy" {
		t.Fatalf("result = %#v, want launch_result frame", result)
	}

	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(payload), `"accepted":false`) {
		t.Fatalf("payload = %s, want explicit accepted:false", payload)
	}
}
