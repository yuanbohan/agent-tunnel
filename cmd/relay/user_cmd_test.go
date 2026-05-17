package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	handlertypes "yuanbohan/tunnel/internal/relay/handler/types"
)

type userClientStub struct {
	username string
	tier     string
}

func (s *userClientStub) CreateInvites(_ context.Context, count int, expiresInDays int) ([]string, error) {
	return nil, nil
}

func (s *userClientStub) DisableInvite(_ context.Context, code string) error {
	return nil
}

func (s *userClientStub) DeleteUser(_ context.Context, usernameNorm string) error {
	s.username = usernameNorm
	return nil
}

func (s *userClientStub) ListInvites(_ context.Context) ([]handlertypes.OperatorInviteCodeListEntry, error) {
	return nil, nil
}

func (s *userClientStub) SetUserTier(_ context.Context, username string, tier string) (handlertypes.OperatorSetUserTierResponse, error) {
	s.username = username
	s.tier = tier
	return handlertypes.OperatorSetUserTierResponse{
		Username:     username,
		PreviousTier: "free",
		Tier:         tier,
	}, nil
}

func TestRunUserDeleteNormalizesUsername(t *testing.T) {
	client := &userClientStub{}
	var stdout bytes.Buffer

	err := runUserDelete(context.Background(), client, userDeleteConfig{
		Username: "Alice",
	}, &stdout)
	if err != nil {
		t.Fatalf("runUserDelete returned error: %v", err)
	}
	if client.username != "alice" {
		t.Fatalf("username = %q, want alice", client.username)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("deleted alice")) {
		t.Fatalf("stdout = %q, want deleted alice", stdout.String())
	}
}

func TestRunUserTierNormalizesInputs(t *testing.T) {
	client := &userClientStub{}
	var stdout bytes.Buffer

	err := runUserTier(context.Background(), client, userTierConfig{
		Username: "Alice",
		Tier:     "PRO",
	}, &stdout)
	if err != nil {
		t.Fatalf("runUserTier returned error: %v", err)
	}
	if client.username != "alice" {
		t.Fatalf("username = %q, want alice", client.username)
	}
	if client.tier != "pro" {
		t.Fatalf("tier = %q, want pro", client.tier)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("set alice tier free -> pro")) {
		t.Fatalf("stdout = %q, want tier transition", stdout.String())
	}
}

func TestRunUserTierJSONPrintsStructuredResponse(t *testing.T) {
	client := &userClientStub{}
	var stdout bytes.Buffer

	err := runUserTier(context.Background(), client, userTierConfig{
		Username: "Alice",
		Tier:     "PRO",
		JSON:     true,
	}, &stdout)
	if err != nil {
		t.Fatalf("runUserTier returned error: %v", err)
	}
	var response handlertypes.OperatorSetUserTierResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("JSON unmarshal returned error: %v\n%s", err, stdout.String())
	}
	if response.Username != "alice" || response.PreviousTier != "free" || response.Tier != "pro" {
		t.Fatalf("response = %#v, want alice free -> pro", response)
	}
}
