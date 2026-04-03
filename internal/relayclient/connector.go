package relayclient

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/internal/relayapi"
	"yuanbohan/tunnel/session"
)

type Connector struct {
	cfg  Config
	info relayapi.SessionInfo
	hub  *session.Hub

	outbound chan []byte
	dialer   *websocket.Dialer
}

type readResult struct {
	msg protocol.Message
	err error
}

func New(cfg Config, info relayapi.SessionInfo) *Connector {
	return &Connector{
		cfg:      cfg,
		info:     info,
		outbound: make(chan []byte, 128),
		dialer:   websocket.DefaultDialer,
	}
}

func (c *Connector) BindHub(hub *session.Hub) {
	c.hub = hub
}

func (c *Connector) WriteOutput(data []byte) error {
	cp := append([]byte(nil), data...)
	select {
	case c.outbound <- cp:
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
	headers.Set("Authorization", "Bearer "+c.cfg.Token)

	conn, _, err := c.dialer.DialContext(ctx, c.cfg.URL+"/agent/ws", headers)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := conn.WriteJSON(relayapi.RegisterFrame(c.info)); err != nil {
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
		case chunk := <-c.outbound:
			if err := conn.WriteJSON(protocol.EncodeOutput(chunk)); err != nil {
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
	switch msg.Type {
	case "input":
		data, err := protocol.DecodeData(msg)
		if err == nil && c.hub != nil {
			_ = c.hub.WriteInput(data)
		}
	case "resize":
		if c.hub != nil {
			_ = c.hub.Resize(msg.Cols, msg.Rows)
		}
	}
}
