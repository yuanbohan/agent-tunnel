package main

import (
	"bytes"
	"context"
	"testing"
)

type userClientStub struct {
	username string
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
