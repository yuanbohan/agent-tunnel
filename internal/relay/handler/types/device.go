package types

type DeviceLaunchRequest struct {
	Command string `json:"command"`
	CWD     string `json:"cwd"`
	Label   string `json:"label,omitempty"`
}

type DeviceLaunchResponse struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	SessionID string `json:"session_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type ComputerInfo struct {
	ComputerID     string `json:"computer_id"`
	DisplayName    string `json:"display_name"`
	PlatformFamily string `json:"platform_family"`
	PlatformID     string `json:"platform_id"`
	LaunchHealth   string `json:"launch_health,omitempty"`
}
