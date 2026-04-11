package api

import (
	"testing"
	"time"

	"yuanbohan/tunnel/internal/relay/auth"
)

func TestNewAgentTokenResponseUsesUnixSeconds(t *testing.T) {
	createdAt := time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	lastUsedAt := createdAt.Add(time.Minute)
	revokedAt := createdAt.Add(2 * time.Minute)

	resp := newAgentTokenResponse(auth.AgentTokenRecord{
		ID:         "agt_123",
		Name:       "Laptop",
		CreatedAt:  createdAt,
		LastUsedAt: &lastUsedAt,
		RevokedAt:  &revokedAt,
	})

	if resp.CreatedAt != createdAt.Unix() {
		t.Fatalf("CreatedAt = %d, want %d", resp.CreatedAt, createdAt.Unix())
	}
	if resp.LastUsedAt == nil || *resp.LastUsedAt != lastUsedAt.Unix() {
		t.Fatalf("LastUsedAt = %v, want %d", resp.LastUsedAt, lastUsedAt.Unix())
	}
	if resp.RevokedAt == nil || *resp.RevokedAt != revokedAt.Unix() {
		t.Fatalf("RevokedAt = %v, want %d", resp.RevokedAt, revokedAt.Unix())
	}
}
