package types

type DeviceLaunchRequest struct {
	Command string `json:"command"`
}

type DeviceLaunchResponse struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}
