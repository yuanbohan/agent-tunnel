package protocol

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
	CWD       string      `json:"cwd,omitempty"`
	Label     string      `json:"label,omitempty"`
	Status    string      `json:"status,omitempty"`
	Reason    string      `json:"reason,omitempty"`
}

func DeviceRegisterFrame(info DeviceInfo) DeviceFrame {
	return DeviceFrame{
		Type:   "register",
		Device: &info,
	}
}

func DeviceLaunchRequestFrame(requestID, command, cwd, label string) DeviceFrame {
	return DeviceFrame{
		Type:      "launch_request",
		RequestID: requestID,
		Command:   command,
		CWD:       cwd,
		Label:     label,
	}
}

func DeviceLaunchResultFrame(requestID, status, reason string) DeviceFrame {
	return DeviceFrame{
		Type:      "launch_result",
		RequestID: requestID,
		Status:    status,
		Reason:    reason,
	}
}
