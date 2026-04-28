package connector

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xterm "github.com/gitpod-io/xterm-go"
	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/tunnel/session"
)

type recordingCloser struct {
	closed atomic.Int32
}

func (c *recordingCloser) Close() error {
	c.closed.Add(1)
	return nil
}

func joinedBufferText(t *testing.T, buf *xterm.Buffer) string {
	t.Helper()

	lines := make([]string, buf.Lines.Length())
	for i := range lines {
		line := buf.Lines.Get(i)
		if line == nil {
			t.Fatalf("line %d = nil", i)
		}
		lines[i] = line.TranslateToString(true, 0, -1)
	}
	return strings.Join(lines, "\n")
}

func snapshotDoneFromAttachOpen(t *testing.T, c *Connector) protocol.AgentFrame {
	t.Helper()

	const clientID = "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1"
	doneCh := make(chan protocol.AgentFrame, 1)
	errCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}
		defer conn.Close()

		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if messageType != websocket.TextMessage {
				continue
			}
			var frame protocol.AgentFrame
			if err := json.Unmarshal(payload, &frame); err != nil {
				errCh <- err
				return
			}
			if frame.Type == "snapshot_done" {
				doneCh <- frame
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	go func() {
		if err := c.handleAttachOpen(conn, clientID); err != nil {
			errCh <- err
		}
	}()

	select {
	case frame := <-doneCh:
		return frame
	case err := <-errCh:
		t.Fatalf("attach open returned error before snapshot_done: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot_done")
	}
	return protocol.AgentFrame{}
}

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

func TestConnectorIncludesLaunchContextInRegisterFrame(t *testing.T) {
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
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.SetLaunchContext(protocol.LaunchContext{Source: protocol.SessionLaunchSourceMobile, RequestID: "req-123"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case frame := <-received:
		if frame.LaunchContext == nil {
			t.Fatal("LaunchContext = nil, want context")
		}
		if frame.LaunchContext.Source != protocol.SessionLaunchSourceMobile {
			t.Fatalf("LaunchContext.Source = %q, want mobile", frame.LaunchContext.Source)
		}
		if frame.LaunchContext.RequestID != "req-123" {
			t.Fatalf("LaunchContext.RequestID = %q, want req-123", frame.LaunchContext.RequestID)
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

		msg := protocol.ForwardInputTextFrame("", "hello", false)
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

func TestConnectorRunsStopHandlerForStopSessionFrame(t *testing.T) {
	stopped := make(chan struct{}, 1)
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
		if err := conn.WriteJSON(protocol.StopSessionFrame()); err != nil {
			t.Fatalf("WriteJSON returned error: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.SetStopHandler(func() {
		stopped <- struct{}{}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stop handler")
	}
}

func TestConnectorIgnoresMalformedAndUnknownControlFrames(t *testing.T) {
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

		if err := conn.WriteMessage(websocket.TextMessage, []byte("{not-json")); err != nil {
			t.Fatalf("WriteMessage malformed returned error: %v", err)
		}
		if err := conn.WriteJSON(protocol.AgentFrame{Type: "unknown"}); err != nil {
			t.Fatalf("WriteJSON unknown returned error: %v", err)
		}
		if err := conn.WriteJSON(protocol.ForwardInputTextFrame("", "hello", false)); err != nil {
			t.Fatalf("WriteJSON valid input returned error: %v", err)
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
		t.Fatal("timed out waiting for valid input after malformed/unknown frames")
	}
}

func TestConnectorUsesInitialSizeBeforeHubBind(t *testing.T) {
	const clientID = "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1"

	received := make(chan protocol.AgentFrame, 1)
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

		if err := conn.WriteJSON(protocol.AttachOpenFrame(clientID)); err != nil {
			t.Fatalf("WriteJSON attach_open returned error: %v", err)
		}

		var frame protocol.AgentFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("ReadJSON attach_ready returned error: %v", err)
		}
		received <- frame
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

	select {
	case frame := <-received:
		if frame.Type != "attach_ready" {
			t.Fatalf("Type = %q, want attach_ready", frame.Type)
		}
		if frame.ClientID != clientID {
			t.Fatalf("ClientID = %q, want %q", frame.ClientID, clientID)
		}
		if frame.Cols != 120 || frame.Rows != 40 {
			t.Fatalf("size = %dx%d, want 120x40", frame.Cols, frame.Rows)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for attach_ready frame")
	}
}

func TestConnectorRoutesStructuredInputFramesIntoHub(t *testing.T) {
	tests := []struct {
		name    string
		message protocol.AgentFrame
		want    []string
	}{
		{name: "input text", message: protocol.ForwardInputTextFrame("", "hello", false), want: []string{"hello"}},
		{name: "input text submit", message: protocol.ForwardInputTextFrame("", "hello", true), want: []string{"hello", "\r"}},
		{name: "empty input text submit", message: protocol.ForwardInputTextFrame("", "", true), want: []string{"\r"}},
		{name: "input key", message: protocol.ForwardInputKeyFrame("", "TAB"), want: []string{"\t"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inputCh := make(chan string, 4)
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

			got := make([]string, 0, len(tc.want))
			deadline := time.After(2 * time.Second)
			for len(got) < len(tc.want) {
				select {
				case chunk := <-inputCh:
					got = append(got, chunk)
				case <-deadline:
					t.Fatalf("timed out waiting for input, got %#v want %#v", got, tc.want)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("input = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestConnectorRecordsSubmitAnchorsForSubmitInput(t *testing.T) {
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)
	c.BindHub(hub)

	for i := range 3 {
		if i > 0 {
			c.mirror.WriteOutput([]byte("\r\n"))
		}
		c.mirror.WriteOutput([]byte("prompt"))
		c.deliverInputToHub(hub, protocol.ForwardInputTextFrame("client-1", "hello", true))
	}

	done := snapshotDoneFromAttachOpen(t, c)
	if done.ClientID != "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1" {
		t.Fatalf("snapshot_done client_id = %q, want test client", done.ClientID)
	}
	if len(done.SubmitAnchors) != 3 {
		t.Fatalf("anchor count = %d, want 3: %#v", len(done.SubmitAnchors), done.SubmitAnchors)
	}
	for i, anchor := range done.SubmitAnchors {
		if anchor.ID != "submit-"+strconv.Itoa(i+1) {
			t.Fatalf("anchor[%d].ID = %q, want submit-%d", i, anchor.ID, i+1)
		}
		if anchor.Line < 0 {
			t.Fatalf("anchor[%d].Line = %d, want non-negative", i, anchor.Line)
		}
		if anchor.SubmittedAt <= 0 {
			t.Fatalf("anchor[%d].SubmittedAt = %d, want unix timestamp", i, anchor.SubmittedAt)
		}
	}
}

func TestConnectorRecordsSubmitAnchorsForRemoteKeyAndLocalEnter(t *testing.T) {
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)
	c.BindHub(hub)

	c.mirror.WriteOutput([]byte("prompt"))
	c.deliverInputToHub(hub, protocol.ForwardInputKeyFrame("client-1", "ENTER"))
	c.mirror.WriteOutput([]byte("\r\nprompt2"))
	if err := hub.WriteInput([]byte("local\r")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}

	done := snapshotDoneFromAttachOpen(t, c)
	if len(done.SubmitAnchors) != 2 {
		t.Fatalf("anchor count = %d, want 2: %#v", len(done.SubmitAnchors), done.SubmitAnchors)
	}
}

func TestConnectorRecordsSubmitAnchorForTextContainingCarriageReturn(t *testing.T) {
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)
	c.BindHub(hub)

	c.mirror.WriteOutput([]byte("prompt"))
	c.deliverInputToHub(hub, protocol.ForwardInputTextFrame("client-1", "hello\r", false))

	done := snapshotDoneFromAttachOpen(t, c)
	if len(done.SubmitAnchors) != 1 {
		t.Fatalf("anchor count = %d, want 1: %#v", len(done.SubmitAnchors), done.SubmitAnchors)
	}
}

func TestConnectorRecordsOneSubmitAnchorPerCarriageReturn(t *testing.T) {
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)
	c.BindHub(hub)

	c.mirror.WriteOutput([]byte("prompt"))
	c.deliverInputToHub(hub, protocol.ForwardInputTextFrame("client-1", "cmd1\rcmd2\r", false))

	done := snapshotDoneFromAttachOpen(t, c)
	if len(done.SubmitAnchors) != 2 {
		t.Fatalf("anchor count = %d, want 2: %#v", len(done.SubmitAnchors), done.SubmitAnchors)
	}
}

func TestConnectorRemovesSubmitAnchorWhenInputWriteFails(t *testing.T) {
	writeErr := errors.New("write failed")
	hub := session.NewHub(func([]byte) error { return writeErr }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)
	c.BindHub(hub)

	c.mirror.WriteOutput([]byte("prompt"))
	if err := hub.WriteInput([]byte("local\r")); !errors.Is(err, writeErr) {
		t.Fatalf("WriteInput error = %v, want %v", err, writeErr)
	}

	done := snapshotDoneFromAttachOpen(t, c)
	if len(done.SubmitAnchors) != 0 {
		t.Fatalf("anchors = %#v, want none after failed input write", done.SubmitAnchors)
	}
}

func TestConnectorIgnoresCarriageReturnsInsideBracketedPaste(t *testing.T) {
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)
	c.BindHub(hub)

	c.mirror.WriteOutput([]byte("prompt"))
	if err := hub.WriteInput([]byte("\x1b[200~line1\rline2\r\x1b[201~")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}
	if err := hub.WriteInput([]byte("\r")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}

	done := snapshotDoneFromAttachOpen(t, c)
	if len(done.SubmitAnchors) != 1 {
		t.Fatalf("anchor count = %d, want only the post-paste Enter anchor: %#v", len(done.SubmitAnchors), done.SubmitAnchors)
	}
}

func TestConnectorTracksSplitBracketedPasteBoundaries(t *testing.T) {
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)
	c.BindHub(hub)

	c.mirror.WriteOutput([]byte("prompt"))
	if err := hub.WriteInput([]byte("\x1b[2")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}
	if err := hub.WriteInput([]byte("00~line1\rline2\r\x1b[2")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}
	if err := hub.WriteInput([]byte("01~\r")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}

	done := snapshotDoneFromAttachOpen(t, c)
	if len(done.SubmitAnchors) != 1 {
		t.Fatalf("anchor count = %d, want only the post-paste Enter anchor: %#v", len(done.SubmitAnchors), done.SubmitAnchors)
	}
}

func TestConnectorQueuedSubmitInputRecordsAnchorAfterHubBind(t *testing.T) {
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)

	c.mirror.WriteOutput([]byte("prompt"))
	c.routeInput(protocol.ForwardInputKeyFrame("client-1", "ENTER"))
	c.BindHub(hub)

	done := snapshotDoneFromAttachOpen(t, c)
	if len(done.SubmitAnchors) != 1 {
		t.Fatalf("anchor count = %d, want 1: %#v", len(done.SubmitAnchors), done.SubmitAnchors)
	}
}

func TestConnectorDoesNotRecordAnchorsForNonSubmitInput(t *testing.T) {
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)
	c.BindHub(hub)

	c.mirror.WriteOutput([]byte("prompt"))
	c.deliverInputToHub(hub, protocol.ForwardInputTextFrame("client-1", "draft", false))
	c.deliverInputToHub(hub, protocol.ForwardInputKeyFrame("client-1", "TAB"))
	if err := hub.WriteInput([]byte("local text")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}

	done := snapshotDoneFromAttachOpen(t, c)
	if len(done.SubmitAnchors) != 0 {
		t.Fatalf("anchors = %#v, want none", done.SubmitAnchors)
	}
}

func TestConnectorEmitsLiveSubmitAnchorForSuccessfulSubmitInput(t *testing.T) {
	tests := []struct {
		name string
		send func(*Connector, *session.Hub) error
	}{
		{
			name: "local terminal enter",
			send: func(_ *Connector, hub *session.Hub) error {
				return hub.WriteInput([]byte("local\r"))
			},
		},
		{
			name: "remote input_key enter",
			send: func(c *Connector, hub *session.Hub) error {
				c.deliverInputToHub(hub, protocol.ForwardInputKeyFrame("client-1", "ENTER"))
				return nil
			},
		},
		{
			name: "remote input_text submit",
			send: func(c *Connector, hub *session.Hub) error {
				c.deliverInputToHub(hub, protocol.ForwardInputTextFrame("client-1", "", true))
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
			c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
			c.applyResize(20, 4, false)
			c.BindHub(hub)
			c.attachMu.Lock()
			c.attached["client-1"] = struct{}{}
			c.attachMu.Unlock()
			c.mirror.WriteOutput([]byte("prompt"))

			if err := tc.send(c, hub); err != nil {
				t.Fatalf("send returned error: %v", err)
			}

			frame := readEphemeralSubmitAnchorFrame(t, c)
			if frame.ClientID != "client-1" {
				t.Fatalf("ClientID = %q, want client-1", frame.ClientID)
			}
			if frame.SubmitAnchor == nil {
				t.Fatalf("SubmitAnchor = nil, want live anchor")
			}
			if frame.SubmitAnchor.ID != "submit-1" || frame.SubmitAnchor.Line < 0 || frame.SubmitAnchor.SubmittedAt <= 0 {
				t.Fatalf("SubmitAnchor = %#v, want valid submit-1 anchor", frame.SubmitAnchor)
			}
		})
	}
}

func TestConnectorEmitsLiveSubmitAnchorToAllAttachedClients(t *testing.T) {
	hub := session.NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)
	c.BindHub(hub)
	c.attachMu.Lock()
	c.attached["client-1"] = struct{}{}
	c.attached["client-2"] = struct{}{}
	c.attachMu.Unlock()
	c.mirror.WriteOutput([]byte("prompt"))

	c.deliverInputToHub(hub, protocol.ForwardInputKeyFrame("client-1", "ENTER"))

	gotClients := map[string]bool{}
	var anchorID string
	for range 2 {
		frame := readEphemeralSubmitAnchorFrame(t, c)
		gotClients[frame.ClientID] = true
		if frame.SubmitAnchor == nil {
			t.Fatalf("SubmitAnchor = nil, want live anchor")
		}
		if anchorID == "" {
			anchorID = frame.SubmitAnchor.ID
		}
		if frame.SubmitAnchor.ID != anchorID {
			t.Fatalf("SubmitAnchor.ID = %q, want %q", frame.SubmitAnchor.ID, anchorID)
		}
	}
	if !gotClients["client-1"] || !gotClients["client-2"] {
		t.Fatalf("clients = %#v, want client-1 and client-2", gotClients)
	}
}

func TestConnectorDoesNotEmitLiveSubmitAnchorWhenInputWriteFails(t *testing.T) {
	writeErr := errors.New("write failed")
	hub := session.NewHub(func([]byte) error { return writeErr }, func(int, int) error { return nil })
	c := New("ws://unused", "token", protocol.SessionInfo{SessionID: "sess-1", Launcher: "codex"})
	c.applyResize(20, 4, false)
	c.BindHub(hub)
	c.attachMu.Lock()
	c.attached["client-1"] = struct{}{}
	c.attachMu.Unlock()
	c.mirror.WriteOutput([]byte("prompt"))

	if err := hub.WriteInput([]byte("local\r")); !errors.Is(err, writeErr) {
		t.Fatalf("WriteInput error = %v, want %v", err, writeErr)
	}
	select {
	case frame := <-c.ephemeral:
		t.Fatalf("unexpected live frame after failed write: %#v", frame)
	default:
	}

	done := snapshotDoneFromAttachOpen(t, c)
	if len(done.SubmitAnchors) != 0 {
		t.Fatalf("anchors = %#v, want none after failed input write", done.SubmitAnchors)
	}
}

func readEphemeralSubmitAnchorFrame(t *testing.T, c *Connector) protocol.AgentFrame {
	t.Helper()

	select {
	case outbound := <-c.ephemeral:
		frame, ok := outbound.json.(protocol.AgentFrame)
		if !ok {
			t.Fatalf("ephemeral json = %#v, want protocol.AgentFrame", outbound.json)
		}
		if frame.Type != "submit_anchor" {
			t.Fatalf("frame.Type = %q, want submit_anchor", frame.Type)
		}
		return frame
	default:
		t.Fatal("expected submit_anchor frame, got none")
		return protocol.AgentFrame{}
	}
}

func TestConnectorAttachOpenSendsSnapshotThenLiveBytes(t *testing.T) {
	const clientID = "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1"

	registerSeen := make(chan struct{}, 1)
	allowAttach := make(chan struct{})
	attachReadyCh := make(chan protocol.AgentFrame, 1)
	snapshotCh := make(chan protocol.AttachPacket, 1)
	snapshotDoneCh := make(chan protocol.AgentFrame, 1)
	livePacketCh := make(chan protocol.AttachPacket, 1)
	doneReading := make(chan struct{}, 1)

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
		registerSeen <- struct{}{}

		<-allowAttach
		if err := conn.WriteJSON(protocol.AttachOpenFrame(clientID)); err != nil {
			t.Fatalf("WriteJSON attach_open returned error: %v", err)
		}

		sawSnapshot := false
		sawSnapshotDone := false
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage returned error: %v", err)
			}

			switch messageType {
			case websocket.TextMessage:
				var frame protocol.AgentFrame
				if err := json.Unmarshal(payload, &frame); err != nil {
					t.Fatalf("Unmarshal text frame returned error: %v", err)
				}
				switch frame.Type {
				case "attach_ready":
					attachReadyCh <- frame
				case "snapshot_done":
					sawSnapshotDone = true
					snapshotDoneCh <- frame
				default:
					continue
				}
			case websocket.BinaryMessage:
				packet, err := protocol.DecodeAttachPacket(payload)
				if err != nil {
					t.Fatalf("DecodeAttachPacket returned error: %v", err)
				}
				if !sawSnapshot {
					sawSnapshot = true
					snapshotCh <- packet
				} else {
					if !sawSnapshotDone {
						t.Fatal("received live packet before snapshot_done")
					}
					livePacketCh <- packet
					if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
						t.Fatalf("SetReadDeadline returned error: %v", err)
					}
					for {
						messageType, payload, err := conn.ReadMessage()
						if err != nil {
							if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
								doneReading <- struct{}{}
								return
							}
							t.Fatalf("ReadMessage after live packet returned error: %v", err)
						}
						if messageType != websocket.BinaryMessage {
							continue
						}
						packet, err := protocol.DecodeAttachPacket(payload)
						if err != nil {
							t.Fatalf("DecodeAttachPacket after live packet returned error: %v", err)
						}
						if string(packet.Payload) == "live bytes" {
							t.Fatalf("received duplicate live packet for %q", clientID)
						}
					}
				}
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.applyResize(120, 3, false)
	c.mirror.WriteOutput([]byte("line001\r\nline002\r\nline003\r\nline004"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case <-registerSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for register")
	}

	close(allowAttach)

	var attachReady protocol.AgentFrame
	select {
	case attachReady = <-attachReadyCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for attach_ready")
	}
	if attachReady.ClientID != clientID || attachReady.Cols != 120 || attachReady.Rows != 3 {
		t.Fatalf("attach_ready = %#v, want client id %s size 120x3", attachReady, clientID)
	}

	var snapshot protocol.AttachPacket
	select {
	case snapshot = <-snapshotCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot packet")
	}
	if snapshot.ClientID != clientID {
		t.Fatalf("snapshot client_id = %q, want %q", snapshot.ClientID, clientID)
	}

	restored := xterm.New(xterm.WithCols(attachReady.Cols), xterm.WithRows(attachReady.Rows), xterm.WithScrollback(10000))
	_, _ = restored.Write(snapshot.Payload)
	for _, want := range []string{"line002", "line003", "line004"} {
		if !strings.Contains(restored.String(), want) {
			t.Fatalf("restored snapshot viewport = %q, want %q", restored.String(), want)
		}
	}
	if strings.Contains(restored.String(), "line001") {
		t.Fatalf("restored snapshot viewport = %q, did not expect scrollback-only line", restored.String())
	}
	if got := joinedBufferText(t, restored.NormalBuffer()); !strings.Contains(got, "line001") {
		t.Fatalf("restored normal buffer = %q, want scrollback line001", got)
	}

	if err := c.WriteOutput([]byte("live bytes")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}

	select {
	case frame := <-snapshotDoneCh:
		if frame.ClientID != clientID {
			t.Fatalf("snapshot_done = %#v, want client id %s", frame, clientID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot_done")
	}

	select {
	case packet := <-livePacketCh:
		if packet.ClientID != clientID {
			t.Fatalf("live packet client_id = %q, want %q", packet.ClientID, clientID)
		}
		if string(packet.Payload) != "live bytes" {
			t.Fatalf("live packet payload = %q, want %q", string(packet.Payload), "live bytes")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for live packet")
	}

	select {
	case <-doneReading:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for duplicate-live verification")
	}
}

func TestConnectorAttachOpenBypassesFullEphemeralQueue(t *testing.T) {
	const clientID = "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1"

	attachReadyCh := make(chan protocol.AgentFrame, 1)
	snapshotCh := make(chan protocol.AttachPacket, 1)
	snapshotDoneCh := make(chan protocol.AgentFrame, 1)

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}
		defer conn.Close()

		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage returned error: %v", err)
			}

			switch messageType {
			case websocket.TextMessage:
				var frame protocol.AgentFrame
				if err := json.Unmarshal(payload, &frame); err != nil {
					t.Fatalf("Unmarshal text frame returned error: %v", err)
				}
				switch frame.Type {
				case "attach_ready":
					attachReadyCh <- frame
				case "snapshot_done":
					snapshotDoneCh <- frame
					return
				default:
					t.Fatalf("frame.Type = %q, want attach_ready or snapshot_done before queued ephemerals", frame.Type)
				}
			case websocket.BinaryMessage:
				packet, err := protocol.DecodeAttachPacket(payload)
				if err != nil {
					t.Fatalf("DecodeAttachPacket returned error: %v", err)
				}
				snapshotCh <- packet
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.applyResize(120, 40, false)
	c.mirror.WriteOutput([]byte("snapshot line"))
	for i := 0; i < cap(c.ephemeral); i++ {
		c.ephemeral <- outboundFrame{json: protocol.ResizeFrame(120+i, 40)}
	}

	done := make(chan error, 1)
	go func() {
		done <- c.handleAttachOpen(conn, clientID)
	}()

	select {
	case frame := <-attachReadyCh:
		if frame.Type != "attach_ready" || frame.ClientID != clientID {
			t.Fatalf("attach_ready = %#v, want attach_ready for %s", frame, clientID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for attach_ready")
	}

	select {
	case packet := <-snapshotCh:
		if packet.ClientID != clientID {
			t.Fatalf("snapshot client_id = %q, want %q", packet.ClientID, clientID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot packet")
	}

	select {
	case frame := <-snapshotDoneCh:
		if frame.Type != "snapshot_done" || frame.ClientID != clientID {
			t.Fatalf("snapshot_done = %#v, want snapshot_done for %s", frame, clientID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot_done")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handleAttachOpen returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handleAttachOpen to finish")
	}
}

func TestConnectorAttachOpenWithEmptySnapshotSkipsBinarySnapshot(t *testing.T) {
	const clientID = "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1"

	attachReadyCh := make(chan protocol.AgentFrame, 1)
	snapshotDoneCh := make(chan protocol.AgentFrame, 1)
	unexpectedBinaryCh := make(chan protocol.AttachPacket, 1)

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

		if err := conn.WriteJSON(protocol.AttachOpenFrame(clientID)); err != nil {
			t.Fatalf("WriteJSON attach_open returned error: %v", err)
		}

		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage returned error: %v", err)
			}

			switch messageType {
			case websocket.TextMessage:
				var frame protocol.AgentFrame
				if err := json.Unmarshal(payload, &frame); err != nil {
					t.Fatalf("Unmarshal returned error: %v", err)
				}
				switch frame.Type {
				case "attach_ready":
					attachReadyCh <- frame
				case "snapshot_done":
					snapshotDoneCh <- frame
					return
				}
			case websocket.BinaryMessage:
				packet, err := protocol.DecodeAttachPacket(payload)
				if err != nil {
					t.Fatalf("DecodeAttachPacket returned error: %v", err)
				}
				unexpectedBinaryCh <- packet
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	c := New(wsURL, "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.applyResize(120, 40, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case frame := <-attachReadyCh:
		if frame.Type != "attach_ready" || frame.ClientID != clientID || frame.Cols != 120 || frame.Rows != 40 {
			t.Fatalf("attach_ready = %#v, want attach_ready %s 120x40", frame, clientID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for attach_ready")
	}

	select {
	case packet := <-unexpectedBinaryCh:
		t.Fatalf("packet = %#v, want no binary snapshot for empty screen", packet)
	case frame := <-snapshotDoneCh:
		if frame.Type != "snapshot_done" || frame.ClientID != clientID {
			t.Fatalf("snapshot_done = %#v, want snapshot_done for %s", frame, clientID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot_done")
	}
}

func TestConnectorSubmitInputWritesSeparateDelayedChunks(t *testing.T) {
	type inputEvent struct {
		data string
		at   time.Time
	}

	events := make([]inputEvent, 0, 2)
	hub := session.NewHub(func(data []byte) error {
		events = append(events, inputEvent{
			data: string(data),
			at:   time.Now(),
		})
		return nil
	}, func(int, int) error { return nil })

	c := New("", "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})

	c.deliverInputToHub(hub, protocol.ForwardInputTextFrame("", "hello", true))

	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].data != "hello" {
		t.Fatalf("first event = %q, want hello", events[0].data)
	}
	if events[1].data != "\r" {
		t.Fatalf("second event = %q, want \\r", events[1].data)
	}

	minGap := defaultSubmitEnterGap - 20*time.Millisecond
	if gap := events[1].at.Sub(events[0].at); gap < minGap {
		t.Fatalf("submit gap = %v, want at least %v", gap, minGap)
	}
}

func TestConnectorReconnectDoesNotReplayOldOutput(t *testing.T) {
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

	received := make(chan protocol.AgentFrame, 1)
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
		if register.Session == nil || register.Session.SessionID != "sess-1" {
			t.Fatalf("register session = %#v, want sess-1", register.Session)
		}

		_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		var frame protocol.AgentFrame
		if err := conn.ReadJSON(&frame); err != nil {
			if !strings.Contains(err.Error(), "i/o timeout") {
				t.Fatalf("ReadJSON second frame returned error: %v", err)
			}
			received <- protocol.AgentFrame{Type: "none"}
			return
		}
		received <- frame
	}))
	defer secondServer.Close()

	c.url = "ws" + strings.TrimPrefix(secondServer.URL, "http")

	done := make(chan error, 1)
	go func() {
		_, err := c.runOnce(ctx, 0)
		done <- err
	}()

	select {
	case frame := <-received:
		if frame.Type != "none" {
			t.Fatalf("frame = %#v, want no replay after reconnect", frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnect check")
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

		if err := conn.WriteJSON(protocol.ForwardInputTextFrame("", "queued", false)); err != nil {
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

func TestConnectorQueueOverflowClosesActiveConnection(t *testing.T) {
	c := New("ws://relay.test", "token", protocol.SessionInfo{
		SessionID: "sess-1",
		Launcher:  "codex",
	})
	c.ephemeral = make(chan outboundFrame, 1)
	c.ephemeral <- outboundFrame{json: protocol.ResizeFrame(120, 40)}

	overflow := make(chan error, 1)
	closer := &recordingCloser{}
	c.setActiveConnection(closer, overflow)
	defer c.clearActiveConnection()

	c.enqueueEphemeralBinary([]byte("overflow"))

	select {
	case err := <-overflow:
		if !errors.Is(err, errOutboundBackpressure) {
			t.Fatalf("overflow err = %v, want errOutboundBackpressure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for overflow signal")
	}

	if closer.closed.Load() != 1 {
		t.Fatalf("closed = %d, want 1", closer.closed.Load())
	}
}

func TestDeliverReadResultStopsWhenDoneClosed(t *testing.T) {
	done := make(chan struct{})
	incoming := make(chan readResult, 1)
	incoming <- readResult{}
	close(done)

	if ok := deliverReadResult(done, incoming, readResult{err: errors.New("closed")}); ok {
		t.Fatal("deliverReadResult returned true, want false")
	}
}

func TestConnectorAttachCloseStopsLiveDelivery(t *testing.T) {
	const clientID = "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1"

	attachClosed := make(chan struct{}, 1)
	livePacketCh := make(chan protocol.AttachPacket, 1)
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

		if err := conn.WriteJSON(protocol.AttachOpenFrame(clientID)); err != nil {
			t.Fatalf("WriteJSON attach_open returned error: %v", err)
		}

		deadline := time.Now().Add(2 * time.Second)
		sawSnapshotDone := false
		for !sawSnapshotDone {
			_ = conn.SetReadDeadline(deadline)
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage returned error: %v", err)
			}
			if messageType != websocket.TextMessage {
				continue
			}
			var frame protocol.AgentFrame
			if err := json.Unmarshal(payload, &frame); err != nil {
				t.Fatalf("Unmarshal returned error: %v", err)
			}
			if frame.Type == "snapshot_done" {
				sawSnapshotDone = true
			}
		}

		if err := conn.WriteJSON(protocol.AttachCloseFrame(clientID, "client_closed")); err != nil {
			t.Fatalf("WriteJSON attach_close returned error: %v", err)
		}
		attachClosed <- struct{}{}

		_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		messageType, payload, err := conn.ReadMessage()
		if err == nil && messageType == websocket.BinaryMessage {
			packet, err := protocol.DecodeAttachPacket(payload)
			if err != nil {
				t.Fatalf("DecodeAttachPacket returned error: %v", err)
			}
			livePacketCh <- packet
		}
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

	select {
	case <-attachClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for attach_close")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.attachMu.Lock()
		_, stillAttached := c.attached[clientID]
		c.attachMu.Unlock()
		if !stillAttached {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for attach_close to update connector state")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := c.WriteOutput([]byte("hello")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}

	select {
	case packet := <-livePacketCh:
		t.Fatalf("packet = %#v, want no live packet after attach_close", packet)
	case <-time.After(500 * time.Millisecond):
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
