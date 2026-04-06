package connector

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

	if _, err := c.runOnce(ctx, 0); err == nil {
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
		_, err := c.runOnce(ctx, 0)
		done <- err
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

func TestConnectorQueuesInputUntilHubIsBound(t *testing.T) {
	inputCh := make(chan string, 1)

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

		if err := conn.WriteJSON(protocol.EncodeInputText("queued")); err != nil {
			t.Fatalf("WriteJSON returned error: %v", err)
		}

		<-r.Context().Done()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	if !c.WaitUntilConnected(ctx, time.Second) {
		t.Fatal("WaitUntilConnected returned false, want true")
	}

	time.Sleep(50 * time.Millisecond)

	hub := session.NewHub(func(data []byte) error {
		inputCh <- string(data)
		return nil
	}, func(int, int) error { return nil })
	c.BindHub(hub)

	select {
	case got := <-inputCh:
		if got != "queued" {
			t.Fatalf("input = %q, want queued", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for queued input")
	}
}

func TestConnectorWaitUntilConnectedReturnsTrueAfterRegister(t *testing.T) {
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

		<-r.Context().Done()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	if !c.WaitUntilConnected(ctx, time.Second) {
		t.Fatal("WaitUntilConnected returned false, want true")
	}
}

func TestConnectorWaitUntilConnectedReturnsFalseOnTimeout(t *testing.T) {
	c := New("ws://relay.test", "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.dialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	if c.WaitUntilConnected(ctx, 25*time.Millisecond) {
		t.Fatal("WaitUntilConnected returned true, want false")
	}
}

func TestConnectorEmitsStateChangesAcrossReconnect(t *testing.T) {
	var connections atomic.Int32
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}

		var register protocol.AgentFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Fatalf("ReadJSON returned error: %v", err)
		}

		switch connections.Add(1) {
		case 1:
			_ = conn.Close()
		default:
			defer conn.Close()
			<-r.Context().Done()
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.retryBackoff = []time.Duration{10 * time.Millisecond}

	stateCh, cancelStates := c.SubscribeStateChanges()
	defer cancelStates()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	want := []State{StateConnecting, StateConnected, StateReconnecting, StateConnected}
	got := make([]State, 0, len(want))
	deadline := time.After(2 * time.Second)

	for len(got) < len(want) {
		select {
		case state := <-stateCh:
			if len(got) == 0 && state == StateDisconnected {
				continue
			}
			if len(got) > 0 && got[len(got)-1] == state {
				continue
			}
			got = append(got, state)
		case <-deadline:
			t.Fatalf("state changes = %#v, want %#v", got, want)
		}
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("state changes = %#v, want %#v", got, want)
		}
	}
}

func TestConnectorInitialConnectTimeoutAppliesOnlyToFirstAttempt(t *testing.T) {
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

		<-r.Context().Done()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.connectTTL = 25 * time.Millisecond
	c.retryBackoff = []time.Duration{10 * time.Millisecond}
	var dialAttempts atomic.Int32
	c.dialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if dialAttempts.Add(1) == 1 {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startedAt := time.Now()
	go c.Run(ctx)

	if !c.WaitUntilConnected(ctx, 250*time.Millisecond) {
		t.Fatal("WaitUntilConnected returned false after the retry, want true")
	}
	if elapsed := time.Since(startedAt); elapsed >= 200*time.Millisecond {
		t.Fatalf("connection elapsed = %v, want connector to recover within 200ms", elapsed)
	}
	if attempts := dialAttempts.Load(); attempts < 2 {
		t.Fatalf("dial attempts = %d, want at least 2", attempts)
	}
}

func TestConnectorReconnectBackoffResetsAfterSuccessfulReconnect(t *testing.T) {
	var connections atomic.Int32
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}

		var register protocol.AgentFrame
		if err := conn.ReadJSON(&register); err != nil {
			t.Fatalf("ReadJSON returned error: %v", err)
		}

		switch connections.Add(1) {
		case 1, 2:
			_ = conn.Close()
		default:
			defer conn.Close()
			<-r.Context().Done()
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.retryBackoff = []time.Duration{3 * time.Second, 5 * time.Second, 10 * time.Second}
	var sleeps []time.Duration

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.sleep = func(ctx context.Context, d time.Duration) bool {
		sleeps = append(sleeps, d)
		if len(sleeps) >= 2 {
			cancel()
		}
		return true
	}

	go c.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for len(sleeps) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if len(sleeps) < 2 {
		t.Fatalf("sleep calls = %#v, want 2", sleeps)
	}
	if sleeps[0] != 3*time.Second || sleeps[1] != 3*time.Second {
		t.Fatalf("sleep calls = %#v, want [3s 3s]", sleeps)
	}
}
