package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelayAuthAPILoginAndCreateAgentToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			if r.Method != http.MethodPost {
				t.Fatalf("login method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"code":0,"message":"success","body":{"access_token":"app-token","refresh_token":"refresh","expires_in":86400,"token_type":"Bearer"}}`)
		case "/api/agent-tokens":
			if got := r.Header.Get("Authorization"); got != "Bearer app-token" {
				t.Fatalf("Authorization = %q, want Bearer app-token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"code":0,"message":"success","body":{"id":"tok_123","name":"tunnel-devbox-20260417-120000","created_at":1712345600,"token":"agent-token"}}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	api := newRelayAuthAPI(server.URL)
	session, err := api.login(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatalf("login returned error: %v", err)
	}
	if session.AccessToken != "app-token" {
		t.Fatalf("AccessToken = %q, want app-token", session.AccessToken)
	}

	created, err := api.createAgentToken(context.Background(), session.AccessToken, "tunnel-devbox-20260417-120000")
	if err != nil {
		t.Fatalf("createAgentToken returned error: %v", err)
	}
	if created.Token != "agent-token" {
		t.Fatalf("Token = %q, want agent-token", created.Token)
	}
	if created.ID != "tok_123" {
		t.Fatalf("ID = %q, want tok_123", created.ID)
	}
}

func TestRelayAuthAPIPropagatesEnvelopeErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"code":1011,"message":"The username or password is invalid.","body":null}`)
	}))
	defer server.Close()

	api := newRelayAuthAPI(server.URL)
	_, err := api.login(context.Background(), "alice", "wrong")
	if err == nil {
		t.Fatal("login error = nil, want unauthorized error")
	}
}
