package relayserver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
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

func TestHandlerServesBuiltInRelayShellWhenRelayHTMLIsMissing(t *testing.T) {
	reg := NewRegistry()
	handler := NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Files: fstest.MapFS{
			"index.html": {
				Data: []byte("<!doctype html><div id=\"index-root\">index-root</div>"),
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "relay-root") {
		t.Fatalf("body = %q, want built-in relay shell marker", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "index-root") {
		t.Fatalf("body = %q, want built-in relay shell instead of index.html", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "/ws") {
		t.Fatalf("body = %q, want no localhost /ws dependency in relay fallback shell", rec.Body.String())
	}
}

func TestAgentRegisterAddsLiveSessionAndBrowserAttachReceivesOutput(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}))
	defer server.Close()

	agentURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer agent-token")
	agentConn, _, err := websocket.DefaultDialer.Dial(agentURL, headers)
	if err != nil {
		t.Fatalf("Dial agent returned error: %v", err)
	}
	defer agentConn.Close()

	register := relayapi.RegisterFrame(relayapi.SessionInfo{
		SessionID:      "sess-1",
		Launcher:       "codex",
		Label:          "api-fix",
		CommandPreview: "codex --profile prod",
		CWD:            "/tmp/project",
	})
	if err := agentConn.WriteJSON(register); err != nil {
		t.Fatalf("WriteJSON register returned error: %v", err)
	}

	browserURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/ws"
	browserHeaders := http.Header{}
	browserHeaders.Set("Authorization", basicAuth("demo", "secret"))
	browserConn, _, err := websocket.DefaultDialer.Dial(browserURL, browserHeaders)
	if err != nil {
		t.Fatalf("Dial browser returned error: %v", err)
	}
	defer browserConn.Close()

	output := protocol.EncodeOutput([]byte("world"))
	if err := agentConn.WriteJSON(output); err != nil {
		t.Fatalf("WriteJSON output returned error: %v", err)
	}

	var got protocol.Message
	if err := browserConn.ReadJSON(&got); err != nil {
		t.Fatalf("ReadJSON browser returned error: %v", err)
	}
	if got.Type != "output" {
		t.Fatalf("Type = %q, want output", got.Type)
	}
}

func TestBrowserAttachRoutesInputToRegisteredAgent(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}))
	defer server.Close()

	agentURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/agent/ws"
	agentHeaders := http.Header{}
	agentHeaders.Set("Authorization", "Bearer agent-token")
	agentConn, _, err := websocket.DefaultDialer.Dial(agentURL, agentHeaders)
	if err != nil {
		t.Fatalf("Dial agent returned error: %v", err)
	}
	defer agentConn.Close()

	if err := agentConn.WriteJSON(relayapi.RegisterFrame(relayapi.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})); err != nil {
		t.Fatalf("WriteJSON register returned error: %v", err)
	}

	browserURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/ws"
	browserHeaders := http.Header{}
	browserHeaders.Set("Authorization", basicAuth("demo", "secret"))
	browserConn, _, err := websocket.DefaultDialer.Dial(browserURL, browserHeaders)
	if err != nil {
		t.Fatalf("Dial browser returned error: %v", err)
	}
	defer browserConn.Close()

	if err := browserConn.WriteJSON(protocol.Message{
		Type: "input",
		Data: base64.StdEncoding.EncodeToString([]byte("hello")),
	}); err != nil {
		t.Fatalf("WriteJSON browser input returned error: %v", err)
	}

	var got protocol.Message
	if err := agentConn.ReadJSON(&got); err != nil {
		t.Fatalf("ReadJSON agent returned error: %v", err)
	}
	if got.Type != "input" {
		t.Fatalf("Type = %q, want input", got.Type)
	}
	data, err := protocol.DecodeData(got)
	if err != nil {
		t.Fatalf("DecodeData returned error: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("input = %q, want hello", string(data))
	}
}

func TestBrowserAttachRejectsForeignOrigin(t *testing.T) {
	reg := NewRegistry()
	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})

	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}))
	defer server.Close()

	browserURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	headers.Set("Origin", "https://evil.example")

	_, resp, err := websocket.DefaultDialer.Dial(browserURL, headers)
	if err == nil {
		t.Fatal("Dial browser succeeded with foreign origin, want failure")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", responseStatusCode(resp))
	}
}

func TestBrowserAttachAllowsSameOrigin(t *testing.T) {
	reg := NewRegistry()
	reg.Register(relayapi.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})

	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	browserURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	headers.Set("Origin", parsedURL.Scheme+"://"+parsedURL.Host)

	browserConn, _, err := websocket.DefaultDialer.Dial(browserURL, headers)
	if err != nil {
		t.Fatalf("Dial browser returned error: %v", err)
	}
	defer browserConn.Close()
}

func TestBrowserAttachSurvivesTemporaryAgentDisconnectAndResumesAfterReconnect(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}))
	defer server.Close()

	agentURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/agent/ws"
	browserURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/ws"

	agentHeaders := http.Header{}
	agentHeaders.Set("Authorization", "Bearer agent-token")
	browserHeaders := http.Header{}
	browserHeaders.Set("Authorization", basicAuth("demo", "secret"))

	agentConn, _, err := websocket.DefaultDialer.Dial(agentURL, agentHeaders)
	if err != nil {
		t.Fatalf("Dial agent returned error: %v", err)
	}
	if err := agentConn.WriteJSON(relayapi.RegisterFrame(relayapi.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})); err != nil {
		t.Fatalf("WriteJSON register returned error: %v", err)
	}

	browserConn, _, err := websocket.DefaultDialer.Dial(browserURL, browserHeaders)
	if err != nil {
		t.Fatalf("Dial browser returned error: %v", err)
	}
	defer browserConn.Close()

	if err := agentConn.Close(); err != nil {
		t.Fatalf("Close agent returned error: %v", err)
	}

	waitForSessionCount(t, server.URL, browserHeaders.Get("Authorization"), 0)

	offlineConn, resp, err := websocket.DefaultDialer.Dial(browserURL, browserHeaders)
	if err == nil {
		offlineConn.Close()
		t.Fatal("Dial browser while session offline succeeded, want failure")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("offline attach status = %v, want 404", responseStatusCode(resp))
	}

	agentConn, _, err = websocket.DefaultDialer.Dial(agentURL, agentHeaders)
	if err != nil {
		t.Fatalf("Dial agent reconnect returned error: %v", err)
	}
	defer agentConn.Close()

	if err := agentConn.WriteJSON(relayapi.RegisterFrame(relayapi.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})); err != nil {
		t.Fatalf("WriteJSON reconnect register returned error: %v", err)
	}

	if err := agentConn.WriteJSON(protocol.EncodeOutput([]byte("after-reconnect"))); err != nil {
		t.Fatalf("WriteJSON output returned error: %v", err)
	}

	_ = browserConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var got protocol.Message
	if err := browserConn.ReadJSON(&got); err != nil {
		t.Fatalf("ReadJSON browser returned error: %v", err)
	}
	if got.Type != "output" {
		t.Fatalf("Type = %q, want output", got.Type)
	}
	data, err := protocol.DecodeData(got)
	if err != nil {
		t.Fatalf("DecodeData returned error: %v", err)
	}
	if string(data) != "after-reconnect" {
		t.Fatalf("output = %q, want after-reconnect", string(data))
	}
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func waitForSessionCount(t *testing.T, baseURL, auth string, want int) {
	t.Helper()

	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/api/sessions", nil)
		if err != nil {
			t.Fatalf("NewRequest returned error: %v", err)
		}
		req.Header.Set("Authorization", auth)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do returned error: %v", err)
		}
		var sessions []relayapi.SessionInfo
		if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
			resp.Body.Close()
			t.Fatalf("Decode returned error: %v", err)
		}
		resp.Body.Close()
		if len(sessions) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %d live sessions", want)
}

func responseStatusCode(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func testFiles() fstest.MapFS {
	return fstest.MapFS{
		"relay.html": {
			Data: []byte("<!doctype html><div id=\"relay-root\">relay-root</div>"),
		},
	}
}
