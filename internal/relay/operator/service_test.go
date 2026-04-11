package operator

import (
	"context"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/relay/auth"
)

type batchStoreStub struct {
	params []auth.CreateInviteCodeParams
	err    error
}

func (s *batchStoreStub) CreateInviteCode(context.Context, auth.CreateInviteCodeParams) (auth.InviteCodeRecord, error) {
	return auth.InviteCodeRecord{}, nil
}

func (s *batchStoreStub) CreateInviteCodes(_ context.Context, params []auth.CreateInviteCodeParams) error {
	s.params = append([]auth.CreateInviteCodeParams(nil), params...)
	return s.err
}

func (s *batchStoreStub) DisableInviteCode(context.Context, string, string, time.Time) error {
	return nil
}

func (s *batchStoreStub) DeleteUser(context.Context, string, string, time.Time) (auth.DeleteUserResult, error) {
	return auth.DeleteUserResult{}, nil
}

func TestOperatorServiceCreateInviteCodesBatchesWrites(t *testing.T) {
	store := &batchStoreStub{}
	digester, err := auth.NewSecretDigester("operator-secret")
	if err != nil {
		t.Fatalf("NewSecretDigester returned error: %v", err)
	}

	now := time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	service := NewOperatorService(store, digester)
	service.now = func() time.Time { return now }

	codes, err := service.CreateInviteCodes(context.Background(), 2, 7)
	if err != nil {
		t.Fatalf("CreateInviteCodes returned error: %v", err)
	}
	if len(codes) != 2 {
		t.Fatalf("len(codes) = %d, want 2", len(codes))
	}
	if len(store.params) != 2 {
		t.Fatalf("len(store.params) = %d, want 2", len(store.params))
	}
	for i, code := range codes {
		if store.params[i].CodeDigest != digester.Digest(code) {
			t.Fatalf("CodeDigest = %q, want digest of %q", store.params[i].CodeDigest, code)
		}
		if store.params[i].CodeHint != code[len(code)-2:] {
			t.Fatalf("CodeHint = %q, want %q", store.params[i].CodeHint, code[len(code)-2:])
		}
	}
}
