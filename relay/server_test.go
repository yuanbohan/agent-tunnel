package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
)

type mockWSConn struct {
	mu             sync.Mutex
	deadline       time.Time
	messages       [][]byte
	setDeadlineErr error
	writeErr       error
	closeErr       error
}

func (m *mockWSConn) WriteMessage(_ int, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeErr != nil {
		return m.writeErr
	}
	m.messages = append(m.messages, append([]byte(nil), data...))
	return nil
}

func (m *mockWSConn) SetWriteDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setDeadlineErr != nil {
		return m.setDeadlineErr
	}
	m.deadline = t
	return nil
}

func (m *mockWSConn) Close() error { return m.closeErr }

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func readLogEntries(t *testing.T, buf *syncBuffer) []map[string]any {
	t.Helper()

	raw := strings.TrimSpace(buf.String())
	if raw == "" {
		return nil
	}

	lines := strings.Split(raw, "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("Unmarshal log line returned error: %v\nline: %s", err, line)
		}
		entries = append(entries, entry)
	}
	return entries
}

func countLogEvents(entries []map[string]any, event string) int {
	count := 0
	for _, entry := range entries {
		if logString(entry, "event") == event {
			count++
		}
	}
	return count
}

func findLogEntryByEventAndPath(t *testing.T, entries []map[string]any, event, path string) map[string]any {
	t.Helper()

	for _, entry := range entries {
		if logString(entry, "event") == event && logString(entry, "path") == path {
			return entry
		}
	}
	t.Fatalf("missing log entry event=%q path=%q in %#v", event, path, entries)
	return nil
}

func findLogEntryByEvent(t *testing.T, entries []map[string]any, event string) map[string]any {
	t.Helper()

	for _, entry := range entries {
		if logString(entry, "event") == event {
			return entry
		}
	}
	t.Fatalf("missing log entry event=%q in %#v", event, entries)
	return nil
}

func logString(entry map[string]any, key string) string {
	value, _ := entry[key].(string)
	return value
}

func logNumber(t *testing.T, entry map[string]any, key string) float64 {
	t.Helper()

	value, ok := entry[key].(float64)
	if !ok {
		t.Fatalf("entry[%q] = %#v, want number in %#v", key, entry[key], entry)
	}
	return value
}

func TestWSAgentPeerSendJSONSetsWriteDeadline(t *testing.T) {
	conn := &mockWSConn{}
	peer := &wsAgentPeer{
		conn:         conn,
		writeTimeout: 5 * time.Second,
	}

	if err := peer.SendJSON(protocol.ActivityFrame(40)); err != nil {
		t.Fatalf("SendJSON returned error: %v", err)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.deadline.IsZero() {
		t.Fatal("SetWriteDeadline was not called")
	}
	if len(conn.messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(conn.messages))
	}
}

func TestWSAgentPeerSendJSONReturnsDeadlineError(t *testing.T) {
	conn := &mockWSConn{setDeadlineErr: errors.New("deadline failed")}
	peer := &wsAgentPeer{
		conn:         conn,
		writeTimeout: 5 * time.Second,
	}

	if err := peer.SendJSON(protocol.ActivityFrame(40)); err == nil || err.Error() != "deadline failed" {
		t.Fatalf("SendJSON error = %v, want deadline failed", err)
	}
}

func TestHandlerRejectsSessionsWithoutBasicAuth(t *testing.T) {
	reg := NewRegistry()
	handler := NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
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
		StartedAt:      10,
	}, fakeAgentPeer{})

	handler := NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
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
	if sessions[0].State != protocol.SessionStateConnected {
		t.Fatalf("State = %q, want connected", sessions[0].State)
	}
	if strings.Contains(rec.Body.String(), "latest_seq") {
		t.Fatalf("body = %s, did not expect field %q", rec.Body.String(), "latest_seq")
	}
}

func TestHandlerReturnsReconnectingSessionsWithBasicAuth(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)
	reg.DisconnectIfOwner("sess-1", peer)

	handler := NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
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
	if len(sessions) != 1 || sessions[0].State != protocol.SessionStateReconnecting {
		t.Fatalf("sessions = %#v, want one reconnecting session", sessions)
	}
}

func TestHandlerReturns404ForUnknownAttachSession(t *testing.T) {
	reg := NewRegistry()
	handler := NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/missing/attach/ws", nil)
	req.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if body["reason"] != "session_not_found" {
		t.Fatalf("reason = %q, want session_not_found", body["reason"])
	}
}

func TestHandlerReturns409ForReconnectingAttachSession(t *testing.T) {
	reg := NewRegistry()
	peer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)
	reg.DisconnectIfOwner("sess-1", peer)

	handler := NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/attach/ws", nil)
	req.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if body["reason"] != "session_reconnecting" {
		t.Fatalf("reason = %q, want session_reconnecting", body["reason"])
	}
}

func TestAttachWebSocketRejectsCrossOriginBrowserDial(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/attach/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	headers.Set("Origin", "https://evil.example")

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Dial returned nil error, want origin rejection")
	}
	if resp == nil {
		t.Fatal("resp = nil, want HTTP response")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAttachWebSocketRejectsCrossSchemeSameHostOrigin(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/attach/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	headers.Set("Origin", "https://"+strings.TrimPrefix(server.URL, "http://"))

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Dial returned nil error, want scheme mismatch rejection")
	}
	if resp == nil {
		t.Fatal("resp = nil, want HTTP response")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAttachWebSocketAcceptsForwardedSameOrigin(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/attach/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	headers.Set("Origin", "https://relay.example")
	headers.Set("X-Forwarded-Host", "relay.example")
	headers.Set("X-Forwarded-Proto", "https")

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		if resp != nil {
			t.Fatalf("Dial returned error: %v (status=%d)", err, resp.StatusCode)
		}
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	open := readAgentFrame(t, agentConn)
	if open.Type != "attach_open" || open.ClientID == "" {
		t.Fatalf("attach_open = %#v, want client id", open)
	}
}

func TestAttachWebSocketRejectsMalformedOrigin(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/attach/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	headers.Set("Origin", "://bad origin")

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Dial returned nil error, want malformed origin rejection")
	}
	if resp == nil {
		t.Fatal("resp = nil, want HTTP response")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAttachWebSocketForwardsSnapshotLiveBytesAndInput(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()

	attachConn := dialAttachClient(t, server.URL, "sess-1")
	defer attachConn.Close()

	open := readAgentFrame(t, agentConn)
	if open.Type != "attach_open" || open.ClientID == "" {
		t.Fatalf("attach_open = %#v, want client id", open)
	}

	if err := agentConn.WriteJSON(protocol.AttachReadyFrame(open.ClientID, 120, 40)); err != nil {
		t.Fatalf("WriteJSON attach_ready returned error: %v", err)
	}
	packet, err := protocol.EncodeTerminalBytesPacket(open.ClientID, []byte("snapshot bytes"))
	if err != nil {
		t.Fatalf("EncodeTerminalBytesPacket returned error: %v", err)
	}
	if err := agentConn.WriteMessage(websocket.BinaryMessage, packet); err != nil {
		t.Fatalf("WriteMessage snapshot returned error: %v", err)
	}
	if err := agentConn.WriteJSON(protocol.SnapshotDoneFrame(open.ClientID)); err != nil {
		t.Fatalf("WriteJSON snapshot_done returned error: %v", err)
	}

	attached := readAttachControl(t, attachConn)
	if attached.Type != "attached" || attached.SessionID != "sess-1" || attached.Cols != 120 || attached.Rows != 40 {
		t.Fatalf("attached = %#v, want sess-1 120x40", attached)
	}
	if snapshot := string(readAttachBinary(t, attachConn)); snapshot != "snapshot bytes" {
		t.Fatalf("snapshot = %q, want snapshot bytes", snapshot)
	}
	if done := readAttachControl(t, attachConn); done.Type != "snapshot_done" {
		t.Fatalf("snapshot_done = %#v, want snapshot_done", done)
	}

	if err := attachConn.WriteJSON(protocol.EncodeClientInputText("hello", true)); err != nil {
		t.Fatalf("WriteJSON input_text returned error: %v", err)
	}
	inputText := readAgentFrame(t, agentConn)
	if inputText.Type != "input_text" || inputText.ClientID != open.ClientID || inputText.Text != "hello" || !inputText.Submit {
		t.Fatalf("input_text = %#v, want routed submit hello", inputText)
	}

	if err := attachConn.WriteJSON(protocol.EncodeClientInputKey("TAB")); err != nil {
		t.Fatalf("WriteJSON input_key returned error: %v", err)
	}
	inputKey := readAgentFrame(t, agentConn)
	if inputKey.Type != "input_key" || inputKey.ClientID != open.ClientID || inputKey.Key != "TAB" {
		t.Fatalf("input_key = %#v, want routed TAB", inputKey)
	}

	if err := agentConn.WriteJSON(protocol.ResizeFrame(100, 30)); err != nil {
		t.Fatalf("WriteJSON resize returned error: %v", err)
	}
	if resize := readAttachControl(t, attachConn); resize.Type != "resize" || resize.Cols != 100 || resize.Rows != 30 {
		t.Fatalf("resize = %#v, want 100x30", resize)
	}

	livePacket, err := protocol.EncodeTerminalBytesPacket(open.ClientID, []byte("live bytes"))
	if err != nil {
		t.Fatalf("EncodeTerminalBytesPacket live returned error: %v", err)
	}
	if err := agentConn.WriteMessage(websocket.BinaryMessage, livePacket); err != nil {
		t.Fatalf("WriteMessage live returned error: %v", err)
	}
	if live := string(readAttachBinary(t, attachConn)); live != "live bytes" {
		t.Fatalf("live = %q, want live bytes", live)
	}
}

func TestAttachWebSocketClosesWhenAgentDisconnects(t *testing.T) {
	reg := NewRegistry()
	reg.reconnectGrace = 200 * time.Millisecond
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	attachConn := dialAttachClient(t, server.URL, "sess-1")
	defer attachConn.Close()

	open := readAgentFrame(t, agentConn)
	if err := agentConn.WriteJSON(protocol.AttachReadyFrame(open.ClientID, 120, 40)); err != nil {
		t.Fatalf("WriteJSON attach_ready returned error: %v", err)
	}
	if err := agentConn.WriteJSON(protocol.SnapshotDoneFrame(open.ClientID)); err != nil {
		t.Fatalf("WriteJSON snapshot_done returned error: %v", err)
	}

	if msg := readAttachControl(t, attachConn); msg.Type != "attached" {
		t.Fatalf("attached = %#v, want attached", msg)
	}
	if msg := readAttachControl(t, attachConn); msg.Type != "snapshot_done" {
		t.Fatalf("snapshot_done = %#v, want snapshot_done", msg)
	}

	_ = agentConn.Close()

	if closing := readAttachControl(t, attachConn); closing.Type != "closing" || closing.Reason != "session_reconnecting" {
		t.Fatalf("closing = %#v, want session_reconnecting", closing)
	}
}

func TestHandlerAccessLogsRequestsAndSkipsHealthz(t *testing.T) {
	reg := NewRegistry()
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})
	logs := &syncBuffer{}

	handler := NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
		Logger:     NewLogger(logs),
	})

	healthRec := httptest.NewRecorder()
	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(healthRec, healthReq)

	unauthRec := httptest.NewRecorder()
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	unauthReq.Header.Set("User-Agent", "scanbot/1.0")
	unauthReq.Header.Set("X-Request-Id", "req-unauth-1")
	handler.ServeHTTP(unauthRec, unauthReq)

	badMethodRec := httptest.NewRecorder()
	badMethodReq := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/attach/ws", nil)
	badMethodReq.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(badMethodRec, badMethodReq)

	entries := readLogEntries(t, logs)
	if countLogEvents(entries, "http_request_completed") != 2 {
		t.Fatalf("http_request_completed count = %d, want 2", countLogEvents(entries, "http_request_completed"))
	}

	for _, entry := range entries {
		if logString(entry, "event") == "http_request_completed" && logString(entry, "path") == "/healthz" {
			t.Fatalf("unexpected healthz access log: %#v", entry)
		}
	}

	unauthEntry := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/sessions")
	if got := int(logNumber(t, unauthEntry, "status")); got != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", got)
	}
	if got := logString(unauthEntry, "target"); got != "/api/sessions" {
		t.Fatalf("target = %q, want /api/sessions", got)
	}
	if got := logString(unauthEntry, "user_agent"); got != "scanbot/1.0" {
		t.Fatalf("user_agent = %q, want scanbot/1.0", got)
	}
	if got := logString(unauthEntry, "request_id"); got != "req-unauth-1" {
		t.Fatalf("request_id = %q, want req-unauth-1", got)
	}
	if got := int64(logNumber(t, unauthEntry, "response_bytes")); got != int64(unauthRec.Body.Len()) {
		t.Fatalf("response_bytes = %d, want %d", got, unauthRec.Body.Len())
	}

	badMethodEntry := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/sessions/sess-1/attach/ws")
	if got := int(logNumber(t, badMethodEntry, "status")); got != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", got)
	}
	if got := logString(badMethodEntry, "target"); got != "/api/sessions/sess-1/attach/ws" {
		t.Fatalf("target = %q, want attach target preserved", got)
	}
	if got := int64(logNumber(t, badMethodEntry, "response_bytes")); got != int64(badMethodRec.Body.Len()) {
		t.Fatalf("response_bytes = %d, want %d", got, badMethodRec.Body.Len())
	}

	authFailed := findLogEntryByEvent(t, entries, "auth_failed")
	if got := logString(authFailed, "path"); got != "/api/sessions" {
		t.Fatalf("auth_failed path = %q, want /api/sessions", got)
	}
}

func TestHandlerLogsWebSocketUpgradeFailureWithoutLifecycle(t *testing.T) {
	logs := &syncBuffer{}
	reg := NewRegistry()
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})
	handler := NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
		Logger:     NewLogger(logs),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/attach/ws", nil)
	req.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(rec, req)

	entries := readLogEntries(t, logs)
	upgradeFailed := findLogEntryByEvent(t, entries, "ws_upgrade_failed")
	if got := logString(upgradeFailed, "path"); got != "/api/sessions/sess-1/attach/ws" {
		t.Fatalf("path = %q, want attach path", got)
	}
	access := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/sessions/sess-1/attach/ws")
	if got := int(logNumber(t, access, "status")); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got)
	}
}

func doAuthenticatedGET(t *testing.T, target string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	req.Header.Set("Authorization", basicAuth("demo", "secret"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	return resp
}

func readAgentFrame(t *testing.T, conn *websocket.Conn) protocol.AgentFrame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var frame protocol.AgentFrame
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	return frame
}

func readAttachControl(t *testing.T, conn *websocket.Conn) protocol.AttachControlMessage {
	t.Helper()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage returned error: %v", err)
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var msg protocol.AttachControlMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			t.Fatalf("Unmarshal returned error: %v", err)
		}
		return msg
	}
}

func readAttachBinary(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage returned error: %v", err)
		}
		if messageType == websocket.BinaryMessage {
			return payload
		}
	}
}

func basicAuth(user, pass string) string {
	return "Basic " + basicAuthValue(user, pass)
}

func basicAuthValue(user, pass string) string {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth(user, pass)
	return strings.TrimPrefix(req.Header.Get("Authorization"), "Basic ")
}

func dialAndRegisterAgent(t *testing.T, serverURL, sessionID string) *websocket.Conn {
	t.Helper()
	return dialAndRegisterAgentWithHeaders(t, serverURL, sessionID, nil)
}

func dialAndRegisterAgentWithHeaders(t *testing.T, serverURL, sessionID string, extraHeaders http.Header) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer agent-token")
	for k, values := range extraHeaders {
		for _, value := range values {
			headers.Add(k, value)
		}
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}

	if err := conn.WriteJSON(protocol.RegisterFrame(protocol.SessionInfo{
		SessionID:      sessionID,
		Launcher:       "codex",
		CWD:            "/tmp/project",
		CommandPreview: "codex",
		StartedAt:      10,
	})); err != nil {
		t.Fatalf("WriteJSON register returned error: %v", err)
	}

	return conn
}

func dialAttachClient(t *testing.T, serverURL, sessionID string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/sessions/" + sessionID + "/attach/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial attach returned error: %v", err)
	}
	return conn
}
