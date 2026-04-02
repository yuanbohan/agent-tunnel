package relayclient

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/relayapi"
	"yuanbohan/tunnel/internal/session"
)

func TestConnectorSendsRegisterBeforeStreamingOutput(t *testing.T) {
	received := make(chan relayapi.AgentFrame, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}
		defer conn.Close()

		var frame relayapi.AgentFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("ReadJSON returned error: %v", err)
		}
		received <- frame
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	connector := New(Config{URL: wsURL, Token: "token"}, relayapi.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	connector.BindHub(hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go connector.Run(ctx)

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

		var register relayapi.AgentFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Fatalf("ReadJSON returned error: %v", err)
		}

		msg := protocol.Message{
			Type: "input",
			Data: base64.StdEncoding.EncodeToString([]byte("hello")),
		}
		if err := conn.WriteJSON(msg); err != nil {
			t.Fatalf("WriteJSON returned error: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	connector := New(Config{URL: wsURL, Token: "token"}, relayapi.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	connector.BindHub(hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go connector.Run(ctx)

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

		var register relayapi.AgentFrame
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
	connector := New(Config{URL: wsURL, Token: "token"}, relayapi.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go connector.Run(ctx)

	if err := connector.WriteOutput([]byte("world")); err != nil {
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
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for output")
	}
}
