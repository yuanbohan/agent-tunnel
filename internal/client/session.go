package client

import (
	"context"
	"encoding/base64"
	"os"
	"sync"

	"github.com/gorilla/websocket"
	"golang.org/x/term"
	"yuanbohan/tunnel/internal/protocol"
)

// Connect dials the agent WebSocket and proxies stdin/stdout until the
// context is cancelled or either side closes.
func Connect(ctx context.Context, url string) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			conn.Close() // unblocks conn.ReadJSON() in output loop
		})
	}

	defer stop()

	// Send initial terminal size.
	if cols, rows, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
		conn.WriteJSON(protocol.Message{Type: "resize", Cols: cols, Rows: rows})
	}

	// Read stdin in a dedicated goroutine and fan out via channel so the
	// stdin loop below can also select on ctx.Done().
	type stdinMsg struct {
		data []byte
		err  error
	}
	stdinCh := make(chan stdinMsg, 1)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				stdinCh <- stdinMsg{data: cp}
			}
			if err != nil {
				stdinCh <- stdinMsg{err: err}
				return
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)

	// Stdin loop: stdin → WebSocket input messages
	go func() {
		defer wg.Done()
		defer stop()
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-stdinCh:
				if m.err != nil {
					return
				}
				encoded := base64.StdEncoding.EncodeToString(m.data)
				if err := conn.WriteJSON(protocol.Message{Type: "input", Data: encoded}); err != nil {
					return
				}
			}
		}
	}()

	// Output loop: WebSocket output messages → stdout
	go func() {
		defer wg.Done()
		defer stop()
		for {
			var msg protocol.Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type == "output" {
				data, err := protocol.DecodeData(msg)
				if err != nil {
					continue
				}
				os.Stdout.Write(data)
			}
		}
	}()

	wg.Wait()
	return nil
}
