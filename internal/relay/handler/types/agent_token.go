package types

type CreateAgentTokenRequest struct {
	Name string `json:"name"`
}

type AgentTokenResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at,omitempty"`
	RevokedAt  *int64 `json:"revoked_at,omitempty"`
}

type CreatedAgentTokenResponse struct {
	AgentTokenResponse
	Token string `json:"token"`
}
