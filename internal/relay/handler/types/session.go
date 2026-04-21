package types

type TerminateSessionResponse struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}
