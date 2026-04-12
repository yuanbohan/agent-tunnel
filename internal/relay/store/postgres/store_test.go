package postgres

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"yuanbohan/tunnel/internal/migration"
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
	if err := migration.RunMigrations(ctx, db, testSchemaDir(t)); err != nil {
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
	suffix := strconv.FormatInt(now.UnixNano(), 10)
	username := "integration-user-" + suffix
	sessionID := "appsess-integration-" + suffix
	accessToken := "access-token-" + suffix
	refreshToken := "refresh-token-" + suffix
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
		UsernameNorm:     username,
		InviteCodeDigest: digester.Digest(code),
		PasswordHash:     passwordHash,
		Now:              now,
	})
	if err != nil {
		t.Fatalf("RegisterUser returned error: %v", err)
	}
	if user.UsernameNorm != username {
		t.Fatalf("UsernameNorm = %q, want %q", user.UsernameNorm, username)
	}

	session, err := store.CreateAppSession(ctx, auth.CreateAppSessionParams{
		ID:                 sessionID,
		UserID:             user.ID,
		AccessTokenDigest:  digester.Digest(accessToken),
		AccessExpiresAt:    now.Add(time.Hour),
		RefreshTokenDigest: digester.Digest(refreshToken),
		RefreshExpiresAt:   now.Add(24 * time.Hour),
		Now:                now,
	})
	if err != nil {
		t.Fatalf("CreateAppSession returned error: %v", err)
	}
	if session.ID != sessionID {
		t.Fatalf("session.ID = %q, want %q", session.ID, sessionID)
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

	_, err = store.FindAppSessionByAccessToken(ctx, digester.Digest(accessToken), changeTime, auth.DefaultAppSessionAbsoluteTTL)
	if err != auth.ErrAppSessionRevoked {
		t.Fatalf("FindAppSessionByAccessToken error = %v, want ErrAppSessionRevoked", err)
	}
}

func TestPostgresStoreEnforcesAbsoluteSessionBoundaryDuringRefresh(t *testing.T) {
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
	if err := migration.RunMigrations(ctx, db, testSchemaDir(t)); err != nil {
		t.Fatalf("RunMigrations returned error: %v", err)
	}

	store := NewPostgresStore(db)
	digester, err := auth.NewSecretDigester("integration-secret")
	if err != nil {
		t.Fatalf("NewSecretDigester returned error: %v", err)
	}

	baseNow := time.Now().UTC()
	createdAt := baseNow.Add(-(auth.DefaultAppSessionAbsoluteTTL - time.Hour))
	absoluteExpiresAt := createdAt.Add(auth.DefaultAppSessionAbsoluteTTL)
	suffix := strconv.FormatInt(baseNow.UnixNano(), 10)
	username := "integration-absolute-user-" + suffix
	sessionID := "appsess-absolute-" + suffix
	accessToken := "access-token-" + suffix
	refreshToken := "refresh-token-" + suffix
	nextAccessToken := "next-access-token-" + suffix
	nextRefreshToken := "next-refresh-token-" + suffix

	code, err := auth.GenerateInviteCode()
	if err != nil {
		t.Fatalf("GenerateInviteCode returned error: %v", err)
	}
	if _, err := store.CreateInviteCode(ctx, auth.CreateInviteCodeParams{
		CodeDigest: digester.Digest(code),
		CodeHint:   code[len(code)-2:],
		CreatedBy:  "integration-test",
		ExpiresAt:  createdAt.Add(2 * time.Hour),
		Now:        createdAt,
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
		UsernameNorm:     username,
		InviteCodeDigest: digester.Digest(code),
		PasswordHash:     passwordHash,
		Now:              createdAt,
	})
	if err != nil {
		t.Fatalf("RegisterUser returned error: %v", err)
	}

	if _, err := store.CreateAppSession(ctx, auth.CreateAppSessionParams{
		ID:                 sessionID,
		UserID:             user.ID,
		AccessTokenDigest:  digester.Digest(accessToken),
		AccessExpiresAt:    absoluteExpiresAt,
		RefreshTokenDigest: digester.Digest(refreshToken),
		RefreshExpiresAt:   absoluteExpiresAt,
		Now:                createdAt,
	}); err != nil {
		t.Fatalf("CreateAppSession returned error: %v", err)
	}

	refreshNow := absoluteExpiresAt.Add(-30 * time.Minute)
	store.now = func() time.Time { return refreshNow }

	refreshed, err := store.RotateAppSessionByRefreshToken(ctx, auth.RotateAppSessionParams{
		RefreshTokenDigest:    digester.Digest(refreshToken),
		NewAccessTokenDigest:  digester.Digest(nextAccessToken),
		NewAccessExpiresAt:    refreshNow.Add(auth.DefaultAccessTokenTTL),
		NewRefreshTokenDigest: digester.Digest(nextRefreshToken),
		NewRefreshExpiresAt:   refreshNow.Add(auth.DefaultRefreshTokenTTL),
		AbsoluteTTL:           auth.DefaultAppSessionAbsoluteTTL,
		Now:                   refreshNow,
	})
	if err != nil {
		t.Fatalf("RotateAppSessionByRefreshToken returned error: %v", err)
	}
	if !refreshed.AccessExpiresAt.Equal(absoluteExpiresAt) {
		t.Fatalf("AccessExpiresAt = %s, want %s", refreshed.AccessExpiresAt, absoluteExpiresAt)
	}
	if !refreshed.RefreshExpiresAt.Equal(absoluteExpiresAt) {
		t.Fatalf("RefreshExpiresAt = %s, want %s", refreshed.RefreshExpiresAt, absoluteExpiresAt)
	}

	if _, err := store.FindAppSessionByAccessToken(ctx, digester.Digest(nextAccessToken), absoluteExpiresAt, auth.DefaultAppSessionAbsoluteTTL); err != auth.ErrAppSessionExpired {
		t.Fatalf("FindAppSessionByAccessToken at absolute expiry error = %v, want ErrAppSessionExpired", err)
	}

	store.now = func() time.Time { return absoluteExpiresAt }
	if _, err := store.RotateAppSessionByRefreshToken(ctx, auth.RotateAppSessionParams{
		RefreshTokenDigest:    digester.Digest(nextRefreshToken),
		NewAccessTokenDigest:  digester.Digest("late-access-token-" + suffix),
		NewAccessExpiresAt:    absoluteExpiresAt.Add(auth.DefaultAccessTokenTTL),
		NewRefreshTokenDigest: digester.Digest("late-refresh-token-" + suffix),
		NewRefreshExpiresAt:   absoluteExpiresAt.Add(auth.DefaultRefreshTokenTTL),
		AbsoluteTTL:           auth.DefaultAppSessionAbsoluteTTL,
		Now:                   absoluteExpiresAt.Add(-time.Second),
	}); err != auth.ErrAppSessionExpired {
		t.Fatalf("RotateAppSessionByRefreshToken at absolute expiry error = %v, want ErrAppSessionExpired", err)
	}
}

func testSchemaDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join("..", "..", "..", "..", "schema")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("stat schema dir %q: %v", dir, err)
	}
	return dir
}
