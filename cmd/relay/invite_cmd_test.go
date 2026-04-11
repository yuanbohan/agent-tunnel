package main

import (
	"bytes"
	"context"
	"testing"
)

type inviteClientStub struct {
	create struct {
		count         int
		expiresInDays int
	}
	disableCode string
	codes       []string
}

func (s *inviteClientStub) CreateInvites(_ context.Context, count int, expiresInDays int) ([]string, error) {
	s.create.count = count
	s.create.expiresInDays = expiresInDays
	return s.codes, nil
}

func (s *inviteClientStub) DisableInvite(_ context.Context, code string) error {
	s.disableCode = code
	return nil
}

func (s *inviteClientStub) DeleteUser(_ context.Context, username string) error {
	return nil
}

func TestRunInviteCreatePrintsReturnedCodes(t *testing.T) {
	client := &inviteClientStub{codes: []string{"AB2C3D", "EF4G5H"}}
	var stdout bytes.Buffer

	err := runInviteCreate(context.Background(), client, inviteCreateConfig{
		Count:         2,
		ExpiresInDays: 7,
	}, &stdout)
	if err != nil {
		t.Fatalf("runInviteCreate returned error: %v", err)
	}
	if client.create.count != 2 {
		t.Fatalf("count = %d, want 2", client.create.count)
	}
	if client.create.expiresInDays != 7 {
		t.Fatalf("expiresInDays = %d, want 7", client.create.expiresInDays)
	}
	if got := stdout.String(); got != "AB2C3D\nEF4G5H\n" {
		t.Fatalf("stdout = %q, want both invite codes", got)
	}
}

func TestRunInviteDisableNormalizesCode(t *testing.T) {
	client := &inviteClientStub{}
	var stdout bytes.Buffer

	err := runInviteDisable(context.Background(), client, inviteDisableConfig{
		Code: "ab2c3d",
	}, &stdout)
	if err != nil {
		t.Fatalf("runInviteDisable returned error: %v", err)
	}
	if client.disableCode != "AB2C3D" {
		t.Fatalf("disableCode = %q, want AB2C3D", client.disableCode)
	}
}
