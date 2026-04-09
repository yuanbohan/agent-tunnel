package relay

import (
	"bytes"
	"encoding/base64"
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

type sessionFramesResponse []struct {
	Seq     uint64    `json:"seq"`
	DataB64 string    `json:"data_b64"`
	Cols    int       `json:"cols"`
	Rows    int       `json:"rows"`
	TS      time.Time `json:"ts"`
}

type historyProxyErrorResponse struct {
	Reason string `json:"reason"`
}

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

func waitForLogEvent(t *testing.T, buf *syncBuffer, event string, want int) []map[string]any {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries := readLogEntries(t, buf)
		if countLogEvents(entries, event) >= want {
			return entries
		}
		time.Sleep(10 * time.Millisecond)
	}

	entries := readLogEntries(t, buf)
	t.Fatalf("event %q count = %d, want at least %d\nlogs:\n%s", event, countLogEvents(entries, event), want, buf.String())
	return nil
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

func TestWSAgentPeerSendSetsWriteDeadline(t *testing.T) {
	conn := &mockWSConn{}
	peer := &wsAgentPeer{
		conn:         conn,
		writeTimeout: 5 * time.Second,
	}

	if err := peer.Send(protocol.EncodeHistoryRequest("req-1", nil, nil)); err != nil {
		t.Fatalf("Send returned error: %v", err)
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

func TestWSAgentPeerSendReturnsDeadlineError(t *testing.T) {
	conn := &mockWSConn{setDeadlineErr: errors.New("deadline failed")}
	peer := &wsAgentPeer{
		conn:         conn,
		writeTimeout: 5 * time.Second,
	}

	if err := peer.Send(protocol.EncodeHistoryRequest("req-1", nil, nil)); err == nil || err.Error() != "deadline failed" {
		t.Fatalf("Send error = %v, want deadline failed", err)
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
		StartedAt:      time.Unix(10, 0),
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
	if !strings.Contains(rec.Body.String(), "latest_seq") {
		t.Fatalf("body = %s, want field %q", rec.Body.String(), "latest_seq")
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

func TestHandlerServesSessionFrames(t *testing.T) {
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

	frame2Time := time.Unix(41, 0).UTC()
	frame3Time := time.Unix(42, 0).UTC()
	requestCh := replyToNextHistoryRequest(agentConn, func(msg protocol.Message) protocol.Message {
		return protocol.EncodeHistoryResponse(msg.RequestID, []protocol.ReplayFrame{
			replayFrame(2, "two", 120, 40, frame2Time),
			replayFrame(3, "three", 132, 43, frame3Time),
		})
	})

	resp := doAuthenticatedGET(t, server.URL+"/api/sessions/sess-1/frames?from=2&to=3")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	request := <-requestCh
	if request.err != nil {
		t.Fatalf("history request error = %v", request.err)
	}
	if request.msg.Type != "history_request" || request.msg.RequestID == "" {
		t.Fatalf("request = %#v, want history_request with request_id", request.msg)
	}
	if request.msg.From == nil || *request.msg.From != 2 {
		t.Fatalf("request.From = %v, want 2", request.msg.From)
	}
	if request.msg.To == nil || *request.msg.To != 3 {
		t.Fatalf("request.To = %v, want 3", request.msg.To)
	}

	var frames sessionFramesResponse
	if err := json.NewDecoder(resp.Body).Decode(&frames); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("len(frames) = %d, want 2", len(frames))
	}
	if frames[0].Seq != 2 || frames[1].Seq != 3 {
		t.Fatalf("seqs = %#v, want 2 then 3", frames)
	}
	if !frames[0].TS.Equal(frame2Time) || !frames[1].TS.Equal(frame3Time) {
		t.Fatalf("timestamps = %#v, want preserved agent-authored times", frames)
	}
}

func TestHandlerServesAllSessionFramesWhenRangeOmitted(t *testing.T) {
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

	requestCh := replyToNextHistoryRequest(agentConn, func(msg protocol.Message) protocol.Message {
		return protocol.EncodeHistoryResponse(msg.RequestID, []protocol.ReplayFrame{
			replayFrame(1, "one", 120, 40, time.Unix(40, 0).UTC()),
			replayFrame(2, "two", 120, 40, time.Unix(41, 0).UTC()),
			replayFrame(3, "three", 132, 43, time.Unix(42, 0).UTC()),
		})
	})

	resp := doAuthenticatedGET(t, server.URL+"/api/sessions/sess-1/frames")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	request := <-requestCh
	if request.err != nil {
		t.Fatalf("history request error = %v", request.err)
	}
	if request.msg.From != nil || request.msg.To != nil {
		t.Fatalf("request bounds = %#v/%#v, want nil/nil", request.msg.From, request.msg.To)
	}

	var frames sessionFramesResponse
	if err := json.NewDecoder(resp.Body).Decode(&frames); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("len(frames) = %d, want 3", len(frames))
	}
}

func TestHandlerReturns404ForUnknownSessionFrames(t *testing.T) {
	reg := NewRegistry()
	handler := NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/missing/frames", nil)
	req.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	var body historyProxyErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if body.Reason != "session_not_found" {
		t.Fatalf("reason = %q, want session_not_found", body.Reason)
	}
}

func TestHandlerReturns409ForReconnectingSessionFrames(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/frames", nil)
	req.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}

	var body historyProxyErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if body.Reason != "session_reconnecting" {
		t.Fatalf("reason = %q, want session_reconnecting", body.Reason)
	}
}

func TestHandlerReturnsTimeoutWhenAgentNeverAnswersHistoryRequest(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:              reg,
		User:                  "demo",
		Password:              "secret",
		AgentToken:            "agent-token",
		HistoryRequestTimeout: 20 * time.Millisecond,
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()

	resp := doAuthenticatedGET(t, server.URL+"/api/sessions/sess-1/frames")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}

	var body historyProxyErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if body.Reason != "upstream_timeout" {
		t.Fatalf("reason = %q, want upstream_timeout", body.Reason)
	}
}

func TestHandlerReturnsBadGatewayForMalformedHistoryResponse(t *testing.T) {
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

	requestCh := replyToNextHistoryRequest(agentConn, func(msg protocol.Message) protocol.Message {
		return protocol.Message{
			Type:      "history_response",
			RequestID: msg.RequestID,
			Frames: []protocol.ReplayFrame{{
				Seq:     2,
				DataB64: base64.StdEncoding.EncodeToString([]byte("two")),
				Cols:    120,
				Rows:    40,
			}},
		}
	})

	resp := doAuthenticatedGET(t, server.URL+"/api/sessions/sess-1/frames?from=2&to=2")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if request := <-requestCh; request.err != nil {
		t.Fatalf("history request error = %v", request.err)
	}

	var body historyProxyErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if body.Reason != "invalid_agent_response" {
		t.Fatalf("reason = %q, want invalid_agent_response", body.Reason)
	}
}

func TestHandlerReturnsBadGatewayForInvalidHistoryResponseDataB64(t *testing.T) {
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

	requestCh := replyToNextHistoryRequest(agentConn, func(msg protocol.Message) protocol.Message {
		return protocol.Message{
			Type:      "history_response",
			RequestID: msg.RequestID,
			Frames: []protocol.ReplayFrame{{
				Seq:     2,
				DataB64: "***not-base64***",
				Cols:    120,
				Rows:    40,
				TS:      time.Unix(41, 0).UTC(),
			}},
		}
	})

	resp := doAuthenticatedGET(t, server.URL+"/api/sessions/sess-1/frames?from=2&to=2")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if request := <-requestCh; request.err != nil {
		t.Fatalf("history request error = %v", request.err)
	}

	var body historyProxyErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if body.Reason != "invalid_agent_response" {
		t.Fatalf("reason = %q, want invalid_agent_response", body.Reason)
	}
}

func TestHandlerReturnsBadGatewayForMismatchedHistoryResponse(t *testing.T) {
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

	requestCh := replyToNextHistoryRequest(agentConn, func(protocol.Message) protocol.Message {
		return protocol.EncodeHistoryResponse("wrong-request-id", []protocol.ReplayFrame{
			replayFrame(2, "two", 120, 40, time.Unix(41, 0).UTC()),
		})
	})

	resp := doAuthenticatedGET(t, server.URL+"/api/sessions/sess-1/frames?from=2&to=2")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if request := <-requestCh; request.err != nil {
		t.Fatalf("history request error = %v", request.err)
	}

	var body historyProxyErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if body.Reason != "invalid_agent_response" {
		t.Fatalf("reason = %q, want invalid_agent_response", body.Reason)
	}
}

func TestHandlerServesFramesAgainAfterReconnectWithinGrace(t *testing.T) {
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
	_ = agentConn.Close()

	resp := doAuthenticatedGET(t, server.URL+"/api/sessions/sess-1/frames")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while reconnecting", resp.StatusCode)
	}

	reconnected := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer reconnected.Close()
	requestCh := replyToNextHistoryRequest(reconnected, func(msg protocol.Message) protocol.Message {
		return protocol.EncodeHistoryResponse(msg.RequestID, []protocol.ReplayFrame{
			replayFrame(4, "four", 120, 40, time.Unix(44, 0).UTC()),
		})
	})

	resp2 := doAuthenticatedGET(t, server.URL+"/api/sessions/sess-1/frames")
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after reconnect", resp2.StatusCode)
	}
	if request := <-requestCh; request.err != nil {
		t.Fatalf("history request error = %v", request.err)
	}

	var frames sessionFramesResponse
	if err := json.NewDecoder(resp2.Body).Decode(&frames); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if len(frames) != 1 || frames[0].Seq != 4 {
		t.Fatalf("frames = %#v, want one seq 4 frame", frames)
	}
}

func TestHandlerRejectsInvalidFrameRange(t *testing.T) {
	reg := NewRegistry()
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, fakeAgentPeer{})
	handler := NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/frames?from=3&to=2", nil)
	req.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
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

	badRangeRec := httptest.NewRecorder()
	badRangeReq := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/frames?from=3&to=2", nil)
	badRangeReq.Header.Set("Authorization", basicAuth("demo", "secret"))
	handler.ServeHTTP(badRangeRec, badRangeReq)

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

	rangeEntry := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/sessions/sess-1/frames")
	if got := int(logNumber(t, rangeEntry, "status")); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got)
	}
	if got := logString(rangeEntry, "target"); got != "/api/sessions/sess-1/frames?from=3&to=2" {
		t.Fatalf("target = %q, want raw query preserved", got)
	}
	if got := int64(logNumber(t, rangeEntry, "response_bytes")); got != int64(badRangeRec.Body.Len()) {
		t.Fatalf("response_bytes = %d, want %d", got, badRangeRec.Body.Len())
	}

	authFailed := findLogEntryByEvent(t, entries, "auth_failed")
	if got := logString(authFailed, "path"); got != "/api/sessions" {
		t.Fatalf("auth_failed path = %q, want /api/sessions", got)
	}
	if got := logString(authFailed, "user_agent"); got != "scanbot/1.0" {
		t.Fatalf("auth_failed user_agent = %q, want scanbot/1.0", got)
	}
	if got := logString(authFailed, "request_id"); got != "req-unauth-1" {
		t.Fatalf("auth_failed request_id = %q, want req-unauth-1", got)
	}
}

func TestHandlerLogsWebSocketUpgradeFailureWithoutLifecycle(t *testing.T) {
	logs := &syncBuffer{}
	handler := NewHandler(HandlerConfig{
		Registry:   NewRegistry(),
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
		Logger:     NewLogger(logs),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/updates/ws", nil)
	req.Header.Set("Authorization", basicAuth("demo", "secret"))
	req.Header.Set("User-Agent", "probe/2.0")
	req.Header.Set("X-Request-Id", "req-upgrade-1")
	handler.ServeHTTP(rec, req)

	entries := readLogEntries(t, logs)
	upgradeFailed := findLogEntryByEvent(t, entries, "ws_upgrade_failed")
	if got := logString(upgradeFailed, "path"); got != "/api/updates/ws" {
		t.Fatalf("path = %q, want /api/updates/ws", got)
	}
	if got := logString(upgradeFailed, "user_agent"); got != "probe/2.0" {
		t.Fatalf("user_agent = %q, want probe/2.0", got)
	}
	if got := logString(upgradeFailed, "request_id"); got != "req-upgrade-1" {
		t.Fatalf("request_id = %q, want req-upgrade-1", got)
	}

	access := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/updates/ws")
	if got := int(logNumber(t, access, "status")); got != rec.Code {
		t.Fatalf("status = %d, want %d", got, rec.Code)
	}
	if got := logString(access, "user_agent"); got != "probe/2.0" {
		t.Fatalf("user_agent = %q, want probe/2.0", got)
	}
	if got := logString(access, "request_id"); got != "req-upgrade-1" {
		t.Fatalf("request_id = %q, want req-upgrade-1", got)
	}
	if countLogEvents(entries, "updates_ws_connected") != 0 || countLogEvents(entries, "updates_ws_disconnected") != 0 {
		t.Fatalf("unexpected lifecycle logs on failed upgrade: %#v", entries)
	}
}

func TestUpdatesWebSocketStreamsOutputAndRemoval(t *testing.T) {
	reg := NewRegistry()
	reg.reconnectGrace = 20 * time.Millisecond
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
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

	ts := time.Unix(40, 0).UTC()
	if err := agentConn.WriteJSON(protocol.EncodeOutputWithSeqAndSizeAndTime(1, []byte("hello"), 132, 43, ts)); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	var output protocol.ClientUpdateMessage
	if err := updatesConn.ReadJSON(&output); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	if output.Type != "output" || output.SessionID != "sess-1" {
		t.Fatalf("output = %#v, want sess-1 output", output)
	}
	if output.TS == nil || !output.TS.Equal(ts) {
		t.Fatalf("ts = %v, want %v", output.TS, ts)
	}
	if output.Cols != 132 || output.Rows != 43 {
		t.Fatalf("size = %dx%d, want 132x43", output.Cols, output.Rows)
	}
	data, err := base64.StdEncoding.DecodeString(output.DataB64)
	if err != nil {
		t.Fatalf("DecodeString returned error: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q, want hello", string(data))
	}

	_ = agentConn.Close()
	_ = updatesConn.SetReadDeadline(time.Now().Add(time.Second))

	var removed protocol.ClientUpdateMessage
	if err := updatesConn.ReadJSON(&removed); err != nil {
		t.Fatalf("ReadJSON removal returned error: %v", err)
	}
	if removed.Type != "session_removed" || removed.SessionID != "sess-1" {
		t.Fatalf("removed = %#v, want sess-1 session_removed", removed)
	}
}

func TestUpdatesWebSocketLogsTrafficAndAccess(t *testing.T) {
	reg := NewRegistry()
	logs := &syncBuffer{}
	peer := &recordingPeer{}
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, peer)

	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
		Logger:     NewLogger(logs),
	}))
	defer server.Close()

	updatesURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/updates/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	headers.Set("X-Request-Id", "req-updates-1")
	conn, _, err := websocket.DefaultDialer.Dial(updatesURL, headers)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}

	if err := conn.WriteJSON(protocol.EncodeClientInputText("sess-1", "ls\n", false)); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		messages := peer.Messages()
		if len(messages) == 1 && messages[0].Type == "input_text" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if messages := peer.Messages(); len(messages) != 1 || messages[0].Type != "input_text" {
		t.Fatalf("messages = %#v, want one input_text", messages)
	}

	if ok := reg.TouchOutputIfOwner("sess-1", peer, replayFrame(5, "hello", 132, 43, time.Now().UTC())); !ok {
		t.Fatal("TouchOutputIfOwner returned false, want true")
	}

	var output protocol.ClientUpdateMessage
	if err := conn.ReadJSON(&output); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	if output.Type != "output" {
		t.Fatalf("output.Type = %q, want output", output.Type)
	}

	_ = conn.Close()

	entries := waitForLogEvent(t, logs, "updates_ws_disconnected", 1)
	access := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/updates/ws")
	if got := int(logNumber(t, access, "status")); got != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", got)
	}
	if got := logString(access, "request_id"); got != "req-updates-1" {
		t.Fatalf("request_id = %q, want req-updates-1", got)
	}

	connected := findLogEntryByEvent(t, entries, "updates_ws_connected")
	if got := logString(connected, "path"); got != "/api/updates/ws" {
		t.Fatalf("path = %q, want /api/updates/ws", got)
	}
	if got := logString(connected, "request_id"); got != "req-updates-1" {
		t.Fatalf("request_id = %q, want req-updates-1", got)
	}

	disconnected := findLogEntryByEvent(t, entries, "updates_ws_disconnected")
	if got := logString(disconnected, "request_id"); got != "req-updates-1" {
		t.Fatalf("request_id = %q, want req-updates-1", got)
	}
	if got := int64(logNumber(t, disconnected, "inbound_messages")); got < 1 {
		t.Fatalf("inbound_messages = %d, want >= 1", got)
	}
	if got := int64(logNumber(t, disconnected, "outbound_messages")); got < 1 {
		t.Fatalf("outbound_messages = %d, want >= 1", got)
	}
	if got := int64(logNumber(t, disconnected, "inbound_bytes")); got <= 0 {
		t.Fatalf("inbound_bytes = %d, want > 0", got)
	}
	if got := int64(logNumber(t, disconnected, "outbound_bytes")); got <= 0 {
		t.Fatalf("outbound_bytes = %d, want > 0", got)
	}
}

func TestAgentWebSocketIgnoresOutputWithoutDataB64(t *testing.T) {
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

	if err := agentConn.WriteJSON(protocol.Message{
		Type: "output",
		Cols: 132,
		Rows: 43,
	}); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	info, ok := reg.Session("sess-1")
	if !ok {
		t.Fatal("Session returned false, want true")
	}
	if info.LatestSeq != 0 {
		t.Fatalf("LatestSeq = %d, want 0 after invalid output", info.LatestSeq)
	}
}

func TestUpdatesWebSocketAcceptsOriginHeader(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
	}))
	defer server.Close()

	updatesURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/updates/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	headers.Set("Origin", "https://client.example")
	conn, _, err := websocket.DefaultDialer.Dial(updatesURL, headers)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()
}

func TestUpdatesWebSocketForwardsClientInputTextToAgent(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
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

	msg := protocol.EncodeClientInputText("sess-1", "ls\n", false)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		messages := peer.Messages()
		if len(messages) == 1 && messages[0].Type == "input_text" && messages[0].Text == "ls\n" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("messages = %#v, want input_text ls\\n", peer.Messages())
}

func TestUpdatesWebSocketForwardsSubmittingClientInputTextToAgent(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
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

	msg := protocol.EncodeClientInputText("sess-1", "line1\nline2", true)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		messages := peer.Messages()
		if len(messages) == 1 && messages[0].Type == "input_text" && messages[0].Text == "line1\nline2" && messages[0].Submit {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("messages = %#v, want input_text submit line1\\nline2", peer.Messages())
}

func TestUpdatesWebSocketForwardsEmptySubmittingClientInputTextToAgent(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
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

	msg := protocol.EncodeClientInputText("sess-1", "", true)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		messages := peer.Messages()
		if len(messages) == 1 && messages[0].Type == "input_text" && messages[0].Text == "" && messages[0].Submit {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("messages = %#v, want input_text submit empty text", peer.Messages())
}

func TestUpdatesWebSocketForwardsClientInputKeyToAgent(t *testing.T) {
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   reg,
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
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

	msg := protocol.EncodeClientInputKey("sess-1", "TAB", false, false, false)
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		messages := peer.Messages()
		if len(messages) == 1 && messages[0].Type == "input_key" && messages[0].Key == "TAB" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("messages = %#v, want input_key TAB", peer.Messages())
}

func TestAgentWebSocketLogsTrafficAndAccess(t *testing.T) {
	logs := &syncBuffer{}
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   NewRegistry(),
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
		Logger:     NewLogger(logs),
	}))
	defer server.Close()

	agentHeaders := http.Header{}
	agentHeaders.Set("X-Request-Id", "req-agent-1")
	agentConn := dialAndRegisterAgentWithHeaders(t, server.URL, "sess-1", agentHeaders)
	defer agentConn.Close()

	updatesURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/updates/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	updatesConn, _, err := websocket.DefaultDialer.Dial(updatesURL, headers)
	if err != nil {
		t.Fatalf("Dial updates returned error: %v", err)
	}

	msg := protocol.EncodeClientInputText("sess-1", "pwd\n", false)
	if err := updatesConn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	var forwarded protocol.Message
	if err := agentConn.ReadJSON(&forwarded); err != nil {
		t.Fatalf("ReadJSON forwarded returned error: %v", err)
	}
	if forwarded.Type != "input_text" || forwarded.Text != "pwd\n" {
		t.Fatalf("forwarded = %#v, want input_text pwd\\n", forwarded)
	}

	if err := agentConn.WriteJSON(protocol.EncodeOutputWithSeqAndSizeAndTime(1, []byte("hello"), 120, 40, time.Unix(40, 0).UTC())); err != nil {
		t.Fatalf("WriteJSON output returned error: %v", err)
	}

	_ = updatesConn.Close()
	_ = agentConn.Close()

	entries := waitForLogEvent(t, logs, "agent_disconnected", 1)
	access := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/agent/ws")
	if got := int(logNumber(t, access, "status")); got != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", got)
	}
	if got := logString(access, "request_id"); got != "req-agent-1" {
		t.Fatalf("request_id = %q, want req-agent-1", got)
	}

	registered := findLogEntryByEvent(t, entries, "agent_registered")
	if got := logString(registered, "session_id"); got != "sess-1" {
		t.Fatalf("session_id = %q, want sess-1", got)
	}
	if got := logString(registered, "request_id"); got != "req-agent-1" {
		t.Fatalf("request_id = %q, want req-agent-1", got)
	}

	connected := findLogEntryByEvent(t, entries, "agent_ws_connected")
	if got := logString(connected, "request_id"); got != "req-agent-1" {
		t.Fatalf("request_id = %q, want req-agent-1", got)
	}

	disconnected := findLogEntryByEvent(t, entries, "agent_disconnected")
	if got := logString(disconnected, "request_id"); got != "req-agent-1" {
		t.Fatalf("request_id = %q, want req-agent-1", got)
	}
	if got := logString(disconnected, "session_id"); got != "sess-1" {
		t.Fatalf("session_id = %q, want sess-1", got)
	}
	if got := int64(logNumber(t, disconnected, "inbound_messages")); got < 2 {
		t.Fatalf("inbound_messages = %d, want >= 2", got)
	}
	if got := int64(logNumber(t, disconnected, "outbound_messages")); got < 1 {
		t.Fatalf("outbound_messages = %d, want >= 1", got)
	}
	if got := int64(logNumber(t, disconnected, "inbound_bytes")); got <= 0 {
		t.Fatalf("inbound_bytes = %d, want > 0", got)
	}
	if got := int64(logNumber(t, disconnected, "outbound_bytes")); got <= 0 {
		t.Fatalf("outbound_bytes = %d, want > 0", got)
	}
}

func TestAgentWebSocketLogsDisconnectBeforeRegisterWithoutSessionID(t *testing.T) {
	logs := &syncBuffer{}
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:   NewRegistry(),
		User:       "demo",
		Password:   "secret",
		AgentToken: "agent-token",
		Logger:     NewLogger(logs),
	}))
	defer server.Close()

	agentURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer agent-token")
	headers.Set("X-Request-Id", "req-agent-pre-register")
	conn, _, err := websocket.DefaultDialer.Dial(agentURL, headers)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}

	_ = conn.Close()

	entries := waitForLogEvent(t, logs, "agent_disconnected", 1)
	if countLogEvents(entries, "agent_registered") != 0 {
		t.Fatalf("unexpected agent_registered logs: %#v", entries)
	}

	disconnected := findLogEntryByEvent(t, entries, "agent_disconnected")
	if got := logString(disconnected, "request_id"); got != "req-agent-pre-register" {
		t.Fatalf("request_id = %q, want req-agent-pre-register", got)
	}
	if _, ok := disconnected["session_id"]; ok {
		t.Fatalf("session_id present = %#v, want absent", disconnected["session_id"])
	}
}

type historyRequestResult struct {
	msg protocol.Message
	err error
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

func replyToNextHistoryRequest(conn *websocket.Conn, responder func(protocol.Message) protocol.Message) <-chan historyRequestResult {
	ch := make(chan historyRequestResult, 1)
	go func() {
		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			ch <- historyRequestResult{err: err}
			return
		}
		if responder != nil {
			if err := conn.WriteJSON(responder(msg)); err != nil {
				ch <- historyRequestResult{msg: msg, err: err}
				return
			}
		}
		ch <- historyRequestResult{msg: msg}
	}()
	return ch
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func dialAndRegisterAgent(t *testing.T, serverURL, sessionID string) *websocket.Conn {
	t.Helper()

	return dialAndRegisterAgentWithHeaders(t, serverURL, sessionID, nil)
}

func dialAndRegisterAgentWithHeaders(t *testing.T, serverURL, sessionID string, extraHeaders http.Header) *websocket.Conn {
	t.Helper()

	agentURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/agent/ws"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer agent-token")
	for key, values := range extraHeaders {
		for _, value := range values {
			headers.Add(key, value)
		}
	}
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
