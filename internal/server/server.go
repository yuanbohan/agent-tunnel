package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/session"
	"yuanbohan/tunnel/internal/webui"
)

type LiveSession interface {
	AddSink(string, session.OutputSink)
	RemoveSink(string)
	WriteInput([]byte) error
	Resize(int, int) error
}

type Running struct {
	URL      string
	server   *http.Server
	listener net.Listener
}

type wsSink struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *wsSink) WriteOutput(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(protocol.EncodeOutput(data))
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

var nextSinkID uint64

func NewHandler(sess LiveSession) http.Handler {
	fileServer := http.FileServer(http.FS(webui.Files()))
	mux := http.NewServeMux()
	mux.Handle("/", fileServer)
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		sinkID := fmt.Sprintf("ws-%d", atomic.AddUint64(&nextSinkID, 1))
		sess.AddSink(sinkID, &wsSink{conn: conn})
		defer sess.RemoveSink(sinkID)

		for {
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
				_ = sess.WriteInput(data)
			case "resize":
				_ = sess.Resize(msg.Cols, msg.Rows)
			}
		}
	})
	return mux
}

func StartLocal(sess LiveSession) (*Running, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	srv := &http.Server{
		Handler: NewHandler(sess),
	}
	go func() {
		_ = srv.Serve(listener)
	}()

	return &Running{
		URL:      "http://" + listener.Addr().String(),
		server:   srv,
		listener: listener,
	}, nil
}

func (r *Running) Close(ctx context.Context) error {
	return r.server.Shutdown(ctx)
}
