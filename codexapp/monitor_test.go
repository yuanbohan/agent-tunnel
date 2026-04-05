package codexapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
)

func TestPollWaitingStateReturnsTrueForWaitingThread(t *testing.T) {
	server := newFakeAppServer(t, func(method string, _ int64) any {
		if method == "thread/list" {
			return map[string]any{
				"data": []map[string]any{
					{
						"status": map[string]any{
							"type":        "active",
							"activeFlags": []string{"waitingOnUserInput"},
						},
					},
				},
			}
		}
		return map[string]any{}
	})
	defer server.Close()

	waiting, err := pollWaitingState(context.Background(), wsURL(server.URL))
	if err != nil {
		t.Fatalf("pollWaitingState returned error: %v", err)
	}
	if !waiting {
		t.Fatal("waiting = false, want true")
	}
}

func TestMonitorActionRequiredEmitsEnterAndExit(t *testing.T) {
	var listCalls atomic.Int32
	server := newFakeAppServer(t, func(method string, _ int64) any {
		if method != "thread/list" {
			return map[string]any{}
		}
		call := listCalls.Add(1)
		if call == 1 {
			return map[string]any{
				"data": []map[string]any{
					{
						"status": map[string]any{
							"type":        "active",
							"activeFlags": []string{"waitingOnApproval"},
						},
					},
				},
			}
		}
		return map[string]any{
			"data": []map[string]any{
				{
					"status": map[string]any{
						"type": "idle",
					},
				},
			},
		}
	})
	defer server.Close()

	oldInterval := monitorPollInterval
	monitorPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { monitorPollInterval = oldInterval })

	reporter := &recordingStateReporter{events: make(chan protocol.SessionStateEvent, 2)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go MonitorActionRequired(ctx, wsURL(server.URL), reporter)

	enter := reporter.waitForEvent(t)
	exit := reporter.waitForEvent(t)

	if enter.State != protocol.SessionStateActionRequired {
		t.Fatalf("enter state = %q, want %q", enter.State, protocol.SessionStateActionRequired)
	}
	if enter.ActionRequiredSince == nil {
		t.Fatal("enter ActionRequiredSince = nil, want timestamp")
	}
	if exit.State != protocol.SessionStateNormal {
		t.Fatalf("exit state = %q, want %q", exit.State, protocol.SessionStateNormal)
	}
	if exit.ActionRequiredSince != nil {
		t.Fatalf("exit ActionRequiredSince = %v, want nil", exit.ActionRequiredSince)
	}
}

type recordingStateReporter struct {
	mu     sync.Mutex
	events chan protocol.SessionStateEvent
}

func (r *recordingStateReporter) UpdateSessionState(state protocol.SessionState, changedAt time.Time, actionRequiredSince *time.Time) {
	event := protocol.SessionStateEvent{
		SessionID:           "sess-1",
		State:               state,
		ChangedAt:           changedAt,
		ActionRequiredSince: actionRequiredSince,
	}
	r.events <- event
}

func (r *recordingStateReporter) waitForEvent(t *testing.T) protocol.SessionStateEvent {
	t.Helper()
	select {
	case event := <-r.events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for state event")
		return protocol.SessionStateEvent{}
	}
}

func newFakeAppServer(t *testing.T, handler func(method string, id int64) any) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade returned error: %v", err)
		}
		defer conn.Close()

		for {
			var request map[string]any
			if err := conn.ReadJSON(&request); err != nil {
				return
			}
			method, _ := request["method"].(string)
			id, _ := request["id"].(float64)
			response := map[string]any{
				"jsonrpc": "2.0",
				"id":      int64(id),
				"result":  handler(method, int64(id)),
			}
			if err := conn.WriteJSON(response); err != nil {
				return
			}
		}
	}))
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
