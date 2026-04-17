package protocol

import "encoding/json"

type DeviceInfo struct {
	DeviceID       string `json:"device_id"`
	DisplayName    string `json:"display_name"`
	PlatformFamily string `json:"platform_family"`
	PlatformID     string `json:"platform_id"`
}

type DeviceFrame struct {
	Type      string      `json:"type"`
	Device    *DeviceInfo `json:"device,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
	Command   string      `json:"command,omitempty"`
	Accepted  bool        `json:"accepted"`
	Reason    string      `json:"reason,omitempty"`
}

func (f DeviceFrame) MarshalJSON() ([]byte, error) {
	type wireFrame struct {
		Type      string      `json:"type"`
		Device    *DeviceInfo `json:"device,omitempty"`
		RequestID string      `json:"request_id,omitempty"`
		Command   string      `json:"command,omitempty"`
		Accepted  *bool       `json:"accepted,omitempty"`
		Reason    string      `json:"reason,omitempty"`
	}

	wire := wireFrame{
		Type:      f.Type,
		Device:    f.Device,
		RequestID: f.RequestID,
		Command:   f.Command,
		Reason:    f.Reason,
	}
	if f.Type == "launch_result" {
		accepted := f.Accepted
		wire.Accepted = &accepted
	}
	return json.Marshal(wire)
}

func DeviceRegisterFrame(info DeviceInfo) DeviceFrame {
	return DeviceFrame{
		Type:   "register",
		Device: &info,
	}
}

func DeviceLaunchRequestFrame(requestID, command string) DeviceFrame {
	return DeviceFrame{
		Type:      "launch_request",
		RequestID: requestID,
		Command:   command,
	}
}

func DeviceLaunchResultFrame(requestID string, accepted bool, reason string) DeviceFrame {
	return DeviceFrame{
		Type:      "launch_result",
		RequestID: requestID,
		Accepted:  accepted,
		Reason:    reason,
	}
}
