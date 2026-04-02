package server

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/session"
)

type fakeSession struct {
	mu sync.Mutex

	input    []byte
	cols     int
	rows     int
	sinks    map[string]session.OutputSink
	inputCh  chan struct{}
	resizeCh chan struct{}
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

func (f *fakeSession) Resize(cols, rows int) error {
	f.mu.Lock()
	f.cols = cols
	f.rows = rows
	f.mu.Unlock()

	if f.resizeCh != nil {
		select {
		case f.resizeCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func (f *fakeSession) Input() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.input...)
}

func (f *fakeSession) ResizeSize() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cols, f.rows
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

func TestWebSocketBridgeForwardsResize(t *testing.T) {
	sess := &fakeSession{resizeCh: make(chan struct{}, 1)}
	server := httptest.NewServer(NewHandler(sess))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(protocol.Message{
		Type: "resize",
		Cols: 120,
		Rows: 40,
	}); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	select {
	case <-sess.resizeCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resize")
	}

	cols, rows := sess.ResizeSize()
	if cols != 120 || rows != 40 {
		t.Fatalf("resize = %dx%d, want 120x40", cols, rows)
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
