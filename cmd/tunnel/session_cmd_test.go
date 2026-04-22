package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunSessionListRendersBorderedTable(t *testing.T) {
	setEnv(t, tunnelAuthTokenEnv, "")
	store := &fakeStore{
		record: storedAuth{Token: "agent-token"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sessions" || r.Method != http.MethodGet {
			t.Fatalf("request = %s %s, want GET /api/sessions", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer agent-token" {
			t.Fatalf("Authorization = %q, want Bearer agent-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":0,"message":"success","body":[{"session_id":"1700000000000000000","launcher":"codex","label":"very-long-label-that-should-truncate","cwd":"/Users/alice/workspace/github.com/example/repo","command_preview":"codex --profile production --very-long-flag","started_at":1700000000,"platform_family":"macos","platform_id":"macos-arm64","computer_name":"Alice Very Long MacBook Pro","launch_source":"mobile"},{"session_id":"1700000000000000001","launcher":"claude","cwd":"/repo","command_preview":"claude","started_at":1700000001}]}`)
	}))
	defer server.Close()

	oldNewStore := newAuthStore
	t.Cleanup(func() {
		newAuthStore = oldNewStore
	})
	newAuthStore = func() authStore { return store }

	var stdout bytes.Buffer
	if err := runSessionList(context.Background(), sessionCommandArgs{BaseURL: server.URL}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("runSessionList returned error: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"+---------+", "| Scope", "| Source", "| Session", "mobile", "local", "..."} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want substring %q", output, want)
		}
	}
	if strings.Contains(output, "macos-arm64") {
		t.Fatalf("output = %q, did not expect platform_id", output)
	}
}

func TestRunSessionStopCallsStopEndpoint(t *testing.T) {
	setEnv(t, tunnelAuthTokenEnv, "")
	store := &fakeStore{
		record: storedAuth{Token: "agent-token"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sessions/sess-1/stop" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s, want POST /api/sessions/sess-1/stop", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer agent-token" {
			t.Fatalf("Authorization = %q, want Bearer agent-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":0,"message":"success","body":{"session_id":"sess-1","status":"stopped"}}`)
	}))
	defer server.Close()

	oldNewStore := newAuthStore
	t.Cleanup(func() {
		newAuthStore = oldNewStore
	})
	newAuthStore = func() authStore { return store }

	var stdout bytes.Buffer
	if err := runSessionStop(context.Background(), sessionCommandArgs{BaseURL: server.URL}, "sess-1", &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("runSessionStop returned error: %v", err)
	}
	if got := stdout.String(); got != "stopped session sess-1\n" {
		t.Fatalf("stdout = %q, want stopped message", got)
	}
}
