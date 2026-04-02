package server

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/session"
)

type fakeSession struct {
	mu sync.Mutex

	input      []byte
	cols       int
	rows       int
	sinks      map[string]session.OutputSink
	inputCh    chan struct{}
	onResizeCb func(int, int)
}

func (f *fakeSession) AddSink(id string, sink session.OutputSink) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.sinks == nil {
		f.sinks = make(map[string]session.OutputSink)
	}
	f.sinks[id] = sink
}

func (f *fakeSession) RemoveSink(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.sinks, id)
}

func (f *fakeSession) WriteInput(data []byte) error {
	f.mu.Lock()
	f.input = append([]byte(nil), data...)
	f.mu.Unlock()

	if f.inputCh != nil {
		select {
		case f.inputCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func (f *fakeSession) CurrentSize() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cols, f.rows
}

func (f *fakeSession) OnResize(cb func(int, int)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onResizeCb = cb
}

func (f *fakeSession) Input() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.input...)
}

func (f *fakeSession) Sinks() map[string]session.OutputSink {
	f.mu.Lock()
	defer f.mu.Unlock()

	sinks := make(map[string]session.OutputSink, len(f.sinks))
	for id, sink := range f.sinks {
		sinks[id] = sink
	}
	return sinks
}

type recordingSink struct {
	mu     sync.Mutex
	chunks [][]byte
}

func (s *recordingSink) WriteOutput(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks = append(s.chunks, append([]byte(nil), data...))
	return nil
}

func (s *recordingSink) Joined() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var joined []byte
	for _, chunk := range s.chunks {
		joined = append(joined, chunk...)
	}
	return string(joined)
}

type blockingWSConn struct {
	writeStarted chan struct{}
	releaseWrite chan struct{}
	closeSignal  chan struct{}
	closeCalls   atomic.Int32
}

func newBlockingWSConn() *blockingWSConn {
	return &blockingWSConn{
		writeStarted: make(chan struct{}, 1),
		releaseWrite: make(chan struct{}),
		closeSignal:  make(chan struct{}),
	}
}

func (c *blockingWSConn) WriteJSON(any) error {
	select {
	case c.writeStarted <- struct{}{}:
	default:
	}

	select {
	case <-c.releaseWrite:
		return nil
	case <-c.closeSignal:
		return errors.New("closed")
	}
}

func (c *blockingWSConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (c *blockingWSConn) Close() error {
	if c.closeCalls.Add(1) == 1 {
		close(c.closeSignal)
	}
	return nil
}

func TestNewHandlerServesIndex(t *testing.T) {
	handler := NewHandler(&fakeSession{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content-type = %q, want text/html", rec.Header().Get("Content-Type"))
	}
}

func TestWebSocketBridgeForwardsInputAndOutput(t *testing.T) {
	sess := &fakeSession{inputCh: make(chan struct{}, 1)}
	server := httptest.NewServer(NewHandler(sess))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(protocol.Message{
		Type: "input",
		Data: base64.StdEncoding.EncodeToString([]byte("hello")),
	}); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	select {
	case <-sess.inputCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for input")
	}

	if string(sess.Input()) != "hello" {
		t.Fatalf("input = %q, want hello", string(sess.Input()))
	}

	for _, sink := range sess.Sinks() {
		if err := sink.WriteOutput([]byte("world")); err != nil {
			t.Fatalf("WriteOutput returned error: %v", err)
		}
	}

	var msg protocol.Message
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	if msg.Type != "output" {
		t.Fatalf("message type = %q, want output", msg.Type)
	}
	out, err := protocol.DecodeData(msg)
	if err != nil {
		t.Fatalf("DecodeData returned error: %v", err)
	}
	if string(out) != "world" {
		t.Fatalf("output = %q, want world", string(out))
	}
}

func TestWSSinkBackpressureDoesNotBlockHubBroadcast(t *testing.T) {
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	conn := newBlockingWSConn()
	sink := newWSSinkWithConfig(conn, 1, 0)
	t.Cleanup(func() {
		_ = sink.Close()
		close(conn.releaseWrite)
	})

	observer := &recordingSink{}
	hub.AddSink("browser", sink)
	hub.AddSink("observer", observer)

	hub.BroadcastOutput([]byte("one"))

	select {
	case <-conn.writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocked websocket writer")
	}

	hub.BroadcastOutput([]byte("two"))

	done := make(chan struct{})
	go func() {
		hub.BroadcastOutput([]byte("three"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("BroadcastOutput blocked on stalled websocket sink")
	}

	if got := observer.Joined(); got != "onetwothree" {
		t.Fatalf("observer output = %q, want onetwothree", got)
	}
}

func TestWSSinkClosesConnectionWhenQueueFills(t *testing.T) {
	conn := newBlockingWSConn()
	sink := newWSSinkWithConfig(conn, 1, 0)
	t.Cleanup(func() {
		_ = sink.Close()
		close(conn.releaseWrite)
	})

	if err := sink.WriteOutput([]byte("one")); err != nil {
		t.Fatalf("first WriteOutput returned error: %v", err)
	}

	select {
	case <-conn.writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocked websocket writer")
	}

	if err := sink.WriteOutput([]byte("two")); err != nil {
		t.Fatalf("second WriteOutput returned error: %v", err)
	}

	start := time.Now()
	err := sink.WriteOutput([]byte("three"))
	if !errors.Is(err, errWSSinkBackpressure) {
		t.Fatalf("third WriteOutput error = %v, want %v", err, errWSSinkBackpressure)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("third WriteOutput took %v, want under 200ms", elapsed)
	}

	select {
	case <-conn.closeSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close")
	}
}

func TestWebSocketBridgeRejectsCrossOrigin(t *testing.T) {
	sess := &fakeSession{}
	server := httptest.NewServer(NewHandler(sess))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Origin", "http://evil.example")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		conn.Close()
		t.Fatal("Dial succeeded for cross-origin request, want rejection")
	}
}

func TestWebSocketBridgeAllowsSameOrigin(t *testing.T) {
	sess := &fakeSession{}
	server := httptest.NewServer(NewHandler(sess))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Origin", server.URL)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	conn.Close()
}

func TestWebSocketSendsPTYSizeOnConnect(t *testing.T) {
	sess := &fakeSession{cols: 120, rows: 40}

	server := httptest.NewServer(NewHandler(sess))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	var msg protocol.Message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}

	if msg.Type != "resize" || msg.Cols != 120 || msg.Rows != 40 {
		t.Fatalf("initial message = %+v, want resize 120x40", msg)
	}
}

func TestWebSocketIgnoresBrowserResize(t *testing.T) {
	sess := &fakeSession{cols: 80, rows: 24, inputCh: make(chan struct{}, 1)}

	server := httptest.NewServer(NewHandler(sess))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	// Read and discard the initial PTY size message
	var initial protocol.Message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&initial); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}

	// Send a resize from browser — should be ignored
	if err := conn.WriteJSON(protocol.Message{
		Type: "resize",
		Cols: 200,
		Rows: 60,
	}); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	// Send an input message to verify the connection is still working
	if err := conn.WriteJSON(protocol.Message{
		Type: "input",
		Data: base64.StdEncoding.EncodeToString([]byte("test")),
	}); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	select {
	case <-sess.inputCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for input after ignored resize")
	}

	// Verify the session size was NOT changed
	cols, rows := sess.CurrentSize()
	if cols != 80 || rows != 24 {
		t.Fatalf("session resized to %dx%d, want 80x24 (unchanged)", cols, rows)
	}
}

func TestStartLocalReturnsUsableHTTPURL(t *testing.T) {
	running, err := StartLocal(&fakeSession{})
	if err != nil {
		t.Fatalf("StartLocal returned error: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := running.Close(ctx); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}()

	if !strings.HasPrefix(running.URL, "http://127.0.0.1:") {
		t.Fatalf("URL = %q, want http://127.0.0.1:<port>", running.URL)
	}

	resp, err := http.Get(running.URL)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
