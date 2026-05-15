package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

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

func TestHandlerAccessLogsRequestsAndSkipsHealthz(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	logs := &syncBuffer{}

	handler := env.handler(logs)

	healthRec := httptest.NewRecorder()
	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(healthRec, healthReq)

	unauthRec := httptest.NewRecorder()
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/account/policy", nil)
	unauthReq.Header.Set("User-Agent", "scanbot/1.0")
	unauthReq.Header.Set("X-Request-Id", "req-unauth-1")
	handler.ServeHTTP(unauthRec, unauthReq)

	removedRec := httptest.NewRecorder()
	removedReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	removedReq.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(removedRec, removedReq)

	entries := readLogEntries(t, logs)
	if countLogEvents(entries, "http_request_completed") != 2 {
		t.Fatalf("http_request_completed count = %d, want 2", countLogEvents(entries, "http_request_completed"))
	}

	for _, entry := range entries {
		if logString(entry, "event") == "http_request_completed" && logString(entry, "path") == "/healthz" {
			t.Fatalf("unexpected healthz access log: %#v", entry)
		}
	}

	unauthEntry := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/account/policy")
	if got := int(logNumber(t, unauthEntry, "status")); got != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", got)
	}
	if got := logString(unauthEntry, "target"); got != "/api/account/policy" {
		t.Fatalf("target = %q, want /api/account/policy", got)
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

	removedEntry := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/sessions")
	if got := int(logNumber(t, removedEntry, "status")); got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", got)
	}

	authFailed := findLogEntryByEvent(t, entries, "auth_failed")
	if got := logString(authFailed, "path"); got != "/api/account/policy" {
		t.Fatalf("auth_failed path = %q, want /api/account/policy", got)
	}
}

func TestHandlerAccessLogRedactsQueryTokens(t *testing.T) {
	env := newHandlerTestEnv(t)
	logs := &syncBuffer{}
	handler := env.handler(logs)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/connectivity/tunnel/ws?token=secret-token&attempt_id=a", nil)
	handler.ServeHTTP(rec, req)

	entry := findLogEntryByEventAndPath(t, readLogEntries(t, logs), "http_request_completed", "/connectivity/tunnel/ws")
	target := logString(entry, "target")
	if strings.Contains(target, "secret-token") {
		t.Fatalf("target leaked token: %q", target)
	}
	if !strings.Contains(target, "token=%3Credacted%3E") {
		t.Fatalf("target = %q, want redacted token", target)
	}
}

func TestHandlerLogsRemovedAttachRouteWithoutWebSocketLifecycle(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.addInvite(t, "AB2C3D")
	env.registerUser(t, "alice", "password123", "AB2C3D")
	issued := env.login(t, "alice", "password123")
	logs := &syncBuffer{}
	handler := env.handler(logs)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/attach/ws", nil)
	req.Header.Set("Authorization", bearerAuth(issued.AccessToken))
	handler.ServeHTTP(rec, req)

	entries := readLogEntries(t, logs)
	if got := countLogEvents(entries, "ws_upgrade_failed"); got != 0 {
		t.Fatalf("ws_upgrade_failed count = %d, want 0", got)
	}
	access := findLogEntryByEventAndPath(t, entries, "http_request_completed", "/api/sessions/sess-1/attach/ws")
	if got := int(logNumber(t, access, "status")); got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", got)
	}
}
