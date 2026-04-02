package relayserver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"yuanbohan/tunnel/internal/relayapi"
)

func TestHandlerRejectsDashboardWithoutBasicAuth(t *testing.T) {
	reg := NewRegistry()
	handler := NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Files:           testFiles(),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandlerReturnsLiveSessionsWithBasicAuth(t *testing.T) {
	reg := NewRegistry()
	reg.Register(relayapi.SessionInfo{
		SessionID:      "sess-1",
		Launcher:       "codex",
		Label:          "api-fix",
		CommandPreview: "codex --profile prod",
		CWD:            "/tmp/project",
		StartedAt:      time.Unix(10, 0),
	}, fakeAgentPeer{})

	handler := NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Files:           testFiles(),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var sessions []relayapi.SessionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "sess-1" {
		t.Fatalf("sessions = %#v, want sess-1", sessions)
	}
}

func TestHandlerRejectsAgentWebSocketWithWrongToken(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Files:           testFiles(),
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/agent/ws")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestHandlerServesRelayShellOnRootAndSessionPath(t *testing.T) {
	reg := NewRegistry()
	handler := NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Files:           testFiles(),
	})

	for _, path := range []string{"/", "/sessions/sess-1"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", basicAuth("demo", "secret"))
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "relay-root") {
			t.Fatalf("%s body = %q, want relay-root shell marker", path, rec.Body.String())
		}
	}
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func testFiles() fstest.MapFS {
	return fstest.MapFS{
		"relay.html": {
			Data: []byte("<!doctype html><div id=\"relay-root\">relay-root</div>"),
		},
	}
}
