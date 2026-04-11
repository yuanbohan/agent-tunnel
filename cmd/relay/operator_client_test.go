package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yuanbohan/tunnel/internal/relay"
)

func TestHTTPOperatorClientCreateInvites(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != relay.OperatorInviteCodesPath {
			t.Fatalf("path = %q, want %s", r.URL.Path, relay.OperatorInviteCodesPath)
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
		_, _ = w.Write([]byte(`{"codes":["AB2C3D","EF4G5H","JK7M8N"]}`))
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
		_, _ = w.Write([]byte(`{"reason":"invite_code_disabled"}`))
	}))
	defer server.Close()

	client := newHTTPOperatorClient(server.URL, "operator-secret", server.Client())
	err := client.DisableInvite(context.Background(), "AB2C3D")
	if err == nil {
		t.Fatal("expected DisableInvite to fail")
	}
	if err.Error() != "invite_code_disabled" {
		t.Fatalf("error = %q, want invite_code_disabled", err.Error())
	}
}

func TestOperatorAPIPathsStayOutsidePublicAPINamespace(t *testing.T) {
	paths := []string{
		relay.OperatorInviteCodesPath,
		relay.OperatorInviteDisablePath,
		relay.OperatorDeleteUserPath,
	}
	for _, path := range paths {
		if strings.HasPrefix(path, "/api/") {
			t.Fatalf("path = %q, want non-/api operator path", path)
		}
	}
}
