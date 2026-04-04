# Relay Lifecycle Logging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add low-noise structured lifecycle logging to the relay so operators can see startup, authentication failures, WebSocket connection lifecycle, session replacement, and sink backpressure without logging frame payloads.

**Architecture:** Introduce a small relay-scoped structured logger wrapper, inject it through the relay startup and handler paths, and emit logs only at lifecycle boundaries. Keep the registry and WebSocket sink responsible only for events they truly own: session replacement and backpressure. Avoid frame-level logging entirely.

**Tech Stack:** Go, `log/slog`, `net/http`, `github.com/gorilla/websocket`, existing `relay` and `cmd/relay` package tests

---

## File Structure

- Create: `relay/logger.go`
  Responsibility: Relay-scoped structured logger wrapper with a small helper field API and JSON output.
- Create: `relay/logger_test.go`
  Responsibility: Unit tests for JSON log shape and field handling.
- Create: `relay/ws_sink_test.go`
  Responsibility: Deterministic unit test for WebSocket sink backpressure callback behavior.
- Modify: `cmd/relay/main.go`
  Responsibility: Construct the logger, emit `relay_started`, and pass the logger into the handler.
- Modify: `cmd/relay/main_test.go`
  Responsibility: Verify startup logging helper output.
- Modify: `relay/server.go`
  Responsibility: Accept injected logger, emit auth and WebSocket lifecycle logs, classify disconnect reasons, and wire sink backpressure logging.
- Modify: `relay/server_test.go`
  Responsibility: Assert auth failure, upgrade failure, agent lifecycle, and client lifecycle logs appear with the expected fields.
- Modify: `relay/registry.go`
  Responsibility: Store relay logger reference and emit `session_replaced` when a session owner is swapped.
- Modify: `relay/registry_test.go`
  Responsibility: Verify session replacement logging.

No README or protocol doc changes are needed because this work adds operator-facing observability without changing external behavior.

### Task 1: Add the Structured Relay Logger

**Files:**
- Create: `relay/logger.go`
- Test: `relay/logger_test.go`

- [ ] **Step 1: Write the failing logger tests**

Add `relay/logger_test.go` with these tests:

```go
package relay

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerInfoWritesJSONEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf)

	logger.Info(
		"agent_registered",
		String("session_id", "sess-1"),
		String("launcher", "codex"),
	)

	got := buf.String()
	for _, want := range []string{
		`"level":"INFO"`,
		`"event":"agent_registered"`,
		`"session_id":"sess-1"`,
		`"launcher":"codex"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("log = %s, want substring %q", got, want)
		}
	}
	if strings.Contains(got, `"msg":""`) {
		t.Fatalf("log = %s, want empty msg field removed", got)
	}
}

func TestLoggerWarnOmitsFieldsNotPassed(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf)

	logger.Warn("auth_failed", String("path", "/agent/ws"))

	got := buf.String()
	if !strings.Contains(got, `"event":"auth_failed"`) {
		t.Fatalf("log = %s, want auth_failed event", got)
	}
	if strings.Contains(got, `"session_id"`) {
		t.Fatalf("log = %s, did not expect session_id", got)
	}
}
```

- [ ] **Step 2: Run the logger tests to verify they fail**

Run:

```bash
go test ./relay -run 'TestLogger(InfoWritesJSONEvent|WarnOmitsFieldsNotPassed)' -v
```

Expected: FAIL because `NewLogger`, `String`, and `Warn` do not exist yet.

- [ ] **Step 3: Write the minimal structured logger implementation**

Create `relay/logger.go` with this implementation:

```go
package relay

import (
	"context"
	"io"
	"log/slog"
)

type Field = slog.Attr

func String(key, value string) Field { return slog.String(key, value) }
func Int64(key string, value int64) Field { return slog.Int64(key, value) }

type Logger struct {
	base *slog.Logger
}

func NewLogger(w io.Writer) *Logger {
	if w == nil {
		w = io.Discard
	}
	return &Logger{
		base: slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
			ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
				if attr.Key == slog.MessageKey && attr.Value.String() == "" {
					return slog.Attr{}
				}
				return attr
			},
		})),
	}
}

func NewDiscardLogger() *Logger {
	return NewLogger(io.Discard)
}

func (l *Logger) Info(event string, fields ...Field) {
	l.log(slog.LevelInfo, event, fields...)
}

func (l *Logger) Warn(event string, fields ...Field) {
	l.log(slog.LevelWarn, event, fields...)
}

func (l *Logger) Error(event string, fields ...Field) {
	l.log(slog.LevelError, event, fields...)
}

func (l *Logger) log(level slog.Level, event string, fields ...Field) {
	if l == nil || l.base == nil {
		return
	}

	attrs := make([]slog.Attr, 0, len(fields)+1)
	attrs = append(attrs, slog.String("event", event))
	attrs = append(attrs, fields...)

	l.base.LogAttrs(context.Background(), level, "", attrs...)
}
```

- [ ] **Step 4: Run the logger tests to verify they pass**

Run:

```bash
go test ./relay -run 'TestLogger(InfoWritesJSONEvent|WarnOmitsFieldsNotPassed)' -v
```

Expected: PASS.

- [ ] **Step 5: Commit the logger wrapper**

Run:

```bash
git add relay/logger.go relay/logger_test.go
git commit -m "feat: add structured relay logger"
```

### Task 2: Log Relay Startup in `cmd/relay`

**Files:**
- Modify: `cmd/relay/main.go`
- Test: `cmd/relay/main_test.go`

- [ ] **Step 1: Write the failing startup log test**

Append this test to `cmd/relay/main_test.go`:

```go
func TestLogRelayStartedWritesListenAddr(t *testing.T) {
	var buf bytes.Buffer
	logger := relay.NewLogger(&buf)

	logRelayStarted(logger, mainConfig{ListenAddr: "0.0.0.0:8586"})

	got := buf.String()
	if !strings.Contains(got, `"event":"relay_started"`) {
		t.Fatalf("log = %s, want relay_started event", got)
	}
	if !strings.Contains(got, `"listen_addr":"0.0.0.0:8586"`) {
		t.Fatalf("log = %s, want listen_addr", got)
	}
}
```

Update the imports at the top of `cmd/relay/main_test.go` to include:

```go
import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"yuanbohan/tunnel/relay"
)
```

- [ ] **Step 2: Run the startup log test to verify it fails**

Run:

```bash
go test ./cmd/relay -run TestLogRelayStartedWritesListenAddr -v
```

Expected: FAIL because `logRelayStarted` does not exist yet.

- [ ] **Step 3: Add startup logging and inject the logger into the handler**

Update `cmd/relay/main.go` like this:

```go
func logRelayStarted(logger *relay.Logger, cfg mainConfig) {
	logger.Info("relay_started", relay.String("listen_addr", cfg.ListenAddr))
}

func main() {
	port := flag.String("port", "", "listen port")
	flag.Parse()

	cfg, err := loadMainConfig(os.Getenv, *port)
	if err != nil {
		log.Fatal(err)
	}

	logger := relay.NewLogger(os.Stderr)
	logRelayStarted(logger, cfg)

	handler := relay.NewHandler(relay.HandlerConfig{
		Registry:        relay.NewRegistry(),
		BrowserUser:     cfg.BrowserUser,
		BrowserPassword: cfg.BrowserPassword,
		AgentToken:      cfg.AgentToken,
		Logger:          logger,
	})

	log.Fatal(newHTTPServer(cfg, handler).ListenAndServe())
}
```

- [ ] **Step 4: Run the startup log test to verify it passes**

Run:

```bash
go test ./cmd/relay -run TestLogRelayStartedWritesListenAddr -v
```

Expected: PASS.

- [ ] **Step 5: Commit the startup logging change**

Run:

```bash
git add cmd/relay/main.go cmd/relay/main_test.go
git commit -m "feat: log relay startup"
```

### Task 3: Log Auth, Upgrade, and Connection Lifecycle in `relay/server.go`

**Files:**
- Modify: `relay/server.go`
- Test: `relay/server_test.go`

- [ ] **Step 1: Write the failing handler log tests**

Add these helpers and tests to `relay/server_test.go`:

```go
type logCapture struct {
	buf bytes.Buffer
}

func newLogCapture() (*Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return NewLogger(&buf), &buf
}

func waitForLogSubstring(t *testing.T, buf *bytes.Buffer, want string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("log = %s, want substring %q", buf.String(), want)
}

func TestHandlerLogsAgentAuthFailure(t *testing.T) {
	logger, buf := newLogCapture()
	handler := NewHandler(HandlerConfig{
		Registry:        NewRegistry(),
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Logger:          logger,
		Files:           testFiles(),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agent/ws", nil)
	req.RemoteAddr = "10.0.0.8:5000"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(buf.String(), `"event":"auth_failed"`) {
		t.Fatalf("log = %s, want auth_failed", buf.String())
	}
	if !strings.Contains(buf.String(), `"auth_type":"bearer"`) {
		t.Fatalf("log = %s, want bearer auth_type", buf.String())
	}
}

func TestAgentRegisterLogsLifecycle(t *testing.T) {
	logger, buf := newLogCapture()
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Logger:          logger,
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	waitForLogSubstring(t, buf, `"event":"agent_registered"`)

	_ = agentConn.Close()
	waitForLogSubstring(t, buf, `"event":"agent_disconnected"`)
}

func TestClientAttachLogsLifecycle(t *testing.T) {
	logger, buf := newLogCapture()
	reg := NewRegistry()
	server := httptest.NewServer(NewHandler(HandlerConfig{
		Registry:        reg,
		BrowserUser:     "demo",
		BrowserPassword: "secret",
		AgentToken:      "agent-token",
		Logger:          logger,
	}))
	defer server.Close()

	agentConn := dialAndRegisterAgent(t, server.URL, "sess-1")
	defer agentConn.Close()

	browserURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/sessions/sess-1/ws"
	headers := http.Header{}
	headers.Set("Authorization", basicAuth("demo", "secret"))
	browserConn, _, err := websocket.DefaultDialer.Dial(browserURL, headers)
	if err != nil {
		t.Fatalf("Dial browser returned error: %v", err)
	}

	waitForLogSubstring(t, buf, `"event":"client_ws_connected"`)
	_ = browserConn.Close()
	waitForLogSubstring(t, buf, `"event":"client_disconnected"`)
}
```

Also extend `TestBrowserAttachRejectsForeignOrigin` to build the handler with `Logger: logger` and assert the buffer contains `"event":"ws_upgrade_failed"`.

Update the import block in `relay/server_test.go` to include `bytes`.

- [ ] **Step 2: Run the server log tests to verify they fail**

Run:

```bash
go test ./relay -run 'TestHandlerLogsAgentAuthFailure|TestAgentRegisterLogsLifecycle|TestClientAttachLogsLifecycle|TestBrowserAttachRejectsForeignOrigin' -v
```

Expected: FAIL because `HandlerConfig` has no `Logger`, no lifecycle logs are emitted, and the foreign-origin test will not find `ws_upgrade_failed`.

- [ ] **Step 3: Implement handler logging and disconnect classification**

Update `relay/server.go` with these changes:

```go
type HandlerConfig struct {
	Registry              *Registry
	BrowserUser           string
	BrowserPassword       string
	AgentToken            string
	Files                 fs.FS
	Logger                *Logger
	AgentReadTimeout      time.Duration
	AgentPingInterval     time.Duration
	AgentPingWriteTimeout time.Duration
}

type disconnectState struct {
	mu     sync.Mutex
	reason string
}

func newDisconnectState(initial string) *disconnectState {
	return &disconnectState{reason: initial}
}

func (s *disconnectState) Force(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reason = reason
}

func (s *disconnectState) SetIfNot(reason, blocked string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reason == blocked {
		return
	}
	s.reason = reason
}

func (s *disconnectState) Get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

func classifyCloseReason(err error) string {
	if err == nil {
		return "client_closed"
	}
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return "client_closed"
	}
	return "read_error"
}

func NewHandler(cfg HandlerConfig) http.Handler {
	registry := cfg.Registry
	if registry == nil {
		registry = NewRegistry()
	}

	logger := cfg.Logger
	if logger == nil {
		logger = NewDiscardLogger()
	}
	registry.SetLogger(logger)

	// existing setup...

	mux.HandleFunc("/agent/ws", func(w http.ResponseWriter, r *http.Request) {
		if !checkBearer(r, cfg.AgentToken) {
			logger.Warn(
				"auth_failed",
				String("path", r.URL.Path),
				String("remote_addr", r.RemoteAddr),
				String("auth_type", "bearer"),
			)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := agentUpgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Warn(
				"ws_upgrade_failed",
				String("path", r.URL.Path),
				String("remote_addr", r.RemoteAddr),
				String("role", "agent"),
			)
			return
		}
		defer conn.Close()

		logger.Info("agent_ws_connected", String("remote_addr", r.RemoteAddr))

		var register protocol.AgentFrame
		if err := conn.ReadJSON(&register); err != nil || register.Type != "register" || register.Session == nil {
			return
		}

		sessionID := register.Session.SessionID
		startedAt := time.Now()
		disconnect := newDisconnectState("client_closed")

		peer := newWSAgentPeer(conn)
		registry.Register(*register.Session, peer)
		logger.Info(
			"agent_registered",
			String("session_id", register.Session.SessionID),
			String("launcher", register.Session.Launcher),
			String("label", register.Session.Label),
			String("cwd", register.Session.CWD),
		)
		defer func() {
			registry.RemoveIfOwner(sessionID, peer)
			logger.Info(
				"agent_disconnected",
				String("session_id", sessionID),
				Int64("duration_ms", time.Since(startedAt).Milliseconds()),
				String("reason", disconnect.Get()),
			)
		}()

		for {
			var msg protocol.Message
			if err := conn.ReadJSON(&msg); err != nil {
				disconnect.SetIfNot(classifyCloseReason(err), "backpressure")
				return
			}
			switch msg.Type {
			case "output":
				data, err := protocol.DecodeData(msg)
				if err != nil {
					continue
				}
				registry.TouchOutputIfOwner(sessionID, peer, data, time.Now().UTC())
			case "resize":
				registry.BroadcastResize(sessionID, msg.Cols, msg.Rows)
			}
		}
	})

	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.BrowserUser, cfg.BrowserPassword) {
			logger.Warn(
				"auth_failed",
				String("path", r.URL.Path),
				String("remote_addr", r.RemoteAddr),
				String("auth_type", "basic"),
			)
			w.Header().Set("WWW-Authenticate", `Basic realm="agentunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// existing route parsing...

		conn, err := browserUpgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Warn(
				"ws_upgrade_failed",
				String("path", r.URL.Path),
				String("remote_addr", r.RemoteAddr),
				String("role", "client"),
			)
			return
		}
		defer conn.Close()

		sinkID := "browser-" + strconv.FormatUint(atomic.AddUint64(&nextSinkID, 1), 10)
		disconnect := newDisconnectState("client_closed")

		sink := newWSSinkWithConfig(conn, defaultWSSinkBufferSize, defaultWSWriteTimeout, nil)
		if err := registry.AddSink(sessionID, sinkID, sink); err != nil {
			disconnect.Force("session_not_found")
			_ = sink.Close()
			return
		}

		startedAt := time.Now()
		logger.Info(
			"client_ws_connected",
			String("session_id", sessionID),
			String("client_id", sinkID),
			String("remote_addr", r.RemoteAddr),
			String("user_agent", r.UserAgent()),
		)

		defer func() {
			registry.RemoveSink(sessionID, sinkID)
			_ = sink.Close()
			logger.Info(
				"client_disconnected",
				String("session_id", sessionID),
				String("client_id", sinkID),
				Int64("duration_ms", time.Since(startedAt).Milliseconds()),
				String("reason", disconnect.Get()),
			)
		}()

		for {
			var msg protocol.Message
			if err := conn.ReadJSON(&msg); err != nil {
				disconnect.SetIfNot(classifyCloseReason(err), "backpressure")
				return
			}

			if msg.Type == "input" {
				data, err := protocol.DecodeData(msg)
				if err == nil {
					_ = registry.WriteInput(sessionID, data)
				}
			}
		}
	})

	return mux
}
```

- [ ] **Step 4: Run the server log tests to verify they pass**

Run:

```bash
go test ./relay -run 'TestHandlerLogsAgentAuthFailure|TestAgentRegisterLogsLifecycle|TestClientAttachLogsLifecycle|TestBrowserAttachRejectsForeignOrigin' -v
```

Expected: PASS.

- [ ] **Step 5: Commit the server lifecycle logging**

Run:

```bash
git add relay/server.go relay/server_test.go
git commit -m "feat: log relay connection lifecycle"
```

### Task 4: Log Session Replacement and Sink Backpressure

**Files:**
- Modify: `relay/registry.go`
- Test: `relay/registry_test.go`
- Create: `relay/ws_sink_test.go`
- Modify: `relay/server.go`

- [ ] **Step 1: Write the failing replacement and backpressure tests**

Append this test to `relay/registry_test.go`:

```go
func TestRegistryReplaceSessionIDLogsSessionReplaced(t *testing.T) {
	var buf bytes.Buffer
	reg := NewRegistry()
	reg.SetLogger(NewLogger(&buf))

	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, &recordingPeer{})
	reg.Register(protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"}, &recordingPeer{})

	got := buf.String()
	if !strings.Contains(got, `"event":"session_replaced"`) {
		t.Fatalf("log = %s, want session_replaced", got)
	}
	if !strings.Contains(got, `"session_id":"sess-1"`) {
		t.Fatalf("log = %s, want session_id", got)
	}
}
```

Update `relay/registry_test.go` imports to include `strings`.

Create `relay/ws_sink_test.go` with this deterministic backpressure test:

```go
package relay

import (
	"testing"
	"time"

	"yuanbohan/tunnel/protocol"
)

type blockingWSConn struct {
	writes  chan protocol.Message
	release chan struct{}
}

func (c *blockingWSConn) WriteJSON(v any) error {
	msg, ok := v.(protocol.Message)
	if !ok {
		panic("unexpected message type")
	}
	c.writes <- msg
	<-c.release
	return nil
}

func (c *blockingWSConn) SetWriteDeadline(time.Time) error { return nil }
func (c *blockingWSConn) Close() error                     { return nil }

func TestWSSinkBackpressureCallsHook(t *testing.T) {
	conn := &blockingWSConn{
		writes:  make(chan protocol.Message, 2),
		release: make(chan struct{}),
	}
	called := 0
	sink := newWSSinkWithConfig(conn, 1, 0, func() { called++ })
	defer sink.Close()
	defer close(conn.release)

	if err := sink.WriteOutput([]byte("one")); err != nil {
		t.Fatalf("WriteOutput one returned error: %v", err)
	}
	<-conn.writes

	if err := sink.WriteOutput([]byte("two")); err != nil {
		t.Fatalf("WriteOutput two returned error: %v", err)
	}
	if err := sink.WriteOutput([]byte("three")); err != errWSSinkBackpressure {
		t.Fatalf("WriteOutput three error = %v, want %v", err, errWSSinkBackpressure)
	}
	if called != 1 {
		t.Fatalf("backpressure hook count = %d, want 1", called)
	}
}
```

- [ ] **Step 2: Run the replacement and backpressure tests to verify they fail**

Run:

```bash
go test ./relay -run 'TestRegistryReplaceSessionIDLogsSessionReplaced|TestWSSinkBackpressureCallsHook' -v
```

Expected: FAIL because `Registry` has no logger setter and `newWSSinkWithConfig` has no backpressure hook parameter.

- [ ] **Step 3: Implement registry logging and wire backpressure logging into the server**

Update `relay/registry.go` like this:

```go
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*liveSession
	logger   *Logger
}

func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*liveSession),
		logger:   NewDiscardLogger(),
	}
}

func (r *Registry) SetLogger(logger *Logger) {
	if logger == nil {
		logger = NewDiscardLogger()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logger = logger
}

func (r *Registry) Register(info protocol.SessionInfo, peer AgentPeer) {
	r.mu.Lock()
	old := r.sessions[info.SessionID]
	// existing replacement logic...
	r.mu.Unlock()

	if old != nil {
		r.logger.Warn("session_replaced", String("session_id", info.SessionID))
	}
	if old != nil && old.peer != nil {
		_ = old.peer.Close()
	}
}
```

Update `relay/server.go` like this:

```go
type wsSink struct {
	conn           wsConn
	writeTimeout   time.Duration
	onBackpressure func()

	mu        sync.RWMutex
	closed    bool
	outbound  chan protocol.Message
	closeOnce sync.Once
}

func newWSSink(conn wsConn) *wsSink {
	return newWSSinkWithConfig(conn, defaultWSSinkBufferSize, defaultWSWriteTimeout, nil)
}

func newWSSinkWithConfig(conn wsConn, bufferSize int, writeTimeout time.Duration, onBackpressure func()) *wsSink {
	if bufferSize <= 0 {
		bufferSize = 1
	}

	sink := &wsSink{
		conn:           conn,
		writeTimeout:   writeTimeout,
		onBackpressure: onBackpressure,
		outbound:       make(chan protocol.Message, bufferSize),
	}
	go sink.run()
	return sink
}

func (s *wsSink) enqueue(msg protocol.Message) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errWSSinkClosed
	}

	select {
	case s.outbound <- msg:
		s.mu.RUnlock()
		return nil
	default:
		s.mu.RUnlock()
		if s.onBackpressure != nil {
			s.onBackpressure()
		}
		_ = s.Close()
		return errWSSinkBackpressure
	}
}
```

Then replace the client sink creation in `relay/server.go` with:

```go
sink := newWSSinkWithConfig(conn, defaultWSSinkBufferSize, defaultWSWriteTimeout, func() {
	disconnect.Force("backpressure")
	logger.Warn(
		"sink_backpressure",
		String("session_id", sessionID),
		String("client_id", sinkID),
	)
})
```

- [ ] **Step 4: Run the focused tests and then the full relay test suites**

Run:

```bash
go test ./relay -run 'TestRegistryReplaceSessionIDLogsSessionReplaced|TestWSSinkBackpressureCallsHook' -v
go test ./relay ./cmd/relay -v
```

Expected:

- the focused replacement and backpressure tests PASS
- the full `relay` and `cmd/relay` package tests PASS

- [ ] **Step 5: Commit the replacement and backpressure logging**

Run:

```bash
git add relay/registry.go relay/registry_test.go relay/server.go relay/ws_sink_test.go
git commit -m "feat: log relay replacement and backpressure events"
```

## Plan Self-Review

Spec coverage check:

- `relay_started`: covered by Task 2.
- `auth_failed`: covered by Task 3.
- `ws_upgrade_failed`: covered by Task 3.
- `agent_ws_connected`, `agent_registered`, `agent_disconnected`: covered by Task 3.
- `client_ws_connected`, `client_disconnected`: covered by Task 3.
- `session_replaced`: covered by Task 4.
- `sink_backpressure`: covered by Task 4.
- No frame payload logging: preserved by all tasks because no task adds logs on successful `input`, `output`, or `resize` forwarding.

Placeholder scan:

- No `TODO`, `TBD`, or “similar to” references remain.
- Every code step includes concrete snippets.
- Every verification step includes explicit commands and expected results.

Type consistency check:

- `Logger`, `Field`, `String`, and `Int64` are introduced in Task 1 and used consistently in Tasks 2 through 4.
- `HandlerConfig.Logger` is added in Task 3 and then reused in Task 4.
- `newWSSinkWithConfig(..., onBackpressure func())` is introduced in Task 4 and used consistently in the same task.
