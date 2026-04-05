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

func MonitorActionRequired(ctx context.Context, wsURL string, reporter sessionStateReporter) {
	if reporter == nil || wsURL == "" {
		return
	}

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
			currentState = protocol.SessionStateActionRequired
			actionRequiredSince = &now
			reporter.UpdateSessionState(currentState, now, actionRequiredSince)
		case !waiting && currentState != protocol.SessionStateNormal:
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

func (c *rpcClient) initialize(ctx context.Context) error {
	var result json.RawMessage
	return c.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "agentunnel",
			"version": "dev",
		},
	}, &result)
}

func (c *rpcClient) listThreads(ctx context.Context) ([]threadInfo, error) {
	var response threadListResponse
	if err := c.call(ctx, "thread/list", map[string]any{}, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

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
