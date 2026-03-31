package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Handler is an http.HandlerFunc that upgrades to WebSocket and runs a PTY session.
func Handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade: %v", err)
		return
	}

	ptmx, cmd, err := SpawnShell()
	if err != nil {
		log.Printf("spawn shell: %v", err)
		conn.Close()
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			ptmx.Close() // unblocks ptmx.Read() in write loop
			conn.Close() // unblocks conn.ReadMessage() in read loop
		})
	}

	defer func() {
		stop()
		cmd.Process.Kill()
		cmd.Wait()
	}()

	var wg sync.WaitGroup
	wg.Add(2)

	// Write loop: PTY → WebSocket
	go func() {
		defer wg.Done()
		defer stop()
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				return
			}
			msg := protocol.EncodeOutput(buf[:n])
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
	}()

	// Read loop: WebSocket → PTY
	go func() {
		defer wg.Done()
		defer stop()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg protocol.Message
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "input":
				data, err := base64.StdEncoding.DecodeString(msg.Data)
				if err != nil {
					continue
				}
				ptmx.Write(data)
			case "resize":
				if msg.Cols > 0 && msg.Rows > 0 {
					ResizePTY(ptmx, msg.Cols, msg.Rows)
				}
			}
		}
	}()

	wg.Wait()
}
