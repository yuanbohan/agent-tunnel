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
