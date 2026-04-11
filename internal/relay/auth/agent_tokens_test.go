package auth

import (
	"context"
	"testing"
	"time"
)

func TestAgentTokenServiceCreateListAuthenticateAndRevoke(t *testing.T) {
	now := time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	store := newFakeStore(func() time.Time { return now })
	user := User{
		ID:           1,
		Username:     "alice",
		UsernameNorm: "alice",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	store.usersByName[user.UsernameNorm] = user
	store.usersByID[user.ID] = user

	digester, err := NewSecretDigester("token-secret")
	if err != nil {
		t.Fatalf("NewSecretDigester returned error: %v", err)
	}
	service := NewAgentTokenService(store, digester)
	service.now = func() time.Time { return now }

	created, err := service.Create(context.Background(), user.ID, "MacBook")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.Plaintext == "" {
		t.Fatal("expected plaintext token")
	}

	listed, err := service.List(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("len(List) = %d, want 1", len(listed))
	}

	authenticated, err := service.Authenticate(context.Background(), created.Plaintext)
	if err != nil {
		t.Fatalf("Authenticate returned error: %v", err)
	}
	if authenticated.User.ID != user.ID {
		t.Fatalf("authenticated user = %d, want %d", authenticated.User.ID, user.ID)
	}

	if err := service.Revoke(context.Background(), user.ID, created.Record.ID, "alice"); err != nil {
		t.Fatalf("Revoke returned error: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), created.Plaintext); err == nil {
		t.Fatal("expected revoked token to fail")
	}
}
