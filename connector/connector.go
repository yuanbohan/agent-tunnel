package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/session"
)

type Connector struct {
	url   string
	token string
	info  protocol.SessionInfo
	hub   *session.Hub

	outbound chan protocol.Message
	dialer   *websocket.Dialer
}

type readResult struct {
	msg protocol.Message
	err error
}

func New(url, token string, info protocol.SessionInfo) *Connector {
	return &Connector{
		url:      url,
		token:    token,
		info:     info,
		outbound: make(chan protocol.Message, 128),
		dialer:   websocket.DefaultDialer,
	}
}

func (c *Connector) BindHub(hub *session.Hub) {
	c.hub = hub
	hub.OnResize(func(cols, rows int) {
		msg := protocol.Message{Type: "resize", Cols: cols, Rows: rows}
		select {
		case c.outbound <- msg:
		default:
		}
	})
}

func (c *Connector) WriteOutput(data []byte) error {
	msg := protocol.EncodeOutput(append([]byte(nil), data...))
	select {
	case c.outbound <- msg:
	default:
	}
	return nil
}

func (c *Connector) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.runOnce(ctx); err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

func (c *Connector) runOnce(ctx context.Context) error {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.token)

	conn, _, err := c.dialer.DialContext(ctx, c.url+"/agent/ws", headers)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := conn.WriteJSON(protocol.RegisterFrame(c.info)); err != nil {
		return err
	}

	incoming := make(chan readResult, 1)
	go c.readLoop(conn, incoming)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result, ok := <-incoming:
			if !ok {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			c.handleMessage(result.msg)
		case msg := <-c.outbound:
			if err := conn.WriteJSON(msg); err != nil {
				return err
			}
		}
	}
}

func (c *Connector) readLoop(conn *websocket.Conn, incoming chan<- readResult) {
	defer close(incoming)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			incoming <- readResult{err: err}
			return
		}

		var msg protocol.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		incoming <- readResult{msg: msg}
	}
}

func (c *Connector) handleMessage(msg protocol.Message) {
	if msg.Type == "input" {
		data, err := protocol.DecodeData(msg)
		if err == nil && c.hub != nil {
			_ = c.hub.WriteInput(data)
		}
	}
}
