package relayclient

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/relayapi"
	"yuanbohan/tunnel/internal/session"
)

type Connector struct {
	cfg  Config
	info relayapi.SessionInfo
	hub  *session.Hub

	outbound chan []byte
	dialer   *websocket.Dialer
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

	errCh := make(chan error, 2)
	go func() { errCh <- c.readLoop(conn) }()
	go func() { errCh <- c.writeLoop(ctx, conn) }()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (c *Connector) readLoop(conn *websocket.Conn) error {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		var msg protocol.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

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
}

func (c *Connector) writeLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk := <-c.outbound:
			if err := conn.WriteJSON(protocol.EncodeOutput(chunk)); err != nil {
				return err
			}
		}
	}
}
