package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
)

type sessionFramesResponse []struct {
	Seq     uint64 `json:"seq"`
	DataB64 string `json:"data_b64"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

func TestHandlerRejectsDashboardWithoutBasicAuth(t *testing.T) {
	reg := NewRegistry()
	handler := NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
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
	if !strings.Contains(rec.Body.String(), "latest_seq") {
		t.Fatalf("body = %s, want field %q", rec.Body.String(), "latest_seq")
	}
}

func TestHandlerServesSessionFrames(t *testing.T) {
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

	for _, frame := range []struct {
		cols int
		rows int
		data string
	}{
		{cols: 120, rows: 40, data: "one"},
		{cols: 120, rows: 40, data: "two"},
		{cols: 132, rows: 43, data: "three"},
	} {
		if err := agentConn.WriteJSON(protocol.Message{Type: "resize", Cols: frame.cols, Rows: frame.rows}); err != nil {
			t.Fatalf("WriteJSON resize returned error: %v", err)
		}
		if err := agentConn.WriteJSON(protocol.EncodeOutput([]byte(frame.data))); err != nil {
			t.Fatalf("WriteJSON output returned error: %v", err)
		}
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/sessions/sess-1/frames?from=2&to=3", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	req.Header.Set("Authorization", basicAuth("demo", "secret"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var frames sessionFramesResponse
	if err := json.NewDecoder(resp.Body).Decode(&frames); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("len(Frames) = %d, want 2", len(frames))
	}
	if frames[0].Seq != 2 || frames[1].Seq != 3 {
		t.Fatalf("seqs = %#v, want 2 then 3", frames)
	}
}

func TestHandlerRejectsInvalidFrameRange(t *testing.T) {
	reg := NewRegistry()
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})
	handler := NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/frames?from=3&to=2", nil)
	req.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpdatesWebSocketStreamsOutputAndRemoval(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")

	updatesURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/updates/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	updatesConn, _, err := websocket.DefaultDialer.Dial(updatesURL, headers)
	if err != nil {
		t.Fatalf("Dial updates returned error: %v", err)
	}
	defer updatesConn.Close()

	if err := agentConn.WriteJSON(protocol.EncodeOutput([]byte("hello"))); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	var output protocol.ClientUpdateMessage
	if err := updatesConn.ReadJSON(&output); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	if output.Type != "output" || output.SessionID != "sess-1" {
		t.Fatalf("output = %#v, want sess-1 output", output)
	}
	data, err := base64.StdEncoding.DecodeString(output.Data)
	if err != nil {
		t.Fatalf("DecodeString returned error: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q, want hello", string(data))
	}

	_ = agentConn.Close()

	var removed protocol.ClientUpdateMessage
	if err := updatesConn.ReadJSON(&removed); err != nil {
		t.Fatalf("ReadJSON removal returned error: %v", err)
	}
	if removed.Type != "session_removed" || removed.SessionID != "sess-1" {
		t.Fatalf("removed = %#v, want sess-1 session_removed", removed)
	}
}

func TestUpdatesWebSocketForwardsClientInputToAgent(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
	}))
	defer server.Close()

	peer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	updatesURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/updates/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	conn, _, err := websocket.DefaultDialer.Dial(updatesURL, headers)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	msg := protocol.ClientUpdateMessage{
		SessionID: "sess-1",
		Type:      "input",
		Data:      base64.StdEncoding.EncodeToString([]byte("ls\n")),
	}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if bytes.Equal(bytes.Join(peer.inputs, nil), []byte("ls\n")) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("inputs = %#v, want ls\\n", peer.inputs)
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func dialAndRegisterAgent(t *testing.T, serverURL, sessionID string) *websocket.Conn {
	t.Helper()

	agentURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer agent-token")
	conn, _, err := websocket.DefaultDialer.Dial(agentURL, headers)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}

	info := protocol.SessionInfo{
		SessionID:      sessionID,
		Launcher:       "codex",
		CWD:            "/tmp/project",
		CommandPreview: "codex",
		StartedAt:      time.Unix(10, 0),
	}
	if err := conn.WriteJSON(protocol.RegisterFrame(info)); err != nil {
		t.Fatalf("WriteJSON register returned error: %v", err)
	}

	return conn
}
