package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/session"
)

func TestConnectorSendsRegisterBeforeStreamingOutput(t *testing.T) {
	received := make(chan protocol.AgentFrame, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}
		defer conn.Close()

		var frame protocol.AgentFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("ReadJSON returned error: %v", err)
		}
		received <- frame
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.BindHub(hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case frame := <-received:
		if frame.Type != "register" {
			t.Fatalf("Type = %q, want register", frame.Type)
		}
		if frame.Session == nil || frame.Session.SessionID != "sess-1" {
			t.Fatalf("Session = %#v, want sess-1", frame.Session)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for register frame")
	}
}

func TestConnectorRoutesInputFrameIntoHub(t *testing.T) {
	inputCh := make(chan string, 1)
	hub := session.NewHub(func(data []byte) error {
		inputCh <- string(data)
		return nil
	}, func(int, int) error { return nil })

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}
		defer conn.Close()

		var register protocol.AgentFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Fatalf("ReadJSON returned error: %v", err)
		}

		msg := protocol.EncodeInput([]byte("hello"))
		if err := conn.WriteJSON(msg); err != nil {
			t.Fatalf("WriteJSON returned error: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.BindHub(hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case got := <-inputCh:
		if got != "hello" {
			t.Fatalf("input = %q, want hello", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for input")
	}
}

func TestConnectorStreamsOutputFramesToRelay(t *testing.T) {
	received := make(chan protocol.Message, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}
		defer conn.Close()

		var register protocol.AgentFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Fatalf("ReadJSON register returned error: %v", err)
		}

		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("ReadJSON output returned error: %v", err)
		}
		received <- msg
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.BindHub(hub)
	if err := hub.Resize(120, 40); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	if err := c.WriteOutput([]byte("world")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}

	select {
	case msg := <-received:
		if msg.Type != "output" {
			t.Fatalf("Type = %q, want output", msg.Type)
		}
		data, err := protocol.DecodeData(msg)
		if err != nil {
			t.Fatalf("DecodeData returned error: %v", err)
		}
		if string(data) != "world" {
			t.Fatalf("output = %q, want world", string(data))
		}
		if msg.Cols != 120 || msg.Rows != 40 {
			t.Fatalf("size = %dx%d, want 120x40", msg.Cols, msg.Rows)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for output")
	}
}

func TestConnectorUsesInitialSizeBeforeHubBind(t *testing.T) {
	received := make(chan protocol.Message, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}
		defer conn.Close()

		var register protocol.AgentFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Fatalf("ReadJSON register returned error: %v", err)
		}

		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("ReadJSON output returned error: %v", err)
		}
		received <- msg
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.SetInitialSize(120, 40)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	if err := c.WriteOutput([]byte("hello")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}

	select {
	case msg := <-received:
		if msg.Type != "output" {
			t.Fatalf("Type = %q, want output", msg.Type)
		}
		if msg.Cols != 120 || msg.Rows != 40 {
			t.Fatalf("size = %dx%d, want 120x40", msg.Cols, msg.Rows)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for output frame")
	}
}

func TestConnectorRoutesStructuredInputFramesIntoHub(t *testing.T) {
	tests := []struct {
		name    string
		message protocol.Message
		want    string
	}{
		{name: "input text", message: protocol.EncodeInputText("hello"), want: "hello"},
		{name: "input key", message: protocol.EncodeInputKey("TAB", false, false, false), want: "\t"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inputCh := make(chan string, 1)
			hub := session.NewHub(func(data []byte) error {
				inputCh <- string(data)
				return nil
			}, func(int, int) error { return nil })

			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Fatalf("Upgrade returned error: %v", err)
				}
				defer conn.Close()

				var register protocol.AgentFrame
				if err := conn.ReadJSON(&register); err != nil {
					t.Fatalf("ReadJSON returned error: %v", err)
				}

				if err := conn.WriteJSON(tc.message); err != nil {
					t.Fatalf("WriteJSON returned error: %v", err)
				}
			}))
			defer server.Close()

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
			c := New(wsURL, "token", protocol.SessionInfo{
				SessionID: "sess-1",
				Launcher:  "codex",
			})
			c.BindHub(hub)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go c.Run(ctx)

			select {
			case got := <-inputCh:
				if got != tc.want {
					t.Fatalf("input = %q, want %q", got, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for input")
			}
		})
	}
}

func TestConnectorBuffersOutputAcrossReconnectWithoutLeakingOldWriter(t *testing.T) {
	firstUpgrader := websocket.Upgrader{}
	firstServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := firstUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade first returned error: %v", err)
		}
		defer conn.Close()

		var register protocol.AgentFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Fatalf("ReadJSON first register returned error: %v", err)
		}
	}))
	defer firstServer.Close()

	firstURL := "ws" + strings.TrimPrefix(firstServer.URL, "http")
	c := New(firstURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := c.runOnce(ctx); err == nil {
		t.Fatal("runOnce returned nil after relay disconnect, want error")
	}

	if err := c.WriteOutput([]byte("persisted")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}

	received := make(chan protocol.Message, 1)
	secondUpgrader := websocket.Upgrader{}
	secondServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := secondUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade second returned error: %v", err)
		}
		defer conn.Close()

		var register protocol.AgentFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Fatalf("ReadJSON second register returned error: %v", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("ReadJSON second output returned error: %v", err)
		}
		received <- msg
	}))
	defer secondServer.Close()

	c.url = "ws" + strings.TrimPrefix(secondServer.URL, "http")

	done := make(chan error, 1)
	go func() {
		done <- c.runOnce(ctx)
	}()

	select {
	case msg := <-received:
		if msg.Type != "output" {
			t.Fatalf("Type = %q, want output", msg.Type)
		}
		data, err := protocol.DecodeData(msg)
		if err != nil {
			t.Fatalf("DecodeData returned error: %v", err)
		}
		if string(data) != "persisted" {
			t.Fatalf("output = %q, want persisted", string(data))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnected output")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second runOnce to exit")
	}
}
