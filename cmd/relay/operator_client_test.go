package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	handlertypes "yuanbohan/tunnel/internal/relay/handler/types"
)

func TestHTTPOperatorClientCreateInvites(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != handlertypes.OperatorInviteCodesPath {
			t.Fatalf("path = %q, want %s", r.URL.Path, handlertypes.OperatorInviteCodesPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer operator-secret" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if got := strings.TrimSpace(string(body)); got != `{"count":3,"expires_in_days":7}` {
			t.Fatalf("body = %q, want operator create payload", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"code":0,"message":"success","body":{"codes":["AB2C3D","EF4G5H","JK7M8N"]}}`))
	}))
	defer server.Close()

	client := newHTTPOperatorClient(server.URL, "operator-secret", server.Client())
	codes, err := client.CreateInvites(context.Background(), 3, 7)
	if err != nil {
		t.Fatalf("CreateInvites returned error: %v", err)
	}
	if len(codes) != 3 || codes[0] != "AB2C3D" {
		t.Fatalf("codes = %#v, want created invite codes", codes)
	}
}

func TestHTTPOperatorClientReturnsReasonOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":1008,"message":"This invite code has been disabled.","body":null}`))
	}))
	defer server.Close()

	client := newHTTPOperatorClient(server.URL, "operator-secret", server.Client())
	err := client.DisableInvite(context.Background(), "AB2C3D")
	if err == nil {
		t.Fatal("expected DisableInvite to fail")
	}
	if err.Error() != "This invite code has been disabled." {
		t.Fatalf("error = %q, want invite disabled message", err.Error())
	}
}

func TestHTTPOperatorClientRejectsLegacySuccessBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"codes":["AB2C3D"]}`))
	}))
	defer server.Close()

	client := newHTTPOperatorClient(server.URL, "operator-secret", server.Client())
	_, err := client.CreateInvites(context.Background(), 1, 7)
	if err == nil {
		t.Fatal("expected CreateInvites to fail on legacy success body")
	}
	if !strings.Contains(err.Error(), "invalid envelope success body") {
		t.Fatalf("error = %q, want invalid envelope success body", err.Error())
	}
}

func TestHTTPOperatorClientRejectsLegacyErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"reason":"invite_code_disabled"}`))
	}))
	defer server.Close()

	client := newHTTPOperatorClient(server.URL, "operator-secret", server.Client())
	err := client.DisableInvite(context.Background(), "AB2C3D")
	if err == nil {
		t.Fatal("expected DisableInvite to fail on legacy error body")
	}
	if !strings.Contains(err.Error(), "invalid envelope error body") {
		t.Fatalf("error = %q, want invalid envelope error body", err.Error())
	}
}

func TestHTTPOperatorClientListInvites(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != handlertypes.OperatorInviteListPath {
			t.Fatalf("path = %q, want %s", r.URL.Path, handlertypes.OperatorInviteListPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer operator-secret" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if got := strings.TrimSpace(string(body)); got != `null` {
			t.Fatalf("body = %q, want null body", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"message":"success","body":{"invite_codes":[{"code":"AB2C3D","created_by":"operator","created_at":1,"expires_at":1000,"expired":false,"available":true,"disabled":false,"consumed":false}]}}`))
	}))
	defer server.Close()

	client := newHTTPOperatorClient(server.URL, "operator-secret", server.Client())
	invites, err := client.ListInvites(context.Background())
	if err != nil {
		t.Fatalf("ListInvites returned error: %v", err)
	}
	if len(invites) != 1 || invites[0].Code != "AB2C3D" {
		t.Fatalf("invites = %#v, want one invite code AB2C3D", invites)
	}
}

func TestHTTPOperatorClientSetUserTier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != handlertypes.OperatorUserTierPath {
			t.Fatalf("path = %q, want %s", r.URL.Path, handlertypes.OperatorUserTierPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer operator-secret" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if got := strings.TrimSpace(string(body)); got != `{"username":"alice","tier":"pro"}` {
			t.Fatalf("body = %q, want operator tier payload", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"message":"success","body":{"username":"alice","previous_tier":"free","tier":"pro"}}`))
	}))
	defer server.Close()

	client := newHTTPOperatorClient(server.URL, "operator-secret", server.Client())
	updated, err := client.SetUserTier(context.Background(), "alice", "pro")
	if err != nil {
		t.Fatalf("SetUserTier returned error: %v", err)
	}
	if updated.Username != "alice" || updated.PreviousTier != "free" || updated.Tier != "pro" {
		t.Fatalf("updated = %#v, want alice free -> pro", updated)
	}
}

func TestOperatorAPIPathsStayOutsidePublicAPINamespace(t *testing.T) {
	paths := []string{
		handlertypes.OperatorInviteCodesPath,
		handlertypes.OperatorInviteDisablePath,
		handlertypes.OperatorInviteListPath,
		handlertypes.OperatorDeleteUserPath,
		handlertypes.OperatorUserTierPath,
	}
	for _, path := range paths {
		if strings.HasPrefix(path, "/api/") {
			t.Fatalf("path = %q, want non-/api operator path", path)
		}
	}
}
