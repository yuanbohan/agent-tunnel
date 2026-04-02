package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/session"
)

type fakeSession struct {
	input   []byte
	cols    int
	rows    int
	sinks   map[string]session.OutputSink
	inputCh chan struct{}
}

func (f *fakeSession) AddSink(id string, sink session.OutputSink) {
	if f.sinks == nil {
		f.sinks = make(map[string]session.OutputSink)
	}
	f.sinks[id] = sink
}

func (f *fakeSession) RemoveSink(id string) {
	delete(f.sinks, id)
}

func (f *fakeSession) WriteInput(data []byte) error {
	f.input = append([]byte(nil), data...)
	if f.inputCh != nil {
		select {
		case f.inputCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func (f *fakeSession) Resize(cols, rows int) error {
	f.cols = cols
	f.rows = rows
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

	if string(sess.input) != "hello" {
		t.Fatalf("input = %q, want hello", string(sess.input))
	}

	for _, sink := range sess.sinks {
		if err := sink.WriteOutput([]byte("world")); err != nil {
			t.Fatalf("WriteOutput returned error: %v", err)
		}
	}

	var msg protocol.Message
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	out, err := protocol.DecodeData(msg)
	if err != nil {
		t.Fatalf("DecodeData returned error: %v", err)
	}
	if string(out) != "world" {
		t.Fatalf("output = %q, want world", string(out))
	}
}
