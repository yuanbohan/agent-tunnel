package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
)

type sessionHistoryResponse struct {
	Messages []struct {
		Seq     uint64 `json:"seq"`
		DataB64 string `json:"data_b64"`
	} `json:"messages"`
	HasMore     bool   `json:"has_more"`
	LatestSeq   uint64 `json:"latest_seq"`
	LastReadSeq uint64 `json:"last_read_seq"`
}

type readStateResponse struct {
	SessionID   string `json:"session_id"`
	LatestSeq   uint64 `json:"latest_seq"`
	LastReadSeq uint64 `json:"last_read_seq"`
	UnreadCount uint64 `json:"unread_count"`
}

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
	reg.Register(protocol.SessionInfo{
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

	var sessions []protocol.SessionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "sess-1" {
		t.Fatalf("sessions = %#v, want sess-1", sessions)
	}
	for _, want := range []string{"latest_seq", "last_read_seq", "unread_count", "preview_seq", "preview_b64"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("body = %s, want field %q", rec.Body.String(), want)
		}
	}
}

func TestHandlerServesSessionHistoryAndReadState(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()

	for _, output := range []string{"one", "two", "three"} {
		if err := agentConn.WriteJSON(protocol.EncodeOutput([]byte(output))); err != nil {
			t.Fatalf("WriteJSON output returned error: %v", err)
		}
	}
	waitForLatestSeq(t, reg, "sess-1", 3)

	historyReq := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/history", nil)
	historyReq.Header.Set("Authorization", basicAuth("demo", "secret"))
	historyRec := httptest.NewRecorder()
	NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}).ServeHTTP(historyRec, historyReq)

	if historyRec.Code != http.StatusOK {
		t.Fatalf("history status = %d, want 200", historyRec.Code)
	}

	var history sessionHistoryResponse
	if err := json.Unmarshal(historyRec.Body.Bytes(), &history); err != nil {
		t.Fatalf("Unmarshal history returned error: %v", err)
	}
	if history.LatestSeq != 3 {
		t.Fatalf("LatestSeq = %d, want 3", history.LatestSeq)
	}
	if history.LastReadSeq != 0 {
		t.Fatalf("LastReadSeq = %d, want 0", history.LastReadSeq)
	}
	if history.HasMore {
		t.Fatal("HasMore = true, want false")
	}
	if len(history.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(history.Messages))
	}
	for i, want := range []string{"one", "two", "three"} {
		if history.Messages[i].Seq != uint64(i+1) {
			t.Fatalf("message %d seq = %d, want %d", i, history.Messages[i].Seq, i+1)
		}
		data, err := base64.StdEncoding.DecodeString(history.Messages[i].DataB64)
		if err != nil {
			t.Fatalf("DecodeString returned error: %v", err)
		}
		if string(data) != want {
			t.Fatalf("message %d data = %q, want %q", i, string(data), want)
		}
	}

	historyReq = httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/history?before=3&limit=1", nil)
	historyReq.Header.Set("Authorization", basicAuth("demo", "secret"))
	historyRec = httptest.NewRecorder()
	NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}).ServeHTTP(historyRec, historyReq)

	if historyRec.Code != http.StatusOK {
		t.Fatalf("paged history status = %d, want 200", historyRec.Code)
	}
	if err := json.Unmarshal(historyRec.Body.Bytes(), &history); err != nil {
		t.Fatalf("Unmarshal paged history returned error: %v", err)
	}
	if !history.HasMore {
		t.Fatal("HasMore = false, want true for paged history")
	}
	if len(history.Messages) != 1 || history.Messages[0].Seq != 2 {
		t.Fatalf("paged history messages = %#v, want seq 2 only", history.Messages)
	}

	historyReq = httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/history?after=2&before=4", nil)
	historyReq.Header.Set("Authorization", basicAuth("demo", "secret"))
	historyRec = httptest.NewRecorder()
	NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}).ServeHTTP(historyRec, historyReq)

	if historyRec.Code != http.StatusOK {
		t.Fatalf("bounded after history status = %d, want 200", historyRec.Code)
	}
	if err := json.Unmarshal(historyRec.Body.Bytes(), &history); err != nil {
		t.Fatalf("Unmarshal bounded after history returned error: %v", err)
	}
	if history.HasMore {
		t.Fatal("bounded after history HasMore = true, want false")
	}
	if len(history.Messages) != 1 || history.Messages[0].Seq != 3 {
		t.Fatalf("bounded after history messages = %#v, want seq 3 only", history.Messages)
	}

	readBody := strings.NewReader(`{"seq":2}`)
	readReq := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/read", readBody)
	readReq.Header.Set("Authorization", basicAuth("demo", "secret"))
	readReq.Header.Set("Content-Type", "application/json")
	readRec := httptest.NewRecorder()
	NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}).ServeHTTP(readRec, readReq)

	if readRec.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200", readRec.Code)
	}

	var afterRead readStateResponse
	if err := json.Unmarshal(readRec.Body.Bytes(), &afterRead); err != nil {
		t.Fatalf("Unmarshal read response returned error: %v", err)
	}
	if afterRead.LastReadSeq != 2 {
		t.Fatalf("LastReadSeq = %d, want 2", afterRead.LastReadSeq)
	}
	if afterRead.UnreadCount != 1 {
		t.Fatalf("UnreadCount = %d, want 1", afterRead.UnreadCount)
	}

	readBody = strings.NewReader(`{"seq":1}`)
	readReq = httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/read", readBody)
	readReq.Header.Set("Authorization", basicAuth("demo", "secret"))
	readReq.Header.Set("Content-Type", "application/json")
	readRec = httptest.NewRecorder()
	NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}).ServeHTTP(readRec, readReq)

	if readRec.Code != http.StatusOK {
		t.Fatalf("read regression status = %d, want 200", readRec.Code)
	}
	if err := json.Unmarshal(readRec.Body.Bytes(), &afterRead); err != nil {
		t.Fatalf("Unmarshal read regression response returned error: %v", err)
	}
	if afterRead.LastReadSeq != 2 {
		t.Fatalf("LastReadSeq regression = %d, want 2", afterRead.LastReadSeq)
	}
	if afterRead.UnreadCount != 1 {
		t.Fatalf("UnreadCount regression = %d, want 1", afterRead.UnreadCount)
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

func TestHandlerLogsAgentAuthFailure(t *testing.T) {
	reg := NewRegistry()
	logs := newLogRecorder()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Logger:          NewLogger(logs),
		Files:           testFiles(),
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/agent/ws")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	entry := waitForLogEvent(t, logs, "auth_failed", func(entry map[string]any) bool {
		return entry["path"] == "/agent/ws" && entry["auth_type"] == "bearer"
	})
	if entry["remote_addr"] == "" {
		t.Fatalf("remote_addr = %v, want non-empty", entry["remote_addr"])
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

func TestHandlerServesBuiltInRelayShellWhenIndexHTMLIsMissing(t *testing.T) {
	reg := NewRegistry()
	handler := NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Files:           fstest.MapFS{},
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

	register := protocol.RegisterFrame(protocol.SessionInfo{
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

func TestBrowserAttachAddsSequenceNumbersToOutputFrames(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()

	browserURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/ws"
	browserHeaders := http.Header{}
	browserHeaders.Set("Authorization", basicAuth("demo", "secret"))
	browserConn, _, err := websocket.DefaultDialer.Dial(browserURL, browserHeaders)
	if err != nil {
		t.Fatalf("Dial browser returned error: %v", err)
	}
	defer browserConn.Close()

	if err := agentConn.WriteJSON(protocol.EncodeOutput([]byte("hello"))); err != nil {
		t.Fatalf("WriteJSON output returned error: %v", err)
	}

	_, raw, err := browserConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage browser returned error: %v", err)
	}
	if !strings.Contains(string(raw), `"seq":1`) {
		t.Fatalf("browser frame = %s, want seq 1", string(raw))
	}
}

func TestAgentResizeBroadcastsToBrowser(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()

	browserURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/ws"
	browserHeaders := http.Header{}
	browserHeaders.Set("Authorization", basicAuth("demo", "secret"))
	browserConn, _, err := websocket.DefaultDialer.Dial(browserURL, browserHeaders)
	if err != nil {
		t.Fatalf("Dial browser returned error: %v", err)
	}
	defer browserConn.Close()

	if err := agentConn.WriteJSON(protocol.Message{Type: "resize", Cols: 120, Rows: 40}); err != nil {
		t.Fatalf("WriteJSON resize returned error: %v", err)
	}

	_ = browserConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var got protocol.Message
	if err := browserConn.ReadJSON(&got); err != nil {
		t.Fatalf("ReadJSON browser returned error: %v", err)
	}
	if got.Type != "resize" {
		t.Fatalf("Type = %q, want resize", got.Type)
	}
	if got.Cols != 120 || got.Rows != 40 {
		t.Fatalf("resize = %dx%d, want 120x40", got.Cols, got.Rows)
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

	if err := agentConn.WriteJSON(protocol.RegisterFrame(protocol.SessionInfo{
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

func dialAndRegisterAgent(t *testing.T, serverURL, sessionID string) *websocket.Conn {
	t.Helper()

	agentURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer agent-token")
	agentConn, _, err := websocket.DefaultDialer.Dial(agentURL, headers)
	if err != nil {
		t.Fatalf("Dial agent returned error: %v", err)
	}

	if err := agentConn.WriteJSON(protocol.RegisterFrame(protocol.SessionInfo{
		SessionID: sessionID,
		Launcher:  "codex",
	})); err != nil {
		t.Fatalf("WriteJSON register returned error: %v", err)
	}
	return agentConn
}

func waitForLatestSeq(t *testing.T, reg *Registry, sessionID string, want uint64) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		info, ok := reg.Session(sessionID)
		if ok && info.LatestSeq >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	info, ok := reg.Session(sessionID)
	if !ok {
		t.Fatalf("session %q disappeared while waiting for latest_seq", sessionID)
	}
	t.Fatalf("LatestSeq = %d, want at least %d", info.LatestSeq, want)
}

func TestBrowserAttachRejectsForeignOrigin(t *testing.T) {
	reg := NewRegistry()
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})
	logs := newLogRecorder()

	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Logger:          NewLogger(logs),
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

	entry := waitForLogEvent(t, logs, "ws_upgrade_failed", func(entry map[string]any) bool {
		return entry["path"] == "/api/sessions/sess-1/ws" && entry["role"] == "client"
	})
	if entry["remote_addr"] == "" {
		t.Fatalf("remote_addr = %v, want non-empty", entry["remote_addr"])
	}
}

func TestBrowserAttachAllowsSameOrigin(t *testing.T) {
	reg := NewRegistry()
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})

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

func TestBrowserAttachStaysLiveAcrossSameSessionReplacementAndRoutesToNewAgent(t *testing.T) {
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

	oldAgentConn, _, err := websocket.DefaultDialer.Dial(agentURL, agentHeaders)
	if err != nil {
		t.Fatalf("Dial old agent returned error: %v", err)
	}
	defer oldAgentConn.Close()

	if err := oldAgentConn.WriteJSON(protocol.RegisterFrame(protocol.SessionInfo{
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

	newAgentConn, _, err := websocket.DefaultDialer.Dial(agentURL, agentHeaders)
	if err != nil {
		t.Fatalf("Dial new agent returned error: %v", err)
	}
	defer newAgentConn.Close()

	if err := newAgentConn.WriteJSON(protocol.RegisterFrame(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})); err != nil {
		t.Fatalf("WriteJSON replacement register returned error: %v", err)
	}

	if err := newAgentConn.WriteJSON(protocol.EncodeOutput([]byte("after-replace"))); err != nil {
		t.Fatalf("WriteJSON output returned error: %v", err)
	}

	_ = browserConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var output protocol.Message
	if err := browserConn.ReadJSON(&output); err != nil {
		t.Fatalf("ReadJSON browser returned error: %v", err)
	}
	if output.Type != "output" {
		t.Fatalf("Type = %q, want output", output.Type)
	}
	outputData, err := protocol.DecodeData(output)
	if err != nil {
		t.Fatalf("DecodeData output returned error: %v", err)
	}
	if string(outputData) != "after-replace" {
		t.Fatalf("output = %q, want after-replace", string(outputData))
	}

	if err := browserConn.WriteJSON(protocol.Message{
		Type: "input",
		Data: base64.StdEncoding.EncodeToString([]byte("hello-new")),
	}); err != nil {
		t.Fatalf("WriteJSON browser input returned error: %v", err)
	}

	_ = newAgentConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var input protocol.Message
	if err := newAgentConn.ReadJSON(&input); err != nil {
		t.Fatalf("ReadJSON new agent returned error: %v", err)
	}
	if input.Type != "input" {
		t.Fatalf("Type = %q, want input", input.Type)
	}
	inputData, err := protocol.DecodeData(input)
	if err != nil {
		t.Fatalf("DecodeData input returned error: %v", err)
	}
	if string(inputData) != "hello-new" {
		t.Fatalf("input = %q, want hello-new", string(inputData))
	}
}

func TestAgentDisconnectRemovesSessionFromList(t *testing.T) {
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
		t.Fatalf("Dial returned error: %v", err)
	}

	if err := agentConn.WriteJSON(protocol.RegisterFrame(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	waitForSessionCount(t, server.URL, basicAuth("demo", "secret"), 1)

	_ = agentConn.Close()
	waitForSessionCount(t, server.URL, basicAuth("demo", "secret"), 0)
}

func TestAgentRegisterLogsLifecycle(t *testing.T) {
	reg := NewRegistry()
	logs := newLogRecorder()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Logger:          NewLogger(logs),
	}))
	defer server.Close()

	agentURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer agent-token")
	agentConn, _, err := websocket.DefaultDialer.Dial(agentURL, headers)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}

	connected := waitForLogEvent(t, logs, "agent_ws_connected", func(entry map[string]any) bool {
		return entry["path"] == "/agent/ws"
	})
	if connected["remote_addr"] == "" {
		t.Fatalf("remote_addr = %v, want non-empty", connected["remote_addr"])
	}

	if err := agentConn.WriteJSON(protocol.RegisterFrame(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
		Label:     "api-fix",
		CWD:       "/tmp/project",
	})); err != nil {
		t.Fatalf("WriteJSON register returned error: %v", err)
	}

	registered := waitForLogEvent(t, logs, "agent_registered", func(entry map[string]any) bool {
		return entry["session_id"] == "sess-1"
	})
	if registered["launcher"] != "codex" {
		t.Fatalf("launcher = %v, want codex", registered["launcher"])
	}
	if registered["label"] != "api-fix" {
		t.Fatalf("label = %v, want api-fix", registered["label"])
	}
	if registered["cwd"] != "/tmp/project" {
		t.Fatalf("cwd = %v, want /tmp/project", registered["cwd"])
	}

	_ = agentConn.Close()
	waitForSessionCount(t, server.URL, basicAuth("demo", "secret"), 0)

	disconnected := waitForLogEvent(t, logs, "agent_disconnected", func(entry map[string]any) bool {
		return entry["session_id"] == "sess-1"
	})
	if disconnected["reason"] == "" {
		t.Fatalf("reason = %v, want non-empty", disconnected["reason"])
	}
	if _, ok := disconnected["duration_ms"].(float64); !ok {
		t.Fatalf("duration_ms = %T, want number", disconnected["duration_ms"])
	}
}

func TestClientAttachLogsLifecycle(t *testing.T) {
	reg := NewRegistry()
	logs := newLogRecorder()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Logger:          NewLogger(logs),
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()
	waitForLogEvent(t, logs, "agent_registered", func(entry map[string]any) bool {
		return entry["session_id"] == "sess-1"
	})

	browserURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	headers.Set("User-Agent", "relay-test-client/1.0")

	browserConn, _, err := websocket.DefaultDialer.Dial(browserURL, headers)
	if err != nil {
		t.Fatalf("Dial browser returned error: %v", err)
	}

	connected := waitForLogEvent(t, logs, "client_ws_connected", func(entry map[string]any) bool {
		return entry["session_id"] == "sess-1" && entry["user_agent"] == "relay-test-client/1.0"
	})
	clientID, ok := connected["client_id"].(string)
	if !ok || clientID == "" {
		t.Fatalf("client_id = %v, want non-empty string", connected["client_id"])
	}
	if connected["remote_addr"] == "" {
		t.Fatalf("remote_addr = %v, want non-empty", connected["remote_addr"])
	}

	_ = browserConn.Close()

	disconnected := waitForLogEvent(t, logs, "client_disconnected", func(entry map[string]any) bool {
		return entry["session_id"] == "sess-1" && entry["client_id"] == clientID
	})
	if disconnected["reason"] == "" {
		t.Fatalf("reason = %v, want non-empty", disconnected["reason"])
	}
	if _, ok := disconnected["duration_ms"].(float64); !ok {
		t.Fatalf("duration_ms = %T, want number", disconnected["duration_ms"])
	}
}

func TestStaleAgentConnectionTimesOutAndRemovesSessionFromList(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:              reg,
		BrowserUser:           "demo",
		BrowserPassword:       "secret",
		AgentToken:            "agent-token",
		AgentReadTimeout:      75 * time.Millisecond,
		AgentPingInterval:     20 * time.Millisecond,
		AgentPingWriteTimeout: 20 * time.Millisecond,
	}))
	defer server.Close()

	agentURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer agent-token")
	agentConn, _, err := websocket.DefaultDialer.Dial(agentURL, headers)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer agentConn.Close()

	if err := agentConn.WriteJSON(protocol.RegisterFrame(protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	waitForSessionCount(t, server.URL, basicAuth("demo", "secret"), 1)
	waitForSessionCount(t, server.URL, basicAuth("demo", "secret"), 0)
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

		var sessions []protocol.SessionInfo
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
		"index.html": {
			Data: []byte("<!doctype html><div id=\"relay-root\">relay-root</div>"),
		},
	}
}

type logRecorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newLogRecorder() *logRecorder {
	return &logRecorder{}
}

func (r *logRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *logRecorder) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.buf.Bytes()...)
}

func waitForLogEvent(t *testing.T, logs *logRecorder, event string, match func(map[string]any) bool) map[string]any {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, entry := range parseLogEntries(t, logs.snapshot()) {
			if entry["event"] != event {
				continue
			}
			if match == nil || match(entry) {
				return entry
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for log event %q in %q", event, string(logs.snapshot()))
	return nil
}

func parseLogEntries(t *testing.T, raw []byte) []map[string]any {
	t.Helper()

	lines := bytes.Split(raw, []byte{'\n'})
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("unmarshal log line %q: %v", string(line), err)
		}
		entries = append(entries, entry)
	}
	return entries
}
