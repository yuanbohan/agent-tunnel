package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
)

var monitorPollInterval = 500 * time.Millisecond

// sessionStateReporter is intentionally tiny so the monitor does not depend on
// relay internals. In production, connector.Connector implements this interface
// and forwards state changes to the relay as websocket frames.
type sessionStateReporter interface {
	UpdateSessionState(state protocol.SessionState, changedAt time.Time, actionRequiredSince *time.Time)
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcEnvelope struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type threadListResponse struct {
	Data []threadInfo `json:"data"`
}

type threadInfo struct {
	Status threadStatus `json:"status"`
}

type threadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

// MonitorActionRequired polls the local Codex app-server and converts Codex
// thread lifecycle into the relay's simpler session lifecycle:
//
//	waitingOnApproval / waitingOnUserInput -> action_required
//	anything else                          -> normal
//
// Data flow:
//
//	app-server websocket -> rpcClient -> waiting bool -> reporter
//	-> connector outbound queue -> relay `/agent/ws`
func MonitorActionRequired(ctx context.Context, wsURL string, reporter sessionStateReporter) {
	if reporter == nil || wsURL == "" {
		return
	}

	// These local fields deduplicate reports so the relay only sees transitions,
	// not a new `session_state` message on every poll tick.
	currentState := protocol.SessionStateNormal
	var actionRequiredSince *time.Time

	for {
		waiting, err := pollWaitingState(ctx, wsURL)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}

		now := time.Now().UTC()
		switch {
		case waiting && currentState != protocol.SessionStateActionRequired:
			// Preserve the first timestamp of the unresolved waiting episode so
			// downstream clients can reason about "how long have we been blocked?"
			currentState = protocol.SessionStateActionRequired
			actionRequiredSince = &now
			reporter.UpdateSessionState(currentState, now, actionRequiredSince)
		case !waiting && currentState != protocol.SessionStateNormal:
			// Returning to normal clears the waiting-episode anchor.
			currentState = protocol.SessionStateNormal
			actionRequiredSince = nil
			reporter.UpdateSessionState(currentState, now, nil)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(monitorPollInterval):
		}
	}
}

// pollWaitingState opens a short-lived websocket session to the local app-server
// and asks for the current thread list. This function is deliberately stateless:
// each poll round derives the answer from the latest app-server truth instead of
// trying to maintain an incremental cache inside agentunnel.
func pollWaitingState(ctx context.Context, wsURL string) (bool, error) {
	dialer := websocket.Dialer{}
	conn, _, err := dialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return false, err
	}
	defer conn.Close()

	client := &rpcClient{conn: conn}
	if err := client.initialize(ctx); err != nil {
		return false, err
	}

	threads, err := client.listThreads(ctx)
	if err != nil {
		return false, err
	}
	for _, thread := range threads {
		if thread.statusIsWaiting() {
			return true, nil
		}
	}
	return false, nil
}

type rpcClient struct {
	conn   *websocket.Conn
	nextID int64
}

// initialize performs the minimum handshake required before Codex accepts
// subsequent JSON-RPC calls on the app-server websocket.
func (c *rpcClient) initialize(ctx context.Context) error {
	var result json.RawMessage
	return c.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "agentunnel",
			"version": "dev",
		},
	}, &result)
}

// listThreads is the only app-server API this monitor currently needs. The
// monitor intentionally maps rich thread data down to a single waiting/not-waiting
// bit, because the relay only exposes session-level state today.
func (c *rpcClient) listThreads(ctx context.Context) ([]threadInfo, error) {
	var response threadListResponse
	if err := c.call(ctx, "thread/list", map[string]any{}, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// call is a minimal JSON-RPC helper. It ignores unrelated server notifications
// and waits only for the response that matches the request id we just sent.
func (c *rpcClient) call(ctx context.Context, method string, params any, result any) error {
	c.nextID++
	request := rpcRequest{
		JSONRPC: "2.0",
		ID:      c.nextID,
		Method:  method,
		Params:  params,
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
		_ = c.conn.SetReadDeadline(deadline)
	}
	if err := c.conn.WriteJSON(request); err != nil {
		return err
	}

	for {
		var envelope rpcEnvelope
		if err := c.conn.ReadJSON(&envelope); err != nil {
			return err
		}
		if envelope.Method != "" {
			continue
		}
		if envelope.ID != request.ID {
			continue
		}
		if envelope.Error != nil {
			return fmt.Errorf("app-server %s failed: %s", method, envelope.Error.Message)
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(envelope.Result, result)
	}
}

// statusIsWaiting encodes the current contract between Codex and the relay.
// If Codex later exposes more structured waiting states, this function is the
// place where the translation into relay session state should evolve.
func (t threadInfo) statusIsWaiting() bool {
	if t.Status.Type != "active" {
		return false
	}
	for _, flag := range t.Status.ActiveFlags {
		if flag == "waitingOnApproval" || flag == "waitingOnUserInput" {
			return true
		}
	}
	return false
}
