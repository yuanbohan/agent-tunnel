package types

type StopSessionResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}
