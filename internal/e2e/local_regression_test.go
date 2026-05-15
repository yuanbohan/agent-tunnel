package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/relay/handler/response"
)

const scenarioTimeout = 2 * time.Minute

func TestLocalRegressionFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), scenarioTimeout)
	defer cancel()

	h := newHarness(t)
	h.Prepare(ctx)

	client := newAppClient(h.baseURL)
	runID := time.Now().UTC().UnixNano()
	username := fmt.Sprintf("e2euser%d", runID)
	password := "password123"
	newPassword := "betterpass456"

	inviteCode := h.CreateInvite(ctx)
	register, err := client.Register(inviteCode, username, password)
	if err != nil {
		t.Fatalf("register returned error: %v", err)
	}
	if register.Username != username {
		t.Fatalf("register username = %q, want %q", register.Username, username)
	}

	issued, err := client.Login(username, password)
	if err != nil {
		t.Fatalf("login returned error: %v", err)
	}

	createdToken, err := client.CreateAgentToken(issued.AccessToken, "Local E2E")
	if err != nil {
		t.Fatalf("CreateAgentToken returned error: %v", err)
	}

	tunnel := h.StartTunnel(createdToken.Token)
	waitForTunnelOutput(t, ctx, tunnel, "READY e2e-launcher")

	status, envelope, err := client.GetAPIStatus("/api/sessions", issued.AccessToken)
	if err != nil {
		t.Fatalf("GetAPIStatus /api/sessions returned error: %v", err)
	}
	if status != 404 {
		t.Fatalf("removed session list status = %d envelope=%#v, want 404", status, envelope)
	}
	if envelope.Code != response.CodeNotFound || envelope.Message != "The requested endpoint was not found." {
		t.Fatalf("removed session list envelope = %#v, want endpoint-not-found envelope", envelope)
	}

	writeTunnelInput(t, tunnel, "ping\r")
	waitForTunnelOutput(t, ctx, tunnel, "REPLY ping")

	if err := client.ChangePassword(issued.AccessToken, password, newPassword); err != nil {
		t.Fatalf("ChangePassword returned error: %v", err)
	}
	if tunnel.Exited() {
		t.Fatalf("tunnel exited after password change: %v\n%s", tunnel.WaitErr(), tunnel.Tail())
	}
	writeTunnelInput(t, tunnel, "after-change\r")
	waitForTunnelOutput(t, ctx, tunnel, "REPLY after-change")

	status, envelope, err = client.GetAPIStatus("/api/account/policy", issued.AccessToken)
	if err != nil {
		t.Fatalf("GetAPIStatus /api/account/policy returned error: %v", err)
	}
	if status != 401 {
		t.Fatalf("old access token status = %d envelope=%#v, want 401", status, envelope)
	}
	if envelope.Code != 1016 || envelope.Message != "The request is unauthorized." {
		t.Fatalf("old access token envelope = %#v, want unauthorized envelope", envelope)
	}

	relogin, err := client.Login(username, newPassword)
	if err != nil {
		t.Fatalf("relogin returned error: %v", err)
	}
	if _, err := client.AccountPolicy(relogin.AccessToken); err != nil {
		t.Fatalf("AccountPolicy after relogin returned error: %v", err)
	}

	assertDurableState(t, ctx, h, username, register.UserID)
}

func assertDurableState(t *testing.T, ctx context.Context, h *Harness, username string, userID int64) {
	t.Helper()

	user, err := loadUserByUsername(ctx, h.db, username)
	if err != nil {
		t.Fatalf("loadUserByUsername returned error: %v", err)
	}
	if user.ID != userID {
		t.Fatalf("user.ID = %d, want %d", user.ID, userID)
	}
	if user.UsernameNorm != username {
		t.Fatalf("user.UsernameNorm = %q, want %q", user.UsernameNorm, username)
	}

	invite, err := loadInviteForUser(ctx, h.db, user.ID)
	if err != nil {
		t.Fatalf("loadInviteForUser returned error: %v", err)
	}
	if !invite.ConsumedAt.Valid {
		t.Fatal("invite was not marked consumed")
	}
	if !invite.ConsumedByUserID.Valid || invite.ConsumedByUserID.Int64 != user.ID {
		t.Fatalf("invite consumed_by_user_id = %#v, want %d", invite.ConsumedByUserID, user.ID)
	}
	if invite.ConsumedByUsername != user.Username {
		t.Fatalf("invite consumed_by_username = %q, want %q", invite.ConsumedByUsername, user.Username)
	}

	appSessions, err := loadAppSessionsForUser(ctx, h.db, user.ID)
	if err != nil {
		t.Fatalf("loadAppSessionsForUser returned error: %v", err)
	}
	if len(appSessions) != 2 {
		t.Fatalf("len(appSessions) = %d, want 2", len(appSessions))
	}
	if !appSessions[0].RevokedAt.Valid || appSessions[0].RevokeReason != "password_changed" {
		t.Fatalf("first app session = %#v, want revoked password_changed", appSessions[0])
	}
	if appSessions[1].RevokedAt.Valid {
		t.Fatalf("second app session = %#v, want active session", appSessions[1])
	}

	agentTokens, err := loadAgentTokensForUser(ctx, h.db, user.ID)
	if err != nil {
		t.Fatalf("loadAgentTokensForUser returned error: %v", err)
	}
	if len(agentTokens) != 1 {
		t.Fatalf("len(agentTokens) = %d, want 1", len(agentTokens))
	}
	if agentTokens[0].Name != "Local E2E" {
		t.Fatalf("agent token name = %q, want Local E2E", agentTokens[0].Name)
	}
	if !agentTokens[0].LastUsedAt.Valid {
		t.Fatalf("agent token = %#v, want last_used_at set", agentTokens[0])
	}
	if agentTokens[0].RevokedAt.Valid {
		t.Fatalf("agent token = %#v, want active token", agentTokens[0])
	}
}

func writeTunnelInput(t *testing.T, tunnel *TunnelProcess, input string) {
	t.Helper()

	if _, err := tunnel.ptmx.Write([]byte(input)); err != nil {
		t.Fatalf("write tunnel input %q returned error: %v", input, err)
	}
}

func waitForTunnelOutput(t *testing.T, ctx context.Context, tunnel *TunnelProcess, want string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tunnel.Exited() {
			t.Fatalf("tunnel exited before output %q appeared: %v\n%s", want, tunnel.WaitErr(), tunnel.Tail())
		}
		if strings.Contains(tunnel.Tail(), want) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context canceled while waiting for tunnel output %q: %v", want, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for tunnel output %q\n%s", want, tunnel.Tail())
}
