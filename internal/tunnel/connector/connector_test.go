package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
)

func TestConnectorSendsRegisterFrame(t *testing.T) {
	registerCh := make(chan protocol.AgentFrame, 1)
	server := newConnectorTestServer(t, func(conn *websocket.Conn, register protocol.AgentFrame) {
		registerCh <- register
	})
	defer server.Close()

	c := New(connectorWSURL(server), "token-1", protocol.SessionInfo{
		SessionID:      "sess-1",
		Launcher:       "codex",
		CWD:            "/repo",
		CommandPreview: "codex",
	})
	c.SetReconnectEnabled(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case frame := <-registerCh:
		if frame.Type != "register" || frame.Session == nil || frame.Session.SessionID != "sess-1" {
			t.Fatalf("register = %#v, want register sess-1", frame)
		}
		if frame.LaunchContext != nil {
			t.Fatalf("LaunchContext = %#v, want nil", frame.LaunchContext)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for register frame")
	}
}

func TestConnectorIncludesLaunchContextAndSendsLaunchReady(t *testing.T) {
	framesCh := make(chan protocol.AgentFrame, 2)
	server := newConnectorTestServer(t, func(conn *websocket.Conn, register protocol.AgentFrame) {
		framesCh <- register
		var frame protocol.AgentFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Errorf("ReadJSON launch_ready returned error: %v", err)
			return
		}
		framesCh <- frame
	})
	defer server.Close()

	launchContext := protocol.LaunchContext{Source: protocol.SessionLaunchSourceMobile, RequestID: "req-123"}
	c := New(connectorWSURL(server), "token-1", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.SetLaunchContext(launchContext)
	c.SetReconnectEnabled(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	register := readConnectorFrame(t, framesCh)
	if register.Type != "register" || register.LaunchContext == nil || *register.LaunchContext != launchContext {
		t.Fatalf("register = %#v, want launch context", register)
	}

	c.MarkLaunchReady(launchContext)
	ready := readConnectorFrame(t, framesCh)
	if ready.Type != "launch_ready" || ready.LaunchContext == nil || *ready.LaunchContext != launchContext {
		t.Fatalf("launch_ready = %#v, want launch context", ready)
	}
}

func TestConnectorResendsLaunchReadyAfterReconnect(t *testing.T) {
	launchContext := protocol.LaunchContext{Source: protocol.SessionLaunchSourceMobile, RequestID: "req-123"}
	firstRegistered := make(chan struct{})
	closeFirst := make(chan struct{})
	readyCh := make(chan protocol.AgentFrame, 1)
	var connections atomic.Int32

	server := newConnectorTestServer(t, func(conn *websocket.Conn, register protocol.AgentFrame) {
		switch connections.Add(1) {
		case 1:
			close(firstRegistered)
			<-closeFirst
			_ = conn.Close()
		case 2:
			var ready protocol.AgentFrame
			if err := conn.ReadJSON(&ready); err != nil {
				t.Errorf("ReadJSON launch_ready after reconnect returned error: %v", err)
				return
			}
			readyCh <- ready
		default:
			t.Errorf("unexpected extra connector registration: %#v", register)
		}
	})
	defer server.Close()

	c := New(connectorWSURL(server), "token-1", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.retryBackoff = []time.Duration{10 * time.Millisecond}
	c.SetLaunchContext(launchContext)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case <-firstRegistered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first registration")
	}

	c.MarkLaunchReady(launchContext)
	close(closeFirst)

	ready := readConnectorFrame(t, readyCh)
	if ready.Type != "launch_ready" || ready.LaunchContext == nil || *ready.LaunchContext != launchContext {
		t.Fatalf("launch_ready after reconnect = %#v, want launch context", ready)
	}
}

func TestConnectorIgnoresUnexpectedInboundAttachFrames(t *testing.T) {
	ready := make(chan struct{})
	framesCh := make(chan protocol.AgentFrame, 1)
	server := newConnectorTestServer(t, func(conn *websocket.Conn, register protocol.AgentFrame) {
		if err := conn.WriteJSON(map[string]any{"type": "attach_open", "client_id": "legacy-client"}); err != nil {
			t.Errorf("WriteJSON attach_open returned error: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte("legacy terminal bytes")); err != nil {
			t.Errorf("WriteMessage binary returned error: %v", err)
			return
		}
		close(ready)
		var frame protocol.AgentFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Errorf("ReadJSON launch_ready returned error: %v", err)
			return
		}
		framesCh <- frame
	})
	defer server.Close()

	launchContext := protocol.LaunchContext{Source: protocol.SessionLaunchSourceMobile, RequestID: "req-123"}
	c := New(connectorWSURL(server), "token-1", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.SetReconnectEnabled(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server to send legacy frames")
	}

	c.MarkLaunchReady(launchContext)
	frame := readConnectorFrame(t, framesCh)
	if frame.Type != "launch_ready" {
		t.Fatalf("frame = %#v, want launch_ready after ignored legacy frames", frame)
	}
}

func TestConnectorWaitUntilConnectedReturnsTrueAfterRegister(t *testing.T) {
	registered := make(chan struct{})
	server := newConnectorTestServer(t, func(conn *websocket.Conn, register protocol.AgentFrame) {
		close(registered)
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()

	c := New(connectorWSURL(server), "token-1", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.SetReconnectEnabled(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	if !c.WaitUntilConnected(context.Background(), 2*time.Second) {
		t.Fatal("WaitUntilConnected returned false, want true")
	}
	select {
	case <-registered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for register frame")
	}
}

func readConnectorFrame(t *testing.T, ch <-chan protocol.AgentFrame) protocol.AgentFrame {
	t.Helper()
	select {
	case frame := <-ch:
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connector frame")
		return protocol.AgentFrame{}
	}
}

func newConnectorTestServer(t *testing.T, handler func(*websocket.Conn, protocol.AgentFrame)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/ws" {
			t.Fatalf("path = %q, want /agent/ws", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q, want Bearer token-1", got)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}
		defer conn.Close()

		var register protocol.AgentFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Fatalf("ReadJSON register returned error: %v", err)
		}
		if handler != nil {
			handler(conn, register)
		}
	}))
}

func TestConnectorRunUsesAgentWebSocketPath(t *testing.T) {
	pathCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathCh <- r.URL.Path
		http.Error(w, "stop", http.StatusInternalServerError)
	}))
	defer server.Close()

	c := New(strings.TrimRight(connectorWSURL(server), "/"), "token-1", protocol.SessionInfo{SessionID: "sess-1"})
	c.SetReconnectEnabled(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case path := <-pathCh:
		if path != "/agent/ws" {
			t.Fatalf("path = %q, want /agent/ws", path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request path")
	}
}

func connectorWSURL(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http")
}
