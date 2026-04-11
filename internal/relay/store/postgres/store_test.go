package postgres

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"yuanbohan/tunnel/internal/relay/auth"
)

func TestPostgresStoreIntegration(t *testing.T) {
	dsn := os.Getenv("AGENTUNNEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AGENTUNNEL_TEST_DATABASE_URL is not set")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := RunMigrations(ctx, db, testSchemaDir(t)); err != nil {
		t.Fatalf("RunMigrations returned error: %v", err)
	}

	store := NewPostgresStore(db)
	digester, err := auth.NewSecretDigester("integration-secret")
	if err != nil {
		t.Fatalf("NewSecretDigester returned error: %v", err)
	}
	code, err := auth.GenerateInviteCode()
	if err != nil {
		t.Fatalf("GenerateInviteCode returned error: %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.CreateInviteCode(ctx, auth.CreateInviteCodeParams{
		CodeDigest: digester.Digest(code),
		CodeHint:   code[len(code)-2:],
		CreatedBy:  "integration-test",
		ExpiresAt:  now.Add(30 * time.Minute),
		Now:        now,
	}); err != nil {
		t.Fatalf("CreateInviteCode returned error: %v", err)
	}

	hasher := auth.PasswordHasher{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	}
	passwordHash, err := hasher.HashPassword(ctx, "password123")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}

	user, err := store.RegisterUser(ctx, auth.RegisterUserParams{
		UsernameNorm:     "integration-user",
		InviteCodeDigest: digester.Digest(code),
		PasswordHash:     passwordHash,
		Now:              now,
	})
	if err != nil {
		t.Fatalf("RegisterUser returned error: %v", err)
	}
	if user.UsernameNorm != "integration-user" {
		t.Fatalf("UsernameNorm = %q, want integration-user", user.UsernameNorm)
	}

	session, err := store.CreateAppSession(ctx, auth.CreateAppSessionParams{
		ID:                 "appsess-integration",
		UserID:             user.ID,
		AccessTokenDigest:  digester.Digest("access-token"),
		AccessExpiresAt:    now.Add(time.Hour),
		RefreshTokenDigest: digester.Digest("refresh-token"),
		RefreshExpiresAt:   now.Add(24 * time.Hour),
		Now:                now,
	})
	if err != nil {
		t.Fatalf("CreateAppSession returned error: %v", err)
	}
	if session.ID != "appsess-integration" {
		t.Fatalf("session.ID = %q, want appsess-integration", session.ID)
	}

	newPasswordHash, err := hasher.HashPassword(ctx, "better-password123")
	if err != nil {
		t.Fatalf("HashPassword for new password returned error: %v", err)
	}
	changeTime := now.Add(time.Minute)
	if err := store.ChangeUserPassword(ctx, user.ID, newPasswordHash, changeTime); err != nil {
		t.Fatalf("ChangeUserPassword returned error: %v", err)
	}

	updatedUser, err := store.FindUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("FindUserByID returned error: %v", err)
	}
	if updatedUser.PasswordHash != newPasswordHash {
		t.Fatal("password hash was not updated")
	}

	_, err = store.FindAppSessionByAccessToken(ctx, digester.Digest("access-token"), changeTime)
	if err != auth.ErrAppSessionRevoked {
		t.Fatalf("FindAppSessionByAccessToken error = %v, want ErrAppSessionRevoked", err)
	}
}

func testSchemaDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join("..", "..", "..", "..", "schema", "relay")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("stat schema dir %q: %v", dir, err)
	}
	return dir
}
