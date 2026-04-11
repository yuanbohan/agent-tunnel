package types

import "time"

type CreateAgentTokenRequest struct {
	Name string `json:"name"`
}

type AgentTokenResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type CreatedAgentTokenResponse struct {
	AgentTokenResponse
	Token string `json:"token"`
}
